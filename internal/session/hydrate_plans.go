package session

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
)

// ExtractRunPlanSlugFromPromptText returns a slug when the user asks to implement a plan by path or slug.
// Outside ask mode the manager turns such a prompt into a plan run; in ask mode
// the "@plans/<slug>.plan.md" mention is only inlined as reading material by
// ResolvePromptMentions.
func ExtractRunPlanSlugFromPromptText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, tok := range mention.Parse(text) {
		for _, r := range tok.Readings {
			if !r.Range.IsZero() || !plans.IsPlanMention(r.Path) {
				continue
			}
			rel := filepathToSlash(r.Path)
			base := rel[strings.LastIndex(rel, "/")+1:]
			return strings.TrimSuffix(base, plans.FileSuffix)
		}
	}
	// "implement the plan auth-refactor" style
	const prefix = "implement the plan "
	if strings.Contains(text, prefix) {
		rest := strings.TrimSpace(text[strings.Index(text, prefix)+len(prefix):])
		if i := strings.IndexAny(rest, " \t\n\r.,;"); i >= 0 {
			rest = rest[:i]
		}
		if err := plans.ValidateSlug(rest); err == nil {
			return rest
		}
	}
	return ""
}

func filepathToSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

func contentBlocksToPlainText(blocks []acp.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == acp.ContentTypeText || blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}
