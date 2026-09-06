package hooks_test

// Unit tests for the hooks package: the file shape, the matcher, the loader's
// scope and policy decisions, and the runner's process contract. Hook
// processes are the test binary itself re-executed through hooktest, so no
// shell scripts are involved and the tests hold on Windows too.

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
)

// TestHelperHook is not a real test: re-executed with the hook-helper
// positional arguments it becomes the hook process the tests below spawn.
func TestHelperHook(t *testing.T) {
	if !hooktest.Main(flag.Args()) {
		t.Skip("helper process")
	}
}

// ---- file shape ----

func TestParseClaudeShape(t *testing.T) {
	data := []byte(`{
	  "hooks": {
	    "PreToolUse": [
	      {"matcher": "run_command", "hooks": [
	        {"type": "command", "command": "./guard.sh", "timeout": 30},
	        {"type": "command", "command": "/usr/bin/python3", "args": ["policy.py", "--strict"], "failClosed": true, "async": true, "commandWindows": "py -3 policy.py"}
	      ]}
	    ],
	    "PostToolUse": [
	      {"hooks": [{"type": "command", "command": "gofmt -l .", "fail_closed": true}]}
	    ]
	  }
	}`)
	def, err := hooks.Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pre := def.Events[hooks.EventPreToolUse]
	if len(pre) != 1 || pre[0].Matcher != "run_command" || len(pre[0].Handlers) != 2 {
		t.Fatalf("PreToolUse groups = %+v", pre)
	}
	first := pre[0].Handlers[0]
	if first.Type != hooks.HandlerCommand || first.Command != "./guard.sh" || first.TimeoutSeconds != 30 || first.ExecForm {
		t.Fatalf("first handler = %+v", first)
	}
	second := pre[0].Handlers[1]
	if !second.ExecForm || len(second.Args) != 2 || second.Args[1] != "--strict" || !second.FailClosed || !second.Async || second.CommandWindows != "py -3 policy.py" {
		t.Fatalf("second handler = %+v", second)
	}
	post := def.Events[hooks.EventPostToolUse]
	if len(post) != 1 || post[0].Matcher != "" || len(post[0].Handlers) != 1 || !post[0].Handlers[0].FailClosed {
		t.Fatalf("PostToolUse groups = %+v", post)
	}
}

func TestParseFlagsUnsupportedHandlersAndIgnoresOtherKeys(t *testing.T) {
	data := []byte(`{
	  "permissions": {"allow": ["Bash(git *)"]},
	  "hooks": {
	    "PreToolUse": [{"matcher": "Bash", "hooks": [
	      {"type": "prompt", "prompt": "is this safe? $ARGUMENTS"},
	      {"type": "command", "command": "./ok.sh", "if": "Bash(rm *)"}
	    ]}],
	    "SomethingElse": [{"hooks": [{"type": "command", "command": "./never.sh"}]}]
	  }
	}`)
	def, err := hooks.Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	handlers := def.Events[hooks.EventPreToolUse][0].Handlers
	if len(handlers) != 2 {
		t.Fatalf("handlers = %+v", handlers)
	}
	if handlers[0].Unsupported == "" || !strings.Contains(handlers[0].Unsupported, "prompt") {
		t.Fatalf("prompt handler must be flagged unsupported, got %+v", handlers[0])
	}
	if handlers[1].Unsupported != "" {
		t.Fatalf("command handler must stay runnable, got %+v", handlers[1])
	}
	if _, ok := def.Events["SomethingElse"]; ok {
		t.Fatal("unknown events must not be kept")
	}
	if len(def.Warnings) == 0 {
		t.Fatal("unsupported handler and unknown event must leave warnings")
	}
}

func TestParseRejectsMalformedFiles(t *testing.T) {
	for _, data := range []string{`{not json`, `[]`, `{"hooks": []}`, `{"hooks": {"PreToolUse": {}}}`, `{"hooks": {"PreToolUse": [{"hooks": [{"type": "command"}]}]}}`} {
		if _, err := hooks.Parse([]byte(data)); err == nil {
			t.Fatalf("%s must fail to parse", data)
		}
	}
	def, err := hooks.Parse([]byte(`{"permissions": {}}`))
	if err != nil || len(def.Events) != 0 {
		t.Fatalf("a settings file without hooks is an empty definition, got %+v, %v", def, err)
	}
}

// ---- matcher ----

func TestMatchTool(t *testing.T) {
	cases := []struct {
		matcher, tool string
		want          bool
	}{
		{"", "run_command", true},
		{"*", "read", true},
		{"run_command", "run_command", true},
		{"run_command", "run_command_extra", false},
		{"edit|write", "write", true},
		{"edit, write", "edit", true},
		{"edit|write", "read", false},
		{"^run_.*", "run_command", true},
		{"read.*", "read", true},
		{"Bash", "run_command", true},
		{"Edit|Write", "edit", true},
		{"Edit|Write", "write", true},
		{"Read", "read", true},
		{"Task", "spawn_agent", true},
		{"Agent", "spawn_agent", true},
		{"WebFetch", "webfetch", true},
		{"mcp__filesystem__read_file", "filesystem__read_file", true},
		{"mcp__filesystem__.*", "filesystem__read_file", true},
		{"filesystem__.*", "filesystem__read_file", true},
		{"mcp__filesystem__.*", "other__read_file", false},
		{"(", "read", false},
	}
	for _, c := range cases {
		if got := hooks.MatchTool(c.matcher, c.tool); got != c.want {
			t.Errorf("MatchTool(%q, %q) = %v, want %v", c.matcher, c.tool, got, c.want)
		}
	}
}

func TestMatchSubjectHasNoToolAliases(t *testing.T) {
	if !hooks.Match("startup|resume", "resume") {
		t.Fatal("exact list must match the source")
	}
	if hooks.Match("Bash", "run_command") {
		t.Fatal("plain Match must not apply tool aliases")
	}
}

// ---- loader ----

func writeHooks(t *testing.T, path string, entries ...hooktest.Entry) {
	t.Helper()
	if err := hooktest.Write(path, entries...); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sourceByDisplay(sources []*hooks.Source, display string) *hooks.Source {
	for _, s := range sources {
		if s.Display == display {
			return s
		}
	}
	return nil
}

func TestLoaderScopesFilesAndAppliesPolicy(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "work")
	entry := hooktest.Entry{Event: hooks.EventPreToolUse, Matcher: "run_command", Handlers: []hooks.Handler{hooktest.Handler("allow")}}
	writeHooks(t, filepath.Join(home, "hooks.json"), entry)
	writeHooks(t, filepath.Join(cwd, ".coddy", "hooks.json"), entry)
	writeHooks(t, filepath.Join(cwd, ".claude", "settings.json"), entry)
	files := []string{"${CODDY_HOME}/hooks.json", "${CWD}/.claude/settings.json", "${CWD}/.claude/settings.local.json", "${CWD}/.coddy/hooks.json"}

	for _, tc := range []struct {
		policy          string
		wantProject     int
		projectRunnable bool
	}{
		{"ask", 2, false},
		{"allow", 2, true},
		{"deny", 0, false},
	} {
		sources := hooks.NewLoader(files, tc.policy).Load(cwd, home)
		user := sourceByDisplay(sources, filepath.Join(home, "hooks.json"))
		if user == nil || user.Scope != hooks.ScopeUser || !user.Runnable() {
			t.Fatalf("policy %s: user file must load and run, got %+v", tc.policy, user)
		}
		project := 0
		for _, s := range sources {
			if s.Scope != hooks.ScopeProject {
				continue
			}
			project++
			if s.Runnable() != tc.projectRunnable {
				t.Fatalf("policy %s: project source %s runnable = %v, want %v", tc.policy, s.Display, s.Runnable(), tc.projectRunnable)
			}
			if tc.policy == "ask" && s.Trust != hooks.TrustNeedsApproval {
				t.Fatalf("policy ask: project source must need approval, got %q", s.Trust)
			}
			if s.Digest == "" || !strings.HasPrefix(s.Digest, "sha256:") {
				t.Fatalf("project source must carry a digest, got %q", s.Digest)
			}
		}
		if project != tc.wantProject {
			t.Fatalf("policy %s: project sources = %d, want %d", tc.policy, project, tc.wantProject)
		}
		if missing := sourceByDisplay(sources, filepath.Join(".claude", "settings.local.json")); missing != nil {
			t.Fatalf("a missing file must not produce a source, got %+v", missing)
		}
	}
}

func TestLoaderKeepsUnparsableFileAsInvalidSource(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	sources := hooks.NewLoader([]string{"${CODDY_HOME}/hooks.json"}, "ask").Load(filepath.Join(root, "work"), home)
	if len(sources) != 1 || sources[0].Err == nil || sources[0].Runnable() {
		t.Fatalf("broken file must be listed as invalid and never run, got %+v", sources)
	}
}

// ---- runner ----

func userSource(entries ...hooktest.Entry) *hooks.Source {
	events := map[string][]hooks.Group{}
	for _, e := range entries {
		events[e.Event] = append(events[e.Event], hooks.Group{Matcher: e.Matcher, Handlers: e.Handlers})
	}
	return &hooks.Source{Display: "test", Scope: hooks.ScopeUser, Trust: hooks.TrustTrusted, Definition: &hooks.Definition{Events: events}}
}

func preToolUse(matcher string, handlers ...hooks.Handler) hooktest.Entry {
	return hooktest.Entry{Event: hooks.EventPreToolUse, Matcher: matcher, Handlers: handlers}
}

func newRunner(t *testing.T, sources ...*hooks.Source) *hooks.Runner {
	t.Helper()
	return &hooks.Runner{
		Sources:        sources,
		Session:        hooks.Session{ID: "sess-1", CWD: t.TempDir(), Mode: "agent", PermissionMode: "ask", Model: "fake/model", Turn: 1, TranscriptPath: "/tmp/none/messages.json"},
		TimeoutSeconds: 20,
		MaxOutputChars: 10000,
	}
}

func commandEvent(command string) hooks.Event {
	return hooks.ToolEvent(hooks.EventPreToolUse, "run_command", map[string]interface{}{"command": command}, "call-1")
}

func TestRunnerDenyFromJSONOnlyForMatchingCommands(t *testing.T) {
	r := newRunner(t, userSource(preToolUse("run_command", hooktest.Handler("deny", "rm -rf"))))
	out := r.Run(context.Background(), commandEvent("rm -rf /"))
	if out.Decision != hooks.DecisionDeny || out.Reason != "destructive command" || !out.Blocked() {
		t.Fatalf("outcome = %+v", out)
	}
	out = r.Run(context.Background(), commandEvent("echo hi"))
	if out.Decision != "" || out.Blocked() || out.Ran != 1 {
		t.Fatalf("silent hook must leave no decision, got %+v", out)
	}
}

func TestRunnerExitTwoBlocksWithStderr(t *testing.T) {
	r := newRunner(t, userSource(preToolUse("*", hooktest.Handler("exit2", "not on my watch"))))
	out := r.Run(context.Background(), commandEvent("echo hi"))
	if !out.Blocked() || out.Reason != "not on my watch" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestRunnerRunsOnlyMatchingGroups(t *testing.T) {
	r := newRunner(t, userSource(
		preToolUse("read", hooktest.Handler("exit2", "never")),
		hooktest.Entry{Event: hooks.EventPostToolUse, Matcher: "*", Handlers: []hooks.Handler{hooktest.Handler("exit2", "wrong event")}},
	))
	out := r.Run(context.Background(), commandEvent("echo hi"))
	if out.Ran != 0 || out.Blocked() {
		t.Fatalf("no group matches run_command on PreToolUse, got %+v", out)
	}
	if r.HasHandlers(hooks.EventSessionStart) {
		t.Fatal("no SessionStart handlers were defined")
	}
	if !r.HasHandlers(hooks.EventPostToolUse) {
		t.Fatal("PostToolUse handlers were defined")
	}
}

func TestRunnerChainsRewrittenInputAndCollectsContext(t *testing.T) {
	record := filepath.Join(t.TempDir(), "payload.json")
	r := newRunner(t, userSource(preToolUse("run_command",
		hooktest.Handler("rewrite", "echo one"),
		hooktest.Handler("record", record),
		hooktest.Handler("context", "first"),
		hooktest.Handler("context", "second"),
	)))
	out := r.Run(context.Background(), commandEvent("echo original"))
	if out.Decision != hooks.DecisionAllow {
		t.Fatalf("rewrite carries allow, got %+v", out)
	}
	if got, _ := out.UpdatedInput["command"].(string); got != "echo one" {
		t.Fatalf("updated input = %v", out.UpdatedInput)
	}
	if strings.Join(out.Context, "|") != "first|second" {
		t.Fatalf("context = %v", out.Context)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("second hook must have run: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["tool_input"].(map[string]interface{})["command"]; got != "echo one" {
		t.Fatalf("later hooks must see the rewritten input, got %v", got)
	}
}

func TestRunnerMostRestrictiveDecisionWins(t *testing.T) {
	r := newRunner(t, userSource(preToolUse("*",
		hooktest.Handler("allow"),
		hooktest.Handler("ask"),
		hooktest.Handler("allow"),
	)))
	out := r.Run(context.Background(), commandEvent("echo hi"))
	if out.Decision != hooks.DecisionAsk || out.Ran != 3 {
		t.Fatalf("ask must win over allow and every hook must run, got %+v", out)
	}
	r = newRunner(t, userSource(preToolUse("*",
		hooktest.Handler("deny", "echo"),
		hooktest.Handler("allow"),
	)))
	out = r.Run(context.Background(), commandEvent("echo hi"))
	if out.Decision != hooks.DecisionDeny || out.Ran != 2 {
		t.Fatalf("deny must win and later hooks still run, got %+v", out)
	}
}

func TestRunnerNonZeroExitIsNotBlockingUnlessFailClosed(t *testing.T) {
	r := newRunner(t, userSource(preToolUse("*", hooktest.Handler("fail", "3"))))
	out := r.Run(context.Background(), commandEvent("echo hi"))
	if out.Blocked() || len(out.Errors) != 1 || !strings.Contains(out.Errors[0], "exit status 3") {
		t.Fatalf("a crashing hook is a non-blocking error, got %+v", out)
	}
	closed := hooktest.Handler("fail", "3")
	closed.FailClosed = true
	r = newRunner(t, userSource(preToolUse("*", closed)))
	out = r.Run(context.Background(), commandEvent("echo hi"))
	if !out.Blocked() || !strings.Contains(out.Reason, "exit status 3") {
		t.Fatalf("failClosed turns the error into a block, got %+v", out)
	}
	r = newRunner(t, userSource(preToolUse("*", hooktest.Handler("garbage"))))
	out = r.Run(context.Background(), commandEvent("echo hi"))
	if out.Blocked() || len(out.Errors) != 1 {
		t.Fatalf("unparsable JSON is a non-blocking error, got %+v", out)
	}
}

func TestRunnerTimeoutTerminatesTheHook(t *testing.T) {
	slow := hooktest.Handler("sleep", "20")
	slow.TimeoutSeconds = 1
	r := newRunner(t, userSource(preToolUse("*", slow)))
	start := time.Now()
	out := r.Run(context.Background(), commandEvent("echo hi"))
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("timeout must cut the hook, took %v", elapsed)
	}
	if out.Blocked() || len(out.Errors) != 1 || !strings.Contains(out.Errors[0], "timed out") {
		t.Fatalf("timeout is a non-blocking error, got %+v", out)
	}
	slow.FailClosed = true
	r = newRunner(t, userSource(preToolUse("*", slow)))
	if out := r.Run(context.Background(), commandEvent("echo hi")); !out.Blocked() {
		t.Fatalf("failClosed timeout must block, got %+v", out)
	}
}

func TestRunnerPayloadAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "payload.json")
	envFile := filepath.Join(dir, "env.txt")
	r := newRunner(t, userSource(preToolUse("*",
		hooktest.Handler("record", record),
		hooktest.Handler("env", envFile, "CODDY_PROJECT_DIR", "CODDY_SESSION_ID", "CODDY_HOOK_EVENT", "CLAUDE_PROJECT_DIR"),
	)))
	r.Session.Subagent = &hooks.Subagent{Name: "reviewer", ParentSessionID: "parent-1", Depth: 1}
	if out := r.Run(context.Background(), commandEvent("echo payload")); out.Ran != 2 {
		t.Fatalf("both hooks must run, got %+v", out)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("payload must be JSON: %v", err)
	}
	for key, want := range map[string]interface{}{
		"session_id":      "sess-1",
		"hook_event_name": hooks.EventPreToolUse,
		"cwd":             r.Session.CWD,
		"transcript_path": "/tmp/none/messages.json",
		"permission_mode": "ask",
		"mode":            "agent",
		"model":           "fake/model",
		"turn":            float64(1),
		"tool_name":       "run_command",
		"tool_use_id":     "call-1",
	} {
		if payload[key] != want {
			t.Fatalf("payload[%s] = %v, want %v", key, payload[key], want)
		}
	}
	if sub, _ := payload["subagent"].(map[string]interface{}); sub["name"] != "reviewer" || sub["parent_session_id"] != "parent-1" || sub["depth"] != float64(1) {
		t.Fatalf("subagent block = %v", payload["subagent"])
	}
	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"CODDY_PROJECT_DIR=" + r.Session.CWD,
		"CODDY_SESSION_ID=sess-1",
		"CODDY_HOOK_EVENT=" + hooks.EventPreToolUse,
		"CLAUDE_PROJECT_DIR=" + r.Session.CWD,
	} {
		if !strings.Contains(string(env), line+"\n") {
			t.Fatalf("environment lacks %q:\n%s", line, env)
		}
	}
}

func TestRunnerPlainStdoutIsContextOnlyOnPromptEvents(t *testing.T) {
	r := newRunner(t, userSource(
		hooktest.Entry{Event: hooks.EventUserPromptSubmit, Handlers: []hooks.Handler{hooktest.Handler("text", "remember the tests")}},
		preToolUse("*", hooktest.Handler("text", "ignored")),
	))
	out := r.Run(context.Background(), hooks.Event{Name: hooks.EventUserPromptSubmit, Fields: map[string]interface{}{"prompt": "hi"}})
	if strings.Join(out.Context, "|") != "remember the tests" {
		t.Fatalf("plain stdout on UserPromptSubmit is context, got %+v", out)
	}
	out = r.Run(context.Background(), commandEvent("echo hi"))
	if len(out.Context) != 0 || out.Blocked() || len(out.Errors) != 0 {
		t.Fatalf("plain stdout on PreToolUse is ignored, got %+v", out)
	}
}

func TestRunnerStopDecisionsAndContinueFalse(t *testing.T) {
	r := newRunner(t, userSource(hooktest.Entry{Event: hooks.EventStop, Handlers: []hooks.Handler{hooktest.Handler("block", "run the tests first")}}))
	out := r.Run(context.Background(), hooks.Event{Name: hooks.EventStop, Fields: map[string]interface{}{"stop_hook_active": false}})
	if out.Decision != hooks.DecisionBlock || out.Reason != "run the tests first" || !out.Blocked() {
		t.Fatalf("outcome = %+v", out)
	}
	r = newRunner(t, userSource(preToolUse("*", hooktest.Handler("continue-false", "halt everything"), hooktest.Handler("system-message", "shown to the user"))))
	out = r.Run(context.Background(), commandEvent("echo hi"))
	if !out.Stop || out.StopReason != "halt everything" {
		t.Fatalf("continue:false must stop, got %+v", out)
	}
	if strings.Join(out.SystemMessages, "|") != "shown to the user" {
		t.Fatalf("systemMessage must be collected, got %+v", out)
	}
}

func TestRunnerAsyncHooksNeverDecide(t *testing.T) {
	async := hooktest.Handler("exit2", "too late")
	async.Async = true
	r := newRunner(t, userSource(preToolUse("*", async)))
	// The detached process may start after this test's temp dir is gone.
	r.Session.CWD = os.TempDir()
	out := r.Run(context.Background(), commandEvent("echo hi"))
	if out.Blocked() || out.Ran != 1 {
		t.Fatalf("async hooks cannot block, got %+v", out)
	}
}

func TestRunnerSkipsSourcesThatMayNotRun(t *testing.T) {
	record := filepath.Join(t.TempDir(), "payload.json")
	held := userSource(preToolUse("*", hooktest.Handler("record", record)))
	held.Scope = hooks.ScopeProject
	held.Trust = hooks.TrustNeedsApproval
	unsupported := userSource(preToolUse("*", hooks.Handler{Type: "prompt", Unsupported: "handler type prompt is not supported"}))
	r := newRunner(t, held, unsupported)
	out := r.Run(context.Background(), commandEvent("echo hi"))
	if out.Ran != 0 {
		t.Fatalf("held and unsupported handlers must not run, got %+v", out)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("the unapproved project hook ran")
	}
}

func TestRunnerTruncatesOversizedContext(t *testing.T) {
	long := strings.Repeat("x", 500)
	r := newRunner(t, userSource(preToolUse("*", hooktest.Handler("context", long))))
	r.MaxOutputChars = 100
	out := r.Run(context.Background(), commandEvent("echo hi"))
	if len(out.Context) != 1 || len(out.Context[0]) > 160 || !strings.Contains(out.Context[0], "truncated") {
		t.Fatalf("context must be truncated with a marker, got %d chars: %q", len(out.Context[0]), out.Context[0])
	}
}

func TestRunnerShellFormGoesThroughTheHostShell(t *testing.T) {
	handler := hooks.Handler{
		Type:    hooks.HandlerCommand,
		Command: `"` + os.Args[0] + `" -test.run=^TestHelperHook$ ` + hooktest.Sentinel + ` allow`,
	}
	r := newRunner(t, userSource(preToolUse("*", handler)))
	out := r.Run(context.Background(), commandEvent("echo hi"))
	if out.Decision != hooks.DecisionAllow || len(out.Errors) != 0 {
		t.Fatalf("shell form must run the helper, got %+v", out)
	}
}
