//go:build http

package httpserver

import "github.com/EvilFreelancer/coddy-agent/internal/httpx"

// ListenAndServe starts the HTTP server on addr using the configured handler.
//
// The bounds come from internal/httpx rather than http.ListenAndServe's
// defaults, which are none at all: a peer that opens a connection and never
// finishes its headers would otherwise pin a goroutine indefinitely. The
// whole-request deadlines stay unset there, so streaming turns are untouched.
func ListenAndServe(addr string, srv *Server) error {
	return httpx.NewServer(addr, srv.Handler()).ListenAndServe()
}
