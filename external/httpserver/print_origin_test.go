//go:build http

package httpserver

// The runs of one-shot print mode (`coddy -p`) on the HTTP surface: the
// listing leaves them out unless asked, the bulk delete scopes reach them, and
// a remote print run asks for the mark with the prompt that creates its
// session. The happy path is features/print_sessions.feature.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// storePrintRun persists one bundle the way `coddy -p` creates it: marked as a
// print run from its first write.
func storePrintRun(t *testing.T, mgr *session.Manager, store *session.FileStore, text string) string {
	t.Helper()
	mgr.SetNextSessionOrigin(session.PrintOrigin)
	return storeSession(t, mgr, store, text)
}

func sameSet(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			return false
		}
	}
	return true
}

func TestSessionListLeavesPrintRunsOutUnlessAsked(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	chat := storeSession(t, mgr, store, "a person's chat")
	run := storePrintRun(t, mgr, store, "a script's run")

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"", []string{chat}},
		{"include_print=true", []string{chat, run}},
		{"origin=print", []string{run}},
		{"origin=local", []string{chat}},
		{"origin=local&include_print=true", []string{chat}},
	} {
		code, body := getSessions(t, srv, tc.query)
		if code != http.StatusOK {
			t.Fatalf("%q: status %d", tc.query, code)
		}
		if got := listedIDs(body); !sameSet(got, tc.want...) {
			t.Errorf("%q: listed %v, want %v", tc.query, got, tc.want)
		}
	}

	_, body := getSessions(t, srv, "origin=print")
	raw, _ := body["sessions"].([]interface{})
	row, _ := raw[0].(map[string]interface{})
	if got, _ := row["origin"].(string); got != session.PrintOrigin {
		t.Fatalf("row origin = %q, want %q", got, session.PrintOrigin)
	}

	code, body := getSessions(t, srv, "origin=cli")
	if code != http.StatusBadRequest || !strings.Contains(body["error"].(map[string]interface{})["message"].(string), `"print"`) {
		t.Fatalf("origin=cli: status %d body %v, want 400 naming print", code, body)
	}
}

func TestBulkDeleteScopesReachPrintRuns(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	run := storePrintRun(t, mgr, store, "an archived script run")
	if code, body := patchSessionJSON(t, srv, run, map[string]interface{}{"archived": true}); code != http.StatusOK {
		t.Fatalf("archive: %d %v", code, body)
	}
	if code, body := postBulkDelete(t, srv, map[string]interface{}{"scope": "archived"}); code != http.StatusOK {
		t.Fatalf("bulk delete archived: %d %v", code, body)
	}
	if _, err := os.Stat(store.SessionPath(run)); !os.IsNotExist(err) {
		t.Fatalf("emptying the archive left the archived print run: %v", err)
	}

	chat := storeSession(t, mgr, store, "a chat")
	other := storePrintRun(t, mgr, store, "a live script run")
	if code, body := postBulkDelete(t, srv, map[string]interface{}{"scope": "all"}); code != http.StatusOK {
		t.Fatalf("bulk delete all: %d %v", code, body)
	}
	for _, id := range []string{chat, other} {
		if _, err := os.Stat(store.SessionPath(id)); !os.IsNotExist(err) {
			t.Fatalf("the whole history kept %s: %v", id, err)
		}
	}
}

func postResponse(t *testing.T, srv *Server, sessionID, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"agent","input":"hi","stream":false}`))
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("X-Coddy-Session-ID", sessionID)
	}
	if origin != "" {
		req.Header.Set(session.OriginHeader, origin)
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	return rec
}

func TestPromptHeaderMarksOnlyTheSessionItCreates(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)

	const fresh = "sess_remote_print_run"
	if rec := postResponse(t, srv, fresh, session.PrintOrigin); rec.Code != http.StatusOK {
		t.Fatalf("prompt: %d %s", rec.Code, rec.Body.String())
	}
	snap, err := store.ReadSnapshot(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.Origin != session.PrintOrigin {
		t.Fatalf("created session origin = %q, want %q", snap.Meta.Origin, session.PrintOrigin)
	}

	// Without an id the server mints one and marks it the same way.
	rec := postResponse(t, srv, "", session.PrintOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("prompt: %d %s", rec.Code, rec.Body.String())
	}
	minted := rec.Header().Get("X-Coddy-Session-ID")
	if snap, err := store.ReadSnapshot(minted); err != nil || snap.Meta.Origin != session.PrintOrigin {
		t.Fatalf("minted session %q origin = %+v, %v", minted, snap, err)
	}

	chat := storeSession(t, mgr, store, "a person's chat")
	if rec := postResponse(t, srv, chat, session.PrintOrigin); rec.Code != http.StatusOK {
		t.Fatalf("prompt: %d %s", rec.Code, rec.Body.String())
	}
	if snap, _ := store.ReadSnapshot(chat); snap.Meta.Origin != "" {
		t.Fatalf("an existing chat was relabelled %q", snap.Meta.Origin)
	}
}

func TestPromptHeaderRefusesAnyOtherOrigin(t *testing.T) {
	srv, _, store := bulkDeleteServer(t)
	const id = "sess_pretends_to_be_a_bot"
	rec := postResponse(t, srv, id, session.GatewayOrigin("telegram"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), session.OriginHeader) {
		t.Fatalf("status %d body %s, want 400 naming the header", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(store.SessionPath(id)); !os.IsNotExist(err) {
		t.Fatalf("a refused request created a bundle: %v", err)
	}
}
