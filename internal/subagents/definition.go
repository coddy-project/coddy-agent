// Package subagents loads subagent definitions - markdown files with YAML
// frontmatter whose body is the role a child agent plays - decides how far a
// definition may be trusted, bounds how many children run at once, and renders
// the catalog the parent model and the operator read.
//
// The package knows nothing about sessions or the ReAct loop: it produces
// immutable Definition values and pure decisions (trust state, effective tool
// set, permission narrowing, timeout) that internal/agent applies.
package subagents

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Scope tells where a definition came from, which decides how it is trusted.
type Scope string

const (
	// ScopeBuiltin is a definition embedded in the binary.
	ScopeBuiltin Scope = "builtin"
	// ScopeUser is a file outside the workspace, typically ${CODDY_HOME}/agents:
	// the operator's own.
	ScopeUser Scope = "user"
	// ScopeProject is a file inside the workspace: it arrived with the checkout
	// and follows subagents.project_trust.
	ScopeProject Scope = "project"
)

// Permission mode values a definition may request. They mirror
// config.PermMode* without importing config, so this package stays below it.
const (
	PermissionAsk         = "ask"
	PermissionAcceptEdits = "accept_edits"
	PermissionBypass      = "bypass"
)

// Bounds keep a definition tree from flooding the prompt or the loader.
const (
	// MaxDefinitionsPerDir is how many files one directory may contribute.
	MaxDefinitionsPerDir = 200
	// MaxFileBytes is the largest definition file the loader reads.
	MaxFileBytes = 256 * 1024
	// MaxRoleBytes is how much of the body becomes the child's role.
	MaxRoleBytes = 64 * 1024
	// MaxDescriptionRunes bounds the one-liner shown in the catalog.
	MaxDescriptionRunes = 200
	// MaxPromptBytes bounds the task text a spawn_agent call may carry.
	MaxPromptBytes = 32 * 1024
)

// roleTruncatedMarker ends a role body the loader had to cut.
const roleTruncatedMarker = "\n\n[role truncated: the definition body exceeded 64 KiB]"

// DirectoryFileName is the file a per-agent directory holds
// (<dir>/<name>/AGENT.md); the directory name is the agent name.
const DirectoryFileName = "AGENT.md"

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Definition is one loaded subagent. It is immutable once returned by the
// loader: trust, tool set and role decisions all read the same value the file
// was parsed into, never the file again.
type Definition struct {
	Name        string
	Description string
	// Model is a models[].model id, or empty to inherit the parent's model.
	Model string
	// Mode is "agent", "plan", or empty to inherit the parent's mode.
	Mode string
	// Tools is an allowlist of tool names or prefix* patterns; empty means
	// everything the parent has.
	Tools []string
	// DisallowedTools is a denylist applied after Tools.
	DisallowedTools []string
	// PermissionMode is the requested mode (ask, accept_edits, bypass) or
	// empty to inherit; it can only narrow the parent's mode.
	PermissionMode string
	// MaxTurns caps the child's ReAct rounds; 0 uses the configured default.
	MaxTurns int
	// TimeoutSeconds is the definition's own run limit; 0 defers to the call
	// and the configured default.
	TimeoutSeconds int
	// Background forces detached runs.
	Background bool
	// Hidden keeps the definition out of the model-facing catalog.
	Hidden bool
	// Role is the body of the file: the child's role block.
	Role string

	Scope   Scope
	Builtin bool
	// Path is the source file (empty for built-ins).
	Path string
	// Digest is the SHA-256 of the file bytes; trust receipts bind to it.
	Digest string
}

// frontmatter is the YAML header of a definition file. Aliases used by other
// agents (Claude Code camelCase) are accepted so their files load unchanged.
type frontmatter struct {
	Name            string      `yaml:"name"`
	Description     string      `yaml:"description"`
	Model           string      `yaml:"model"`
	Mode            string      `yaml:"mode"`
	Tools           interface{} `yaml:"tools"`
	DisallowedTools interface{} `yaml:"disallowed_tools"`
	DisallowedAlias interface{} `yaml:"disallowedTools"`
	PermissionMode  string      `yaml:"permission_mode"`
	PermissionAlias string      `yaml:"permissionMode"`
	MaxTurns        int         `yaml:"max_turns"`
	MaxTurnsAlias   int         `yaml:"maxTurns"`
	TimeoutSeconds  int         `yaml:"timeout_seconds"`
	Background      bool        `yaml:"background"`
	Hidden          bool        `yaml:"hidden"`
}

// Parse turns a definition file into a Definition. path decides the default
// name: the file stem, or the directory name for <dir>/AGENT.md. The digest
// covers the exact bytes given, so an edited file never matches an old receipt.
func Parse(path string, data []byte) (*Definition, error) {
	body, fm, ok := splitFrontmatter(data)
	if !ok {
		return nil, fmt.Errorf("%s: missing YAML frontmatter", path)
	}
	var meta frontmatter
	if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
		return nil, fmt.Errorf("%s: frontmatter: %w", path, err)
	}

	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = defaultName(path)
	}
	if !namePattern.MatchString(name) {
		return nil, fmt.Errorf("%s: invalid subagent name %q (use lowercase letters, digits, hyphen, underscore)", path, name)
	}
	description := strings.TrimSpace(meta.Description)
	if description == "" {
		return nil, fmt.Errorf("%s: description is required", path)
	}
	if r := []rune(description); len(r) > MaxDescriptionRunes {
		description = string(r[:MaxDescriptionRunes-1]) + "…"
	}

	mode := strings.ToLower(strings.TrimSpace(meta.Mode))
	switch mode {
	case "", "agent", "plan":
	default:
		return nil, fmt.Errorf("%s: mode must be agent or plan, got %q", path, meta.Mode)
	}

	permRaw := strings.TrimSpace(meta.PermissionMode)
	if permRaw == "" {
		permRaw = strings.TrimSpace(meta.PermissionAlias)
	}
	perm := ""
	if permRaw != "" {
		normalized, known := NormalizePermissionMode(permRaw)
		if !known {
			return nil, fmt.Errorf("%s: unknown permission_mode %q", path, permRaw)
		}
		perm = normalized
	}

	maxTurns := meta.MaxTurns
	if maxTurns <= 0 {
		maxTurns = meta.MaxTurnsAlias
	}
	if maxTurns < 0 {
		maxTurns = 0
	}
	timeout := meta.TimeoutSeconds
	if timeout < 0 {
		timeout = 0
	}

	denied := stringList(meta.DisallowedTools)
	if len(denied) == 0 {
		denied = stringList(meta.DisallowedAlias)
	}

	role := strings.TrimSpace(body)
	if len(role) > MaxRoleBytes {
		role = strings.TrimSpace(role[:MaxRoleBytes]) + roleTruncatedMarker
	}

	sum := sha256.Sum256(data)
	return &Definition{
		Name:            name,
		Description:     description,
		Model:           strings.TrimSpace(meta.Model),
		Mode:            mode,
		Tools:           stringList(meta.Tools),
		DisallowedTools: denied,
		PermissionMode:  perm,
		MaxTurns:        maxTurns,
		TimeoutSeconds:  timeout,
		Background:      meta.Background,
		Hidden:          meta.Hidden,
		Role:            role,
		Path:            path,
		Digest:          hex.EncodeToString(sum[:]),
	}, nil
}

// defaultName derives the agent name from its path: the directory for an
// AGENT.md, otherwise the file stem.
func defaultName(path string) string {
	base := filepath.Base(path)
	if strings.EqualFold(base, DirectoryFileName) {
		return strings.ToLower(filepath.Base(filepath.Dir(path)))
	}
	return strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
}

// splitFrontmatter separates the --- delimited YAML header from the body.
func splitFrontmatter(data []byte) (body, fm string, ok bool) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), MaxFileBytes+1)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", false
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", "", false
	}
	return strings.Join(lines[end+1:], "\n"), strings.Join(lines[1:end], "\n"), true
}

// stringList accepts a YAML list or a comma-separated string (Claude Code
// writes `tools: Read, Grep`) and returns trimmed, non-empty entries.
func stringList(v interface{}) []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		for _, part := range strings.Split(t, ",") {
			add(part)
		}
	case []interface{}:
		for _, item := range t {
			if s, ok := item.(string); ok {
				add(s)
			} else if item != nil {
				add(fmt.Sprint(item))
			}
		}
	default:
		add(fmt.Sprint(t))
	}
	return out
}

// Allows reports whether the definition's own allowlist and denylist admit a
// tool name. It does not know the parent's set; EffectiveTools intersects
// both.
func (d *Definition) Allows(tool string) bool {
	if d == nil {
		return true
	}
	for _, p := range d.DisallowedTools {
		if MatchTool(p, tool) {
			return false
		}
	}
	if len(d.Tools) == 0 {
		return true
	}
	for _, p := range d.Tools {
		if MatchTool(p, tool) {
			return true
		}
	}
	return false
}

// MatchTool matches a tool name against a pattern: an exact name, a bare *,
// or a prefix ending in * (context7__* admits every tool of that MCP server).
func MatchTool(pattern, name string) bool {
	pattern = strings.TrimSpace(pattern)
	name = strings.TrimSpace(name)
	if pattern == "" || name == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == name
}
