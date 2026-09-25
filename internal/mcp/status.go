package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// ServerStatus is a safe, secret-free view of a configured MCP server.
type ServerStatus struct {
	Name        string       `json:"name"`
	Scope       string       `json:"source"`
	Origin      string       `json:"origin"`
	Status      string       `json:"status"`
	Error       string       `json:"error,omitempty"`
	Enabled     bool         `json:"enabled"`
	Trusted     bool         `json:"trusted"`
	Declaration string       `json:"-"`
	Tools       []ToolStatus `json:"tools"`
}

type ToolStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// ListStatus probes approved, enabled servers through the trust gate. Its
// declaration summary omits environment and header values.
func ListStatus(ctx context.Context, cfg *config.Config, cwd string, log *slog.Logger) ([]ServerStatus, error) {
	managed, err := ListManagedServers(cfg, cwd)
	if err != nil {
		return nil, err
	}
	gate := NewTrustGate(cfg)
	rows := make([]ServerStatus, 0, len(managed))
	for _, srv := range managed {
		trust := gate.Evaluate(cwd, srv)
		envKeys := make([]string, 0, len(srv.Config.Env))
		for _, item := range srv.Config.Env {
			envKeys = append(envKeys, item.Name)
		}
		headerKeys := make([]string, 0, len(srv.Config.Headers))
		for _, item := range srv.Config.Headers {
			headerKeys = append(headerKeys, item.Name)
		}
		source := config.MCPJSONPath(cwd)
		if srv.Origin == OriginHome {
			source = config.GlobalMCPJSONPath(cfg.Paths.Home)
		}
		if srv.Origin == OriginConfig {
			source = cfg.Paths.ConfigPath
		}
		row := ServerStatus{Name: srv.Config.Name, Scope: srv.Scope, Origin: srv.Origin,
			Enabled: !srv.Config.Disabled, Trusted: trust == TrustStateAllowed, Tools: []ToolStatus{},
			Declaration: DeclarationSummary(EffectiveTransport(srv.Config), srv.Config.Command, srv.Config.Args, srv.Config.URL, envKeys, headerKeys, cwd, source)}
		switch {
		case trust != TrustStateAllowed:
			row.Status = string(trust)
		case srv.Config.Disabled:
			row.Status = "disabled"
		case !SupportedTransport(EffectiveTransport(srv.Config)):
			row.Status = "unsupported"
		default:
			probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			tools, probeErr := gate.Probe(probeCtx, srv, cwd, log)
			cancel()
			if probeErr != nil {
				row.Status, row.Error = "error", probeErr.Error()
			} else {
				row.Status = "connected"
			}
			for _, tool := range tools {
				row.Tools = append(row.Tools, ToolStatus{Name: tool.Name, Enabled: !containsName(srv.Config.DisabledTools, tool.Name)})
			}
			sort.Slice(row.Tools, func(i, j int) bool { return row.Tools[i].Name < row.Tools[j].Name })
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// DeclarationSummary presents an approval without exposing credential values.
func DeclarationSummary(transport, command string, args []string, target string, envKeys, headerKeys []string, cwd, source string) string {
	sort.Strings(envKeys)
	sort.Strings(headerKeys)
	line := strings.TrimSpace(command + " " + strings.Join(args, " "))
	if target != "" {
		line = target
	}
	return fmt.Sprintf("%s · %s · env: %s · headers: %s · workspace: %s · source: %s",
		transport, line, strings.Join(envKeys, ", "), strings.Join(headerKeys, ", "), cwd, source)
}

func containsName(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

// SetStatus applies a control offered by /mcp. Trust approval is deliberately
// absent: callers that offer it must show the full declaration first.
func SetStatus(cfg *config.Config, cwd, name, tool string, enabled bool) error {
	if name == "" {
		return fmt.Errorf("MCP server name is required")
	}
	if tool == "" {
		return SetServerDisabled(cfg, cwd, name, !enabled)
	}
	return SetToolDisabled(cfg, cwd, name, tool, !enabled)
}
