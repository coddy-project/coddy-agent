// Package httpserver implements an OpenAI-compatible HTTP API for Coddy.
//
// The API itself is behind the http build tag. This file is not: it carries the
// options a caller fills in and the flag that says whether this binary can serve
// at all, so `coddy serve` can read a configuration that asks for the API and
// answer honestly in a build that has none.
package httpserver

import (
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Options are what one HTTP server instance is built from.
type Options struct {
	// Cfg is the configuration the server starts with. Later edits reach it
	// through ReplaceConfig, not through this pointer.
	Cfg *config.Config
	// Mgr owns the sessions this server serves.
	Mgr *session.Manager
	// Log is the process logger.
	Log *slog.Logger
	// DefaultCWD is the workspace a session gets when the client names none.
	DefaultCWD string
	// Home is the agent state directory, used by the swarm join credentials.
	Home string
	// ListenAddr is the already-resolved host:port to bind.
	ListenAddr string
	// ExtraAuthTokens are bearer tokens supplied out of band (--auth-token,
	// CODDY_HTTP_TOKEN) so a credential need not be written into config.yaml.
	ExtraAuthTokens []string
	// OnServer, when set, is handed the live server as it comes up and nil as
	// it goes down. It is how the process installs the turn mirror, and how it
	// drops the mirror again when this subsystem restarts.
	OnServer func(*Server)
}
