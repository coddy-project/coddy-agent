//go:build cli

package cli

import (
	"context"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
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

func (a *App) openModeSelector() {
	items := []tui.SelectItem{
		{Value: "agent", Label: "agent", Description: "Full tool access"},
		{Value: "plan", Label: "plan", Description: "Read-only planning tools"},
	}
	sel := newSelectorModal(a.theme, "Select mode", items, 4, a.screen.RequestRender)
	sel.OnDone = func(item *tui.SelectItem) {
		a.closeModal()
		if item == nil {
			a.screen.RequestRender()
			return
		}
		sessionID := a.sessionID
		mode := item.Value
		go func() {
			if err := a.mgr.HandleSessionSetMode(context.Background(), acp.SessionSetModeParams{SessionID: sessionID, ModeID: mode}); err != nil {
				_ = a.Sender().SendSessionUpdate(sessionID, statusErr{msg: "mode: " + err.Error()})
			}
		}()
	}
	a.openModal(sel)
}

func (a *App) openThemeSelector() {
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

// switchTheme rebuilds themed content in place.
func (a *App) switchTheme(name string) {
	a.applyTheme(name)
	// Rebuild the static chrome; transcript components keep their pre-baked
	// colors (documented v1 limitation, matches a fresh-session restart).
	a.header = newHeader(a.theme)
	a.header.SetExpanded(a.expanded)
	a.populateHeader()
	a.foot = newFooter(a.theme, a.mgr.Cfg().Paths.CWD)
	a.refreshFooterModel()
	a.foot.SetSession("", a.modeID)
	a.editor = tui.NewEditor(a.term, tui.EditorTheme{BorderColor: a.theme.FgFn(roleBorderMuted)}, 0)
	a.editor.OnSubmit = a.onSubmit
	provider := newCompletionProvider(a.mgr.Cfg().Paths.CWD, a.slashCatalog)
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
	a.screen.SetFocus(a.editor)
	a.screen.Invalidate()
}

func (a *App) openResumeSelector() {
	sessionID := a.sessionID
	go func() {
		cwd := a.mgr.Cfg().Paths.CWD
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
	}
	a.appendStatus(roleDim, strings.Join(lines, "\n"))
}

// resumeList is an internal update carrying the /resume picker data.
type resumeList struct{ res *acp.SessionListResult }

// openResumePicker shows the /resume selector over the fetched session list.
func (a *App) openResumePicker(res *acp.SessionListResult) {
	if res == nil || len(res.Sessions) == 0 {
		a.appendStatus(roleDim, "No sessions to resume in this folder")
		return
	}
	items := make([]tui.SelectItem, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		label := s.SessionID
		if s.Title != nil && *s.Title != "" {
			label = *s.Title
		}
		desc := s.SessionID
		if s.UpdatedAt != nil && *s.UpdatedAt != "" {
			desc = *s.UpdatedAt + " · " + s.SessionID
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

// resumeInto switches the transcript to an existing session.
func (a *App) resumeInto(id string) {
	old := a.sessionID
	if a.turnActive {
		a.mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: old})
		a.turnActive = false
		a.stopSpinner()
	}
	a.resetTranscript()
	a.sessionID = id
	go func() {
		cwd := a.mgr.Cfg().Paths.CWD
		res, err := a.mgr.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: id, CWD: cwd})
		if err != nil {
			_ = a.Sender().SendSessionUpdate(id, statusErr{msg: "resume: " + err.Error()})
			return
		}
		if res != nil && res.Modes != nil {
			select {
			case a.updatesCh <- updateMsg{sessionID: id, update: acp.ModeUpdate{SessionUpdate: "current_mode_update", CurrentModeID: res.Modes.CurrentModeID}}:
			case <-a.closed:
			}
		}
		if old != "" && old != id {
			a.mgr.ForgetLiveSession(old)
		}
		a.mgr.HandleSessionReady(id)
	}()
}
