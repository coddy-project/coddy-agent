//go:build cli

package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// mcpBackend is the part of the console's backend behind /mcp: the in-process
// session manager, or under --remote the client that drives the server's MCP
// management routes.
type mcpBackend interface {
	MCPServers(ctx context.Context, cwd string) ([]mcp.ServerStatus, error)
	SetMCPEnabled(ctx context.Context, cwd, name, tool string, enabled bool) error
	SetMCPTrust(ctx context.Context, cwd, name, fingerprint string, trusted bool) error
}

// mcpWorkTimeout bounds one /mcp read or change: the probes of the list, or a
// switch together with the refresh of the live sessions it brings along.
const mcpWorkTimeout = 45 * time.Second

func (a *App) mcpBackend() mcpBackend {
	b, _ := a.mgr.(mcpBackend)
	return b
}

// /mcp reads on a worker: a stdio probe may take seconds and must not block
// the terminal's input loop.
func (a *App) openMCP() {
	if a.busyWithLocalShell() {
		return
	}
	if a.mcpBackend() == nil {
		a.appendStatus(roleWarning, "MCP management is unavailable")
		return
	}
	a.appendStatus(roleDim, "Reading MCP servers…")
	a.refreshMCP("")
}

func (a *App) refreshMCP(focus string) {
	cwd := a.config().Paths.CWD
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ctx, cancel := context.WithTimeout(a.workCtx, mcpWorkTimeout)
		defer cancel()
		rows, err := a.mcpBackend().MCPServers(ctx, cwd)
		select {
		case a.updatesCh <- updateMsg{update: uiCall(func() {
			if err != nil {
				a.appendStatus(roleError, "MCP: "+err.Error())
				return
			}
			a.showMCPServers(rows, focus)
		})}:
		case <-a.closed:
		}
	}()
}

// mcpRowSummary is a server's line in the list: scope, status, "off" when the
// server is switched off and its status says something else (a trust verdict
// would otherwise hide the switch), and how many tools it listed.
func mcpRowSummary(row mcp.ServerStatus) string {
	parts := []string{row.Scope, row.Status}
	if !row.Enabled && row.Status != "disabled" {
		parts = append(parts, "off")
	}
	return strings.Join(parts, " · ") + fmt.Sprintf(" · %d tools", len(row.Tools))
}

func (a *App) showMCPServers(rows []mcp.ServerStatus, focus string) {
	items := make([]tui.SelectItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, tui.SelectItem{Value: row.Name,
			Label: tui.SanitizeText(row.Name), Description: mcpRowSummary(row)})
	}
	if len(items) == 0 {
		items = append(items, tui.SelectItem{Value: "", Label: "No MCP servers configured"})
	}
	sel := newSelectorModal(a.theme, "MCP servers", items, 10, a.screen.RequestRender)
	sel.help = "↑↓ navigate · enter details · esc back"
	sel.rebuild()
	for i, row := range rows {
		if row.Name == focus {
			sel.list.SetSelectedIndex(i)
			break
		}
	}
	sel.OnDone = func(item *tui.SelectItem) {
		if item == nil {
			a.closeModal()
			return
		}
		for _, row := range rows {
			if row.Name == item.Value {
				a.showMCPServer(row)
				return
			}
		}
	}
	a.openModal(sel)
	a.screen.RequestRender()
}

// mcpServerItems are the controls of one server: its switch, a trust control
// where a per-server decision exists (a project server under
// mcp.project_trust ask, the rule of the web's shield), and a switch for every
// tool it listed.
func mcpServerItems(row mcp.ServerStatus) []tui.SelectItem {
	status := "on · " + row.Status
	if !row.Enabled {
		status = "off · " + row.Status
	}
	if row.Error != "" {
		status += ": " + tui.SanitizeText(row.Error)
	}
	items := []tui.SelectItem{{Value: "switch", Label: "Toggle server", Description: status}}
	if row.Approvable {
		label := "Grant trust"
		if row.Trusted {
			label = "Revoke trust"
		}
		items = append(items, tui.SelectItem{Value: "trust", Label: label, Description: tui.SanitizeText(row.Declaration)})
	}
	for _, tool := range row.Tools {
		state := "on"
		if !tool.Enabled {
			state = "off"
		}
		items = append(items, tui.SelectItem{Value: "tool:" + tool.Name, Label: tui.SanitizeText(tool.Name), Description: state + " · enter to toggle"})
	}
	return items
}

func (a *App) showMCPServer(row mcp.ServerStatus) {
	status := row.Status
	if row.Error != "" {
		status += ": " + tui.SanitizeText(row.Error)
	}
	sel := newSelectorModal(a.theme, tui.SanitizeText(row.Name)+" · "+status, mcpServerItems(row), 12, a.screen.RequestRender)
	sel.OnDone = func(item *tui.SelectItem) {
		if item == nil {
			a.refreshMCP(row.Name)
			return
		}
		switch {
		case item.Value == "switch":
			a.setMCPSwitch(row.Name, "", !row.Enabled)
		case item.Value == "trust":
			if row.Trusted {
				a.setMCPTrust(row, false)
			} else {
				a.confirmMCPTrust(row)
			}
		case strings.HasPrefix(item.Value, "tool:"):
			name := strings.TrimPrefix(item.Value, "tool:")
			for _, tool := range row.Tools {
				if tool.Name == name {
					a.setMCPSwitch(row.Name, name, !tool.Enabled)
					break
				}
			}
		}
	}
	a.openModal(sel)
	a.screen.RequestRender()
}

// confirmMCPTrust shows the declaration once more, whole and wrapped above the
// choice rather than cut to a row's width, before it is approved: the
// approval covers the command, its arguments and the names of the variables
// it gets, so all of them have to be on screen.
func (a *App) confirmMCPTrust(row mcp.ServerStatus) {
	items := []tui.SelectItem{
		{Value: "no", Label: "Cancel"},
		{Value: "yes", Label: "Approve this declaration"},
	}
	sel := newSelectorModal(a.theme, "Trust "+tui.SanitizeText(row.Name)+"?", items, 4, a.screen.RequestRender)
	sel.body = tui.SanitizeText(row.Declaration)
	sel.rebuild()
	sel.OnDone = func(item *tui.SelectItem) {
		if item == nil || item.Value != "yes" {
			a.showMCPServer(row)
			return
		}
		a.setMCPTrust(row, true)
	}
	a.openModal(sel)
}

// setMCPSwitch flips a server's switch, or one tool's when tool is set.
func (a *App) setMCPSwitch(name, tool string, enabled bool) {
	a.runMCPChange(name, func(ctx context.Context, backend mcpBackend, cwd string) error {
		return backend.SetMCPEnabled(ctx, cwd, name, tool, enabled)
	})
}

// setMCPTrust grants or withdraws the workspace approval of a project server.
// A grant names the declaration the confirmation showed by its fingerprint, so
// a checkout that rewrote the entry since is refused rather than approved.
func (a *App) setMCPTrust(row mcp.ServerStatus, trusted bool) {
	fingerprint := ""
	if trusted {
		fingerprint = row.Fingerprint
	}
	a.runMCPChange(row.Name, func(ctx context.Context, backend mcpBackend, cwd string) error {
		return backend.SetMCPTrust(ctx, cwd, row.Name, fingerprint, trusted)
	})
}

// runMCPChange applies one /mcp change on a worker, then reopens the list on
// the server it changed; a failure is reported above the list.
func (a *App) runMCPChange(name string, change func(ctx context.Context, backend mcpBackend, cwd string) error) {
	cwd := a.config().Paths.CWD
	backend := a.mcpBackend()
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ctx, cancel := context.WithTimeout(a.workCtx, mcpWorkTimeout)
		defer cancel()
		err := change(ctx, backend, cwd)
		select {
		case a.updatesCh <- updateMsg{update: uiCall(func() {
			if err != nil {
				a.appendStatus(roleError, "MCP: "+err.Error())
			}
			a.refreshMCP(name)
		})}:
		case <-a.closed:
		}
	}()
}
