//go:build http

package httpserver

// Tests of the npx pin on PUT /coddy/mcp/{name} (mcp_mgmt.go): the entry
// written with the registry's current release and the pin in the answer, an
// unresolved package saved as sent with pinned false, and no pin at all for
// a server that is not an npx package.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// pinRegistry stands in for the npm registry through npm_config_registry:
// the versions given, 404 for the rest.
func pinRegistry(t *testing.T, versions map[string]string) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/latest")
		v, ok := versions[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"` + v + `"}`))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("npm_config_registry", ts.URL)
}

// newPinTestServer runs the HTTP surface over a manager with no sessions,
// which is all a PUT of an mcp.json entry needs.
func newPinTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODDY_HOME", home)
	configPath := filepath.Join(home, "config.yaml")
	initial := "providers:\n  - name: openai\n    type: openai\n    api_key: k\nmodels:\n  - model: openai/gpt-4o\n    max_tokens: 4096\nagent:\n  model: openai/gpt-4o\n"
	if err := os.WriteFile(configPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home, Config: configPath})
	if err != nil {
		t.Fatal(err)
	}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, home
}

func putMCPServer(t *testing.T, ts *httptest.Server, name string, entry config.MCPJSONServer) (int, map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/coddy/mcp/"+name+"?scope=global", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out := map[string]interface{}{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestMCPServerPutPinsAnNPXPackage(t *testing.T) {
	pinRegistry(t, map[string]string{"@upstash/context7-mcp": "1.0.14"})
	ts, home := newPinTestServer(t)
	status, out := putMCPServer(t, ts, "context7", config.MCPJSONServer{Command: "npx", Args: []string{"-y", "@upstash/context7-mcp"}})
	if status != http.StatusOK {
		t.Fatalf("status = %d body %v", status, out)
	}
	pin, _ := out["pin"].(map[string]interface{})
	if pin == nil || pin["pinned"] != true || pin["version"] != "1.0.14" || pin["package"] != "@upstash/context7-mcp" {
		t.Fatalf("pin = %v, want pinned to 1.0.14", out["pin"])
	}
	if msg, _ := pin["message"].(string); !strings.Contains(msg, "first frame") {
		t.Fatalf("message = %q, want the reason", msg)
	}
	entries, err := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if got := entries["context7"].Args; len(got) != 2 || got[1] != "@upstash/context7-mcp@1.0.14" {
		t.Fatalf("saved args = %v, want the pinned spec", got)
	}
}

func TestMCPServerPutSavesUnpinnedWhenTheRegistryHasNoAnswer(t *testing.T) {
	pinRegistry(t, nil)
	ts, home := newPinTestServer(t)
	status, out := putMCPServer(t, ts, "docker", config.MCPJSONServer{Command: "npx", Args: []string{"-y", "mcp-server-docker"}})
	if status != http.StatusOK {
		t.Fatalf("status = %d body %v: a resolution failure must not fail the save", status, out)
	}
	pin, _ := out["pin"].(map[string]interface{})
	if pin == nil || pin["pinned"] != false {
		t.Fatalf("pin = %v, want an unpinned report", out["pin"])
	}
	entries, err := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if got := entries["docker"].Args; len(got) != 2 || got[1] != "mcp-server-docker" {
		t.Fatalf("saved args = %v, want the entry as sent", got)
	}
}

func TestMCPServerPutHasNoPinForOtherCommands(t *testing.T) {
	pinRegistry(t, map[string]string{"server.js": "1.0.0"})
	ts, _ := newPinTestServer(t)
	status, out := putMCPServer(t, ts, "local", config.MCPJSONServer{Command: "node", Args: []string{"server.js"}})
	if status != http.StatusOK {
		t.Fatalf("status = %d body %v", status, out)
	}
	if _, present := out["pin"]; present {
		t.Fatalf("response = %v, want no pin for a server that is not an npx package", out)
	}
}
