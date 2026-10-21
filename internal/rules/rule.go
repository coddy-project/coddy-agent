package rules

import (
	"path/filepath"
	"strings"
)

// Source identifies which on-disk rules layout produced a rule.
type Source string

const (
	// SourceUser is ${CODDY_HOME}/rules: the rules of the person running
	// coddy, applied in every workspace rather than travelling with a
	// checkout. The most specific copy still wins, so a project file of the
	// same name overrides one of these.
	SourceUser  Source = "user"
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
	// ApplyAuto: alwaysApply true; in the system prompt when no glob or
	// directory gates it, otherwise attached with the first tool call or
	// mention of a matching path.
	ApplyAuto ApplyMode = "auto"
	// ApplyMention: alwaysApply false; body only when @ruleName appears in user text.
	ApplyMention ApplyMode = "mention"
)

// Rule is a loaded project rule file.
type Rule struct {
	ID string
	// Name is how the rule is named: its file's path inside the rule folder
	// it was read from, slash-separated and without the extension
	// ("frontend/style" for .coddy/rules/frontend/style.mdc, "style" at the
	// top of the folder). It is what @rule: and a bare @name answer to.
	Name     string
	FilePath string
	// RelPath is the file's path inside its rule folder, slash-separated,
	// extension included ("frontend/style.mdc"); "" for a rule not read from
	// a rule folder (a nested AGENTS.md). Rules of two folders that share it
	// are the same rule kept twice, and the first one read wins.
	RelPath     string
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
	// The rule reaches the model with the first tool call or mention whose
	// path is ScopeDir itself or lives under it (and matches its globs, when
	// it has any), and again after a compaction or a change to its file. Set
	// for what is read on demand for the folders a tool enters: nested
	// AGENTS.md files (AgentsForPaths) and the rule folders of subfolders
	// (Factory.Nested); empty for the rules of the workspace's own folders.
	ScopeDir string
}

// AlwaysOn reports whether r applies from the first turn of a session,
// whatever it touches: an auto rule that no pattern and no directory scope
// gates. Only these rules are carried by the system prompt; every other rule
// waits for a path or a mention and arrives in the message that brought it.
func (r *Rule) AlwaysOn() bool {
	return r != nil && r.ApplyMode == ApplyAuto && r.AlwaysApply && len(r.Globs) == 0 && r.ScopeDir == ""
}

// CanonicalName is the @mention identifier: Name, or the file stem when the
// rule has none.
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

// DedupeKey returns a stable key for catalog deduplication: the file's path
// inside its rule folder (RelPath), so the project's rule and the operator's
// rule at the same path are one rule, while files of one name in different
// folders of a rule folder are different rules. Nested AGENTS.md files all
// share a basename, so agents rules key on the full path.
func (r *Rule) DedupeKey() string {
	if r == nil {
		return ""
	}
	if r.Source == SourceAgents {
		return strings.ToLower(filepath.ToSlash(r.FilePath))
	}
	if r.RelPath != "" {
		return strings.ToLower(r.RelPath)
	}
	return strings.ToLower(filepath.Base(r.FilePath))
}

// BaseName is the last segment of the rule's name: "style" for
// "frontend/style".
func (r *Rule) BaseName() string {
	name := r.CanonicalName()
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}
