//go:build cli

package cli

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// dispatchSlash intercepts client-side slash commands. Returns true when the
// text was handled locally; /compact, /plugin, and skill commands fall
// through to the agent.
func (a *App) dispatchSlash(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	fields := strings.Fields(trimmed)
	cmd := strings.TrimPrefix(fields[0], "/")
	switch cmd {
	case "model":
		if len(fields) > 1 {
			a.setModel(fields[1])
			return true
		}
		a.openModelSelector()
		return true
	case "mode":
		if len(fields) > 1 && session.IsValidMode(fields[1]) {
			a.applyMode(fields[1])
			return true
		}
		a.openModeSelector()
		return true
	case "resume":
		a.openResumeSelector()
		return true
	case "new":
		a.newSession()
		return true
	case "theme":
		a.openThemeSelector()
		return true
	case "hotkeys":
		a.showHotkeys()
		return true
	case "quit", "exit":
		a.requestQuit(nil)
		return true
	}
	return false
}

// applyMode switches the session mode through the manager.
func (a *App) applyMode(mode string) {
	sessionID := a.sessionID
	go func() {
		if err := a.mgr.HandleSessionSetMode(context.Background(), acp.SessionSetModeParams{SessionID: sessionID, ModeID: mode}); err != nil {
			_ = a.Sender().SendSessionUpdate(sessionID, statusErr{msg: "mode: " + err.Error()})
		}
	}()
}

func (a *App) openModeSelector() {
	if a.busyWithLocalShell() {
		return
	}
	items := []tui.SelectItem{
		{Value: "agent", Label: "agent", Description: "Full tool access"},
		{Value: "plan", Label: "plan", Description: "Read-only planning tools"},
		{Value: "ask", Label: "ask", Description: "Read-only research and answers"},
	}
	sel := newSelectorModal(a.theme, "Select mode", items, 4, a.screen.RequestRender)
	for i, it := range items {
		if it.Value == a.modeID {
			sel.list.SetSelectedIndex(i)
			break
		}
	}
	sel.OnDone = func(item *tui.SelectItem) {
		a.closeModal()
		if item == nil {
			a.screen.RequestRender()
			return
		}
		a.applyMode(item.Value)
	}
	a.openModal(sel)
}

func (a *App) openThemeSelector() {
	if a.busyWithLocalShell() {
		return
	}
	items := []tui.SelectItem{
		{Value: "dark", Label: "dark", Description: "Coddy dark palette"},
		{Value: "light", Label: "light", Description: "Coddy light palette"},
	}
	sel := newSelectorModal(a.theme, "Select theme", items, 4, a.screen.RequestRender)
	sel.OnDone = func(item *tui.SelectItem) {
		a.closeModal()
		if item != nil && item.Value != a.themeName {
			a.switchTheme(item.Value)
		}
		a.screen.RequestRender()
	}
	a.openModal(sel)
}

// switchTheme rebuilds themed content in place, preserving the editor text.
func (a *App) switchTheme(name string) {
	pendingText := a.editor.Text()
	a.applyTheme(name)
	// Rebuild the static chrome; transcript components keep their pre-baked
	// colors (documented v1 limitation, matches a fresh-session restart).
	a.header = newHeader(a.theme)
	a.header.SetExpanded(a.expanded)
	a.populateHeader()
	a.foot = newFooter(a.theme, a.cfg.Paths.CWD)
	a.refreshFooterModel()
	a.foot.SetSession("", a.modeID)
	a.editor = tui.NewEditor(a.term, tui.EditorTheme{BorderColor: a.theme.FgFn(roleBorderMuted)}, 0)
	a.editor.OnSubmit = a.onSubmit
	a.editor.OnChange = a.onEditorChange
	provider := newCompletionProvider(a.cfg.Paths.CWD, a.slashCatalog)
	a.editor.SetAutocomplete(provider, selectListTheme(a.theme), tui.SelectListLayout{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 32}, a.screen.RequestRender)

	root := a.screen.Root
	root.Clear()
	root.AddChild(a.header)
	root.AddChild(a.chat)
	root.AddChild(a.status)
	root.AddChild(a.plan)
	a.editorWrap = &tui.Container{}
	a.editorWrap.AddChild(a.editor)
	root.AddChild(a.editorWrap)
	root.AddChild(a.foot)
	if pendingText != "" {
		a.editor.SetText(pendingText)
	}
	a.screen.SetFocus(a.editor)
	a.screen.Invalidate()
}

func (a *App) openResumeSelector() {
	// Refuse before the picker opens, not after a session is chosen.
	if a.busyWithLocalShell() {
		return
	}
	sessionID := a.sessionID
	go func() {
		cwd := a.cfg.Paths.CWD
		res, err := a.mgr.HandleSessionList(context.Background(), acp.SessionListParams{CWD: &cwd})
		if err != nil {
			_ = a.Sender().SendSessionUpdate(sessionID, statusErr{msg: "resume: " + err.Error()})
			return
		}
		select {
		case a.updatesCh <- updateMsg{sessionID: sessionID, update: resumeList{res: res}}:
		case <-a.closed:
		}
	}()
}

func (a *App) showHotkeys() {
	lines := []string{
		"enter send · shift+enter/ctrl+j newline",
		"escape interrupt · ctrl+c clear/exit · ctrl+d exit",
		"ctrl+l model selector · ctrl+p cycle models",
		"shift+tab cycle reasoning · ctrl+t thinking · ctrl+o expand",
		"up/down prompt history · / commands · @ file mention",
		"!!<command> run it here, hidden from the agent",
	}
	a.appendStatus(roleDim, strings.Join(lines, "\n"))
}

// resumeList is an internal update carrying the /resume picker data.
type resumeList struct{ res *acp.SessionListResult }

// openResumePicker shows the /resume selector over the fetched session list.
func (a *App) openResumePicker(res *acp.SessionListResult) {
	// The list arrives asynchronously: a command may have started since.
	if a.busyWithLocalShell() {
		return
	}
	if res == nil || len(res.Sessions) == 0 {
		a.appendStatus(roleDim, "No sessions to resume in this folder")
		return
	}
	items := make([]tui.SelectItem, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		label := s.SessionID
		if s.Title != nil && *s.Title != "" {
			label = tui.SanitizeText(*s.Title)
		}
		desc := shortSessionID(s.SessionID)
		if s.UpdatedAt != nil && *s.UpdatedAt != "" {
			desc = relativeTime(*s.UpdatedAt) + " · " + desc
		}
		items = append(items, tui.SelectItem{Value: s.SessionID, Label: label, Description: desc})
	}
	sel := newSelectorModal(a.theme, "Resume Session", items, 10, a.screen.RequestRender)
	sel.OnDone = func(item *tui.SelectItem) {
		a.closeModal()
		if item == nil {
			a.screen.RequestRender()
			return
		}
		a.resumeInto(item.Value)
	}
	a.openModal(sel)
}

// resumeInto switches the transcript to an existing session, serialized the
// same way as /new: never while another switch runs, and only after a live
// turn has ended.
func (a *App) resumeInto(id string) {
	if a.switching {
		a.appendStatus(roleWarning, "A session switch is already in progress")
		return
	}
	if a.shellActive {
		a.appendStatus(roleWarning, "A local command is running (escape to stop it)")
		return
	}
	a.switching = true
	a.deferUntilTurnEnd(func() { a.startResumeWorker(a.sessionID, id) })
}

func (a *App) startResumeWorker(old, id string) {
	a.resetTranscript()
	a.sessionID = id
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		cwd := a.cfg.Paths.CWD
		res, err := a.mgr.HandleSessionLoad(a.workCtx, acp.SessionLoadParams{SessionID: id, CWD: cwd})
		if err != nil {
			_ = a.Sender().SendSessionUpdate(id, statusErr{msg: "resume: " + err.Error(), always: true})
			return
		}
		var modes *acp.ModeState
		var opts []acp.ConfigOption
		if res != nil {
			modes = res.Modes
			opts = res.ConfigOptions
		}
		select {
		case a.updatesCh <- updateMsg{sessionID: id, update: sessionResumed{id: id, modes: modes, opts: opts}}:
		case <-a.closed:
		}
		if old != "" && old != id {
			a.mgr.ForgetLiveSession(old)
		}
		a.mgr.HandleSessionReady(id)
	}()
}

// sessionResumed is an internal update completing /resume.
type sessionResumed struct {
	id    string
	modes *acp.ModeState
	opts  []acp.ConfigOption
}

// shortSessionID trims a session id to a readable prefix.
func shortSessionID(id string) string {
	if len(id) > 16 {
		return id[:16]
	}
	return id
}

// relativeTime renders an RFC3339 timestamp as a coarse "Nx ago" label.
func relativeTime(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
}
