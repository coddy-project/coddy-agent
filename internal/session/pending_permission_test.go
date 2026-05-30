package session_test

import (
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
