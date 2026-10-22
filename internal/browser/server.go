package browser

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// Server exposes only the browser protocol and an optional, explicitly scoped
// chat handler. Its credential is independent of the main HTTP API.
type Server struct {
	Hub     *Hub
	Token   string
	Chat    http.Handler
	mu      sync.Mutex
	sockets map[*websocket.Conn]struct{}
}

func NewServer(hub *Hub, token string) *Server {
	return &Server{Hub: hub, Token: token, sockets: make(map[*websocket.Conn]struct{})}
}

func extensionOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "chrome-extension" || u.Scheme == "moz-extension") && u.Host != "" && u.Path == "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func (s *Server) accepts(token string) bool {
	return s.Token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.Token)) == 1
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin != "" && !extensionOrigin(origin) {
		http.Error(w, "extension origin required", http.StatusForbidden)
		return
	}
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Coddy-Session-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/browser/v1/connect" {
		if !extensionOrigin(origin) {
			http.Error(w, "extension origin required", http.StatusForbidden)
			return
		}
		websocket.Server{Handshake: func(_ *websocket.Config, _ *http.Request) error { return nil }, Handler: s.socket}.ServeHTTP(w, r)
		return
	}
	if !s.accepts(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
		http.Error(w, "invalid browser key", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/browser/v1/status" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"version": ProtocolVersion, "browsers": s.Hub.List()})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/browser/v1/files/") {
		serveFile(w, r)
		return
	}
	if s.Chat != nil && strings.HasPrefix(r.URL.Path, "/browser/v1/chat/") {
		clone := r.Clone(r.Context())
		clone.URL.Path = strings.TrimPrefix(r.URL.Path, "/browser/v1/chat")
		s.Chat.ServeHTTP(w, clone)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) socket(ws *websocket.Conn) {
	s.mu.Lock()
	if len(s.sockets) >= 64 {
		s.mu.Unlock()
		_ = ws.Close()
		return
	}
	s.sockets[ws] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.sockets, ws); s.mu.Unlock(); _ = ws.Close() }()
	ws.MaxPayloadBytes = 16 << 10
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello struct {
		Type         string   `json:"type"`
		Version      int      `json:"version"`
		Token        string   `json:"token"`
		ID           string   `json:"id"`
		Name         string   `json:"name"`
		Capabilities []string `json:"capabilities"`
	}
	if err := websocket.JSON.Receive(ws, &hello); err != nil {
		return
	}
	if hello.Type != "hello" || hello.Version != ProtocolVersion || !s.accepts(hello.Token) {
		_ = websocket.JSON.Send(ws, map[string]string{"type": "error", "error": "invalid browser key or protocol version"})
		return
	}
	ws.MaxPayloadBytes = 6 << 20
	c, err := s.Hub.Connect(hello.ID, hello.Name, hello.Capabilities)
	if err != nil {
		_ = websocket.JSON.Send(ws, map[string]string{"type": "error", "error": err.Error()})
		return
	}
	defer s.Hub.Disconnect(c)
	if err := websocket.JSON.Send(ws, map[string]any{"type": "ready", "version": ProtocolVersion}); err != nil {
		return
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case cmd := <-c.Commands:
				_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if websocket.JSON.Send(ws, cmd) != nil {
					_ = ws.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()
	for {
		_ = ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		var msg struct {
			Type  string `json:"type"`
			Tabs  []Tab  `json:"tabs"`
			Grant string `json:"grant"`
			Reply
		}
		if websocket.JSON.Receive(ws, &msg) != nil {
			return
		}
		switch msg.Type {
		case "tabs":
			if s.Hub.UpdateTabs(c, msg.Tabs, msg.Grant) != nil {
				return
			}
		case "reply":
			s.Hub.Reply(c, msg.Reply)
		case "stop":
			s.Hub.Stop(c)
		case "ping":
		default:
			return
		}
	}
}

// Serve owns listener and websocket lifetimes, including config reloads.
func (s *Server) Serve(ctx context.Context, address, certFile, keyFile string) error {
	if strings.TrimSpace(s.Token) == "" {
		return errors.New("browser.token is required")
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	defer func() { _ = srv.Close() }()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
			return
		}
		s.mu.Lock()
		for ws := range s.sockets {
			_ = ws.Close()
		}
		s.mu.Unlock()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(stop)
	}()
	if certFile != "" {
		err = srv.ServeTLS(ln, certFile, keyFile)
	} else {
		err = srv.Serve(ln)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
