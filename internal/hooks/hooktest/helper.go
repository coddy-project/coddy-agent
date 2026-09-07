// Package hooktest re-executes the current test binary as a hook process, so
// hook behaviour can be tested without shell scripts and on every platform.
//
// A test binary that wants to act as a hook defines a test named TestHelperHook
// that calls Main(flag.Args()); the hook handlers built by Handler run that
// binary with "-test.run=^TestHelperHook$ hook-helper <mode> <params...>", so
// the ordinary test run (no positional arguments) skips the helper.
package hooktest

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
)

// Sentinel is the first positional argument that turns a test binary into a
// hook helper.
const Sentinel = "hook-helper"

// Main runs the helper when args name it and never returns in that case (the
// process exits with the code the mode prescribes). It returns false when the
// arguments do not select the helper, so the calling test can skip.
func Main(args []string) bool {
	if len(args) < 2 || args[0] != Sentinel {
		return false
	}
	mode := args[1]
	params := args[2:]
	stdin, _ := io.ReadAll(os.Stdin)
	var payload map[string]interface{}
	_ = json.Unmarshal(stdin, &payload)
	command := ""
	if ti, ok := payload["tool_input"].(map[string]interface{}); ok {
		command, _ = ti["command"].(string)
	}
	eventName, _ := payload["hook_event_name"].(string)
	param := func(i int) string {
		if i < len(params) {
			return params[i]
		}
		return ""
	}
	emit := func(v interface{}) {
		b, _ := json.Marshal(v)
		_, _ = os.Stdout.Write(b)
		os.Exit(0)
	}
	specific := func(fields map[string]interface{}) map[string]interface{} {
		fields["hookEventName"] = eventName
		return map[string]interface{}{"hookSpecificOutput": fields}
	}
	switch mode {
	case "silent":
		os.Exit(0)
	case "deny":
		// Deny when the command carries the fragment; stay silent otherwise.
		if strings.Contains(command, param(0)) {
			emit(specific(map[string]interface{}{
				"permissionDecision":       "deny",
				"permissionDecisionReason": "destructive command",
			}))
		}
		os.Exit(0)
	case "allow":
		emit(specific(map[string]interface{}{"permissionDecision": "allow"}))
	case "ask":
		emit(specific(map[string]interface{}{"permissionDecision": "ask", "permissionDecisionReason": "hook asks"}))
	case "rewrite":
		emit(specific(map[string]interface{}{
			"permissionDecision": "allow",
			"updatedInput":       map[string]interface{}{"command": param(0)},
		}))
	case "rewrite-ask":
		// Rewrite the command and still leave the permission prompt in place.
		emit(specific(map[string]interface{}{
			"permissionDecision": "ask",
			"updatedInput":       map[string]interface{}{"command": param(0)},
		}))
	case "context":
		emit(specific(map[string]interface{}{"additionalContext": param(0)}))
	case "text":
		fmt.Print(param(0))
		os.Exit(0)
	case "exit2":
		fmt.Fprint(os.Stderr, param(0))
		os.Exit(2)
	case "fail":
		code, _ := strconv.Atoi(param(0))
		fmt.Fprint(os.Stderr, "helper failing on purpose")
		os.Exit(code)
	case "sleep":
		secs, _ := strconv.Atoi(param(0))
		time.Sleep(time.Duration(secs) * time.Second)
		os.Exit(0)
	case "record":
		_ = os.WriteFile(param(0), stdin, 0o600)
		os.Exit(0)
	case "env":
		var b strings.Builder
		for _, name := range params[1:] {
			b.WriteString(name + "=" + os.Getenv(name) + "\n")
		}
		_ = os.WriteFile(param(0), []byte(b.String()), 0o600)
		os.Exit(0)
	case "block":
		emit(map[string]interface{}{"decision": "block", "reason": param(0)})
	case "block-once":
		// Block unless the payload says a stop hook already continued the turn.
		if active, _ := payload["stop_hook_active"].(bool); active {
			os.Exit(0)
		}
		emit(map[string]interface{}{"decision": "block", "reason": param(0)})
	case "continue-false":
		emit(map[string]interface{}{"continue": false, "stopReason": param(0)})
	case "system-message":
		emit(map[string]interface{}{"systemMessage": param(0)})
	case "garbage":
		fmt.Print("{not json")
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "hook helper: unknown mode %q", mode)
	os.Exit(3)
	return true
}

// Handler returns an exec-form command handler that re-runs the current test
// binary as the helper in the given mode.
func Handler(mode string, params ...string) hooks.Handler {
	return hooks.Handler{
		Type:     hooks.HandlerCommand,
		Command:  os.Args[0],
		Args:     append([]string{"-test.run=^TestHelperHook$", Sentinel, mode}, params...),
		ExecForm: true,
	}
}

// Entry is one matcher group of one event in a definition file.
type Entry struct {
	Event    string
	Matcher  string
	Handlers []hooks.Handler
}

// Write renders entries in the Claude Code file shape and writes them to path,
// creating parent directories.
func Write(path string, entries ...Entry) error {
	events := map[string][]interface{}{}
	for _, e := range entries {
		handlers := make([]interface{}, 0, len(e.Handlers))
		for _, h := range e.Handlers {
			row := map[string]interface{}{"type": h.Type, "command": h.Command}
			if h.ExecForm {
				row["args"] = h.Args
			}
			if h.TimeoutSeconds > 0 {
				row["timeout"] = h.TimeoutSeconds
			}
			if h.Async {
				row["async"] = true
			}
			if h.FailClosed {
				row["failClosed"] = true
			}
			handlers = append(handlers, row)
		}
		group := map[string]interface{}{"hooks": handlers}
		if e.Matcher != "" {
			group["matcher"] = e.Matcher
		}
		events[e.Event] = append(events[e.Event], group)
	}
	data, err := json.MarshalIndent(map[string]interface{}{"hooks": events}, "", "  ")
	if err != nil {
		return err
	}
	if dir := dirOf(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o600)
}

func dirOf(path string) string {
	i := strings.LastIndexAny(path, `/\`)
	if i < 0 {
		return ""
	}
	return path[:i]
}
