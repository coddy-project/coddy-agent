// Management operations shared by the HTTP API and CLI: merged server list
// with source labels, enable/disable persistence into the owning file
// (config.yaml or .coddy/mcp.json), and project server CRUD.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Server sources reported by ListManagedServers.
const (
	SourceConfig  = "config"  // defined in config.yaml mcp_servers
	SourceProject = "project" // defined in (or overridden by) .coddy/mcp.json
)

// ManagedServer is one merged server definition with its origin.
type ManagedServer struct {
	Config config.MCPServerConfig
	Source string
}

// ListManagedServers merges global and project servers for cwd, labeling each
// entry with the file that owns its definition. A project entry overriding a
// config-defined name owns the definition and is labeled project.
func ListManagedServers(cfg *config.Config, cwd string) ([]ManagedServer, error) {
	project, err := config.LoadProjectMCPServers(cwd)
	if err != nil {
		return nil, err
	}
	projectNames := make(map[string]bool, len(project))
	for _, srv := range project {
		projectNames[srv.Name] = true
	}
	merged := config.MergeMCPServers(cfg.MCPServers, project)
	out := make([]ManagedServer, 0, len(merged))
	for _, srv := range merged {
		source := SourceConfig
		if projectNames[srv.Name] {
			source = SourceProject
		}
		out = append(out, ManagedServer{Config: srv, Source: source})
	}
	return out, nil
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

// SetServerDisabled persists the server-level switch into the owning file.
func SetServerDisabled(cfg *config.Config, cwd, name string, disabled bool) error {
	srv, err := findManaged(cfg, cwd, name)
	if err != nil {
		return err
	}
	if srv.Source == SourceProject {
		return config.SetProjectMCPServerDisabled(cwd, name, disabled)
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
	if srv.Source == SourceProject {
		return config.SetProjectMCPToolDisabled(cwd, name, tool, disabled)
	}
	return mutateGlobalServer(cfg, name, func(s *config.MCPServerConfig) {
		s.DisabledTools = config.SetToolDisabledList(s.DisabledTools, tool, disabled)
	})
}

// DeleteServer removes a project-defined server from .coddy/mcp.json.
// Config-defined servers are refused; they are edited via the config API.
func DeleteServer(cfg *config.Config, cwd, name string) error {
	srv, err := findManaged(cfg, cwd, name)
	if err != nil {
		return err
	}
	if srv.Source != SourceProject {
		return fmt.Errorf("mcp server %q is defined in config.yaml; edit mcp_servers there", name)
	}
	removed, err := config.DeleteProjectMCPServer(cwd, name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("mcp server %q not found in %s", name, config.MCPJSONPath(cwd))
	}
	return nil
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

// Probe connects to a stdio MCP server, fetches its tool list, and closes the
// connection. It is used by the management API to show tools without a session.
func Probe(ctx context.Context, srv config.MCPServerConfig, cwd string, log *slog.Logger) ([]ToolInfo, error) {
	if srv.Type != "" && srv.Type != "stdio" {
		return nil, fmt.Errorf("unsupported MCP transport: %s", srv.Type)
	}
	args := make([]string, len(srv.Args))
	for i, a := range srv.Args {
		args[i] = config.ExpandCWD(a, cwd)
	}
	env := make([]string, len(srv.Env))
	for i, e := range srv.Env {
		env[i] = e.Name + "=" + config.ExpandCWD(e.Value, cwd)
	}
	client, err := NewStdioClient(ctx, srv.Name, srv.Command, args, env, log)
	if err != nil {
		return nil, err
	}
	tools := client.Tools()
	_ = client.Close()
	return tools, nil
}
