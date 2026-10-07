package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMCPServerConfigDisabledYAML(t *testing.T) {
	// The YAML shape of a declaration is what an old config.yaml mcp_servers
	// list is read as when it moves into mcp.json (legacy_keys.go).
	src := `
- name: files
  command: npx
  args: ["-y", "@modelcontextprotocol/server-filesystem"]
  disabled: true
  disabled_tools: ["write_file", "move_file"]
`
	var servers []MCPServerConfig
	if err := yaml.Unmarshal([]byte(src), &servers); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %d, want 1", len(servers))
	}
	srv := servers[0]
	if !srv.Disabled {
		t.Errorf("Disabled = false, want true")
	}
	if len(srv.DisabledTools) != 2 || srv.DisabledTools[0] != "write_file" {
		t.Errorf("DisabledTools = %v, want [write_file move_file]", srv.DisabledTools)
	}
}

func TestMCPJSONPaths(t *testing.T) {
	if got := MCPJSONPath("/proj"); got != filepath.Join("/proj", ".coddy", "mcp.json") {
		t.Errorf("MCPJSONPath = %q", got)
	}
	// home is already the ~/.coddy state dir, so the global file sits directly in it.
	if got := GlobalMCPJSONPath("/home/u/.coddy"); got != filepath.Join("/home/u/.coddy", "mcp.json") {
		t.Errorf("GlobalMCPJSONPath = %q", got)
	}
}

func writeMCPJSONAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMCPJSONServersCursorFormat(t *testing.T) {
	path := MCPJSONPath(t.TempDir())
	writeMCPJSONAt(t, path, `{
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

	servers, err := LoadMCPJSONServers(path)
	if err != nil {
		t.Fatalf("LoadMCPJSONServers: %v", err)
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

func TestLoadMCPJSONServersMissing(t *testing.T) {
	servers, err := LoadMCPJSONServers(filepath.Join(t.TempDir(), "mcp.json"))
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("servers = %v, want empty", servers)
	}
}

func TestLoadMCPJSONServersInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeMCPJSONAt(t, path, `{"mcpServers": {`)
	if _, err := LoadMCPJSONServers(path); err == nil {
		t.Fatal("invalid JSON must return an error")
	}
}

func TestMergeMCPServers(t *testing.T) {
	base := []MCPServerConfig{
		{Name: "a", Command: "base-a"},
		{Name: "b", Command: "base-b"},
	}
	overlay := []MCPServerConfig{
		{Name: "b", Command: "overlay-b", Disabled: true},
		{Name: "c", Command: "overlay-c"},
	}
	merged := MergeMCPServers(base, overlay)
	if len(merged) != 3 {
		t.Fatalf("merged = %d entries, want 3: %+v", len(merged), merged)
	}
	// Base order is preserved; overlay overrides by name; new entries append.
	if merged[0].Name != "a" || merged[0].Command != "base-a" {
		t.Errorf("merged[0] = %+v, want base a", merged[0])
	}
	if merged[1].Name != "b" || merged[1].Command != "overlay-b" || !merged[1].Disabled {
		t.Errorf("merged[1] = %+v, want overlay override of b", merged[1])
	}
	if merged[2].Name != "c" {
		t.Errorf("merged[2] = %+v, want overlay c", merged[2])
	}

	// Chained precedence: config < global json < project json.
	global := []MCPServerConfig{{Name: "a", Command: "global-a"}}
	project := []MCPServerConfig{{Name: "a", Command: "project-a"}}
	chained := MergeMCPServers(MergeMCPServers(base, global), project)
	if chained[0].Command != "project-a" {
		t.Errorf("chained[0] = %+v, want project override", chained[0])
	}
}

func TestUpsertAndDeleteMCPJSONServer(t *testing.T) {
	path := MCPJSONPath(t.TempDir())

	// Upsert into a missing file creates it (including the .coddy dir).
	if err := UpsertMCPJSONServer(path, "demo", MCPJSONServer{Command: "demo-mcp"}); err != nil {
		t.Fatalf("upsert new: %v", err)
	}
	entries, err := ReadMCPJSONFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if entries["demo"].Command != "demo-mcp" {
		t.Fatalf("entries = %+v, want demo", entries)
	}

	// Update preserves sibling entries.
	if err := UpsertMCPJSONServer(path, "other", MCPJSONServer{Command: "other-mcp"}); err != nil {
		t.Fatalf("upsert other: %v", err)
	}
	if err := UpsertMCPJSONServer(path, "demo", MCPJSONServer{Command: "demo-mcp", Args: []string{"--x"}}); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	entries, _ = ReadMCPJSONFile(path)
	if len(entries) != 2 || len(entries["demo"].Args) != 1 {
		t.Fatalf("entries after update = %+v", entries)
	}

	removed, err := DeleteMCPJSONServer(path, "demo")
	if err != nil || !removed {
		t.Fatalf("delete: removed=%v err=%v", removed, err)
	}
	removed, err = DeleteMCPJSONServer(path, "demo")
	if err != nil || removed {
		t.Fatalf("second delete: removed=%v err=%v", removed, err)
	}
	entries, _ = ReadMCPJSONFile(path)
	if len(entries) != 1 {
		t.Fatalf("entries after delete = %+v, want only other", entries)
	}
}

// UpdateMCPJSONServer hands the change the entry the file stores, writes what
// it returns, and leaves the file as it was when the change refuses.
func TestUpdateMCPJSONServer(t *testing.T) {
	path := MCPJSONPath(t.TempDir())
	if err := UpsertMCPJSONServer(path, "demo", MCPJSONServer{Command: "demo-mcp", Env: map[string]string{"KEY": "kept"}}); err != nil {
		t.Fatal(err)
	}
	written, err := UpdateMCPJSONServer(path, "demo", func(stored MCPJSONServer, exists bool) (MCPJSONServer, error) {
		if !exists || stored.Env["KEY"] != "kept" {
			t.Fatalf("stored = %+v, %v", stored, exists)
		}
		stored.Args = []string{"--x"}
		return stored, nil
	})
	if err != nil || len(written.Args) != 1 {
		t.Fatalf("written = %+v, %v", written, err)
	}
	before, _ := os.ReadFile(path)
	refused := errors.New("refused")
	if _, err := UpdateMCPJSONServer(path, "fresh", func(_ MCPJSONServer, exists bool) (MCPJSONServer, error) {
		if exists {
			t.Fatal("a name the file lacks reads as stored")
		}
		return MCPJSONServer{}, refused
	}); !errors.Is(err, refused) {
		t.Fatalf("refusal = %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("a refused change rewrote the file:\n%s", after)
	}
}

func TestSetMCPJSONServerDisabled(t *testing.T) {
	// Exercise against the global-file path shape to cover both layouts.
	path := GlobalMCPJSONPath(t.TempDir())
	if err := UpsertMCPJSONServer(path, "demo", MCPJSONServer{Command: "demo-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPJSONServerDisabled(path, "demo", true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	entries, _ := ReadMCPJSONFile(path)
	if !entries["demo"].Disabled {
		t.Fatalf("demo not disabled: %+v", entries)
	}
	if err := SetMCPJSONServerDisabled(path, "demo", false); err != nil {
		t.Fatalf("enable: %v", err)
	}
	entries, _ = ReadMCPJSONFile(path)
	if entries["demo"].Disabled {
		t.Fatalf("demo still disabled: %+v", entries)
	}
	if err := SetMCPJSONServerDisabled(path, "ghost", true); err == nil {
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

func TestSetMCPJSONToolDisabled(t *testing.T) {
	path := MCPJSONPath(t.TempDir())
	if err := UpsertMCPJSONServer(path, "demo", MCPJSONServer{Command: "demo-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPJSONToolDisabled(path, "demo", "echo", true); err != nil {
		t.Fatalf("disable tool: %v", err)
	}
	// Disabling twice stays idempotent.
	if err := SetMCPJSONToolDisabled(path, "demo", "echo", true); err != nil {
		t.Fatalf("disable tool again: %v", err)
	}
	entries, _ := ReadMCPJSONFile(path)
	if got := entries["demo"].DisabledTools; len(got) != 1 || got[0] != "echo" {
		t.Fatalf("DisabledTools = %v, want [echo]", got)
	}
	if err := SetMCPJSONToolDisabled(path, "demo", "echo", false); err != nil {
		t.Fatalf("enable tool: %v", err)
	}
	entries, _ = ReadMCPJSONFile(path)
	if got := entries["demo"].DisabledTools; len(got) != 0 {
		t.Fatalf("DisabledTools = %v, want empty", got)
	}
}

// The YAML mcp_servers key is gone: a config that still has it moves its
// servers into <home>/mcp.json on load, as written (${VAR} references
// included), leaves a server the file already declares alone, and loses the
// key; the rest of the file stays byte for byte, and a backup of the old file
// is kept beside it.
func TestLegacyMCPServersMoveIntoHomeMCPJSON(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := UpsertMCPJSONServer(GlobalMCPJSONPath(home), "kept", MCPJSONServer{Command: "already-there"}); err != nil {
		t.Fatal(err)
	}
	head := "# yaml-language-server: $schema=https://coddy.dev/config.schema.json\nproviders:\n  - name: local\n    type: openai\n    api_key: test-key\nmodels:\n  - model: local/m\nagent:\n  model: local/m\n\n"
	legacy := "# MCP servers the agent connects to\nmcp_servers:\n  - name: github\n    command: npx\n    args: [\"-y\", \"@modelcontextprotocol/server-github\"]\n    env:\n      - name: GITHUB_TOKEN\n        value: ${GITHUB_TOKEN}\n  - name: docs\n    url: https://example.test/mcp\n    headers:\n      - name: Authorization\n        value: Bearer ${DOCS_TOKEN}\n    disabled_tools: [search]\n  - name: kept\n    command: from-yaml\n"
	tail := "\n# Rules\nrules:\n  enable: true\n"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(head+legacy+tail), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "secret-should-not-be-written")

	if _, err := LoadWithPaths(Paths{Home: home, CWD: dir, ConfigPath: path}); err != nil {
		t.Fatal(err)
	}

	got, err := ReadMCPJSONFile(GlobalMCPJSONPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if gh := got["github"]; gh.Command != "npx" || !reflect.DeepEqual(gh.Args, []string{"-y", "@modelcontextprotocol/server-github"}) || gh.Env["GITHUB_TOKEN"] != "${GITHUB_TOKEN}" {
		t.Fatalf("github moved as %+v", gh)
	}
	if d := got["docs"]; d.URL != "https://example.test/mcp" || d.Headers["Authorization"] != "Bearer ${DOCS_TOKEN}" || !reflect.DeepEqual(d.DisabledTools, []string{"search"}) {
		t.Fatalf("docs moved as %+v", d)
	}
	if k := got["kept"]; k.Command != "already-there" {
		t.Fatalf("a server the file declares must stay as it is, got %+v", k)
	}
	raw, err := os.ReadFile(GlobalMCPJSONPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-should-not-be-written") {
		t.Fatal("an environment value was written into mcp.json")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != head+strings.TrimPrefix(tail, "\n") {
		t.Fatalf("config.yaml after the move:\n%s\nwant the file without the mcp_servers block, one blank line between the sections around it", after)
	}
	backups, _ := filepath.Glob(path + ".bak-*")
	if len(backups) != 1 {
		t.Fatalf("want one backup of the old config, got %v", backups)
	}
	if b, _ := os.ReadFile(backups[0]); string(b) != head+legacy+tail {
		t.Fatal("the backup must hold the old file as it was")
	}

	// A second load finds nothing to move and writes nothing.
	if _, err := LoadWithPaths(Paths{Home: home, CWD: dir, ConfigPath: path}); err != nil {
		t.Fatal(err)
	}
	if again, _ := filepath.Glob(path + ".bak-*"); len(again) != 1 {
		t.Fatalf("a load with nothing to move made another backup: %v", again)
	}
}

// An empty mcp_servers (the old example config had one) is just dropped.
func TestLegacyEmptyMCPServersKeyIsDropped(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	body := "agent:\n  model: local/m\nmcp_servers: []\nrules:\n  enable: true\n"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithPaths(Paths{Home: home, CWD: dir, ConfigPath: path}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != "agent:\n  model: local/m\nrules:\n  enable: true\n" {
		t.Fatalf("config.yaml after dropping an empty key:\n%s", after)
	}

	// The key last in the file leaves no blank line behind it.
	last := "agent:\n  model: local/m\n\nmcp_servers: []\n"
	if err := os.WriteFile(path, []byte(last), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithPaths(Paths{Home: home, CWD: dir, ConfigPath: path}); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != "agent:\n  model: local/m\n" {
		t.Fatalf("config.yaml after dropping the last key:\n%q", after)
	}
	if _, err := os.Stat(GlobalMCPJSONPath(home)); !os.IsNotExist(err) {
		t.Fatal("an empty key must not create an mcp.json")
	}
}

// A moved value keeps meaning what config.yaml made of it: an environment
// reference stays a reference (mcp.json resolves ${NAME} when the server
// starts, so no secret is written), a bare $NAME becomes ${NAME}, the "$$"
// escape becomes a literal "$" ("$${" when a brace follows, mcp.json's own
// escape), and ${CODDY_HOME} is written out as the home it named.
func TestLegacyMCPValueKeepsItsMeaning(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	cases := map[string]string{
		"${GITHUB_TOKEN}":       "${GITHUB_TOKEN}",
		"Bearer $TOKEN":         "Bearer ${TOKEN}",
		"pa$$word":              "pa$word",
		"$${LITERAL}":           "$${LITERAL}",
		"${CODDY_HOME}/bin/srv": yamlSafePath(home) + "/bin/srv",
		"${CWD}/tools":          "${CWD}/tools",
		"plain":                 "plain",
		"costs 5$":              "costs 5$",
	}
	for in, want := range cases {
		if got := legacyMCPValue(in, home); got != want {
			t.Errorf("legacyMCPValue(%q) = %q, want %q", in, got, want)
		}
	}

	// And a server starts with the value config.yaml used to give it.
	t.Setenv("TOKEN", "t0k")
	t.Setenv("GITHUB_TOKEN", "gh")
	cwd := filepath.Join(t.TempDir(), "work")
	for in := range cases {
		want := ExpandCWD(expandConfigText(in, Paths{Home: home}), cwd)
		if got := ExpandMCPValue(legacyMCPValue(in, home), cwd); got != want {
			t.Errorf("%q starts the server with %q, config.yaml gave it %q", in, got, want)
		}
	}
}

// mcp.json values resolve when a server starts: ${CWD} is the session
// workspace, ${NAME} and ${env:NAME} the environment (${NAME:-default} when
// it is unset or empty), "$${" a literal "${", and any other "$" is literal.
func TestExpandMCPValue(t *testing.T) {
	t.Setenv("MCP_TEST_TOKEN", "abc")
	t.Setenv("MCP_TEST_EMPTY", "")
	cwd := filepath.Join(t.TempDir(), "work")
	cases := []struct{ in, want string }{
		{"${CWD}/bin", cwd + "/bin"},
		{"Bearer ${MCP_TEST_TOKEN}", "Bearer abc"},
		{"${env:MCP_TEST_TOKEN}", "abc"},
		{"${MCP_TEST_UNSET}", ""},
		{"${MCP_TEST_UNSET:-fallback}", "fallback"},
		{"${MCP_TEST_EMPTY:-fallback}", "fallback"},
		{"${MCP_TEST_TOKEN:-fallback}", "abc"},
		{"$${MCP_TEST_TOKEN}", "${MCP_TEST_TOKEN}"},
		{"pa$word $MCP_TEST_TOKEN", "pa$word $MCP_TEST_TOKEN"},
		{"${unclosed", "${unclosed"},
		{"plain", "plain"},
	}
	for _, c := range cases {
		if got := ExpandMCPValue(c.in, cwd); got != c.want {
			t.Errorf("ExpandMCPValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// The variables a value reads, by name, as an approval shows them.
	if got := MCPValueVariables("${CWD}/x ${A} ${env:B} ${C:-d} $${E} $F ${unclosed"); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Errorf("MCPValueVariables = %v, want [A B C]", got)
	}
}

// The block of mcp_servers ends where the parser puts the next key, so a
// layout the old line rule misread - a comment at column 0 among the items,
// a sequence written without indentation, a flow list closed at column 0 -
// is cut whole and the file still reads.
func TestLegacyMoveCutsTheWholeBlockWhateverItsLayout(t *testing.T) {
	head := "agent:\n  model: local/m\n"
	tail := "rules:\n  enable: true\n"
	cases := map[string]string{
		"comment before items written without indentation": "mcp_servers:\n# first server\n- name: a\n  command: x\n",
		"commented-out entry between indented items":       "mcp_servers:\n  - name: a\n    command: x\n#  - name: b\n#    command: y\n  - name: c\n    command: z\n",
		"commented-out entry between unindented items":     "mcp_servers:\n- name: a\n  command: x\n# - name: b\n- name: c\n  command: z\n",
		"flow list closed at column 0":                     "mcp_servers: [\n  {name: a, command: x},\n  {name: c, command: z}\n]\n",
	}
	for name, legacy := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(head+legacy+tail), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadWithPaths(Paths{Home: home, CWD: dir, ConfigPath: path}); err != nil {
				t.Fatalf("load after the move: %v", err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != head+tail {
				t.Fatalf("config.yaml after the move:\n%s\nwant:\n%s", after, head+tail)
			}
			moved, _ := ReadMCPJSONFile(GlobalMCPJSONPath(home))
			if _, ok := moved["a"]; !ok {
				t.Fatalf("servers moved: %v", moved)
			}
			// A second load finds nothing more to do.
			if _, err := LoadWithPaths(Paths{Home: home, CWD: dir, ConfigPath: path}); err != nil {
				t.Fatal(err)
			}
			if backups, _ := filepath.Glob(path + ".bak-*"); len(backups) != 1 {
				t.Fatalf("backups = %v, want one", backups)
			}
		})
	}
}

// A key inside a flow-style mapping is not a block of lines: what it holds is
// moved, the key stays for the operator, and nothing around it is lost.
func TestLegacyKeyInAFlowMappingIsMovedButNotCut(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	body := "skills: {dirs: [/opt/team], sources: [owner/new], auto_discovery: false}\nrules:\n  enable: true\n"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithPaths(Paths{Home: home, CWD: dir, ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Skills.AutoDiscoveryEnabled() || len(cfg.Skills.Dirs) != 1 {
		t.Fatalf("the rest of the skills mapping was lost: %+v", cfg.Skills)
	}
	if after, _ := os.ReadFile(path); string(after) != body {
		t.Fatalf("a flow mapping was rewritten:\n%s", after)
	}
	if got, _ := ReadMarketplacesFile(GlobalMarketplacesPath(home)); !reflect.DeepEqual(got.Sources, []string{"owner/new"}) {
		t.Fatalf("the sources were not moved: %+v", got)
	}
}

// A config.yaml read because the home has none may have come with a
// checkout: its servers are not made the operator's own and the file is not
// rewritten.
func TestLegacyKeysOfAWorkspaceConfigAreNotMoved(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	body := "mcp_servers:\n  - name: from-checkout\n    command: ./run-me.sh\nskills:\n  sources:\n    - attacker/skills\n"
	if err := os.WriteFile(filepath.Join(project, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromCLI(CLIPaths{Home: home, CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Paths.ConfigFromWorkspace || cfg.Paths.ConfigPath != filepath.Join(project, "config.yaml") {
		t.Fatalf("paths = %+v, want the workspace's file marked as such", cfg.Paths)
	}
	if after, _ := os.ReadFile(filepath.Join(project, "config.yaml")); string(after) != body {
		t.Fatalf("the workspace's config.yaml was rewritten:\n%s", after)
	}
	if backups, _ := filepath.Glob(filepath.Join(project, "config.yaml.bak-*")); len(backups) != 0 {
		t.Fatalf("a backup was left in the workspace: %v", backups)
	}
	if moved, _ := ReadMCPJSONFile(GlobalMCPJSONPath(home)); len(moved) != 0 {
		t.Fatalf("servers of a checkout moved into the home: %v", moved)
	}
	if got, _ := ReadMarketplacesFile(GlobalMarketplacesPath(home)); len(got.Sources) != 0 {
		t.Fatalf("sources of a checkout moved into the home: %+v", got)
	}
}

// A key that could not move yet (the file it moves into does not read) stays
// in config.yaml, and a save of the settings keeps it there as the file had
// it instead of dropping what it declares.
func TestASaveKeepsAKeyThatCouldNotMoveYet(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalMCPJSONPath(home), []byte("{\"mcpServers\": {,}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalMarketplacesPath(home), []byte("{,}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "agent:\n  model: local/m\nmcp_servers:\n  - name: github\n    command: npx\nskills:\n  sources:\n    - owner/new\nrules:\n  enable: true\n"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithPaths(Paths{Home: home, CWD: dir, ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if still, _ := os.ReadFile(path); string(still) != body {
		t.Fatalf("a move that could not run rewrote the file:\n%s", still)
	}
	out, err := MarshalConfigYAMLForFile(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mcp_servers:", "name: github", "sources:", "owner/new"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("a save dropped %q:\n%s", want, out)
		}
	}
}
