//go:build http

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestSessionMutationIPC(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	mgr := session.NewManager(&config.Config{}, nil, nil, slog.Default(), "", &session.FileStore{Root: root})
	calls := 0
	handler := sessionManagementIPCHandler(mgr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/coddy/sessions/sess_123" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method == http.MethodPatch {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["title"] != "new" {
				t.Errorf("body = %v, %v", body, err)
			}
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	owner, err := localipc.Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- owner.Serve(ctx, handler) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		got, err := mutateStoredSession(ctx, home, root, "sess_123", method, map[string]any{"title": "new"})
		if err != nil || string(got) != `{"ok":true}` {
			t.Fatalf("%s: %s %v", method, got, err)
		}
	}
	if _, err := mutateStoredSession(ctx, home, t.TempDir(), "sess_123", http.MethodDelete, nil); err == nil {
		t.Fatal("different root accepted")
	}
	if calls != 2 {
		t.Fatalf("delegated %d times", calls)
	}
}

func TestSessionMutationIPCIncompleteReply(t *testing.T) {
	for _, tc := range []struct {
		name             string
		body, disconnect bool
	}{
		{"timeout_headers", false, false}, {"timeout_body", true, false},
		{"lost_headers", false, true}, {"lost_body", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			owner, err := localipc.Claim(home)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- owner.Serve(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.body {
						w.Header().Set("Content-Length", "32")
						w.Header().Set(sessionManagementResponseHeader, "1")
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
					}
					if tc.disconnect {
						panic(http.ErrAbortHandler) // Drop the connection after accepting the request.
					}
					<-r.Context().Done()
				}))
			}()
			t.Cleanup(func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
				if err := owner.Close(); err != nil {
					t.Error(err)
				}
			})
			_, err = mutateStoredSessionWithTimeout(ctx, home, t.TempDir(), "sess_test", http.MethodDelete, nil, 200*time.Millisecond)
			if err == nil || !strings.Contains(err.Error(), "outcome unknown") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSessionMutationIPCOldOwner(t *testing.T) {
	home := t.TempDir()
	owner, err := localipc.Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- owner.Serve(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/coddy/sessions/") {
				t.Error("unsafe legacy mutation")
			}
			_, _ = io.WriteString(w, `{}`)
		}))
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := mutateStoredSession(ctx, home, t.TempDir(), "sess_123", http.MethodDelete, nil); err == nil {
		t.Fatal("old owner accepted")
	}
}
