package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

// The ToolKind enum of the ACP schema. A strict client drops a tool_call whose
// kind is anything else, so nothing outside it may reach the wire.
var schemaToolKinds = []string{"read", "edit", "delete", "move", "search", "execute", "think", "fetch", "switch_mode", "other"}

func TestToolKindEnumMatchesSchema(t *testing.T) {
	for _, k := range schemaToolKinds {
		if !IsToolKind(k) {
			t.Errorf("IsToolKind(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"", "write", "run_command", "shell", "fs", "tool", "todo", "Edit"} {
		if IsToolKind(k) {
			t.Errorf("IsToolKind(%q) = true, want false", k)
		}
	}
}

func TestWireToolKind(t *testing.T) {
	cases := []struct {
		kind, title, want string
	}{
		// The tool name decides when the title is one.
		{"write", "write", ToolKindEdit},
		{"write", "edit", ToolKindEdit},
		{"write", "apply_patch", ToolKindEdit},
		{"write", "mkdir", ToolKindEdit},
		{"write", "touch", ToolKindEdit},
		{"write", "config_commit", ToolKindEdit},
		{"write", "config_rollback", ToolKindEdit},
		{"write", "rm", ToolKindDelete},
		{"write", "rmdir", ToolKindDelete},
		{"write", "mv", ToolKindMove},
		{"run_command", "run_command", ToolKindExecute},
		{"other", "ssh_run_command", ToolKindExecute},
		{"read", "glob", ToolKindSearch},
		{"read", "grep", ToolKindSearch},
		{"read", "coddy_docs_search", ToolKindSearch},
		{"read", "webfetch", ToolKindFetch},
		{"read", "websearch", ToolKindFetch},
		{"other", "http_request", ToolKindFetch},
		{"read", "read", ToolKindRead},
		// A permission prompt titles the call "Run: <name>".
		{"write", "Run: rm", ToolKindDelete},
		{"run_command", "Run: run_command", ToolKindExecute},
		// Without a known name, the internal kind is translated.
		{"write", "", ToolKindEdit},
		{"run_command", "", ToolKindExecute},
		{"read", "probe__lookup", ToolKindRead},
		{"switch_mode", "", ToolKindSwitchMode},
		{"other", "probe__lookup", ToolKindOther},
		// Anything else falls back to other, never past the enum.
		{"", "", ToolKindOther},
		{"shell", "", ToolKindOther},
		{"fs", "something", ToolKindOther},
		{"Write", "", ToolKindOther},
	}
	for _, c := range cases {
		if got := WireToolKind(c.kind, c.title); got != c.want {
			t.Errorf("WireToolKind(%q, %q) = %q, want %q", c.kind, c.title, got, c.want)
		}
	}
}

func newCapturingServer() (*Server, *bytes.Buffer) {
	var out bytes.Buffer
	s := NewServer(nil, nil)
	s.writer = json.NewEncoder(&out)
	return s, &out
}

func decodeWireKind(t *testing.T, out *bytes.Buffer, path ...string) string {
	t.Helper()
	var msg map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &msg); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	var cur interface{} = msg
	for _, p := range path {
		m, _ := cur.(map[string]interface{})
		cur = m[p]
	}
	kind, _ := cur.(string)
	return kind
}

// Every tool_call the server writes carries a kind from the ACP enum, however
// the agent spelled it internally.
func TestSendSessionUpdateEmitsSchemaToolKinds(t *testing.T) {
	internal := []string{"read", "write", "run_command", "other", "switch_mode", "", "shell", "fs", "tool", "todo"}
	titles := []string{"", "write", "rm", "mv", "run_command", "grep", "webfetch", "probe__lookup", "Run: rm"}
	for _, kind := range internal {
		for _, title := range titles {
			for _, ptr := range []bool{false, true} {
				s, out := newCapturingServer()
				u := ToolCallUpdate{SessionUpdate: UpdateTypeToolCall, ToolCallID: "c1", Title: title, Kind: kind, Status: "pending"}
				var update interface{} = u
				if ptr {
					update = &u
				}
				if err := s.SendSessionUpdate("sess", update); err != nil {
					t.Fatal(err)
				}
				got := decodeWireKind(t, out, "params", "update", "kind")
				if !IsToolKind(got) {
					t.Errorf("kind %q title %q (ptr=%v): wire kind %q is outside the ACP enum", kind, title, ptr, got)
				}
			}
		}
	}
}

func TestSendSessionUpdateMapsToolKindsByName(t *testing.T) {
	cases := map[string]string{
		"write": ToolKindEdit, "rm": ToolKindDelete, "mv": ToolKindMove,
		"run_command": ToolKindExecute, "grep": ToolKindSearch, "webfetch": ToolKindFetch,
	}
	for title, want := range cases {
		s, out := newCapturingServer()
		u := &ToolCallUpdate{SessionUpdate: UpdateTypeToolCall, ToolCallID: "c1", Title: title, Kind: "write", Status: "pending"}
		if err := s.SendSessionUpdate("sess", u); err != nil {
			t.Fatal(err)
		}
		if got := decodeWireKind(t, out, "params", "update", "kind"); got != want {
			t.Errorf("tool %q: wire kind %q, want %q", title, got, want)
		}
		// The caller's value is left as it was: other surfaces read the internal kind.
		if u.Kind != "write" {
			t.Errorf("SendSessionUpdate changed the caller's kind to %q", u.Kind)
		}
	}
}

func TestRequestPermissionEmitsSchemaToolKind(t *testing.T) {
	s, out := newCapturingServer()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _ = s.RequestPermission(ctx, PermissionRequestParams{
		SessionID: "sess",
		ToolCall:  PermissionToolCall{ToolCallID: "c1", Title: "Run: run_command", Kind: "run_command", Status: "pending"},
	})
	if got := decodeWireKind(t, out, "params", "toolCall", "kind"); got != ToolKindExecute {
		t.Errorf("permission tool call kind %q, want %q", got, ToolKindExecute)
	}
}
