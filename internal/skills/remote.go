package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/web"
)

// remoteLockFile is the provenance sidecar written into the managed skills dir.
const remoteLockFile = ".remote.json"

// maxManifestBytes caps an API marketplace.json download.
const maxManifestBytes = 4 << 20

// maxWalkDepth bounds the recursive SKILL.md search inside a clone.
const maxWalkDepth = 6

// RemoteEntry records where an installed skill came from (one per skill dir).
type RemoteEntry struct {
	Source string `json:"source"`         // the configured source string
	Repo   string `json:"repo,omitempty"` // git URL the skill was cloned from
	Ref    string `json:"ref,omitempty"`  // branch or tag
	URL    string `json:"url,omitempty"`  // API marketplace URL, when applicable
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
		if err := gitws.Clone(spec.url, spec.ref, clone); err != nil {
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
		if err := gitws.Clone(cloneURL, p.Source.Ref, dst); err != nil {
			return fmt.Errorf("clone plugin %q: %w", p.Name, err)
		}
		entry := base
		entry.Repo = cloneURL
		entry.Ref = p.Source.Ref
		return installFromDir(dst, entry, managedDir, lock, res)

	case "path":
		if repoRoot == "" {
			return fmt.Errorf("plugin %q: relative path source requires a repository (not supported for API sources)", p.Name)
		}
		dir := filepath.Join(repoRoot, filepath.Clean("/"+p.Source.Path))
		return installFromDir(dir, base, managedDir, lock, res)

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
		if err := os.RemoveAll(dst); err != nil && firstErr == nil {
			firstErr = err
			continue
		}
		if err := copySkillDir(h.dir, dst); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		lock[name] = entry
		if existed == nil {
			res.Updated = append(res.Updated, name)
		} else {
			res.Added = append(res.Added, name)
		}
	}
	return firstErr
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

// copySkillDir recursively copies src to dst, excluding .git.
func copySkillDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
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
	client := &http.Client{Timeout: 30 * time.Second}
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
	for _, s := range cfg.Skills.Sources {
		if strings.EqualFold(strings.TrimSpace(s), source) {
			return false, nil
		}
	}
	cfg.Skills.Sources = append(cfg.Skills.Sources, source)
	if err := persistConfig(cfg); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveRemote deletes an installed remote skill directory and its lock entry.
func RemoveRemote(cfg *config.Config, skillName string) error {
	name, err := sanitizeSkillName(skillName)
	if err != nil {
		return err
	}
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
