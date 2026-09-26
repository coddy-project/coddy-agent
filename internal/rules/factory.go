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
// always contributes; the project folders form a chain, of which only the
// first that holds a rule file is read. Every coding agent keeps its rules in
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
	// Rules are the rules of the session, one per file name.
	Rules []*Rule
	// ProjectFolder is the project folder the rules came from, as the chain
	// names it (".cursor/rules"), or "" when no folder of the chain holds a
	// rule file.
	ProjectFolder string
	// Skipped are the project folders after ProjectFolder that hold rule
	// files too and were not read. Only Inspect looks at them.
	Skipped []string
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
// read and the folders further down the chain that hold rules and were not.
func (f *Factory) Inspect(cwd string, systems []Source) (*Discovery, error) {
	return f.discover(cwd, systems, true)
}

// discover runs one pass. A provider root is relative to cwd unless it is
// already absolute, which is how the operator's own folder outside the
// workspace joins the same pass. Every rule is anchored at the absolute cwd
// (Rule.Root) so its globs match project-relative paths whichever folder the
// file itself came from. A project rule wins over the operator's rule of the
// same file name.
func (f *Factory) discover(cwd string, systems []Source, inspect bool) (*Discovery, error) {
	allowAll := len(systems) == 0
	allowed := make(map[Source]bool, len(systems))
	for _, s := range systems {
		allowed[s] = true
	}
	admitted := func(p Provider) bool { return allowAll || allowed[p.ID()] }
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
	var loaded []*Rule
	for _, p := range f.chain {
		if !admitted(p) {
			continue
		}
		if d.ProjectFolder != "" {
			if !inspect {
				break
			}
			if holdsRuleFile(rootOf(p)) {
				d.Skipped = append(d.Skipped, p.RulesRoot())
			}
			continue
		}
		rs, err := p.Load(rootOf(p))
		if err != nil || len(rs) == 0 {
			continue
		}
		d.ProjectFolder = p.RulesRoot()
		loaded = append(loaded, rs...)
	}
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
		// that shares a file name with one of the project's is dropped, and
		// within one folder the first file of that name wins.
		if _, ok := byKey[key]; !ok {
			byKey[key] = r
		}
	}
	d.Rules = make([]*Rule, 0, len(byKey))
	for _, r := range byKey {
		d.Rules = append(d.Rules, r)
		if r.Source == SourceUser && d.UserFolder == "" {
			for _, p := range f.always {
				if p.ID() == SourceUser {
					d.UserFolder = p.RulesRoot()
				}
			}
		}
	}
	sort.Slice(d.Rules, func(i, j int) bool {
		if d.Rules[i].Source != d.Rules[j].Source {
			return d.Rules[i].Source < d.Rules[j].Source
		}
		return d.Rules[i].FilePath < d.Rules[j].FilePath
	})
	return d, nil
}

// errFound stops the walk of holdsRuleFile at the first rule file.
var errFound = errors.New("found")

// holdsRuleFile reports whether root is a directory with at least one .md or
// .mdc file anywhere under it.
func holdsRuleFile(root string) bool {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false
	}
	err = filepath.WalkDir(root, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() {
			if _, ok := FormatForPath(d.Name()); ok {
				return errFound
			}
		}
		return nil
	})
	return errors.Is(err, errFound)
}
