//go:build http

package httpserver

import "net/http"

// ListenAndServe starts the HTTP server on addr using the configured handler.
func ListenAndServe(addr string, srv *Server) error {
	return http.ListenAndServe(addr, srv.Handler())
}
