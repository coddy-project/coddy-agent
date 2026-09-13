//go:build gateway || gateway.telegram

package telegram

import (
	"regexp"
	"strconv"
	"strings"
)

// This file is the gateway's outbound rendering step for Telegram: the one
// place where an answer written for nobody in particular is turned into the
// syntax this messenger understands.
//
// The agent writes ordinary GitHub-flavoured Markdown - the same text a
// browser tab or a terminal shows - and nothing about Telegram is put into the
// prompt or kept in the transcript. A conversation held in a chat is an
// ordinary session, so the next integration renders the very same answer in
// its own syntax by adding a file like this one, and touches nothing else.
//
// Telegram legacy Markdown supports: *bold*, _italic_, `inline code`,
// ```pre blocks```, [text](url). It does NOT support ## headings,
// **double-star bold**, tables, or horizontal rules.
//
// Conversion rules (code is always preserved verbatim):
//   - Fenced code blocks and inline code spans → set aside before anything else
//     runs and put back untouched afterwards, language hint included
//   - ATX headings (# … ######) → *Heading text*
//   - Double-star bold **text** / __text__ → *text* / _text_
//   - Bullet asterisk "* item" at line start, indented or not → "• item"
//   - Markdown tables → best-effort plain text (pipes stripped, alignment rows removed)
//   - Horizontal rules (--- / === / ***) → a plain separator line
var (
	reHeader      = regexp.MustCompile(`(?m)^#{1,6} +(.+)$`)
	reDoubleStar  = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reDoubleUnder = regexp.MustCompile(`__(.+?)__`)
	reBulletStar  = regexp.MustCompile(`(?m)^([ \t]*)\* `)
	reHRule       = regexp.MustCompile(`(?m)^(\*{3,}|-{3,}|={3,})$`)
	reTableAlign  = regexp.MustCompile(`(?m)^\|?[\s\-:|]+\|[\s\-:|]*\|?$`) // alignment row
	// A fence opens on three or more backticks or tildes; an inline span is one
	// backtick to the next on the same line.
	reFenceOpen  = regexp.MustCompile("^[`~]{3,}")
	reInlineCode = regexp.MustCompile("`[^`\n]+`")
)

// mdToTelegram converts text from standard Markdown to Telegram legacy-Markdown
// format. The result is meant to be sent with ParseMode="Markdown"; the sender
// retries without a parse mode if Telegram still rejects it, so a stray
// asterisk in prose costs formatting, never the reply.
func mdToTelegram(text string) string {
	return convertMarkdown(text, "*", "_")
}

// mdToPlainPreview renders the same text for a message sent with no parse mode:
// the live streaming preview, which is edited many times a turn and is often a
// half-written sentence Telegram could not parse. Nothing interprets markers
// there, so emphasis is dropped rather than left on screen as punctuation - the
// heading of a section the model is writing reads as a heading, not as "##".
func mdToPlainPreview(text string) string {
	return convertMarkdown(text, "", "")
}

// convertMarkdown applies the conversion with the emphasis markers the target
// message understands; empty markers drop the emphasis instead of marking it.
func convertMarkdown(text, bold, italic string) string {
	// Code is set aside first: every rule below would otherwise rewrite the
	// code it was meant to leave alone, turning a Go `**p` or a shell comment
	// into formatting.
	masked, code := maskCode(text)

	// ${1} rather than $1: an underscore is a word character, so "_$1_" names
	// a group called "1_" and expands to nothing.
	masked = reHeader.ReplaceAllString(masked, bold+"${1}"+bold)
	masked = reDoubleStar.ReplaceAllString(masked, bold+"${1}"+bold)
	masked = reDoubleUnder.ReplaceAllString(masked, italic+"${1}"+italic)
	masked = reBulletStar.ReplaceAllString(masked, "${1}• ")
	masked = reHRule.ReplaceAllString(masked, "────────────────")
	masked = convertTables(masked)

	return restoreCode(masked, code)
}

// codePlaceholder is the token a piece of code is replaced by while the
// conversions run. It carries a NUL on both sides, which cannot appear in a
// Telegram message, so no rule and no text of the answer can match it.
func codePlaceholder(idx int) string {
	return "\x00CODE" + strconv.Itoa(idx) + "\x00"
}

// maskCode replaces every fenced code block and then every inline code span
// with a placeholder, and returns the masked text together with the pieces in
// order. Fences go first so a backtick inside a block cannot be read as the
// start of an inline span.
func maskCode(text string) (string, []string) {
	masked, code := maskFencedBlocks(text)
	masked = reInlineCode.ReplaceAllStringFunc(masked, func(span string) string {
		placeholder := codePlaceholder(len(code))
		code = append(code, span)
		return placeholder
	})
	return masked, code
}

// maskFencedBlocks sets aside every fenced block. A fence opens on a run of at
// least three backticks or tildes and closes on a line that is nothing but the
// same character repeated at least as many times, which is what CommonMark
// says and what lets a four-backtick fence hold a three-backtick line. An
// unclosed block is taken to run to the end of the message - a truncated
// answer stopped inside its code - and Telegram reads it the same way.
func maskFencedBlocks(text string) (string, []string) {
	var (
		blocks     []string
		out        strings.Builder
		blockLines []string
		fence      string
	)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if fence == "" {
			if open := reFenceOpen.FindString(trimmed); open != "" {
				fence = open
				blockLines = []string{line}
			} else {
				out.WriteString(line)
			}
		} else {
			blockLines = append(blockLines, line)
			if closesFence(trimmed, fence) {
				out.WriteString(codePlaceholder(len(blocks)))
				blocks = append(blocks, strings.Join(blockLines, "\n"))
				blockLines = nil
				fence = ""
			}
		}
		if i < len(lines)-1 && fence == "" {
			out.WriteByte('\n')
		}
	}
	if len(blockLines) > 0 {
		out.WriteString(codePlaceholder(len(blocks)))
		blocks = append(blocks, strings.Join(blockLines, "\n"))
	}
	return out.String(), blocks
}

// closesFence reports whether a line ends the block opened by open: the same
// character, at least as many of it, and nothing else on the line.
func closesFence(trimmed, open string) bool {
	if len(trimmed) < len(open) {
		return false
	}
	return strings.Count(trimmed, open[:1]) == len(trimmed)
}

// restoreCode puts the code back where its placeholders are.
func restoreCode(text string, code []string) string {
	for i, piece := range code {
		text = strings.ReplaceAll(text, codePlaceholder(i), piece)
	}
	return text
}

// convertTables removes Markdown table alignment rows and strips pipe characters,
// turning table rows into plain comma-separated or space-aligned text.
func convertTables(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// The alignment row (|---|:---:|) carries nothing a reader wants, and
		// emptying it in place would leave a blank line through the middle of
		// the flattened table, so the line goes.
		if trimmed != "" && reTableAlign.MatchString(trimmed) {
			continue
		}
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
	return collapseBlankLines(strings.Join(out, "\n"))
}

var reBlankLines = regexp.MustCompile(`\n{3,}`)

func collapseBlankLines(s string) string {
	return reBlankLines.ReplaceAllString(s, "\n\n")
}
