//go:build browser

package browser

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// Paths of the listener. Everything else answers 404.
const (
	pathConnect     = "/browser/v1/connect"
	pathFilesPrefix = "/browser/v1/files/"
)

// Socket timing: the extension pings every 20 s, so a minute of silence is a
// browser that is gone; initialize has to arrive right after the upgrade.
const (
	helloDeadline = 5 * time.Second
	readDeadline  = 60 * time.Second
	writeDeadline = 10 * time.Second
)

// Server is the listener of the extension. Its key is independent of the
// HTTP API's credentials and reaches only this protocol and its transfers.
type Server struct {
	Hub *Hub
	Key string
	Log *slog.Logger

	mu      sync.Mutex
	sockets map[*websocket.Conn]struct{}
}

func NewServer(hub *Hub, key string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{Hub: hub, Key: key, Log: log, sockets: make(map[*websocket.Conn]struct{})}
}

// extensionOrigin accepts the origin of a Chromium extension page or worker
// and nothing else, so a web page the user visits cannot open the socket, even
// to a listener on localhost.
func extensionOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "chrome-extension" && u.Host != "" && u.Path == "" &&
		u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func (s *Server) accepts(key string) bool {
	return s.Key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(s.Key)) == 1
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
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	switch {
	case r.URL.Path == pathConnect:
		if !extensionOrigin(origin) {
			http.Error(w, "extension origin required", http.StatusForbidden)
			return
		}
		// The handshake check is the origin above; the key arrives in
		// initialize, because a browser cannot set headers on a WebSocket.
		websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: s.socket}.ServeHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, pathFilesPrefix):
		if !s.accepts(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
			http.Error(w, "invalid extension key", http.StatusUnauthorized)
			return
		}
		serveFile(w, r, strings.TrimPrefix(r.URL.Path, pathFilesPrefix))
	default:
		http.NotFound(w, r)
	}
}

type initializeParams struct {
	ProtocolVersion int         `json:"protocolVersion"`
	Key             string      `json:"key"`
	Browser         BrowserInfo `json:"browser"`
	Actions         []string    `json:"actions"`
}

type initializeResult struct {
	ProtocolVersion int          `json:"protocolVersion"`
	Server          serverInfo   `json:"server"`
	Limits          serverLimits `json:"limits"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type serverLimits struct {
	MaxFileBytes int `json:"maxFileBytes"`
}

type tabsParams struct {
	Tabs  []Tab  `json:"tabs"`
	Grant string `json:"grant"`
}

func (s *Server) track(ws *websocket.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sockets) >= 64 {
		return false
	}
	s.sockets[ws] = struct{}{}
	return true
}

func (s *Server) untrack(ws *websocket.Conn) {
	s.mu.Lock()
	delete(s.sockets, ws)
	s.mu.Unlock()
	_ = ws.Close()
}

func send(ws *websocket.Conn, frame []byte) error {
	_ = ws.SetWriteDeadline(time.Now().Add(writeDeadline))
	return websocket.Message.Send(ws, string(frame))
}

// handshake reads initialize and answers it. Nothing else is accepted before
// it, and an extension that fails it is told why and closed.
func (s *Server) handshake(ws *websocket.Conn) (*Connection, bool) {
	_ = ws.SetReadDeadline(time.Now().Add(helloDeadline))
	ws.MaxPayloadBytes = 16 << 10
	var raw []byte
	if err := websocket.Message.Receive(ws, &raw); err != nil {
		return nil, false
	}
	var msg message
	if err := json.Unmarshal(raw, &msg); err != nil || !msg.isRequest() || msg.Method != methodInitialize {
		_ = send(ws, failure(msg.ID, codeInvalidRequest, "initialize must be the first message", nil))
		return nil, false
	}
	var p initializeParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		_ = send(ws, failure(msg.ID, codeInvalidParams, "invalid initialize parameters", nil))
		return nil, false
	}
	if !s.accepts(p.Key) {
		_ = send(ws, failure(msg.ID, codeUnauthorized, "invalid extension key", nil))
		return nil, false
	}
	if p.ProtocolVersion != ProtocolVersion {
		_ = send(ws, failure(msg.ID, codeProtocolVersion, "unsupported protocol version", map[string]any{"supported": []int{ProtocolVersion}}))
		return nil, false
	}
	c, err := s.Hub.Connect(p.Browser, p.Actions)
	if err != nil {
		_ = send(ws, failure(msg.ID, codeInvalidParams, err.Error(), nil))
		return nil, false
	}
	frame, err := result(msg.ID, initializeResult{
		ProtocolVersion: ProtocolVersion,
		Server:          serverInfo{Name: "coddy", Version: version.Version},
		Limits:          serverLimits{MaxFileBytes: MaxFileBytes},
	})
	if err != nil || send(ws, frame) != nil {
		s.Hub.Disconnect(c)
		return nil, false
	}
	return c, true
}

func (s *Server) socket(ws *websocket.Conn) {
	if !s.track(ws) {
		_ = ws.Close()
		return
	}
	defer s.untrack(ws)
	c, ok := s.handshake(ws)
	if !ok {
		return
	}
	defer s.Hub.Disconnect(c)
	s.Log.Info("browser connected", "browser", c.info.ID, "name", c.info.Name, "version", c.info.Version)
	defer s.Log.Info("browser disconnected", "browser", c.info.ID)
	// Commands carry no page data, but answers do: a snapshot of a long page,
	// a screenshot. Files go through the transfer route instead.
	ws.MaxPayloadBytes = 8 << 20

	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		for {
			select {
			case frame := <-c.out:
				if send(ws, frame) != nil {
					_ = ws.Close()
					return
				}
			case <-c.done:
				// Replaced by a newer connection of the same browser, or the
				// listener is going away.
				_ = ws.Close()
				return
			case <-stopped:
				return
			}
		}
	}()
	for {
		_ = ws.SetReadDeadline(time.Now().Add(readDeadline))
		var raw []byte
		if websocket.Message.Receive(ws, &raw) != nil {
			return
		}
		var msg message
		if json.Unmarshal(raw, &msg) != nil {
			return
		}
		switch {
		case msg.isResponse():
			id, err := msg.stringID()
			if err == nil {
				s.Hub.Answer(c, id, msg.Result, msg.Error)
			}
		case msg.isNotification():
			if !s.notice(c, msg) {
				return
			}
		case msg.isRequest():
			// The extension asks Coddy nothing after initialize yet; a
			// newer extension that does hears so and goes on.
			select {
			case c.out <- failure(msg.ID, codeMethodNotFound, "method not found: "+msg.Method, nil):
			default:
			}
		default:
			return
		}
	}
}

// notice handles a notification from the extension. A malformed tab list ends
// the connection: the extension and Coddy no longer agree on what is shared.
func (s *Server) notice(c *Connection, msg message) bool {
	switch msg.Method {
	case methodTabs:
		var p tabsParams
		if json.Unmarshal(msg.Params, &p) != nil {
			return false
		}
		if err := s.Hub.UpdateTabs(c, p.Tabs, p.Grant); err != nil {
			s.Log.Warn("browser sent an invalid tab list", "browser", c.info.ID, "err", err)
			return false
		}
	case methodStop:
		s.Hub.Stop(c)
		s.Log.Info("browser stopped by the user", "browser", c.info.ID)
	case methodPing:
	default:
		// A notification this server does not know is a newer extension's
		// and is ignored, so either side can be upgraded first.
	}
	return true
}

// Serve owns the listener and the sockets, including a rebuild on a config
// reload: when ctx ends every socket is closed and the extensions reconnect to
// whatever listener comes next.
func (s *Server) Serve(ctx context.Context, address, certFile, keyFile string) error {
	if strings.TrimSpace(s.Key) == "" {
		return errors.New("browser.token is required")
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	srv := &http.Server{
		Handler: s, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
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
