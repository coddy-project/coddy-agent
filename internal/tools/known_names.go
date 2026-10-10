package tools

import (
	"sort"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// KnownToolNames lists every built-in tool name this build can offer, the
// optional ones included (switch_model, which a configuration with nothing to
// switch to leaves out, and the scheduler's tools in a build that has them):
// the names models[].tools and models[].disallowed_tools may mean. MCP tools
// are not in it, a server's tools being known only while it is connected.
//
// The config check cannot import this package (config sits below it), so
// cmd/coddy hands this function down at start-up with config.RegisterToolCatalog.
func KnownToolNames() []string {
	r := NewRegistry()
	registerSchedulerTools(r, &config.Config{Scheduler: config.SchedulerConfig{Enabled: true}})
	seen := map[string]bool{ToolSwitchModel: true}
	for _, def := range r.AllToolDefinitions() {
		seen[def.Name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
