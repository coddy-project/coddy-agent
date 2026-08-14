//go:build cli

package cli

import (
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// permissionModal renders a permission request: tool title, prompt body, and
// the option list provided by the agent (permission.Options output).
type permissionModal struct {
	tui.Container

	theme *tui.Theme
	list  *tui.SelectList

	OnDone func(res *acp.PermissionResult)
}

func newPermissionModal(theme *tui.Theme, params acp.PermissionRequestParams, requestRender func()) *permissionModal {
	m := &permissionModal{theme: theme}
	items := make([]tui.SelectItem, 0, len(params.Options))
	for _, opt := range params.Options {
		items = append(items, tui.SelectItem{Value: opt.OptionID, Label: tui.SanitizeText(opt.Name)})
	}
	m.list = tui.NewSelectList(items, max(len(items), 3), selectListTheme(theme), tui.SelectListLayout{})
	m.list.OnSelect = func(item tui.SelectItem) {
		if m.OnDone != nil {
			m.OnDone(&acp.PermissionResult{Outcome: "selected", OptionID: item.Value})
		}
	}
	m.list.OnCancel = func() {
		if m.OnDone != nil {
			m.OnDone(&acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"})
		}
	}

	th := theme
	m.AddChild(tui.NewSpacer(1))
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleWarning)))
	m.AddChild(tui.NewText(th.Fg(roleWarning, th.Bold("Permission required")), 1, 0, nil))
	if title := strings.TrimSpace(params.ToolCall.Title); title != "" {
		m.AddChild(tui.NewText(th.Fg(roleText, tui.SanitizeText(title)), 1, 0, nil))
	}
	if body := permissionBody(params); body != "" {
		m.AddChild(tui.NewText(th.Fg(roleMuted, tui.SanitizeText(body)), 1, 0, nil))
	}
	m.AddChild(tui.NewSpacer(1))
	m.AddChild(m.list)
	m.AddChild(tui.NewText(th.Fg(roleDim, "↑↓ navigate · enter choose · esc reject"), 1, 0, nil))
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleWarning)))
	_ = requestRender
	return m
}

func permissionBody(params acp.PermissionRequestParams) string {
	for _, c := range params.ToolCall.Content {
		if c.Content.Text != "" {
			return c.Content.Text
		}
	}
	return ""
}

// HandleInput forwards to the option list.
func (m *permissionModal) HandleInput(data []byte) { m.list.HandleInput(data) }

// questionModal renders the question tool: one prompt at a time with options,
// multi-select via space, and an optional custom free-text answer.
type questionModal struct {
	tui.Container

	theme    *tui.Theme
	params   acp.QuestionRequestParams
	current  int
	answers  [][]string
	selected map[int]bool
	custom   *tui.Editor
	inCustom bool
	list     *tui.SelectList
	render   func()

	OnDone func(res *acp.QuestionResult)
}

const customAnswerValue = "\x00custom"

func newQuestionModal(theme *tui.Theme, params acp.QuestionRequestParams, requestRender func()) *questionModal {
	m := &questionModal{theme: theme, params: params, selected: map[int]bool{}, render: requestRender}
	m.buildQuestion()
	return m
}

func (m *questionModal) question() *acp.QuestionPrompt {
	if m.current < 0 || m.current >= len(m.params.Questions) {
		return nil
	}
	return &m.params.Questions[m.current]
}

func (m *questionModal) buildQuestion() {
	m.Clear()
	q := m.question()
	if q == nil {
		return
	}
	th := m.theme
	// Value carries the option INDEX so answers return the original label
	// even when sanitizing changed (or collided) the display text.
	items := make([]tui.SelectItem, 0, len(q.Options)+1)
	for i, opt := range q.Options {
		display := tui.SanitizeText(opt.Label)
		if q.Multiple {
			box := "[ ] "
			if m.selected[i] {
				box = "[x] "
			}
			display = box + display
		}
		items = append(items, tui.SelectItem{Value: strconv.Itoa(i), Label: display, Description: tui.SanitizeText(opt.Description)})
	}
	if q.Custom {
		items = append(items, tui.SelectItem{Value: customAnswerValue, Label: "Custom answer..."})
	}
	m.list = tui.NewSelectList(items, min(len(items), 8), selectListTheme(th), tui.SelectListLayout{})
	m.list.OnSelect = func(item tui.SelectItem) { m.confirm(item) }
	m.list.OnCancel = func() {
		if m.OnDone != nil {
			m.OnDone(&acp.QuestionResult{})
		}
	}

	m.AddChild(tui.NewSpacer(1))
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
	if q.Header != "" {
		m.AddChild(tui.NewText(th.Fg(roleMuted, tui.SanitizeText(q.Header)), 1, 0, nil))
	}
	m.AddChild(tui.NewText(th.Fg(roleAccent, th.Bold(tui.SanitizeText(q.Question))), 1, 0, nil))
	m.AddChild(m.list)
	hint := "↑↓ navigate · enter choose · esc cancel"
	if q.Multiple {
		hint = "↑↓ navigate · space toggle · enter confirm · esc cancel"
	}
	m.AddChild(tui.NewText(th.Fg(roleDim, hint), 1, 0, nil))
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
	m.inCustom = false
}

func (m *questionModal) buildCustom() {
	m.Clear()
	th := m.theme
	q := m.question()
	m.custom = tui.NewEditor(nil, tui.EditorTheme{BorderColor: th.FgFn(roleBorderAccent)}, 0)
	m.custom.SetFocused(true)
	m.custom.OnSubmit = func(text string) {
		m.finishQuestion([]string{text})
	}
	m.AddChild(tui.NewSpacer(1))
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
	if q != nil {
		m.AddChild(tui.NewText(th.Fg(roleAccent, th.Bold(tui.SanitizeText(q.Question))), 1, 0, nil))
	}
	m.AddChild(tui.NewText(th.Fg(roleMuted, "Type your answer, enter to confirm:"), 1, 0, nil))
	m.AddChild(m.custom)
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
	m.inCustom = true
}

func (m *questionModal) confirm(item tui.SelectItem) {
	q := m.question()
	if q == nil {
		return
	}
	if item.Value == customAnswerValue {
		m.buildCustom()
		if m.render != nil {
			m.render()
		}
		return
	}
	chosen, err := strconv.Atoi(item.Value)
	if err != nil || chosen < 0 || chosen >= len(q.Options) {
		return
	}
	if q.Multiple {
		// Space toggles; enter on an item includes it and confirms the set.
		var answers []string
		for i, opt := range q.Options {
			if m.selected[i] || i == chosen {
				answers = append(answers, opt.Label)
			}
		}
		m.finishQuestion(answers)
		return
	}
	m.finishQuestion([]string{q.Options[chosen].Label})
}

func (m *questionModal) finishQuestion(answers []string) {
	m.answers = append(m.answers, answers)
	m.current++
	m.selected = map[int]bool{}
	if m.current >= len(m.params.Questions) {
		if m.OnDone != nil {
			m.OnDone(&acp.QuestionResult{Answers: m.answers})
		}
		return
	}
	m.buildQuestion()
	if m.render != nil {
		m.render()
	}
}

// HandleInput routes to the custom editor or the option list; space toggles
// multi-select items.
func (m *questionModal) HandleInput(data []byte) {
	if m.inCustom {
		if key, ok := tui.ParseKey(data); ok && key.String() == "escape" {
			m.buildQuestion()
			if m.render != nil {
				m.render()
			}
			return
		}
		m.custom.HandleInput(data)
		return
	}
	q := m.question()
	if q != nil && q.Multiple {
		if key, ok := tui.ParseKey(data); ok && key.String() == "space" {
			keep := -1
			if it := m.list.SelectedItem(); it != nil {
				if idx, err := strconv.Atoi(it.Value); err == nil && idx >= 0 && idx < len(q.Options) {
					m.selected[idx] = !m.selected[idx]
					keep = idx
				}
			}
			// Rebuild so the [x] markers reflect the toggle, keeping the
			// cursor on the same row.
			m.buildQuestion()
			if keep >= 0 {
				m.list.SetSelectedIndex(keep)
			}
			if m.render != nil {
				m.render()
			}
			return
		}
	}
	m.list.HandleInput(data)
}

// InsertPaste forwards paste bodies to the custom answer editor when active.
func (m *questionModal) InsertPaste(body string) {
	if m.inCustom && m.custom != nil {
		m.custom.InsertPaste(body)
	}
}
