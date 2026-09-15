package session_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestPendingPermissionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	params := acp.PermissionRequestParams{
		SessionID: "sess_test",
		ToolCall: acp.PermissionToolCall{
			ToolCallID: "call_1",
			Title:      "Run: run_command",
			Kind:       "shell",
			Status:     "pending",
		},
		Options: []acp.PermissionOption{
			{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
		},
	}
	if err := session.WritePendingPermission(dir, params, "run_command", `{"command":"ls"}`); err != nil {
		t.Fatal(err)
	}
	if !session.PendingPermissionHeld(dir) {
		t.Fatal("expected held")
	}
	got, err := session.ReadPendingPermission(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ToolCall.ToolCallID != "call_1" {
		t.Fatalf("toolCallId %q", got.ToolCall.ToolCallID)
	}
	if got.ToolName != "run_command" {
		t.Fatalf("toolName %q", got.ToolName)
	}
	if err := session.ClearPendingPermission(dir); err != nil {
		t.Fatal(err)
	}
	if session.PendingPermissionHeld(dir) {
		t.Fatal("expected cleared")
	}
	_, err = session.ReadPendingPermission(dir)
	if err == nil {
		t.Fatal("expected read error after clear")
	}
	_ = filepath.Base(dir)
}

// --- pending_plan_context.go ------------------------------------------------

// The hand-off of a plan run outlives the process that started the turn: the
// permission prompt it stopped on is answered later, and the continuation
// renders the same system prompt.
func TestPendingPlanContextRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got := session.ReadPendingPlanContext(dir); got != "" {
		t.Fatalf("an empty bundle reported a hand-off: %q", got)
	}
	if err := session.WritePendingPlanContext(dir, "PLAN BODY"); err != nil {
		t.Fatal(err)
	}
	if got := session.ReadPendingPlanContext(dir); got != "PLAN BODY" {
		t.Fatalf("read back %q", got)
	}
	if err := session.ClearPendingPlanContext(dir); err != nil {
		t.Fatal(err)
	}
	if got := session.ReadPendingPlanContext(dir); got != "" {
		t.Fatalf("the hand-off survived the clear: %q", got)
	}
	// Clearing what is not there is how a turn that never ran a plan ends.
	if err := session.ClearPendingPlanContext(dir); err != nil {
		t.Fatalf("clearing an absent record: %v", err)
	}
}

// A record written by another version, or one somebody truncated, reads as
// absent. Refusing the resume over it would cost the user their answer; the
// missing hand-off costs the model context and nothing else.
func TestUnreadablePendingPlanContextReadsAsAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pending_plan_context.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := session.ReadPendingPlanContext(dir); got != "" {
		t.Fatalf("a malformed record was read as %q", got)
	}
}

// Without a bundle the hand-off is memory only, and nothing tries to write it.
func TestPendingPlanContextWithoutABundle(t *testing.T) {
	st := &session.State{ID: "t", CWD: t.TempDir()}
	st.SetPendingPlanContext("PLAN BODY")
	if got := st.PendingPlanContext(); got != "PLAN BODY" {
		t.Fatalf("read back %q", got)
	}
	st.ClearPendingPlanContext()
	if got := st.PendingPlanContext(); got != "" {
		t.Fatalf("the hand-off survived the clear: %q", got)
	}
	if err := session.WritePendingPlanContext("", "x"); err == nil {
		t.Fatal("writing without a session directory must fail")
	}
}
