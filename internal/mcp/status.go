package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// ServerStatus is a safe, secret-free view of a configured MCP server.
type ServerStatus struct {
	Name    string `json:"name"`
	Scope   string `json:"source"`
	Origin  string `json:"origin"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Enabled bool   `json:"enabled"`
	Trusted bool   `json:"trusted"`
	// Approvable says a per-server trust decision exists for the row: a
	// project entry under mcp.project_trust ask. A surface offers grant and
	// revoke only then, the rule of the web's showsTrustControl.
	Approvable bool `json:"-"`
	// Fingerprint is the digest of the declaration listed. An approval sends
	// it back, so it cannot land on a declaration rewritten since.
	Fingerprint string       `json:"fingerprint,omitempty"`
	Declaration string       `json:"-"`
	Tools       []ToolStatus `json:"tools"`
}

// ToolStatus is one tool of a listed server and its switch.
type ToolStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// statusProbe is the probe ListStatus runs for one server, a variable so a
// test can watch the probes without spawning servers.
var statusProbe = func(ctx context.Context, gate *TrustGate, srv ManagedServer, cwd string, log *slog.Logger) ([]ToolInfo, error) {
	return gate.Probe(ctx, srv, cwd, log)
}

// statusProbeTimeout bounds one server's probe (spawn, initialize, tools/list),
// the same budget the HTTP list gives a probe.
const statusProbeTimeout = 8 * time.Second

// ListStatus probes approved, enabled servers through the trust gate. The
// probes run side by side, so the list takes as long as its slowest server.
// Its declaration summary omits environment and header values.
func ListStatus(ctx context.Context, cfg *config.Config, cwd string, log *slog.Logger) ([]ServerStatus, error) {
	managed, err := ListManagedServers(cfg, cwd)
	if err != nil {
		return nil, err
	}
	gate := NewTrustGate(cfg)
	rows := make([]ServerStatus, len(managed))
	var wg sync.WaitGroup
	for i, srv := range managed {
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
		rows[i] = ServerStatus{Name: srv.Config.Name, Scope: srv.Scope, Origin: srv.Origin,
			Enabled: !srv.Config.Disabled, Trusted: trust == TrustStateAllowed, Tools: []ToolStatus{},
			// A per-server decision exists only for a project entry under ask:
			// under allow every one starts, under deny none does.
			Approvable:  srv.Origin == OriginProject && gate.Policy() == config.ProjectTrustAsk,
			Fingerprint: Fingerprint(srv.Config),
			Declaration: DeclarationSummary(EffectiveTransport(srv.Config), srv.Config.Command, srv.Config.Args, srv.Config.URL, envKeys, headerKeys, cwd, source)}
		switch {
		case trust != TrustStateAllowed:
			rows[i].Status = string(trust)
		case srv.Config.Disabled:
			rows[i].Status = "disabled"
		case !SupportedTransport(EffectiveTransport(srv.Config)):
			rows[i].Status = "unsupported"
		default:
			wg.Add(1)
			go func(row *ServerStatus, srv ManagedServer) {
				defer wg.Done()
				probeCtx, cancel := context.WithTimeout(ctx, statusProbeTimeout)
				tools, probeErr := statusProbe(probeCtx, gate, srv, cwd, log)
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
			}(&rows[i], srv)
		}
	}
	wg.Wait()
	return rows, nil
}

// DeclarationSummary presents an approval without exposing credential values:
// the transport, the command line or the URL, the names of the environment
// variables and headers it carries (never their values), the workspace and
// the file it came from. A list that is empty is left out.
func DeclarationSummary(transport, command string, args []string, target string, envKeys, headerKeys []string, cwd, source string) string {
	sort.Strings(envKeys)
	sort.Strings(headerKeys)
	line := strings.TrimSpace(command + " " + strings.Join(args, " "))
	if target != "" {
		line = target
	}
	parts := []string{transport, line}
	if len(envKeys) > 0 {
		parts = append(parts, "env: "+strings.Join(envKeys, ", "))
	}
	if len(headerKeys) > 0 {
		parts = append(parts, "headers: "+strings.Join(headerKeys, ", "))
	}
	parts = append(parts, "workspace: "+cwd, "source: "+source)
	return strings.Join(parts, " · ")
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
