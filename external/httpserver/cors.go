//go:build http

package httpserver

import "net/http"

// corsMiddleware answers CORS preflight requests and adds Access-Control-* headers for allowed
// origins. It wraps the auth gate so preflight OPTIONS (which carry no Authorization header) are
// handled before authentication. It is a transparent pass-through when CORS is disabled.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A canvas frame runs in an opaque origin, so its own module scripts
		// and fetches are cross-origin requests from "null": the frame's files
		// answer any origin, without credentials, whatever httpserver.cors
		// says, including the preflight of a fetch with a header of its own.
		if isCanvasFilePath(r.URL.Path) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "*")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, HEAD")
				if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
					h.Set("Access-Control-Allow-Headers", req)
				}
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			if c := s.activeCfg(); c != nil {
				if allow, ok := c.HTTPServer.CORSAllowOrigin(origin); ok {
					h := w.Header()
					h.Set("Access-Control-Allow-Origin", allow)
					h.Add("Vary", "Origin")
					h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
					h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Coddy-Session-ID, Range, If-None-Match")
					h.Set("Access-Control-Expose-Headers", "Content-Range, Accept-Ranges, ETag, Content-Disposition")
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
