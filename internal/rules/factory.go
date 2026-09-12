package rules

import (
	"path/filepath"
	"sort"
	"strings"
)

// Factory holds registered rule providers.
type Factory struct {
	providers []Provider
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

// DefaultFactory returns built-in providers in discover precedence order
// (later wins on dedupe): the operator's own ${CODDY_HOME}/rules first, since
// the more specific project copy of a file should win, then coddy's own
// folder, then the tool-neutral .agents/rules, then the other tools' folders.
// home is the resolved CODDY_HOME; "" leaves the user root out. Nested
// AGENTS.md files have no provider: they are never walked, only read for the
// folders a tool enters (AgentsForPaths).
func DefaultFactory(home string) *Factory {
	var providers []Provider
	// Registered first, so the slice order and sourceRank agree: whatever the
	// project ships beats the operator's copy of the same file name.
	if dir := UserRulesDir(home); dir != "" {
		providers = append(providers, NewMarkdownProvider(SourceUser, dir))
	}
	return NewFactory(append(providers,
		NewMarkdownProvider(SourceCodex, ".codex/rules"),
		NewMarkdownProvider(SourceClaude, ".claude/rules"),
		NewMarkdownProvider(SourceCursor, ".cursor/rules"),
		NewMarkdownProvider(SourceAgentsDir, ".agents/rules"),
		NewMarkdownProvider(SourceCoddy, ".coddy/rules"),
	)...)
}

// NewFactory creates a factory with the given providers (later providers win dedupe by basename).
func NewFactory(providers ...Provider) *Factory {
	return &Factory{providers: append([]Provider(nil), providers...)}
}

// Register adds a provider (appended last for dedupe precedence).
func (f *Factory) Register(p Provider) {
	if p == nil {
		return
	}
	f.providers = append(f.providers, p)
}

// Providers returns registered providers in order.
func (f *Factory) Providers() []Provider {
	return append([]Provider(nil), f.providers...)
}

// sourceRank for dedupe: higher wins.
func sourceRank(s Source) int {
	switch s {
	case SourceCoddy:
		return 7
	case SourceAgentsDir:
		return 6
	case SourceCursor:
		return 5
	case SourceClaude:
		return 4
	case SourceCodex:
		return 3
	case SourceAgents:
		return 2
	case SourceUser:
		return 1
	default:
		return 0
	}
}

// Discover loads rules from every provider whose root exists. A provider root
// is relative to cwd unless it is already absolute, which is how the operator's
// own folder outside the workspace joins the same pass. Every rule is anchored
// at the absolute cwd (Rule.Root) so its globs match project-relative paths
// whichever folder the file itself came from.
func (f *Factory) Discover(cwd string, systems []Source) ([]*Rule, error) {
	allowAll := len(systems) == 0
	allowed := make(map[Source]bool, len(systems))
	for _, s := range systems {
		allowed[s] = true
	}
	projectRoot, err := filepath.Abs(cwd)
	if err != nil {
		projectRoot = cwd
	}

	byKey := make(map[string]*Rule)
	for _, p := range f.providers {
		if !allowAll && !allowed[p.ID()] {
			continue
		}
		root := p.RulesRoot()
		if !filepath.IsAbs(root) {
			root = filepath.Join(cwd, root)
		}
		loaded, err := p.Load(root)
		if err != nil {
			continue
		}
		for _, r := range loaded {
			key := r.DedupeKey()
			if key == "" {
				continue
			}
			if r.Root == "" {
				r.Root = projectRoot
			}
			prev, ok := byKey[key]
			if !ok || sourceRank(r.Source) > sourceRank(prev.Source) {
				byKey[key] = r
			}
		}
	}
	out := make([]*Rule, 0, len(byKey))
	for _, r := range byKey {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].FilePath < out[j].FilePath
	})
	return out, nil
}
