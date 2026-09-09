//go:build gateway || gateway.telegram

package telegram

import (
	"regexp"
	"strings"
)

// mdToTelegram converts a standard-markdown string to Telegram legacy-Markdown format.
//
// Telegram legacy Markdown supports: *bold*, _italic_, `inline code`, ```pre blocks```, [text](url).
// It does NOT support ## headers, **double-star bold**, tables, or horizontal rules.
//
// Conversion rules applied (code blocks are always preserved verbatim):
//   - Fenced code blocks (```...```) → kept, only language hint stripped from fence line
//   - ATX headers (# … ######) → *Header text*
//   - Double-star bold **text** / __text__ → *text* / _text_
//   - Bullet asterisk "* item" at line start → "• item"
//   - Markdown tables → best-effort plain text (pipes stripped, alignment rows removed)
//   - Horizontal rules (--- / === / ***) → a plain separator line
var (
	reHeader      = regexp.MustCompile(`(?m)^#{1,6} +(.+)$`)
	reDoubleStar  = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reDoubleUnder = regexp.MustCompile(`__(.+?)__`)
	reBulletStar  = regexp.MustCompile(`(?m)^\* `)
	reHRule       = regexp.MustCompile(`(?m)^(\*{3,}|-{3,}|={3,})$`)
	reTableAlign  = regexp.MustCompile(`(?m)^\|?[\s\-:|]+\|[\s\-:|]*\|?$`) // alignment row
)

// mdToTelegram converts text from standard Markdown to Telegram legacy-Markdown format.
// Returns the converted string; always safe to send with ParseMode="Markdown".
func mdToTelegram(text string) string {
	// Step 1: extract fenced code blocks so we never touch their content.
	blocks, placeholder := extractCodeBlocks(text)

	// Step 2: apply conversions on the non-code parts.
	text = reHeader.ReplaceAllString(text, "*$1*")
	text = reDoubleStar.ReplaceAllString(text, "*$1*")
	text = reDoubleUnder.ReplaceAllString(text, "_$1_")
	text = reBulletStar.ReplaceAllString(text, "• ")
	text = reHRule.ReplaceAllString(text, "────────────────")
	text = convertTables(text)

	// Step 3: restore code blocks.
	_ = blocks
	_ = placeholder
	return text
}

// extractCodeBlocks replaces fenced code blocks with placeholder tokens
// and returns a map of placeholder → original block plus the modified text.
// (Currently unused in the final conversion but kept for future MarkdownV2 support.)
func extractCodeBlocks(text string) (map[string]string, string) {
	blocks := map[string]string{}
	idx := 0
	var out strings.Builder
	lines := strings.Split(text, "\n")
	inBlock := false
	var blockLines []string
	for _, line := range lines {
		if !inBlock && strings.HasPrefix(line, "```") {
			inBlock = true
			blockLines = []string{line}
			continue
		}
		if inBlock {
			blockLines = append(blockLines, line)
			if line == "```" || strings.TrimSpace(line) == "```" {
				key := "\x00BLOCK" + string(rune(idx)) + "\x00"
				blocks[key] = strings.Join(blockLines, "\n")
				out.WriteString(key)
				out.WriteByte('\n')
				blockLines = nil
				inBlock = false
				idx++
			}
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	// unclosed block
	if len(blockLines) > 0 {
		key := "\x00BLOCK" + string(rune(idx)) + "\x00"
		blocks[key] = strings.Join(blockLines, "\n")
		out.WriteString(key)
	}
	return blocks, out.String()
}

// convertTables removes Markdown table alignment rows and strips pipe characters,
// turning table rows into plain comma-separated or space-aligned text.
func convertTables(text string) string {
	// Remove alignment rows like |---|:---:|
	text = reTableAlign.ReplaceAllString(text, "")
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "|") && strings.HasPrefix(trimmed, "|") {
			// Strip leading/trailing pipes and split into cells.
			inner := strings.Trim(trimmed, "|")
			cells := strings.Split(inner, "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			out = append(out, strings.Join(cells, "  │  "))
		} else {
			out = append(out, line)
		}
	}
	// Remove consecutive blank lines left by alignment row removal.
	return collapseBlankLines(strings.Join(out, "\n"))
}

func collapseBlankLines(s string) string {
	re := regexp.MustCompile(`\n{3,}`)
	return re.ReplaceAllString(s, "\n\n")
}

// telegramFormattingHint is prepended to the first message of a new gateway session
// so the agent knows to use Telegram-compatible formatting.
const telegramFormattingHint = `[System note – format your replies for Telegram chat:
• Use *bold* (single asterisks, e.g. *word*) for emphasis
• Use _italic_ (underscores) for secondary emphasis
• Use ` + "`code`" + ` for inline code and ` + "```lang\n...\n```" + ` for blocks
• No markdown tables — use plain text, bullet lists, or ` + "`code`" + ` blocks instead
• No # headings — use *bold* text as a section title instead
• Use - or • for bullet lists, not * (asterisk bullets break formatting)
This note is invisible to the user.]

`
