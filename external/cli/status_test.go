//go:build cli

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestStatusVerbForTool(t *testing.T) {
	cases := map[string]string{
		"read":                    "Reading",
		"print_tree":              "Listing",
		"grep":                    "Searching",
		"glob":                    "Searching",
		"APPLY_PATCH":             "Editing",
		"write":                   "Writing",
		"run_command":             "Running",
		"ssh_run_command":         "Running over SSH",
		"spawn_agent":             "Running subagent",
		"rmdir":                   "Deleting",
		"webfetch":                "Fetching",
		"coddy_docs_search":       "Searching the docs",
		"coddy_docs_read":         "Reading the docs",
		"http_request":            "Sending a request",
		"load_skill":              "Loading a skill",
		"plan_read":               "Reading the plan",
		"plan_write":              "Updating the plan",
		"coddy_todo_write":        "Updating the plan",
		"coddy_todo_plan_read":    "Reading the plan",
		"coddy_scheduler_job_get": "Updating the schedule",
		"coddy_memory_search":     "Working with memory",
		"config_set":              "Updating the configuration",
		"background_wait":         "Waiting for a background task",
		"background_output":       "Reading background output",
		"background_stop":         "Stopping a background task",
		"background_reap":         "Cleaning up background tasks",
		"background_list":         "Checking background tasks",
		// An MCP tool reads as an action naming the server and the tool; its
		// raw `server__tool` registry id says neither.
		"some_mcp_server__do_thing": "Calling do_thing on the MCP server some_mcp_server",
		"mcp__github__create_issue": "Calling create_issue on the MCP server github",
		"notion__pages__create":     "Calling pages__create on the MCP server notion",
		"weird__":                   "Running a tool",
		"mcp__only":                 "Running a tool",
		"":                          "Running a tool",
	}
	for name, want := range cases {
		if got := statusVerbForTool(name); got != want {
			t.Errorf("statusVerbForTool(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestStatusTargetFromArgs(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want string
	}{
		{"path", "read", `{"path":"README.md"}`, "README.md"},
		{"command", "run_command", `{"command":"go test ./..."}`, "go test ./..."},
		{"remote command", "ssh_run_command", `{"command":"uptime","host":"box"}`, "uptime"},
		{"subagent", "spawn_agent", `{"agent":"reviewer","prompt":"review the diff"}`, "reviewer"},
		{"pattern", "grep", `{"pattern":"TODO","path":"internal"}`, "TODO"},
		{"query", "websearch", `{"query":"go slog"}`, "go slog"},
		{"source", "mv", `{"src":"a.go","dst":"b.go"}`, "a.go"},
		{"url", "webfetch", `{"url":"https://example.dev"}`, "https://example.dev"},
		{"docs query", "coddy_docs_search", `{"query":"telegram proxy","limit":3}`, "telegram proxy"},
		{"docs page", "coddy_docs_read", `{"page":"features/mentions#completion","offset":40}`, "features/mentions#completion"},
		{"request", "http_request", `{"method":"post","url":"https://api.example.dev/items","json":{}}`, "POST https://api.example.dev/items"},
		{"request without a method", "http_request", `{"url":"http://localhost:8080/health"}`, "http://localhost:8080/health"},
		{"arguments envelope", "read", `Arguments: {"path":"a.go"}`, "a.go"},
		// The body of a write is never the target: it would fill the whole row.
		{"never the body", "write", `{"path":"a.go","content":"package main"}`, "a.go"},
		{"question carries no target", "question", `{"question":"which one?"}`, ""},
		// An MCP server names its own arguments: the first one that reads as a
		// label is what the call is about when it takes none of Coddy's names.
		{"mcp label argument", "playwright__browser_click", `{"element":"  Search button  ","ref":"e12"}`, "Search button"},
		{"mcp known argument wins", "mcp__playwright__browser_navigate", `{"url":"https://example.dev/a"}`, "https://example.dev/a"},
		{"mcp body is not a label", "github__create_issue", `{"body":"line one\nline two","title":"Crash on start"}`, "Crash on start"},
		{"mcp overlong value is not a label", "github__create_issue", `{"body":"` + strings.Repeat("x", 200) + `"}`, ""},
		{"mcp skips values that are not labels", "server__tool", `{"count":3,"flag":true,"blank":"   ","subject":"ok"}`, "ok"},
		// The fallback is for tools Coddy does not define: a built-in one keeps
		// naming the argument it is documented to take.
		{"unknown tool keeps its known arguments", "something_new", `{"foo":"bar"}`, ""},
		{"a write without a path names nothing", "write", `{"content":"package main"}`, ""},
		{"no arguments yet", "read", "", ""},
		{"unparsable arguments", "read", "not json", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := statusTargetFromArgs(c.tool, c.args); got != c.want {
				t.Errorf("statusTargetFromArgs(%q, %q) = %q, want %q", c.tool, c.args, got, c.want)
			}
		})
	}
}

func TestTruncateStatusTarget(t *testing.T) {
	if got := truncateStatusTarget("internal/agent/react.go", 48); got != "internal/agent/react.go" {
		t.Errorf("short path was rewritten: %q", got)
	}

	deep := truncateStatusTarget("a/very/deeply/nested/directory/tree/inside/the/repo/react.go", 48)
	if !strings.HasPrefix(deep, "…/") || !strings.HasSuffix(deep, "react.go") {
		t.Errorf("deep path = %q, want a …/ prefix and the file name", deep)
	}
	if n := len([]rune(deep)); n > 48 {
		t.Errorf("deep path is %d runes, want <= 48", n)
	}

	// A Windows path has to read like a POSIX one; the untruncated value is not shown.
	win := truncateStatusTarget(`H:\Projects\coddy\internal\agent\react.go`, 48)
	if strings.ContainsRune(win, '\\') {
		t.Errorf("windows separators survived: %q", win)
	}

	if got := truncateStatusTarget("go  test\n  ./...", 48); got != "go test ./..." {
		t.Errorf("whitespace was not collapsed: %q", got)
	}

	// A command keeps its head: the program name is what identifies it.
	long := truncateStatusTarget("go test ./... -run "+strings.Repeat("x", 200), 48)
	if !strings.HasPrefix(long, "go test ./...") || !strings.HasSuffix(long, "…") {
		t.Errorf("long command = %q, want the head kept and the tail cut", long)
	}
	if n := len([]rune(long)); n > 48 {
		t.Errorf("long command is %d runes, want <= 48", n)
	}

	huge := truncateStatusTarget("dir/"+strings.Repeat("x", 200), 48)
	if n := len([]rune(huge)); n > 48 {
		t.Errorf("oversized segment is %d runes, want <= 48", n)
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := map[time.Duration]string{
		0:                               "0s",
		999 * time.Millisecond:          "0s",
		time.Second:                     "1s",
		59 * time.Second:                "59s",
		time.Minute:                     "1m 00s",
		65 * time.Second:                "1m 05s",
		59*time.Minute + 59*time.Second: "59m 59s",
		time.Hour:                       "1h 00m",
		-time.Second:                    "",
	}
	for d, want := range cases {
		if got := formatElapsed(d); got != want {
			t.Errorf("formatElapsed(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestLiveStatusText(t *testing.T) {
	tool := newWorkingStatus("Reading", "README.md")
	if got := tool.statusText(12 * time.Second); got != "Reading README.md · 12s" {
		t.Errorf("tool status = %q", got)
	}
	// The model's own phases are covered by the turn clock that leads the line, so
	// they carry no second clock of their own.
	if got := newModelStatus("Thinking…").statusText(3 * time.Second); got != "Thinking…" {
		t.Errorf("model phase status = %q, want no step counter", got)
	}
	if got := newWaitingStatus().statusText(3 * time.Second); got != statusWaitingModel {
		t.Errorf("waiting status = %q, want no step counter", got)
	}

	// A non-counting status renders without a counter regardless of elapsed time.
	blocked := liveStatus{verb: "Waiting for your approval"}
	if got := blocked.statusText(30 * time.Second); got != "Waiting for your approval" {
		t.Errorf("blocked status = %q, want no counter", got)
	}
}

func TestWaitingStatusEscalates(t *testing.T) {
	waiting := newWaitingStatus()
	cases := []struct {
		elapsed time.Duration
		want    string
	}{
		{0, statusWaitingModel},
		{14 * time.Second, statusWaitingModel},
		{15 * time.Second, statusWaitingSlow},
		{59 * time.Second, statusWaitingSlow},
		{60 * time.Second, statusWaitingStuck},
		{10 * time.Minute, statusWaitingStuck},
	}
	for _, c := range cases {
		got := waiting.statusText(c.elapsed)
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("after %v the status is %q, want it to start with %q", c.elapsed, got, c.want)
		}
	}
}

func TestSetStatusKeepsTheStartOfARepeatedStep(t *testing.T) {
	// A start time far enough in the past that a re-stamp is unmistakable; two
	// time.Now() calls in one test can land on the same coarse clock tick.
	first := time.Now().Add(-time.Hour)
	a := &App{stepStatus: liveStatus{verb: "Thinking…", startedAt: first, counts: true}}

	// Reasoning arrives one chunk at a time; restarting the counter on each of them
	// would peg it at 0s for the whole block.
	a.setStatus(newWorkingStatus("Thinking…", ""))
	if !a.stepStatus.startedAt.Equal(first) {
		t.Fatal("a repeated step restarted its counter")
	}

	a.setStatus(newWorkingStatus("Reading", "README.md"))
	if !a.stepStatus.startedAt.After(first) {
		t.Fatal("a new step kept the previous start time")
	}
	if a.stepStatus.target != "README.md" {
		t.Fatalf("target = %q", a.stepStatus.target)
	}
}

func TestTurnLine(t *testing.T) {
	cases := []struct {
		elapsed time.Duration
		tokens  int
		tasks   int
		want    string
	}{
		// Before the first token the line is the clock alone.
		{57 * time.Second, 0, 0, "57s"},
		{45 * time.Second, 433, 0, "45s · 433 tokens"},
		{5 * time.Second, 1, 0, "5s · 1 token"},
		{125 * time.Second, 1200, 0, "2m 05s · 1.2k tokens"},
		{908 * time.Second, 13_500, 1, "15m 08s · 13.5k tokens · 1 running task"},
		{30 * time.Second, 0, 3, "30s · 3 running tasks"},
	}
	for _, c := range cases {
		if got := turnLine(c.elapsed, c.tokens, c.tasks); got != c.want {
			t.Errorf("turnLine(%v, %d, %d) = %q, want %q", c.elapsed, c.tokens, c.tasks, got, c.want)
		}
	}
}

func TestStatusMessageLeadsWithTheTurnsOwnNumbers(t *testing.T) {
	a := &App{turnActive: true, turnStartedAt: time.Now().Add(-125 * time.Second), turnTokens: 1200, runningTasks: 1}
	a.setStatus(liveStatus{verb: "Running", target: "make test", startedAt: time.Now().Add(-45 * time.Second), counts: true})
	if got := a.statusMessage(); got != "2m 05s · 1.2k tokens · 1 running task · Running make test · 45s" {
		t.Fatalf("statusMessage() = %q", got)
	}

	// Waiting on the model before the first token: the clock and the phrase.
	b := &App{turnActive: true, turnStartedAt: time.Now().Add(-57 * time.Second)}
	b.stepStatus = newWaitingStatus()
	b.stepStatus.startedAt = time.Now().Add(-57 * time.Second)
	if got := b.statusMessage(); got != "57s · "+statusWaitingSlow {
		t.Fatalf("waiting statusMessage() = %q", got)
	}

	// An operator gate keeps the turn clock - it is wall time since the prompt -
	// and still has no step counter.
	c := &App{turnActive: true, turnStartedAt: time.Now().Add(-30 * time.Second), turnTokens: 80}
	c.setStatus(newWorkingStatus("Running", "sleep 6"))
	c.blockStatus("Waiting for your approval")
	if got := c.statusMessage(); got != "30s · 80 tokens · Waiting for your approval" {
		t.Fatalf("blocked statusMessage() = %q", got)
	}
}

func TestTurnProgressUpdateFeedsTheLine(t *testing.T) {
	a := &App{turnActive: true, sessionID: "s1", turnSessionID: "s1", turnStartedAt: time.Now().Add(-10 * time.Second)}
	a.applyTurnProgress(acp.TurnProgressUpdate{OutputTokens: 433, ElapsedMs: 10_000, Estimated: true})
	if a.turnTokens != 433 {
		t.Fatalf("turnTokens = %d, want 433", a.turnTokens)
	}
	// A turn this console did not time itself - it attached to one another client
	// started - takes the server's clock.
	b := &App{remoteTurnActive: true}
	b.applyTurnProgress(acp.TurnProgressUpdate{OutputTokens: 5, ElapsedMs: 42_000})
	if got := time.Since(b.turnStartedAt).Round(time.Second); got != 42*time.Second {
		t.Fatalf("adopted turn clock reads %v, want 42s", got)
	}
}

func TestStatusMessageBeforeAnyStep(t *testing.T) {
	a := &App{}
	if got := a.statusMessage(); got != statusWaitingModel {
		t.Errorf("statusMessage() = %q, want %q", got, statusWaitingModel)
	}
}

func TestBlockedQuestionShowsNoCounter(t *testing.T) {
	// The question tool's verb is the same string as the modal's blocked phrase,
	// so the modal transition must still strip the counter: nothing is running
	// while the operator types an answer.
	a := &App{turnActive: true}
	a.setStatus(newWorkingStatus(statusVerbForTool("question"), ""))
	a.blockStatus("Waiting for your answer")
	if got := a.statusMessage(); got != "Waiting for your answer" {
		t.Fatalf("counter ticks while blocked on the operator: %q", got)
	}
}

func TestBlockedOverlayOutlivesLateToolUpdates(t *testing.T) {
	a := &App{turnActive: true}
	a.setStatus(newWorkingStatus("Running", "sleep 6"))
	a.blockStatus("Waiting for your approval")
	// The gated call's in_progress update can land after the modal opened
	// (updatesCh and permCh race in the UI select); the gate must still win.
	a.setStatus(newWorkingStatus("Running", "sleep 6"))
	if got := a.statusMessage(); got != "Waiting for your approval" {
		t.Fatalf("modal status lost to a late tool update: %q", got)
	}

	a.unblockStatus()
	got := a.statusMessage()
	if !strings.HasPrefix(got, "Running sleep 6") {
		t.Fatalf("gated tool not restored after approval: %q", got)
	}
	// The approved tool only starts executing now, so its clock restarts;
	// counting from before the modal would bill the operator's thinking time.
	if !strings.Contains(got, "· 0s") {
		t.Fatalf("step clock did not restart when the gate lifted: %q", got)
	}
}

func TestUnblockWithoutGateKeepsTheStepClock(t *testing.T) {
	// closeModal runs for every modal (model picker, history); without an
	// active gate it must not touch the running step's counter.
	first := time.Now().Add(-time.Hour)
	a := &App{turnActive: true, stepStatus: liveStatus{verb: "Running", target: "x", startedAt: first, counts: true}}
	a.unblockStatus()
	if !a.stepStatus.startedAt.Equal(first) {
		t.Fatal("unblockStatus without a gate restarted the step clock")
	}
}

// The one line the console prints about a settled memory run.
func TestMemoryRunLine(t *testing.T) {
	cases := map[string]acp.MemoryRunUpdate{
		"memory: recalled in 3.2s (task bg_3)":                                  {Status: "finished", TaskID: "bg_3", TaskStatus: "succeeded", DurationMs: 3210, Delivered: true},
		"memory: finished in 3.2s, nothing reached this turn (task bg_3)":       {Status: "finished", TaskID: "bg_3", TaskStatus: "succeeded", DurationMs: 3210},
		"memory: timed_out after 5m0s (task bg_4)":                              {Status: "finished", TaskID: "bg_4", TaskStatus: "timed_out", DurationMs: 300_000},
		"memory: failed after 1s (task bg_5) - create memory session: no store": {Status: "finished", TaskID: "bg_5", TaskStatus: "failed", DurationMs: 1000, Reason: "create memory session: no store"},
		"memory: skipped - memory runs in flight for this session: 2 of 2":      {Status: "skipped", Reason: "memory runs in flight for this session: 2 of 2"},
	}
	for want, u := range cases {
		if got := memoryRunLine(u); got != want {
			t.Errorf("memoryRunLine(%+v) = %q, want %q", u, got, want)
		}
	}
}

// The turn's clock and tokens are the numbers of the session on screen. A switch drops
// them - the next turn_progress of whichever turn the console then hears restores both
// from the server's figures - or the line would pair one session's clock with another
// session's tokens.
func TestASessionSwitchDropsTheTurnNumbersAndTheNextProgressRestoresThem(t *testing.T) {
	a := newRemoteControlStand(t).app
	a.sessionID = sharedControlSession
	a.turnStartedAt, a.turnTokens = time.Now().Add(-5*time.Minute), 1200

	a.adoptSession("sess_other", nil, nil)
	if !a.turnStartedAt.IsZero() || a.turnTokens != 0 {
		t.Fatalf("the other session's numbers stayed: started %v, %d tokens", a.turnStartedAt, a.turnTokens)
	}

	a.applyTurnProgress(acp.TurnProgressUpdate{ElapsedMs: 42_000, OutputTokens: 77})
	if got := time.Since(a.turnStartedAt).Round(time.Second); got != 42*time.Second || a.turnTokens != 77 {
		t.Fatalf("after the next turn_progress: clock %v, %d tokens", got, a.turnTokens)
	}
}
