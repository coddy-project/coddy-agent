package skills

// Reading the lists and updating what is installed. Two steps, as in Claude
// Code: `plugin marketplace update` reads the list of every marketplace and
// source again (RefreshIndex) and reports the installed skills a newer version
// is published for, installing and updating nothing; `plugin update`
// (UpdateSkills) installs those newer versions. Neither ever installs a skill
// that is not installed: a skill the operator removed stays removed, the
// skills of the standard delivery included, and so does the other skill of a
// plugin whose one skill was removed.
//
// The skills of the standard delivery count as installed from the system
// catalogue they are published in, under their own names, for as long as no
// install has replaced them: the delivery writes them out of the binary and
// records them in .bundled.json, not in .remote.json.

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// UpdateStatus reports whether a newer version of an installed skill is
// published by the marketplace or source it was installed from.
type UpdateStatus struct {
	Name            string `json:"name"`
	Source          string `json:"source"`
	Version         string `json:"version"` // installed
	Latest          string `json:"latest"`  // latest declared upstream
	UpdateAvailable bool   `json:"update_available"`

	// known says the source was read and publishes a version for the skill,
	// so Latest is what it publishes rather than the installed version.
	known bool
}

// IndexResult is what reading the lists of the marketplaces and sources in
// effect found.
type IndexResult struct {
	// Read names what was read: a marketplace by its name, a source by its
	// address.
	Read []string `json:"read"`
	// Updates are the installed skills a newer version is published for.
	Updates []UpdateStatus `json:"updates"`
	Failed  []SyncFailure  `json:"failed"`
	// Held are the project entries the workspace trust gate kept out of the
	// read: approve one, then read again.
	Held []Declaration `json:"held,omitempty"`
	// Marketplace is the marketplace RefreshIndex was asked to read, with the
	// list it just read; nil for a source and for every entry at once.
	Marketplace *AddedMarketplace `json:"-"`
}

// RefreshIndex reads again the list of the marketplace or source key names
// (a marketplace by name, either kind by source), or of every marketplace and
// source in effect for cwd when key is empty, records the list of each
// marketplace it read, and reports the installed skills a newer version is
// published for. It installs and updates nothing. A project entry the trust
// gate holds back is not read: it is reported in Held, or refused when key
// names it. A list that cannot be read is reported in Failed.
func RefreshIndex(ctx context.Context, cfg *config.Config, cwd, key string) (*IndexResult, error) {
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	decls, errs := Declarations(cfg, cwd)
	res := &IndexResult{}
	key = strings.TrimSpace(key)
	var targets []Declaration
	if key == "" {
		for _, err := range errs {
			res.Failed = append(res.Failed, SyncFailure{Source: "marketplaces.json", Error: err.Error()})
		}
		res.Held = append(res.Held, held(decls)...)
		for _, d := range inEffect(decls) {
			i := indexDeclaredSource(targets, d.Source)
			switch {
			case i < 0:
				targets = append(targets, d)
			case d.Kind == KindMarketplace && targets[i].Kind != KindMarketplace:
				// One read serves both; the marketplace keeps its list.
				targets[i] = d
			}
		}
	} else {
		d, ok := declarationInEffect(decls, key)
		if !ok {
			if d, found := FindDeclaration(cfg, cwd, key); found && d.State != StateReady {
				return nil, heldError(d)
			}
			return nil, fmt.Errorf("%q is neither a marketplace nor a source in effect; `plugin marketplace list` shows them, and `plugin install %s` installs a source that is not added", key, key)
		}
		targets = []Declaration{d}
	}

	// A list cache that does not parse is reported and left as it is, so
	// nothing writes over what it holds.
	_, cacheErr := readMarketplaces(cfg)
	if cacheErr != nil {
		res.Failed = append(res.Failed, SyncFailure{Source: marketplacesFile, Error: cacheErr.Error()})
	}
	indexes := &sourceIndexes{managedDir: managedDir}
	defer indexes.close()
	for _, d := range targets {
		idx, err := indexes.read(ctx, d.Source)
		if err != nil {
			res.Failed = append(res.Failed, SyncFailure{Source: d.Key(), Error: err.Error()})
			continue
		}
		res.Read = append(res.Read, d.Key())
		if d.Kind != KindMarketplace || idx.mf == nil {
			continue
		}
		m := AddedMarketplace{Name: d.Name, Source: d.Source, Plugins: listingOf(idx.mf), UpdatedAt: nowStamp(), Origin: d.Origin}
		if cacheErr == nil {
			if err := cacheListing(cfg, m); err != nil {
				res.Failed = append(res.Failed, SyncFailure{Source: marketplacesFile, Error: err.Error()})
			}
		}
		if key != "" {
			res.Marketplace = &m
		}
	}

	entries := installedEntries(managedDir)
	if key != "" {
		for name, ent := range entries {
			if !sameSource(ent.Source, targets[0].Source) {
				delete(entries, name)
			}
		}
	}
	for _, st := range checkEntries(ctx, decls, entries, indexes) {
		if st.UpdateAvailable {
			res.Updates = append(res.Updates, st)
		}
	}
	return res, nil
}

// CheckUpdates reports, per installed skill (see installedEntries), whether a
// newer version is published by the source it was installed from. It reads
// those sources but never modifies installed skills. Sources that cannot be
// reached are treated as "no update" rather than failing the whole check, and
// a source a project of cwd declares and the trust gate holds back is not
// contacted at all: it reports no update.
func CheckUpdates(ctx context.Context, cfg *config.Config, cwd string) ([]UpdateStatus, error) {
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	entries := installedEntries(managedDir)
	if len(entries) == 0 {
		return nil, nil
	}
	decls, _ := Declarations(cfg, cwd)
	indexes := &sourceIndexes{managedDir: managedDir}
	defer indexes.close()
	return checkEntries(ctx, decls, entries, indexes), nil
}

// UpdateSkills installs the newer versions of installed skills: of every one a
// newer version is published for when name is empty, else of the skill or the
// plugin name names, which it reinstalls unless what is installed is known to
// be the newest version already. Only installed skills are written: one the
// operator removed is not brought back, not even as part of a plugin whose
// other skill is updated, and a copy newer than what is published is left as
// it is. A named skill from a source a project of cwd declares that the trust
// gate holds back is refused with how to approve it; without a name such
// skills are skipped.
func UpdateSkills(ctx context.Context, cfg *config.Config, cwd, name string) (*SyncResult, error) {
	name = strings.TrimSpace(name)
	syncMu.Lock()
	defer syncMu.Unlock()
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	entries := installedEntries(managedDir)
	decls, _ := Declarations(cfg, cwd)
	if name != "" {
		entries = entriesNamed(entries, name)
		if len(entries) == 0 {
			return nil, fmt.Errorf("%q is not a skill installed from a marketplace or a source, so there is nothing to update it from; `plugin list` names the installed skills and where each comes from", name)
		}
		for _, ent := range entries {
			if d, isHeld := heldSource(decls, ent.Source); isHeld {
				return nil, heldError(d)
			}
		}
	}

	indexes := &sourceIndexes{managedDir: managedDir}
	defer indexes.close()
	res := &SyncResult{}
	due := map[string]RemoteEntry{}
	for _, st := range checkEntries(ctx, decls, entries, indexes) {
		switch {
		case st.UpdateAvailable:
			due[st.Name] = entries[st.Name]
		case name == "":
		case st.known && strings.TrimSpace(st.Version) != "":
			// Asked for by name and already the newest, or newer than what
			// is published: an edit kept by raising its version stays.
			res.Current = append(res.Current, st.Name)
		default:
			// Asked for by name, and nothing says which version is newest.
			due[st.Name] = entries[st.Name]
		}
	}
	if len(due) == 0 {
		return res, nil
	}

	lock := readRemoteLock(managedDir)
	for _, g := range groupForUpdate(due) {
		if err := updatePluginGroup(ctx, g, indexes, managedDir, lock, res); err != nil {
			res.Failed = append(res.Failed, SyncFailure{Source: g.label(), Error: err.Error()})
		}
	}
	if err := writeRemoteLock(managedDir, lock); err != nil {
		return res, fmt.Errorf("write lock: %w", err)
	}
	return res, nil
}

// InstallSource installs every plugin source publishes, as it publishes them
// now (every skill of a repository without a marketplace.json): the explicit
// install of a source, which nothing else does. A source no file of cwd
// declares in effect is declared in the operator's marketplaces.json first;
// declared reports whether this call did that.
func InstallSource(ctx context.Context, cfg *config.Config, cwd, source string) (res *SyncResult, declared bool, err error) {
	source = strings.TrimSpace(source)
	if _, err := parseSource(source); err != nil {
		return nil, false, err
	}
	decls, _ := Declarations(cfg, cwd)
	if _, ok := declarationInEffect(decls, source); !ok {
		if declared, err = AddSource(cfg, cwd, source, ScopeGlobal); err != nil {
			return nil, false, err
		}
	}
	res, err = SyncSource(ctx, cfg, source)
	return res, declared, err
}

// installedEntries lists by name the installed skills an update may touch:
// every skill the lock records as installed from a marketplace or a source,
// and every skill of the standard delivery still on disk that no install has
// replaced, as installed from the system catalogue under its own name, at the
// version its SKILL.md declares.
func installedEntries(managedDir string) map[string]RemoteEntry {
	out := readRemoteLock(managedDir)
	if len(SystemSources) == 0 {
		return out
	}
	receipt, err := readDeliveryReceipt(managedDir)
	if err != nil {
		return out
	}
	for name := range receipt.Skills {
		if _, ok := out[name]; ok {
			continue
		}
		on := inspectInstalledSkill(filepath.Join(managedDir, name))
		if !on.present || !on.readable {
			// Deleted by the operator, or not Coddy's to judge.
			continue
		}
		out[name] = RemoteEntry{Source: SystemSources[0].Source, Plugin: name, Version: on.version}
	}
	return out
}

// entriesNamed keeps the entries a name the operator typed stands for: the
// skill of that name, or every installed skill of the plugin of that name.
func entriesNamed(entries map[string]RemoteEntry, name string) map[string]RemoteEntry {
	out := map[string]RemoteEntry{}
	for n, ent := range entries {
		if strings.EqualFold(n, name) || strings.EqualFold(strings.TrimSpace(ent.Plugin), name) {
			out[n] = ent
		}
	}
	return out
}

// checkEntries reports, per entry and sorted by name, whether its source now
// publishes a newer version. A source that cannot be read reports no update,
// and one a project declares that the trust gate holds back is not contacted.
func checkEntries(ctx context.Context, decls []Declaration, entries map[string]RemoteEntry, indexes *sourceIndexes) []UpdateStatus {
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]UpdateStatus, 0, len(names))
	for _, name := range names {
		ent := entries[name]
		st := UpdateStatus{Name: name, Source: ent.Source, Version: ent.Version, Latest: ent.Version}
		if _, isHeld := heldSource(decls, ent.Source); isHeld {
			out = append(out, st)
			continue
		}
		key := strings.TrimSpace(ent.Plugin)
		if key == "" {
			key = name
		}
		if idx, err := indexes.read(ctx, ent.Source); err == nil {
			if latest := strings.TrimSpace(idx.versions[key]); latest != "" {
				st.Latest, st.known = latest, true
				st.UpdateAvailable = isUpdate(latest, ent.Version)
			}
		}
		out = append(out, st)
	}
	return out
}

// sourceIndex is what one source published when it was read.
type sourceIndex struct {
	*openedMarketplace
	versions map[string]string
}

// sourceIndexes reads each source once per call, whatever spelling of it the
// entries carry, and remembers a source that could not be read too. close
// removes the clones the update read from.
type sourceIndexes struct {
	managedDir string
	reads      []sourceIndexRead
}

type sourceIndexRead struct {
	source string
	idx    *sourceIndex
	err    error
}

func (x *sourceIndexes) read(ctx context.Context, source string) (*sourceIndex, error) {
	for _, r := range x.reads {
		if sameSource(r.source, source) {
			return r.idx, r.err
		}
	}
	var idx *sourceIndex
	om, err := openSource(ctx, source, x.managedDir)
	if err == nil {
		idx = &sourceIndex{openedMarketplace: om, versions: om.versions()}
	}
	x.reads = append(x.reads, sourceIndexRead{source: source, idx: idx, err: err})
	return idx, err
}

func (x *sourceIndexes) close() {
	for _, r := range x.reads {
		if r.idx != nil {
			r.idx.close()
		}
	}
}

// pluginGroup is one plugin of one source, or the skills of a source without
// a marketplace.json (plugin "").
type pluginGroup struct {
	source string
	plugin string
	skills []string // the installed skills to rewrite, lower case
}

func (g pluginGroup) label() string {
	if g.plugin != "" {
		return g.plugin
	}
	return g.source
}

// groupForUpdate gathers the entries due an update by the plugin they come
// from, in a stable order.
func groupForUpdate(due map[string]RemoteEntry) []pluginGroup {
	var out []pluginGroup
	names := make([]string, 0, len(due))
	for n := range due {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ent := due[n]
		plugin := strings.TrimSpace(ent.Plugin)
		i := 0
		for ; i < len(out); i++ {
			if sameSource(out[i].source, ent.Source) && strings.EqualFold(out[i].plugin, plugin) {
				break
			}
		}
		if i == len(out) {
			out = append(out, pluginGroup{source: ent.Source, plugin: plugin})
		}
		out[i].skills = append(out[i].skills, strings.ToLower(n))
	}
	return out
}

// updatePluginGroup reinstalls the installed skills of g from what its source
// publishes now. Callers hold syncMu and write the lock afterwards.
func updatePluginGroup(ctx context.Context, g pluginGroup, indexes *sourceIndexes, managedDir string, lock map[string]RemoteEntry, res *SyncResult) error {
	idx, err := indexes.read(ctx, g.source)
	if err != nil {
		return err
	}
	only := map[string]bool{}
	for _, n := range g.skills {
		only[n] = true
	}
	if g.plugin == "" {
		if idx.repoRoot == "" {
			return fmt.Errorf("%s publishes no skills to update from", g.source)
		}
		return installFromDir(idx.repoRoot, idx.base, managedDir, lock, res, only)
	}
	if idx.mf == nil {
		return fmt.Errorf("%s no longer publishes a marketplace.json; its skills stay installed", g.source)
	}
	p := idx.mf.plugin(g.plugin)
	if p == nil {
		if _, ok := systemMarketplace(g.source); ok {
			return fmt.Errorf("plugin %q is not in %s; a Coddy update brings its new versions", g.plugin, g.source)
		}
		return fmt.Errorf("plugin %q is no longer in %s; its skills stay installed", g.plugin, g.source)
	}
	if err := installPlugin(ctx, *p, idx.repoRoot, g.source, idx.base, managedDir, lock, res, only); err != nil {
		return fmt.Errorf("plugin %q: %w", g.plugin, err)
	}
	return nil
}

// declarationInEffect finds the entry in effect key names: a marketplace by
// name, either kind by source, a marketplace first when a source of the same
// address is declared too.
func declarationInEffect(decls []Declaration, key string) (Declaration, bool) {
	var found *Declaration
	for _, d := range inEffect(decls) {
		if !keyNames(key, d.Kind, d.Name, d.Source) {
			continue
		}
		if d.Kind == KindMarketplace {
			return d, true
		}
		if found == nil {
			found = &d
		}
	}
	if found == nil {
		return Declaration{}, false
	}
	return *found, true
}

// indexDeclaredSource finds the entry of decls reading from source, or -1.
func indexDeclaredSource(decls []Declaration, source string) int {
	for i, d := range decls {
		if sameSource(d.Source, source) {
			return i
		}
	}
	return -1
}
