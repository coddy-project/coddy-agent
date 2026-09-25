//go:build cli

package cli

// The console's view of a background MCP connect: the footer count while
// the configured servers come up after the first frame, the rows that say a
// server failed or waits for approval, and the status line of a prompt sent
// before the servers answered. The manager reports the progress through
// session.MCPConnectUpdate, a control update that never reaches the ACP wire.

import (
	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// mcpStatusStep identifies the "Connecting MCP servers" step of a turn on the
// status line, so a connect that settles mid-turn hands the line back to the
// model's own phases and nothing else does.
const mcpStatusStep = "mcp"

// applyMCPConnect renders the progress of the session's background MCP
// connect: the footer counts the servers while any is still connecting, a
// server that failed or that the trust gate holds is said once as a row of
// the transcript, and a turn parked on the connect resumes its waiting
// status once the dial has settled.
func (a *App) applyMCPConnect(u session.MCPConnectUpdate) {
	connected, total := u.Counts()
	a.mcpConnected, a.mcpTotal, a.mcpPending = connected, total, !u.Done
	a.foot.SetMCP(connected, total, !u.Done)
	for _, srv := range u.Servers {
		switch srv.State {
		case session.MCPConnectStateFailed:
			if !a.reportMCPOnce(srv.Name) {
				continue
			}
			msg := "MCP server " + srv.Name + " did not connect: " + srv.Error
			if srv.Hint != "" {
				msg += ". " + srv.Hint
			}
			a.appendStatus(roleWarning, tui.SanitizeText(msg))
		case session.MCPConnectStateHeld:
			if !a.reportMCPOnce(srv.Name) {
				continue
			}
			msg := "MCP server " + srv.Name + " waits for approval"
			if srv.Hint != "" {
				msg += ": " + srv.Hint
			}
			a.appendStatus(roleDim, tui.SanitizeText(msg))
		}
	}
	if u.Done && a.turnActive && a.stepStatus.step == mcpStatusStep {
		a.stepStatus = newWaitingStatus()
	}
}

// reportMCPOnce records that the server's row has been shown for the current
// session and reports whether this call is the first to show it.
func (a *App) reportMCPOnce(name string) bool {
	if a.mcpReported == nil {
		a.mcpReported = map[string]bool{}
	}
	key := a.sessionID + "\x00" + name
	if a.mcpReported[key] {
		return false
	}
	a.mcpReported[key] = true
	return true
}

// seedMCPStatus reads the session's connect progress off the manager. It
// runs when the console adopts a session, so a connect that settled between
// session/new and the first frame, or whose updates were dropped as stale
// during a switch, is still shown correctly.
func (a *App) seedMCPStatus() {
	a.mcpConnected, a.mcpTotal, a.mcpPending = 0, 0, false
	a.foot.SetMCP(0, 0, false)
	if a.mgr == nil || a.sessionID == "" {
		return
	}
	st := a.mgr.SessionByID(a.sessionID)
	if st == nil {
		return
	}
	if snap, ok := st.MCPConnectSnapshot(); ok {
		a.applyMCPConnect(snap)
	}
}
