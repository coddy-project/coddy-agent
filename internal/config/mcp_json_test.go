package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMCPServerConfigDisabledYAML(t *testing.T) {
	src := `
mcp_servers:
  - name: files
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem"]
    disabled: true
    disabled_tools: ["write_file", "move_file"]
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("servers = %d, want 1", len(cfg.MCPServers))
	}
	srv := cfg.MCPServers[0]
	if !srv.Disabled {
		t.Errorf("Disabled = false, want true")
	}
	if len(srv.DisabledTools) != 2 || srv.DisabledTools[0] != "write_file" {
		t.Errorf("DisabledTools = %v, want [write_file move_file]", srv.DisabledTools)
	}
}

func writeProjectMCPFile(t *testing.T, cwd, content string) {
	t.Helper()
	dir := filepath.Join(cwd, ".coddy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProjectMCPServersCursorFormat(t *testing.T) {
	cwd := t.TempDir()
	writeProjectMCPFile(t, cwd, `{
  "mcpServers": {
    "files": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${CWD}"],
      "env": {"B_TOKEN": "b", "A_TOKEN": "a"},
      "disabledTools": ["write_file"]
    },
    "off": {
      "command": "some-mcp",
      "disabled": true
    },
    "remote": {
      "url": "https://example.com/sse"
    }
  }
}`)

	servers, err := LoadProjectMCPServers(cwd)
	if err != nil {
		t.Fatalf("LoadProjectMCPServers: %v", err)
	}
	if len(servers) != 3 {
		t.Fatalf("servers = %d, want 3: %+v", len(servers), servers)
	}
	byName := map[string]MCPServerConfig{}
	for _, s := range servers {
		byName[s.Name] = s
	}

	files := byName["files"]
	if files.Command != "npx" || len(files.Args) != 3 {
		t.Errorf("files = %+v, want npx with 3 args", files)
	}
	// Env maps are converted to name-sorted slices for determinism.
	if len(files.Env) != 2 || files.Env[0].Name != "A_TOKEN" || files.Env[1].Name != "B_TOKEN" {
		t.Errorf("files.Env = %+v, want sorted [A_TOKEN B_TOKEN]", files.Env)
	}
	if len(files.DisabledTools) != 1 || files.DisabledTools[0] != "write_file" {
		t.Errorf("files.DisabledTools = %v, want [write_file]", files.DisabledTools)
	}
	if files.Disabled {
		t.Errorf("files.Disabled = true, want false")
	}

	if !byName["off"].Disabled {
		t.Errorf("off.Disabled = false, want true")
	}

	// URL-only entries surface as http transport so callers can reject them gracefully.
	remote := byName["remote"]
	if remote.Type != "http" || remote.URL != "https://example.com/sse" {
		t.Errorf("remote = %+v, want inferred http type with url", remote)
	}
}

func TestLoadProjectMCPServersMissing(t *testing.T) {
	servers, err := LoadProjectMCPServers(t.TempDir())
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("servers = %v, want empty", servers)
	}
}

func TestLoadProjectMCPServersInvalidJSON(t *testing.T) {
	cwd := t.TempDir()
	writeProjectMCPFile(t, cwd, `{"mcpServers": {`)
	if _, err := LoadProjectMCPServers(cwd); err == nil {
		t.Fatal("invalid JSON must return an error")
	}
}

func TestMergeMCPServers(t *testing.T) {
	global := []MCPServerConfig{
		{Name: "a", Command: "global-a"},
		{Name: "b", Command: "global-b"},
	}
	project := []MCPServerConfig{
		{Name: "b", Command: "project-b", Disabled: true},
		{Name: "c", Command: "project-c"},
	}
	merged := MergeMCPServers(global, project)
	if len(merged) != 3 {
		t.Fatalf("merged = %d entries, want 3: %+v", len(merged), merged)
	}
	// Global order is preserved; project overrides by name; new entries append.
	if merged[0].Name != "a" || merged[0].Command != "global-a" {
		t.Errorf("merged[0] = %+v, want global a", merged[0])
	}
	if merged[1].Name != "b" || merged[1].Command != "project-b" || !merged[1].Disabled {
		t.Errorf("merged[1] = %+v, want project override of b", merged[1])
	}
	if merged[2].Name != "c" {
		t.Errorf("merged[2] = %+v, want project c", merged[2])
	}
}

func TestUpsertAndDeleteProjectMCPServer(t *testing.T) {
	cwd := t.TempDir()

	// Upsert into a missing file creates .coddy/mcp.json.
	if err := UpsertProjectMCPServer(cwd, "demo", ProjectMCPServer{Command: "demo-mcp"}); err != nil {
		t.Fatalf("upsert new: %v", err)
	}
	entries, err := ReadProjectMCPFile(cwd)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if entries["demo"].Command != "demo-mcp" {
		t.Fatalf("entries = %+v, want demo", entries)
	}

	// Update preserves sibling entries.
	if err := UpsertProjectMCPServer(cwd, "other", ProjectMCPServer{Command: "other-mcp"}); err != nil {
		t.Fatalf("upsert other: %v", err)
	}
	if err := UpsertProjectMCPServer(cwd, "demo", ProjectMCPServer{Command: "demo-mcp", Args: []string{"--x"}}); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	entries, _ = ReadProjectMCPFile(cwd)
	if len(entries) != 2 || len(entries["demo"].Args) != 1 {
		t.Fatalf("entries after update = %+v", entries)
	}

	removed, err := DeleteProjectMCPServer(cwd, "demo")
	if err != nil || !removed {
		t.Fatalf("delete: removed=%v err=%v", removed, err)
	}
	removed, err = DeleteProjectMCPServer(cwd, "demo")
	if err != nil || removed {
		t.Fatalf("second delete: removed=%v err=%v", removed, err)
	}
	entries, _ = ReadProjectMCPFile(cwd)
	if len(entries) != 1 {
		t.Fatalf("entries after delete = %+v, want only other", entries)
	}
}

func TestSetProjectMCPServerDisabled(t *testing.T) {
	cwd := t.TempDir()
	if err := UpsertProjectMCPServer(cwd, "demo", ProjectMCPServer{Command: "demo-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := SetProjectMCPServerDisabled(cwd, "demo", true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	entries, _ := ReadProjectMCPFile(cwd)
	if !entries["demo"].Disabled {
		t.Fatalf("demo not disabled: %+v", entries)
	}
	if err := SetProjectMCPServerDisabled(cwd, "demo", false); err != nil {
		t.Fatalf("enable: %v", err)
	}
	entries, _ = ReadProjectMCPFile(cwd)
	if entries["demo"].Disabled {
		t.Fatalf("demo still disabled: %+v", entries)
	}
	if err := SetProjectMCPServerDisabled(cwd, "ghost", true); err == nil {
		t.Fatal("unknown server must error")
	}
}

func TestBuildMCPToolFilter(t *testing.T) {
	servers := []MCPServerConfig{
		{Name: "off", Disabled: true},
		{Name: "partial", DisabledTools: []string{"write_file"}},
	}
	allowed := BuildMCPToolFilter(servers)
	if allowed("off", "anything") {
		t.Error("disabled server must hide all tools")
	}
	if allowed("partial", "write_file") {
		t.Error("disabled tool must be hidden")
	}
	if !allowed("partial", "read_file") {
		t.Error("other tools of the server stay visible")
	}
	// Servers not in the config (e.g. ACP client-supplied) stay fully allowed.
	if !allowed("unknown", "tool") {
		t.Error("unknown server must stay allowed")
	}
}

func TestSetProjectMCPToolDisabled(t *testing.T) {
	cwd := t.TempDir()
	if err := UpsertProjectMCPServer(cwd, "demo", ProjectMCPServer{Command: "demo-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := SetProjectMCPToolDisabled(cwd, "demo", "echo", true); err != nil {
		t.Fatalf("disable tool: %v", err)
	}
	// Disabling twice stays idempotent.
	if err := SetProjectMCPToolDisabled(cwd, "demo", "echo", true); err != nil {
		t.Fatalf("disable tool again: %v", err)
	}
	entries, _ := ReadProjectMCPFile(cwd)
	if got := entries["demo"].DisabledTools; len(got) != 1 || got[0] != "echo" {
		t.Fatalf("DisabledTools = %v, want [echo]", got)
	}
	if err := SetProjectMCPToolDisabled(cwd, "demo", "echo", false); err != nil {
		t.Fatalf("enable tool: %v", err)
	}
	entries, _ = ReadProjectMCPFile(cwd)
	if got := entries["demo"].DisabledTools; len(got) != 0 {
		t.Fatalf("DisabledTools = %v, want empty", got)
	}
}
