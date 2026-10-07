//go:build cli

package cli

// The session goal on the console (docs/features/session-supervisor.md): the
// footer names its status, a goal turn the supervisor starts is a row in the
// transcript, and a bare /goal opens the goal menu, which reads the goal and
// pauses, resumes or clears it. /goal with anything after it is a prompt the
// session manager answers, as on every surface.

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// goalWorkTimeout bounds one read or change of the goal behind the menu.
const goalWorkTimeout = 30 * time.Second

// goalNoticeMemory is how many versions of noticed updates are remembered:
// the copies of one change arrive close together.
const goalNoticeMemory = 32

// goalRowObjectiveRunes caps the objective in a goal row and in the menu. The
// whole text of a long objective is the menu's to show, a row only names it.
const (
	goalRowObjectiveRunes  = 240
	goalMenuObjectiveRunes = 1200
)

// goalResumePrompt is what the menu's Resume sends, as if typed: resuming
// starts a turn, which only a prompt does.
const goalResumePrompt = "/goal resume"

// goalBackend returns the backend's goal surface: the remote client drives the
// server's routes itself, the in-process manager answers through managerGoals.
func (a *App) goalBackend() goalBackend {
	switch b := a.mgr.(type) {
	case goalBackend:
		return b
	case *session.Manager:
		return managerGoals{m: b}
	}
	return nil
}

// applyGoalUpdate adopts a session_goal update, whoever made the change: a
// command typed here, the menu, the supervisor, another surface. The notice is
// printed once per change - a remote change reaches the console down the
// turn's stream and the events stream alike, with the same version - and a
// snapshot older than the one on screen is not adopted.
func (a *App) applyGoalUpdate(u acp.SessionGoalUpdate) {
	// A goal set or resumed is named by its turn's row, the progress of a
	// check is the spinner's; what is left is how the goal ended or moved.
	if notice := session.GoalEndNote(u); notice != "" && a.firstGoalNotice(u.Version) {
		if a.takeGoalEcho(notice) {
			// The goal command typed or queued here answers itself: the
			// agent's line saying what the command did.
		} else {
			role := roleDim
			if u.Goal != nil && u.Goal.Status == acp.GoalStatusBlocked {
				role = roleWarning
			}
			a.appendStatus(role, notice)
		}
	}
	if u.Version != 0 && u.Version < a.goalVersion {
		return
	}
	a.goalVersion = u.Version
	a.setGoal(u.Goal)
}

// firstGoalNotice records that the notice of version v is being handled and
// reports whether it was not before. Version 0 is never remembered.
func (a *App) firstGoalNotice(v uint64) bool {
	if v == 0 {
		return true
	}
	for _, seen := range a.goalNoticed {
		if seen == v {
			return false
		}
	}
	a.goalNoticed = append(a.goalNoticed, v)
	if len(a.goalNoticed) > goalNoticeMemory {
		a.goalNoticed = a.goalNoticed[len(a.goalNoticed)-goalNoticeMemory:]
	}
	return true
}

// adoptGoalSnapshot shows the goal of a session just entered. Entering a
// session starts the version order over, like the settings snapshot.
func (a *App) adoptGoalSnapshot(u acp.SessionGoalUpdate) {
	a.goalVersion = 0
	u.Notice = ""
	a.applyGoalUpdate(u)
}

// resetGoal forgets the goal of the session the console leaves.
func (a *App) resetGoal() {
	a.goalVersion = 0
	a.goalNoticed = nil
	a.goalEchoes = nil
	a.setGoal(nil)
}

// setGoal puts a goal on screen: the footer, and the goal menu when it is open.
func (a *App) setGoal(g *acp.SessionGoal) {
	if g == nil {
		a.goal = nil
	} else {
		copied := *g
		a.goal = &copied
	}
	a.foot.SetGoal(a.goal)
	a.refreshGoalMenu()
	a.screen.RequestRender()
}

// goalCommandAnswersItself reports whether text is a /goal command whose turn
// says what it did: setting or resuming a goal opens with the goal row, a pause
// or a clear is answered by the agent's line. A bare /goal never reaches the
// manager from here.
// goalEchoPrefix is the notice a goal command's own answer already says: a
// pause and a clear answer with the line the change publishes. A set and a
// resume answer with their turn's row, whose notice GoalEndNote leaves out.
func goalEchoPrefix(text string) string {
	switch session.ParseGoalCommand(text).Kind {
	case session.GoalCommandPause:
		return "Goal paused:"
	case session.GoalCommandClear:
		return "Goal cleared:"
	}
	return ""
}

// expectGoalEcho remembers that a goal command sent from here will answer
// for the change it makes.
func (a *App) expectGoalEcho(text string) {
	if p := goalEchoPrefix(text); p != "" {
		a.goalEchoes = append(a.goalEchoes, p)
	}
}

// takeGoalEcho reports whether notice is the change a goal command sent from
// here makes, and forgets that command.
func (a *App) takeGoalEcho(notice string) bool {
	for i, p := range a.goalEchoes {
		if strings.HasPrefix(notice, p) {
			a.goalEchoes = append(a.goalEchoes[:i], a.goalEchoes[i+1:]...)
			return true
		}
	}
	return false
}

// goalTurnLine is the row of a goal turn: the target glyph and the note every
// surface shows, with the work the check found remaining under it.
func goalTurnLine(u acp.GoalTurnUpdate) string {
	u.Objective = clipGoalText(u.Objective, goalRowObjectiveRunes)
	var b strings.Builder
	b.WriteString("◎ ")
	b.WriteString(session.GoalTurnNote(u))
	for _, item := range u.Remaining {
		if item = clipGoalText(item, goalRowObjectiveRunes); item != "" {
			b.WriteString("\n  - ")
			b.WriteString(item)
		}
	}
	return b.String()
}

// applyGoalTurn renders the first update of a goal turn, live or replayed. The
// instruction the model reads is not shown; the turn's answer is a block of
// its own.
func (a *App) applyGoalTurn(u acp.GoalTurnUpdate) {
	a.curAssistant = nil
	a.appendStatus(roleDim, goalTurnLine(u))
}

// clipGoalText folds a text to one line and cuts it to limit runes.
func clipGoalText(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// openGoalMenu answers a bare /goal: the goal of the session on screen, read
// afresh on a worker (over --remote it is a request), then the menu.
func (a *App) openGoalMenu() {
	if a.busyWithLocalShell() {
		return
	}
	gb := a.goalBackend()
	if gb == nil {
		a.appendStatus(roleWarning, "The session goal is unavailable")
		return
	}
	sessionID := a.sessionID
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ctx, cancel := context.WithTimeout(a.workCtx, goalWorkTimeout)
		defer cancel()
		u, err := gb.FetchSessionGoal(ctx, sessionID)
		a.postUI(func() {
			if sessionID != a.sessionID {
				return
			}
			if err != nil {
				// The menu still opens on what the console holds.
				a.appendStatus(roleWarning, "Goal: "+err.Error())
			} else {
				a.applyGoalUpdate(u)
			}
			a.showGoalMenu()
		})
	}()
}

// showGoalMenu opens the goal menu over the goal on screen. A prompt that came
// up while the goal was read keeps the screen: the menu can be opened again.
func (a *App) showGoalMenu() {
	if a.modal != nil {
		return
	}
	sel := newSelectorModal(a.theme, "Session goal", goalMenuItems(a.goal), 4, a.screen.RequestRender)
	sel.help = "↑↓ navigate · enter select · esc close"
	sel.body = goalMenuBody(a.theme, a.goal)
	sel.rebuild()
	sel.OnDone = func(item *tui.SelectItem) {
		a.goalMenu = nil
		a.closeModal()
		if item != nil {
			switch item.Value {
			case "pause":
				a.pauseGoal()
			case "resume":
				// As if typed: resuming starts a turn, which only a prompt
				// does, and a turn already running queues it like any other.
				a.submitPrompt(goalResumePrompt)
			case "clear":
				a.confirmClearGoal()
			}
		}
		a.screen.RequestRender()
	}
	a.goalMenu = sel
	a.openModal(sel)
	a.screen.RequestRender()
}

// refreshGoalMenu follows a change of the goal in the menu on screen: the
// supervisor checks and continues while it is open. The highlighted action
// stays highlighted while it is still offered.
func (a *App) refreshGoalMenu() {
	sel := a.goalMenu
	if sel == nil || a.modal != tui.Component(sel) {
		return
	}
	keep := ""
	if it := sel.list.SelectedItem(); it != nil {
		keep = it.Value
	}
	items := goalMenuItems(a.goal)
	sel.filter = ""
	sel.list.SetItems(items)
	for i, it := range items {
		if it.Value == keep {
			sel.list.SetSelectedIndex(i)
			break
		}
	}
	sel.body = goalMenuBody(a.theme, a.goal)
	sel.rebuild()
}

// goalMenuItems are the actions the goal's status allows: Pause while the
// supervisor works on it, Resume while it is held (a complete goal is not
// resumed - a new objective replaces it), Clear whenever there is one.
func goalMenuItems(g *acp.SessionGoal) []tui.SelectItem {
	closeItem := tui.SelectItem{Value: "close", Label: "Close"}
	if g == nil {
		return []tui.SelectItem{closeItem}
	}
	var items []tui.SelectItem
	switch g.Status {
	case acp.GoalStatusActive:
		items = append(items, tui.SelectItem{Value: "pause", Label: "Pause",
			Description: "Stop the automatic continuations, keep the goal"})
	case acp.GoalStatusPaused, acp.GoalStatusBlocked, acp.GoalStatusLimited:
		items = append(items, tui.SelectItem{Value: "resume", Label: "Resume",
			Description: "Send " + goalResumePrompt + ": work on it again with a fresh budget"})
	}
	items = append(items,
		tui.SelectItem{Value: "clear", Label: "Clear", Description: "Remove the goal"},
		closeItem)
	return items
}

// goalMenuBody is what the menu says about the goal: the objective, the status
// and why, the supervisor's last check, the checklist it keeps and the numbers.
func goalMenuBody(th *tui.Theme, g *acp.SessionGoal) string {
	if g == nil {
		return th.Fg(roleMuted, "No goal is set. /goal <objective> sets one and starts working on it.")
	}
	var lines []string
	lines = append(lines, th.Fg(roleText, tui.SanitizeText(clipGoalText(g.Objective, goalMenuObjectiveRunes))))
	status := th.Fg(goalStatusRole(g.Status), tui.SanitizeText(g.Status))
	if reason := strings.TrimSpace(g.StatusReason); reason != "" {
		status += th.Fg(roleMuted, " · "+tui.SanitizeText(reason))
	}
	lines = append(lines, th.Fg(roleDim, "Status: ")+status)
	if c := g.LastCheck; c != nil {
		verdict := strings.ReplaceAll(tui.SanitizeText(c.Verdict), "_", " ")
		if c.Verified {
			verdict += ", verified"
		}
		line := th.Fg(roleDim, "Last check: ") + verdict
		if reason := strings.TrimSpace(c.Reason); reason != "" {
			line += th.Fg(roleMuted, " · "+tui.SanitizeText(reason))
		}
		lines = append(lines, line)
		for _, item := range c.Remaining {
			if item = strings.TrimSpace(item); item != "" {
				lines = append(lines, th.Fg(roleMuted, "  - "+tui.SanitizeText(item)))
			}
		}
	}
	if len(g.Checklist) > 0 {
		lines = append(lines, th.Fg(roleDim, "Checklist:"))
		for _, item := range g.Checklist {
			lines = append(lines, "  "+goalItemMark(th, item.Status)+" "+tui.SanitizeText(item.Text))
		}
	}
	if checker := goalChecker(g); checker != "" {
		lines = append(lines, th.Fg(roleDim, "Checked by: ")+tui.SanitizeText(checker))
	}
	lines = append(lines, th.Fg(roleDim, goalNumbers(g)))
	return strings.Join(lines, "\n")
}

// goalChecker names the model and the reasoning level that check the goal:
// what /goal --model and --reasoning chose, else the model that answered the
// last check; empty when neither is known.
func goalChecker(g *acp.SessionGoal) string {
	model := strings.TrimSpace(g.Model)
	if model == "" && g.LastCheck != nil {
		model = strings.TrimSpace(g.LastCheck.Model)
	}
	if level := strings.TrimSpace(g.Reasoning); level != "" {
		if model == "" {
			model = "the session model"
		}
		return model + ", reasoning " + level
	}
	return model
}

// goalItemMark is the mark of a checklist item: met, not met, not verified.
func goalItemMark(th *tui.Theme, status string) string {
	switch status {
	case "met":
		return th.Fg(roleSuccess, "✓")
	case "not_met":
		return th.Fg(roleError, "✗")
	default:
		return th.Fg(roleDim, "?")
	}
}

// goalNumbers is the goal's bookkeeping in one line: continuations used of the
// limit, checks, the time its turns ran and the tokens they spent.
func goalNumbers(g *acp.SessionGoal) string {
	parts := []string{
		"continuations " + itoa(g.Continuations) + " of " + itoa(g.MaxContinuations),
		itoa(g.Checks) + " " + plural(g.Checks, "check", "checks"),
		"active " + formatElapsed(time.Duration(g.ActiveMs)*time.Millisecond),
	}
	tokens := "tokens " + tui.FormatTokenCount(g.TokensUsed)
	if g.TokenBudget > 0 {
		tokens += " of " + tui.FormatTokenCount(g.TokenBudget)
	}
	parts = append(parts, tokens)
	return strings.Join(parts, " · ")
}

// confirmClearGoal asks before the goal goes: a cleared goal is not restored,
// only set again. Cancel returns to the menu.
func (a *App) confirmClearGoal() {
	if a.goal == nil {
		return
	}
	items := []tui.SelectItem{
		{Value: "no", Label: "Cancel"},
		{Value: "yes", Label: "Clear the goal"},
	}
	sel := newSelectorModal(a.theme, "Clear the goal?", items, 4, a.screen.RequestRender)
	sel.body = tui.SanitizeText(clipGoalText(a.goal.Objective, goalMenuObjectiveRunes))
	sel.rebuild()
	sel.OnDone = func(item *tui.SelectItem) {
		a.closeModal()
		if item == nil || item.Value != "yes" {
			a.showGoalMenu()
			return
		}
		a.clearGoal()
		a.screen.RequestRender()
	}
	a.openModal(sel)
}

// pauseGoal stops the automatic continuations through the backend.
func (a *App) pauseGoal() {
	a.runGoalChange(func(ctx context.Context, gb goalBackend, sessionID string) (acp.SessionGoalUpdate, error) {
		return gb.PauseSessionGoal(ctx, sessionID)
	})
}

// clearGoal removes the goal through the backend.
func (a *App) clearGoal() {
	a.runGoalChange(func(ctx context.Context, gb goalBackend, sessionID string) (acp.SessionGoalUpdate, error) {
		return gb.ClearSessionGoal(ctx, sessionID)
	})
}

// runGoalChange applies one change of the goal on a worker and adopts the goal
// it answers with. The change's notice arrives as a session_goal update of its
// own; a failure is reported in the transcript.
func (a *App) runGoalChange(change func(ctx context.Context, gb goalBackend, sessionID string) (acp.SessionGoalUpdate, error)) {
	gb := a.goalBackend()
	if gb == nil {
		a.appendStatus(roleWarning, "The session goal is unavailable")
		return
	}
	sessionID := a.sessionID
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ctx, cancel := context.WithTimeout(a.workCtx, goalWorkTimeout)
		defer cancel()
		u, err := change(ctx, gb, sessionID)
		a.postUI(func() {
			if sessionID != a.sessionID {
				return
			}
			if err != nil {
				a.appendStatus(roleError, "Goal: "+err.Error())
				return
			}
			a.applyGoalUpdate(u)
		})
	}()
}
