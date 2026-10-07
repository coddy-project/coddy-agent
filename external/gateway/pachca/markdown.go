//go:build gateway || gateway.pachca

package pachca

// Outbound rendering: the safety net under prompt.go, because a model does
// not always write for the chat it is in.
//
// Pachca parses bold, italic, strikethrough, links, inline code and fenced
// code blocks, and turns a "# heading" line into bold, so most of what a model
// writes goes out as it is. Two things do not survive: a table is shown as a
// wall of pipes, and a quote loses its ">" and reads as ordinary text. A table
// becomes a fenced text block, which keeps its columns; a quote line gets a
// bar in front. Fenced blocks are set aside before any rule runs, so nothing
// inside code is touched, and an unclosed fence (a cut-off answer) runs to the
// end of the text.

import (
	"strings"
	"unicode/utf8"
)

// renderMarkdown turns a model's answer into the text Pachca shows well.
func renderMarkdown(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if isFence(line) {
			// Copy the block through untouched, up to its closing fence or
			// the end of the text.
			out = append(out, line)
			for i++; i < len(lines); i++ {
				out = append(out, lines[i])
				if isFence(lines[i]) {
					break
				}
			}
			continue
		}
		if isTableRow(line) && i+1 < len(lines) && isTableSeparator(lines[i+1]) {
			j := i
			var table []string
			for ; j < len(lines) && isTableRow(lines[j]); j++ {
				if isTableSeparator(lines[j]) {
					continue
				}
				table = append(table, lines[j])
			}
			out = append(out, "```text")
			out = append(out, alignTable(table)...)
			out = append(out, "```")
			i = j - 1
			continue
		}
		if q, ok := quoteBody(line); ok {
			out = append(out, "│ "+q)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func isFence(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "|") && strings.Count(t, "|") >= 2
}

func isTableSeparator(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") {
		return false
	}
	t = strings.Trim(t, "|")
	if t == "" {
		return false
	}
	for _, cell := range strings.Split(t, "|") {
		c := strings.TrimSpace(cell)
		c = strings.TrimPrefix(c, ":")
		c = strings.TrimSuffix(c, ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func tableCells(row string) []string {
	t := strings.TrimSpace(row)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	cells := strings.Split(t, "|")
	for i := range cells {
		c := strings.TrimSpace(cells[i])
		// Inline markup means nothing inside a code block.
		c = strings.ReplaceAll(c, "**", "")
		c = strings.ReplaceAll(c, "`", "")
		cells[i] = c
	}
	return cells
}

// alignTable pads every column to its widest cell.
func alignTable(rows []string) []string {
	var grid [][]string
	var widths []int
	for _, r := range rows {
		cells := tableCells(r)
		for i, c := range cells {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			if n := utf8.RuneCountInString(c); n > widths[i] {
				widths[i] = n
			}
		}
		grid = append(grid, cells)
	}
	out := make([]string, 0, len(grid))
	for _, cells := range grid {
		var b strings.Builder
		for i, c := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(c)
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)))
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func quoteBody(line string) (string, bool) {
	t := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(t, ">") {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimPrefix(t, ">"), " "), true
}

// plainPreview is the live message while the answer is still being written:
// the tail of what has arrived, so a long answer keeps moving on screen.
func plainPreview(text string, limit int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	r := []rune(text)
	return "…" + string(r[len(r)-limit:])
}

// splitMessage cuts text into chunks of at most limit runes, preferring a
// paragraph break, then a line break, then a space. A fenced block cut in two
// is closed at the end of one chunk and opened again, with its language, at
// the start of the next, so both halves still render as code.
func splitMessage(text string, limit int) []string {
	if limit < minChunkRunes {
		limit = minChunkRunes
	}
	var chunks []string
	rest := text
	for utf8.RuneCountInString(rest) > limit {
		r := []rune(rest)
		// Leave room for the fence that may have to close this chunk.
		budget := limit - 8
		cut := lastBreak(r[:budget])
		head := strings.TrimRight(string(r[:cut]), " \n")
		tail := strings.TrimLeft(string(r[cut:]), "\n")
		if lang, open := openFence(head); open {
			head += "\n```"
			tail = "```" + lang + "\n" + tail
		}
		chunks = append(chunks, head)
		rest = tail
	}
	if strings.TrimSpace(rest) != "" || len(chunks) == 0 {
		chunks = append(chunks, rest)
	}
	return chunks
}

// minChunkRunes is the smallest piece a too-long refusal may split a text into.
const minChunkRunes = 200

func lastBreak(r []rune) int {
	s := string(r)
	for _, sep := range []string{"\n\n", "\n", " "} {
		if i := strings.LastIndex(s, sep); i > len(s)/2 {
			return utf8.RuneCountInString(s[:i])
		}
	}
	return len(r)
}

// openFence reports whether text ends inside a fenced block, and that block's
// language.
func openFence(text string) (string, bool) {
	open := false
	lang := ""
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "```") {
			continue
		}
		if open {
			open = false
			continue
		}
		open = true
		lang = strings.TrimSpace(strings.TrimPrefix(t, "```"))
	}
	return lang, open
}
