package agent

// Edge cases of the PreToolUse `handled` answer; the happy path lives in
// features/hooks_tool_calls.feature. A handler that reports the call as failed
// must answer for it like a failed local run: the error reaches the model as
// the tool result, the tool itself never runs, and no permission prompt is
// raised for work that already happened.

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
)

func TestHandledHookErrorFailsTheCallWithoutRunningIt(t *testing.T) {
	s := &hooksFeatureState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.addHook(hooks.EventPreToolUse, "run_command", hooktest.Handler("handle-error", "conflict: buffer changed")); err != nil {
		t.Fatal(err)
	}
	if err := s.agentSession(); err != nil {
		t.Fatal(err)
	}
	if err := s.modelRunsCommand("echo never-run"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.lastResult, "conflict: buffer changed") {
		t.Fatalf("tool result %q must carry the handler's error", s.lastResult)
	}
	if strings.Contains(s.lastResult, "never-run") {
		t.Fatalf("tool result %q must not report a local run", s.lastResult)
	}
	if err := s.noPermissionRequest(); err != nil {
		t.Fatal(err)
	}
}
