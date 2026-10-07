// Management operations shared by the HTTP API and CLI: merged server list
// with scope/origin labels, enable/disable persistence into <home>/mcp.json
// or operator-owned project overrides, and mcp.json server CRUD. MCP servers
// come from two files only - <home>/mcp.json and the project's
// .coddy/mcp.json, the project winning a name - never from config.yaml.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Scopes reported by ListManagedServers (user-facing grouping).
const (
	ScopeGlobal = "global" // <home>/mcp.json
	ScopeLocal  = "local"  // <cwd>/.coddy/mcp.json
)

// Origins identify the file that owns a server definition.
const (
	OriginHome    = "home"    // <home>/mcp.json (global, Cursor-style)
	OriginProject = "project" // <cwd>/.coddy/mcp.json (project-local)
	// OriginClient marks a declaration an ACP client sent with session/new or
	// session/load: no file owns it and no trust gate decides on it.
	OriginClient = "client"
)

// ManagedServer is one merged server definition with its scope and origin.
type ManagedServer struct {
	Config config.MCPServerConfig
	Scope  string
	Origin string
}

// ListManagedServers merges the global and the project servers for cwd in
// that precedence order (the project overrides a name), labeling each entry
// with the file that owns its definition.
func ListManagedServers(cfg *config.Config, cwd string) ([]ManagedServer, error) {
	global, err := config.LoadMCPJSONServers(config.GlobalMCPJSONPath(cfg.Paths.Home))
	if err != nil {
		return nil, err
	}
	project, err := config.LoadMCPJSONServers(config.MCPJSONPath(cwd))
	if err != nil {
		return nil, err
	}
	return applyProjectSwitches(cfg.Paths.Home, cwd, mergeManaged(global, project))
}

// loadMCPJSONTolerant reads one mcp.json, logging and skipping a broken file.
func loadMCPJSONTolerant(path string, log *slog.Logger) []config.MCPServerConfig {
	servers, err := config.LoadMCPJSONServers(path)
	if err != nil {
		if log != nil {
			log.Warn("failed to load mcp.json", "path", path, "error", err)
		}
		return nil
	}
	return servers
}

// GlobalServers lists the servers of <home>/mcp.json, with no workspace's
// project file over them. A broken file is logged and skipped, as at session
// start.
func GlobalServers(cfg *config.Config, log *slog.Logger) []ManagedServer {
	return mergeManaged(loadMCPJSONTolerant(config.GlobalMCPJSONPath(cfg.Paths.Home), log), nil)
}

// ListManagedServersTolerant is ListManagedServers with a broken mcp.json
// logged and skipped instead of failing the whole list. Session bootstrap
// uses it so one unreadable file cannot stop a session from starting.
func ListManagedServersTolerant(cfg *config.Config, cwd string, log *slog.Logger) []ManagedServer {
	merged := mergeManaged(
		loadMCPJSONTolerant(config.GlobalMCPJSONPath(cfg.Paths.Home), log),
		loadMCPJSONTolerant(config.MCPJSONPath(cwd), log))
	servers, err := applyProjectSwitches(cfg.Paths.Home, cwd, merged)
	if err != nil {
		// The operator's switches for the project servers cannot be read, so
		// none of them is known to be on: they stay off until the file is
		// repaired. Global servers keep their own declarations and switches;
		// an empty list here would also have emptied the per-turn tool filter,
		// turning back on every tool the operator switched off.
		if log != nil {
			log.Warn("failed to load MCP overrides; project MCP servers stay off until it is repaired",
				"path", overridesPath(cfg.Paths.Home), "error", err)
		}
		for i := range merged {
			if merged[i].Origin == OriginProject {
				merged[i].Config.Disabled = true
			}
		}
		return merged
	}
	return servers
}

// mergeManaged overlays the project's mcp.json onto the global one and labels
// every merged entry with the file that owns its definition.
func mergeManaged(global, project []config.MCPServerConfig) []ManagedServer {
	origins := make(map[string]string, len(global)+len(project))
	for _, srv := range global {
		origins[srv.Name] = OriginHome
	}
	for _, srv := range project {
		origins[srv.Name] = OriginProject
	}
	merged := config.MergeMCPServers(global, project)
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

// owningJSONPath returns the mcp.json path that defines srv, or "" when no
// file does (a declaration an ACP client sent).
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

// SetServerDisabled persists the server-level switch outside the checkout for
// project entries, or into the owning file for global entries.
func SetServerDisabled(cfg *config.Config, cwd, name string, disabled bool) error {
	srv, err := findManaged(cfg, cwd, name)
	if err != nil {
		return err
	}
	if srv.Origin == OriginProject {
		return updateProjectSwitch(cfg, cwd, name, func(s *projectSwitches) { s.Disabled = &disabled })
	}
	if path := owningJSONPath(cfg, cwd, srv); path != "" {
		return config.SetMCPJSONServerDisabled(path, name, disabled)
	}
	return fmt.Errorf("mcp server %q is not declared in an mcp.json file", name)
}

// SetToolDisabled persists a per-tool switch using the same scope rule.
func SetToolDisabled(cfg *config.Config, cwd, name, tool string, disabled bool) error {
	srv, err := findManaged(cfg, cwd, name)
	if err != nil {
		return err
	}
	if srv.Origin == OriginProject {
		return updateProjectSwitch(cfg, cwd, name, func(s *projectSwitches) {
			if s.DisabledTools == nil {
				s.DisabledTools = make(map[string]bool)
			}
			s.DisabledTools[tool] = disabled
		})
	}
	if path := owningJSONPath(cfg, cwd, srv); path != "" {
		return config.SetMCPJSONToolDisabled(path, name, tool, disabled)
	}
	return fmt.Errorf("mcp server %q is not declared in an mcp.json file", name)
}

// SaveServer writes one entry into the mcp.json file scope selects -
// ScopeGlobal writes <home>/mcp.json, ScopeLocal <cwd>/.coddy/mcp.json -
// replacing the entry of that name, in one read-modify-write of the file.
//
// The list never shows an env or header value (it shows config.RedactedValue
// in its place), so a value spelled that way keeps the one the file stores
// for that name; a key left out is removed, as the entry is replaced whole.
// shown is the fingerprint of the declaration the client was shown: when it
// is set and the stored entry is no longer that declaration, nothing is
// written (ErrDeclarationChanged). A project entry keeps values only against
// it, so a value the checkout put in the file after the listing is never
// written back and approved by a save nobody saw it in.
//
// A project entry saved under mcp.project_trust ask is approved as written:
// the operator typing the entry is the decision the trust gate asks for.
func SaveServer(cfg *config.Config, cwd, name, scope string, entry config.MCPJSONServer, shown string) error {
	var path string
	switch scope {
	case ScopeLocal:
		path = config.MCPJSONPath(cwd)
	case ScopeGlobal:
		path = config.GlobalMCPJSONPath(cfg.Paths.Home)
	default:
		return fmt.Errorf("unknown mcp scope %q (use %q or %q)", scope, ScopeGlobal, ScopeLocal)
	}
	written, err := config.UpdateMCPJSONServer(path, name, func(stored config.MCPJSONServer, exists bool) (config.MCPJSONServer, error) {
		if shown != "" && (!exists || Fingerprint(config.MCPServerFromJSON(name, stored)) != shown) {
			return config.MCPJSONServer{}, fmt.Errorf("mcp %s: %w; review it and save again", name, ErrDeclarationChanged)
		}
		next, kept, err := keepRedactedValues(name, entry, stored)
		if err != nil {
			return config.MCPJSONServer{}, err
		}
		if kept && scope == ScopeLocal && shown == "" {
			return config.MCPJSONServer{}, fmt.Errorf("mcp server %q: keeping a stored value of a project entry needs the fingerprint the list showed for it", name)
		}
		return next, nil
	})
	if err != nil {
		return err
	}
	if scope == ScopeLocal {
		return approveWritten(cfg, cwd, path, name, written)
	}
	return nil
}

// keepRedactedValues fills every env or header value of entry spelled
// config.RedactedValue with the value stored holds for that name, and reports
// whether it kept any. A placeholder for a name stored has no value for is an
// error naming it: that value was never in the file, so it has to be typed.
func keepRedactedValues(name string, entry, stored config.MCPJSONServer) (config.MCPJSONServer, bool, error) {
	kept := false
	fill := func(kind string, values, from map[string]string) (map[string]string, error) {
		if len(values) == 0 {
			return values, nil
		}
		out := make(map[string]string, len(values))
		for key, value := range values {
			if value != config.RedactedValue {
				out[key] = value
				continue
			}
			old, ok := from[key]
			if !ok {
				return nil, fmt.Errorf("mcp server %q: %s %s is %s, but the file stores no value for it; type the value", name, kind, key, config.RedactedValue)
			}
			out[key] = old
			kept = true
		}
		return out, nil
	}
	env, err := fill("env", entry.Env, stored.Env)
	if err != nil {
		return config.MCPJSONServer{}, false, err
	}
	headers, err := fill("header", entry.Headers, stored.Headers)
	if err != nil {
		return config.MCPJSONServer{}, false, err
	}
	entry.Env, entry.Headers = env, headers
	return entry, kept, nil
}

// approveWritten records trust for the project entry a save just wrote: the
// entry itself, not the file read again, which the checkout may have changed
// since. Under mcp.project_trust allow or deny nothing is recorded: neither
// has an approval to record.
func approveWritten(cfg *config.Config, cwd, path, name string, written config.MCPJSONServer) error {
	if cfg.MCP.ResolvedProjectTrust() != config.ProjectTrustAsk {
		return nil
	}
	return NewTrustStore(cfg.Paths.Home).Approve(cwd, path, config.MCPServerFromJSON(name, written))
}

// DeleteServer removes a server from the mcp.json file that declares it.
func DeleteServer(cfg *config.Config, cwd, name string) error {
	srv, err := findManaged(cfg, cwd, name)
	if err != nil {
		return err
	}
	path := owningJSONPath(cfg, cwd, srv)
	if path == "" {
		return fmt.Errorf("mcp server %q is not declared in an mcp.json file", name)
	}
	removed, err := config.DeleteMCPJSONServer(path, name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("mcp server %q not found in %s", name, path)
	}
	if srv.Origin == OriginProject {
		// The switches belong to the declaration that went, so a later server
		// of the same name starts from its own declaration. They are dropped
		// after it, never before: a delete that fails must leave a server the
		// operator switched off switched off. Switches the home cannot drop
		// name a server no longer declared and start nothing, so the delete
		// stands and the leftover is logged.
		if err := dropProjectSwitches(cfg, cwd, name); err != nil {
			slog.Warn("mcp server deleted; its switches stay in the overrides file",
				"name", name, "workspace", CanonicalWorkspace(cwd),
				"file", overridesPath(cfg.Paths.Home), "error", err)
		}
	}
	return nil
}

// SetProjectTrust persists the mcp.project_trust policy into config.yaml. It
// lives next to the other MCP switches so the management surface owns every
// MCP decision, instead of splitting one server list across two settings tabs.
func SetProjectTrust(cfg *config.Config, policy string) error {
	check := config.MCP{ProjectTrust: policy}
	if err := check.Validate(); err != nil {
		return err
	}
	// Read-modify-write under the config file lock: apply the trust change to
	// a fresh on-disk config, then mirror it back.
	// Only the policy moves; the other mcp settings stay as the file has them.
	return config.WithConfigFileLock(func() error {
		fresh, err := freshGlobalConfig(cfg)
		if err != nil {
			return err
		}
		fresh.MCP.ProjectTrust = check.ProjectTrust
		if err := persistConfigYAML(fresh); err != nil {
			return err
		}
		cfg.MCP.ProjectTrust = check.ProjectTrust
		return nil
	})
}

// freshGlobalConfig re-reads the active YAML so a mutation applies to the
// latest on-disk state. Callers hold the config file lock. A missing file
// falls back to the caller's config (it will be created by the persist).
func freshGlobalConfig(cfg *config.Config) (*config.Config, error) {
	if strings.TrimSpace(cfg.Paths.ConfigPath) == "" {
		return cfg, nil
	}
	reloaded, err := config.LoadWithPaths(cfg.Paths)
	switch {
	case err == nil && reloaded != nil:
		return reloaded, nil
	case errors.Is(err, os.ErrNotExist):
		return cfg, nil
	default:
		return nil, fmt.Errorf("reload config before mcp change: %w", err)
	}
}

// persistConfigYAML backs up and atomically rewrites config.yaml from cfg.
func persistConfigYAML(cfg *config.Config) error {
	path := cfg.Paths.ConfigPath
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path is empty")
	}
	data, err := config.MarshalConfigYAMLForFile(cfg, path)
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
