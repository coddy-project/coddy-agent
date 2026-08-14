//go:build cli

package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rivo/uniseg"
)

const maxHistoryEntries = 100

// Large pastes collapse into an atomic marker (pi thresholds).
const (
	pasteMarkerLineThreshold = 10
	pasteMarkerCharThreshold = 1000
)

// EditorTheme styles the editor chrome.
type EditorTheme struct {
	BorderColor func(string) string
}

// AutocompleteItem is one suggestion row.
type AutocompleteItem struct {
	Value       string
	Label       string
	Description string
}

// AutocompleteProvider supplies suggestions for the editor content.
type AutocompleteProvider interface {
	// Suggestions returns items for the current content and cursor, or nil.
	// force is set when completion was requested explicitly (tab).
	Suggestions(lines []string, cursorLine, cursorCol int, force bool) []AutocompleteItem
	// Apply inserts item into the content, returning new lines and cursor.
	Apply(lines []string, cursorLine, cursorCol int, item AutocompleteItem) (newLines []string, newLine, newCol int)
}

// Editor is the multi-line input with horizontal rule borders, sticky-column
// vertical movement, history, and autocomplete (subset port of pi-tui Editor).
type Editor struct {
	theme    EditorTheme
	terminal Terminal

	lines      []string
	cursorLine int
	cursorCol  int // byte offset within lines[cursorLine]

	scrollOffset    int
	lastLayoutWidth int
	preferredVisCol int
	hasPreferredCol bool
	paddingX        int
	focused         bool

	history        []string
	historyIndex   int // -1 = not browsing
	historyDraft   []string
	historyDraftLn int
	historyDraftCl int

	killRing []string

	pastes       map[int]string
	pasteCounter int

	provider    AutocompleteProvider
	acList      *SelectList
	acOpen      bool
	acMaxRows   int
	acStyle     SelectListTheme
	acLayout    SelectListLayout
	requestSync func()

	OnSubmit func(text string)
	OnChange func(text string)
	// OnUnhandled receives key sequences the editor did not consume.
	OnUnhandled func(data []byte)
}

// NewEditor creates an editor bound to t for row-count-dependent layout.
func NewEditor(t Terminal, theme EditorTheme, paddingX int) *Editor {
	return &Editor{
		theme:        theme,
		terminal:     t,
		lines:        []string{""},
		historyIndex: -1,
		paddingX:     paddingX,
		acMaxRows:    5,
		pastes:       map[int]string{},
	}
}

// SetAutocomplete wires a provider, list styling, and a render callback.
func (e *Editor) SetAutocomplete(p AutocompleteProvider, style SelectListTheme, layout SelectListLayout, requestRender func()) {
	e.provider = p
	e.acStyle = style
	e.acLayout = layout
	e.requestSync = requestRender
}

// SetFocused implements Focusable.
func (e *Editor) SetFocused(focused bool) { e.focused = focused }

// Text returns the current content joined with newlines.
func (e *Editor) Text() string { return strings.Join(e.lines, "\n") }

// SetText replaces the whole content and moves the cursor to the end.
func (e *Editor) SetText(text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	e.lines = strings.Split(text, "\n")
	if len(e.lines) == 0 {
		e.lines = []string{""}
	}
	e.cursorLine = len(e.lines) - 1
	e.cursorCol = len(e.lines[e.cursorLine])
	e.hasPreferredCol = false
	e.pastes = map[int]string{}
	e.pasteCounter = 0
	e.refreshAutocomplete(false)
	e.notifyChange()
}

// IsEmpty reports whether the editor holds only empty content.
func (e *Editor) IsEmpty() bool { return len(e.lines) == 1 && e.lines[0] == "" }

// AddToHistory records a submitted prompt (dedup consecutive, cap 100).
func (e *Editor) AddToHistory(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if len(e.history) > 0 && e.history[0] == trimmed {
		return
	}
	e.history = append([]string{trimmed}, e.history...)
	if len(e.history) > maxHistoryEntries {
		e.history = e.history[:maxHistoryEntries]
	}
}

// AutocompleteOpen reports whether the suggestion list is showing.
func (e *Editor) AutocompleteOpen() bool { return e.acOpen }

// Invalidate clears cached layout state.
func (e *Editor) Invalidate() {
	if e.acList != nil {
		e.acList.Invalidate()
	}
}

// HandleInput processes one key sequence.
func (e *Editor) HandleInput(data []byte) {
	key, isKey := ParseKey(data)

	// Autocomplete menu owns navigation keys while open.
	if e.acOpen && isKey {
		switch key.String() {
		case "up", "down":
			e.acList.HandleInput(data)
			return
		case "enter", "tab":
			slashContext := len(e.lines) > 0 && strings.HasPrefix(e.lines[0], "/")
			if it := e.acList.SelectedItem(); it != nil {
				e.applyCompletion(*it)
			}
			// Enter on a slash command applies and submits in one stroke
			// (pi behavior: `/resume` + enter opens the picker directly).
			if key.Name == "enter" && slashContext {
				e.closeAutocomplete()
				e.submit()
			}
			return
		case "escape":
			e.closeAutocomplete()
			return
		}
	}

	if isKey {
		switch key.String() {
		case "enter":
			e.submit()
			return
		case "shift+enter", "ctrl+j":
			e.insertNewline()
			return
		case "tab":
			e.refreshAutocomplete(true)
			if !e.acOpen {
				e.insertText("\t")
			}
			return
		case "backspace":
			e.deleteCharBackward()
			return
		case "delete":
			e.deleteCharForward()
			return
		case "ctrl+w", "alt+backspace":
			e.deleteWordBackward()
			return
		case "alt+d", "alt+delete":
			e.deleteWordForward()
			return
		case "ctrl+u":
			e.deleteToLineStart()
			return
		case "ctrl+k":
			e.deleteToLineEnd()
			return
		case "left", "ctrl+b":
			e.moveLeft()
			return
		case "right", "ctrl+f":
			e.moveRight()
			return
		case "alt+left", "ctrl+left", "alt+b":
			e.moveWordLeft()
			return
		case "alt+right", "ctrl+right", "alt+f":
			e.moveWordRight()
			return
		case "home", "ctrl+a":
			e.cursorCol = 0
			e.hasPreferredCol = false
			return
		case "end", "ctrl+e":
			e.cursorCol = len(e.lines[e.cursorLine])
			e.hasPreferredCol = false
			return
		case "up":
			e.moveUp()
			return
		case "down":
			e.moveDown()
			return
		}
	}

	// Plain printable input (including UTF-8 and pasted bodies).
	s := string(data)
	if s != "" && !strings.ContainsRune(s, 0x1b) && isPrintableInput(s) {
		e.insertText(s)
		return
	}
	if e.OnUnhandled != nil {
		e.OnUnhandled(data)
	}
}

// InsertPaste inserts a bracketed-paste body, normalizing line endings and
// tabs (4 spaces, pi normalizeText). Large pastes collapse into an atomic
// `[paste #N +K lines]` marker; the body is expanded again on submit.
func (e *Editor) InsertPaste(body string) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	body = strings.ReplaceAll(body, "\t", "    ")
	var b strings.Builder
	for _, r := range body {
		if r == '\n' || r >= 0x20 {
			b.WriteRune(r)
		}
	}
	text := b.String()
	lineCount := strings.Count(text, "\n") + 1
	if lineCount > pasteMarkerLineThreshold || len(text) > pasteMarkerCharThreshold {
		e.pasteCounter++
		e.pastes[e.pasteCounter] = text
		var marker string
		if lineCount > pasteMarkerLineThreshold {
			marker = fmt.Sprintf("[paste #%d +%d lines]", e.pasteCounter, lineCount)
		} else {
			marker = fmt.Sprintf("[paste #%d %d chars]", e.pasteCounter, len(text))
		}
		e.insertText(marker)
		return
	}
	e.insertText(text)
}

var pasteMarkerRegex = regexp.MustCompile(`\[paste #(\d+)(?: (?:\+\d+ lines|\d+ chars))?\]`)

// markerRangeEndingAt reports a paste marker whose closing bracket sits at
// col (backspace lands right after it).
func (e *Editor) markerRangeEndingAt(line string, col int) (start, end, id int, ok bool) {
	for _, m := range pasteMarkerRegex.FindAllStringSubmatchIndex(line, -1) {
		if m[1] == col {
			markerID, err := strconv.Atoi(line[m[2]:m[3]])
			if err != nil {
				return 0, 0, 0, false
			}
			return m[0], m[1], markerID, true
		}
	}
	return 0, 0, 0, false
}

// markerRangeStartingAt reports a paste marker beginning exactly at col.
func (e *Editor) markerRangeStartingAt(line string, col int) (start, end, id int, ok bool) {
	for _, m := range pasteMarkerRegex.FindAllStringSubmatchIndex(line, -1) {
		if m[0] == col {
			markerID, err := strconv.Atoi(line[m[2]:m[3]])
			if err != nil {
				return 0, 0, 0, false
			}
			return m[0], m[1], markerID, true
		}
	}
	return 0, 0, 0, false
}

// expandPasteMarkers substitutes stored paste bodies back into the text.
func (e *Editor) expandPasteMarkers(text string) string {
	if len(e.pastes) == 0 {
		return text
	}
	return pasteMarkerRegex.ReplaceAllStringFunc(text, func(m string) string {
		sub := pasteMarkerRegex.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		id, err := strconv.Atoi(sub[1])
		if err != nil {
			return m
		}
		if body, ok := e.pastes[id]; ok {
			return body
		}
		return m
	})
}

func isPrintableInput(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\t' {
			return false
		}
		if r == 0x7f {
			return false
		}
	}
	return true
}

func (e *Editor) submit() {
	// Backslash-enter inserts a newline (pi workaround for terminals without
	// shift+enter reporting).
	line := e.lines[e.cursorLine]
	if e.cursorCol > 0 && e.cursorCol <= len(line) && line[e.cursorCol-1] == '\\' {
		e.lines[e.cursorLine] = line[:e.cursorCol-1] + line[e.cursorCol:]
		e.cursorCol--
		e.insertNewline()
		return
	}
	text := strings.TrimSpace(e.expandPasteMarkers(e.Text()))
	e.lines = []string{""}
	e.cursorLine, e.cursorCol = 0, 0
	e.scrollOffset = 0
	e.historyIndex = -1
	e.hasPreferredCol = false
	e.pastes = map[int]string{}
	e.pasteCounter = 0
	e.closeAutocomplete()
	e.notifyChange()
	if e.OnSubmit != nil {
		e.OnSubmit(text)
	}
}

func (e *Editor) insertNewline() {
	line := e.lines[e.cursorLine]
	before, after := line[:e.cursorCol], line[e.cursorCol:]
	e.lines[e.cursorLine] = before
	rest := append([]string{after}, e.lines[e.cursorLine+1:]...)
	e.lines = append(e.lines[:e.cursorLine+1], rest...)
	e.cursorLine++
	e.cursorCol = 0
	e.hasPreferredCol = false
	e.refreshAutocomplete(false)
	e.notifyChange()
}

func (e *Editor) insertText(s string) {
	s = strings.ReplaceAll(s, "\t", "    ")
	if strings.Contains(s, "\n") {
		parts := strings.Split(s, "\n")
		for i, part := range parts {
			if i > 0 {
				e.insertNewline()
			}
			e.insertText(part)
		}
		return
	}
	line := e.lines[e.cursorLine]
	e.lines[e.cursorLine] = line[:e.cursorCol] + s + line[e.cursorCol:]
	e.cursorCol += len(s)
	e.hasPreferredCol = false
	e.refreshAutocomplete(false)
	e.notifyChange()
}

func (e *Editor) deleteCharBackward() {
	if e.cursorCol > 0 {
		line := e.lines[e.cursorLine]
		if start, end, id, ok := e.markerRangeEndingAt(line, e.cursorCol); ok {
			delete(e.pastes, id)
			e.lines[e.cursorLine] = line[:start] + line[end:]
			e.cursorCol = start
			e.hasPreferredCol = false
			e.refreshAutocomplete(false)
			e.notifyChange()
			return
		}
		prev := prevGraphemeStart(line, e.cursorCol)
		e.lines[e.cursorLine] = line[:prev] + line[e.cursorCol:]
		e.cursorCol = prev
	} else if e.cursorLine > 0 {
		prevLine := e.lines[e.cursorLine-1]
		e.cursorCol = len(prevLine)
		e.lines[e.cursorLine-1] = prevLine + e.lines[e.cursorLine]
		e.lines = append(e.lines[:e.cursorLine], e.lines[e.cursorLine+1:]...)
		e.cursorLine--
	} else {
		return
	}
	e.hasPreferredCol = false
	e.refreshAutocomplete(false)
	e.notifyChange()
}

func (e *Editor) deleteCharForward() {
	line := e.lines[e.cursorLine]
	if e.cursorCol < len(line) {
		if start, end, id, ok := e.markerRangeStartingAt(line, e.cursorCol); ok {
			delete(e.pastes, id)
			e.lines[e.cursorLine] = line[:start] + line[end:]
			e.refreshAutocomplete(false)
			e.notifyChange()
			return
		}
		next := nextGraphemeEnd(line, e.cursorCol)
		e.lines[e.cursorLine] = line[:e.cursorCol] + line[next:]
	} else if e.cursorLine < len(e.lines)-1 {
		e.lines[e.cursorLine] = line + e.lines[e.cursorLine+1]
		e.lines = append(e.lines[:e.cursorLine+1], e.lines[e.cursorLine+2:]...)
	} else {
		return
	}
	e.refreshAutocomplete(false)
	e.notifyChange()
}

func (e *Editor) deleteWordBackward() {
	if e.cursorCol == 0 {
		e.deleteCharBackward()
		return
	}
	line := e.lines[e.cursorLine]
	start := wordLeftBoundary(line, e.cursorCol)
	e.kill(line[start:e.cursorCol], true)
	e.lines[e.cursorLine] = line[:start] + line[e.cursorCol:]
	e.cursorCol = start
	e.refreshAutocomplete(false)
	e.notifyChange()
}

func (e *Editor) deleteWordForward() {
	line := e.lines[e.cursorLine]
	if e.cursorCol >= len(line) {
		e.deleteCharForward()
		return
	}
	end := wordRightBoundary(line, e.cursorCol)
	e.kill(line[e.cursorCol:end], false)
	e.lines[e.cursorLine] = line[:e.cursorCol] + line[end:]
	e.refreshAutocomplete(false)
	e.notifyChange()
}

func (e *Editor) deleteToLineStart() {
	line := e.lines[e.cursorLine]
	if e.cursorCol == 0 {
		return
	}
	e.kill(line[:e.cursorCol], true)
	e.lines[e.cursorLine] = line[e.cursorCol:]
	e.cursorCol = 0
	e.refreshAutocomplete(false)
	e.notifyChange()
}

func (e *Editor) deleteToLineEnd() {
	line := e.lines[e.cursorLine]
	if e.cursorCol >= len(line) {
		return
	}
	e.kill(line[e.cursorCol:], false)
	e.lines[e.cursorLine] = line[:e.cursorCol]
	e.refreshAutocomplete(false)
	e.notifyChange()
}

func (e *Editor) kill(text string, prepend bool) {
	if text == "" {
		return
	}
	e.killRing = append(e.killRing, text)
	_ = prepend
}

func (e *Editor) moveLeft() {
	if e.cursorCol > 0 {
		e.cursorCol = prevGraphemeStart(e.lines[e.cursorLine], e.cursorCol)
	} else if e.cursorLine > 0 {
		e.cursorLine--
		e.cursorCol = len(e.lines[e.cursorLine])
	}
	e.hasPreferredCol = false
}

func (e *Editor) moveRight() {
	line := e.lines[e.cursorLine]
	if e.cursorCol < len(line) {
		e.cursorCol = nextGraphemeEnd(line, e.cursorCol)
	} else if e.cursorLine < len(e.lines)-1 {
		e.cursorLine++
		e.cursorCol = 0
	}
	e.hasPreferredCol = false
}

func (e *Editor) moveWordLeft() {
	if e.cursorCol == 0 && e.cursorLine > 0 {
		e.cursorLine--
		e.cursorCol = len(e.lines[e.cursorLine])
		return
	}
	e.cursorCol = wordLeftBoundary(e.lines[e.cursorLine], e.cursorCol)
	e.hasPreferredCol = false
}

func (e *Editor) moveWordRight() {
	line := e.lines[e.cursorLine]
	if e.cursorCol >= len(line) && e.cursorLine < len(e.lines)-1 {
		e.cursorLine++
		e.cursorCol = 0
		return
	}
	e.cursorCol = wordRightBoundary(line, e.cursorCol)
	e.hasPreferredCol = false
}

// moveUp browses history when on the first visual line, else moves the cursor.
func (e *Editor) moveUp() {
	vm := e.visualMap(e.layoutWidth())
	cur := e.visualIndexFor(vm, e.cursorLine, e.cursorCol)
	if cur == 0 {
		if e.historyEligibleUp() {
			e.historyPrevious()
			return
		}
		e.cursorCol = 0
		e.hasPreferredCol = false
		return
	}
	e.moveToVisualLine(vm, cur-1)
}

func (e *Editor) moveDown() {
	vm := e.visualMap(e.layoutWidth())
	cur := e.visualIndexFor(vm, e.cursorLine, e.cursorCol)
	if cur >= len(vm)-1 {
		if e.historyIndex >= 0 {
			e.historyNext()
			return
		}
		e.cursorCol = len(e.lines[e.cursorLine])
		e.hasPreferredCol = false
		return
	}
	e.moveToVisualLine(vm, cur+1)
}

func (e *Editor) historyEligibleUp() bool {
	return e.IsEmpty() || e.historyIndex >= 0 || e.cursorCol == 0
}

func (e *Editor) historyPrevious() {
	if len(e.history) == 0 || e.historyIndex >= len(e.history)-1 {
		return
	}
	if e.historyIndex == -1 {
		e.historyDraft = append([]string(nil), e.lines...)
		e.historyDraftLn, e.historyDraftCl = e.cursorLine, e.cursorCol
	}
	e.historyIndex++
	e.loadHistoryEntry()
	e.cursorLine, e.cursorCol = 0, 0
}

func (e *Editor) historyNext() {
	if e.historyIndex < 0 {
		return
	}
	e.historyIndex--
	if e.historyIndex == -1 {
		e.lines = append([]string(nil), e.historyDraft...)
		if len(e.lines) == 0 {
			e.lines = []string{""}
		}
		e.cursorLine = min(e.historyDraftLn, len(e.lines)-1)
		e.cursorCol = min(e.historyDraftCl, len(e.lines[e.cursorLine]))
		e.notifyChange()
		return
	}
	e.loadHistoryEntry()
	e.cursorLine = len(e.lines) - 1
	e.cursorCol = len(e.lines[e.cursorLine])
}

func (e *Editor) loadHistoryEntry() {
	entry := e.history[e.historyIndex]
	e.lines = strings.Split(entry, "\n")
	if len(e.lines) == 0 {
		e.lines = []string{""}
	}
	e.notifyChange()
}

func (e *Editor) notifyChange() {
	if e.OnChange != nil {
		e.OnChange(e.Text())
	}
}

// --- autocomplete ---

func (e *Editor) refreshAutocomplete(force bool) {
	if e.provider == nil {
		return
	}
	items := e.provider.Suggestions(e.lines, e.cursorLine, e.cursorCol, force)
	if len(items) == 0 {
		e.closeAutocomplete()
		return
	}
	sel := make([]SelectItem, 0, len(items))
	for _, it := range items {
		sel = append(sel, SelectItem(it))
	}
	if e.acList == nil {
		e.acList = NewSelectList(sel, e.acMaxRows, e.acStyle, e.acLayout)
	} else {
		e.acList.SetItems(sel)
	}
	e.acOpen = true
	if force && len(items) == 1 {
		e.applyCompletion(sel[0])
	}
}

func (e *Editor) applyCompletion(item SelectItem) {
	lines, ln, col := e.provider.Apply(e.lines, e.cursorLine, e.cursorCol, AutocompleteItem(item))
	if len(lines) == 0 {
		lines = []string{""}
	}
	e.lines = lines
	e.cursorLine = min(ln, len(lines)-1)
	e.cursorCol = min(col, len(lines[e.cursorLine]))
	e.closeAutocomplete()
	e.notifyChange()
	e.refreshAutocomplete(false)
}

func (e *Editor) closeAutocomplete() { e.acOpen = false }

// --- rendering ---

type visualLine struct {
	logical  int
	startCol int // byte offset of segment start
	endCol   int // byte offset of segment end
}

func (e *Editor) layoutWidth() int {
	if e.lastLayoutWidth > 0 {
		return e.lastLayoutWidth
	}
	return 80
}

func (e *Editor) visualMap(layoutWidth int) []visualLine {
	var vm []visualLine
	for li, line := range e.lines {
		if line == "" {
			vm = append(vm, visualLine{logical: li, startCol: 0, endCol: 0})
			continue
		}
		start := 0
		width := 0
		lastBreak := -1
		i := 0
		g := uniseg.NewGraphemes(line)
		type seg struct {
			str        string
			start, end int
		}
		var segs []seg
		for g.Next() {
			str := g.Str()
			segs = append(segs, seg{str: str, start: i, end: i + len(str)})
			i += len(str)
		}
		for si := 0; si < len(segs); si++ {
			w := graphemeWidth(segs[si].str)
			if width+w > layoutWidth && width > 0 {
				breakAt := si
				if lastBreak > 0 {
					breakAt = lastBreak
				}
				if breakAt <= 0 || segs[breakAt].start <= start {
					breakAt = si
				}
				vm = append(vm, visualLine{logical: li, startCol: start, endCol: segs[breakAt].start})
				start = segs[breakAt].start
				width = 0
				lastBreak = -1
				si = breakAt - 1
				continue
			}
			if segs[si].str == " " || (si > 0 && isCJKBreak(segs[si].str)) {
				lastBreak = si + 1
			}
			width += w
		}
		vm = append(vm, visualLine{logical: li, startCol: start, endCol: len(line)})
	}
	if len(vm) == 0 {
		vm = []visualLine{{logical: 0}}
	}
	return vm
}

func (e *Editor) visualIndexFor(vm []visualLine, line, col int) int {
	for i, v := range vm {
		if v.logical != line {
			continue
		}
		last := i == len(vm)-1 || vm[i+1].logical != line
		if col < v.endCol || (last && col >= v.startCol) || (col == v.startCol) {
			if col >= v.startCol {
				return i
			}
		}
	}
	for i, v := range vm {
		if v.logical == line {
			return i
		}
	}
	return 0
}

func (e *Editor) moveToVisualLine(vm []visualLine, target int) {
	if target < 0 || target >= len(vm) {
		return
	}
	cur := e.visualIndexFor(vm, e.cursorLine, e.cursorCol)
	curV := vm[cur]
	curVisCol := VisibleWidth(e.lines[curV.logical][curV.startCol:e.cursorCol])
	if !e.hasPreferredCol {
		e.preferredVisCol = curVisCol
		e.hasPreferredCol = true
	}
	tv := vm[target]
	line := e.lines[tv.logical]
	segment := line[tv.startCol:tv.endCol]
	segWidth := VisibleWidth(segment)
	want := min(e.preferredVisCol, segWidth)
	e.cursorLine = tv.logical
	e.cursorCol = tv.startCol + byteOffsetForVisCol(segment, want)
	isLast := target == len(vm)-1 || vm[target+1].logical != tv.logical
	if !isLast && e.cursorCol >= tv.endCol && tv.endCol > tv.startCol {
		e.cursorCol = prevGraphemeStart(line, tv.endCol)
	}
}

func byteOffsetForVisCol(segment string, visCol int) int {
	width := 0
	g := uniseg.NewGraphemes(segment)
	offset := 0
	for g.Next() {
		w := graphemeWidth(g.Str())
		if width+w > visCol {
			return offset
		}
		width += w
		offset += len(g.Str())
	}
	return offset
}

// Render draws border, visible content lines with the fake cursor, bottom
// border, and the autocomplete list.
func (e *Editor) Render(width int) []string {
	maxPadding := max(0, (width-1)/2)
	paddingX := min(e.paddingX, maxPadding)
	contentWidth := max(1, width-paddingX*2)
	layoutWidth := contentWidth
	if paddingX == 0 {
		layoutWidth = max(1, contentWidth-1)
	}
	e.lastLayoutWidth = layoutWidth

	rows := 24
	if e.terminal != nil {
		rows = e.terminal.Rows()
	}
	maxVisibleLines := max(5, rows*3/10)

	vm := e.visualMap(layoutWidth)
	cursorVis := e.visualIndexFor(vm, e.cursorLine, e.cursorCol)

	if cursorVis < e.scrollOffset {
		e.scrollOffset = cursorVis
	}
	if cursorVis >= e.scrollOffset+maxVisibleLines {
		e.scrollOffset = cursorVis - maxVisibleLines + 1
	}
	if e.scrollOffset > max(0, len(vm)-maxVisibleLines) {
		e.scrollOffset = max(0, len(vm)-maxVisibleLines)
	}

	visibleEnd := min(len(vm), e.scrollOffset+maxVisibleLines)
	borderColor := e.theme.BorderColor
	if borderColor == nil {
		borderColor = func(s string) string { return s }
	}

	var result []string
	// Top border: repeat the styled rune (pi repeats the wrapped string).
	if e.scrollOffset > 0 {
		result = append(result, scrollBorder(width, "↑", e.scrollOffset, borderColor))
	} else {
		result = append(result, strings.Repeat(borderColor("─"), width))
	}

	pad := strings.Repeat(" ", paddingX)
	for vi := e.scrollOffset; vi < visibleEnd; vi++ {
		v := vm[vi]
		line := e.lines[v.logical]
		segment := line[v.startCol:v.endCol]
		out := segment
		if e.focusedCursorOn(vm, vi, cursorVis) {
			rel := e.cursorCol - v.startCol
			rel = max(0, min(rel, len(segment)))
			before := segment[:rel]
			atEnd := rel >= len(segment)
			marker := ""
			if e.focused {
				marker = CursorMarker
			}
			if atEnd {
				out = before + marker + "\x1b[7m \x1b[0m"
			} else {
				gEnd := nextGraphemeEnd(segment, rel)
				out = before + marker + "\x1b[7m" + segment[rel:gEnd] + "\x1b[0m" + segment[gEnd:]
			}
		}
		result = append(result, pad+out)
	}

	linesBelow := len(vm) - visibleEnd
	if linesBelow > 0 {
		result = append(result, scrollBorder(width, "↓", linesBelow, borderColor))
	} else {
		result = append(result, strings.Repeat(borderColor("─"), width))
	}

	if e.acOpen && e.acList != nil {
		for _, l := range e.acList.Render(contentWidth) {
			result = append(result, pad+l)
		}
	}
	return result
}

func (e *Editor) focusedCursorOn(vm []visualLine, vi, cursorVis int) bool {
	return vi == cursorVis
}

// scrollBorder renders `─── ↑ N more ` + fill (pi createScrollBorder).
func scrollBorder(width int, direction string, hidden int, color func(string) string) string {
	indicator := fmt.Sprintf("─── %s %d more ", direction, hidden)
	fill := width - VisibleWidth(indicator)
	if fill < 0 {
		return color(TruncateToWidth(indicator, width, "..."))
	}
	return color(indicator + strings.Repeat("─", fill))
}

// --- grapheme / word boundaries ---

func prevGraphemeStart(s string, from int) int {
	if from <= 0 {
		return 0
	}
	prev := 0
	g := uniseg.NewGraphemes(s[:from])
	offset := 0
	for g.Next() {
		prev = offset
		offset += len(g.Str())
	}
	return prev
}

func nextGraphemeEnd(s string, from int) int {
	if from >= len(s) {
		return len(s)
	}
	g := uniseg.NewGraphemes(s[from:])
	if g.Next() {
		return from + len(g.Str())
	}
	return len(s)
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b >= 0x80
}

func wordLeftBoundary(line string, from int) int {
	i := from
	for i > 0 && line[i-1] == ' ' {
		i--
	}
	for i > 0 && isWordByte(line[i-1]) {
		i--
	}
	if i == from && i > 0 {
		i--
	}
	return i
}

func wordRightBoundary(line string, from int) int {
	i := from
	for i < len(line) && line[i] == ' ' {
		i++
	}
	for i < len(line) && isWordByte(line[i]) {
		i++
	}
	if i == from && i < len(line) {
		i++
	}
	return i
}
