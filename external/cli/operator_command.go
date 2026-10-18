//go:build cli

package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// opRequest is an operator command waiting for the operator: the request the
// modal shows and the ticket the answer goes into.
type opRequest struct {
	ctx    context.Context
	req    acp.OperatorCommandRequest
	ticket *acp.OperatorCommandTicket
}

var errConsoleClosed = errors.New("the console closed")

// OperatorCommandsAvailable implements acp.OperatorCommandSender: the console
// has a person in front of it. In remote mode the server asks only a console
// that declared it answers (internal/remote), so this holds there too.
func (s *sender) OperatorCommandsAvailable() bool {
	return s != nil && s.app != nil
}

// RequestOperatorCommand shows the modal and blocks until the operator runs
// or declines. A run keeps the ticket in the app, so escape stops the command
// until the tool reports it ended.
func (s *sender) RequestOperatorCommand(ctx context.Context, req acp.OperatorCommandRequest) (*acp.OperatorCommandDecision, error) {
	a := s.app
	tk := acp.NewOperatorCommandTicket()
	select {
	case a.opCh <- opRequest{ctx: ctx, req: req, ticket: tk}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.closed:
		return nil, errConsoleClosed
	}
	go func() {
		// A console that quits leaves nobody to answer or to stop: a
		// waiting request is declined, a command it ran is killed.
		select {
		case <-a.closed:
			switch tk.State() {
			case acp.OperatorStateRunning, acp.OperatorStateStopping:
				_ = tk.Answer(acp.OperatorActionStop, "")
				_ = tk.Answer(acp.OperatorActionStop, "")
			default:
				_ = tk.Answer(acp.OperatorActionDecline, "")
			}
		case <-tk.Finished():
		}
	}()
	d, err := tk.Wait(ctx)
	if err != nil {
		// A Run pressed as the turn was cancelled: the tool never gets it,
		// so the console must not keep the ticket for escape.
		a.postUI(func() { a.operatorEnded(tk) })
		return nil, err
	}
	if d.Action == acp.OperatorActionRun {
		go func() {
			<-tk.Finished()
			a.postUI(func() { a.operatorEnded(tk) })
		}()
	}
	return d, nil
}

// operatorRunning hands escape the ticket of the command the operator just
// ran in sessionID; it is set by the Run itself, on the UI loop, so an escape
// pressed right after it stops the command rather than the turn.
func (a *App) operatorRunning(tk *acp.OperatorCommandTicket, sessionID string) {
	a.opTicket, a.opSession = tk, sessionID
}

func (a *App) operatorEnded(tk *acp.OperatorCommandTicket) {
	if a.opTicket == tk {
		a.opTicket, a.opSession = nil, ""
	}
}

// stopOperatorCommand is escape while the operator's command runs: the first
// stops it (Ctrl+C; the turn goes on with what it printed), the second kills
// it. It reports whether escape was taken.
func (a *App) stopOperatorCommand() bool {
	tk := a.opTicket
	// Only the command of the session on screen: escape in another session
	// is about that one.
	if tk == nil || a.opSession != a.sessionID {
		return false
	}
	switch tk.State() {
	case acp.OperatorStateRunning:
		_ = tk.Answer(acp.OperatorActionStop, "")
		a.appendStatus(roleWarning, "Stopping your command (escape again to kill it)")
		return true
	case acp.OperatorStateStopping:
		_ = tk.Answer(acp.OperatorActionStop, "")
		a.appendStatus(roleWarning, "Killing your command")
		return true
	}
	return false
}

func (a *App) openOperatorModal(req opRequest) {
	a.showGate(req.ctx, func() {
		a.blockStatus("Waiting for you to run or decline a command")
		m := newOperatorModal(a.theme, req.req)
		m.OnDone = func(action string) {
			if req.ticket.Answer(action, "") == nil && action == acp.OperatorActionRun {
				a.operatorRunning(req.ticket, req.req.SessionID)
			}
			a.closeModal()
			a.screen.RequestRender()
		}
		a.openModal(m)
	})
}

// operatorModal shows an operator command exactly as it will run and takes
// Run or Decline. The selection starts on Decline: an Enter typed before the
// modal appeared never runs anything, and no letter key decides.
type operatorModal struct {
	tui.Container

	list *tui.SelectList
	done bool

	OnDone func(action string)
}

func newOperatorModal(theme *tui.Theme, req acp.OperatorCommandRequest) *operatorModal {
	m := &operatorModal{}
	items := []tui.SelectItem{
		{Value: acp.OperatorActionDecline, Label: "Decline"},
		{Value: acp.OperatorActionRun, Label: "Run"},
	}
	m.list = tui.NewSelectList(items, 3, selectListTheme(theme), tui.SelectListLayout{})
	m.list.OnSelect = func(item tui.SelectItem) { m.finish(item.Value) }
	m.list.OnCancel = func() { m.finish(acp.OperatorActionDecline) }

	th := theme
	m.AddChild(tui.NewSpacer(1))
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleWarning)))
	m.AddChild(tui.NewText(th.Fg(roleWarning, th.Bold("Run this command yourself?")), 1, 0, nil))
	m.AddChild(tui.NewText(th.Fg(roleMuted, "The agent was refused this command and asks you to run it, exactly as shown."), 1, 0, nil))
	// The executable too: a shell swapped through the configuration must show.
	shell := tui.SanitizeText(req.Shell)
	if req.ShellPath != "" {
		shell += " (" + tui.SanitizeText(req.ShellPath) + ")"
	}
	where := "shell " + shell + " · host " + tui.SanitizeText(req.Host)
	if req.Multiplexer != "" {
		where += " · in " + tui.SanitizeText(req.Multiplexer)
	}
	m.AddChild(tui.NewText(th.Fg(roleDim, where), 1, 0, nil))
	m.AddChild(tui.NewText(th.Fg(roleDim, "folder "+tui.SanitizeText(req.CWD)), 1, 0, nil))
	if reason := strings.TrimSpace(req.Reason); reason != "" {
		m.AddChild(tui.NewText(th.Fg(roleText, "Agent's reason: "+tui.SanitizeText(reason)), 1, 0, nil))
	}
	m.AddChild(tui.NewSpacer(1))
	// The whole command, every line: the tool refused one whose characters
	// would read differently, so this is what the shell gets.
	for i, line := range strings.Split(req.Command, "\n") {
		prefix := "$ "
		if i > 0 {
			prefix = "  "
		}
		m.AddChild(tui.NewText(th.Fg(roleAccent, prefix+tui.SanitizeText(line)), 1, 0, nil))
	}
	if req.AttachCommand != "" {
		m.AddChild(tui.NewText(th.Fg(roleDim, "terminal: "+tui.SanitizeText(req.AttachCommand)), 1, 0, nil))
	}
	m.AddChild(tui.NewSpacer(1))
	m.AddChild(m.list)
	m.AddChild(tui.NewText(th.Fg(roleDim, "↑↓ navigate · enter choose · esc decline"), 1, 0, nil))
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleWarning)))
	return m
}

func (m *operatorModal) finish(action string) {
	if m.done {
		return
	}
	m.done = true
	if m.OnDone != nil {
		m.OnDone(action)
	}
}

// HandleInput forwards to the list. No letter is a decision: the console
// takes keys while a turn runs, and one typed for the editor as the modal
// opened must not run a command; Run is the second row and Enter.
func (m *operatorModal) HandleInput(data []byte) { m.list.HandleInput(data) }

// operatorFrameOutput is the output window a frame of a running operator
// command carries, if it is one.
func operatorFrameOutput(meta map[string]interface{}) (string, bool) {
	coddy, ok := meta["coddy"].(map[string]interface{})
	if !ok {
		return "", false
	}
	oc, ok := coddy["operatorCommand"].(map[string]interface{})
	if !ok {
		return "", false
	}
	out, _ := oc["output"].(string)
	return out, true
}
