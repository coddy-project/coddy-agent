//go:build browser

package tools

import (
	browsertool "github.com/EvilFreelancer/coddy-agent/external/browser/tool"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// registerBrowserTools offers the browser tool where the configuration turns
// the extension listener on: without it no browser can connect, and a tool
// that can only fail would cost every request its definition.
func registerBrowserTools(r *Registry, cfg *config.Config) {
	if cfg == nil || !cfg.Browser.Enable {
		return
	}
	r.Register(browsertool.Tool())
}
