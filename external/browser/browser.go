// Package browser lets the agent drive the user's own browser through the
// Coddy for Browser extension (github.com/coddy-project/coddy-for-browser).
//
// The extension dials out: it opens one WebSocket to the HTTP server of
// `coddy serve`, its home node, and keeps it open, and everything Coddy asks
// of the browser travels down that socket as a JSON-RPC 2.0 request the
// extension answers. So the server never needs a route to the browser, never
// opens a debug port, and a browser behind NAT works the same as one on the
// same machine. The extension's side panel shows the web UI of the same
// server. The protocol is docs/reference/browser-protocol.md.
//
// A session of another process - a console or an editor on the same machine,
// a node of the swarm - reaches the browsers of a home node through that
// node's HTTP API (/coddy/browser/v1), never through the socket; the design
// is docs/plans/browser.md.
//
// The endpoint, the hub and the browser tool are behind the browser build
// tag. This file is not: it carries what the HTTP server fills in and the
// flag that says whether this binary has the module, so `coddy serve` can read
// a configuration that enables it and refuse it by name in a build without it.
package browser

import (
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The routes the endpoint takes on the HTTP server. They are reached with the
// extension key, never with the HTTP API's credentials, so the server's own
// gate lets them through.
const (
	ConnectPattern = "GET /browser/v1/connect"
	FilesPattern   = "/browser/v1/files/{ticket}"
)

// The routes other Coddy processes reach this server's browsers through.
// They are part of the HTTP API, behind its ordinary credentials, and answer
// 404 unless browser.serve_remote is on.
const (
	APIBrowsersPattern = "GET /coddy/browser/v1/browsers"
	APICallPattern     = "POST /coddy/browser/v1/call"
)

// MountOptions are what the endpoint is mounted with.
type MountOptions struct {
	// Key is the extension key of the live configuration, "" while
	// browser.enable is off: the routes then answer 404.
	Key func() string
	// ServeRemote is browser.serve_remote of the live configuration: whether
	// the API routes serve the browsers to other Coddy processes.
	ServeRemote func() bool
	// Mgr owns the sessions whose turns use the browser; the endpoint follows
	// its turns to tell a browser when a chat holding its tabs stops working,
	// and names each chat's tab group after the chat. Nil follows nothing.
	Mgr *session.Manager
	// Log is the process logger.
	Log *slog.Logger
}

// Mounted is the endpoint on a server.
type Mounted struct {
	// Disconnect ends every extension socket: the key changed, and an
	// extension still holding the old one must not keep its connection.
	Disconnect func()
	// Close detaches the endpoint from the manager and ends every socket:
	// the server is going away.
	Close func()
}
