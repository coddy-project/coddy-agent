package session

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
)

func TestGoalStatePersistsWithSession(t *testing.T) {
	fs := &FileStore{Root: t.TempDir()}
	dir, err := fs.EnsureLayout("sess_goal")
	if err != nil {
		t.Fatal(err)
	}
	st := &State{ID: "sess_goal", CWD: t.TempDir(), Mode: ModeAgent, SessionDir: dir}
	goal, err := NewGoal("ship the fix")
	if err != nil {
		t.Fatal(err)
	}
	goal.Continuations, goal.Checks, goal.TokensUsed = 2, 3, 500
	goal.LastCheck = &GoalCheck{Verdict: GoalVerdictNotMet, Reason: "tests fail", Remaining: []string{"fix TestX"}}
	goal.Checklist = []GoalItem{{Text: "tests pass", Status: "not_met"}}
	st.SetGoal(goal)
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Meta.Goal
	if got == nil || got.Objective != "ship the fix" || got.Status != GoalActive || got.Continuations != 2 ||
		got.LastCheck == nil || got.LastCheck.Remaining[0] != "fix TestX" || got.Checklist[0].Text != "tests pass" || got.ID == "" {
		t.Fatalf("persisted goal = %+v", got)
	}
}

func TestNewGoalValidatesTheObjective(t *testing.T) {
	if _, err := NewGoal("   "); err == nil {
		t.Fatal("an empty objective was accepted")
	}
	if _, err := NewGoal(strings.Repeat("я", MaxGoalObjectiveRunes+1)); err == nil || !strings.Contains(err.Error(), "4000") {
		t.Fatalf("an overlong objective: %v", err)
	}
	g, err := NewGoal(strings.Repeat("я", MaxGoalObjectiveRunes))
	if err != nil || g.Status != GoalActive || !g.Active() {
		t.Fatalf("goal=%+v err=%v", g, err)
	}
}

func TestParseGoalCommand(t *testing.T) {
	for text, want := range map[string]GoalCommand{
		"/goal":                 {Kind: GoalCommandShow},
		"  /goal  ":             {Kind: GoalCommandShow},
		"/goal clear":           {Kind: GoalCommandClear},
		"/goal STOP":            {Kind: GoalCommandClear},
		"/goal cancel":          {Kind: GoalCommandClear},
		"/goal pause":           {Kind: GoalCommandPause},
		"/goal resume":          {Kind: GoalCommandResume},
		"/goal ship the fix":    {Kind: GoalCommandSet, Objective: "ship the fix"},
		"/goal\nmulti\nline":    {Kind: GoalCommandSet, Objective: "multi\nline"},
		"/goal clear the cache": {Kind: GoalCommandSet, Objective: "clear the cache"},
		"/goal --model mini --reasoning high ship it":  {Kind: GoalCommandSet, Objective: "ship it", Model: "mini", Reasoning: "high"},
		"/goal --model=p2/gpt-4o-mini ship --model it": {Kind: GoalCommandSet, Objective: "ship --model it", Model: "p2/gpt-4o-mini"},
		"/goal --reasoning=low resume":                 {Kind: GoalCommandResume, Reasoning: "low"},
		"/goal --model":                                {Kind: GoalCommandSet, Err: "--model needs a value"},
		"/goal --model mini":                           {Kind: GoalCommandSet, Model: "mini", Err: "the goal objective is empty"},
		"/goal --frob x ship":                          {Kind: GoalCommandSet, Objective: "x ship", Err: "unknown option --frob"},
		"/goal --frob -m mini ship":                    {Kind: GoalCommandSet, Objective: "ship", Model: "mini", Err: "unknown option --frob"},
		"/goal -m= ship it":                            {Kind: GoalCommandSet, Objective: "ship it", Err: "--model needs a value"},
		"/goal -m mini -r high ship it":                {Kind: GoalCommandSet, Objective: "ship it", Model: "mini", Reasoning: "high"},
		"/goal -r=low -m=mini resume":                  {Kind: GoalCommandResume, Model: "mini", Reasoning: "low"},
		"/goal - fix the flaky test":                   {Kind: GoalCommandSet, Objective: "- fix the flaky test"},
		"/goal -x stays text":                          {Kind: GoalCommandSet, Objective: "-x stays text"},
		"/goal --model mini clear":                     {Kind: GoalCommandClear, Model: "mini", Err: "--model and --reasoning go with a new goal or /goal resume"},
		"/goal --then-file .coddy/next.md ship it":     {Kind: GoalCommandSet, Objective: "ship it", ThenFile: ".coddy/next.md"},
		"/goal --then-file=a.md -m mini ship it":       {Kind: GoalCommandSet, Objective: "ship it", Model: "mini", ThenFile: "a.md"},
		"/goal --then-file":                            {Kind: GoalCommandSet, Err: "--then-file needs a value"},
		"/goal --then-file a.md resume":                {Kind: GoalCommandResume, ThenFile: "a.md", Err: "--then-file goes with a new goal"},
		"/goal --then-file a.md clear":                 {Kind: GoalCommandClear, ThenFile: "a.md", Err: "--then-file goes with a new goal"},
		`/goal --then-file "my next.md" ship it`:       {Kind: GoalCommandSet, Objective: `next.md" ship it`, ThenFile: `"my`, Err: "--then-file takes a path without quotes or spaces"},
		"/goals":                                       {},
		"/goal-x":                                      {},
		"set a goal":                                   {},
	} {
		if got := ParseGoalCommand(text); got != want {
			t.Errorf("%q: got %+v, want %+v", text, got, want)
		}
	}
}

func TestGoalStatusText(t *testing.T) {
	if got := GoalStatusText(GoalState{}, GoalChecker{}, 10, 0); !strings.Contains(got, "No goal is set") {
		t.Fatalf("empty = %q", got)
	}
	g, _ := NewGoal("ship it")
	g.Status, g.StatusReason, g.Continuations = GoalBlocked, "which DB?", 3
	g.LastCheck = &GoalCheck{Verdict: GoalVerdictNeedsUser, Reason: "which DB?", Remaining: []string{"ask the operator"}}
	got := GoalStatusText(g, GoalChecker{Model: "p/think", Reasoning: "medium"}, 10, 0)
	for _, want := range []string{"Goal (blocked): ship it", "Reason: which DB?", "Checked by: p/think\nReasoning: medium", "3 of 10", "needs_user", "- ask the operator", "/goal resume"} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
}

// The checker a surface shows is the one the next check runs on: the goal's
// model and level, else supervisor.model or the session's model at that
// model's default level.
func TestGoalCheckerNamesTheModelAndItsLevel(t *testing.T) {
	levels := []string{"low", "medium", "high"}
	cfg := &config.Config{
		Models: []config.ModelEntry{
			{Model: "p/plain"},
			{Model: "p/think", ReasoningLevels: &levels, ReasoningDefault: "medium"},
			{Model: "p/nodefault", ReasoningLevels: &levels},
		},
		Agent: config.Agent{Model: "p/plain"},
	}
	st := &State{ID: "s", Mode: ModeAgent}
	g, _ := NewGoal("ship it")
	for _, tc := range []struct {
		name, supervisor, model, reasoning string
		want                               GoalChecker
	}{
		{name: "the session model without levels", want: GoalChecker{Model: "p/plain"}},
		{name: "the model's default level", supervisor: "p/think", want: GoalChecker{Model: "p/think", Reasoning: "medium"}},
		{name: "a level the goal chose", model: "p/think", reasoning: "high", want: GoalChecker{Model: "p/think", Reasoning: "high"}},
		{name: "levels without a configured default", supervisor: "p/nodefault", want: GoalChecker{Model: "p/nodefault", Reasoning: config.ReasoningDefault}},
	} {
		cfg.Supervisor.Model = tc.supervisor
		g.Model, g.Reasoning = tc.model, tc.reasoning
		if got := GoalCheckerFor(cfg, st, g); got != tc.want {
			t.Errorf("%s: checker = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestGoalSnapshotCarriesBudgetsAndTheLastCheck(t *testing.T) {
	g, _ := NewGoal("ship it")
	g.TokensUsed, g.TokensBase = 900, 400
	g.LastCheck = &GoalCheck{Verdict: GoalVerdictMet, Verified: true}
	budget, limit := 1000, 5
	snap := GoalSnapshot(g, &config.Config{Supervisor: config.Supervisor{TokenBudget: &budget, MaxContinuations: &limit}})
	if snap.TokensUsed != 500 || snap.TokenBudget != 1000 || snap.MaxContinuations != 5 || !snap.LastCheck.Verified || snap.Status != "active" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if GoalSnapshot(GoalState{}, nil) != nil {
		t.Fatal("no goal must snapshot to nil")
	}
}

// A chained goal says where the next task comes from and which sessions it
// links, on every surface: the snapshot and the text of a bare /goal.
func TestGoalSnapshotAndStatusNameTheChain(t *testing.T) {
	g, _ := NewGoal("ship it")
	g.ThenFile, g.ChainIndex, g.ContinuedFrom, g.ContinuedIn = ".coddy/next.md", 2, "sess_a", "sess_c"
	snap := GoalSnapshot(g, nil)
	if snap.ThenFile != ".coddy/next.md" || snap.ChainIndex != 2 || snap.ContinuedFrom != "sess_a" || snap.ContinuedIn != "sess_c" {
		t.Fatalf("snapshot = %+v", snap)
	}
	text := GoalStatusText(g, GoalChecker{}, 10, 0)
	for _, want := range []string{"Then: .coddy/next.md", "Continued from: sess_a", "Continued in: sess_c"} {
		if !strings.Contains(text, want) {
			t.Errorf("status lacks %q:\n%s", want, text)
		}
	}
}

// riskyGoal is a goal that carries what the risk gate keeps: where it started
// and which migration files /goal resume accepted.
func riskyGoal(t *testing.T) GoalState {
	t.Helper()
	goal, err := NewGoal("clean the stack")
	if err != nil {
		t.Fatal(err)
	}
	goal.Start = &GoalStart{Dir: "work", FileBaseline: gitws.FileBaseline{Head: "abc123", Dirty: map[string]string{"migrations/1.sql": "abc"}}}
	goal.AcceptedFiles = map[string]string{"migrations/2.sql": "def"}
	return goal
}

// A row is stored with the id of the goal only while that goal is active: a
// goal that is paused, blocked, limited or complete has no turns running, and
// a row stored then (a person's message, a note) is nobody's evidence.
func TestRowsAreStampedOnlyWhileTheGoalIsActive(t *testing.T) {
	st := &State{ID: "sess_stamp", Mode: ModeAgent}
	var want []string
	add := func(content, goalID string) {
		t.Helper()
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: content})
		want = append(want, goalID)
	}
	// A plan snapshot is a row of the history like any other, stored by its own
	// method: it is stamped by the same rule.
	addPlan := func(slug, goalID string) {
		t.Helper()
		st.AppendPlanDocument(plans.Document{Slug: slug, Name: slug})
		want = append(want, goalID)
	}
	setStatus := func(status GoalStatus) {
		st.UpdateGoal("", func(g *GoalState) bool { g.Status = status; return true })
	}

	add("no goal yet", "")
	addPlan("before-goal", "")
	first, _ := NewGoal("ship it")
	st.SetGoal(first)
	add("while active", first.ID)
	addPlan("while-active", first.ID)
	for _, status := range []GoalStatus{GoalPaused, GoalBlocked, GoalLimited, GoalComplete} {
		setStatus(status)
		add("while "+string(status), "")
		addPlan("while-"+string(status), "")
	}
	setStatus(GoalActive)
	add("resumed", first.ID)
	addPlan("resumed", first.ID)

	second, _ := NewGoal("ship it")
	st.SetGoal(second)
	add("under the second goal", second.ID)
	st.SetGoal(GoalState{})
	add("goal cleared", "")

	if first.ID == second.ID {
		t.Fatalf("two goals share the id %q", first.ID)
	}
	msgs := st.GetMessages()
	if len(msgs) != len(want) {
		t.Fatalf("%d rows, want %d", len(msgs), len(want))
	}
	for i, m := range msgs {
		if m.Goal != want[i] {
			t.Errorf("row %d (%q, plan %v) carries goal %q, want %q", i, m.Content, m.PlanDocument != nil, m.Goal, want[i])
		}
	}
}

// A client that sends its own history with a request (chat completions) sends
// rows without the goal fields: they are never on the wire. The rows of the new
// history that are the stored ones, in order, keep what the server stamped on
// them, so the gate still reads the docker calls they carry; a row the client
// wrote or changed carries nothing, because nothing says the goal stored it.
func TestReplaceMessagesFromClientKeepsTheStampsOfTheRowsItReplays(t *testing.T) {
	const (
		down = `{"command":"docker compose down"}`
		rm   = `{"command":"docker rm -f db"}`
	)
	goal, _ := NewGoal("clean up")
	seed := func() *State {
		st := &State{ID: "sess_replay", Mode: ModeAgent}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "before the goal"})
		st.SetGoal(goal)
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "clean up"})
		// A row only the UI shows: a client has no way to send it back.
		st.AppendPlanDocument(plans.Document{Slug: "plan"})
		st.AddMessage(riskCall("c1", "run_command", down))
		st.AddMessage(riskResult("c1", "stopped db"))
		st.AddMessage(riskCall("c2", "run_command", rm))
		st.AddMessage(riskResult("c2", "removed db"))
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})
		st.mu.Lock()
		st.Messages[3].RiskAcknowledged = true
		st.mu.Unlock()
		return st
	}
	// What a client sends back of a history: the same rows as the wire carries
	// them, the plan snapshot and the goal fields gone.
	wire := func(msgs []llm.Message) []llm.Message {
		var out []llm.Message
		for _, m := range msgs {
			if m.PlanDocument != nil {
				continue
			}
			m.Goal, m.RiskAcknowledged = "", false
			out = append(out, m)
		}
		return out
	}
	type stamp struct {
		goal  string
		acked bool
	}
	stamps := func(msgs []llm.Message) []stamp {
		out := make([]stamp, len(msgs))
		for i, m := range msgs {
			out[i] = stamp{m.Goal, m.RiskAcknowledged}
		}
		return out
	}

	t.Run("a replayed history keeps every stamp and the gate reads it as before", func(t *testing.T) {
		st := seed()
		stored := st.GetMessages()
		_, before := goalFindings(stored, goalRows(stored, goal, false), goal, goalFiles{})

		st.ReplaceMessagesFromClient(append(wire(stored), llm.Message{Role: llm.RoleUser, Content: "next"}))

		got := st.GetMessages()
		var want []stamp
		for _, m := range stored {
			if m.PlanDocument == nil {
				want = append(want, stamp{m.Goal, m.RiskAcknowledged})
			}
		}
		want = append(want, stamp{})
		if !reflect.DeepEqual(stamps(got), want) {
			t.Fatalf("stamps after the replay = %v, want %v", stamps(got), want)
		}
		_, after := goalFindings(got, goalRows(got, goal, false), goal, goalFiles{})
		if len(before) != 2 || !reflect.DeepEqual(after, before) {
			t.Fatalf("findings after the replay = %+v, want the %+v the stored history gave", after, before)
		}
	})

	t.Run("a row the client changed gets no stamp, nor the result of it, and the rows after keep theirs", func(t *testing.T) {
		st := seed()
		sent := wire(st.GetMessages())
		sent[4].ToolCalls = []llm.ToolCall{{ID: "c2", Name: "run_command", InputJSON: `{"command":"docker rm -f other"}`}}
		st.ReplaceMessagesFromClient(sent)

		got := st.GetMessages()
		// The result of the changed call is the stored row, but it now follows a
		// call nobody stored, so it proves nothing about it.
		for i, wantGoal := range []string{"", goal.ID, goal.ID, goal.ID, "", "", goal.ID} {
			if got[i].Goal != wantGoal {
				t.Errorf("row %d carries goal %q, want %q", i, got[i].Goal, wantGoal)
			}
		}
	})

	t.Run("a history the client trimmed keeps the stamps of what is left", func(t *testing.T) {
		st := seed()
		st.ReplaceMessagesFromClient(wire(st.GetMessages())[2:])

		got := st.GetMessages()
		want := []stamp{{goal.ID, true}, {goal.ID, false}, {goal.ID, false}, {goal.ID, false}, {goal.ID, false}}
		if !reflect.DeepEqual(stamps(got), want) {
			t.Fatalf("stamps after the trim = %v, want %v", stamps(got), want)
		}
	})

	t.Run("a row out of order or written by the client is nobody's evidence", func(t *testing.T) {
		st := seed()
		sent := wire(st.GetMessages())
		sent[2], sent[4] = sent[4], sent[2]
		sent = append(sent, riskCall("c3", "run_command", down))
		st.ReplaceMessagesFromClient(sent)

		got := st.GetMessages()
		if got[len(got)-1].Goal != "" {
			t.Errorf("a row the client wrote carries goal %q", got[len(got)-1].Goal)
		}
		// The rows are matched in the stored order, each stored row once: the
		// call the client moved ahead keeps its stamp, the ones it jumped over
		// are no longer the stored rows at their place.
		// The result of the moved call (row 5) stays unstamped too: the call it
		// follows now is not the stored call it answered.
		for i, wantGoal := range []string{"", goal.ID, goal.ID, "", "", "", goal.ID} {
			if got[i].Goal != wantGoal {
				t.Errorf("row %d carries goal %q, want %q", i, got[i].Goal, wantGoal)
			}
		}
	})

	t.Run("a bootstrap takes the rows as they come", func(t *testing.T) {
		st := seed()
		st.ReplaceMessagesWithoutPersist(wire(st.GetMessages()))
		for i, m := range st.GetMessages() {
			if m.Goal != "" || m.RiskAcknowledged {
				t.Errorf("row %d was stamped by a replacement that is no client's", i)
			}
		}
	})
}

// A result proves that a call never ran only next to the call it answered. A
// provider may reuse a call id, so a client that trims the history can send the
// refusal of a later call right behind an earlier call of the same id and
// words, and the stamp the refusal carries would make that earlier call look
// like one that never ran. A replayed result keeps its stamp only when the
// assistant row that owned the stored result is the stored row the replayed
// assistant row before it was matched to.
func TestReplaceMessagesFromClientKeepsAResultStampOnlyNextToItsOwnCall(t *testing.T) {
	const down = `{"command":"docker compose down"}`
	goal, _ := NewGoal("clean up")
	// The same call twice under one id: it ran the first time and was refused
	// the second.
	seed := func() *State {
		st := &State{ID: "sess_reused_id", Mode: ModeAgent}
		st.SetGoal(goal)
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "clean up"})
		st.AddMessage(riskCall("X", "run_command", down))
		st.AddMessage(riskResult("X", "stopped db"))
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "again"})
		st.AddMessage(riskCall("X", "run_command", down))
		st.AddMessage(riskResult("X", ToolResultDeniedByUser))
		return st
	}
	dockerFindings := func(st *State) (n int) {
		msgs := st.GetMessages()
		_, all := goalFindings(msgs, goalRows(msgs, goal, false), goal, goalFiles{})
		for _, f := range all {
			if f.kind == dockerMutation {
				n++
			}
		}
		return n
	}

	t.Run("the refusal of a later call does not pass for the answer to the earlier one", func(t *testing.T) {
		st := seed()
		if n := dockerFindings(st); n != 1 {
			t.Fatalf("the stored history reports %d docker calls, want the one that ran", n)
		}
		stored := st.GetMessages()
		st.ReplaceMessagesFromClient([]llm.Message{
			{Role: llm.RoleUser, Content: "clean up"},
			{Role: stored[1].Role, ToolCalls: stored[1].ToolCalls},
			{Role: stored[5].Role, ToolCallID: stored[5].ToolCallID, Content: stored[5].Content},
		})

		got := st.GetMessages()
		if got[1].Goal != goal.ID {
			t.Fatalf("the replayed call carries goal %q, want %q", got[1].Goal, goal.ID)
		}
		if got[2].Goal != "" {
			t.Errorf("the replayed refusal carries goal %q although it answered another call", got[2].Goal)
		}
		if n := dockerFindings(st); n != 1 {
			t.Fatalf("%d docker findings after the replay, want the call that ran reported", n)
		}
	})

	t.Run("a result replayed next to its own call keeps the stamp and still exempts a refused one", func(t *testing.T) {
		st := seed()
		stored := st.GetMessages()
		// The first call and its result trimmed away, the second pair kept.
		st.ReplaceMessagesFromClient([]llm.Message{
			{Role: llm.RoleUser, Content: "clean up"},
			{Role: stored[3].Role, Content: stored[3].Content},
			{Role: stored[4].Role, ToolCalls: stored[4].ToolCalls},
			{Role: stored[5].Role, ToolCallID: stored[5].ToolCallID, Content: stored[5].Content},
		})

		for i, m := range st.GetMessages() {
			if m.Goal != goal.ID {
				t.Errorf("row %d carries goal %q, want %q", i, m.Goal, goal.ID)
			}
		}
		if n := dockerFindings(st); n != 0 {
			t.Fatalf("%d docker findings for a call whose refusal sits next to it, want 0", n)
		}
	})
}

// The goal stamp is carried greedily, because losing the evidence is the
// dangerous direction. The acknowledgement is not: it is carried only to a row
// whose match is unambiguous - the one stored row equal to it - since a client
// that trims or reorders the history it sends can make the first of several
// identical rows stand for a later one nobody accepted. A dropped
// acknowledgement blocks the goal again, which costs one /goal resume.
func TestReplaceMessagesFromClientCarriesAnAcknowledgementOnlyToAnUnambiguousRow(t *testing.T) {
	const down = `{"command":"docker compose down"}`
	goal, _ := NewGoal("clean up")
	// A provider may reuse a call id: the second call is equal to the first on
	// everything a client can send back. Only the first was accepted by a resume.
	seed := func() *State {
		st := &State{ID: "sess_ambiguous", Mode: ModeAgent}
		st.SetGoal(goal)
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "clean up"})
		st.AddMessage(riskCall("call_0", "run_command", down))
		st.AddMessage(riskResult("call_0", "stopped db"))
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "up again"})
		st.AddMessage(riskCall("call_0", "run_command", down))
		st.AddMessage(riskResult("call_0", "stopped db"))
		st.mu.Lock()
		st.Messages[1].RiskAcknowledged = true
		st.mu.Unlock()
		return st
	}
	unacknowledged := func(st *State) (findings []goalFinding) {
		msgs := st.GetMessages()
		_, all := goalFindings(msgs, goalRows(msgs, goal, false), goal, goalFiles{})
		for _, f := range all {
			if f.kind == dockerMutation && !f.acknowledged {
				findings = append(findings, f)
			}
		}
		return findings
	}

	t.Run("a trimmed history does not carry the first call's acceptance to its twin", func(t *testing.T) {
		st := seed()
		st.ReplaceMessagesFromClient([]llm.Message{
			{Role: llm.RoleUser, Content: "clean up"},
			riskCall("call_0", "run_command", down),
			riskResult("call_0", "stopped db"),
		})
		got := st.GetMessages()
		if got[1].Goal != goal.ID || got[1].RiskAcknowledged {
			t.Fatalf("the replayed twin carries goal %q, acknowledged %v; want the goal's id, not acknowledged", got[1].Goal, got[1].RiskAcknowledged)
		}
		if n := len(unacknowledged(st)); n != 1 {
			t.Fatalf("%d unacknowledged docker findings after the trim, want 1", n)
		}
	})

	t.Run("a call that has an identical twin loses its acceptance even in a full replay", func(t *testing.T) {
		st := seed()
		stored := st.GetMessages()
		sent := make([]llm.Message, len(stored))
		for i, m := range stored {
			m.Goal, m.RiskAcknowledged = "", false
			sent[i] = m
		}
		st.ReplaceMessagesFromClient(sent)
		got := st.GetMessages()
		for i, m := range got {
			if m.Goal != goal.ID {
				t.Errorf("row %d carries goal %q, want %q", i, m.Goal, goal.ID)
			}
			if m.RiskAcknowledged {
				t.Errorf("row %d is acknowledged although another stored row is equal to it", i)
			}
		}
		if n := len(unacknowledged(st)); n != 2 {
			t.Fatalf("%d unacknowledged docker findings after the replay, want 2", n)
		}
	})
}

// A summary row a compaction inserts is stored like an appended one: stamped
// while the goal is active, wherever it lands.
func TestCompactionSummaryRowsAreStampedLikeAppendedOnes(t *testing.T) {
	st := &State{ID: "sess_fold", Mode: ModeAgent}
	goal, _ := NewGoal("ship it")
	st.SetGoal(goal)
	for _, content := range []string{"a", "b", "c"} {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: content})
	}
	st.InsertCompactionSummary(1, NewCompactionSummaryMessage("folded", "m"))
	st.InsertCompactionSummary(99, NewCompactionSummaryMessage("appended", "m"))
	st.UpdateGoal("", func(g *GoalState) bool { g.Status = GoalPaused; return true })
	st.InsertCompactionSummary(0, NewCompactionSummaryMessage("while paused", "m"))

	msgs := st.GetMessages()
	if len(msgs) != 6 {
		t.Fatalf("%d rows, want 6", len(msgs))
	}
	for i, wantGoal := range []string{"", goal.ID, goal.ID, goal.ID, goal.ID, goal.ID} {
		if msgs[i].Goal != wantGoal {
			t.Errorf("row %d (%.20q) carries goal %q, want %q", i, msgs[i].Content, msgs[i].Goal, wantGoal)
		}
	}
	if !strings.Contains(msgs[0].Content, "while paused") || !strings.Contains(msgs[2].Content, "folded") || !strings.Contains(msgs[5].Content, "appended") {
		t.Fatalf("rows are not where they were inserted: %+v", msgs)
	}
}

// The goal a surface reads is a copy all the way down: the start record and the
// accepted files are the state's own, and changing what a caller got back, or
// what it handed in, must not reach them.
func TestGoalCloneCopiesStartAndAcceptedFilesDeep(t *testing.T) {
	st := &State{ID: "sess_clone"}
	given := riskyGoal(t)
	st.SetGoal(given)
	// What the goal must still be afterwards, as a copy of its own.
	var want GoalState
	raw, err := json.Marshal(st.GetGoal())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}

	got := st.GetGoal()
	got.Start.Dir = "other"
	got.Start.Head = "other"
	got.Start.Dirty["migrations/1.sql"] = "changed"
	got.Start.Dirty["migrations/new.sql"] = "added"
	got.AcceptedFiles["migrations/2.sql"] = "changed"
	got.AcceptedFiles["migrations/new.sql"] = "added"
	if again := st.GetGoal(); !reflect.DeepEqual(again, want) {
		t.Fatalf("changing the copy GetGoal returned reached the state:\n got %+v\nwant %+v", again, want)
	}

	given.Start.Head = "other"
	given.Start.Dirty["migrations/1.sql"] = "changed"
	given.AcceptedFiles["migrations/2.sql"] = "changed"
	if again := st.GetGoal(); !reflect.DeepEqual(again, want) {
		t.Fatalf("changing the goal SetGoal was handed reached the state:\n got %+v\nwant %+v", again, want)
	}
}

// What the risk gate keeps lives on the persisted goal and on the stored rows:
// the start record, the accepted files, the id a row carries and its
// acknowledgement survive a save and a restore of the bundle, and a goal with
// none of it adds nothing to the JSON.
func TestGoalRiskRecordsPersistWithSession(t *testing.T) {
	fs := &FileStore{Root: t.TempDir()}
	dir, err := fs.EnsureLayout("sess_risk")
	if err != nil {
		t.Fatal(err)
	}
	st := &State{ID: "sess_risk", CWD: t.TempDir(), Mode: ModeAgent, SessionDir: dir}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "before the goal"})
	goal := riskyGoal(t)
	st.SetGoal(goal)
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run_command", InputJSON: `{"command":"docker compose down"}`}}, RiskAcknowledged: true})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "plain"})
	want := st.GetGoal()
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.Goal == nil || !reflect.DeepEqual(*snap.Meta.Goal, want) {
		t.Fatalf("saved and read back:\n got %+v\nwant %+v", snap.Meta.Goal, want)
	}
	if got := snap.Meta.Goal; got.Start == nil || got.Start.Head != "abc123" || got.Start.Dirty["migrations/1.sql"] != "abc" || got.AcceptedFiles["migrations/2.sql"] != "def" {
		t.Fatalf("start or accepted files lost: %+v", got)
	}
	if len(snap.Messages) != 3 {
		t.Fatalf("%d rows read back, want 3", len(snap.Messages))
	}
	for i, wantRow := range []struct {
		goal string
		ack  bool
	}{{"", false}, {goal.ID, true}, {goal.ID, false}} {
		if m := snap.Messages[i]; m.Goal != wantRow.goal || m.RiskAcknowledged != wantRow.ack {
			t.Errorf("row %d read back as goal=%q acknowledged=%v, want goal=%q acknowledged=%v", i, m.Goal, m.RiskAcknowledged, wantRow.goal, wantRow.ack)
		}
	}

	restored := &State{ID: "sess_risk"}
	restored.RestoreGoalWithoutPersist(*snap.Meta.Goal)
	if got := restored.GetGoal(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored:\n got %+v\nwant %+v", got, want)
	}

	plain, _ := NewGoal("ship it")
	raw, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"start"`, `"acceptedFiles"`} {
		if strings.Contains(string(raw), key) {
			t.Errorf("a goal with no risk records has %s in its JSON: %s", key, raw)
		}
	}
	row, err := json.Marshal(llm.Message{Role: llm.RoleAssistant, Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"goal"`, `"risk_acknowledged"`} {
		if strings.Contains(string(row), key) {
			t.Errorf("a row of no goal has %s in its JSON: %s", key, row)
		}
	}
}

// acceptFixture is a history of assistant rows around a goal's docker steps:
// 0, 7 and 9 are the goal's rows whose docker calls count (9 already
// acknowledged); 1 is refused, 3 is no docker, 5 is another goal's, 6 no
// goal's. A result is stored under the goal of its call, as the store stamps
// it, since only such a result proves its call never ran.
func acceptFixture(goal, other GoalState) []llm.Message {
	const down = `{"command":"docker compose down"}`
	return []llm.Message{
		/* 0 */ {Role: llm.RoleAssistant, Goal: goal.ID, ToolCalls: []llm.ToolCall{{ID: "a1", Name: "run_command", InputJSON: down}}},
		/* 1 */ {Role: llm.RoleAssistant, Goal: goal.ID, ToolCalls: []llm.ToolCall{{ID: "a2", Name: "run_command", InputJSON: down}}},
		/* 2 */ {Role: llm.RoleTool, Goal: goal.ID, ToolCallID: "a2", Content: ToolResultDeniedByUser},
		/* 3 */ {Role: llm.RoleAssistant, Goal: goal.ID, ToolCalls: []llm.ToolCall{{ID: "a3", Name: "run_command", InputJSON: `{"command":"go test ./..."}`}}},
		/* 4 */ {Role: llm.RoleTool, Goal: goal.ID, ToolCallID: "a3", Content: "ok"},
		/* 5 */ {Role: llm.RoleAssistant, Goal: other.ID, ToolCalls: []llm.ToolCall{{ID: "a4", Name: "run_command", InputJSON: down}}},
		/* 6 */ {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a5", Name: "run_command", InputJSON: down}}},
		/* 7 */ {Role: llm.RoleAssistant, Goal: goal.ID, ToolCalls: []llm.ToolCall{{ID: "a6", Name: "run_command", InputJSON: down}}},
		/* 8 */ {Role: llm.RoleTool, Goal: goal.ID, ToolCallID: "a6", Content: "stopped db"},
		/* 9 */ {Role: llm.RoleAssistant, Goal: goal.ID, RiskAcknowledged: true, ToolCalls: []llm.ToolCall{{ID: "a7", Name: "run_command", InputJSON: down}}},
		/* 10 */ {Role: llm.RoleUser, Goal: goal.ID, Content: "docker compose down"},
	}
}

func acceptRisks(msgs []llm.Message, goal *GoalState, files goalFiles) (st *State, edits [2]uint64) {
	st = &State{ID: "sess_accept"}
	st.mu.Lock()
	st.Messages = append([]llm.Message(nil), msgs...)
	st.mu.Unlock()
	_, _, edits[0], _ = st.MessagesForPersist()
	st.mu.Lock()
	st.acceptGoalRisksLocked(goal, files)
	st.mu.Unlock()
	_, _, edits[1], _ = st.MessagesForPersist()
	return st, edits
}

// acceptGoalRisksLocked is what /goal resume does to a blocked goal: it marks
// the goal's own assistant rows whose docker steps count, and records each
// changed migration file at its content now.
func TestAcceptGoalRisksMarksTheGoalsCountingDockerRowsAndRecordsFiles(t *testing.T) {
	goal, _ := NewGoal("clean up")
	other, _ := NewGoal("clean up")
	msgs := acceptFixture(goal, other)
	wantAck := map[int]bool{0: true, 7: true, 9: true}

	next := goal
	next.AcceptedFiles = map[string]string{"migrations/a.sql": "old", "migrations/keep.sql": "k"}
	st, edits := acceptRisks(msgs, &next, goalFiles{changed: map[string]string{"migrations/a.sql": "new", "migrations/gone.sql": ""}})

	rows, _, _, _ := st.MessagesForPersist()
	for i, m := range rows {
		if m.RiskAcknowledged != wantAck[i] {
			t.Errorf("row %d acknowledged = %v, want %v", i, m.RiskAcknowledged, wantAck[i])
		}
	}
	if edits[1] <= edits[0] {
		t.Errorf("marking rows did not move the edit revision (%d -> %d): the stored transcript would not be rewritten", edits[0], edits[1])
	}
	wantFiles := map[string]string{"migrations/a.sql": "new", "migrations/keep.sql": "k", "migrations/gone.sql": ""}
	if !reflect.DeepEqual(next.AcceptedFiles, wantFiles) {
		t.Errorf("accepted files = %v, want %v", next.AcceptedFiles, wantFiles)
	}

	t.Run("nothing new to accept leaves the revision alone", func(t *testing.T) {
		done := make([]llm.Message, len(msgs))
		copy(done, msgs)
		done[0].RiskAcknowledged, done[7].RiskAcknowledged = true, true
		again := goal
		_, edits := acceptRisks(done, &again, goalFiles{})
		if edits[1] != edits[0] {
			t.Errorf("edit revision moved (%d -> %d) with no row to mark", edits[0], edits[1])
		}
		if again.AcceptedFiles != nil {
			t.Errorf("accepted files = %v with no file changed", again.AcceptedFiles)
		}
	})
	t.Run("the first accepted file makes the map", func(t *testing.T) {
		fresh := goal
		acceptRisks(msgs, &fresh, goalFiles{changed: map[string]string{"migrations/a.sql": "h"}})
		if !reflect.DeepEqual(fresh.AcceptedFiles, map[string]string{"migrations/a.sql": "h"}) {
			t.Errorf("accepted files = %v", fresh.AcceptedFiles)
		}
	})
}

// A goal with no id (one a binary from before ids stored) has no rows: goalRows
// reads none for it, and accepting its risks must not mark the rows no goal
// stamped, which carry the same empty id.
func TestAcceptGoalRisksLeavesTheRowsOfNoGoalAlone(t *testing.T) {
	goal, _ := NewGoal("clean up")
	other, _ := NewGoal("clean up")
	noID := GoalState{Objective: "clean up", Status: GoalBlocked}
	st, _ := acceptRisks(acceptFixture(goal, other), &noID, goalFiles{})
	rows, _, _, _ := st.MessagesForPersist()
	for _, i := range []int{0, 6, 7} {
		if rows[i].RiskAcknowledged {
			t.Errorf("row %d was accepted for a goal that has no id", i)
		}
	}
}

// A goal the PATCH endpoint replaces is a new goal: a fresh id, nothing the
// replaced one had accepted, its own start - so the rows the replaced goal
// stored are not its rows.
func TestSetGoalObjectiveGivesAFreshGoalItsOwnEvidence(t *testing.T) {
	mgr, fs := newTestManager(t)
	cwd := t.TempDir()
	st := newRewindState(t, mgr, fs, "sess_patch", nil)
	st.CWD = cwd

	old := riskyGoal(t)
	st.SetGoal(old)
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run_command", InputJSON: `{"command":"docker compose down"}`}}})
	oldRows := goalRows(st.GetMessages(), st.GetGoal(), false)
	if len(oldRows) != 1 {
		t.Fatalf("the old goal has %d rows, want 1", len(oldRows))
	}

	got, err := mgr.SetGoalObjective("sess_patch", "a different objective")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.ID == old.ID {
		t.Errorf("id = %q, want a fresh one (was %q)", got.ID, old.ID)
	}
	if got.AcceptedFiles != nil {
		t.Errorf("accepted files = %v, want none", got.AcceptedFiles)
	}
	if got.Start == nil || got.Start.Dir != cwd || got.Start.Head == old.Start.Head || len(got.Start.Dirty) != 0 {
		t.Errorf("start = %+v, want the session's own folder %q and none of the old goal's record", got.Start, cwd)
	}
	if now := st.GetGoal(); now.ID != got.ID || now.Objective != got.Objective || !reflect.DeepEqual(now.Start, got.Start) {
		t.Errorf("the session holds %+v, SetGoalObjective returned %+v", now, got)
	}
	if rows := goalRows(st.GetMessages(), st.GetGoal(), false); len(rows) != 0 {
		t.Errorf("the replaced goal's rows %v are the new goal's", rows)
	}
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "working"})
	if rows := goalRows(st.GetMessages(), st.GetGoal(), false); len(rows) != 1 || rows[0] != 1 {
		t.Errorf("rows of the new goal = %v, want only the one stored after it was set", rows)
	}
}
