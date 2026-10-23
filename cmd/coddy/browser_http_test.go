//go:build http

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// The extension key is documented to start agent conversations, not to reach
// every conversation on the server. These hold that line: a session id the
// extension merely names is refused, and the chat list only shows the ones
// this port started.

func TestChatScopeOwnsOnlyWhatItAdopted(t *testing.T) {
	scope := &chatScope{started: make(map[string]struct{})}
	if scope.owns("sess_a") {
		t.Fatal("a conversation nobody started is already owned")
	}
	if err := scope.adopt("sess_a"); err != nil {
		t.Fatal(err)
	}
	if !scope.owns("sess_a") {
		t.Fatal("an adopted conversation is not owned")
	}
	if scope.owns("sess_b") {
		t.Fatal("adopting one conversation owns another")
	}
}

func TestKeepStartedChatsDropsForeignRows(t *testing.T) {
	scope := &chatScope{started: map[string]struct{}{"sess_mine": {}}}
	body, err := json.Marshal(map[string]any{
		"object":     "coddy.session_list",
		"sessions":   []any{map[string]any{"id": "sess_mine", "title": "Mine"}, map[string]any{"id": "sess_theirs", "title": "Theirs"}},
		"nextCursor": "12",
		"hasMore":    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := keepStartedChats(body, scope)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Sessions   []map[string]any `json:"sessions"`
		NextCursor *string          `json:"nextCursor"`
		HasMore    bool             `json:"hasMore"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Sessions) != 1 || doc.Sessions[0]["id"] != "sess_mine" {
		t.Fatalf("list kept the conversations outside this port's reach: %s", out)
	}
	// Preserve the cursor so the panel can reach owned chats on later pages.
	if doc.NextCursor == nil || *doc.NextCursor != "12" || !doc.HasMore {
		t.Fatalf("pagination still points past a filtered list: %s", out)
	}
}

func TestKeepStartedChatsLeavesAnUnknownShapeAlone(t *testing.T) {
	body := []byte(`{"object":"coddy.session_list"}`)
	out, err := keepStartedChats(body, &chatScope{started: make(map[string]struct{})})
	if err == nil && string(out) != string(body) {
		t.Fatalf("rewrote a body it does not understand: %s", out)
	}
}

func TestBrowserChatRefusesConversationsItDidNotStart(t *testing.T) {
	browserChats = &chatScope{started: map[string]struct{}{"sess_mine": {}}}
	handler, cleanup := browserChatHandler(&serve.Runtime{})
	defer cleanup()

	cases := []struct {
		name   string
		method string
		path   string
		header string
		body   string
	}{
		{"a permission prompt of another conversation", http.MethodPost, "/coddy/sessions/sess_theirs/permission", "", `{"toolCallId":"t","optionId":"allow"}`},
		{"the transcript of another conversation", http.MethodGet, "/coddy/sessions/sess_theirs/messages", "", ""},
		{"a turn that names another conversation", http.MethodPost, "/v1/responses", "sess_theirs", `{"model":"agent","input":"hi","stream":true}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			if c.header != "" {
				r.Header.Set("X-Coddy-Session-ID", c.header)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status %d, want %d: %s", w.Code, http.StatusForbidden, w.Body.String())
			}
		})
	}
}

func TestBrowserChatStillDropsRoutesItDoesNotExpose(t *testing.T) {
	browserChats = &chatScope{started: map[string]struct{}{"sess_mine": {}}}
	handler, cleanup := browserChatHandler(&serve.Runtime{})
	defer cleanup()

	// Ownership is not a widening of the route surface: a conversation this
	// port started still cannot reach the routes the allowlist omits, like
	// the compact call, which would rewrite the transcript.
	r := httptest.NewRequest(http.MethodPost, "/coddy/sessions/sess_mine/compact", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("an unexposed route answered %d, want 404", w.Code)
	}
}

func TestChatScopeSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	first := &chatScope{started: make(map[string]struct{}), dir: dir}
	if err := first.adopt("sess_mine"); err != nil {
		t.Fatal(err)
	}
	restarted := &chatScope{started: make(map[string]struct{}), dir: dir}
	if !restarted.owns("sess_mine") || restarted.owns("sess_foreign") {
		t.Fatal("restart lost ownership or granted a foreign conversation")
	}
}

func TestChatScopeDoesNotGrantWhenPersistenceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	scope := &chatScope{started: make(map[string]struct{}), dir: path}
	w := httptest.NewRecorder()
	w.Header().Set("X-Coddy-Session-ID", "sess_mine")
	response := &chatStartResponse{ResponseWriter: w, scope: scope, minted: true}
	response.WriteHeader(http.StatusOK)
	if w.Code != http.StatusInternalServerError || scope.owns("sess_mine") {
		t.Fatal("failed persistence reported success or granted access")
	}
}

func TestKeepStartedChatsCanContinuePastForeignPage(t *testing.T) {
	scope := &chatScope{started: map[string]struct{}{"sess_mine": {}}}
	out, err := keepStartedChats([]byte(`{"sessions":[{"id":"sess_foreign"}],"nextCursor":"50","hasMore":true}`), scope)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Sessions   []any
		NextCursor string
		HasMore    bool
	}
	if err := json.Unmarshal(out, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 0 || page.NextCursor != "50" || !page.HasMore {
		t.Fatalf("lost continuation after a foreign page: %s", out)
	}
	out, err = keepStartedChats([]byte(`{"sessions":[{"id":"sess_mine"}],"nextCursor":null,"hasMore":false}`), scope)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "sess_mine") {
		t.Fatal("lost owned chat on next page")
	}
}
