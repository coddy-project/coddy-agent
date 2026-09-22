//go:build http

package httpserver

// Edge cases of the subagent HTTP surface that are not part of the happy path
// in features/subagents_http.feature: a child transcript cannot be branched,
// a child bundle is stored inside the session that spawned it rather than
// beside it in the sessions root, the catalog reports every bound a definition
// declares, and the catalog routes answer errors as JSON.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	defer func() { _ = resp.Body.Close() }()
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
	childID := session.NewSessionID()
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

func TestSubagentHTTPRewindingAChildIsRefused(t *testing.T) {
	rig := newSubagentEdgeRig(t)
	childID := rig.createChild(t)
	payload := map[string]interface{}{"userMessageIndex": 0}

	status, body := rig.request(t, http.MethodPost, "/coddy/sessions/"+childID+"/rewind", payload, nil)
	if status != http.StatusConflict || !strings.Contains(errorMessage(body), "read-only") {
		t.Fatalf("live child rewind = %d %v, want 409 read-only", status, body)
	}

	rig.mgr.RetireSubagentSession(childID)
	status, body = rig.request(t, http.MethodPost, "/coddy/sessions/"+childID+"/rewind", payload, nil)
	if status != http.StatusConflict || !strings.Contains(errorMessage(body), rig.parent) {
		t.Fatalf("retired child rewind = %d %v, want 409 naming the parent", status, body)
	}
	// Nothing was truncated: the sessions root still holds the parent alone, and
	// the child is where it was written, inside the parent's bundle.
	entries, err := os.ReadDir(rig.store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != rig.parent {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("session bundles after the refusals = %v, want only the parent", names)
	}
	want := filepath.Join(rig.store.SessionPath(rig.parent), session.ChildSessionsDirName, childID)
	if got := rig.store.SessionPath(childID); got != want {
		t.Fatalf("child bundle at %q, want %q", got, want)
	}
}

// A child transcript is reachable by its id like any other session - nothing
// about the id sets it apart any more - and every route that would write to it
// refuses, naming the chat to prompt instead.
func TestSubagentHTTPChildIsReadOnlyThroughItsID(t *testing.T) {
	rig := newSubagentEdgeRig(t)
	childID := rig.createChild(t)
	if st, err := rig.mgr.EnsureHTTPSession(context.Background(), childID, rig.root); err != nil || st == nil {
		t.Fatalf("EnsureHTTPSession on a live child = %v, %v", st, err)
	}

	headers := map[string]string{"X-Coddy-Session-ID": childID}
	chat := map[string]interface{}{"model": "openai/gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}}
	status, body := rig.request(t, http.MethodPost, "/v1/chat/completions", chat, headers)
	if status != http.StatusConflict || !strings.Contains(errorMessage(body), rig.parent) {
		t.Fatalf("chat completions against a child = %d %v, want 409 naming the parent", status, body)
	}
	responses := map[string]interface{}{"model": "openai/gpt-4o", "input": "hi"}
	status, body = rig.request(t, http.MethodPost, "/v1/responses", responses, headers)
	if status != http.StatusConflict || !strings.Contains(errorMessage(body), rig.parent) {
		t.Fatalf("responses against a child = %d %v, want 409 naming the parent", status, body)
	}
	workspace := map[string]interface{}{"path": rig.root}
	status, body = rig.request(t, http.MethodPost, "/coddy/sessions/"+childID+"/workspace", workspace, nil)
	if status != http.StatusConflict {
		t.Fatalf("workspace on a child = %d %v, want 409", status, body)
	}
}

// catalogRow finds one definition in a GET /coddy/subagents body.
func catalogRow(t *testing.T, body map[string]interface{}, name string) map[string]interface{} {
	t.Helper()
	items, _ := body["items"].([]interface{})
	for _, raw := range items {
		if row, ok := raw.(map[string]interface{}); ok && row["name"] == name {
			return row
		}
	}
	t.Fatalf("catalog does not list %q: %v", name, body)
	return nil
}

// The catalog is what an approval surface reasons about, so it has to name the
// bounds the definition declares - not just its name and description - and
// never the role body of a file nobody has approved yet.
func TestSubagentCatalogServesTheDeclaredBounds(t *testing.T) {
	rig := newSubagentEdgeRig(t)
	writeProjectDefinition(t, rig.root, "bounded", "---\ndescription: lives in the project workspace\n"+
		"tools: read, grep\ndisallowed_tools: run_command\npermission_mode: ask\ntimeout_seconds: 120\nmax_turns: 7\nbackground: true\n---\n"+
		"You review code and report findings.\n")

	status, body := rig.request(t, http.MethodGet, "/coddy/subagents", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("catalog status %d body %v", status, body)
	}
	row := catalogRow(t, body, "bounded")
	tools, _ := row["tools"].([]interface{})
	if len(tools) != 2 || tools[0] != "read" || tools[1] != "grep" {
		t.Fatalf("tools = %v", row["tools"])
	}
	denied, _ := row["disallowed_tools"].([]interface{})
	if len(denied) != 1 || denied[0] != "run_command" {
		t.Fatalf("disallowed_tools = %v", row["disallowed_tools"])
	}
	if row["permission_mode"] != "ask" || row["background"] != true {
		t.Fatalf("bounds = %v", row)
	}
	if secs, _ := row["timeout_seconds"].(float64); secs != 120 {
		t.Fatalf("timeout_seconds = %v", row["timeout_seconds"])
	}
	if turns, _ := row["max_turns"].(float64); turns != 7 {
		t.Fatalf("max_turns = %v", row["max_turns"])
	}
	if size, _ := row["role_bytes"].(float64); size == 0 {
		t.Fatalf("role_bytes = %v", row["role_bytes"])
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "You review code") {
		t.Fatalf("the role body reached the client: %s", encoded)
	}
	// A built-in restricting nothing must not claim an empty allowlist.
	if general := catalogRow(t, body, "general"); general["tools"] != nil {
		t.Fatalf("general restricts no tools, so the row must omit the key: %v", general)
	}
}

// Every error on the catalog routes is read by the SPA with res.json(), so the
// body has to be JSON and say so.
func TestSubagentRouteErrorsAreJSON(t *testing.T) {
	rig := newSubagentEdgeRig(t)
	for _, tc := range []struct {
		name, method, path string
		want               int
	}{
		{"relative cwd", http.MethodGet, "/coddy/subagents?cwd=relative/path", http.StatusBadRequest},
		{"unknown name", http.MethodPost, "/coddy/subagents/nope/trust", http.StatusNotFound},
		{"builtin", http.MethodPost, "/coddy/subagents/general/trust", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, rig.ts.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", res.StatusCode, tc.want)
			}
			if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("Content-Type %q", ct)
			}
			raw, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			var parsed struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &parsed); err != nil {
				t.Fatalf("body is not JSON (%v): %s", err, raw)
			}
			if strings.TrimSpace(parsed.Error.Message) == "" {
				t.Fatalf("error body carries no message: %s", raw)
			}
		})
	}
}

// writeProjectDefinition puts a definition into the workspace's .coddy/agents.
func writeProjectDefinition(t *testing.T, workspace, name, body string) {
	t.Helper()
	dir := filepath.Join(workspace, ".coddy", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
