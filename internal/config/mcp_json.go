package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// MCPJSONServer is one entry of a Cursor-compatible mcp.json file (global
// <home>/mcp.json or project <cwd>/.coddy/mcp.json). Env and Headers are JSON
// objects (name -> value), unlike the YAML list form.
type MCPJSONServer struct {
	Type          string            `json:"type,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Disabled      bool              `json:"disabled,omitempty"`
	DisabledTools []string          `json:"disabledTools,omitempty"`
}

// mcpJSONFile mirrors the Cursor mcp.json layout: a single "mcpServers"
// object keyed by server name.
type mcpJSONFile struct {
	MCPServers map[string]MCPJSONServer `json:"mcpServers"`
}

// MCPJSONPath returns the project-local (workspace) MCP config path under cwd.
func MCPJSONPath(cwd string) string {
	return filepath.Join(cwd, ".coddy", "mcp.json")
}

// GlobalMCPJSONPath returns the user-global MCP config path. home is the
// coddy state directory (~/.coddy), so the file sits at ~/.coddy/mcp.json —
// the analogue of Cursor's ~/.cursor/mcp.json.
func GlobalMCPJSONPath(home string) string {
	return filepath.Join(home, "mcp.json")
}

// ReadMCPJSONFile returns the raw named entries of an mcp.json file. A
// missing file is not an error and yields an empty map.
func ReadMCPJSONFile(path string) (map[string]MCPJSONServer, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path derives from home/cwd
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]MCPJSONServer{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var file mcpJSONFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if file.MCPServers == nil {
		file.MCPServers = map[string]MCPJSONServer{}
	}
	return file.MCPServers, nil
}

// LoadMCPJSONServers reads an mcp.json file and converts each entry to the
// YAML-config server shape. Entries are returned name-sorted so downstream
// merging stays deterministic.
func LoadMCPJSONServers(path string) ([]MCPServerConfig, error) {
	entries, err := ReadMCPJSONFile(path)
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
		servers = append(servers, mcpJSONServerToConfig(name, entries[name]))
	}
	return servers, nil
}

func mcpJSONServerToConfig(name string, e MCPJSONServer) MCPServerConfig {
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

func writeMCPJSONFileEntries(path string, entries map[string]MCPJSONServer) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(mcpJSONFile{MCPServers: entries}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o644)
}

// UpsertMCPJSONServer creates or replaces one named entry in an mcp.json file.
func UpsertMCPJSONServer(path, name string, srv MCPJSONServer) error {
	entries, err := ReadMCPJSONFile(path)
	if err != nil {
		return err
	}
	entries[name] = srv
	return writeMCPJSONFileEntries(path, entries)
}

// DeleteMCPJSONServer removes a named entry; reports whether it existed.
func DeleteMCPJSONServer(path, name string) (bool, error) {
	entries, err := ReadMCPJSONFile(path)
	if err != nil {
		return false, err
	}
	if _, ok := entries[name]; !ok {
		return false, nil
	}
	delete(entries, name)
	return true, writeMCPJSONFileEntries(path, entries)
}

// SetMCPJSONServerDisabled flips the disabled flag of an existing entry.
func SetMCPJSONServerDisabled(path, name string, disabled bool) error {
	entries, err := ReadMCPJSONFile(path)
	if err != nil {
		return err
	}
	e, ok := entries[name]
	if !ok {
		return fmt.Errorf("mcp server %q not found in %s", name, path)
	}
	e.Disabled = disabled
	entries[name] = e
	return writeMCPJSONFileEntries(path, entries)
}

// SetMCPJSONToolDisabled adds or removes a tool in an entry's disabledTools.
func SetMCPJSONToolDisabled(path, name, tool string, disabled bool) error {
	entries, err := ReadMCPJSONFile(path)
	if err != nil {
		return err
	}
	e, ok := entries[name]
	if !ok {
		return fmt.Errorf("mcp server %q not found in %s", name, path)
	}
	e.DisabledTools = SetToolDisabledList(e.DisabledTools, tool, disabled)
	entries[name] = e
	return writeMCPJSONFileEntries(path, entries)
}

// SetToolDisabledList adds or removes tool in a disabled-tools list, keeping
// the result sorted and free of duplicates.
func SetToolDisabledList(tools []string, tool string, disabled bool) []string {
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

// BuildMCPToolFilter compiles the disable switches of the effective server
// list into a predicate: false hides the tool from the agent. Servers absent
// from the list (e.g. supplied by an ACP client) stay fully allowed.
func BuildMCPToolFilter(servers []MCPServerConfig) func(server, tool string) bool {
	disabledServers := make(map[string]bool)
	disabledTools := make(map[string]map[string]bool)
	for _, s := range servers {
		if s.Disabled {
			disabledServers[s.Name] = true
		}
		if len(s.DisabledTools) > 0 {
			m := make(map[string]bool, len(s.DisabledTools))
			for _, t := range s.DisabledTools {
				m[t] = true
			}
			disabledTools[s.Name] = m
		}
	}
	return func(server, tool string) bool {
		if disabledServers[server] {
			return false
		}
		return !disabledTools[server][tool]
	}
}

// MergeMCPServers overlays higher-precedence servers onto base ones: an
// overlay entry with the same name replaces the base definition in place; new
// overlay entries append after the base list. Precedence chain:
// config.yaml < <home>/mcp.json < <cwd>/.coddy/mcp.json.
func MergeMCPServers(base, overlay []MCPServerConfig) []MCPServerConfig {
	overrides := make(map[string]MCPServerConfig, len(overlay))
	for _, srv := range overlay {
		overrides[srv.Name] = srv
	}
	merged := make([]MCPServerConfig, 0, len(base)+len(overlay))
	seen := make(map[string]bool, len(base))
	for _, srv := range base {
		if o, ok := overrides[srv.Name]; ok {
			merged = append(merged, o)
		} else {
			merged = append(merged, srv)
		}
		seen[srv.Name] = true
	}
	for _, srv := range overlay {
		if !seen[srv.Name] {
			merged = append(merged, srv)
		}
	}
	return merged
}
