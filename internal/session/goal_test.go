package session

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
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
		"/goal --frob x ship":                          {Kind: GoalCommandSet, Objective: "x ship", Err: "unknown option --frob"},
		"/goal --frob -m mini ship":                    {Kind: GoalCommandSet, Objective: "ship", Model: "mini", Err: "unknown option --frob"},
		"/goal -m= ship it":                            {Kind: GoalCommandSet, Objective: "ship it", Err: "--model needs a value"},
		"/goal -m mini -r high ship it":                {Kind: GoalCommandSet, Objective: "ship it", Model: "mini", Reasoning: "high"},
		"/goal -r=low -m=mini resume":                  {Kind: GoalCommandResume, Model: "mini", Reasoning: "low"},
		"/goal\u00a0nbsp after the command":            {Kind: GoalCommandSet, Objective: "nbsp after the command"},
		"/goal\u2003em space sets it":                  {Kind: GoalCommandSet, Objective: "em space sets it"},
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

// A prompt of one text block plus attachments - an @ mention resolved into a
// resource, a file the composer or an editor attached - is still a /goal
// command: the attachments come back for the turn it starts (issue #534).
// Any other shape stays an ordinary message.
func TestGoalCommandOfReadsTextWithAttachments(t *testing.T) {
	text := func(s string) acp.ContentBlock { return acp.ContentBlock{Type: acp.ContentTypeText, Text: s} }
	file := func(uri string) acp.ContentBlock {
		return acp.ContentBlock{Type: acp.ContentTypeResource, Resource: &acp.Resource{URI: uri, Text: "body"}}
	}
	for _, tc := range []struct {
		name   string
		prompt []acp.ContentBlock
		kind   GoalCommandKind
		want   int // attachments
	}{
		{name: "text only", prompt: []acp.ContentBlock{text("/goal ship it")}, kind: GoalCommandSet},
		{name: "a mention after the text", prompt: []acp.ContentBlock{text("/goal ship @plan.md"), file("plan.md")}, kind: GoalCommandSet, want: 1},
		{name: "an attachment before the text", prompt: []acp.ContentBlock{file("plan.md"), text("/goal ship it")}, kind: GoalCommandSet, want: 1},
		{name: "several attachments", prompt: []acp.ContentBlock{text("/goal ship it"), file("a.md"), file("b.md")}, kind: GoalCommandSet, want: 2},
		{name: "a show with an attachment", prompt: []acp.ContentBlock{text("/goal"), file("plan.md")}, kind: GoalCommandShow, want: 1},
		{name: "an unreadable link after the text", prompt: []acp.ContentBlock{text("/goal ship it"), text("[Linked resource: buf <zed://buffer/123>]")}, kind: GoalCommandSet, want: 1},
		{name: "an unreadable link before the text", prompt: []acp.ContentBlock{text("[Linked resource: buf <zed://buffer/123>]"), text("/goal ship it")}, kind: GoalCommandSet, want: 1},
		{name: "prose with an attachment", prompt: []acp.ContentBlock{text("ship it"), file("plan.md")}, kind: GoalCommandNone},
		{name: "two text blocks", prompt: []acp.ContentBlock{text("/goal ship it"), text("and more")}, kind: GoalCommandNone},
		{name: "attachments only", prompt: []acp.ContentBlock{file("plan.md")}, kind: GoalCommandNone},
		{name: "an empty text with an attachment", prompt: []acp.ContentBlock{text("  "), file("plan.md")}, kind: GoalCommandNone},
		{name: "empty", prompt: nil, kind: GoalCommandNone},
	} {
		cmd, attachments := goalCommandOf(tc.prompt)
		if cmd.Kind != tc.kind || len(attachments) != tc.want {
			t.Errorf("%s: cmd = %+v, attachments = %d, want kind %d with %d", tc.name, cmd, len(attachments), tc.kind, tc.want)
		}
		if tc.kind == GoalCommandSet && cmd.Objective == "" {
			t.Errorf("%s: the objective was lost: %+v", tc.name, cmd)
		}
	}
}

// An editor's resource_link to something the agent cannot open - an internal
// URI like zed://buffer/123 - is hydrated into a line naming it
// (promptfiles.go). That line is not a second thing the operator typed: the
// prompt is still a /goal command, and the line rides the kickoff (issue
// #534).
func TestGoalCommandOfReadsThroughAnUnreadableLink(t *testing.T) {
	cwd := t.TempDir()
	prompt := []acp.ContentBlock{
		{Type: acp.ContentTypeText, Text: "/goal fix the parser"},
		{Type: acp.ContentTypeResourceLink, URI: "zed://buffer/123", Name: "buf"},
	}
	blocks, err := hydrateClientResources(cwd, prompt)
	if err != nil {
		t.Fatal(err)
	}
	cmd, attachments := goalCommandOf(blocks)
	if cmd.Kind != GoalCommandSet || cmd.Objective != "fix the parser" {
		t.Fatalf("cmd = %+v, want the goal set from the typed text", cmd)
	}
	if len(attachments) != 1 || !isLinkedResourceLine(attachments[0].Text) {
		t.Fatalf("attachments = %+v, want the line naming the link", attachments)
	}
}
