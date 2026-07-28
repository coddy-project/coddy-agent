package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/web"
)

// sourceMu serializes mutations of skills.sources (add/remove) so concurrent
// /plugin commands cannot corrupt the slice or race on the config.yaml write.
var sourceMu sync.Mutex

// syncMu serializes materialization into the managed skills dir so concurrent
// Sync/UpdateSkill calls cannot race on the shared staging directories.
var syncMu sync.Mutex

// safeClone applies the SSRF guard to http(s) clone URLs (blocking loopback /
// private hosts reachable over http(s), including those coming from a
// marketplace manifest) before cloning. Operator-chosen local/SSH transports
// (file://, git@host:path) are cloned as-is — they are not network SSRF vectors
// and file:// backs offline marketplaces and the tests.
func safeClone(url, ref, dest string) error {
	low := strings.ToLower(strings.TrimSpace(url))
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") {
		if _, err := web.ValidateFetchURL(context.Background(), url); err != nil {
			return fmt.Errorf("clone url not allowed: %w", err)
		}
	}
	return gitws.Clone(url, ref, dest)
}

// remoteLockFile is the provenance sidecar written into the managed skills dir.
const remoteLockFile = ".remote.json"

// maxManifestBytes caps an API marketplace.json download.
const maxManifestBytes = 4 << 20

// maxWalkDepth bounds the recursive SKILL.md search inside a clone.
const maxWalkDepth = 6

// RemoteEntry records where an installed skill came from (one per skill dir).
type RemoteEntry struct {
	Source  string `json:"source"`            // the configured source string
	Repo    string `json:"repo,omitempty"`    // git URL the skill was cloned from
	Ref     string `json:"ref,omitempty"`     // branch or tag
	URL     string `json:"url,omitempty"`     // API marketplace URL, when applicable
	Plugin  string `json:"plugin,omitempty"`  // marketplace plugin entry name (for update lookup)
	Version string `json:"version,omitempty"` // installed version, as declared at sync time
}

// SyncResult summarizes a Sync run.
type SyncResult struct {
	Added   []string      `json:"added"`
	Updated []string      `json:"updated"`
	Failed  []SyncFailure `json:"failed"`
}

// SyncFailure is one source that could not be processed.
type SyncFailure struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

// sourceSpec is a classified top-level config source.
type sourceSpec struct {
	kind string // "git" | "api"
	url  string // git clone URL or API URL
	ref  string // branch/tag for git
}

// parseSource classifies a configured skills.sources entry.
//
//	owner/repo             → git https://github.com/owner/repo
//	owner/repo@ref         → git, ref
//	https://github.com/owner/repo(.git) → git
//	git@host:path / *.git  → git
//	https://host/marketplace.json (or any other http[s]) → api
func parseSource(raw string) (sourceSpec, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return sourceSpec{}, fmt.Errorf("empty source")
	}

	// scp-style git URL: git@host:owner/repo(.git)
	if strings.HasPrefix(s, "git@") {
		return sourceSpec{kind: "git", url: s}, nil
	}

	if strings.Contains(s, "://") {
		low := strings.ToLower(s)
		if strings.HasPrefix(low, "file://") || isGitCloneURL(low) {
			return sourceSpec{kind: "git", url: s}, nil
		}
		return sourceSpec{kind: "api", url: s}, nil
	}

	// No scheme: treat as owner/repo[@ref] GitHub shorthand.
	ref := ""
	repo := s
	if at := strings.LastIndex(s, "@"); at >= 0 {
		repo = s[:at]
		ref = s[at+1:]
	}
	repo = strings.TrimSuffix(repo, "/")
	if strings.Count(repo, "/") != 1 || strings.HasPrefix(repo, "/") || strings.HasSuffix(repo, "/") {
		return sourceSpec{}, fmt.Errorf("unrecognized source %q (expected owner/repo, a git URL, or an http(s) marketplace URL)", raw)
	}
	return sourceSpec{kind: "git", url: "https://github.com/" + repo, ref: ref}, nil
}

// isGitCloneURL reports whether a scheme'd URL should be cloned rather than
// fetched as an API marketplace. github.com/owner/repo and *.git are git.
func isGitCloneURL(lowerURL string) bool {
	if strings.HasSuffix(lowerURL, ".git") {
		return true
	}
	if strings.HasSuffix(lowerURL, ".json") {
		return false
	}
	for _, host := range []string{"github.com/", "gitlab.com/", "bitbucket.org/"} {
		if i := strings.Index(lowerURL, host); i >= 0 {
			rest := strings.Trim(lowerURL[i+len(host):], "/")
			if rest != "" && strings.Count(rest, "/") == 1 {
				return true
			}
		}
	}
	return false
}

// Sync fetches every configured source and materializes skills into the
// managed dir. It never runs automatically; callers invoke it explicitly.
func Sync(ctx context.Context, cfg *config.Config) (*SyncResult, error) {
	syncMu.Lock()
	defer syncMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		return nil, fmt.Errorf("create managed dir: %w", err)
	}
	lock := readRemoteLock(managedDir)
	res := &SyncResult{}

	for _, src := range cfg.Skills.Sources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		if err := syncOne(ctx, src, managedDir, lock, res); err != nil {
			res.Failed = append(res.Failed, SyncFailure{Source: src, Error: err.Error()})
		}
	}

	if err := writeRemoteLock(managedDir, lock); err != nil {
		return res, fmt.Errorf("write lock: %w", err)
	}
	return res, nil
}

// SyncSource fetches a single source (a GitHub owner/repo, git URL, or
// marketplace.json URL) and materializes its skills, independent of whether the
// source is listed in skills.sources. Backs `plugin marketplace sync <src>`.
func SyncSource(ctx context.Context, cfg *config.Config, source string) (*SyncResult, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("empty source")
	}
	if _, err := parseSource(source); err != nil {
		return nil, err
	}
	syncMu.Lock()
	defer syncMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		return nil, fmt.Errorf("create managed dir: %w", err)
	}
	lock := readRemoteLock(managedDir)
	res := &SyncResult{}
	if err := syncOne(ctx, source, managedDir, lock, res); err != nil {
		res.Failed = append(res.Failed, SyncFailure{Source: source, Error: err.Error()})
	}
	if err := writeRemoteLock(managedDir, lock); err != nil {
		return res, fmt.Errorf("write lock: %w", err)
	}
	return res, nil
}

// SkillReadonly reports whether a loaded skill cannot be deleted from disk
// (bundled skills carry a relative virtual FilePath; everything the loader found
// on disk is absolute and therefore removable).
func SkillReadonly(sk *Skill) bool {
	return sk == nil || !filepath.IsAbs(sk.FilePath)
}

// DeleteSkill removes any on-disk skill by canonical name (not just remote ones),
// with its remote lock entry when present. Bundled skills are read-only and
// cannot be deleted. cwd expands ${CWD} in skills.dirs for lookup.
func DeleteSkill(cfg *config.Config, cwd, skillName string) error {
	name, err := sanitizeSkillName(skillName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	syncMu.Lock()
	defer syncMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)

	loader := NewLoader(cfg.Skills.Dirs)
	loaded, err := loader.LoadAll(cwd, cfg.Paths.Home, managedDir)
	if err != nil {
		return err
	}
	var target *Skill
	for _, sk := range loaded {
		if CanonicalCommandName(sk) == name {
			target = sk
			break
		}
	}
	if target == nil {
		return fmt.Errorf("skill %q not found", name)
	}
	if SkillReadonly(target) {
		return fmt.Errorf("skill %q is read-only (bundled) and cannot be deleted", name)
	}
	victim, err := skillDeletePath(cfg, cwd, target.FilePath)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(victim); err != nil {
		return fmt.Errorf("remove skill: %w", err)
	}
	if lock := readRemoteLock(managedDir); lock != nil {
		if _, ok := lock[name]; ok {
			delete(lock, name)
			_ = writeRemoteLock(managedDir, lock)
		}
	}
	return nil
}

// skillDeletePath resolves what to remove for a skill file: its containing
// directory for a `<dir>/SKILL.md`, or the file itself for a root `.md`/`.mdc`.
// It refuses paths that are not strictly inside a configured skills directory.
func skillDeletePath(cfg *config.Config, cwd, filePath string) (string, error) {
	base := filepath.Base(filePath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	victim := filePath
	if strings.EqualFold(stem, "SKILL") {
		victim = filepath.Dir(filePath)
	}
	victim = filepath.Clean(victim)
	for _, d := range cfg.Skills.Dirs {
		root := filepath.Clean(ExpandConfiguredPath(d, cwd, cfg.Paths.Home))
		if victim == root {
			return "", fmt.Errorf("refusing to delete the skills directory itself")
		}
		if strings.HasPrefix(victim, root+string(filepath.Separator)) {
			return victim, nil
		}
	}
	return "", fmt.Errorf("refusing to delete skill outside configured skill directories")
}

func syncOne(ctx context.Context, src, managedDir string, lock map[string]RemoteEntry, res *SyncResult) error {
	spec, err := parseSource(src)
	if err != nil {
		return err
	}

	switch spec.kind {
	case "api":
		mf, err := fetchManifestHTTP(ctx, spec.url)
		if err != nil {
			return err
		}
		return installMarketplace(mf, "", src, RemoteEntry{Source: src, URL: spec.url}, managedDir, lock, res)

	case "git":
		tmp, err := os.MkdirTemp("", "coddy-skillsrc-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		clone := filepath.Join(tmp, "repo")
		if err := safeClone(spec.url, spec.ref, clone); err != nil {
			return fmt.Errorf("clone %s: %w", spec.url, err)
		}
		base := RemoteEntry{Source: src, Repo: spec.url, Ref: spec.ref}
		if mfPath := findMarketplaceFile(clone); mfPath != "" {
			mf, err := parseMarketplace(mfPath)
			if err != nil {
				return fmt.Errorf("parse manifest: %w", err)
			}
			return installMarketplace(mf, clone, src, base, managedDir, lock, res)
		}
		// No manifest: treat the whole clone as a skill container.
		return installFromDir(clone, base, managedDir, lock, res)

	default:
		return fmt.Errorf("unsupported source kind %q", spec.kind)
	}
}

// installMarketplace resolves every plugin in a manifest and installs its skills.
// repoRoot is the marketplace clone (for relative path sources); "" for API manifests.
func installMarketplace(mf *Marketplace, repoRoot, src string, base RemoteEntry, managedDir string, lock map[string]RemoteEntry, res *SyncResult) error {
	var firstErr error
	for _, p := range mf.Plugins {
		if err := installPlugin(p, repoRoot, src, base, managedDir, lock, res); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func installPlugin(p MarketplacePlugin, repoRoot, src string, base RemoteEntry, managedDir string, lock map[string]RemoteEntry, res *SyncResult) error {
	entry := base
	entry.Plugin = strings.TrimSpace(p.Name)
	entry.Version = strings.TrimSpace(p.Version)

	switch p.Source.Kind {
	case "github", "url":
		cloneURL := p.Source.URL
		if p.Source.Kind == "github" {
			cloneURL = "https://github.com/" + strings.Trim(p.Source.Repo, "/")
		}
		if cloneURL == "" {
			return fmt.Errorf("plugin %q: empty source url", p.Name)
		}
		tmp, err := os.MkdirTemp("", "coddy-plugin-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		dst := filepath.Join(tmp, "repo")
		if err := safeClone(cloneURL, p.Source.Ref, dst); err != nil {
			return fmt.Errorf("clone plugin %q: %w", p.Name, err)
		}
		entry.Repo = cloneURL
		entry.Ref = p.Source.Ref
		return installFromDir(dst, entry, managedDir, lock, res)

	case "path":
		if repoRoot == "" {
			return fmt.Errorf("plugin %q: relative path source requires a repository (not supported for API sources)", p.Name)
		}
		dir := filepath.Join(repoRoot, filepath.Clean("/"+p.Source.Path))
		return installFromDir(dir, entry, managedDir, lock, res)

	default:
		return fmt.Errorf("plugin %q: unsupported source kind %q", p.Name, p.Source.Kind)
	}
}

// installFromDir finds every skill dir under root and copies each into managedDir.
func installFromDir(root string, entry RemoteEntry, managedDir string, lock map[string]RemoteEntry, res *SyncResult) error {
	hits := locateSkillDirs(root)
	if len(hits) == 0 {
		return fmt.Errorf("no SKILL.md found under %s", filepath.Base(root))
	}
	var firstErr error
	for _, h := range hits {
		name, err := sanitizeSkillName(h.name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		dst := filepath.Join(managedDir, name)
		_, existed := os.Stat(dst)
		// Copy into a sibling temp dir, move any existing install aside to a
		// backup, swap the new copy in, then drop the backup — so neither a copy
		// nor a rename failure can leave the skill deleted or half-written. Sync
		// is serialized (syncMu) so these sidecar names never collide.
		tmpDst := filepath.Join(managedDir, ".tmp-"+name)
		bakDst := filepath.Join(managedDir, ".bak-"+name)
		_ = os.RemoveAll(tmpDst)
		_ = os.RemoveAll(bakDst)
		if err := copySkillDir(h.dir, tmpDst); err != nil {
			_ = os.RemoveAll(tmpDst)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		movedAside := false
		if existed == nil { // dst currently exists
			if err := os.Rename(dst, bakDst); err != nil {
				_ = os.RemoveAll(tmpDst)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			movedAside = true
		}
		if err := os.Rename(tmpDst, dst); err != nil {
			_ = os.RemoveAll(tmpDst)
			if movedAside {
				if rbErr := os.Rename(bakDst, dst); rbErr != nil {
					// Both the swap and the rollback failed: keep the backup and
					// surface where the previous copy is so it can be recovered.
					err = fmt.Errorf("install %q failed (%w) and rollback failed (%v); previous copy left at %s", name, err, rbErr, bakDst)
				}
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		_ = os.RemoveAll(bakDst)
		ent := entry
		if ent.Version == "" {
			ent.Version = skillDirVersion(h.dir)
		}
		lock[name] = ent
		if existed == nil {
			res.Updated = append(res.Updated, name)
		} else {
			res.Added = append(res.Added, name)
		}
	}
	return firstErr
}

// skillDirVersion reads the optional version from a skill directory's SKILL.md
// frontmatter. Used when a marketplace plugin entry does not declare a version.
func skillDirVersion(dir string) string {
	if s, err := loadFile(filepath.Join(dir, "SKILL.md")); err == nil {
		return strings.TrimSpace(s.Version)
	}
	return ""
}

// skillHit is a discovered skill directory (the one holding SKILL.md).
type skillHit struct {
	dir  string
	name string
}

// locateSkillDirs recursively finds directories containing a SKILL.md under
// root (any depth up to maxWalkDepth), skipping .git and node_modules. It does
// not hardcode skills/ or plugins/, so it handles root, skills/<name>/,
// .claude/skills/<name>/, and plugins/<p>/skills/<s>/ layouts alike.
//
// Duplicate skill names (e.g. a root SKILL.md plus a nested skills/<name>/SKILL.md)
// collapse to one hit, preferring the deeper (resource-colocated) directory.
func locateSkillDirs(root string) []skillHit {
	byName := map[string]skillHit{}
	depthOf := map[string]int{}

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxWalkDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
			name := skillNameForDir(dir, root)
			if name != "" {
				if prev, ok := byName[name]; !ok || depth > depthOf[name] {
					byName[name] = skillHit{dir: dir, name: name}
					depthOf[name] = depth
					_ = prev
				}
			}
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			n := e.Name()
			if n == ".git" || n == "node_modules" {
				continue
			}
			walk(filepath.Join(dir, n), depth+1)
		}
	}
	walk(root, 0)

	out := make([]skillHit, 0, len(byName))
	for _, h := range byName {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// skillNameForDir derives a skill's canonical name. Precedence: SKILL.md
// frontmatter name, then .claude-plugin/plugin.json name, then the directory
// basename. At the clone root the basename is a throwaway temp name, so
// frontmatter/plugin.json are strongly preferred there.
func skillNameForDir(dir, root string) string {
	if s, err := loadFile(filepath.Join(dir, "SKILL.md")); err == nil {
		if n := strings.TrimSpace(s.Name); n != "" && !strings.EqualFold(n, "SKILL") {
			return n
		}
	}
	if pj := readPluginJSON(dir); pj != nil && strings.TrimSpace(pj.Name) != "" {
		return strings.TrimSpace(pj.Name)
	}
	if dir == root {
		return "" // no reliable name for a root SKILL.md; skip rather than use temp dir name
	}
	return filepath.Base(dir)
}

// copySkillDir recursively copies src to dst, excluding .git and skipping
// symlinks (following a link inside an untrusted clone could copy arbitrary
// host files into the managed skills dir).
func copySkillDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil // never follow links out of the skill directory
		}
		if info.IsDir() {
			if info.Name() == ".git" && rel != "." {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		return copyFile(path, filepath.Join(dst, rel), info)
	})
}

func copyFile(src, dst string, info os.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src) //nolint:gosec // src is inside a controlled clone dir
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm()) //nolint:gosec // dst under managed dir
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// fetchManifestHTTP GETs an agents-standard marketplace manifest from an API URL,
// guarding against SSRF and capping the response size.
func fetchManifestHTTP(ctx context.Context, rawURL string) (*Marketplace, error) {
	if _, err := web.ValidateFetchURL(ctx, rawURL); err != nil {
		return nil, fmt.Errorf("url not allowed: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "coddy-agent-skills")
	client := &http.Client{
		Timeout: 30 * time.Second,
		// Re-run the SSRF guard on every redirect target so a public URL cannot
		// bounce the request to localhost / private infrastructure.
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if _, err := web.ValidateFetchURL(r.Context(), r.URL.String()); err != nil {
				return fmt.Errorf("redirect not allowed: %w", err)
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("marketplace fetch %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes))
	if err != nil {
		return nil, err
	}
	var mf Marketplace
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("parse marketplace json: %w", err)
	}
	return &mf, nil
}

// ---- lockfile ----

func remoteLockPath(managedDir string) string { return filepath.Join(managedDir, remoteLockFile) }

// readRemoteLock loads the provenance sidecar; a missing/invalid file yields an empty map.
func readRemoteLock(managedDir string) map[string]RemoteEntry {
	out := map[string]RemoteEntry{}
	data, err := os.ReadFile(remoteLockPath(managedDir))
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

func writeRemoteLock(managedDir string, lock map[string]RemoteEntry) error {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(remoteLockPath(managedDir), data, 0o644)
}

// RemoteSources returns the set of skill names installed from a remote source,
// keyed by canonical skill name. Used by callers to badge remote skills.
func RemoteSources(cfg *config.Config) map[string]RemoteEntry {
	return readRemoteLock(cfg.Skills.ManagedDir(cfg.Paths.Home))
}

// ---- config mutation ----

// AddSource appends a source to skills.sources and persists config.yaml.
// It reports whether the source was newly added.
func AddSource(cfg *config.Config, source string) (bool, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return false, fmt.Errorf("empty source")
	}
	if _, err := parseSource(source); err != nil {
		return false, err
	}
	sourceMu.Lock()
	defer sourceMu.Unlock()
	return applySourceChange(cfg, func(current []string) ([]string, bool, error) {
		for _, s := range current {
			if strings.EqualFold(strings.TrimSpace(s), source) {
				return current, false, nil
			}
		}
		return append(append([]string(nil), current...), source), true, nil
	})
}

// applySourceChange reloads the on-disk config so a stale in-memory *Config
// cannot clobber unrelated settings, lets mutate compute the new source list
// from the current on-disk sources, persists only when it changed, and mirrors
// the result back into cfg. Callers hold sourceMu.
func applySourceChange(cfg *config.Config, mutate func(current []string) (next []string, changed bool, err error)) (bool, error) {
	fresh := cfg
	if strings.TrimSpace(cfg.Paths.ConfigPath) != "" {
		reloaded, err := config.LoadWithPaths(cfg.Paths)
		switch {
		case err == nil && reloaded != nil:
			fresh = reloaded
		case errors.Is(err, os.ErrNotExist):
			// No config file on disk yet — it will be created from cfg below.
		default:
			// A real read/parse error: fail loudly rather than persist the
			// (possibly stale) caller config over whatever is on disk.
			return false, fmt.Errorf("reload config before source change: %w", err)
		}
	}
	next, changed, err := mutate(fresh.Skills.Sources)
	if err != nil {
		return false, err
	}
	if !changed {
		cfg.Skills.Sources = append([]string(nil), fresh.Skills.Sources...)
		return false, nil
	}
	fresh.Skills.Sources = next
	if err := persistConfig(fresh); err != nil {
		return false, err
	}
	cfg.Skills.Sources = append([]string(nil), next...)
	return true, nil
}

// RemoveRemote deletes an installed remote skill directory and its lock entry.
func RemoveRemote(cfg *config.Config, skillName string) error {
	name, err := sanitizeSkillName(skillName)
	if err != nil {
		return err
	}
	// Serialize with Sync/UpdateSkill so removal cannot race a materialization
	// (deleting a dir mid-swap) or lose a concurrent .remote.json update.
	syncMu.Lock()
	defer syncMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	lock := readRemoteLock(managedDir)
	if _, ok := lock[name]; !ok {
		return fmt.Errorf("skill %q is not a remote (synced) skill", name)
	}
	if err := os.RemoveAll(filepath.Join(managedDir, name)); err != nil {
		return fmt.Errorf("remove skill dir: %w", err)
	}
	delete(lock, name)
	return writeRemoteLock(managedDir, lock)
}

// ---- version display ----

// InstalledVersion returns the version to display for a loaded skill: the
// version recorded in the remote lockfile when the skill was synced, else the
// skill's own SKILL.md frontmatter version. Empty when neither is known.
func InstalledVersion(remote map[string]RemoteEntry, name string, sk *Skill) string {
	if ent, ok := remote[name]; ok && strings.TrimSpace(ent.Version) != "" {
		return ent.Version
	}
	if sk != nil {
		return strings.TrimSpace(sk.Version)
	}
	return ""
}

// ---- update detection ----

// UpdateStatus reports whether a newer version of an installed remote skill is
// available in its marketplace source.
type UpdateStatus struct {
	Name            string `json:"name"`
	Source          string `json:"source"`
	Version         string `json:"version"` // installed
	Latest          string `json:"latest"`  // latest declared upstream
	UpdateAvailable bool   `json:"update_available"`
}

// CheckUpdates fetches the manifest for every remote source and reports, per
// installed remote skill, whether a newer version is available. It performs
// network / git access but never modifies installed skills. Sources that cannot
// be reached are treated as "no update" rather than failing the whole check.
func CheckUpdates(ctx context.Context, cfg *config.Config) ([]UpdateStatus, error) {
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	lock := readRemoteLock(managedDir)
	if len(lock) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(lock))
	for n := range lock {
		names = append(names, n)
	}
	sort.Strings(names)

	cache := map[string]map[string]string{} // source -> (plugin/skill name -> version)
	out := make([]UpdateStatus, 0, len(names))
	for _, name := range names {
		ent := lock[name]
		st := UpdateStatus{Name: name, Source: ent.Source, Version: ent.Version, Latest: ent.Version}
		versions, ok := cache[ent.Source]
		if !ok {
			versions, _ = sourceManifestVersions(ctx, ent.Source) // best-effort
			cache[ent.Source] = versions
		}
		key := ent.Plugin
		if key == "" {
			key = name
		}
		if latest := strings.TrimSpace(versions[key]); latest != "" {
			st.Latest = latest
			st.UpdateAvailable = compareVersions(latest, ent.Version) > 0
		}
		out = append(out, st)
	}
	return out, nil
}

// UpdateSkill re-syncs the source that provides skillName, installing whatever
// version that source currently declares. Fails if the skill was not installed
// from a remote source.
func UpdateSkill(ctx context.Context, cfg *config.Config, skillName string) (*SyncResult, error) {
	name, err := sanitizeSkillName(skillName)
	if err != nil {
		return nil, err
	}
	syncMu.Lock()
	defer syncMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	lock := readRemoteLock(managedDir)
	ent, ok := lock[name]
	if !ok {
		return nil, fmt.Errorf("skill %q is not a remote (synced) skill", name)
	}
	res := &SyncResult{}
	if err := syncOne(ctx, ent.Source, managedDir, lock, res); err != nil {
		res.Failed = append(res.Failed, SyncFailure{Source: ent.Source, Error: err.Error()})
	}
	if err := writeRemoteLock(managedDir, lock); err != nil {
		return res, fmt.Errorf("write lock: %w", err)
	}
	return res, nil
}

// AvailablePlugin is one installable plugin advertised by a configured
// marketplace, for the "install skills" browse/filter UI.
type AvailablePlugin struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version,omitempty"`
	Source      string `json:"source"`    // the configured source it comes from
	Installed   bool   `json:"installed"` // already present on disk
}

// AvailablePlugins fetches every configured marketplace manifest (network / git)
// and returns the plugins they advertise, flagged with whether each is already
// installed. Sources that cannot be reached are skipped best-effort.
func AvailablePlugins(ctx context.Context, cfg *config.Config, cwd string) ([]AvailablePlugin, error) {
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	installed := map[string]bool{}
	loader := NewLoader(cfg.Skills.Dirs)
	if loaded, err := loader.LoadAll(cwd, cfg.Paths.Home, cfg.Skills.ManagedDir(cfg.Paths.Home)); err == nil {
		for _, sk := range loaded {
			installed[CanonicalCommandName(sk)] = true
		}
	}
	seen := map[string]bool{}
	out := []AvailablePlugin{}
	for _, src := range ListSources(cfg) {
		mf, err := fetchSourceManifest(ctx, src)
		if err != nil || mf == nil {
			continue
		}
		for _, p := range mf.Plugins {
			name := strings.TrimSpace(p.Name)
			if name == "" {
				continue
			}
			key := src + "\x00" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, AvailablePlugin{
				Name:        name,
				Description: strings.TrimSpace(p.Description),
				Version:     strings.TrimSpace(p.Version),
				Source:      src,
				Installed:   installed[name],
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// InstallPlugin installs a single plugin from a marketplace source (rather than
// syncing every plugin the source advertises). Backs the browse/filter install UI.
func InstallPlugin(ctx context.Context, cfg *config.Config, source, pluginName string) (*SyncResult, error) {
	source = strings.TrimSpace(source)
	pluginName = strings.TrimSpace(pluginName)
	if source == "" || pluginName == "" {
		return nil, fmt.Errorf("install requires a source and a plugin name")
	}
	spec, err := parseSource(source)
	if err != nil {
		return nil, err
	}
	syncMu.Lock()
	defer syncMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		return nil, fmt.Errorf("create managed dir: %w", err)
	}
	lock := readRemoteLock(managedDir)
	res := &SyncResult{}

	var mf *Marketplace
	repoRoot := ""
	base := RemoteEntry{Source: source}
	switch spec.kind {
	case "api":
		if mf, err = fetchManifestHTTP(ctx, spec.url); err != nil {
			return nil, err
		}
		base.URL = spec.url
	case "git":
		tmp, err := os.MkdirTemp("", "coddy-installplugin-")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		clone := filepath.Join(tmp, "repo")
		if err := safeClone(spec.url, spec.ref, clone); err != nil {
			return nil, fmt.Errorf("clone %s: %w", spec.url, err)
		}
		base.Repo = spec.url
		base.Ref = spec.ref
		mfPath := findMarketplaceFile(clone)
		if mfPath == "" {
			return nil, fmt.Errorf("source %q has no marketplace.json to install a named plugin from", source)
		}
		if mf, err = parseMarketplace(mfPath); err != nil {
			return nil, err
		}
		repoRoot = clone
	default:
		return nil, fmt.Errorf("unsupported source kind %q", spec.kind)
	}

	var target *MarketplacePlugin
	for i := range mf.Plugins {
		if strings.EqualFold(strings.TrimSpace(mf.Plugins[i].Name), pluginName) {
			target = &mf.Plugins[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("plugin %q not found in %s", pluginName, source)
	}
	if err := installPlugin(*target, repoRoot, source, base, managedDir, lock, res); err != nil {
		res.Failed = append(res.Failed, SyncFailure{Source: source, Error: err.Error()})
	}
	if err := writeRemoteLock(managedDir, lock); err != nil {
		return res, fmt.Errorf("write lock: %w", err)
	}
	return res, nil
}

// fetchSourceManifest fetches a source's agents-standard marketplace manifest
// (HTTP for API sources, a shallow clone for git sources). Returns an error when
// the source has no manifest.
func fetchSourceManifest(ctx context.Context, source string) (*Marketplace, error) {
	spec, err := parseSource(source)
	if err != nil {
		return nil, err
	}
	switch spec.kind {
	case "api":
		return fetchManifestHTTP(ctx, spec.url)
	case "git":
		tmp, err := os.MkdirTemp("", "coddy-mf-")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		clone := filepath.Join(tmp, "repo")
		if err := safeClone(spec.url, spec.ref, clone); err != nil {
			return nil, err
		}
		mfPath := findMarketplaceFile(clone)
		if mfPath == "" {
			return nil, fmt.Errorf("no marketplace.json in %s", source)
		}
		return parseMarketplace(mfPath)
	default:
		return nil, fmt.Errorf("unsupported source kind %q", spec.kind)
	}
}

// sourceManifestVersions fetches a source's marketplace manifest and returns a
// pluginName -> version map. For a plain repo with no manifest it maps each
// discovered skill's frontmatter version by skill name instead.
func sourceManifestVersions(ctx context.Context, source string) (map[string]string, error) {
	spec, err := parseSource(source)
	if err != nil {
		return nil, err
	}
	switch spec.kind {
	case "api":
		mf, err := fetchManifestHTTP(ctx, spec.url)
		if err != nil {
			return nil, err
		}
		return marketplaceVersions(mf), nil

	case "git":
		tmp, err := os.MkdirTemp("", "coddy-skillcheck-")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		clone := filepath.Join(tmp, "repo")
		if err := safeClone(spec.url, spec.ref, clone); err != nil {
			return nil, err
		}
		if mfPath := findMarketplaceFile(clone); mfPath != "" {
			mf, err := parseMarketplace(mfPath)
			if err != nil {
				return nil, err
			}
			return marketplaceVersions(mf), nil
		}
		out := map[string]string{}
		for _, h := range locateSkillDirs(clone) {
			if v := skillDirVersion(h.dir); v != "" {
				out[h.name] = v
			}
		}
		return out, nil

	default:
		return map[string]string{}, nil
	}
}

// marketplaceVersions maps each plugin name to its declared version (entries
// without a version are omitted, so update detection has no false positives).
func marketplaceVersions(mf *Marketplace) map[string]string {
	out := map[string]string{}
	for _, p := range mf.Plugins {
		if v := strings.TrimSpace(p.Version); v != "" {
			out[strings.TrimSpace(p.Name)] = v
		}
	}
	return out
}

// compareVersions returns -1, 0, or 1 comparing two versions using semantic
// versioning precedence: numeric core fields compare left to right, and when
// cores are equal a normal release outranks a prerelease (§11). An optional
// leading "v" and any +build metadata are ignored. When either side has a
// non-numeric core it falls back to a lexical comparison.
func compareVersions(a, b string) int {
	ca, pa, oka := parseSemver(a)
	cb, pb, okb := parseSemver(b)
	if oka && okb {
		if c := compareCore(ca, cb); c != 0 {
			return c
		}
		return comparePrerelease(pa, pb)
	}
	sa := strings.TrimPrefix(strings.TrimSpace(a), "v")
	sb := strings.TrimPrefix(strings.TrimSpace(b), "v")
	return strings.Compare(sa, sb)
}

// parseSemver splits "v1.2.3-rc.1+build" into the numeric core [1 2 3] and the
// prerelease string ("rc.1"); ok is false when the core is not all-integer.
func parseSemver(v string) (core []int, prerelease string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return nil, "", false
	}
	if i := strings.IndexByte(v, '+'); i >= 0 { // drop build metadata
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 { // split off prerelease
		prerelease = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	core = make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, "", false
		}
		core = append(core, n)
	}
	return core, prerelease, true
}

// compareCore compares dotted numeric fields, treating a missing field as 0.
func compareCore(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// comparePrerelease implements semver §11 precedence for the prerelease part:
// an empty prerelease (a release) outranks any prerelease; otherwise dot-
// separated identifiers compare field by field (numeric < alphanumeric, numeric
// numerically, alphanumeric lexically), and a shorter set of fields is lower.
func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		if i >= len(as) {
			return -1
		}
		if i >= len(bs) {
			return 1
		}
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aerr == nil: // numeric identifiers are lower than alphanumeric
			return -1
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return 0
}

// ---- source management ----

// ListSources returns the configured remote skill sources (trimmed, non-empty).
func ListSources(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Skills.Sources))
	for _, s := range cfg.Skills.Sources {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// RemoveSource drops a source from skills.sources and persists config.yaml.
// It reports whether a matching source was found and removed.
func RemoveSource(cfg *config.Config, source string) (bool, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return false, fmt.Errorf("empty source")
	}
	sourceMu.Lock()
	defer sourceMu.Unlock()
	return applySourceChange(cfg, func(current []string) ([]string, bool, error) {
		kept := make([]string, 0, len(current))
		removed := false
		for _, s := range current {
			if strings.EqualFold(strings.TrimSpace(s), source) {
				removed = true
				continue
			}
			kept = append(kept, s)
		}
		return kept, removed, nil
	})
}

func persistConfig(cfg *config.Config) error {
	path := cfg.Paths.ConfigPath
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path is empty")
	}
	data, err := config.MarshalConfigYAML(cfg)
	if err != nil {
		return err
	}
	if err := config.BackupCurrent(path); err != nil {
		return err
	}
	return config.AtomicWriteConfigYAML(path, data)
}
