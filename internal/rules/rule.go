package rules

import (
	"path/filepath"
	"strings"
)

// Source identifies which on-disk rules layout produced a rule.
type Source string

const (
	SourceCoddy  Source = "coddy"
	SourceCursor Source = "cursor"
	SourceClaude Source = "claude"
	SourceCodex  Source = "codex"
)

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
	Description string
	Globs       []string
	AlwaysApply bool
	ApplyMode   ApplyMode
	Content     string
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
func (r *Rule) DedupeKey() string {
	if r == nil {
		return ""
	}
	return strings.ToLower(filepath.Base(r.FilePath))
}
