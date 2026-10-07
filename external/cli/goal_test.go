//go:build cli

package cli

// Edges of the session goal on the console (goal.go). The happy path - a goal
// set, continued, paused, resumed, blocked, found again on resume and cleared -
// is features/cli_session_goal.feature.

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const keyDown = "\x1b[B"

// goalStand is a console over a real session manager: its runner announces a
// goal turn the way the agent does and ends it at once, and the supervisor's
// check always answers that it needs the operator.
type goalStand struct {
	app *App
	mgr *session.Manager
	sid string

	mu      sync.Mutex
	markers []*llm.GoalTurn
}

func newGoalStand(t *testing.T) *goalStand {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: home},
		Models: []config.ModelEntry{{Model: "stub/model", MaxTokens: 100, MaxContextTokens: 1000}},
		Agent:  config.Agent{Model: "stub/model"},
	}
	g := &goalStand{}
	late := &lateBoundSender{}
	log := slog.New(slog.DiscardHandler)
	g.mgr = session.NewManager(cfg, late, g.run, log, home, &session.FileStore{Root: filepath.Join(home, "sessions")})
	g.mgr.SetGoalJudge(func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		return session.GoalCheckResult{Verdict: session.GoalVerdictNeedsUser, Reason: "which database?"}, nil
	})
	g.app = newApp(cfg, g.mgr, log, &bddTerminal{cols: 100, rows: 30}, "dark", true)
	late.inner = g.app.Sender()
	t.Cleanup(func() {
		g.app.Close()
		g.app.JoinWorkers(3e9)
	})
	res, err := g.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	g.sid = res.SessionID
	g.app.adoptSession(res.SessionID, res.Modes, res.ConfigOptions)
	return g
}

func (g *goalStand) run(_ context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
	marker := st.TakeTurnGoal()
	if marker != nil {
		_ = snd.SendSessionUpdate(st.GetID(), session.GoalTurnUpdate(marker))
	}
	g.mu.Lock()
	g.markers = append(g.markers, marker)
	g.mu.Unlock()
	text := ""
	for _, b := range prompt {
		text += b.Text
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text, GoalTurn: marker})
	return string(acp.StopReasonEndTurn), nil
}

func (g *goalStand) turns() []*llm.GoalTurn {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]*llm.GoalTurn(nil), g.markers...)
}

// setGoal gives the session a goal without a turn and waits for the console
// to show it.
func (g *goalStand) setGoal(t *testing.T, objective string) {
	t.Helper()
	if _, err := g.mgr.SetGoalObjective(g.sid, objective); err != nil {
		t.Fatal(err)
	}
	pumpControls(t, g.app, func(updateMsg) bool { return g.app.goal != nil })
}

// submit types text into the editor and presses enter.
func (g *goalStand) submit(text string) {
	g.app.editor.SetText(text)
	g.app.dispatchInput([]byte("\r"))
}

func (g *goalStand) openMenu(t *testing.T) string {
	t.Helper()
	g.submit("/goal")
	pumpControls(t, g.app, func(updateMsg) bool { return g.app.modal != nil })
	return modalText(t, g.app)
}

func (g *goalStand) waitTurnDone(t *testing.T) {
	t.Helper()
	pumpControls(t, g.app, func(msg updateMsg) bool {
		_, done := msg.update.(turnDone)
		return done
	})
}

func (g *goalStand) footer() string {
	return plain(strings.Join(g.app.foot.Render(120), "\n"))
}

func (g *goalStand) managerGoal(t *testing.T) *acp.SessionGoal {
	t.Helper()
	u, err := g.mgr.SessionGoal(g.sid)
	if err != nil {
		t.Fatal(err)
	}
	return u.Goal
}

// The footer names the goal in the colour of its status, in both palettes.
func TestFooterNamesTheGoalStatusInItsColour(t *testing.T) {
	for _, name := range []string{"dark", "light"} {
		th := newTheme(name)
		if th.Fg(roleAccent, "x") == th.Fg(roleWarning, "x") {
			t.Fatalf("%s: the palette does not tell the roles apart", name)
		}
		for _, tc := range []struct {
			goal acp.SessionGoal
			note string
			role string
		}{
			{acp.SessionGoal{Status: acp.GoalStatusActive, Continuations: 2, MaxContinuations: 10}, " • ◎ goal active 2/10 (/goal)", roleAccent},
			{acp.SessionGoal{Status: acp.GoalStatusPaused, Continuations: 2, MaxContinuations: 10}, " • ◎ goal paused (/goal)", roleMuted},
			{acp.SessionGoal{Status: acp.GoalStatusBlocked}, " • ◎ goal blocked (/goal)", roleWarning},
			{acp.SessionGoal{Status: acp.GoalStatusComplete}, " • ◎ goal complete (/goal)", roleSuccess},
			{acp.SessionGoal{Status: acp.GoalStatusLimited, Continuations: 10, MaxContinuations: 10}, " • ◎ goal limited (/goal)", roleMuted},
		} {
			f := newFooter(th, "/work")
			goal := tc.goal
			goal.Objective = "ship the release"
			f.SetGoal(&goal)
			line := f.Render(120)[0]
			if !strings.Contains(line, th.Fg(tc.role, tc.note)) {
				t.Errorf("%s %s: footer = %q, want %q in %s", name, tc.goal.Status, plain(line), tc.note, tc.role)
			}
		}
		f := newFooter(th, "/work")
		if line := plain(f.Render(120)[0]); strings.Contains(line, "goal") {
			t.Errorf("%s: a session without a goal names one: %q", name, line)
		}
	}
}

// On a narrow line the goal note gives up the command, then the progress, and
// leaves only where not even its status fits; the other notes stay whole and
// the line never overflows.
func TestFooterShortensTheGoalNoteOnANarrowLine(t *testing.T) {
	cwd := "/var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T/coddy-cli-bdd-1789734719342464000/work"
	for _, tc := range []struct {
		width   int
		want    string
		without string
	}{
		{100, "◎ goal active 2/10 (/goal)", ""},
		{62, "◎ goal active", "2/10"},
		{40, "", "goal"},
	} {
		f := newFooter(newTheme("dark"), cwd)
		f.SetRunningTasks(2)
		f.permission = "bypass"
		f.SetGoal(&acp.SessionGoal{Objective: "ship", Status: acp.GoalStatusActive, Continuations: 2, MaxContinuations: 10})
		line := plain(f.Render(tc.width)[0])
		if got := tui.VisibleWidth(line); got > tc.width {
			t.Fatalf("width %d: the line is %d cells wide:\n%s", tc.width, got, line)
		}
		for _, note := range []string{"2 tasks running (/tasks)", "bypass"} {
			if !strings.Contains(line, note) {
				t.Errorf("width %d: %q fell off the footer:\n%s", tc.width, note, line)
			}
		}
		if tc.want != "" && !strings.Contains(line, tc.want) {
			t.Errorf("width %d: footer %q lacks %q", tc.width, line, tc.want)
		}
		if tc.without != "" && strings.Contains(line, tc.without) {
			t.Errorf("width %d: footer %q still has %q", tc.width, line, tc.without)
		}
	}
}

// A theme switch rebuilds the footer; the goal and the rest of its state stay.
func TestThemeSwitchKeepsTheGoalAndThePermissionMode(t *testing.T) {
	a := newGoalStand(t).app
	a.applyGoalUpdate(acp.SessionGoalUpdate{SessionID: a.sessionID, Version: 3,
		Goal: &acp.SessionGoal{Objective: "ship", Status: acp.GoalStatusBlocked}})
	a.foot.SetSettings("bypass", nil)
	a.switchTheme("light")
	line := plain(a.foot.Render(120)[0])
	if !strings.Contains(line, "◎ goal blocked") || !strings.Contains(line, "bypass") {
		t.Fatalf("the footer after the switch = %q", line)
	}
	if a.foot.theme != a.theme {
		t.Fatal("the footer kept the old palette")
	}
}

// A goal turn is a row: the note every surface shows, the work the check found
// remaining under it, a long objective cut to one line.
func TestGoalTurnRowsNameTheTurnAndWhatRemains(t *testing.T) {
	a := newTestApp(t)
	a.sessionID = "sess_goal"
	a.applyLoopMessage(updateMsg{sessionID: "sess_goal", update: acp.GoalTurnUpdate{
		SessionUpdate: acp.UpdateTypeGoalTurn, Kind: acp.GoalTurnContinue, Index: 2, Limit: 10,
		Objective: "ship", Reason: "the linter fails", Remaining: []string{"fix lint in a.go", "run the tests"},
	}})
	text := transcriptText(a)
	for _, want := range []string{"◎ Goal continuation 2 of 10: the linter fails", "  - fix lint in a.go", "  - run the tests"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the transcript lacks %q:\n%s", want, text)
		}
	}

	long := strings.Repeat("write the whole changelog\n", 40)
	line := goalTurnLine(acp.GoalTurnUpdate{Kind: acp.GoalTurnKickoff, Objective: long})
	if strings.Contains(line, "\n") || !strings.HasPrefix(line, "◎ Goal set: write the whole changelog write") || !strings.HasSuffix(line, "…") {
		t.Fatalf("kickoff row = %q", line)
	}
	if n := utf8.RuneCountInString(line); n > goalRowObjectiveRunes+len("◎ Goal set: ")+1 {
		t.Fatalf("kickoff row is %d runes long", n)
	}
}

// A change reaches a remote console twice - the turn's stream and the events
// stream - with one version: the notice is printed once. An older snapshot is
// not adopted, though its notice reports a change not shown yet; another
// session's update is not this one's.
func TestGoalUpdatesPrintTheirNoticeOnceAndKeepTheNewestGoal(t *testing.T) {
	a := newTestApp(t)
	a.sessionID = "sess_goal"
	apply := func(sid string, u acp.SessionGoalUpdate) {
		u.SessionUpdate = acp.UpdateTypeSessionGoal
		a.applyLoopMessage(updateMsg{sessionID: sid, update: u})
	}
	paused := acp.SessionGoalUpdate{SessionID: "sess_goal", Version: 5, Notice: "Goal paused: ship",
		Goal: &acp.SessionGoal{Objective: "ship", Status: acp.GoalStatusPaused}}
	apply("sess_goal", paused)
	apply("sess_goal", paused)
	if n := strings.Count(transcriptText(a), "Goal paused: ship"); n != 1 {
		t.Fatalf("the notice is printed %d times:\n%s", n, transcriptText(a))
	}
	apply("sess_goal", acp.SessionGoalUpdate{SessionID: "sess_goal", Version: 4, Notice: "Goal check deferred: background work is still running",
		Goal: &acp.SessionGoal{Objective: "ship", Status: acp.GoalStatusActive}})
	if !strings.Contains(transcriptText(a), "Goal check deferred") {
		t.Fatalf("the older update's notice is lost:\n%s", transcriptText(a))
	}
	if a.goal == nil || a.goal.Status != acp.GoalStatusPaused {
		t.Fatalf("an older snapshot replaced the goal: %+v", a.goal)
	}
	apply("sess_other", acp.SessionGoalUpdate{SessionID: "sess_other", Version: 9, Notice: "Goal set: other",
		Goal: &acp.SessionGoal{Objective: "other", Status: acp.GoalStatusActive}})
	if strings.Contains(transcriptText(a), "Goal set: other") || a.goal.Objective != "ship" {
		t.Fatal("another session's goal reached this one")
	}
	apply("sess_goal", acp.SessionGoalUpdate{SessionID: "sess_goal", Version: 6, Notice: "Goal cleared: ship"})
	if a.goal != nil || strings.Contains(plain(a.foot.Render(120)[0]), "goal") {
		t.Fatalf("a cleared goal is still shown: %+v", a.goal)
	}
}

// What the menu offers follows the status: Pause while the supervisor works on
// the goal, Resume while it is held, nothing to resume once it is complete.
func TestGoalMenuOffersWhatTheStatusAllows(t *testing.T) {
	values := func(items []tui.SelectItem) string {
		var out []string
		for _, it := range items {
			out = append(out, it.Value)
		}
		return strings.Join(out, ",")
	}
	for status, want := range map[string]string{
		acp.GoalStatusActive:   "pause,clear,close",
		acp.GoalStatusPaused:   "resume,clear,close",
		acp.GoalStatusBlocked:  "resume,clear,close",
		acp.GoalStatusLimited:  "resume,clear,close",
		acp.GoalStatusComplete: "clear,close",
	} {
		if got := values(goalMenuItems(&acp.SessionGoal{Objective: "x", Status: status})); got != want {
			t.Errorf("%s: items %s, want %s", status, got, want)
		}
	}
	if got := values(goalMenuItems(nil)); got != "close" {
		t.Errorf("no goal: items %s, want close", got)
	}
}

// The menu says everything the supervisor keeps about the goal.
func TestGoalMenuBodyShowsTheCheckTheChecklistAndTheNumbers(t *testing.T) {
	body := plain(goalMenuBody(newTheme("dark"), &acp.SessionGoal{
		Objective: "ship the release", Status: acp.GoalStatusBlocked, StatusReason: "which branch?",
		Continuations: 3, MaxContinuations: 10, Checks: 4, ActiveMs: 125000, TokensUsed: 12300, TokenBudget: 200000,
		LastCheck: &acp.GoalCheck{Verdict: "needs_user", Reason: "the branch is not named", Remaining: []string{"tag the release"}, Verified: true},
		Checklist: []acp.GoalItem{{Text: "changelog written", Status: "met"}, {Text: "tag pushed", Status: "not_met"}, {Text: "notes sent", Status: "unverified"}},
	}))
	for _, want := range []string{
		"ship the release",
		"Status: blocked · which branch?",
		"Last check: needs user, verified · the branch is not named",
		"  - tag the release",
		"✓ changelog written", "✗ tag pushed", "? notes sent",
		"continuations 3 of 10 · 4 checks · active 2m 05s · tokens 12.3k of 200k",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the menu body lacks %q:\n%s", want, body)
		}
	}
}

func TestGoalMenuBodyNamesTheChecker(t *testing.T) {
	for _, tc := range []struct {
		goal acp.SessionGoal
		want string
	}{
		{acp.SessionGoal{Objective: "x", Status: "active", Model: "hub/qwen", Reasoning: "high"}, "Checked by: hub/qwen\nReasoning: high"},
		{acp.SessionGoal{Objective: "x", Status: "active", Reasoning: "low"}, "Checked by: the session model\nReasoning: low"},
		{acp.SessionGoal{Objective: "x", Status: "active", LastCheck: &acp.GoalCheck{Verdict: "met", Model: "p/judge"}}, "Checked by: p/judge\ncontinuations"},
		// The server's resolved checker: the model's default level, as it is.
		{acp.SessionGoal{Objective: "x", Status: "active", CheckModel: "hub/think", CheckReasoning: "medium"}, "Checked by: hub/think\nReasoning: medium"},
		{acp.SessionGoal{Objective: "x", Status: "active", CheckModel: "hub/think", CheckReasoning: "default"}, "Checked by: hub/think\nReasoning: default"},
	} {
		if body := plain(goalMenuBody(newTheme("dark"), &tc.goal)); !strings.Contains(body, tc.want) {
			t.Errorf("the menu body lacks %q:\n%s", tc.want, body)
		}
	}
	if body := plain(goalMenuBody(newTheme("dark"), &acp.SessionGoal{Objective: "x", Status: "active"})); strings.Contains(body, "Checked by") {
		t.Errorf("a goal with no checker known names one:\n%s", body)
	}
}

// A bare /goal opens the menu and sends nothing: no turn starts, no prompt
// reaches the session.
func TestBareGoalOpensTheMenuAndSendsNothing(t *testing.T) {
	g := newGoalStand(t)
	g.setGoal(t, "ship the release notes")
	text := g.openMenu(t)
	for _, want := range []string{"Session goal", "ship the release notes", "Status: active", "Pause", "Clear", "Close"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the goal menu lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Resume") {
		t.Fatalf("an active goal offers Resume:\n%s", text)
	}
	if g.app.turnActive || len(g.turns()) != 0 {
		t.Fatalf("a bare /goal started a turn (active %v, turns %d)", g.app.turnActive, len(g.turns()))
	}
}

// Without a goal the menu says how to set one and offers nothing else.
func TestGoalMenuWithoutAGoalSaysHowToSetOne(t *testing.T) {
	g := newGoalStand(t)
	text := g.openMenu(t)
	if !strings.Contains(text, "/goal <objective> sets one") || strings.Contains(text, "Clear") {
		t.Fatalf("the menu without a goal:\n%s", text)
	}
	g.app.dispatchInput([]byte("\r"))
	if g.app.modal != nil || len(g.turns()) != 0 {
		t.Fatal("Close did not just close the menu")
	}
}

// Pause goes to the backend; the footer follows and the notice is printed once.
func TestGoalMenuPausesTheGoal(t *testing.T) {
	g := newGoalStand(t)
	g.setGoal(t, "ship the release notes")
	g.openMenu(t)
	g.app.dispatchInput([]byte("\r")) // Pause is the first action
	pumpControls(t, g.app, func(updateMsg) bool {
		return g.app.goal != nil && g.app.goal.Status == acp.GoalStatusPaused
	})
	if got := g.managerGoal(t); got == nil || got.Status != acp.GoalStatusPaused {
		t.Fatalf("the session's goal = %+v, want paused", got)
	}
	if n := strings.Count(transcriptText(g.app), "Goal paused: ship the release notes"); n != 1 {
		t.Fatalf("the pause notice is printed %d times:\n%s", n, transcriptText(g.app))
	}
	if !strings.Contains(g.footer(), "◎ goal paused") {
		t.Fatalf("footer = %q", g.footer())
	}
}

// Clear asks first: Cancel goes back to the menu with the goal kept, the
// confirmation clears it.
func TestGoalMenuClearAsksFirst(t *testing.T) {
	g := newGoalStand(t)
	g.setGoal(t, "ship the release notes")
	g.openMenu(t)
	g.app.dispatchInput([]byte(keyDown))
	g.app.dispatchInput([]byte("\r")) // Clear
	if text := modalText(t, g.app); !strings.Contains(text, "Clear the goal?") || !strings.Contains(text, "ship the release notes") {
		t.Fatalf("Clear did not ask first:\n%s", text)
	}
	g.app.dispatchInput([]byte("\r")) // Cancel
	if text := modalText(t, g.app); !strings.Contains(text, "Session goal") {
		t.Fatalf("Cancel did not return to the goal menu:\n%s", text)
	}
	if g.managerGoal(t) == nil {
		t.Fatal("Cancel cleared the goal")
	}

	g.app.dispatchInput([]byte(keyDown))
	g.app.dispatchInput([]byte("\r")) // Clear
	g.app.dispatchInput([]byte(keyDown))
	g.app.dispatchInput([]byte("\r")) // Clear the goal
	pumpControls(t, g.app, func(updateMsg) bool { return g.app.goal == nil })
	if got := g.managerGoal(t); got != nil {
		t.Fatalf("the session still has a goal: %+v", got)
	}
	if n := strings.Count(transcriptText(g.app), "Goal cleared: ship the release notes"); n != 1 {
		t.Fatalf("the clear notice is printed %d times:\n%s", n, transcriptText(g.app))
	}
	if strings.Contains(g.footer(), "goal") {
		t.Fatalf("footer = %q", g.footer())
	}
}

// Resume submits /goal resume as if typed: the manager resumes the goal and
// starts its turn, whose row says so once - the command's own notice is not
// printed beside it.
func TestGoalMenuResumeSubmitsTheCommand(t *testing.T) {
	g := newGoalStand(t)
	g.setGoal(t, "ship the release notes")
	if _, err := g.mgr.PauseGoal(g.sid); err != nil {
		t.Fatal(err)
	}
	pumpControls(t, g.app, func(updateMsg) bool { return g.app.goal.Status == acp.GoalStatusPaused })
	text := g.openMenu(t)
	if !strings.Contains(text, "Resume") || strings.Contains(text, "Pause") {
		t.Fatalf("a paused goal's menu:\n%s", text)
	}
	g.app.dispatchInput([]byte("\r")) // Resume
	g.waitTurnDone(t)

	turns := g.turns()
	if len(turns) != 1 || turns[0] == nil || turns[0].Kind != acp.GoalTurnResume {
		t.Fatalf("the session ran %+v, want one resume turn", turns)
	}
	transcript := transcriptText(g.app)
	if !strings.Contains(transcript, goalResumePrompt) {
		t.Fatalf("the command is not echoed:\n%s", transcript)
	}
	if n := strings.Count(transcript, "Goal resumed: ship the release notes"); n != 1 || !strings.Contains(transcript, "◎ Goal resumed: ship the release notes") {
		t.Fatalf("the resumed goal is named %d times:\n%s", n, transcript)
	}
	// The supervisor checked the turn and needs the operator: that notice is
	// printed, in the warning colour of a goal that waits.
	if !strings.Contains(transcript, "Goal blocked: which database?") || !strings.Contains(g.footer(), "◎ goal blocked") {
		t.Fatalf("after the check:\n%s\nfooter: %s", transcript, g.footer())
	}
}

// /goal with an objective is a prompt: the manager sets the goal and starts its
// kickoff turn, whose row is the only line naming the new goal.
func TestTypedGoalCommandGoesToTheSession(t *testing.T) {
	g := newGoalStand(t)
	g.submit("/goal write the docs")
	g.waitTurnDone(t)
	turns := g.turns()
	if len(turns) != 1 || turns[0] == nil || turns[0].Kind != acp.GoalTurnKickoff || turns[0].Objective != "write the docs" {
		t.Fatalf("the session ran %+v, want the kickoff of the goal", turns)
	}
	transcript := transcriptText(g.app)
	if n := strings.Count(transcript, "Goal set: write the docs"); n != 1 || !strings.Contains(transcript, "◎ Goal set: write the docs") {
		t.Fatalf("the new goal is named %d times:\n%s", n, transcript)
	}
	if len(g.app.goalEchoes) != 0 {
		t.Fatal("the command's turn ended and the console still waits for its notice")
	}
}

// Entering another session takes its goal along: the footer of a fresh session
// names none.
func TestANewSessionShowsNoGoal(t *testing.T) {
	g := newGoalStand(t)
	g.setGoal(t, "ship the release notes")
	g.app.adoptSession("sess_other", nil, nil)
	if g.app.goal != nil || strings.Contains(g.footer(), "goal") {
		t.Fatalf("the goal of the session left is still shown: %q", g.footer())
	}
}

// A remote console hears the goal on the server's events stream; the copy of
// the same change does not print its notice twice.
func TestRemoteConsoleFollowsTheGoalOnTheEventsStream(t *testing.T) {
	f := newRemoteControlStand(t)
	frame := controlFrame("session_goal", `{"object":"coddy.session_goal","sessionId":"sess_shared",`+
		`"goal":{"id":"goal_1","objective":"ship it","status":"blocked","statusReason":"which branch?","continuations":2,"maxContinuations":10},`+
		`"version":12,"notice":"Goal blocked: which branch?"}`)
	f.syncEvents(t, frame+frame)
	if line := plain(f.app.foot.Render(120)[0]); !strings.Contains(line, "◎ goal blocked (/goal)") {
		t.Fatalf("footer = %q", line)
	}
	if n := strings.Count(transcriptText(f.app), "Goal blocked: which branch?"); n != 1 {
		t.Fatalf("the notice is printed %d times:\n%s", n, transcriptText(f.app))
	}
	held, err := f.h.SessionGoal(sharedControlSession)
	if err != nil || held.Goal == nil || held.Version != 12 {
		t.Fatalf("the remote client holds %+v (%v)", held, err)
	}
}

// A goal command sent from this console - typed, or queued while a turn runs
// - answers for the change it makes: its notice is not printed a second time,
// while the same change made elsewhere still is.
func TestGoalCommandsFromHereAreAnsweredOnce(t *testing.T) {
	g := newGoalStand(t)
	g.app.expectGoalEcho("/goal pause")
	g.app.expectGoalEcho("/goal clear")
	g.app.applyGoalUpdate(acp.SessionGoalUpdate{SessionUpdate: acp.UpdateTypeSessionGoal, Version: 901, Notice: "Goal paused: ship it"})
	g.app.applyGoalUpdate(acp.SessionGoalUpdate{SessionUpdate: acp.UpdateTypeSessionGoal, Version: 902, Notice: "Goal cleared: ship it"})
	g.app.applyGoalUpdate(acp.SessionGoalUpdate{SessionUpdate: acp.UpdateTypeSessionGoal, Version: 903, Notice: "Goal paused: from the browser"})
	g.app.applyGoalUpdate(acp.SessionGoalUpdate{SessionUpdate: acp.UpdateTypeSessionGoal, Version: 904, Notice: "Goal check: the supervisor is reviewing the turn"})
	text := transcriptText(g.app)
	if strings.Contains(text, "Goal paused: ship it") || strings.Contains(text, "Goal cleared: ship it") || strings.Contains(text, "Goal check:") {
		t.Fatalf("a notice the command answered for, or a check's progress, was printed:\n%s", text)
	}
	if !strings.Contains(text, "Goal paused: from the browser") {
		t.Fatalf("a change made elsewhere was not printed:\n%s", text)
	}
}
