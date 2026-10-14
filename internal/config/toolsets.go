package config

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Toolset is a named preset of tool lists (toolsets[]). A models[] row names
// one as the set a session on that model is offered (models[].toolset), and
// the operator may pick another for one session with /toolset <name>, which
// the session keeps (selectedToolset).
// The point is context, not permission: a small-window local model
// that a coding session only ever asks for a dozen tools is not sent the
// schemas of the other forty, while a model without a toolset keeps every tool.
//
// The lists take the syntax and the matching of a subagent definition's tools
// and disallowed_tools (subagents.AllowsTool): an exact name, a bare * or a
// prefix* pattern, MCP tools as server__tool. Tools is an allowlist (empty
// admits everything), DisallowedTools a denylist applied after it. They only
// narrow what the mode, the configuration and a subagent definition offer.
type Toolset struct {
	Name            string   `yaml:"name"`
	Tools           []string `yaml:"tools,omitempty"`
	DisallowedTools []string `yaml:"disallowed_tools,omitempty"`
}

// ToolsetDefault is the session choice that applies no preset: the session is
// offered every tool its mode offers, whatever toolset its model names. It is
// the name of no preset.
const ToolsetDefault = "default"

// toolsetNameRE is what a preset may be called: it is typed after /toolset,
// so no spaces and nothing a command line would split.
var toolsetNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Normalize trims the name and every entry, dropping the blank ones; a list of
// blanks reads as no restriction, the same as an absent key.
func (t *Toolset) Normalize() {
	t.Name = strings.TrimSpace(t.Name)
	t.Tools = trimToolPatterns(t.Tools)
	t.DisallowedTools = trimToolPatterns(t.DisallowedTools)
}

func trimToolPatterns(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Restricts reports whether the preset narrows anything: one with both lists
// empty offers every tool, like no preset at all.
func (t *Toolset) Restricts() bool {
	return t != nil && (len(t.Tools) > 0 || len(t.DisallowedTools) > 0)
}

// FindToolset returns the toolsets[] preset with this name, or nil.
func (c *Config) FindToolset(name string) *Toolset {
	if c == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	for i := range c.Toolsets {
		if c.Toolsets[i].Name == name {
			return &c.Toolsets[i]
		}
	}
	return nil
}

// ToolsetNames lists the presets by name, in the order of the file: what a
// session may pick besides default. Empty when there is none, and a surface
// then shows no toolset selector at all.
func (c *Config) ToolsetNames() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Toolsets))
	for _, t := range c.Toolsets {
		out = append(out, t.Name)
	}
	return out
}

// ValidToolsetChoice reports whether a session may pick choice: default or a
// preset of this configuration.
func (c *Config) ValidToolsetChoice(choice string) bool {
	choice = strings.TrimSpace(choice)
	return choice == ToolsetDefault || c.FindToolset(choice) != nil
}

// ResolveToolset is the preset a session on modelID is offered its tools by
// when it picked choice: none for default, a preset by name, and for no choice
// ("") the toolset the model names. A name this configuration no longer has
// is no choice, as a reasoning level the model does not offer is. Nil means
// no restriction.
func (c *Config) ResolveToolset(modelID, choice string) *Toolset {
	if c == nil {
		return nil
	}
	switch choice = strings.TrimSpace(choice); choice {
	case ToolsetDefault:
		return nil
	case "":
	default:
		if t := c.FindToolset(choice); t != nil {
			return restricting(t)
		}
	}
	m := c.FindModelEntry(modelID)
	if m == nil || m.Toolset == "" {
		return nil
	}
	return restricting(c.FindToolset(m.Toolset))
}

func restricting(t *Toolset) *Toolset {
	if !t.Restricts() {
		return nil
	}
	return t
}

// validateToolsets checks the presets and the models that name one: a valid,
// unique name other than default, and a models[].toolset that is a preset of
// this configuration. A typo there would otherwise offer a small model every
// tool without a word.
func (c *Config) validateToolsets() error {
	seen := make(map[string]struct{}, len(c.Toolsets))
	for i := range c.Toolsets {
		t := &c.Toolsets[i]
		t.Normalize()
		switch {
		case t.Name == "":
			return fmt.Errorf("toolsets[%d]: name is required", i)
		case t.Name == ToolsetDefault:
			return fmt.Errorf("toolsets[%s]: %q is reserved: a session picks it to be offered every tool the mode offers", t.Name, t.Name)
		case !toolsetNameRE.MatchString(t.Name):
			return fmt.Errorf("toolsets[%s]: a name is letters, digits, '.', '_' and '-', starting with a letter or a digit", t.Name)
		}
		if _, dup := seen[t.Name]; dup {
			return fmt.Errorf("toolsets: duplicate name %q", t.Name)
		}
		seen[t.Name] = struct{}{}
	}
	for i := range c.Models {
		m := &c.Models[i]
		if m.Toolset == "" {
			continue
		}
		if _, ok := seen[m.Toolset]; !ok {
			return fmt.Errorf("models[%s].toolset %q: not found in toolsets (configured: %s); leave the key out to offer every tool", m.Model, m.Toolset, strings.Join(c.ToolsetNames(), ", "))
		}
	}
	return nil
}

// The lists of a toolset name tools, and the registry that knows them sits
// above this package (internal/tools imports config). The binary (cmd/coddy)
// hands the tools package's names down at start-up, the way the mention
// package takes its URL fetcher, so the config check can say which entry
// names nothing without this package importing upward. Until a catalog is
// registered nothing is reported: an unknown name is only ever a warning.
var (
	toolCatalogMu sync.RWMutex
	toolCatalog   func() []string
)

// RegisterToolCatalog installs the function that lists every tool name a
// build can offer. The last registration wins; nil removes it.
func RegisterToolCatalog(names func() []string) {
	toolCatalogMu.Lock()
	toolCatalog = names
	toolCatalogMu.Unlock()
}

func registeredToolCatalog() func() []string {
	toolCatalogMu.RLock()
	defer toolCatalogMu.RUnlock()
	return toolCatalog
}

// UnknownToolsetTool is an entry of a toolsets[] list that matches no tool.
type UnknownToolsetTool struct {
	// Toolset is the toolsets[].name the entry sits on.
	Toolset string
	// Key is the list the entry is in: tools or disallowed_tools.
	Key string
	// Name is the entry as written.
	Name string
}

// Path is the list's config path, in the selector form the config check
// places a finding with.
func (u UnknownToolsetTool) Path() string { return "toolsets[" + u.Toolset + "]." + u.Key }

// Message says what is wrong with the entry.
func (u UnknownToolsetTool) Message() string {
	return strconv.Quote(u.Name) + " matches no tool this build offers, so the entry changes nothing"
}

// UnknownToolsetTools lists the entries of toolsets[].tools and
// toolsets[].disallowed_tools that match no tool name. It is a hint, never an
// error: a build omits tools its tags leave out (the scheduler's, for one),
// and an MCP tool (server__tool) exists only while its server is connected,
// so a name or a pattern with a double underscore is taken on trust.
func (c *Config) UnknownToolsetTools() []UnknownToolsetTool {
	if c == nil {
		return nil
	}
	catalog := registeredToolCatalog()
	if catalog == nil {
		return nil
	}
	known := catalog()
	if len(known) == 0 {
		return nil
	}
	var out []UnknownToolsetTool
	for i := range c.Toolsets {
		t := &c.Toolsets[i]
		for _, list := range []struct {
			key   string
			names []string
		}{{"tools", t.Tools}, {"disallowed_tools", t.DisallowedTools}} {
			for _, name := range list.names {
				if name = strings.TrimSpace(name); name != "" && !toolPatternKnown(name, known) {
					out = append(out, UnknownToolsetTool{Toolset: t.Name, Key: list.key, Name: name})
				}
			}
		}
	}
	return out
}

// toolPatternKnown reports whether a tool-list entry can match a tool: an
// exact name that is in known, a prefix* that begins at least one known name,
// a bare *, or anything addressed at an MCP server.
func toolPatternKnown(pattern string, known []string) bool {
	if pattern == "*" || strings.Contains(pattern, "__") {
		return true
	}
	prefix, wildcard := strings.CutSuffix(pattern, "*")
	for _, name := range known {
		if wildcard && strings.HasPrefix(name, prefix) {
			return true
		}
		if !wildcard && name == pattern {
			return true
		}
	}
	return false
}

// LogUnknownToolsetTools writes a startup warning for every entry
// UnknownToolsetTools reports. Nothing is logged when there is none.
func (c *Config) LogUnknownToolsetTools(log *slog.Logger) {
	if log == nil {
		return
	}
	for _, u := range c.UnknownToolsetTools() {
		log.Warn("toolset names no known tool", "setting", u.Path(), "tool", u.Name)
	}
}
