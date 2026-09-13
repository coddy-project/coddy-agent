//go:build http

package httpserver

// Edge and error cases of POST /coddy/sessions/bulk-delete and of the
// include_stats rows of GET /coddy/sessions. The happy paths are the godog
// spec features/session_management.feature.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// bulkDeleteServer builds a server over an empty session store.
func bulkDeleteServer(t *testing.T) (*Server, *session.Manager, *session.FileStore) {
	t.Helper()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "p1", Type: "openai", APIKey: "k"}},
		Models:    []config.ModelEntry{{Model: "p1/gpt-4o"}},
		Agent:     config.Agent{Model: "p1/gpt-4o"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, store)
	srv := New(cfg, mgr, slog.Default(), root)
	t.Cleanup(srv.Drain)
	return srv, mgr, store
}

// storeSession persists one bundle carrying a single user turn.
func storeSession(t *testing.T, mgr *session.Manager, store *session.FileStore, text string) string {
	t.Helper()
	res, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)
	if st == nil {
		t.Fatalf("session %q not registered", res.SessionID)
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text})
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	return res.SessionID
}

func postBulkDelete(t *testing.T, srv *Server, payload interface{}) (int, map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/coddy/sessions/bulk-delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	var parsed map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return rec.Code, parsed
}

func TestBulkDeleteRejectsMalformedRequests(t *testing.T) {
	srv, _, _ := bulkDeleteServer(t)

	cases := []struct {
		name    string
		payload interface{}
	}{
		{"empty ids", map[string]interface{}{"ids": []string{}}},
		{"no ids and no scope", map[string]interface{}{}},
		{"unknown scope", map[string]interface{}{"scope": "everything"}},
		{"ids together with scope all", map[string]interface{}{"scope": "all", "ids": []string{"sess_abc"}}},
		{"traversal in an id", map[string]interface{}{"ids": []string{"../../etc"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _ := postBulkDelete(t, srv, tc.payload)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", code)
			}
		})
	}

	// A body that is not JSON at all is a 400 too, not a panic.
	req := httptest.NewRequest(http.MethodPost, "/coddy/sessions/bulk-delete", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-JSON body: status = %d, want 400", rec.Code)
	}
}

// An id with no bundle is not an error: the single-session DELETE answers 200
// for it, and a table whose row was removed by another tab must not be told the
// whole batch failed.
func TestBulkDeleteCountsAnAlreadyGoneSession(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	kept := storeSession(t, mgr, store, "keep me")

	code, body := postBulkDelete(t, srv, map[string]interface{}{
		"ids": []string{"sess_deadbeefdeadbeefdeadbeef"},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", code, body)
	}
	deleted, _ := body["deleted"].([]interface{})
	failed, _ := body["failed"].([]interface{})
	if len(deleted) != 1 || len(failed) != 0 {
		t.Fatalf("deleted = %v, failed = %v; want one deleted and no failures", deleted, failed)
	}
	if !store.HasPersistedSnapshot(kept) {
		t.Fatal("an unrelated session was removed")
	}
}

// The same id twice is one deletion, and `requested` counts what was actually
// attempted rather than the length of the client's list.
func TestBulkDeleteDeduplicatesIDs(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	id := storeSession(t, mgr, store, "once")

	code, body := postBulkDelete(t, srv, map[string]interface{}{"ids": []string{id, id}})
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", code, body)
	}
	if got, _ := body["requested"].(float64); int(got) != 1 {
		t.Fatalf("requested = %v, want 1", body["requested"])
	}
	deleted, _ := body["deleted"].([]interface{})
	if len(deleted) != 1 {
		t.Fatalf("deleted = %v, want exactly one entry", deleted)
	}
	if store.HasPersistedSnapshot(id) {
		t.Fatal("session bundle survived the delete")
	}
}

// An exception is a promise that a named session survives. An id that matches
// nothing would quietly turn "keep this one" into "delete everything", so it is
// refused before anything is removed.
func TestBulkDeleteRefusesAnExceptionThatIsNotStored(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	first := storeSession(t, mgr, store, "one")
	second := storeSession(t, mgr, store, "two")

	for _, except := range []string{
		"sess_notstoredanywhere00000", // well formed, no bundle
		"../../etc",                   // not an id at all
	} {
		code, body := postBulkDelete(t, srv, map[string]interface{}{
			"scope":  "all",
			"except": []string{except},
		})
		if code != http.StatusBadRequest {
			t.Fatalf("except %q: status = %d, want 400: %v", except, code, body)
		}
	}
	for _, id := range []string{first, second} {
		if !store.HasPersistedSnapshot(id) {
			t.Fatalf("session %q was removed by a refused request", id)
		}
	}
}

// except only means something for scope "all"; accepting it beside an ids list
// would let a caller believe a session was spared that nothing ever consulted.
func TestBulkDeleteRefusesExceptWithAnIDsList(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	id := storeSession(t, mgr, store, "one")

	code, body := postBulkDelete(t, srv, map[string]interface{}{
		"ids":    []string{id},
		"except": []string{id},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %v", code, body)
	}
	if !store.HasPersistedSnapshot(id) {
		t.Fatal("the session was removed by a refused request")
	}
}

// A subagent child is not in the default listing, but its parent is, and the
// delete takes the whole tree. Sparing the child therefore has to spare the
// parent as well, or "delete all but the one I am reading" would delete it.
func TestBulkDeleteAllSparesTheAncestorsOfAnException(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	parent := storeSession(t, mgr, store, "the parent chat")
	other := storeSession(t, mgr, store, "an unrelated chat")

	// A child bundle the way spawn_agent stores one: inside the parent's.
	child := session.NewSessionID()
	dir, err := store.EnsureChildLayout(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: child, CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: dir}
	st.SetSubagentMeta(session.SubagentMeta{ParentSessionID: parent, Name: "explore", TaskID: "bg_1"})
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "go look"})
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	code, body := postBulkDelete(t, srv, map[string]interface{}{
		"scope":  "all",
		"except": []string{child},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", code, body)
	}
	if !store.HasPersistedSnapshot(child) {
		t.Fatal("the excepted child session was deleted with its parent tree")
	}
	if !store.HasPersistedSnapshot(parent) {
		t.Fatal("the parent of the excepted child was deleted, which removes the child")
	}
	if store.HasPersistedSnapshot(other) {
		t.Fatal("an unrelated session survived scope=all")
	}
}

// One tree that will not settle must not abandon the batch: the rest is still
// removed and the answer names the one that stayed, with the retryable reason.
func TestBulkDeleteCarriesOnPastAFailure(t *testing.T) {
	requested, deleted, failed := bulkDeleteSessions(
		[]string{"sess_a", "sess_b", "sess_a", "sess_c"},
		func(id string) error {
			if id == "sess_b" {
				return session.ErrTurnNotSettled
			}
			return nil
		},
	)
	if requested != 3 {
		t.Fatalf("requested = %d, want 3 (the duplicate is one attempt)", requested)
	}
	if len(deleted) != 2 || deleted[0] != "sess_a" || deleted[1] != "sess_c" {
		t.Fatalf("deleted = %v, want the two that could go", deleted)
	}
	if len(failed) != 1 || failed[0]["id"] != "sess_b" {
		t.Fatalf("failed = %v, want only sess_b", failed)
	}
	if failed[0]["error"] != session.ErrTurnNotSettled.Error() {
		t.Fatalf("failed error = %q, want the retryable reason verbatim", failed[0]["error"])
	}
}

// An unexpected failure is logged, not handed back: a filesystem error names
// paths the caller has no business reading, and the single-delete route is
// generic for the same reason.
func TestBulkDeleteDoesNotLeakAnUnexpectedError(t *testing.T) {
	_, deleted, failed := bulkDeleteSessions([]string{"sess_a"}, func(string) error {
		return errors.New("unlinkat /home/someone/.coddy/sessions/sess_a: permission denied")
	})
	if len(deleted) != 0 {
		t.Fatalf("deleted = %v, want none", deleted)
	}
	if len(failed) != 1 || failed[0]["error"] != "delete failed" {
		t.Fatalf("failed = %v, want a generic message", failed)
	}
}

// Without include_stats the rows keep the shape every existing client parses.
func TestSessionListOmitsStatisticsUnlessAsked(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	storeSession(t, mgr, store, "hello")

	req := httptest.NewRequest(http.MethodGet, "/coddy/sessions", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Sessions []map[string]interface{} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(body.Sessions))
	}
	for _, field := range []string{"createdAt", "model", "messageCount", "tokenUsage"} {
		if _, present := body.Sessions[0][field]; present {
			t.Fatalf("default listing carries %q: %v", field, body.Sessions[0])
		}
	}
}

// A session that has never completed a model call has no stats.json; its row
// still reports a token usage object so the table sorts numerically.
func TestSessionListReportsZeroTokensWithoutStatsFile(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	storeSession(t, mgr, store, "no model call yet")

	req := httptest.NewRequest(http.MethodGet, "/coddy/sessions?include_stats=true", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Sessions []struct {
			MessageCount int            `json:"messageCount"`
			Model        string         `json:"model"`
			CreatedAt    string         `json:"createdAt"`
			TokenUsage   map[string]int `json:"tokenUsage"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(body.Sessions))
	}
	row := body.Sessions[0]
	if row.MessageCount != 1 {
		t.Fatalf("messageCount = %d, want 1", row.MessageCount)
	}
	if row.CreatedAt == "" {
		t.Fatal("a bundle created by this build carries no createdAt")
	}
	if row.Model != "" {
		t.Fatalf("model = %q, want empty for a session with no override", row.Model)
	}
	for _, field := range []string{"inputTokens", "outputTokens", "totalTokens"} {
		if row.TokenUsage[field] != 0 {
			t.Fatalf("tokenUsage.%s = %d, want 0", field, row.TokenUsage[field])
		}
	}
}

// The bulk route destroys user data, so it must sit behind the same gate as
// every other /coddy route rather than relying on the SPA never calling it.
func TestBulkDeleteIsRefusedWithoutACredential(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))

	body := bytes.NewReader([]byte(`{"scope":"all"}`))
	req := httptest.NewRequest(http.MethodPost, "/coddy/sessions/bulk-delete", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// A bundle written by an older build has no creation stamp. The row leaves the
// field out rather than reporting a moment that was never recorded, so a table
// can render an explicit "unknown".
func TestSessionListOmitsCreatedAtForALegacyBundle(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	id := storeSession(t, mgr, store, "stored by an older build")

	// Rewrite session.json the way a build without the field left it.
	metaPath := filepath.Join(store.SessionPath(id), "session.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	delete(meta, "createdAt")
	patched, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, patched, 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/coddy/sessions?include_stats=true", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Sessions []map[string]interface{} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(body.Sessions))
	}
	if _, present := body.Sessions[0]["createdAt"]; present {
		t.Fatalf("a legacy bundle reported a createdAt: %v", body.Sessions[0])
	}
}
