//go:build cli

package cli

// Tests of the console's MCP connect view (mcp_status.go): the footer
// segment and its truncation, the failure and approval rows said once, the
// status line handed back when the dial settles, and the first step of a
// turn sent while servers connect. TestHelperConsoleMCP is the gated stdio
// stub the console scenarios of features/cli_tui.feature run.

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	consoleMCPHelperEnv  = "CODDY_TEST_CONSOLE_MCP"
	consoleMCPReleaseEnv = "CODDY_TEST_CONSOLE_MCP_RELEASE"
	// consoleMCPStartedEnv names a file the stub writes as soon as it runs,
	// so a scenario can tell a server that was spawned from one the trust
	// gate held.
	consoleMCPStartedEnv = "CODDY_TEST_CONSOLE_MCP_STARTED"
)

// TestHelperConsoleMCP is the stdio MCP stub of the console scenarios: it
// answers initialize once the release file exists and offers one tool.
func TestHelperConsoleMCP(t *testing.T) {
	if os.Getenv(consoleMCPHelperEnv) != "1" {
		return
	}
	release := os.Getenv(consoleMCPReleaseEnv)
	if started := os.Getenv(consoleMCPStartedEnv); started != "" {
		_ = os.WriteFile(started, []byte("1"), 0o644)
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		id, hasID := request["id"]
		if !hasID {
			continue
		}
		result := map[string]interface{}{}
		switch request["method"] {
		case "initialize":
			for release != "" {
				if _, err := os.Stat(release); err == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "console-probe", "version": "1"},
			}
		case "tools/list":
			result["tools"] = []interface{}{
				map[string]interface{}{"name": "probe", "description": "probe", "inputSchema": map[string]interface{}{"type": "object"}},
			}
		}
		if err := encoder.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
			return
		}
	}
	os.Exit(0)
}

func mcpUpdate(done bool, servers ...session.MCPServerConnect) session.MCPConnectUpdate {
	return session.MCPConnectUpdate{Servers: servers, Done: done}
}

// TestFooterCountsServersWhilePending: the segment names the connected count
// while a server is still connecting and leaves the line once all settled.
func TestFooterCountsServersWhilePending(t *testing.T) {
	a := newTestApp(t)
	a.applyMCPConnect(mcpUpdate(false,
		session.MCPServerConnect{Name: "github", State: session.MCPConnectStateConnected, Tools: 3},
		session.MCPServerConnect{Name: "docker", State: session.MCPConnectStateConnecting},
	))
	if line := a.foot.Render(100)[0]; !strings.Contains(line, "MCP 1/2") {
		t.Fatalf("footer line 1 = %q, want the MCP 1/2 segment", line)
	}
	a.applyMCPConnect(mcpUpdate(true,
		session.MCPServerConnect{Name: "github", State: session.MCPConnectStateConnected, Tools: 3},
		session.MCPServerConnect{Name: "docker", State: session.MCPConnectStateConnected, Tools: 1},
	))
	if line := a.foot.Render(100)[0]; strings.Contains(line, "MCP") {
		t.Fatalf("footer line 1 = %q, want no MCP segment once every server settled", line)
	}
}

// TestFooterSegmentSurvivesANarrowWidth: the path gives way, the count stays.
func TestFooterSegmentSurvivesANarrowWidth(t *testing.T) {
	a := newTestApp(t)
	a.foot.cwd = "/very/long/path/to/a/workspace/that/does/not/fit/on/a/narrow/terminal/at/all"
	a.applyMCPConnect(mcpUpdate(false, session.MCPServerConnect{Name: "one", State: session.MCPConnectStateConnecting}))
	line := a.foot.Render(40)[0]
	if !strings.Contains(line, "MCP 0/1") {
		t.Fatalf("footer line 1 at width 40 = %q, want the MCP segment kept", line)
	}
}

// TestFailedAndHeldServersAreSaidOnce: a failure row carries the hint, and a
// later update with the same server adds no second row. The rows wrap at the
// terminal's width, so the needles are fragments short enough to stay whole.
func TestFailedAndHeldServersAreSaidOnce(t *testing.T) {
	a := newTestApp(t)
	failed := session.MCPServerConnect{Name: "docker", State: session.MCPConnectStateFailed, Error: "exit status 127", Hint: "npx -y mcp-server-docker has no version"}
	held := session.MCPServerConnect{Name: "project", State: session.MCPConnectStateHeld, Hint: "approve it with: coddy mcp trust project"}
	a.applyMCPConnect(mcpUpdate(false, failed, held))
	a.applyMCPConnect(mcpUpdate(true, failed, held))
	text := transcriptText(a)
	if n := strings.Count(text, "did not connect"); n != 1 {
		t.Fatalf("failure row shown %d times:\n%s", n, text)
	}
	for _, fragment := range []string{"MCP server docker", "exit status 127", "has no version"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("failure row lacks %q:\n%s", fragment, text)
		}
	}
	if n := strings.Count(text, "waits for approval"); n != 1 {
		t.Fatalf("held row shown %d times:\n%s", n, text)
	}
	if !strings.Contains(text, "coddy mcp trust") {
		t.Fatalf("held row lacks the approval hint:\n%s", text)
	}
}

// TestSettledConnectHandsTheStatusLineBack: a turn parked on the connect goes
// back to waiting for the model when the dial settles, and a turn on another
// step is left alone.
func TestSettledConnectHandsTheStatusLineBack(t *testing.T) {
	a := newTestApp(t)
	a.turnActive = true
	a.stepStatus = newWorkingStatus(statusConnectingMCP, mcpStatusStep)
	a.applyMCPConnect(mcpUpdate(true, session.MCPServerConnect{Name: "one", State: session.MCPConnectStateConnected}))
	if a.stepStatus.verb != statusWaitingModel {
		t.Fatalf("status after the connect settled = %q, want %q", a.stepStatus.verb, statusWaitingModel)
	}
	a.stepStatus = newWorkingStatus("Reading a file", "call_1")
	a.applyMCPConnect(mcpUpdate(true, session.MCPServerConnect{Name: "one", State: session.MCPConnectStateConnected}))
	if a.stepStatus.verb != "Reading a file" {
		t.Fatalf("status of an unrelated step changed to %q", a.stepStatus.verb)
	}
}

func TestMCPWarningsReturnAfterTranscriptReset(t *testing.T) {
	for _, tc := range []struct {
		state string
		text  string
	}{
		{session.MCPConnectStateFailed, "did not connect"},
		{session.MCPConnectStateHeld, "waits for approval"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			a := newTestApp(t)
			a.sessionID = "original"
			update := mcpUpdate(true, session.MCPServerConnect{Name: "example", State: tc.state})
			a.applyMCPConnect(update)
			a.resetTranscript()
			a.sessionID = "other"
			a.applyMCPConnect(update)
			a.resetTranscript()
			a.sessionID = "original"
			a.applyMCPConnect(update)
			a.applyMCPConnect(update)
			if text := transcriptText(a); strings.Count(text, tc.text) != 1 {
				t.Fatalf("want one restored %q warning after resuming, got:\n%s", tc.text, text)
			}
		})
	}
}

// TestTurnStartsOnTheConnectStepWhilePending: a prompt sent while servers
// connect shows the connect as its first step.
func TestTurnStartsOnTheConnectStepWhilePending(t *testing.T) {
	a := newTestApp(t)
	a.applyMCPConnect(mcpUpdate(false, session.MCPServerConnect{Name: "one", State: session.MCPConnectStateConnecting}))
	if got := a.initialTurnStatus(); got.verb != statusConnectingMCP || got.step != mcpStatusStep {
		t.Fatalf("initial status = %+v, want the MCP connect step", got)
	}
	a.applyMCPConnect(mcpUpdate(true, session.MCPServerConnect{Name: "one", State: session.MCPConnectStateConnected}))
	if got := a.initialTurnStatus(); got.verb != statusWaitingModel {
		t.Fatalf("initial status = %+v, want waiting for the model", got)
	}
}

// TestStaleGenerationIsDropped: the last update of a superseded dial that
// lands after the replacement's does not overwrite what the replacement
// showed; an update without a generation is taken as it comes.
func TestStaleGenerationIsDropped(t *testing.T) {
	a := newTestApp(t)
	newer := mcpUpdate(true, session.MCPServerConnect{Name: "one", State: session.MCPConnectStateConnected})
	newer.Generation = 4
	a.applyMCPConnect(newer)
	stale := mcpUpdate(false, session.MCPServerConnect{Name: "old", State: session.MCPConnectStateFailed, Error: "gone"})
	stale.Generation = 3
	a.applyMCPConnect(stale)
	if a.mcpPending || a.mcpGeneration != 4 {
		t.Fatalf("stale generation applied: pending %v generation %d", a.mcpPending, a.mcpGeneration)
	}
	if strings.Contains(transcriptText(a), "did not connect") {
		t.Fatal("the stale generation's failure row was shown")
	}
	untagged := mcpUpdate(false, session.MCPServerConnect{Name: "one", State: session.MCPConnectStateConnecting})
	a.applyMCPConnect(untagged)
	if !a.mcpPending {
		t.Fatal("an update without a generation was dropped")
	}
	a.seedMCPStatus()
	if a.mcpGeneration != 0 {
		t.Fatalf("seeding kept generation %d", a.mcpGeneration)
	}
}
