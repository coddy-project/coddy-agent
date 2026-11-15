package tools

import (
	"sort"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/preview"
)

// KnownToolNames lists every built-in tool name this build can offer, the
// optional ones included (switch_model, which a configuration with nothing to
// switch to leaves out, and the scheduler's tools and the browser tool in a
// build that has them): the names toolsets[].tools and
// toolsets[].disallowed_tools may mean. MCP tools are not in it, a server's
// tools being known only while it is connected.
//
// The config check cannot import this package (config sits below it), so
// cmd/coddy hands this function down at start-up with config.RegisterToolCatalog.
func KnownToolNames() []string {
	r := NewRegistry()
	registerSchedulerTools(r, &config.Config{Scheduler: config.SchedulerConfig{Enabled: true}})
	registerBrowserTools(r, &config.Config{Browser: config.BrowserConfig{Enable: true}})
	// The publishing tools are registered only where the operator turned the
	// published-preview channel on, and a toolset may name them whatever this
	// machine's configuration says today.
	preview.RegisterPublishTools(r.Register)
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
