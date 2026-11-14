//go:build http

package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
)

const defaultOpenAIMaxBodyBytes int64 = 512 << 20

func (s *Server) decodeOpenAIRequest(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, s.openAIMaxBodyBytes())
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return err
	}
	_, err := io.Copy(io.Discard, r.Body)
	return err
}

func (s *Server) openAIMaxBodyBytes() int64 {
	if cfg := s.activeCfg(); cfg != nil && cfg.HTTPServer.MaxBodyBytes > 0 {
		return cfg.HTTPServer.MaxBodyBytes
	}
	return defaultOpenAIMaxBodyBytes
}

func writeBodyTooLarge(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	_, _ = w.Write([]byte(`{"error":{"message":"request body too large","type":"invalid_request_error","code":"request_too_large","param":null}}` + "\n"))
}
