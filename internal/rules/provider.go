package rules

// Provider loads rules from a well-known directory.
type Provider interface {
	ID() Source
	// RulesRoot is relative to the session CWD (e.g. ".cursor/rules"), or
	// absolute for a root outside the workspace such as ${CODDY_HOME}/rules.
	RulesRoot() string
	Load(root string) ([]*Rule, error)
}

// MarkdownProvider loads .md/.mdc files recursively from root.
type MarkdownProvider struct {
	source Source
	root   string
}

func NewMarkdownProvider(source Source, rootRel string) *MarkdownProvider {
	return &MarkdownProvider{source: source, root: rootRel}
}

func (p *MarkdownProvider) ID() Source { return p.source }

func (p *MarkdownProvider) RulesRoot() string { return p.root }

func (p *MarkdownProvider) Load(root string) ([]*Rule, error) {
	return loadMarkdownRulesFromRoot(root, p.source)
}
