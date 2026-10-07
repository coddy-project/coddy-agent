package skills

// Marketplaces, the catalog kind of entry of a marketplaces.json
// (declarations.go), kept the way Claude Code keeps them. A marketplace is
// known by the name its marketplace.json gives, and declaring it - `plugin
// marketplace add` writes it into the operator's file - installs nothing.
// `plugin install <plugin>@<marketplace>` installs one of its plugins and
// reads the marketplace again first, so a plugin it published after it was
// added is found without an update. Updating it refreshes what it lists and
// the plugins installed from it, and never installs the others.
//
// A source - the system one, one written by `plugin install <owner/repo |
// url>`, one an operator or a project put in a file - keeps the older
// contract: every plugin it publishes is installed and kept in sync.
//
// What each marketplace listed when it was last read is state, not
// configuration: a cache next to the lockfile in the managed skills dir.
// Before the declaration files existed that cache was the list of added
// marketplaces itself; such a cache is moved into the operator's file once.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// marketplacesFile is the cache of what each marketplace listed, in the
// managed skills dir.
const marketplacesFile = ".marketplaces.json"

// marketplacesCacheVersion marks a cache written since the declaration files
// exist; one without it is the older list of added marketplaces.
const marketplacesCacheVersion = 2

// AddedMarketplace is a marketplace in effect: its declaration and what it
// listed when last read.
type AddedMarketplace struct {
	Name      string         `json:"name"`                 // the name its marketplace.json gives
	Source    string         `json:"source"`               // owner/repo, a git URL or a marketplace.json URL
	Plugins   []ListedPlugin `json:"plugins"`              // what it listed when last read
	UpdatedAt string         `json:"updated_at,omitempty"` // when it was last read, RFC 3339
	Origin    string         `json:"-"`                    // where it is declared (OriginHome, OriginProject)
}

// ListedPlugin is one plugin an added marketplace lists.
type ListedPlugin struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
}

// errMarketplaceNotAdded is a name no added marketplace carries.
var errMarketplaceNotAdded = errors.New("marketplace is not added")

// marketplaceNamePattern is what a marketplace name, and the plugin name in
// <plugin>@<marketplace>, may look like. With no "/", ":" or "@" in it the form
// never reads as owner/repo@ref or git@host:path.
var marketplaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ParsePluginRef splits <plugin>@<marketplace>. ok is false for anything
// else, a source (owner/repo[@ref], a git URL, a marketplace URL) included.
func ParsePluginRef(ref string) (plugin, market string, ok bool) {
	plugin, market, found := strings.Cut(strings.TrimSpace(ref), "@")
	if !found || !marketplaceNamePattern.MatchString(plugin) || !marketplaceNamePattern.MatchString(market) {
		return "", "", false
	}
	return plugin, market, true
}

// AddedMarketplaces returns the marketplaces in effect for cwd, by name: the
// operator's, then the project's the trust gate admits, each with what it
// listed when last read.
func AddedMarketplaces(cfg *config.Config, cwd string) ([]AddedMarketplace, error) {
	decls, errs := Declarations(cfg, cwd)
	cache, err := readMarketplaces(cfg)
	if err != nil {
		return nil, err
	}
	out := marketplacesInEffect(decls, cache)
	if len(errs) > 0 && len(out) == 0 {
		return nil, errs[0]
	}
	return out, nil
}

// marketplacesInEffect joins the marketplaces in effect of decls with the
// listings of cache.
func marketplacesInEffect(decls []Declaration, cache []AddedMarketplace) []AddedMarketplace {
	var out []AddedMarketplace
	for _, d := range inEffect(decls) {
		if d.Kind != KindMarketplace {
			continue
		}
		m := AddedMarketplace{Name: d.Name, Source: d.Source, Origin: d.Origin}
		if i := indexAdded(cache, d.Source, true); i >= 0 {
			m.Plugins, m.UpdatedAt = cache[i].Plugins, cache[i].UpdatedAt
		}
		out = append(out, m)
	}
	return out
}

// AddMarketplace reads the marketplace source publishes and declares it - in
// the operator's file for ScopeGlobal (or an empty scope), in the project's
// for ScopeLocal, which also approves it there - or, when it is declared
// already, refreshes its list; refreshed says which. It installs nothing. A
// source without a marketplace.json, or whose marketplace.json gives no
// usable name, cannot be added.
func AddMarketplace(ctx context.Context, cfg *config.Config, cwd, source, scope string) (AddedMarketplace, bool, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return AddedMarketplace{}, false, fmt.Errorf("empty source")
	}
	if _, err := parseSource(source); err != nil {
		return AddedMarketplace{}, false, err
	}
	path, err := declarationPath(cfg, cwd, scope)
	if err != nil {
		return AddedMarketplace{}, false, err
	}
	syncMu.Lock()
	defer syncMu.Unlock()
	om, err := openMarketplace(ctx, source)
	if errors.Is(err, errNoMarketplace) {
		return AddedMarketplace{}, false, fmt.Errorf("%s publishes no marketplace.json, so there is no marketplace to add; install its skills with `plugin install %s`", source, source)
	}
	if err != nil {
		return AddedMarketplace{}, false, err
	}
	defer om.close()
	name := strings.TrimSpace(om.mf.Name)
	if !marketplaceNamePattern.MatchString(name) {
		return AddedMarketplace{}, false, fmt.Errorf("the marketplace.json of %s names it %q, which `plugin install <plugin>@<marketplace>` cannot use; install every plugin it lists with `plugin install %s`", source, name, source)
	}
	declared, err := declareMarketplace(path, name, source)
	if err != nil {
		return AddedMarketplace{}, false, err
	}
	origin := OriginHome
	if scope == ScopeLocal {
		origin = OriginProject
		if err := approveOwn(cfg, cwd, projectEntry(path, Declaration{Kind: KindMarketplace, Name: name, Source: source, Path: path})); err != nil {
			return AddedMarketplace{}, false, err
		}
	}
	added := AddedMarketplace{Name: name, Source: source, Plugins: listingOf(om.mf), UpdatedAt: nowStamp(), Origin: origin}
	if err := cacheListing(cfg, added); err != nil {
		return AddedMarketplace{}, false, err
	}
	return added, !declared, nil
}

// InstallFromMarketplace installs the plugin named plugin from the
// marketplace named market, one in effect for cwd. It reads the marketplace
// again first, so a plugin published after the marketplace was added is
// found, and refreshes its list. A project marketplace the trust gate holds
// back is refused with how to approve it.
func InstallFromMarketplace(ctx context.Context, cfg *config.Config, cwd, plugin, market string) (*SyncResult, error) {
	syncMu.Lock()
	defer syncMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		return nil, fmt.Errorf("create managed dir: %w", err)
	}
	decls, _ := Declarations(cfg, cwd)
	cache, err := readMarketplaces(cfg)
	if err != nil {
		return nil, err
	}
	ms := marketplacesInEffect(decls, cache)
	i := indexAdded(ms, market, false)
	if i < 0 {
		if d, ok := heldMarketplace(decls, market); ok {
			return nil, heldError(d)
		}
		return nil, notAddedError(market, plugin, ms)
	}
	m := ms[i]
	om, err := openMarketplace(ctx, m.Source)
	if err != nil {
		return nil, fmt.Errorf("read marketplace %q from %s: %w", m.Name, m.Source, err)
	}
	defer om.close()
	m.Plugins, m.UpdatedAt = listingOf(om.mf), nowStamp()
	if err := cacheListing(cfg, m); err != nil {
		return nil, err
	}
	target := om.mf.plugin(plugin)
	if target == nil {
		return nil, fmt.Errorf("plugin %q is not in marketplace %q, which lists %d plugin(s) as read just now; `plugin marketplace list %s` names them", plugin, m.Name, len(m.Plugins), m.Name)
	}
	return installOne(ctx, om, *target, managedDir)
}

// heldMarketplace finds a project marketplace named key that the trust gate
// holds back.
func heldMarketplace(decls []Declaration, key string) (Declaration, bool) {
	for _, d := range held(decls) {
		if d.Kind == KindMarketplace && keyNames(key, d.Kind, d.Name, d.Source) {
			return d, true
		}
	}
	return Declaration{}, false
}

// ErrHeld is what every refusal of a project entry the workspace trust gate
// holds back answers to (errors.Is): one awaiting approval, or one
// skills.project_trust: deny switches off.
var ErrHeld = errors.New("held by the workspace trust gate")

// heldDeclarationError is the refusal heldError returns; its text says why
// and what to run.
type heldDeclarationError struct{ msg string }

func (e heldDeclarationError) Error() string        { return e.msg }
func (e heldDeclarationError) Is(target error) bool { return target == ErrHeld }

// heldError says why a project entry is not used and how to change that.
func heldError(d Declaration) error {
	if d.State == StateDenied {
		return heldDeclarationError{fmt.Sprintf("%q is declared by %s, and project marketplaces are switched off by skills.project_trust: deny", d.Key(), d.Path)}
	}
	return heldDeclarationError{fmt.Sprintf("%q is declared by %s, which travels with the checkout, and is not approved for this workspace yet: review it, then approve it with `coddy plugin marketplace trust %s` or the shield in Settings -> Skills", d.Key(), d.Path, d.Key())}
}

// UpdateMarketplace reads the marketplace key names (by name or source), one
// in effect for cwd, again, refreshes its list and reinstalls the plugins
// installed from it. The plugins it lists that are not installed stay so. A
// key no marketplace in effect answers to is errMarketplaceNotAdded.
func UpdateMarketplace(ctx context.Context, cfg *config.Config, cwd, key string) (AddedMarketplace, *SyncResult, error) {
	syncMu.Lock()
	defer syncMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	decls, _ := Declarations(cfg, cwd)
	cache, err := readMarketplaces(cfg)
	if err != nil {
		return AddedMarketplace{}, nil, err
	}
	ms := marketplacesInEffect(decls, cache)
	i := indexAdded(ms, key, false)
	if i < 0 {
		i = indexAdded(ms, key, true)
	}
	if i < 0 {
		if d, ok := heldMarketplace(decls, key); ok {
			return AddedMarketplace{}, nil, heldError(d)
		}
		return AddedMarketplace{}, nil, fmt.Errorf("%w: %q", errMarketplaceNotAdded, key)
	}
	lock := readRemoteLock(managedDir)
	res := &SyncResult{}
	if err := updateAddedLocked(ctx, &ms[i], managedDir, lock, res); err != nil {
		return ms[i], res, err
	}
	if err := writeRemoteLock(managedDir, lock); err != nil {
		return ms[i], res, fmt.Errorf("write lock: %w", err)
	}
	if err := cacheListing(cfg, ms[i]); err != nil {
		return ms[i], res, err
	}
	return ms[i], res, nil
}

// updateAddedLocked reads m again, refreshes its list and reinstalls the
// plugins the lock records as installed from it. Callers hold syncMu, and write
// the lock and the list afterwards.
func updateAddedLocked(ctx context.Context, m *AddedMarketplace, managedDir string, lock map[string]RemoteEntry, res *SyncResult) error {
	om, err := openMarketplace(ctx, m.Source)
	if err != nil {
		return fmt.Errorf("read marketplace %q from %s: %w", m.Name, m.Source, err)
	}
	defer om.close()
	m.Plugins, m.UpdatedAt = listingOf(om.mf), nowStamp()
	for _, name := range installedPlugins(lock, m.Source) {
		p := om.mf.plugin(name)
		if p == nil {
			res.Failed = append(res.Failed, SyncFailure{Source: m.Name, Error: fmt.Sprintf("plugin %q is no longer in the marketplace; its skills stay installed", name)})
			continue
		}
		if err := installPlugin(ctx, *p, om.repoRoot, m.Source, om.base, managedDir, lock, res); err != nil {
			res.Failed = append(res.Failed, SyncFailure{Source: m.Name, Error: err.Error()})
		}
	}
	return nil
}

// updatePluginLocked reinstalls the one plugin ent was installed from, reading
// its marketplace again. Callers hold syncMu and write the lock afterwards.
func updatePluginLocked(ctx context.Context, ent RemoteEntry, managedDir string, lock map[string]RemoteEntry, res *SyncResult) error {
	om, err := openMarketplace(ctx, ent.Source)
	if err != nil {
		return err
	}
	defer om.close()
	p := om.mf.plugin(ent.Plugin)
	if p == nil {
		return fmt.Errorf("plugin %q is no longer in %s; its skills stay installed", ent.Plugin, ent.Source)
	}
	return installPlugin(ctx, *p, om.repoRoot, ent.Source, om.base, managedDir, lock, res)
}

// syncAddedLocked refreshes every marketplace in effect of decls and the
// plugins installed from it, as Sync does after the sources. One that is
// also a source in effect was synced whole already and is skipped. Callers
// hold syncMu.
func syncAddedLocked(ctx context.Context, cfg *config.Config, decls []Declaration, managedDir string, lock map[string]RemoteEntry, res *SyncResult) {
	cache, err := readMarketplaces(cfg)
	if err != nil {
		res.Failed = append(res.Failed, SyncFailure{Source: marketplacesFile, Error: err.Error()})
		return
	}
	ms := marketplacesInEffect(decls, cache)
	whole := wholeSources(decls)
	for i := range ms {
		if containsSource(whole, ms[i].Source) {
			continue
		}
		if err := updateAddedLocked(ctx, &ms[i], managedDir, lock, res); err != nil {
			res.Failed = append(res.Failed, SyncFailure{Source: ms[i].Name, Error: err.Error()})
			continue
		}
		if err := cacheListing(cfg, ms[i]); err != nil {
			res.Failed = append(res.Failed, SyncFailure{Source: marketplacesFile, Error: err.Error()})
		}
	}
}

// isWholeSource reports whether source is one whose every plugin is installed
// for cwd: the system source, or a source of the operator's file or of a
// project file the trust gate admits.
func isWholeSource(cfg *config.Config, cwd, source string) bool {
	return containsSource(ListSources(cfg, cwd), source)
}

// containsSource reports whether sources names source, in any spelling.
func containsSource(sources []string, source string) bool {
	for _, s := range sources {
		if sameSource(s, source) {
			return true
		}
	}
	return false
}

// installedPlugins lists, sorted, the plugins the lock records as installed
// from source.
func installedPlugins(lock map[string]RemoteEntry, source string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ent := range lock {
		name := strings.TrimSpace(ent.Plugin)
		if name == "" || !sameSource(ent.Source, source) || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func notAddedError(market, plugin string, ms []AddedMarketplace) error {
	msg := fmt.Sprintf("marketplace %q is not added: add it with `plugin marketplace add <owner/repo | marketplace.json URL>`, then run `plugin install %s@%s` again", market, plugin, market)
	if len(ms) > 0 {
		names := make([]string, 0, len(ms))
		for _, m := range ms {
			names = append(names, m.Name)
		}
		msg += fmt.Sprintf(" (added: %s)", strings.Join(names, ", "))
	}
	return errors.New(msg)
}

// indexAdded finds an added marketplace by name, or by source when bySource.
func indexAdded(ms []AddedMarketplace, key string, bySource bool) int {
	key = strings.TrimSpace(key)
	for i, m := range ms {
		if bySource && sameSource(m.Source, key) || !bySource && strings.EqualFold(m.Name, key) {
			return i
		}
	}
	return -1
}

// sameSource reports whether a and b name one source: the same string, or the
// same kind, clone or marketplace URL and ref once parsed, so owner/repo matches
// https://github.com/owner/repo and a trailing "/" or ".git" does not matter.
func sameSource(a, b string) bool {
	if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
		return true
	}
	sa, errA := parseSource(a)
	sb, errB := parseSource(b)
	if errA != nil || errB != nil {
		return false
	}
	norm := func(u string) string {
		return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(u), "/"), ".git")
	}
	return sa.kind == sb.kind && strings.EqualFold(norm(sa.url), norm(sb.url)) && sa.ref == sb.ref
}

// UpdateSource refreshes what key names for cwd. A whole source (the system
// one, or a source of a marketplaces.json in effect) syncs every plugin it
// publishes; a marketplace in effect, by name or source, refreshes its list
// and the plugins installed from it; a project entry the trust gate holds
// back is refused; any other source is synced whole once, as `plugin
// marketplace sync <source>` always did. added is the marketplace key named,
// when it is one.
func UpdateSource(ctx context.Context, cfg *config.Config, cwd, key string) (res *SyncResult, added *AddedMarketplace, err error) {
	key = strings.TrimSpace(key)
	target := key
	if ms, err := AddedMarketplaces(cfg, cwd); err == nil {
		if i := indexAdded(ms, key, false); i >= 0 {
			// A name stands for its source, so a marketplace that is a whole
			// source too syncs whole by its name as by its address.
			target = ms[i].Source
		}
	}
	if !isWholeSource(cfg, cwd, target) {
		m, res, err := UpdateMarketplace(ctx, cfg, cwd, key)
		switch {
		case err == nil:
			return res, &m, nil
		case !errors.Is(err, errMarketplaceNotAdded):
			return res, nil, err
		}
		if d, ok := FindDeclaration(cfg, cwd, key); ok && d.State != StateReady {
			return nil, nil, heldError(d)
		}
		if _, perr := parseSource(key); perr != nil {
			return nil, nil, fmt.Errorf("marketplace %q is not added; `plugin marketplace list` shows the added ones", key)
		}
	}
	res, err = SyncSource(ctx, cfg, target)
	return res, nil, err
}

func listingOf(mf *Marketplace) []ListedPlugin {
	out := make([]ListedPlugin, 0, len(mf.Plugins))
	for _, p := range mf.Plugins {
		if name := strings.TrimSpace(p.Name); name != "" {
			out = append(out, ListedPlugin{Name: name, Description: strings.TrimSpace(p.Description), Version: advertisedVersion(p)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339) }

// marketplacesDoc is the file layout of marketplacesFile.
type marketplacesDoc struct {
	Version      int                `json:"version,omitempty"`
	Marketplaces []AddedMarketplace `json:"marketplaces"`
}

// readMarketplaces loads the cached listings; no file is none. A file that
// does not parse is an error rather than an empty list, so nothing writes over
// what it holds.
func readMarketplaces(cfg *config.Config) ([]AddedMarketplace, error) {
	doc, err := readMarketplacesDoc(cfg.Skills.ManagedDir(cfg.Paths.Home))
	return doc.Marketplaces, err
}

func readMarketplacesDoc(managedDir string) (marketplacesDoc, error) {
	data, err := os.ReadFile(filepath.Join(managedDir, marketplacesFile)) //nolint:gosec // a fixed name in the managed dir
	if errors.Is(err, os.ErrNotExist) {
		return marketplacesDoc{Version: marketplacesCacheVersion}, nil
	}
	if err != nil {
		return marketplacesDoc{}, err
	}
	var doc marketplacesDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return marketplacesDoc{}, fmt.Errorf("read %s: %w", filepath.Join(managedDir, marketplacesFile), err)
	}
	return doc, nil
}

// writeMarketplaces saves the cached listings, sorted by name. The caller's
// slice keeps its order.
func writeMarketplaces(managedDir string, ms []AddedMarketplace) error {
	sorted := append([]AddedMarketplace{}, ms...)
	sort.Slice(sorted, func(i, j int) bool { return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name) })
	data, err := json.MarshalIndent(marketplacesDoc{Version: marketplacesCacheVersion, Marketplaces: sorted}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(managedDir, marketplacesFile, append(data, '\n'))
}

// cacheMu serialises the read-modify-write cycles of the listing cache.
var cacheMu sync.Mutex

// cacheListing records what m listed, replacing an earlier listing of the
// same source.
func cacheListing(cfg *config.Config, m AddedMarketplace) error {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	ms, err := readMarketplaces(cfg)
	if err != nil {
		return err
	}
	m.Origin = ""
	if i := indexAdded(ms, m.Source, true); i >= 0 {
		ms[i] = m
	} else {
		ms = append(ms, m)
	}
	return writeMarketplaces(managedDir, ms)
}

// dropMarketplaceListings forgets the listings of marketplaces no file
// declares any more.
func dropMarketplaceListings(cfg *config.Config, gone []config.DeclaredMarketplace) error {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	ms, err := readMarketplaces(cfg)
	if err != nil {
		return err
	}
	kept := ms[:0:0]
	for _, m := range ms {
		drop := false
		for _, g := range gone {
			if sameSource(m.Source, g.Source) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, m)
		}
	}
	if len(kept) == len(ms) {
		return nil
	}
	return writeMarketplaces(cfg.Skills.ManagedDir(cfg.Paths.Home), kept)
}

// legacyCacheMu guards the one-time move of an older cache.
var legacyCacheMu sync.Mutex

// migrateLegacyMarketplaceCache moves the marketplaces an older Coddy added -
// whose only record was the cache, then the list of added marketplaces - into
// the operator's marketplaces.json, once: a marketplace the file declares
// already (by name or source) is left as the file has it. The cache stays, as
// the listings of those marketplaces, marked as moved. A cache or a file that
// cannot be read is left alone for the next call.
func migrateLegacyMarketplaceCache(cfg *config.Config) {
	legacyCacheMu.Lock()
	defer legacyCacheMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	doc, err := readMarketplacesDoc(managedDir)
	if err != nil || doc.Version >= marketplacesCacheVersion {
		return
	}
	path := config.GlobalMarketplacesPath(cfg.Paths.Home)
	if _, err := updateDeclarations(path, func(f *config.MarketplacesFile) (bool, error) {
		changed := false
		for _, m := range doc.Marketplaces {
			name, source := strings.TrimSpace(m.Name), strings.TrimSpace(m.Source)
			if name == "" || source == "" {
				continue
			}
			known := false
			for _, d := range f.Marketplaces {
				if strings.EqualFold(d.Name, name) || sameSource(d.Source, source) {
					known = true
					break
				}
			}
			if !known {
				f.Marketplaces = append(f.Marketplaces, config.DeclaredMarketplace{Name: name, Source: source})
				changed = true
			}
		}
		return changed, nil
	}); err != nil {
		return
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	_ = writeMarketplaces(managedDir, doc.Marketplaces)
}

// writeFileAtomic replaces dir/name with data in one rename, so a reader never
// sees a half-written file and a crash leaves the old one.
func writeFileAtomic(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, name+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
