//go:build http

package httpserver

// Edge cases of the subagent HTTP surface that are not part of the happy
// path in features/subagents_http.feature: the reserved sub_ prefix cannot
// mint a session through the header, and a child transcript cannot be
// branched.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type subagentEdgeRig struct {
	ts     *httptest.Server
	mgr    *session.Manager
	store  *session.FileStore
	root   string
	parent string
}

func newSubagentEdgeRig(t *testing.T) *subagentEdgeRig {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sessRoot := filepath.Join(root, "sessions")
	for _, dir := range []string{home, sessRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: root},
		Models:    []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:     config.Agent{Model: "openai/gpt-4o"},
		Subagents: config.Subagents{Dirs: config.DefaultSubagentDirs()},
	}
	store := &session.FileStore{Root: sessRoot}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, store)
	srv := New(cfg, mgr, slog.Default(), root)
	ts := httptest.NewServer(srv.Handler())
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})
	return &subagentEdgeRig{ts: ts, mgr: mgr, store: store, root: root, parent: res.SessionID}
}

func (r *subagentEdgeRig) request(t *testing.T, method, path string, payload interface{}, headers map[string]string) (int, map[string]interface{}) {
	t.Helper()
	var body *bytes.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, r.ts.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func errorMessage(body map[string]interface{}) string {
	if e, ok := body["error"].(map[string]interface{}); ok {
		msg, _ := e["message"].(string)
		return msg
	}
	return ""
}

// createChild registers a child of the parent and runs its single turn, so
// it has a transcript like a real run leaves behind.
func (r *subagentEdgeRig) createChild(t *testing.T) string {
	t.Helper()
	childID := session.NewSubagentSessionID()
	if _, err := r.mgr.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: r.parent, Name: "explore", TaskID: "bg_1", CWD: r.root, Depth: 1,
	}); err != nil {
		t.Fatal(err)
	}
	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "look around"}}
	if _, err := r.mgr.RunSubagentTurn(context.Background(), childID, prompt, noopSender{}); err != nil {
		t.Fatal(err)
	}
	return childID
}

func TestSubagentHTTPBranchingAChildIsRefused(t *testing.T) {
	rig := newSubagentEdgeRig(t)
	childID := rig.createChild(t)
	payload := map[string]interface{}{"userMessageIndex": 0}

	status, body := rig.request(t, http.MethodPost, "/coddy/sessions/"+childID+"/branches", payload, nil)
	if status != http.StatusConflict || !strings.Contains(errorMessage(body), "read-only") {
		t.Fatalf("live child branch = %d %v, want 409 read-only", status, body)
	}

	rig.mgr.RetireSubagentSession(childID)
	status, body = rig.request(t, http.MethodPost, "/coddy/sessions/"+childID+"/branches", payload, nil)
	if status != http.StatusConflict || !strings.Contains(errorMessage(body), rig.parent) {
		t.Fatalf("retired child branch = %d %v, want 409 naming the parent", status, body)
	}
	// Nothing was forked.
	entries, err := os.ReadDir(rig.store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("session bundles after the refusals = %v, want only the parent and the child", names)
	}
}

func TestSubagentHTTPHeaderCannotMintAReservedSession(t *testing.T) {
	rig := newSubagentEdgeRig(t)
	ghost := "sub_00000000000000000000dead"
	headers := map[string]string{"X-Coddy-Session-ID": ghost}
	chat := map[string]interface{}{"model": "openai/gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}}
	status, body := rig.request(t, http.MethodPost, "/v1/chat/completions", chat, headers)
	if status != http.StatusNotFound {
		t.Fatalf("chat completions with an unknown sub_ header = %d %v, want 404", status, body)
	}
	responses := map[string]interface{}{"model": "openai/gpt-4o", "input": "hi"}
	status, body = rig.request(t, http.MethodPost, "/v1/responses", responses, headers)
	if status != http.StatusNotFound {
		t.Fatalf("responses with an unknown sub_ header = %d %v, want 404", status, body)
	}
	workspace := map[string]interface{}{"path": rig.root}
	status, body = rig.request(t, http.MethodPost, "/coddy/sessions/"+ghost+"/workspace", workspace, nil)
	if status != http.StatusNotFound {
		t.Fatalf("workspace on an unknown sub_ id = %d %v, want 404", status, body)
	}
	if rig.mgr.SessionByID(ghost) != nil || rig.store.HasPersistedSnapshot(ghost) {
		t.Fatal("a session was created under the reserved prefix")
	}
	// A real child is still served through the same header.
	childID := rig.createChild(t)
	if st, err := rig.mgr.EnsureHTTPSession(context.Background(), childID, rig.root); err != nil || st == nil {
		t.Fatalf("EnsureHTTPSession on a live child = %v, %v", st, err)
	}
}
