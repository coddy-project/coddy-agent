package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TestHelperMCPServer is not a real test: when re-executed with
// GO_WANT_MCP_HELPER=1 it becomes a minimal MCP stdio server speaking
// newline-delimited JSON-RPC with two tools (echo, reverse).
func TestHelperMCPServer(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		t.Skip("helper process")
	}
	runFakeMCPServer()
	os.Exit(0)
}

func runFakeMCPServer() {
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	respond := func(id interface{}, result interface{}) {
		msg := map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result}
		data, _ := json.Marshal(msg)
		_, _ = out.Write(append(data, '\n'))
		_ = out.Flush()
	}
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID     interface{}     `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil || req.ID == nil {
			continue // notification or garbage
		}
		switch req.Method {
		case "initialize":
			respond(req.ID, map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "fake-mcp", "version": "0.0.1"},
			})
		case "tools/list":
			schema := map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"text": map[string]interface{}{"type": "string"}},
			}
			respond(req.ID, map[string]interface{}{"tools": []map[string]interface{}{
				{"name": "echo", "description": "Echo text back", "inputSchema": schema},
				{"name": "reverse", "description": "Reverse text", "inputSchema": schema},
			}})
		case "tools/call":
			var params struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			text := params.Arguments["text"]
			if params.Name == "reverse" {
				r := []rune(text)
				for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
					r[i], r[j] = r[j], r[i]
				}
				text = string(r)
			}
			respond(req.ID, map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": text}},
			})
		default:
			respond(req.ID, nil)
		}
	}
}

// fakeServerConfig returns an MCPServerConfig that re-executes this test
// binary as the fake MCP server above.
func fakeServerConfig(name string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Name:    name,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperMCPServer"},
		Env:     []config.EnvVarConfig{{Name: "GO_WANT_MCP_HELPER", Value: "1"}},
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestStdioClientAgainstFakeServer(t *testing.T) {
	srv := fakeServerConfig("fake")
	client, err := NewStdioClient(testCtx(t), srv.Name, srv.Command, srv.Args, []string{"GO_WANT_MCP_HELPER=1"}, slog.Default())
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	tools := client.Tools()
	if len(tools) != 2 || tools[0].Name != "echo" || tools[1].Name != "reverse" {
		t.Fatalf("tools = %+v, want echo+reverse", tools)
	}
	got, err := client.CallTool(testCtx(t), "reverse", `{"text":"abc"}`)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got != "cba" {
		t.Fatalf("reverse = %q, want cba", got)
	}
}

func TestProbeListsTools(t *testing.T) {
	tools, err := Probe(testCtx(t), fakeServerConfig("fake"), t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %+v, want 2", tools)
	}
}

func TestProbeBadCommand(t *testing.T) {
	srv := config.MCPServerConfig{Name: "bad", Command: "/nonexistent-mcp-binary"}
	if _, err := Probe(testCtx(t), srv, t.TempDir(), slog.Default()); err == nil {
		t.Fatal("bad command must error")
	}
}

// ---- management operations ----

// writeTestConfig writes a config.yaml with one server and loads it, pinning
// Paths.Home to the temp dir so the global <home>/mcp.json lands there too.
func writeTestConfig(t *testing.T) (*config.Config, string, string) {
	t.Helper()
	home := t.TempDir()
	cfgPath := home + "/config.yaml"
	yaml := `
mcp_servers:
  - name: cfg-srv
    command: cfg-mcp
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Paths.Home = home
	return cfg, cfgPath, home
}

func TestListManagedServersScopesAndOrigins(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
	cwd := t.TempDir()
	globalPath := config.GlobalMCPJSONPath(home)
	projectPath := config.MCPJSONPath(cwd)

	// Global file adds home-srv and overrides cfg-srv; project file adds
	// proj-srv and overrides home-srv.
	for name, srv := range map[string]config.MCPJSONServer{
		"home-srv": {Command: "home-mcp"},
		"cfg-srv":  {Command: "home-override"},
	} {
		if err := config.UpsertMCPJSONServer(globalPath, name, srv); err != nil {
			t.Fatal(err)
		}
	}
	for name, srv := range map[string]config.MCPJSONServer{
		"proj-srv": {Command: "proj-mcp"},
		"home-srv": {Command: "proj-override"},
	} {
		if err := config.UpsertMCPJSONServer(projectPath, name, srv); err != nil {
			t.Fatal(err)
		}
	}

	servers, err := ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatalf("ListManagedServers: %v", err)
	}
	if len(servers) != 3 {
		t.Fatalf("servers = %+v, want 3", servers)
	}
	type so struct{ scope, origin, command string }
	got := map[string]so{}
	for _, s := range servers {
		got[s.Config.Name] = so{s.Scope, s.Origin, s.Config.Command}
	}
	if got["cfg-srv"] != (so{ScopeGlobal, OriginHome, "home-override"}) {
		t.Errorf("cfg-srv = %+v, want global/home with home override", got["cfg-srv"])
	}
	if got["home-srv"] != (so{ScopeLocal, OriginProject, "proj-override"}) {
		t.Errorf("home-srv = %+v, want local/project with project override", got["home-srv"])
	}
	if got["proj-srv"] != (so{ScopeLocal, OriginProject, "proj-mcp"}) {
		t.Errorf("proj-srv = %+v, want local/project", got["proj-srv"])
	}

	// Without any overrides the config.yaml entry stays config-owned/global.
	if _, err := config.DeleteMCPJSONServer(globalPath, "cfg-srv"); err != nil {
		t.Fatal(err)
	}
	servers, _ = ListManagedServers(cfg, cwd)
	for _, s := range servers {
		if s.Config.Name == "cfg-srv" && (s.Scope != ScopeGlobal || s.Origin != OriginConfig) {
			t.Errorf("cfg-srv = %s/%s, want global/config", s.Scope, s.Origin)
		}
	}
}

func TestSetServerDisabledPersistsToOwningFile(t *testing.T) {
	cfg, cfgPath, home := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "home-srv", config.MCPJSONServer{Command: "home-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "proj-srv", config.MCPJSONServer{Command: "proj-mcp"}); err != nil {
		t.Fatal(err)
	}

	// Project-owned toggle lands in <cwd>/.coddy/mcp.json.
	if err := SetServerDisabled(cfg, cwd, "proj-srv", true); err != nil {
		t.Fatalf("disable project server: %v", err)
	}
	entries, _ := config.ReadMCPJSONFile(config.MCPJSONPath(cwd))
	if !entries["proj-srv"].Disabled {
		t.Errorf("proj-srv not disabled in project mcp.json: %+v", entries)
	}

	// Home-owned toggle lands in <home>/mcp.json.
	if err := SetServerDisabled(cfg, cwd, "home-srv", true); err != nil {
		t.Fatalf("disable home server: %v", err)
	}
	entries, _ = config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if !entries["home-srv"].Disabled {
		t.Errorf("home-srv not disabled in global mcp.json: %+v", entries)
	}

	// Config-owned toggle lands in config.yaml.
	if err := SetServerDisabled(cfg, cwd, "cfg-srv", true); err != nil {
		t.Fatalf("disable config server: %v", err)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.MCPServers) != 1 || !reloaded.MCPServers[0].Disabled {
		t.Errorf("config.yaml servers = %+v, want cfg-srv disabled", reloaded.MCPServers)
	}

	if err := SetServerDisabled(cfg, cwd, "ghost", true); err == nil {
		t.Error("unknown server must error")
	}
}

func TestSetToolDisabledPersistsToOwningFile(t *testing.T) {
	cfg, cfgPath, home := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "home-srv", config.MCPJSONServer{Command: "home-mcp"}); err != nil {
		t.Fatal(err)
	}

	if err := SetToolDisabled(cfg, cwd, "home-srv", "echo", true); err != nil {
		t.Fatalf("disable home tool: %v", err)
	}
	entries, _ := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if got := entries["home-srv"].DisabledTools; len(got) != 1 || got[0] != "echo" {
		t.Errorf("home-srv disabledTools = %v, want [echo]", got)
	}

	if err := SetToolDisabled(cfg, cwd, "cfg-srv", "reverse", true); err != nil {
		t.Fatalf("disable config tool: %v", err)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.MCPServers[0].DisabledTools; len(got) != 1 || got[0] != "reverse" {
		t.Errorf("config.yaml disabled_tools = %v, want [reverse]", got)
	}

	// Re-enable removes the entry again.
	if err := SetToolDisabled(cfg, cwd, "cfg-srv", "reverse", false); err != nil {
		t.Fatal(err)
	}
	reloaded, _ = config.Load(cfgPath)
	if got := reloaded.MCPServers[0].DisabledTools; len(got) != 0 {
		t.Errorf("config.yaml disabled_tools = %v, want empty", got)
	}
}

func TestUpsertServerScopes(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
	cwd := t.TempDir()

	if err := UpsertServer(cfg, cwd, "glob", ScopeGlobal, config.MCPJSONServer{Command: "glob-mcp"}); err != nil {
		t.Fatalf("upsert global: %v", err)
	}
	entries, _ := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if entries["glob"].Command != "glob-mcp" {
		t.Errorf("global mcp.json = %+v, want glob", entries)
	}

	if err := UpsertServer(cfg, cwd, "loc", ScopeLocal, config.MCPJSONServer{Command: "loc-mcp"}); err != nil {
		t.Fatalf("upsert local: %v", err)
	}
	entries, _ = config.ReadMCPJSONFile(config.MCPJSONPath(cwd))
	if entries["loc"].Command != "loc-mcp" {
		t.Errorf("project mcp.json = %+v, want loc", entries)
	}

	if err := UpsertServer(cfg, cwd, "x", "nope", config.MCPJSONServer{Command: "x"}); err == nil {
		t.Error("unknown scope must error")
	}
}

func TestDeleteServerPerOrigin(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "home-srv", config.MCPJSONServer{Command: "home-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "proj-srv", config.MCPJSONServer{Command: "proj-mcp"}); err != nil {
		t.Fatal(err)
	}

	if err := DeleteServer(cfg, cwd, "proj-srv"); err != nil {
		t.Fatalf("delete project server: %v", err)
	}
	if err := DeleteServer(cfg, cwd, "home-srv"); err != nil {
		t.Fatalf("delete home server: %v", err)
	}
	entries, _ := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if _, ok := entries["home-srv"]; ok {
		t.Errorf("home-srv still present: %+v", entries)
	}

	if err := DeleteServer(cfg, cwd, "cfg-srv"); err == nil {
		t.Error("config-defined server must refuse API deletion")
	}
	if err := DeleteServer(cfg, cwd, "ghost"); err == nil {
		t.Error("unknown server must error")
	}
}

func TestValidateServerName(t *testing.T) {
	for _, ok := range []string{"files", "my-server", "srv1"} {
		if err := ValidateServerName(ok); err != nil {
			t.Errorf("ValidateServerName(%q) = %v, want nil", ok, err)
		}
	}
	// "__" is the tool-namespace separator; spaces and separators break lookups.
	for _, bad := range []string{"", "a__b", "a b", "a/b", "a\\b", "  "} {
		if err := ValidateServerName(bad); err == nil {
			t.Errorf("ValidateServerName(%q) = nil, want error", bad)
		}
	}
}

// ---- remote transports (streamable HTTP and legacy SSE) ----

// fakeStreamableHandler implements a minimal MCP streamable HTTP server: one
// POST endpoint accepting JSON-RPC, answering initialize/tools/list/tools/call
// with application/json bodies (or an SSE body when sseResults is set),
// issuing an Mcp-Session-Id on initialize and requiring it on every later
// request (notifications included). With auth set, every request must carry
// the X-Auth header. The special tool names rpcfail / toolfail exercise the
// JSON-RPC-error and isError result paths.
type fakeStreamableHandler struct {
	sseResults bool
	auth       string
	calls      atomic.Int64
}

func (h *fakeStreamableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.auth != "" && r.Header.Get("X-Auth") != h.auth {
		http.Error(w, "missing auth header", http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		ID     interface{}     `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "sess-123" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	if req.ID == nil { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	respond := func(result interface{}) {
		msg, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
		if h.sseResults {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(msg)
	}
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess-123")
		respond(map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"serverInfo":      map[string]interface{}{"name": "fake-streamable", "version": "0.0.1"},
		})
	case "tools/list":
		respond(map[string]interface{}{"tools": []map[string]interface{}{{
			"name":        "remote_echo",
			"description": "Echo over streamable http",
			"inputSchema": map[string]interface{}{"type": "object"},
		}}})
	case "tools/call":
		h.calls.Add(1)
		var params struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &params)
		switch params.Name {
		case "rpcfail":
			msg, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]interface{}{"code": -32602, "message": "invalid params"},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(msg)
		case "toolfail":
			respond(map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "tool blew up"}},
				"isError": true,
			})
		default:
			respond(map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "remote:" + params.Arguments["text"]}},
			})
		}
	default:
		respond(nil)
	}
}

func TestStreamableHTTPClient(t *testing.T) {
	// auth makes the fake reject any request missing the configured header,
	// so this also proves headers are sent on every message.
	h := &fakeStreamableHandler{auth: "tok"}
	ts := httptest.NewServer(h)
	defer ts.Close()

	client, err := NewHTTPClient(testCtx(t), "remote", ts.URL, map[string]string{"X-Auth": "tok"}, slog.Default())
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	tools := client.Tools()
	if len(tools) != 1 || tools[0].Name != "remote_echo" {
		t.Fatalf("tools = %+v, want remote_echo", tools)
	}
	got, err := client.CallTool(testCtx(t), "remote_echo", `{"text":"hi"}`)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got != "remote:hi" {
		t.Fatalf("result = %q, want remote:hi", got)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("server saw %d tool calls, want 1", h.calls.Load())
	}

	// JSON-RPC error responses surface as errors, not silent empty results.
	if _, err := client.CallTool(testCtx(t), "rpcfail", `{}`); err == nil || !strings.Contains(err.Error(), "jsonrpc error -32602") {
		t.Fatalf("rpcfail err = %v, want jsonrpc error -32602", err)
	}
	// isError tool results surface as errors too.
	if _, err := client.CallTool(testCtx(t), "toolfail", `{}`); err == nil || !strings.Contains(err.Error(), "tool blew up") {
		t.Fatalf("toolfail err = %v, want mcp tool error", err)
	}
}

func TestStdioClientSurvivesConnectCtxCancel(t *testing.T) {
	// The connect ctx bounds only the handshake: HTTP-created sessions pass a
	// request-scoped ctx here, and the subprocess must outlive it or every
	// turn after the first would hit a dead server.
	connectCtx, cancel := context.WithCancel(context.Background())
	srv := fakeServerConfig("fake")
	client, err := NewStdioClient(connectCtx, srv.Name, srv.Command, srv.Args, []string{"GO_WANT_MCP_HELPER=1"}, slog.Default())
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	cancel()
	time.Sleep(100 * time.Millisecond) // give a ctx-tied process time to die

	got, err := client.CallTool(testCtx(t), "echo", `{"text":"still-alive"}`)
	if err != nil {
		t.Fatalf("CallTool after connect ctx cancel: %v", err)
	}
	if got != "still-alive" {
		t.Fatalf("result = %q, want still-alive", got)
	}
}

func TestStreamableHTTPClientSSEResponses(t *testing.T) {
	ts := httptest.NewServer(&fakeStreamableHandler{sseResults: true})
	defer ts.Close()

	client, err := NewHTTPClient(testCtx(t), "remote", ts.URL, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewHTTPClient (sse bodies): %v", err)
	}
	defer func() { _ = client.Close() }()
	got, err := client.CallTool(testCtx(t), "remote_echo", `{"text":"sse"}`)
	if err != nil || got != "remote:sse" {
		t.Fatalf("CallTool = %q, %v; want remote:sse", got, err)
	}
}

// fakeLegacySSEServer implements the 2024-11-05 HTTP+SSE transport: GET opens
// an event stream that first announces the POST endpoint, then carries every
// JSON-RPC response; POSTs to the endpoint return 202.
func newFakeLegacySSEServer(t *testing.T) *httptest.Server {
	t.Helper()
	type sseSession struct{ out chan []byte }
	sessions := struct {
		sync.Mutex
		m map[string]*sseSession
	}{m: map[string]*sseSession{}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /sse", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("no flusher")
			return
		}
		sess := &sseSession{out: make(chan []byte, 16)}
		sessions.Lock()
		sessions.m["s1"] = sess
		sessions.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: /messages?session=s1\n\n")
		fl.Flush()
		for {
			select {
			case msg := <-sess.out:
				_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
				fl.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	mux.HandleFunc("POST /messages", func(w http.ResponseWriter, r *http.Request) {
		// The endpoint event announced /messages?session=s1: reject POSTs
		// that dropped the query so the client's endpoint resolution is real.
		if r.URL.Query().Get("session") != "s1" {
			http.Error(w, "missing session query", http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     interface{}     `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		w.WriteHeader(http.StatusAccepted)
		if req.ID == nil {
			return
		}
		sessions.Lock()
		sess := sessions.m["s1"]
		sessions.Unlock()
		respond := func(result interface{}) {
			msg, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
			sess.out <- msg
		}
		switch req.Method {
		case "initialize":
			respond(map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "fake-sse", "version": "0.0.1"},
			})
		case "tools/list":
			respond(map[string]interface{}{"tools": []map[string]interface{}{{
				"name":        "sse_echo",
				"description": "Echo over legacy SSE",
				"inputSchema": map[string]interface{}{"type": "object"},
			}}})
		case "tools/call":
			var params struct {
				Arguments map[string]string `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			respond(map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "sse:" + params.Arguments["text"]}},
			})
		default:
			respond(nil)
		}
	})
	return httptest.NewServer(mux)
}

func TestLegacySSEClient(t *testing.T) {
	ts := newFakeLegacySSEServer(t)
	defer ts.Close()

	client, err := NewSSEClient(testCtx(t), "legacy", ts.URL+"/sse", nil, slog.Default())
	if err != nil {
		t.Fatalf("NewSSEClient: %v", err)
	}
	defer func() { _ = client.Close() }()
	tools := client.Tools()
	if len(tools) != 1 || tools[0].Name != "sse_echo" {
		t.Fatalf("tools = %+v, want sse_echo", tools)
	}
	got, err := client.CallTool(testCtx(t), "sse_echo", `{"text":"x"}`)
	if err != nil || got != "sse:x" {
		t.Fatalf("CallTool = %q, %v; want sse:x", got, err)
	}
}

func TestHTTPClientFallsBackToSSE(t *testing.T) {
	// A server that only speaks the legacy protocol: POST to the SSE URL is
	// rejected, so the streamable attempt fails and the client retries as SSE.
	ts := newFakeLegacySSEServer(t)
	defer ts.Close()

	client, err := NewHTTPClient(testCtx(t), "legacy", ts.URL+"/sse", nil, slog.Default())
	if err != nil {
		t.Fatalf("NewHTTPClient with legacy server: %v", err)
	}
	defer func() { _ = client.Close() }()
	if tools := client.Tools(); len(tools) != 1 || tools[0].Name != "sse_echo" {
		t.Fatalf("tools = %+v, want sse_echo via fallback", tools)
	}
}

func TestConnectDispatchesByType(t *testing.T) {
	streamable := httptest.NewServer(&fakeStreamableHandler{auth: "tok"})
	defer streamable.Close()

	// stdio (default type) still works through Connect.
	stdioClient, err := Connect(testCtx(t), fakeServerConfig("fake"), t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("Connect stdio: %v", err)
	}
	defer func() { _ = stdioClient.Close() }()
	if len(stdioClient.Tools()) != 2 {
		t.Fatalf("stdio tools = %+v", stdioClient.Tools())
	}

	httpClient, err := Connect(testCtx(t), config.MCPServerConfig{
		Name: "remote", Type: "http", URL: streamable.URL,
		Headers: []config.HTTPHeaderConfig{{Name: "X-Auth", Value: "tok"}},
	}, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("Connect http: %v", err)
	}
	defer func() { _ = httpClient.Close() }()
	if len(httpClient.Tools()) != 1 {
		t.Fatalf("http tools = %+v", httpClient.Tools())
	}

	// The streamable-http alias reaches the same transport.
	aliasClient, err := Connect(testCtx(t), config.MCPServerConfig{
		Name: "alias", Type: "streamable-http", URL: streamable.URL,
		Headers: []config.HTTPHeaderConfig{{Name: "X-Auth", Value: "tok"}},
	}, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("Connect streamable-http alias: %v", err)
	}
	defer func() { _ = aliasClient.Close() }()

	// Explicit type sse forces the legacy transport (no fallback involved).
	legacy := newFakeLegacySSEServer(t)
	defer legacy.Close()
	sseClient, err := Connect(testCtx(t), config.MCPServerConfig{
		Name: "legacy", Type: "sse", URL: legacy.URL + "/sse",
	}, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("Connect sse: %v", err)
	}
	defer func() { _ = sseClient.Close() }()
	if tools := sseClient.Tools(); len(tools) != 1 || tools[0].Name != "sse_echo" {
		t.Fatalf("sse tools = %+v", tools)
	}

	if _, err := Connect(testCtx(t), config.MCPServerConfig{Name: "x", Type: "websocket"}, t.TempDir(), slog.Default()); err == nil {
		t.Fatal("unknown transport must error")
	}
}

func TestSSEConnectHonorsCtxOnSilentServer(t *testing.T) {
	// A server that accepts the connection but never sends response headers
	// must not hang Connect/probe past the caller's deadline.
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer silent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := NewSSEClient(ctx, "silent", silent.URL, nil, slog.Default()); err == nil {
		t.Fatal("silent server must error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("connect blocked %v past the 300ms deadline", elapsed)
	}
}

func TestProbeRemoteTransport(t *testing.T) {
	ts := httptest.NewServer(&fakeStreamableHandler{})
	defer ts.Close()
	tools, err := Probe(testCtx(t), config.MCPServerConfig{Name: "remote", Type: "http", URL: ts.URL}, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("Probe http: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "remote_echo" {
		t.Fatalf("tools = %+v", tools)
	}
}
