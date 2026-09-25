package dryrun

// Tests of the --dry-run warning for an MCP server that runs an npx package
// without a version (paths.go, mcpCommands): one warning at the server's
// line, none for a pinned, a placeholder or a disabled entry.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// npxOnPath puts an npx that does nothing on PATH, so the command check
// passes and the args check is what the test reads.
func npxOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	name, body := "npx", "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		name, body = "npx.cmd", "@echo off\r\n"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestMCPUnpinnedNPXIsAWarning(t *testing.T) {
	npxOnPath(t)
	rep := run(t, "mcp_servers:\n"+
		"  - name: github\n    command: npx\n    args: [\"-y\", \"@modelcontextprotocol/server-github\"]\n"+
		"  - name: pinned\n    command: npx\n    args: [\"-y\", \"@modelcontextprotocol/server-github@2026.9.1\"]\n"+
		"  - name: dyn\n    command: npx\n    args: [\"-y\", \"${MCP_PACKAGE}\"]\n"+
		"  - name: off\n    command: npx\n    args: [\"-y\", \"mcp-server-docker\"]\n    disabled: true\n", nil)
	var warnings []Check
	for _, c := range rep.Checks {
		if c.Status == StatusWarning && strings.HasPrefix(c.Path, "mcp_servers[") {
			warnings = append(warnings, c)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly one, for github", warnings)
	}
	w := warnings[0]
	if w.Path != "mcp_servers[github]" || !strings.Contains(w.Message, "has no version") || !strings.Contains(w.Fix, "@modelcontextprotocol/server-github@<version>") {
		t.Fatalf("warning = %+v", w)
	}
	if c := find(t, rep, "mcp_servers[off]"); c.Status != StatusSkipped {
		t.Errorf("disabled %+v", c)
	}
}
