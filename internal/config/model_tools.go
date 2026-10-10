package config

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
)

// The lists models[].tools and models[].disallowed_tools name tools, and the
// registry that knows them sits above this package (internal/tools imports
// config). The binary (cmd/coddy) hands the tools package's names down at
// start-up, the way the mention package takes its URL fetcher, so the config
// check can say which entry names nothing without this package importing
// upward. Until a catalog is registered nothing is reported: an unknown name
// is only ever a warning.
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

// UnknownModelTool is an entry of a models[] tool list that matches no tool.
type UnknownModelTool struct {
	// Model is the models[].model selector the entry sits on.
	Model string
	// Key is the list the entry is in: tools or disallowed_tools.
	Key string
	// Name is the entry as written.
	Name string
}

// Path is the list's config path, in the selector form the config check
// places a finding with.
func (u UnknownModelTool) Path() string { return "models[" + u.Model + "]." + u.Key }

// Message says what is wrong with the entry.
func (u UnknownModelTool) Message() string {
	return strconv.Quote(u.Name) + " matches no tool this build offers, so the entry changes nothing"
}

// UnknownModelTools lists the entries of models[].tools and
// models[].disallowed_tools that match no tool name. It is a hint, never an
// error: a build omits tools its tags leave out (the scheduler's, for one),
// and an MCP tool (server__tool) exists only while its server is connected,
// so a name or a pattern with a double underscore is taken on trust.
func (c *Config) UnknownModelTools() []UnknownModelTool {
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
	var out []UnknownModelTool
	for i := range c.Models {
		m := &c.Models[i]
		for _, list := range []struct {
			key   string
			names []string
		}{{"tools", m.Tools}, {"disallowed_tools", m.DisallowedTools}} {
			for _, name := range list.names {
				if name = strings.TrimSpace(name); name != "" && !toolPatternKnown(name, known) {
					out = append(out, UnknownModelTool{Model: m.Model, Key: list.key, Name: name})
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

// LogUnknownModelTools writes a startup warning for every entry
// UnknownModelTools reports. Nothing is logged when there is none.
func (c *Config) LogUnknownModelTools(log *slog.Logger) {
	if log == nil {
		return
	}
	for _, u := range c.UnknownModelTools() {
		log.Warn("model tool list names no known tool", "setting", u.Path(), "tool", u.Name)
	}
}
