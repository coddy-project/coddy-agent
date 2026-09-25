package mcp

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

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
	rows, err := ListStatus(context.Background(), cfg, cwd, slog.Default())
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
