// Package browser lets the agent drive the user's own browser through the
// Coddy for Browser extension (github.com/coddy-project/coddy-for-browser).
//
// The extension dials out: it opens one WebSocket to the listener of this
// package and keeps it open, and everything Coddy asks of the browser travels
// down that socket as a JSON-RPC 2.0 request the extension answers. So the
// server never needs a route to the browser, never opens a debug port, and a
// browser behind NAT works the same as one on the same machine. The protocol
// is docs/reference/browser-protocol.md.
//
// The listener, the hub and the browser tool are behind the browser build tag.
// This file is not: it carries what a caller fills in and the flag that says
// whether this binary has the module, so `coddy serve` can read a
// configuration that enables it and refuse it by name in a build without it.
package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Options are what one listener is built from.
type Options struct {
	// Cfg is the configuration the listener starts with.
	Cfg *config.Config
	// Mgr owns the sessions whose turns use the browser; the listener follows
	// its turns to give a chat's tabs back when its turn ends.
	Mgr *session.Manager
	// Log is the process logger.
	Log *slog.Logger
}

// Fingerprint is everything a rebuilt listener would read differently: the
// whole browser block, hashed so the key never sits in the string the
// supervisor compares. A new key or address rebuilds the listener in place,
// which disconnects the extensions that used the old one.
func Fingerprint(cfg *config.Config) string {
	raw, _ := json.Marshal(cfg.Browser)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
