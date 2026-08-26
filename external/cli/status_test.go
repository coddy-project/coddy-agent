//go:build cli

package cli

import (
	"strings"
	"testing"
	"time"
)

func TestStatusVerbForTool(t *testing.T) {
	cases := map[string]string{
		"read":                      "Reading",
		"print_tree":                "Listing",
		"grep":                      "Searching",
		"glob":                      "Searching",
		"APPLY_PATCH":               "Editing",
		"write":                     "Writing",
		"run_command":               "Running",
		"ssh_run_command":           "Running over SSH",
		"rmdir":                     "Deleting",
		"webfetch":                  "Fetching",
		"load_skill":                "Loading a skill",
		"plan_read":                 "Reading the plan",
		"plan_write":                "Updating the plan",
		"coddy_todo_write":          "Updating the plan",
		"coddy_todo_plan_read":      "Reading the plan",
		"coddy_scheduler_job_get":   "Updating the schedule",
		"coddy_memory_search":       "Working with memory",
		"config_set":                "Updating the configuration",
		"background_wait":           "Waiting for a background task",
		"background_output":         "Reading background output",
		"background_stop":           "Stopping a background task",
		"background_reap":           "Cleaning up background tasks",
		"background_list":           "Checking background tasks",
		"some_mcp_server__do_thing": "Running a tool",
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
		{"pattern", "grep", `{"pattern":"TODO","path":"internal"}`, "TODO"},
		{"query", "websearch", `{"query":"go slog"}`, "go slog"},
		{"source", "mv", `{"src":"a.go","dst":"b.go"}`, "a.go"},
		{"url", "webfetch", `{"url":"https://example.dev"}`, "https://example.dev"},
		{"arguments envelope", "read", `Arguments: {"path":"a.go"}`, "a.go"},
		// The body of a write is never the target: it would fill the whole row.
		{"never the body", "write", `{"path":"a.go","content":"package main"}`, "a.go"},
		{"question carries no target", "question", `{"question":"which one?"}`, ""},
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
	if got := newWorkingStatus("Thinking…", "").statusText(3 * time.Second); got != "Thinking… · 3s" {
		t.Errorf("bare verb status = %q", got)
	}

	// Blocked on the operator: nothing is running, so a climbing counter would be a lie.
	blocked := newBlockedStatus("Waiting for your approval")
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

func TestStatusMessageBeforeAnyStep(t *testing.T) {
	a := &App{}
	if got := a.statusMessage(); got != statusWaitingModel {
		t.Errorf("statusMessage() = %q, want %q", got, statusWaitingModel)
	}
}
