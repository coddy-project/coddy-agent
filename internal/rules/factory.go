package rules

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Factory holds the rule providers of a session. The operator's own folder
// contributes in every workspace (unless rules.systems leaves it out); the
// project folders form a chain, of which only the first that holds a rule file
// is read. Every coding agent keeps its rules in
// a folder of its own, and a project that works with several of them keeps
// the same rules in each - .cursor/rules/workflow.mdc and its Claude Code
// mirror .claude/rules/workflow.md - so reading them all would hand the model
// every rule twice.
type Factory struct {
	// always are read in every session: the operator's own folder.
	always []Provider
	// chain are the project folders in the order they are looked at.
	chain []Provider
}

// UserRulesDir is the rules folder of the person running coddy, next to
// config.yaml inside CODDY_HOME. Empty home means no such folder: the caller
// resolved no agent home, and nothing outside the workspace is read.
func UserRulesDir(home string) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, "rules")
}

// DefaultFactory returns the built-in providers. The project chain is Coddy's
// own folder, then the tool-neutral .agents/rules written for every agent, then
// the folders of other agents: Cursor's first, since its .mdc dialect is the
// one Coddy's own rules are written in, then Claude Code's, then .codex/rules,
// where Codex itself keeps no prompt rules (its *.rules files are command
// policy). home is the resolved CODDY_HOME; "" leaves the operator's folder
// out. Nested AGENTS.md files have no provider: they are never walked, only
// read for the folders a tool enters (AgentsForPaths).
func DefaultFactory(home string) *Factory {
	f := &Factory{}
	if dir := UserRulesDir(home); dir != "" {
		f.always = append(f.always, NewMarkdownProvider(SourceUser, dir))
	}
	f.chain = []Provider{
		NewMarkdownProvider(SourceCoddy, ".coddy/rules"),
		NewMarkdownProvider(SourceAgentsDir, ".agents/rules"),
		NewMarkdownProvider(SourceCursor, ".cursor/rules"),
		NewMarkdownProvider(SourceClaude, ".claude/rules"),
		NewMarkdownProvider(SourceCodex, ".codex/rules"),
	}
	return f
}

// ProjectFolders lists the project folders of the chain in the order they are
// looked at.
func (f *Factory) ProjectFolders() []string {
	out := make([]string, 0, len(f.chain))
	for _, p := range f.chain {
		out = append(out, p.RulesRoot())
	}
	return out
}

// Discovery is what one discovery pass found.
type Discovery struct {
	// Rules are the rules of the session, one per path inside the rule folder.
	Rules []*Rule
	// ProjectFolder is the project folder the rules came from, as the chain
	// names it (".cursor/rules"), or "" when no folder of the chain holds a
	// rule file.
	ProjectFolder string
	// Chain is the chain this pass looked at: the project folders
	// rules.systems admits, in order.
	Chain []string
	// Skipped are the project folders after ProjectFolder that hold rule
	// files too and were not read. Only Inspect looks at them.
	Skipped []string
	// OnlySkipped are the rule files of the Skipped folders whose name the
	// folder read has no file for, relative to the workspace: not a mirror of
	// a rule that was read, but a rule the session goes without.
	OnlySkipped []string
	// Unreadable are the project folders that exist but could not be read,
	// each with the reason; the chain passed over them. Only Inspect reports
	// them.
	Unreadable []string
	// UserFolder is the operator's own folder when one of its rules is in
	// Rules.
	UserFolder string
}

// Discover loads the rules of a session: the operator's own folder and the
// first project folder of the chain that holds a rule file. systems, when
// not empty, admits only the providers it names; a folder it leaves out is
// not part of the chain.
func (f *Factory) Discover(cwd string, systems []Source) ([]*Rule, error) {
	d, err := f.discover(cwd, systems, false)
	if err != nil {
		return nil, err
	}
	return d.Rules, nil
}

// Inspect is Discover for a reader: it also names the project folder that was
// read, the folders further down the chain that hold rules and were not, and
// the folders that could not be read at all.
func (f *Factory) Inspect(cwd string, systems []Source) (*Discovery, error) {
	return f.discover(cwd, systems, true)
}

// discover runs one pass. A provider root is relative to cwd unless it is
// already absolute, which is how the operator's own folder outside the
// workspace joins the same pass. Every rule is anchored at the absolute cwd
// (Rule.Root) so its globs match project-relative paths whichever folder the
// file itself came from. A project rule wins over the operator's rule at the
// same path inside the rule folder, extension included (Rule.DedupeKey).
func (f *Factory) discover(cwd string, systems []Source, inspect bool) (*Discovery, error) {
	admitted := admittedBy(systems)
	projectRoot, err := filepath.Abs(cwd)
	if err != nil {
		projectRoot = cwd
	}
	rootOf := func(p Provider) string {
		root := p.RulesRoot()
		if !filepath.IsAbs(root) {
			root = filepath.Join(cwd, root)
		}
		return root
	}

	d := &Discovery{}
	loaded := f.readChain(cwd, admitted, inspect, d)
	for _, p := range f.always {
		if !admitted(p) {
			continue
		}
		rs, err := p.Load(rootOf(p))
		if err != nil {
			continue
		}
		loaded = append(loaded, rs...)
	}

	byKey := make(map[string]*Rule)
	for _, r := range loaded {
		key := r.DedupeKey()
		if key == "" {
			continue
		}
		if r.Root == "" {
			r.Root = projectRoot
		}
		// The project folder was loaded first, so a rule of the operator's
		// at the same path as one of the project's (Rule.DedupeKey) is
		// dropped, and within one folder the first file at that path wins.
		if _, ok := byKey[key]; !ok {
			byKey[key] = r
		}
	}
	d.Rules = make([]*Rule, 0, len(byKey))
	userListed := false
	for _, r := range byKey {
		d.Rules = append(d.Rules, r)
		userListed = userListed || r.Source == SourceUser
	}
	if userListed {
		for _, p := range f.always {
			if p.ID() == SourceUser {
				d.UserFolder = p.RulesRoot()
			}
		}
	}
	sort.Strings(d.OnlySkipped)
	sort.Slice(d.Rules, func(i, j int) bool {
		if d.Rules[i].Source != d.Rules[j].Source {
			return d.Rules[i].Source < d.Rules[j].Source
		}
		return d.Rules[i].FilePath < d.Rules[j].FilePath
	})
	return d, nil
}

// readChain reads the project folders of the chain under dir: the first that
// holds a rule file, and with inspect the ones after it as well, which only
// fill d. The workspace and every subfolder that Nested reads choose their
// folder here, so a subproject's mirror of its rules for another agent is
// skipped the way the workspace's is.
func (f *Factory) readChain(dir string, admitted func(Provider) bool, inspect bool, d *Discovery) []*Rule {
	projectRoot, err := filepath.Abs(dir)
	if err != nil {
		projectRoot = dir
	}
	var loaded []*Rule
	read := map[string]bool{}
	for _, p := range f.chain {
		if !admitted(p) {
			continue
		}
		d.Chain = append(d.Chain, p.RulesRoot())
		if d.ProjectFolder != "" && !inspect {
			continue
		}
		// A folder further down the chain is judged the way the one that was
		// read was chosen: it counts when it yields a rule.
		root := p.RulesRoot()
		if !filepath.IsAbs(root) {
			root = filepath.Join(dir, root)
		}
		rs, err := p.Load(root)
		if err != nil {
			if inspect && !errors.Is(err, fs.ErrNotExist) {
				d.Unreadable = append(d.Unreadable, p.RulesRoot()+" ("+err.Error()+")")
			}
			continue
		}
		if len(rs) == 0 {
			continue
		}
		if d.ProjectFolder != "" {
			d.Skipped = append(d.Skipped, p.RulesRoot())
			for _, r := range rs {
				if !read[strings.ToLower(r.CanonicalName())] {
					d.OnlySkipped = append(d.OnlySkipped, workspaceRel(projectRoot, r.FilePath))
				}
			}
			continue
		}
		d.ProjectFolder = p.RulesRoot()
		for _, r := range rs {
			read[strings.ToLower(r.CanonicalName())] = true
		}
		loaded = append(loaded, rs...)
	}
	return loaded
}

// Nested returns the rules of the rule folders of the subfolders on the way
// from root down to paths, read on the spot (issue #557). For every path under
// root, each directory on the chain from root (exclusive) down to the path's
// own directory is probed for the same chain of project folders as the
// workspace (readChain), the first that holds a rule file winning, with the
// stops of the AGENTS.md chain (agentsChainDirs): a hidden directory,
// node_modules or vendor ends it. Nothing is walked, so a subfolder no tool has
// reached costs nothing. A rule of a subfolder is anchored there: Root and
// ScopeDir are its folder, so its globs match paths relative to it and it never
// applies above or beside it, nor reaches the system prompt (Rule.AlwaysOn).
// systems narrows the chain as it does in Discover; the operator's own folder
// is the catalog's and is never read here. Within one folder the first file of
// a name wins, as at the root; rules of different folders never replace each
// other, whatever their names.
func (f *Factory) Nested(root string, paths []string, systems []Source) []*Rule {
	root = cleanRoot(root)
	if root == "" || len(paths) == 0 {
		return nil
	}
	admitted := admittedBy(systems)
	seen := map[string]bool{}
	var out []*Rule
	for _, p := range paths {
		for _, dir := range withoutRepeats(root, agentsChainDirs(root, p)) {
			if seen[dir] {
				continue
			}
			seen[dir] = true
			out = append(out, f.readFolder(dir, admitted)...)
		}
	}
	return out
}

// Folder returns the rules of the rule folder of one subfolder of root, read
// on the spot the way Nested reads every folder on its chain: what
// "@rule:<folder>/<name>" reads, with systems narrowed by the caller (the
// session reads Coddy's own folder only, SourceCoddy). dir must be a folder
// below root, root itself excluded (its rule folder is the catalog's,
// Discover), reached without a segment the chain never enters (a hidden
// folder, node_modules, vendor). Each rule is anchored at dir.
func (f *Factory) Folder(root, dir string, systems []Source) []*Rule {
	root = cleanRoot(root)
	dir, ok := subfolderOf(root, dir)
	if !ok {
		return nil
	}
	return f.readFolder(dir, admittedBy(systems))
}

// Bounds of one FoldersWithRules call: the entries of the folder it reads,
// and the children it looks into (a link resolved, a rule folder stat-ed),
// charged before the look.
const (
	maxFolderEntries = 4096
	maxFolderProbes  = 256
)

// FoldersWithRules returns the names of the subfolders of dir - root, or a
// folder Folder accepts - that keep a folder of the chain systems admits, at
// most limit: what the completion of "@rule:" offers to look into. keep, when set, decides which names are looked at at all (the
// part of a query typed after the last "/", the names a reference can spell),
// so a folder far down a long listing is still found by its name. It looks
// one level down, at no more than maxFolderEntries entries and
// maxFolderProbes children, and at the rule folders' existence, never their
// files, so each step down a group of projects costs the folder it shows. A
// hidden folder, node_modules, vendor and a link that leaves root are never
// offered.
func (f *Factory) FoldersWithRules(root, dir string, systems []Source, keep func(name string) bool, limit int) []string {
	root = cleanRoot(root)
	if root == "" || limit <= 0 {
		return nil
	}
	target := strings.TrimSpace(dir)
	if target != "" && !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	if target == "" || SamePath(target, root) {
		dir = root
	} else {
		var ok bool
		if dir, ok = subfolderOf(root, target); !ok {
			return nil
		}
	}
	// The folders from root down to dir, so a child that is a link back to
	// one of them is not offered.
	var above []os.FileInfo
	for _, a := range append([]string{root}, agentsChainDirs(root, dir)...) {
		if info, err := os.Stat(a); err == nil {
			above = append(above, info)
		}
	}
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer func() { _ = d.Close() }()
	// The names first, up to maxFolderEntries, in lexical order, so the
	// children looked into within the budget are the same on every call.
	var names []string
	isLink := map[string]bool{}
	for read := 0; read < maxFolderEntries; {
		entries, err := d.ReadDir(256)
		for _, e := range entries {
			read++
			if read > maxFolderEntries {
				break
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") || agentsSkipDirs[name] || (keep != nil && !keep(name)) {
				continue
			}
			link := e.Type()&os.ModeSymlink != 0
			if !e.IsDir() && !link {
				continue
			}
			names = append(names, name)
			isLink[name] = link
		}
		if err != nil {
			break
		}
	}
	sort.Strings(names)
	if len(names) > maxFolderProbes {
		names = names[:maxFolderProbes]
	}
	admitted := admittedBy(systems)
	var out []string
	for _, name := range names {
		child := filepath.Join(dir, name)
		if isLink[name] {
			info, err := os.Stat(child)
			if err != nil || !info.IsDir() {
				continue
			}
			back := false
			for _, a := range above {
				back = back || os.SameFile(a, info)
			}
			if back {
				continue
			}
		}
		for _, p := range f.chain {
			if !admitted(p) || filepath.IsAbs(p.RulesRoot()) {
				continue
			}
			if info, err := os.Stat(filepath.Join(child, p.RulesRoot())); err == nil && info.IsDir() {
				out = append(out, name)
				break
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// readFolder reads the chain of project folders under one folder of the
// workspace into rules anchored there: within the folder the first file of a
// path wins, as at the root. Links are followed as they are in the
// workspace's own rule folder.
func (f *Factory) readFolder(dir string, admitted func(Provider) bool) []*Rule {
	names := map[string]bool{}
	var out []*Rule
	for _, r := range f.readChain(dir, admitted, false, &Discovery{}) {
		key := r.DedupeKey()
		if names[key] {
			continue
		}
		names[key] = true
		r.Root, r.ScopeDir = dir, dir
		out = append(out, r)
	}
	return out
}

// cleanRoot is the absolute, clean form of a workspace root, "" for none.
func cleanRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return filepath.Clean(root)
}

// subfolderOf resolves dir (relative to root, or absolute) and reports
// whether it is an existing folder below root that the chain of Nested
// reaches: the last folder of the chain to it is dir itself. The chain is
// lexical, and links are followed as they are in the workspace's own rule
// folder.
func subfolderOf(root, dir string) (string, bool) {
	dir = strings.TrimSpace(dir)
	if root == "" || dir == "" {
		return "", false
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	dir = filepath.Clean(dir)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false
	}
	chain := agentsChainDirs(root, dir)
	if len(chain) == 0 || !SamePath(chain[len(chain)-1], dir) || len(withoutRepeats(root, chain)) != len(chain) {
		return "", false
	}
	return dir, true
}

// withoutRepeats drops from chain - folders on the way down from root - each
// one that is root or a folder before it on the chain: a link back up the
// tree (self -> ., db/again -> db) leads into a folder the chain already
// holds, whose rules would arrive a second time under another path. The
// folders below such a link stay, since they are real folders of their own
// (alias/pkg is pkg), and every other link is followed.
func withoutRepeats(root string, chain []string) []string {
	if len(chain) == 0 {
		return chain
	}
	seen := make([]os.FileInfo, 0, len(chain)+1)
	if info, err := os.Stat(root); err == nil {
		seen = append(seen, info)
	}
	out := chain[:0:0]
	for _, dir := range chain {
		info, err := os.Stat(dir)
		if err != nil {
			// A folder that does not exist yet (a file about to be written)
			// repeats nothing.
			out = append(out, dir)
			continue
		}
		repeat := false
		for _, s := range seen {
			repeat = repeat || os.SameFile(s, info)
		}
		if repeat {
			continue
		}
		seen = append(seen, info)
		out = append(out, dir)
	}
	return out
}

// admittedBy is the filter of rules.systems: every provider when systems is
// empty, else the ones it names.
func admittedBy(systems []Source) func(Provider) bool {
	if len(systems) == 0 {
		return func(Provider) bool { return true }
	}
	allowed := make(map[Source]bool, len(systems))
	for _, s := range systems {
		allowed[s] = true
	}
	return func(p Provider) bool { return allowed[p.ID()] }
}

// workspaceRel names path relative to root, slash-separated, or as it is when
// it lies outside.
func workspaceRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
