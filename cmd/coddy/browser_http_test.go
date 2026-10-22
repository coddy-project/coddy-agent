//go:build http

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	scope.adopt("sess_a")
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
	// The inner handler pages through the whole store; a cursor would lead
	// somewhere this caller may not go.
	if doc.NextCursor != nil || doc.HasMore {
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
