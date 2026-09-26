//go:build cli

package cli

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Both backends of the console carry /mcp. The console finds the controls by
// a type assertion, so a signature that drifts would not fail to compile: the
// command would only answer "MCP management is unavailable".
var (
	_ mcpBackend = (*session.Manager)(nil)
	_ mcpBackend = (*remote.Handler)(nil)
)

// What a server's /mcp controls offer. The happy path of the command lives in
// features/cli_mcp.feature; these are the rows each kind of server gets.

func itemByValue(items []tui.SelectItem, value string) (tui.SelectItem, bool) {
	for _, item := range items {
		if item.Value == value {
			return item, true
		}
	}
	return tui.SelectItem{}, false
}

// A trust control is offered only where there is a decision to take: a
// project server under mcp.project_trust ask, the rule of the web's shield.
// Under allow a "Revoke trust" changed nothing and under deny "Grant trust"
// could only fail.
func TestMCPServerControlsOfferTrustOnlyWhereThereIsADecision(t *testing.T) {
	ask := mcp.ServerStatus{Name: "proj", Origin: mcp.OriginProject, Status: "needs_approval",
		Enabled: true, Approvable: true, Declaration: "stdio · proj-mcp --index · env: TOKEN"}
	items := mcpServerItems(ask)
	trust, ok := itemByValue(items, "trust")
	if !ok || trust.Label != "Grant trust" || !strings.Contains(trust.Description, "proj-mcp --index") {
		t.Fatalf("unapproved project server under ask: items = %+v", items)
	}
	approved := ask
	approved.Trusted, approved.Status = true, "connected"
	if trust, _ := itemByValue(mcpServerItems(approved), "trust"); trust.Label != "Revoke trust" {
		t.Fatalf("approved project server under ask: trust item = %+v", trust)
	}

	for _, row := range []mcp.ServerStatus{
		{Name: "proj", Origin: mcp.OriginProject, Status: "connected", Enabled: true, Trusted: true}, // allow
		{Name: "proj", Origin: mcp.OriginProject, Status: "denied", Enabled: true},                   // deny
		{Name: "glob", Origin: mcp.OriginHome, Status: "connected", Enabled: true, Trusted: true},    // global
		{Name: "yaml", Origin: mcp.OriginConfig, Status: "disabled", Enabled: false, Trusted: true},  // config.yaml
	} {
		if _, ok := itemByValue(mcpServerItems(row), "trust"); ok {
			t.Errorf("%s (%s, %s) offers a trust control", row.Name, row.Origin, row.Status)
		}
		if _, ok := itemByValue(mcpServerItems(row), "switch"); !ok {
			t.Errorf("%s has no server switch", row.Name)
		}
	}
}

// Each tool of a connected server is its own row, saying whether it is on.
func TestMCPServerControlsListToolsWithTheirSwitch(t *testing.T) {
	row := mcp.ServerStatus{Name: "glob", Origin: mcp.OriginHome, Status: "connected", Enabled: true, Trusted: true,
		Tools: []mcp.ToolStatus{{Name: "ping", Enabled: true}, {Name: "write", Enabled: false}}}
	items := mcpServerItems(row)
	ping, ok := itemByValue(items, "tool:ping")
	if !ok || !strings.HasPrefix(ping.Description, "on") {
		t.Fatalf("ping row = %+v", ping)
	}
	write, ok := itemByValue(items, "tool:write")
	if !ok || !strings.HasPrefix(write.Description, "off") {
		t.Fatalf("write row = %+v", write)
	}
}

// A server's line says it is switched off even when its status is a trust
// verdict, which would otherwise hide the switch.
func TestMCPServerSummaryNamesASwitchedOffServer(t *testing.T) {
	for _, tc := range []struct {
		row  mcp.ServerStatus
		want string
	}{
		{mcp.ServerStatus{Scope: "local", Status: "needs_approval", Enabled: false}, "local · needs_approval · off · 0 tools"},
		{mcp.ServerStatus{Scope: "local", Status: "needs_approval", Enabled: true}, "local · needs_approval · 0 tools"},
		{mcp.ServerStatus{Scope: "global", Status: "disabled", Enabled: false}, "global · disabled · 0 tools"},
		{mcp.ServerStatus{Scope: "global", Status: "connected", Enabled: true,
			Tools: []mcp.ToolStatus{{Name: "a"}, {Name: "b"}}}, "global · connected · 2 tools"},
	} {
		if got := mcpRowSummary(tc.row); got != tc.want {
			t.Errorf("summary = %q, want %q", got, tc.want)
		}
	}
}
