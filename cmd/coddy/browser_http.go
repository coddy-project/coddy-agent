//go:build http

package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// browserChatHandler exposes only conversation routes on the extension port.
// The internal credential never leaves this handler; the extension key cannot
// reach configuration, workspace files or any route omitted here.
func browserChatHandler(rt *serve.Runtime) (http.Handler, func()) {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	token := hex.EncodeToString(raw[:])
	s := httpserver.New(rt.Cfg(), rt.Mgr, rt.Log, rt.Paths.CWD)
	s.SetExtraAuthTokens([]string{token})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		allowed := r.Method == "GET" && (path == "/v1/models" || path == "/coddy/sessions")
		allowed = allowed || r.Method == "POST" && path == "/v1/responses"
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) == 4 && parts[0] == "coddy" && parts[1] == "sessions" && strings.HasPrefix(parts[2], "sess_") {
			allowed = allowed || r.Method == "GET" && (parts[3] == "messages" || parts[3] == "activity")
			allowed = allowed || r.Method == "POST" && (parts[3] == "cancel" || parts[3] == "permission" || parts[3] == "question")
		}
		if !allowed {
			http.NotFound(w, r)
			return
		}
		clone := r.Clone(r.Context())
		clone.Header = r.Header.Clone()
		clone.Header.Set("Authorization", "Bearer "+token)
		clone.Header.Del("Origin")
		clone.Header.Del("Cookie")
		s.Handler().ServeHTTP(w, clone)
	}), s.CloseObservers
}
