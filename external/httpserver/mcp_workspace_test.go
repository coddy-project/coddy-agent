//go:build http

package httpserver

import (
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
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type mcpWorkspaceList struct {
	Workspace string `json:"workspace"`
	Items     []struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		Enabled bool   `json:"enabled"`
	} `json:"items"`
}

func TestMCPManagementUsesSessionWorkspaceAndRejectsInvalidHeaders(t *testing.T) {
	home, projectA, projectB := t.TempDir(), t.TempDir(), t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(configPath, []byte("mcp:\n  project_trust: ask\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: projectA, ConfigPath: configPath},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 200}},
		Agent:     config.Agent{Model: "fake/model"},
		MCP:       config.MCP{ProjectTrust: config.ProjectTrustAsk},
	}
	for cwd, command := range map[string]string{projectA: "from-a", projectB: "from-b"} {
		if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "local", config.MCPJSONServer{Command: command}); err != nil {
			t.Fatal(err)
		}
	}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), projectA, nil)
	newSession := func(cwd string) string {
		t.Helper()
		created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
		if err != nil {
			t.Fatal(err)
		}
		st := mgr.SessionByID(created.SessionID)
		t.Cleanup(st.CloseAll)
		return created.SessionID
	}
	sidA, sidB := newSession(projectA), newSession(projectB)
	ts := httptest.NewServer(New(cfg, mgr, slog.Default(), projectA).Handler())
	defer ts.Close()

	do := func(method, path, sessionID string) (int, mcpWorkspaceList) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if sessionID != "" {
			req.Header.Set("X-Coddy-Session-ID", sessionID)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var body mcpWorkspaceList
		if res.StatusCode == http.StatusOK && method == http.MethodGet {
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
		} else {
			_, _ = io.ReadAll(res.Body)
		}
		return res.StatusCode, body
	}
	server := func(body mcpWorkspaceList) struct {
		Name    string
		Command string
		Enabled bool
	} {
		t.Helper()
		if len(body.Items) != 1 || body.Items[0].Name != "local" {
			t.Fatalf("items = %+v, want local", body.Items)
		}
		return struct {
			Name    string
			Command string
			Enabled bool
		}{body.Items[0].Name, body.Items[0].Command, body.Items[0].Enabled}
	}

	if status, body := do(http.MethodGet, "/coddy/mcp", ""); status != http.StatusOK || server(body).Command != "from-a" {
		t.Fatalf("headerless list = %d %+v, want project A", status, body)
	}
	if status, body := do(http.MethodGet, "/coddy/mcp", sidB); status != http.StatusOK || server(body).Command != "from-b" {
		t.Fatalf("session B list = %d %+v, want project B", status, body)
	}
	workspaceRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/coddy/mcp"},
		{http.MethodPost, "/coddy/mcp/local/enable"},
		{http.MethodPost, "/coddy/mcp/local/disable"},
		{http.MethodPost, "/coddy/mcp/local/trust"},
		{http.MethodPost, "/coddy/mcp/local/untrust"},
		{http.MethodPost, "/coddy/mcp/local/tools/echo/enable"},
		{http.MethodPost, "/coddy/mcp/local/tools/echo/disable"},
		{http.MethodPut, "/coddy/mcp/new-local"},
		{http.MethodDelete, "/coddy/mcp/local"},
	}
	for _, header := range []string{"bad id", "sess_unknown"} {
		want := http.StatusBadRequest
		if header == "sess_unknown" {
			want = http.StatusNotFound
		}
		for _, route := range workspaceRoutes {
			if status, _ := do(route.method, route.path, header); status != want {
				t.Errorf("%s %s with %q = %d, want %d", route.method, route.path, header, status, want)
			}
		}
	}
	if status, _ := do(http.MethodPost, "/coddy/mcp/local/disable", sidB); status != http.StatusOK {
		t.Fatalf("disable in session B = %d, want 200", status)
	}
	if status, body := do(http.MethodGet, "/coddy/mcp", sidA); status != http.StatusOK || !server(body).Enabled {
		t.Fatalf("session A after B mutation = %d %+v, want enabled", status, body)
	}
	if status, body := do(http.MethodGet, "/coddy/mcp", sidB); status != http.StatusOK || server(body).Enabled {
		t.Fatalf("session B after mutation = %d %+v, want disabled", status, body)
	}

	// Project trust is global policy, so it intentionally ignores an invalid
	// session header rather than treating it as a workspace-scoped request.
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/coddy/mcp/project-trust", strings.NewReader(`{"policy":"deny"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", "bad id")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("global project trust with invalid header = %d, want 200", res.StatusCode)
	}
}

func TestSessionMessagesActivateMCPOnlyOnInitialSessionRead(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 200}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	store := &session.FileStore{Root: filepath.Join(home, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), cwd, store)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	sid := created.SessionID
	mgr.ForgetLiveSession(sid)
	ts := httptest.NewServer(New(cfg, mgr, slog.Default(), cwd).Handler())
	defer ts.Close()

	get := func(path, header string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if header != "" {
			req.Header.Set("X-Coddy-Session-ID", header)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		_, _ = io.ReadAll(res.Body)
		return res.StatusCode
	}

	if status := get("/coddy/sessions/"+sid+"/messages", sid); status != http.StatusOK {
		t.Fatalf("passive messages read = %d, want 200", status)
	}
	st := mgr.SessionByID(sid)
	if _, recorded := st.MCPConnectSnapshot(); recorded {
		t.Fatal("passive messages read started deferred MCP servers")
	}
	if status := get("/coddy/sessions/"+sid+"/messages?activate_mcp=1&limit=1&before=1", sid); status != http.StatusBadRequest {
		t.Fatalf("paged activation = %d, want 400", status)
	}
	if status := get("/coddy/sessions/"+sid+"/messages?activate_mcp=1", ""); status != http.StatusBadRequest {
		t.Fatalf("headerless activation = %d, want 400", status)
	}
	if status := get("/coddy/sessions/"+sid+"/messages?activate_mcp=1", sid); status != http.StatusOK {
		t.Fatalf("initial activation = %d, want 200", status)
	}
	if _, recorded := st.MCPConnectSnapshot(); !recorded {
		t.Fatal("initial activation did not register its background MCP generation")
	}
}

func TestMCPProbeInvalidationScopesProjectWorkspaces(t *testing.T) {
	workspaceA, workspaceB := t.TempDir(), t.TempDir()
	canonicalA := session.CanonicalWorkspacePath(workspaceA)
	canonicalB := session.CanonicalWorkspacePath(workspaceB)
	server := &Server{mcpProbeCache: map[mcpProbeKey]mcpProbeEntry{
		{origin: mcp.OriginProject, poolKey: "project-a", workspace: canonicalA}: {origin: mcp.OriginProject, name: "same", workspace: canonicalA},
		{origin: mcp.OriginProject, poolKey: "project-b", workspace: canonicalB}: {origin: mcp.OriginProject, name: "same", workspace: canonicalB},
		{origin: mcp.OriginHome, poolKey: "home-a", workspace: canonicalA}:       {origin: mcp.OriginHome, name: "same", workspace: canonicalA},
		{origin: mcp.OriginHome, poolKey: "home-b", workspace: canonicalB}:       {origin: mcp.OriginHome, name: "same", workspace: canonicalB},
	}}

	server.invalidateMCPProbe(mcp.OriginProject, "same", workspaceA)
	if len(server.mcpProbeCache) != 3 {
		t.Fatalf("project invalidation left %d entries, want 3", len(server.mcpProbeCache))
	}
	for _, entry := range server.mcpProbeCache {
		if entry.origin == mcp.OriginProject && entry.workspace == canonicalA {
			t.Fatal("project invalidation retained workspace A's entry")
		}
	}

	server.invalidateMCPProbe(mcp.OriginHome, "same", workspaceA)
	if len(server.mcpProbeCache) != 1 {
		t.Fatalf("global invalidation left %d entries, want project B only", len(server.mcpProbeCache))
	}
	for _, entry := range server.mcpProbeCache {
		if entry.origin != mcp.OriginProject || entry.workspace != canonicalB {
			t.Fatalf("remaining entry = %+v, want project B", entry)
		}
	}
}
