//go:build cli

package tui

import (
	"regexp"
	"strconv"
	"strings"
)

const (
	defaultPrimaryColumnWidth = 32
	primaryColumnGap          = 2
	minDescriptionWidth       = 10
)

var newlineRunRegex = regexp.MustCompile(`[\r\n]+`)

// SelectItem is one row of a SelectList.
type SelectItem struct {
	Value       string
	Label       string
	Description string
}

// SelectListTheme styles SelectList rows.
type SelectListTheme struct {
	SelectedText func(string) string
	Description  func(string) string
	ScrollInfo   func(string) string
	NoMatch      func(string) string
}

// SelectListLayout tunes the two-column layout.
type SelectListLayout struct {
	MinPrimaryColumnWidth int
	MaxPrimaryColumnWidth int
}

// SelectList renders a scrolling selection list with `→ ` cursor prefix and an
// optional description column (port of pi-tui SelectList).
type SelectList struct {
	items         []SelectItem
	filteredItems []SelectItem
	selectedIndex int
	maxVisible    int
	theme         SelectListTheme
	layout        SelectListLayout

	OnSelect          func(item SelectItem)
	OnCancel          func()
	OnSelectionChange func(item SelectItem)
}

// NewSelectList creates a SelectList showing up to maxVisible rows.
func NewSelectList(items []SelectItem, maxVisible int, theme SelectListTheme, layout SelectListLayout) *SelectList {
	return &SelectList{
		items:         items,
		filteredItems: items,
		maxVisible:    maxVisible,
		theme:         theme,
		layout:        layout,
	}
}

// SetItems replaces the item set, keeping the current filter reset.
func (s *SelectList) SetItems(items []SelectItem) {
	s.items = items
	s.filteredItems = items
	s.selectedIndex = 0
}

// SetFilter keeps items whose value starts with filter, or whose label
// contains it (case-insensitive) so titled rows stay searchable.
func (s *SelectList) SetFilter(filter string) {
	lower := strings.ToLower(filter)
	var filtered []SelectItem
	for _, it := range s.items {
		if strings.HasPrefix(strings.ToLower(it.Value), lower) ||
			(it.Label != "" && strings.Contains(strings.ToLower(it.Label), lower)) {
			filtered = append(filtered, it)
		}
	}
	s.filteredItems = filtered
	s.selectedIndex = 0
}

// SetSelectedIndex moves the cursor to index, clamped to the filtered range.
func (s *SelectList) SetSelectedIndex(index int) {
	if len(s.filteredItems) == 0 {
		s.selectedIndex = 0
		return
	}
	s.selectedIndex = max(0, min(index, len(s.filteredItems)-1))
}

// SelectedItem returns the current selection or nil.
func (s *SelectList) SelectedItem() *SelectItem {
	if s.selectedIndex < 0 || s.selectedIndex >= len(s.filteredItems) {
		return nil
	}
	it := s.filteredItems[s.selectedIndex]
	return &it
}

// MoveUp moves the cursor up, wrapping to the bottom.
func (s *SelectList) MoveUp() {
	if len(s.filteredItems) == 0 {
		return
	}
	if s.selectedIndex == 0 {
		s.selectedIndex = len(s.filteredItems) - 1
	} else {
		s.selectedIndex--
	}
	s.notifyChange()
}

// MoveDown moves the cursor down, wrapping to the top.
func (s *SelectList) MoveDown() {
	if len(s.filteredItems) == 0 {
		return
	}
	if s.selectedIndex == len(s.filteredItems)-1 {
		s.selectedIndex = 0
	} else {
		s.selectedIndex++
	}
	s.notifyChange()
}

func (s *SelectList) notifyChange() {
	if s.OnSelectionChange != nil {
		if it := s.SelectedItem(); it != nil {
			s.OnSelectionChange(*it)
		}
	}
}

// HandleInput processes select-list keys: up/down/enter/escape/ctrl+c.
func (s *SelectList) HandleInput(data []byte) {
	switch {
	case MatchesKey(data, "up"):
		s.MoveUp()
	case MatchesKey(data, "down"):
		s.MoveDown()
	case MatchesKey(data, "enter"):
		if it := s.SelectedItem(); it != nil && s.OnSelect != nil {
			s.OnSelect(*it)
		}
	case MatchesKey(data, "escape"), MatchesKey(data, "ctrl+c"):
		if s.OnCancel != nil {
			s.OnCancel()
		}
	}
}

// Invalidate is a no-op (no cached state).
func (s *SelectList) Invalidate() {}

// Render draws the visible window with the scroll indicator.
func (s *SelectList) Render(width int) []string {
	var lines []string
	if len(s.filteredItems) == 0 {
		return []string{s.theme.NoMatch("  No matching commands")}
	}
	primaryColumnWidth := s.primaryColumnWidth()
	startIndex := max(0, min(s.selectedIndex-s.maxVisible/2, len(s.filteredItems)-s.maxVisible))
	endIndex := min(startIndex+s.maxVisible, len(s.filteredItems))
	for i := startIndex; i < endIndex; i++ {
		item := s.filteredItems[i]
		desc := ""
		if item.Description != "" {
			desc = strings.TrimSpace(newlineRunRegex.ReplaceAllString(item.Description, " "))
		}
		lines = append(lines, s.renderItem(item, i == s.selectedIndex, width, desc, primaryColumnWidth))
	}
	if startIndex > 0 || endIndex < len(s.filteredItems) {
		scrollText := "  (" + strconv.Itoa(s.selectedIndex+1) + "/" + strconv.Itoa(len(s.filteredItems)) + ")"
		lines = append(lines, s.theme.ScrollInfo(TruncateToWidth(scrollText, width-2, "")))
	}
	return lines
}

func (s *SelectList) renderItem(item SelectItem, isSelected bool, width int, desc string, primaryColumnWidth int) string {
	prefix := "  "
	if isSelected {
		prefix = "→ "
	}
	prefixWidth := VisibleWidth(prefix)

	if desc != "" && width > 40 {
		effective := max(1, min(primaryColumnWidth, width-prefixWidth-4))
		maxPrimaryWidth := max(1, effective-primaryColumnGap)
		value := TruncateToWidth(s.displayValue(item), maxPrimaryWidth, "")
		valueWidth := VisibleWidth(value)
		spacing := strings.Repeat(" ", max(1, effective-valueWidth))
		descStart := prefixWidth + valueWidth + len(spacing)
		remaining := width - descStart - 2
		if remaining > minDescriptionWidth {
			truncatedDesc := TruncateToWidth(desc, remaining, "")
			if isSelected {
				return s.theme.SelectedText(prefix + value + spacing + truncatedDesc)
			}
			return prefix + value + s.theme.Description(spacing+truncatedDesc)
		}
	}

	maxWidth := width - prefixWidth - 2
	value := TruncateToWidth(s.displayValue(item), maxWidth, "")
	if isSelected {
		return s.theme.SelectedText(prefix + value)
	}
	return prefix + value
}

func (s *SelectList) primaryColumnWidth() int {
	rawMin := s.layout.MinPrimaryColumnWidth
	rawMax := s.layout.MaxPrimaryColumnWidth
	if rawMin == 0 {
		rawMin = rawMax
	}
	if rawMax == 0 {
		rawMax = rawMin
	}
	if rawMin == 0 {
		rawMin, rawMax = defaultPrimaryColumnWidth, defaultPrimaryColumnWidth
	}
	lo := max(1, min(rawMin, rawMax))
	hi := max(1, max(rawMin, rawMax))
	widest := 0
	for _, it := range s.filteredItems {
		widest = max(widest, VisibleWidth(s.displayValue(it))+primaryColumnGap)
	}
	return max(lo, min(widest, hi))
}

func (s *SelectList) displayValue(item SelectItem) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Value
}
