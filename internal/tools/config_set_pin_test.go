package tools

// Tests of the npx pin config_set stages (config_set.go, pinStagedNPXServers)
// against an httptest npm registry: a new server pinned as one more staged
// command, a hand-written server left alone, an unresolved package reported,
// an args edit pinned, a ${VAR} spec skipped.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pinRegistry stands in for the npm registry: it answers the versions given
// and 404 for anything else, and DefaultResolver reads it through
// npm_config_registry.
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

type pinnedResult struct {
	Pending []string `json:"pending"`
	Pinned  []struct {
		Server  string `json:"server"`
		Package string `json:"package"`
		Version string `json:"version"`
		Pinned  bool   `json:"pinned"`
		Message string `json:"message"`
	} `json:"pinned"`
	Hint string `json:"hint"`
}

func decodePinned(t *testing.T, raw string) pinnedResult {
	t.Helper()
	var got pinnedResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("config_set result %s: %v", raw, err)
	}
	return got
}

const pinTestConfig = "agent:\n  max_turns: 17\nmcp_servers:\n  - name: filesystem\n    command: npx\n    args: [\"-y\", \"@modelcontextprotocol/server-filesystem\"]\n"

// TestConfigSetPinsANewNPXServer: the staged entry gets its version as one
// more staged command, and the result says so.
func TestConfigSetPinsANewNPXServer(t *testing.T) {
	pinRegistry(t, map[string]string{"@upstash/context7-mcp": "1.0.14"})
	env := testConfigToolsEnv(t, pinTestConfig)
	got := decodePinned(t, stageCommands(t, env, `set mcp_servers[name=context7]={"command":"npx","args":["-y","@upstash/context7-mcp"]}`))
	if len(got.Pinned) != 1 || !got.Pinned[0].Pinned || got.Pinned[0].Version != "1.0.14" || got.Pinned[0].Server != "context7" {
		t.Fatalf("pinned = %+v, want context7 pinned to 1.0.14", got.Pinned)
	}
	want := `set mcp_servers[name=context7].args=["-y","@upstash/context7-mcp@1.0.14"]`
	if len(got.Pending) != 2 || got.Pending[1] != want {
		t.Fatalf("pending = %v, want the pin as the second command %q", got.Pending, want)
	}
	if !strings.Contains(got.Hint, "pinned") {
		t.Fatalf("hint = %q, want it to tell the agent to relay the pin", got.Hint)
	}
	if pending := pendingOf(t, env); len(pending) != 2 {
		t.Fatalf("config_changes lists %v, want both commands staged", pending)
	}
}

// TestConfigSetLeavesAHandWrittenServerAlone: only servers the batch adds
// or changes are pinned; the Background's filesystem entry stays as it is.
func TestConfigSetLeavesAHandWrittenServerAlone(t *testing.T) {
	pinRegistry(t, map[string]string{"@modelcontextprotocol/server-filesystem": "9.9.9"})
	env := testConfigToolsEnv(t, pinTestConfig)
	got := decodePinned(t, stageCommands(t, env, "set agent.max_turns=20"))
	if len(got.Pinned) != 0 || len(got.Pending) != 1 {
		t.Fatalf("result = %+v, want no pin for a server the batch did not touch", got)
	}
}

// TestConfigSetReportsAnUnresolvedPackage: the registry has no answer, the
// server is staged as written and the result says why that matters.
func TestConfigSetReportsAnUnresolvedPackage(t *testing.T) {
	pinRegistry(t, nil)
	env := testConfigToolsEnv(t, pinTestConfig)
	got := decodePinned(t, stageCommands(t, env, `set mcp_servers[name=docker]={"command":"npx","args":["-y","mcp-server-docker"]}`))
	if len(got.Pinned) != 1 || got.Pinned[0].Pinned || !strings.Contains(got.Pinned[0].Message, "saved unpinned") {
		t.Fatalf("pinned = %+v, want an unresolved report", got.Pinned)
	}
	if len(got.Pending) != 1 {
		t.Fatalf("pending = %v, want the batch staged as written", got.Pending)
	}
}

// TestConfigSetPinsAnArgsEdit: changing the arguments of an existing server
// counts as registering it again.
func TestConfigSetPinsAnArgsEdit(t *testing.T) {
	pinRegistry(t, map[string]string{"@modelcontextprotocol/server-filesystem": "2026.1.1"})
	env := testConfigToolsEnv(t, pinTestConfig)
	got := decodePinned(t, stageCommands(t, env, `set mcp_servers[name=filesystem].args=["-y","@modelcontextprotocol/server-filesystem","/srv"]`))
	if len(got.Pinned) != 1 || !got.Pinned[0].Pinned {
		t.Fatalf("pinned = %+v, want the edited server pinned", got.Pinned)
	}
	want := `set mcp_servers[name=filesystem].args=["-y","@modelcontextprotocol/server-filesystem@2026.1.1","/srv"]`
	if got.Pending[len(got.Pending)-1] != want {
		t.Fatalf("last pending = %q, want %q", got.Pending[len(got.Pending)-1], want)
	}
}

// TestConfigSetSkipsAPlaceholderSpec: a package name that is still a ${VAR}
// is not something to pin.
func TestConfigSetSkipsAPlaceholderSpec(t *testing.T) {
	pinRegistry(t, map[string]string{"${MCP_PACKAGE}": "1.0.0"})
	env := testConfigToolsEnv(t, pinTestConfig)
	got := decodePinned(t, stageCommands(t, env, `set mcp_servers[name=dyn]={"command":"npx","args":["-y","${MCP_PACKAGE}"]}`))
	if len(got.Pinned) != 0 || len(got.Pending) != 1 {
		t.Fatalf("result = %+v, want no pin for a placeholder", got)
	}
}
