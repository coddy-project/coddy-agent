//go:build http

package httpserver

import "net/http"

// corsMiddleware answers CORS preflight requests and adds Access-Control-* headers for allowed
// origins. It wraps the auth gate so preflight OPTIONS (which carry no Authorization header) are
// handled before authentication. It is a transparent pass-through when CORS is disabled.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if c := s.activeCfg(); c != nil {
				if allow, ok := c.HTTPServer.CORSAllowOrigin(origin); ok {
					h := w.Header()
					h.Set("Access-Control-Allow-Origin", allow)
					h.Add("Vary", "Origin")
					h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Coddy-Session-ID")
					h.Set("Access-Control-Max-Age", "600")
				}
			}
		}
		// Preflight: short-circuit before auth. A disallowed origin simply lacks the ACAO header.
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
