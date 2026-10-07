package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// ExpandMCPValue resolves one value of an MCP declaration (command,
// argument, environment value, URL, header) for the server it starts, the
// same way whichever mcp.json declared it: ${CWD} is the session workspace,
// ${NAME} or ${env:NAME} the process environment (unset reads as empty),
// ${NAME:-default} the default when the variable is unset or empty, "$${" a
// literal "${", and a leading ~ the user's home. Any other "$" is literal,
// so a password with a dollar sign in it needs no escaping. The declaration
// itself keeps the references - it is what the settings screen shows and
// what an approval digests - so a secret named this way never lands in the
// file, and an approval names the variables a declaration reads
// (MCPValueVariables).
func ExpandMCPValue(s, cwd string) string {
	return expandHome(walkMCPValue(s, func(ref string) string {
		if ref == "CWD" {
			return cwd
		}
		name, fallback, hasFallback := mcpVariable(ref)
		value := os.Getenv(name)
		if value == "" && hasFallback {
			return fallback
		}
		return value
	}))
}

// MCPValueVariables names, in order of appearance, the environment
// variables one value reads through ExpandMCPValue; ${CWD} and an escaped
// "$${" read none.
func MCPValueVariables(s string) []string {
	var names []string
	walkMCPValue(s, func(ref string) string {
		if ref != "CWD" {
			if name, _, _ := mcpVariable(ref); name != "" {
				names = append(names, name)
			}
		}
		return ""
	})
	return names
}

// walkMCPValue copies s with every ${...} reference replaced by what ref
// returns for its inside; "$${" is a literal "${", and an unclosed "${" is
// kept as it is.
func walkMCPValue(s string, ref func(string) string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "$${") {
			b.WriteString("${")
			i += 3
			continue
		}
		if !strings.HasPrefix(s[i:], "${") {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(ref(s[i+2 : i+2+end]))
		i += 2 + end + 1
	}
	return b.String()
}

// mcpVariable reads the inside of an environment reference: NAME,
// env:NAME, or either with ":-default".
func mcpVariable(ref string) (name, fallback string, hasFallback bool) {
	name, fallback, hasFallback = strings.Cut(ref, ":-")
	return strings.TrimPrefix(name, "env:"), fallback, hasFallback
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
	// the connector picks the streamable HTTP transport (with its legacy-SSE
	// fallback) instead of running an empty command.
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

// MCPServerFromJSON is the declaration one mcp.json entry stands for, exactly
// as LoadMCPJSONServers reads it, so a digest taken of it is the one the
// listing of that file reports.
func MCPServerFromJSON(name string, e MCPJSONServer) MCPServerConfig {
	return mcpJSONServerToConfig(name, e)
}

// MCPJSONFromServer is the mcp.json entry of a declaration, the inverse of
// what LoadMCPJSONServers reads: env and headers become objects, the name is
// the entry's key and is left out.
func MCPJSONFromServer(srv MCPServerConfig) MCPJSONServer {
	e := MCPJSONServer{
		Type:          strings.TrimSpace(srv.Type),
		Command:       srv.Command,
		Args:          append([]string(nil), srv.Args...),
		URL:           srv.URL,
		Disabled:      srv.Disabled,
		DisabledTools: append([]string(nil), srv.DisabledTools...),
	}
	if len(srv.Env) > 0 {
		e.Env = make(map[string]string, len(srv.Env))
		for _, v := range srv.Env {
			e.Env[v.Name] = v.Value
		}
	}
	if len(srv.Headers) > 0 {
		e.Headers = make(map[string]string, len(srv.Headers))
		for _, h := range srv.Headers {
			e.Headers[h.Name] = h.Value
		}
	}
	return e
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
	_, err := UpdateMCPJSONServer(path, name, func(MCPJSONServer, bool) (MCPJSONServer, error) {
		return srv, nil
	})
	return err
}

// UpdateMCPJSONServer changes one named entry of an mcp.json file in a single
// read-modify-write: change receives the entry the file stores (and whether
// it stores one) and returns the entry to write in its place, which is what
// UpdateMCPJSONServer returns. An error from change leaves the file as it was,
// so a check made against the stored entry and the write it allows read the
// same file.
func UpdateMCPJSONServer(path, name string, change func(stored MCPJSONServer, exists bool) (MCPJSONServer, error)) (MCPJSONServer, error) {
	entries, err := ReadMCPJSONFile(path)
	if err != nil {
		return MCPJSONServer{}, err
	}
	stored, exists := entries[name]
	next, err := change(stored, exists)
	if err != nil {
		return MCPJSONServer{}, err
	}
	entries[name] = next
	if err := writeMCPJSONFileEntries(path, entries); err != nil {
		return MCPJSONServer{}, err
	}
	return next, nil
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
// <home>/mcp.json < <cwd>/.coddy/mcp.json.
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
