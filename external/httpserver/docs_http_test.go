//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func newDocsTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = root
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, nil)
	srv := New(cfg, mgr, slog.Default(), root)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})
	return ts
}

func docsGet(t *testing.T, ts *httptest.Server, path string) (int, map[string]interface{}) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET %s: not JSON: %v", path, err)
	}
	return resp.StatusCode, body
}

func TestDocsRoutesRefuseWhatTheyCannotAnswer(t *testing.T) {
	ts := newDocsTestServer(t)
	for _, tc := range []struct {
		path string
		code int
		msg  string
	}{
		{"/coddy/docs/page", http.StatusBadRequest, "ref is required"},
		{"/coddy/docs/page?ref=features/nowhere", http.StatusNotFound, "no documentation page"},
		{"/coddy/docs/page?ref=features/mentions%23nowhere", http.StatusNotFound, "has no section #nowhere"},
		{"/coddy/docs/search?q=proxy&limit=0", http.StatusBadRequest, "limit must be"},
		{"/coddy/docs/search?q=proxy&limit=x", http.StatusBadRequest, "limit must be"},
	} {
		code, body := docsGet(t, ts, tc.path)
		errObj, _ := body["error"].(map[string]interface{})
		msg, _ := errObj["message"].(string)
		if code != tc.code || !strings.Contains(msg, tc.msg) {
			t.Errorf("GET %s = %d %q, want %d %q", tc.path, code, msg, tc.code, tc.msg)
		}
	}
}

func TestDocsPageCarriesTheSectionItWasAskedFor(t *testing.T) {
	ts := newDocsTestServer(t)
	code, body := docsGet(t, ts, "/coddy/docs/page?ref=@coddy:features/mentions%23completion")
	if code != http.StatusOK || body["slug"] != "features/mentions" || body["anchor"] != "completion" || body["url"] != "https://coddy.dev/docs/features/mentions" {
		t.Fatalf("page: %d %v %v %v", code, body["slug"], body["anchor"], body["url"])
	}
	code, body = docsGet(t, ts, "/coddy/docs/page?ref=getting-started/quickstart")
	if code != http.StatusOK || body["prev"] != nil {
		t.Fatalf("the first page has no page before it: %v", body["prev"])
	}
}

func TestDocsSearchWithoutAQueryFindsNothing(t *testing.T) {
	ts := newDocsTestServer(t)
	code, body := docsGet(t, ts, "/coddy/docs/search?q=")
	hits, ok := body["hits"].([]interface{})
	if code != http.StatusOK || !ok || len(hits) != 0 {
		t.Fatalf("empty query: %d %v", code, body)
	}
	_, body = docsGet(t, ts, "/coddy/docs/search?q=telegram+proxy&limit=2")
	if hits, _ := body["hits"].([]interface{}); len(hits) != 2 {
		t.Fatalf("limit 2: %v", body["hits"])
	}
}
