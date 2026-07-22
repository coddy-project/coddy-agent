package skills

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// coddySkillPickRE matches the slash picker insertion form (parity with SPA Composer).
// Name in the label and href must match (checked in collect block; RE2 has no backrefs).
var coddySkillPickRE = regexp.MustCompile(`\[\/([a-zA-Z0-9][a-zA-Z0-9_-]*)\]\(coddy-skill:([a-zA-Z0-9][a-zA-Z0-9_-]*)\)`)

// invokedMidLineSlashRE finds /names after line start or ASCII whitespace outside stripped pick spans.
var invokedMidLineSlashRE = regexp.MustCompile(`(?:^|[\t ])\/([a-zA-Z0-9][a-zA-Z0-9_-]*)`)

// SkillSummary is a slash command listing row (ACP / HTTP catalog).
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// BuiltinCommands returns the deterministic built-in slash commands (they run
// without an LLM turn, unlike skills). Shared by the ACP available-commands
// update and the HTTP /coddy/commands endpoint so both surfaces stay in sync.
// compact is included only when compaction is enabled; plugin is always present.
func BuiltinCommands(compactionEnabled bool) []SkillSummary {
	cmds := make([]SkillSummary, 0, 2)
	if compactionEnabled {
		cmds = append(cmds, SkillSummary{
			Name:        "compact",
			Description: "Summarize older conversation history to free context; recent turns stay verbatim",
		})
	}
	cmds = append(cmds, SkillSummary{
		Name:        "plugin",
		Description: "Manage skill plugins and marketplaces: marketplace list|add|remove|sync, install, remove, enable, disable",
	})
	return cmds
}

// CanonicalCommandName returns the slash command identifier for a loaded skill folder/file.
// Subdirectory layout .../foo/SKILL.md yields "foo". Root *.md yields the file stem.
func CanonicalCommandName(s *Skill) string {
	base := filepath.Base(s.FilePath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if strings.EqualFold(stem, "SKILL") {
		return filepath.Base(filepath.Dir(s.FilePath))
	}
	return stem
}

// ListSkills builds a deduplicated, alphabetically sorted catalog from loaded skill files.
// First occurrence of each canonical name wins (matches skills.dirs order from LoadAll).
func ListSkills(loaded []*Skill) []SkillSummary {
	byName := make(map[string]*Skill)
	for _, sk := range loaded {
		name := CanonicalCommandName(sk)
		if name == "" {
			continue
		}
		if _, ok := byName[name]; ok {
			continue
		}
		byName[name] = sk
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]SkillSummary, 0, len(names))
	for _, n := range names {
		sk := byName[n]
		out = append(out, SkillSummary{Name: n, Description: summaryDescriptionLine(sk)})
	}
	return out
}

func summaryDescriptionLine(s *Skill) string {
	if d := strings.TrimSpace(s.Description); d != "" {
		return d
	}
	return FirstMarkdownBlurb(s.Content)
}

// FirstMarkdownBlurb returns the first non-empty plaintext line suitable for picker descriptions.
func FirstMarkdownBlurb(content string) string {
	lines := strings.Split(content, "\n")
	inFence := false
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		lineTrim := strings.TrimSpace(line)
		if strings.HasPrefix(lineTrim, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if lineTrim == "" || lineTrim == "---" {
			continue
		}
		if strings.HasPrefix(lineTrim, "#") {
			continue
		}
		if len(lineTrim) > 160 {
			return lineTrim[:157] + "..."
		}
		return lineTrim
	}
	return "(no description)"
}

// BuildSlashCatalogMarkdown renders the slash catalog for the system prompt.
func BuildSlashCatalogMarkdown(sums []SkillSummary) string {
	if len(sums) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Slash commands\n\n")
	b.WriteString("Available `/name` prefixes (slash then command name):\n\n")
	for _, s := range sums {
		b.WriteString("- **`/")
		b.WriteString(s.Name)
		b.WriteString("`** ")
		b.WriteString(s.Description)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.String()
}

// ParseInvokedCommandNames finds coddy-skill markdown links from the SPA picker plus /name tokens
// after whitespace or line start outside fenced code and blockquotes. Matches Compose picker output
// `[/cmd](coddy-skill:cmd)` so full skill bodies are injected for those turns.
func ParseInvokedCommandNames(text string) []string {
	lines := strings.Split(text, "\n")
	inFence := false
	var hits []string
	seen := make(map[string]struct{})

	appendName := func(n string) {
		if n == "" {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		hits = append(hits, n)
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")

		lineTrimLead := strings.TrimLeft(line, " \t")
		fenceCandidate := strings.TrimSpace(lineTrimLead)
		if strings.HasPrefix(fenceCandidate, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if stripped, ok := strings.CutPrefix(lineTrimLead, ">"); ok {
			_ = stripped
			continue
		}

		for _, sm := range coddySkillPickRE.FindAllStringSubmatch(line, -1) {
			if len(sm) > 2 && sm[1] == sm[2] {
				appendName(sm[1])
			}
		}
		scratch := coddySkillPickRE.ReplaceAllString(line, " ")
		for _, sm := range invokedMidLineSlashRE.FindAllStringSubmatch(scratch, -1) {
			if len(sm) > 1 {
				appendName(sm[1])
			}
		}
	}
	return hits
}

// SkillBySlashName maps canonical command name to the first matching loaded skill (LoadAll order).
func SkillBySlashName(loaded []*Skill) map[string]*Skill {
	m := make(map[string]*Skill)
	for _, sk := range loaded {
		name := CanonicalCommandName(sk)
		if name == "" {
			continue
		}
		if _, ok := m[name]; !ok {
			m[name] = sk
		}
	}
	return m
}

// BuildInvokedSkillsSection adds full SKILL bodies for user /name invocations (ephemeral per turn).
func BuildInvokedSkillsSection(loaded []*Skill, invokedNames []string) string {
	if len(invokedNames) == 0 {
		return ""
	}
	idx := SkillBySlashName(loaded)
	var b strings.Builder
	b.WriteString("## User-invoked slash command instructions\n\n")
	wrote := false
	for _, n := range invokedNames {
		sk, ok := idx[n]
		if !ok {
			continue
		}
		wrote = true
		b.WriteString("### /")
		b.WriteString(n)
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(sk.Content))
		b.WriteString("\n\n")
	}
	if !wrote {
		return ""
	}
	return b.String()
}

// DedupeSkillsByCanonicalName keeps the first skill per CanonicalCommandName.
func DedupeSkillsByCanonicalName(list []*Skill) []*Skill {
	seen := make(map[string]struct{})
	out := make([]*Skill, 0, len(list))
	for _, sk := range list {
		n := CanonicalCommandName(sk)
		if n == "" {
			out = append(out, sk)
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, sk)
	}
	return out
}

// FilterSummariesByPrefix returns summaries whose Name has the given prefix (case-insensitive).
// Empty prefix matches all items (already sorted by name).
func FilterSummariesByPrefix(sums []SkillSummary, prefix string) []SkillSummary {
	if prefix == "" {
		return sums
	}
	lp := strings.ToLower(prefix)
	var out []SkillSummary
	for _, s := range sums {
		if strings.HasPrefix(strings.ToLower(s.Name), lp) {
			out = append(out, s)
		}
	}
	return out
}

// PaginateSkillSummaries returns a slice for the 1-based page index and whether more pages exist.
func PaginateSkillSummaries(sums []SkillSummary, page, pageSize int) (items []SkillSummary, total int, hasMore bool) {
	total = len(sums)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 1
	}
	start := (page - 1) * pageSize
	if start >= total {
		return []SkillSummary{}, total, false
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return sums[start:end], total, end < total
}
