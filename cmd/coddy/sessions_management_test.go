package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestSessionsManagementSafety(t *testing.T) {
	t.Setenv("CODDY_HOME", t.TempDir())
	t.Setenv("CODDY_CONFIG", "")
	store := &session.FileStore{Root: filepath.Join(t.TempDir(), "sessions")}
	for _, id := range []string{"sess_main", "sess_main_more", "sched_hidden"} {
		if err := saveManagedSession(store, id, "", false); err != nil {
			t.Fatal(err)
		}
	}
	child, err := store.EnsureChildLayout("sess_main", "sess_nested")
	if err != nil {
		t.Fatal(err)
	}
	if err := saveManagedSession(store, "sess_nested", child, false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id, input, current string
		interactive, yes         bool
		wantErr                  string
		wantMutation             bool
	}{
		{name: "ambiguous", id: "sess_ma", yes: true, wantErr: "sess_main, sess_main_more"},
		{name: "hidden child", id: "sess_nested", yes: true, wantErr: "no visible session"},
		{name: "hidden scheduler", id: "sched_hidden", yes: true, wantErr: "no visible session"},
		{name: "pipe cannot confirm", id: "sess_main", input: "yes\n", wantErr: "interactive terminal or --yes"},
		{name: "decline", id: "sess_main", interactive: true, input: "no\n", wantErr: "cancelled"},
		{name: "EOF", id: "sess_main", interactive: true, wantErr: "cancelled"},
		{name: "current", id: "sess_main", current: "sess_main", yes: true, wantErr: "current session"},
		{name: "ancestor", id: "sess_main", current: "sess_nested", yes: true, wantErr: "contains the current session"},
		{name: "exact match wins", id: "sess_main", yes: true, wantMutation: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CODDY_SESSION_ID", tc.current)
			var out, stderr bytes.Buffer
			called := false
			cmd := sessionsCommand{out: &out, errOut: &stderr, in: strings.NewReader(tc.input), interactive: tc.interactive, mutate: func(_ context.Context, home, root, id, method string, patch map[string]any) (json.RawMessage, error) {
				called = true
				if method != http.MethodDelete || patch != nil || id != "sess_main" || root != store.Root || home == "" {
					t.Fatalf("unexpected mutation: %s %q home=%q root=%q patch=%v", method, id, home, root, patch)
				}
				return json.RawMessage(`{"id":"sess_main"}`), nil
			}}
			args := []string{"delete", tc.id, "--sessions-dir", store.Root, "--format", "json"}
			if tc.yes {
				args = append(args, "--yes")
			}
			err := cmd.run(args)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			if called != tc.wantMutation {
				t.Fatalf("mutation called = %v, want %v", called, tc.wantMutation)
			}
			if tc.wantMutation && strings.TrimSpace(out.String()) != `{"id":"sess_main"}` || !tc.wantMutation && out.Len() != 0 {
				t.Fatalf("unexpected stdout: %q", &out)
			}
			if !tc.interactive && stderr.Len() != 0 {
				t.Fatalf("unexpected prompt: %q", &stderr)
			}
		})
	}
}

func TestSessionsManagementArchivedUpdate(t *testing.T) {
	t.Setenv("CODDY_HOME", t.TempDir())
	t.Setenv("CODDY_CONFIG", "")
	store := &session.FileStore{Root: filepath.Join(t.TempDir(), "sessions")}
	if err := saveManagedSession(store, "sess_archived", "", true); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	var received map[string]any
	cmd := sessionsCommand{out: &out, errOut: &stderr, mutate: func(_ context.Context, _, _, id, method string, patch map[string]any) (json.RawMessage, error) {
		if id != "sess_archived" || method != http.MethodPatch {
			t.Fatalf("unexpected mutation: %s %q", method, id)
		}
		received = patch
		return json.Marshal(patch)
	}}
	run := func(args ...string) {
		t.Helper()
		out.Reset()
		if err := cmd.run(append(args, "--sessions-dir", store.Root, "--format", "json")); err != nil {
			t.Fatal(err)
		}
	}
	run("list")
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("list showed archive: %s", &out)
	}
	run("show", "sess_arc")
	var info storedSessionInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Archived || info.Mode != "ask" || info.MessageCount == nil || *info.MessageCount != 1 || info.TaskCount == nil || *info.TaskCount != 0 {
		t.Fatalf("unexpected show result: %s", &out)
	}
	run("update", "sess_arc", "--tags=", "--archived=false")
	if want := (map[string]any{"tags": []string{}, "archived": false}); !reflect.DeepEqual(received, want) {
		t.Fatalf("patch = %#v, want %#v", received, want)
	}
}

func TestSessionsManagementReportsManagerFailure(t *testing.T) {
	t.Setenv("CODDY_HOME", t.TempDir())
	t.Setenv("CODDY_CONFIG", "")
	t.Setenv("CODDY_SESSION_ID", "")
	store := &session.FileStore{Root: filepath.Join(t.TempDir(), "sessions")}
	if err := saveManagedSession(store, "sess_running", "", false); err != nil {
		t.Fatal(err)
	}
	for _, cause := range []error{session.ErrTurnNotSettled, fmt.Errorf("delete failed after removing a descendant"), errors.New("no Coddy runtime IPC endpoint is available")} {
		var out, stderr bytes.Buffer
		cmd := sessionsCommand{out: &out, errOut: &stderr, mutate: func(context.Context, string, string, string, string, map[string]any) (json.RawMessage, error) {
			return nil, cause
		}}
		err := cmd.run([]string{"delete", "sess_running", "--sessions-dir", store.Root, "--yes"})
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "sess_running") || out.Len() != 0 || !store.HasPersistedSnapshot("sess_running") {
			t.Fatalf("error = %v, stdout = %q, persisted = %v", err, &out, store.HasPersistedSnapshot("sess_running"))
		}
	}
}
