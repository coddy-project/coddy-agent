//go:build cli

package cli

import (
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// tasksModal is the /tasks overlay: the background tasks of the session in the place
// of the editor, where the operator sees what runs, reads a task's output and stops
// it. It is the console's twin of the web UI's Tasks panel and says the same three
// things about a task - the tag, the title, the meta line (tasks.go).
//
// The modal draws and takes keys; it reads nothing itself. The App hands it the rows
// of every refresh and the output of the task that is open, and does the work behind
// OnOpen, OnStop and OnRefresh on its workers, because under --remote each of them is
// a network round trip.
type tasksModal struct {
	tui.Container

	theme         *tui.Theme
	requestRender func()
	now           func() time.Time

	rows     []bgtask.Snapshot
	loaded   bool
	selected int

	// openID is the task whose output is on screen; empty shows the list.
	openID          string
	output          string
	outputLoaded    bool
	outputTruncated bool
	note            string

	OnOpen    func(taskID string)
	OnStop    func(taskID string)
	OnRefresh func()
	OnClose   func()
}

const (
	// tasksListRows and tasksOutputRows bound what the overlay takes of the screen;
	// the transcript above it stays readable.
	tasksListRows   = 10
	tasksOutputRows = 16
	// tasksOutputTail is how many lines of a task's output the overlay asks for.
	tasksOutputTail = 200
)

func newTasksModal(theme *tui.Theme, requestRender func()) *tasksModal {
	m := &tasksModal{theme: theme, requestRender: requestRender, now: time.Now}
	m.rebuild()
	return m
}

// SetRows adopts a refresh of the session's tasks. The cursor follows the task it was
// on rather than the row number: a new task arrives at the top of the list.
func (m *tasksModal) SetRows(rows []bgtask.Snapshot) {
	current := ""
	if m.selected >= 0 && m.selected < len(m.rows) {
		current = m.rows[m.selected].ID
	}
	m.rows = rows
	m.loaded = true
	m.selected = 0
	for i, row := range rows {
		if row.ID == current {
			m.selected = i
			break
		}
	}
	m.rebuild()
}

// SetOutput adopts the output of one task; an answer for a task that is no longer the
// open one is dropped.
func (m *tasksModal) SetOutput(taskID, output string, truncated bool) {
	if taskID != m.openID {
		return
	}
	m.output, m.outputLoaded, m.outputTruncated = output, true, truncated
	m.rebuild()
}

// SetNote puts one line under the view: what a stop or a failed read said.
func (m *tasksModal) SetNote(note string) {
	m.note = note
	m.rebuild()
}

// OpenTaskID is the task whose output is on screen, or "".
func (m *tasksModal) OpenTaskID() string { return m.openID }

func (m *tasksModal) selectedRow() (bgtask.Snapshot, bool) {
	if m.selected < 0 || m.selected >= len(m.rows) {
		return bgtask.Snapshot{}, false
	}
	return m.rows[m.selected], true
}

func (m *tasksModal) openRow() (bgtask.Snapshot, bool) {
	for _, row := range m.rows {
		if row.ID == m.openID {
			return row, true
		}
	}
	return bgtask.Snapshot{}, false
}

// HandleInput moves the cursor, opens a task, stops one, asks for a fresh read and
// steps back: escape leaves an open task first, then the overlay.
func (m *tasksModal) HandleInput(data []byte) {
	defer func() {
		if m.requestRender != nil {
			m.requestRender()
		}
	}()
	if key, ok := tui.ParseKey(data); ok {
		switch key.String() {
		case "up":
			if m.openID == "" && m.selected > 0 {
				m.selected--
				m.rebuild()
			}
			return
		case "down":
			if m.openID == "" && m.selected < len(m.rows)-1 {
				m.selected++
				m.rebuild()
			}
			return
		case "enter":
			if row, ok := m.selectedRow(); ok && m.openID == "" {
				m.openID, m.output, m.outputLoaded, m.outputTruncated, m.note = row.ID, "", false, false, ""
				m.rebuild()
				if m.OnOpen != nil {
					m.OnOpen(row.ID)
				}
			}
			return
		case "escape", "ctrl+c":
			if m.openID != "" {
				m.openID, m.output, m.outputLoaded, m.note = "", "", false, ""
				m.rebuild()
				return
			}
			if m.OnClose != nil {
				m.OnClose()
			}
			return
		}
	}
	switch string(data) {
	case "s", "S":
		row, ok := m.selectedRow()
		if m.openID != "" {
			row, ok = m.openRow()
		}
		if ok && !row.Status.Finished() && m.OnStop != nil {
			m.OnStop(row.ID)
		}
	case "r", "R":
		if m.OnRefresh != nil {
			m.OnRefresh()
		}
	}
}

func (m *tasksModal) rebuild() {
	m.Clear()
	th := m.theme
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
	if row, ok := m.openRow(); ok && m.openID != "" {
		m.buildTask(row)
	} else {
		m.openID = ""
		m.buildList()
	}
	if m.note != "" {
		m.AddChild(tui.NewText(th.Fg(roleWarning, tui.SanitizeText(m.note)), 1, 0, nil))
	}
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
	m.Invalidate()
}

func (m *tasksModal) buildList() {
	th := m.theme
	title := th.Fg(roleAccent, th.Bold("Background tasks"))
	if len(m.rows) > 0 {
		title += th.Fg(roleMuted, "  "+itoa(countRunningTasks(m.rows))+" running · "+itoa(len(m.rows))+" in total")
	}
	m.AddChild(tui.NewText(title, 1, 0, nil))

	switch {
	case !m.loaded:
		m.AddChild(tui.NewText(th.Fg(roleDim, "Loading…"), 1, 0, nil))
	case len(m.rows) == 0:
		m.AddChild(tui.NewText(th.Fg(roleDim, "No background tasks in this session yet"), 1, 0, nil))
	default:
		m.AddChild(&taskRows{modal: m})
	}
	m.AddChild(tui.NewText(th.Fg(roleDim, "↑↓ navigate · enter output · s stop · r refresh · esc close"), 1, 0, nil))
}

func (m *tasksModal) buildTask(row bgtask.Snapshot) {
	th := m.theme
	head := m.statusMark(row) + " " + th.Fg(roleAccent, taskTag(row)) + " " + th.Bold(tui.SanitizeText(taskTitle(row))) +
		th.Fg(roleMuted, "  "+taskMetaLine(row, m.now()))
	m.AddChild(tui.NewText(head, 1, 0, nil))
	if command := strings.TrimSpace(row.Command); command != "" {
		m.AddChild(tui.NewText(th.Fg(roleDim, "$ ")+tui.SanitizeText(command), 1, 0, nil))
	}
	if row.Agent != nil && strings.TrimSpace(row.Agent.SessionID) != "" {
		m.AddChild(tui.NewText(th.Fg(roleDim, "transcript: session "+tui.SanitizeText(row.Agent.SessionID)), 1, 0, nil))
	}
	if row.Error != "" {
		m.AddChild(tui.NewText(th.Fg(roleError, tui.SanitizeText(row.Error)), 1, 0, nil))
	}
	m.AddChild(&taskOutput{modal: m})
	help := "r refresh · esc back"
	if !row.Status.Finished() {
		help = "s stop · " + help
	}
	m.AddChild(tui.NewText(th.Fg(roleDim, help), 1, 0, nil))
}

// statusMark is the one-cell status of a task: the same tones the web UI's dot uses.
func (m *tasksModal) statusMark(row bgtask.Snapshot) string {
	th := m.theme
	switch row.Status {
	case bgtask.StatusQueued, bgtask.StatusRunning:
		return th.Fg(roleAccent, "●")
	case bgtask.StatusSucceeded:
		return th.Fg(roleSuccess, "✓")
	case bgtask.StatusFailed, bgtask.StatusTimedOut:
		return th.Fg(roleError, "✗")
	case bgtask.StatusStopped:
		return th.Fg(roleWarning, "■")
	default:
		return th.Fg(roleMuted, "○")
	}
}

// taskRows draws the list at the width it is given: the tag column is sized to the
// longest tag, the meta line keeps the right edge and the title gives way.
type taskRows struct{ modal *tasksModal }

func (r *taskRows) Invalidate() {}

func (r *taskRows) Render(width int) []string {
	m := r.modal
	th := m.theme
	now := m.now()
	tagWidth := 0
	for _, row := range m.rows {
		tagWidth = max(tagWidth, tui.VisibleWidth(taskTag(row)))
	}
	tagWidth = min(tagWidth, 16)

	// A window around the cursor, so a long history scrolls instead of growing.
	first := 0
	if len(m.rows) > tasksListRows {
		first = min(max(0, m.selected-tasksListRows/2), len(m.rows)-tasksListRows)
	}
	last := min(len(m.rows), first+tasksListRows)

	lines := make([]string, 0, last-first+1)
	for i := first; i < last; i++ {
		row := m.rows[i]
		cursor := "  "
		if i == m.selected {
			cursor = th.Fg(roleAccent, "→ ")
		}
		tag := tui.TruncateToWidthPad(taskTag(row), tagWidth, "…")
		meta := taskMetaLine(row, now)
		room := width - 1 - 2 - 2 - tagWidth - 2 - tui.VisibleWidth(meta) - 2
		title := tui.TruncateToWidthPad(tui.SanitizeText(taskTitle(row)), max(room, 8), "…")
		if i == m.selected {
			title = th.Bold(title)
		}
		line := " " + cursor + m.statusMark(row) + " " + th.Fg(roleAccent, tag) + "  " + title + "  " + th.Fg(roleMuted, meta)
		lines = append(lines, tui.TruncateToWidth(line, width, ""))
	}
	if len(m.rows) > tasksListRows {
		lines = append(lines, th.Fg(roleDim, "   "+itoa(m.selected+1)+" of "+itoa(len(m.rows))))
	}
	return lines
}

// taskOutput draws the tail of the open task's output in a box of its own height.
type taskOutput struct{ modal *tasksModal }

func (o *taskOutput) Invalidate() {}

func (o *taskOutput) Render(width int) []string {
	m := o.modal
	th := m.theme
	if !m.outputLoaded {
		return []string{" " + th.Fg(roleDim, "Reading the output…")}
	}
	text := strings.TrimRight(m.output, "\n")
	if strings.TrimSpace(text) == "" {
		return []string{" " + th.Fg(roleDim, "(no output yet)")}
	}
	all := strings.Split(text, "\n")
	shown := all
	if len(shown) > tasksOutputRows {
		shown = shown[len(shown)-tasksOutputRows:]
	}
	lines := make([]string, 0, len(shown)+1)
	if len(shown) < len(all) || m.outputTruncated {
		lines = append(lines, " "+th.Fg(roleDim, "… the last "+itoa(len(shown))+" lines"))
	}
	for _, line := range shown {
		lines = append(lines, tui.TruncateToWidth(" "+tui.SanitizeText(line), width, "…"))
	}
	return lines
}
