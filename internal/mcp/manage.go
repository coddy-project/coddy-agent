// Management operations shared by the HTTP API and CLI: merged server list
// with scope/origin labels, enable/disable persistence into the owning file
// (config.yaml, <home>/mcp.json, or <cwd>/.coddy/mcp.json), and mcp.json
// server CRUD.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Scopes reported by ListManagedServers (user-facing grouping).
const (
	ScopeGlobal = "global" // config.yaml mcp_servers or <home>/mcp.json
	ScopeLocal  = "local"  // <cwd>/.coddy/mcp.json
)

// Origins identify the file that owns a server definition.
const (
	OriginConfig  = "config"  // config.yaml (edited via the config API, not here)
	OriginHome    = "home"    // <home>/mcp.json (global, Cursor-style)
	OriginProject = "project" // <cwd>/.coddy/mcp.json (project-local)
)

// ManagedServer is one merged server definition with its scope and origin.
type ManagedServer struct {
	Config config.MCPServerConfig
	Scope  string
	Origin string
}

// ListManagedServers merges config.yaml, global, and project servers for cwd
// in that precedence order (later overrides earlier by name), labeling each
// entry with the file that owns its definition.
func ListManagedServers(cfg *config.Config, cwd string) ([]ManagedServer, error) {
	global, err := config.LoadMCPJSONServers(config.GlobalMCPJSONPath(cfg.Paths.Home))
	if err != nil {
		return nil, err
	}
	project, err := config.LoadMCPJSONServers(config.MCPJSONPath(cwd))
	if err != nil {
		return nil, err
	}
	return mergeManaged(cfg.MCPServers, global, project), nil
}

// ListManagedServersTolerant is ListManagedServers with a broken mcp.json
// logged and skipped instead of failing the whole list. Session bootstrap
// uses it so one unreadable file cannot stop a session from starting.
func ListManagedServersTolerant(cfg *config.Config, cwd string, log *slog.Logger) []ManagedServer {
	load := func(path string) []config.MCPServerConfig {
		servers, err := config.LoadMCPJSONServers(path)
		if err != nil {
			if log != nil {
				log.Warn("failed to load mcp.json", "path", path, "error", err)
			}
			return nil
		}
		return servers
	}
	return mergeManaged(cfg.MCPServers,
		load(config.GlobalMCPJSONPath(cfg.Paths.Home)),
		load(config.MCPJSONPath(cwd)))
}

// mergeManaged overlays the two mcp.json levels onto config.yaml and labels
// every merged entry with the file that owns its definition.
func mergeManaged(fromConfig, global, project []config.MCPServerConfig) []ManagedServer {
	origins := make(map[string]string, len(fromConfig)+len(global)+len(project))
	for _, srv := range fromConfig {
		origins[srv.Name] = OriginConfig
	}
	for _, srv := range global {
		origins[srv.Name] = OriginHome
	}
	for _, srv := range project {
		origins[srv.Name] = OriginProject
	}
	merged := config.MergeMCPServers(config.MergeMCPServers(fromConfig, global), project)
	out := make([]ManagedServer, 0, len(merged))
	for _, srv := range merged {
		origin := origins[srv.Name]
		scope := ScopeGlobal
		if origin == OriginProject {
			scope = ScopeLocal
		}
		out = append(out, ManagedServer{Config: srv, Scope: scope, Origin: origin})
	}
	return out
}

// findManaged resolves one merged server by name.
func findManaged(cfg *config.Config, cwd, name string) (*ManagedServer, error) {
	servers, err := ListManagedServers(cfg, cwd)
	if err != nil {
		return nil, err
	}
	for i := range servers {
		if servers[i].Config.Name == name {
			return &servers[i], nil
		}
	}
	return nil, fmt.Errorf("mcp server %q not found", name)
}

// owningJSONPath returns the mcp.json path that defines srv, or "" when the
// definition lives in config.yaml.
func owningJSONPath(cfg *config.Config, cwd string, srv *ManagedServer) string {
	switch srv.Origin {
	case OriginProject:
		return config.MCPJSONPath(cwd)
	case OriginHome:
		return config.GlobalMCPJSONPath(cfg.Paths.Home)
	default:
		return ""
	}
}

// SetServerDisabled persists the server-level switch into the owning file.
func SetServerDisabled(cfg *config.Config, cwd, name string, disabled bool) error {
	srv, err := findManaged(cfg, cwd, name)
	if err != nil {
		return err
	}
	if path := owningJSONPath(cfg, cwd, srv); path != "" {
		return config.SetMCPJSONServerDisabled(path, name, disabled)
	}
	return mutateGlobalServer(cfg, name, func(s *config.MCPServerConfig) {
		s.Disabled = disabled
	})
}

// SetToolDisabled persists a per-tool switch into the owning file.
func SetToolDisabled(cfg *config.Config, cwd, name, tool string, disabled bool) error {
	srv, err := findManaged(cfg, cwd, name)
	if err != nil {
		return err
	}
	if path := owningJSONPath(cfg, cwd, srv); path != "" {
		return config.SetMCPJSONToolDisabled(path, name, tool, disabled)
	}
	return mutateGlobalServer(cfg, name, func(s *config.MCPServerConfig) {
		s.DisabledTools = config.SetToolDisabledList(s.DisabledTools, tool, disabled)
	})
}

// UpsertServer creates or updates one entry in the mcp.json file selected by
// scope: ScopeGlobal writes <home>/mcp.json, ScopeLocal writes
// <cwd>/.coddy/mcp.json.
func UpsertServer(cfg *config.Config, cwd, name, scope string, entry config.MCPJSONServer) error {
	switch scope {
	case ScopeLocal:
		if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), name, entry); err != nil {
			return err
		}
		// Writing a project entry through this API is the operator typing the
		// command themselves, which is exactly the decision the trust gate
		// asks for; recording it here avoids asking twice for the same thing.
		return approveOwnDeclaration(cfg, cwd, name)
	case ScopeGlobal:
		return config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(cfg.Paths.Home), name, entry)
	default:
		return fmt.Errorf("unknown mcp scope %q (use %q or %q)", scope, ScopeGlobal, ScopeLocal)
	}
}

// approveOwnDeclaration records trust for a project entry the operator just
// wrote, reading it back so the digest matches what the loader will produce.
// Under mcp.project_trust: deny nothing is recorded, because that policy has
// no approval path at all.
func approveOwnDeclaration(cfg *config.Config, cwd, name string) error {
	if cfg.MCP.ResolvedProjectTrust() != config.ProjectTrustAsk {
		return nil
	}
	path := config.MCPJSONPath(cwd)
	servers, err := config.LoadMCPJSONServers(path)
	if err != nil {
		return err
	}
	for _, srv := range servers {
		if srv.Name == name {
			return NewTrustStore(cfg.Paths.Home).Approve(cwd, path, srv)
		}
	}
	return fmt.Errorf("mcp server %q not found in %s after saving it", name, path)
}

// DeleteServer removes an mcp.json-defined server from its owning file.
// Config.yaml-defined servers are refused; they are edited via the config API.
func DeleteServer(cfg *config.Config, cwd, name string) error {
	srv, err := findManaged(cfg, cwd, name)
	if err != nil {
		return err
	}
	path := owningJSONPath(cfg, cwd, srv)
	if path == "" {
		return fmt.Errorf("mcp server %q is defined in config.yaml; edit mcp_servers there", name)
	}
	removed, err := config.DeleteMCPJSONServer(path, name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("mcp server %q not found in %s", name, path)
	}
	return nil
}

// SetProjectTrust persists the mcp.project_trust policy into config.yaml. It
// lives next to the other MCP switches so the management surface owns every
// MCP decision, instead of splitting one server list across two settings tabs.
func SetProjectTrust(cfg *config.Config, policy string) error {
	next := config.MCP{ProjectTrust: policy}
	if err := next.Validate(); err != nil {
		return err
	}
	cfg.MCP = next
	return persistConfigYAML(cfg)
}

// mutateGlobalServer edits one config.yaml server in memory and persists the
// whole config atomically (same flow as the skills source editor).
func mutateGlobalServer(cfg *config.Config, name string, mutate func(*config.MCPServerConfig)) error {
	found := false
	for i := range cfg.MCPServers {
		if cfg.MCPServers[i].Name == name {
			mutate(&cfg.MCPServers[i])
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("mcp server %q not found in config.yaml", name)
	}
	return persistConfigYAML(cfg)
}

// persistConfigYAML backs up and atomically rewrites config.yaml from cfg.
func persistConfigYAML(cfg *config.Config) error {
	path := cfg.Paths.ConfigPath
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path is empty")
	}
	data, err := config.MarshalConfigYAML(cfg)
	if err != nil {
		return err
	}
	if err := config.BackupCurrent(path); err != nil {
		return err
	}
	return config.AtomicWriteConfigYAML(path, data)
}

// ValidateServerName rejects names that break tool namespacing or lookups:
// "__" is the server/tool separator in namespaced tool names.
func ValidateServerName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("mcp server name is empty")
	}
	if strings.Contains(name, "__") {
		return fmt.Errorf("mcp server name must not contain %q", "__")
	}
	if strings.ContainsAny(name, " \t/\\") {
		return fmt.Errorf("mcp server name must not contain spaces or path separators")
	}
	return nil
}

// Probe connects to an MCP server over its configured transport, fetches its
// tool list, and closes the connection. It is used by the management API to
// show tools without a session.
func Probe(ctx context.Context, srv config.MCPServerConfig, cwd string, log *slog.Logger) ([]ToolInfo, error) {
	client, err := Connect(ctx, srv, cwd, log)
	if err != nil {
		return nil, err
	}
	tools := client.Tools()
	_ = client.Close()
	return tools, nil
}
