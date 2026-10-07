package session

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
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
		"/goal --frob x ship":                          {Kind: GoalCommandSet, Objective: "ship", Err: "unknown option --frob"},
		"/goal -m mini -r high ship it":                {Kind: GoalCommandSet, Objective: "ship it", Model: "mini", Reasoning: "high"},
		"/goal -r=low -m=mini resume":                  {Kind: GoalCommandResume, Model: "mini", Reasoning: "low"},
		"/goal - fix the flaky test":                   {Kind: GoalCommandSet, Objective: "- fix the flaky test"},
		"/goal -x stays text":                          {Kind: GoalCommandSet, Objective: "-x stays text"},
		"/goal --model mini clear":                     {Kind: GoalCommandClear, Model: "mini", Err: "--model and --reasoning go with a new goal or /goal resume"},
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
	if got := GoalStatusText(GoalState{}, 10, 0); !strings.Contains(got, "No goal is set") {
		t.Fatalf("empty = %q", got)
	}
	g, _ := NewGoal("ship it")
	g.Status, g.StatusReason, g.Continuations = GoalBlocked, "which DB?", 3
	g.LastCheck = &GoalCheck{Verdict: GoalVerdictNeedsUser, Reason: "which DB?", Remaining: []string{"ask the operator"}}
	got := GoalStatusText(g, 10, 0)
	for _, want := range []string{"Goal (blocked): ship it", "Reason: which DB?", "3 of 10", "needs_user", "- ask the operator", "/goal resume"} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
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
