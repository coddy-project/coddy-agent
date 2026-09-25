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

type mcpBackend interface {
	MCPServers(context.Context, string) ([]mcp.ServerStatus, error)
	SetMCPEnabled(context.Context, string, string, string, bool) error
	SetMCPTrust(context.Context, string, string, bool) error
}

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
		ctx, cancel := context.WithTimeout(a.workCtx, 45*time.Second)
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

func (a *App) showMCPServers(rows []mcp.ServerStatus, focus string) {
	items := make([]tui.SelectItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, tui.SelectItem{Value: row.Name,
			Label: tui.SanitizeText(row.Name), Description: fmt.Sprintf("%s · %s · %d tools", row.Scope, row.Status, len(row.Tools))})
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

func (a *App) showMCPServer(row mcp.ServerStatus) {
	status := row.Status
	if row.Error != "" {
		status += ": " + tui.SanitizeText(row.Error)
	}
	items := []tui.SelectItem{{Value: "switch", Label: "Toggle server", Description: status}}
	if row.Origin == mcp.OriginProject {
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
	sel := newSelectorModal(a.theme, tui.SanitizeText(row.Name)+" · "+status, items, 12, a.screen.RequestRender)
	sel.OnDone = func(item *tui.SelectItem) {
		if item == nil {
			a.refreshMCP(row.Name)
			return
		}
		switch {
		case item.Value == "switch":
			a.changeMCP(row.Name, "", !row.Enabled, false)
		case item.Value == "trust":
			if row.Trusted {
				a.changeMCP(row.Name, "", false, true)
			} else {
				a.confirmMCPTrust(row)
			}
		case strings.HasPrefix(item.Value, "tool:"):
			name := strings.TrimPrefix(item.Value, "tool:")
			for _, tool := range row.Tools {
				if tool.Name == name {
					a.changeMCP(row.Name, name, !tool.Enabled, false)
					break
				}
			}
		}
	}
	a.openModal(sel)
	a.screen.RequestRender()
}

func (a *App) confirmMCPTrust(row mcp.ServerStatus) {
	items := []tui.SelectItem{
		{Value: "no", Label: "Cancel"},
		{Value: "yes", Label: "Approve this declaration", Description: tui.SanitizeText(row.Declaration)},
	}
	sel := newSelectorModal(a.theme, "Trust "+tui.SanitizeText(row.Name)+"?", items, 4, a.screen.RequestRender)
	sel.OnDone = func(item *tui.SelectItem) {
		if item == nil || item.Value != "yes" {
			a.showMCPServer(row)
			return
		}
		a.changeMCP(row.Name, "", true, true)
	}
	a.openModal(sel)
}

func (a *App) changeMCP(name, tool string, enabled, trust bool) {
	cwd := a.config().Paths.CWD
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ctx, cancel := context.WithTimeout(a.workCtx, 45*time.Second)
		defer cancel()
		var err error
		if trust {
			err = a.mcpBackend().SetMCPTrust(ctx, cwd, name, enabled)
		} else {
			err = a.mcpBackend().SetMCPEnabled(ctx, cwd, name, tool, enabled)
		}
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
