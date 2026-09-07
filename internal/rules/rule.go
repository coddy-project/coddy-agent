package rules

import (
	"path/filepath"
	"strings"
)

// Source identifies which on-disk rules layout produced a rule.
type Source string

const (
	SourceCoddy Source = "coddy"
	// SourceAgentsDir is the tool-neutral .agents/rules folder, the rules
	// sibling of .agents/skills.
	SourceAgentsDir Source = "agents-dir"
	SourceCursor    Source = "cursor"
	SourceClaude    Source = "claude"
	SourceCodex     Source = "codex"
	// SourceAgents is the nested **/AGENTS.md convention (https://agents.md/).
	SourceAgents Source = "agents"
)

// Format is the frontmatter dialect a rule file is read with. The file
// extension picks it, so the same description-only header is a manual rule
// as .mdc and an unconditional one as .md (see classifyRule).
type Format string

const (
	// FormatCursor is the .mdc dialect: description, globs (comma-separated or
	// a list) and alwaysApply, which Cursor defaults to false.
	FormatCursor Format = "cursor"
	// FormatClaude is the .md dialect of .claude/rules: paths (a list of globs)
	// and no alwaysApply; a rule without paths is loaded unconditionally.
	FormatClaude Format = "claude"
	// FormatAgentsMD marks a nested AGENTS.md, which carries no frontmatter.
	FormatAgentsMD Format = "agents.md"
)

// FormatForPath returns the dialect a rule file's extension selects and false
// for files that are not rules.
func FormatForPath(path string) (Format, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mdc":
		return FormatCursor, true
	case ".md":
		return FormatClaude, true
	}
	return "", false
}

// ApplyMode controls how a rule enters the prompt.
type ApplyMode string

const (
	// ApplyAuto: alwaysApply true; sticky after first glob match (or immediate if no globs).
	ApplyAuto ApplyMode = "auto"
	// ApplyMention: alwaysApply false; body only when @ruleName appears in user text.
	ApplyMention ApplyMode = "mention"
)

// Rule is a loaded project rule file.
type Rule struct {
	ID          string
	Name        string
	FilePath    string
	Source      Source
	Format      Format
	Description string
	Globs       []string
	// AlwaysApply reports whether the rule is an auto rule: Cursor's
	// alwaysApply: true, or its equivalents (patterns that gate the rule, a
	// Claude Code rule without paths, a file without frontmatter). It mirrors
	// ApplyMode == ApplyAuto rather than the literal frontmatter key; a rule
	// with Globs or a ScopeDir is auto but still waits for a matching path.
	AlwaysApply bool
	ApplyMode   ApplyMode
	Content     string
	// Root is the project directory the rule was discovered under. Globs are
	// anchored there: a context file is matched by its path relative to Root,
	// since tool calls and file:// attachments deliver absolute paths.
	Root string
	// ScopeDir, when non-empty, restricts an auto rule to a directory subtree.
	// The rule enters the prompt on the first turn a context path is ScopeDir
	// itself or lives under it, then sticks for the session (see UnionStable).
	// Set by AgentsProvider for nested AGENTS.md; empty for every other source.
	ScopeDir string
}

// CanonicalName is the @mention identifier (file stem).
func (r *Rule) CanonicalName() string {
	if r == nil {
		return ""
	}
	if strings.TrimSpace(r.Name) != "" {
		return r.Name
	}
	base := filepath.Base(r.FilePath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// DedupeKey returns a stable key for catalog deduplication (basename across sources).
// Nested AGENTS.md files all share a basename, so agents rules key on the full path.
func (r *Rule) DedupeKey() string {
	if r == nil {
		return ""
	}
	if r.Source == SourceAgents {
		return strings.ToLower(filepath.ToSlash(r.FilePath))
	}
	return strings.ToLower(filepath.Base(r.FilePath))
}
