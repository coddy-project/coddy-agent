package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ProjectMCPServer is one entry of the Cursor-compatible .coddy/mcp.json file.
// Env and Headers are JSON objects (name -> value), unlike the YAML list form.
type ProjectMCPServer struct {
	Type          string            `json:"type,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Disabled      bool              `json:"disabled,omitempty"`
	DisabledTools []string          `json:"disabledTools,omitempty"`
}

// projectMCPFile mirrors the Cursor mcp.json layout: a single "mcpServers"
// object keyed by server name.
type projectMCPFile struct {
	MCPServers map[string]ProjectMCPServer `json:"mcpServers"`
}

// MCPJSONPath returns the project-local MCP config path under cwd.
func MCPJSONPath(cwd string) string {
	return filepath.Join(cwd, ".coddy", "mcp.json")
}

// ReadProjectMCPFile returns the raw named entries of .coddy/mcp.json. A
// missing file is not an error and yields an empty map.
func ReadProjectMCPFile(cwd string) (map[string]ProjectMCPServer, error) {
	path := MCPJSONPath(cwd)
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from the session cwd
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]ProjectMCPServer{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var file projectMCPFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if file.MCPServers == nil {
		file.MCPServers = map[string]ProjectMCPServer{}
	}
	return file.MCPServers, nil
}

// LoadProjectMCPServers reads .coddy/mcp.json under cwd and converts each
// entry to the YAML-config server shape. Entries are returned name-sorted so
// downstream merging stays deterministic.
func LoadProjectMCPServers(cwd string) ([]MCPServerConfig, error) {
	entries, err := ReadProjectMCPFile(cwd)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]MCPServerConfig, 0, len(entries))
	for _, name := range names {
		servers = append(servers, projectServerToConfig(name, entries[name]))
	}
	return servers, nil
}

func projectServerToConfig(name string, e ProjectMCPServer) MCPServerConfig {
	srv := MCPServerConfig{
		Type:          e.Type,
		Name:          name,
		Command:       e.Command,
		Args:          append([]string(nil), e.Args...),
		URL:           e.URL,
		Disabled:      e.Disabled,
		DisabledTools: append([]string(nil), e.DisabledTools...),
	}
	// Cursor url-only entries carry no explicit type; surface them as http so
	// the stdio-only connector can reject them gracefully instead of running
	// an empty command.
	if srv.Type == "" && srv.Command == "" && srv.URL != "" {
		srv.Type = "http"
	}
	srv.Env = make([]EnvVarConfig, 0, len(e.Env))
	for _, name := range sortedKeys(e.Env) {
		srv.Env = append(srv.Env, EnvVarConfig{Name: name, Value: e.Env[name]})
	}
	srv.Headers = make([]HTTPHeaderConfig, 0, len(e.Headers))
	for _, name := range sortedKeys(e.Headers) {
		srv.Headers = append(srv.Headers, HTTPHeaderConfig{Name: name, Value: e.Headers[name]})
	}
	return srv
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writeProjectMCPFileEntries(cwd string, entries map[string]ProjectMCPServer) error {
	path := MCPJSONPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(projectMCPFile{MCPServers: entries}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o644)
}

// UpsertProjectMCPServer creates or replaces one named entry in .coddy/mcp.json.
func UpsertProjectMCPServer(cwd, name string, srv ProjectMCPServer) error {
	entries, err := ReadProjectMCPFile(cwd)
	if err != nil {
		return err
	}
	entries[name] = srv
	return writeProjectMCPFileEntries(cwd, entries)
}

// DeleteProjectMCPServer removes a named entry; reports whether it existed.
func DeleteProjectMCPServer(cwd, name string) (bool, error) {
	entries, err := ReadProjectMCPFile(cwd)
	if err != nil {
		return false, err
	}
	if _, ok := entries[name]; !ok {
		return false, nil
	}
	delete(entries, name)
	return true, writeProjectMCPFileEntries(cwd, entries)
}

// SetProjectMCPServerDisabled flips the disabled flag of an existing entry.
func SetProjectMCPServerDisabled(cwd, name string, disabled bool) error {
	entries, err := ReadProjectMCPFile(cwd)
	if err != nil {
		return err
	}
	e, ok := entries[name]
	if !ok {
		return fmt.Errorf("mcp server %q not found in %s", name, MCPJSONPath(cwd))
	}
	e.Disabled = disabled
	entries[name] = e
	return writeProjectMCPFileEntries(cwd, entries)
}

// SetProjectMCPToolDisabled adds or removes a tool in an entry's disabledTools.
func SetProjectMCPToolDisabled(cwd, name, tool string, disabled bool) error {
	entries, err := ReadProjectMCPFile(cwd)
	if err != nil {
		return err
	}
	e, ok := entries[name]
	if !ok {
		return fmt.Errorf("mcp server %q not found in %s", name, MCPJSONPath(cwd))
	}
	e.DisabledTools = setToolDisabled(e.DisabledTools, tool, disabled)
	entries[name] = e
	return writeProjectMCPFileEntries(cwd, entries)
}

func setToolDisabled(tools []string, tool string, disabled bool) []string {
	out := make([]string, 0, len(tools)+1)
	for _, t := range tools {
		if t != tool {
			out = append(out, t)
		}
	}
	if disabled {
		out = append(out, tool)
		sort.Strings(out)
	}
	return out
}

// MergeMCPServers overlays project servers onto global ones: a project entry
// with the same name replaces the global definition in place; new project
// entries append after the global list.
func MergeMCPServers(global, project []MCPServerConfig) []MCPServerConfig {
	overrides := make(map[string]MCPServerConfig, len(project))
	for _, srv := range project {
		overrides[srv.Name] = srv
	}
	merged := make([]MCPServerConfig, 0, len(global)+len(project))
	seen := make(map[string]bool, len(global))
	for _, srv := range global {
		if o, ok := overrides[srv.Name]; ok {
			merged = append(merged, o)
		} else {
			merged = append(merged, srv)
		}
		seen[srv.Name] = true
	}
	for _, srv := range project {
		if !seen[srv.Name] {
			merged = append(merged, srv)
		}
	}
	return merged
}
