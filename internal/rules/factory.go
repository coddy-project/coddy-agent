package rules

import (
	"path/filepath"
	"sort"
)

// Factory holds registered rule providers.
type Factory struct {
	providers []Provider
}

// DefaultFactory returns built-in providers in discover precedence order
// (later wins on dedupe): coddy's own folder, then the tool-neutral
// .agents/rules, then the other tools' folders, then nested AGENTS.md.
func DefaultFactory() *Factory {
	return NewFactory(
		&AgentsProvider{},
		NewMarkdownProvider(SourceCodex, ".codex/rules"),
		NewMarkdownProvider(SourceClaude, ".claude/rules"),
		NewMarkdownProvider(SourceCursor, ".cursor/rules"),
		NewMarkdownProvider(SourceAgentsDir, ".agents/rules"),
		NewMarkdownProvider(SourceCoddy, ".coddy/rules"),
	)
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
		return 6
	case SourceAgentsDir:
		return 5
	case SourceCursor:
		return 4
	case SourceClaude:
		return 3
	case SourceCodex:
		return 2
	case SourceAgents:
		return 1
	default:
		return 0
	}
}

// Discover loads rules from every provider whose root exists under cwd. Every
// rule is anchored at the absolute cwd (Rule.Root) so its globs can be
// matched against project-relative paths.
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
		root := filepath.Join(cwd, p.RulesRoot())
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
