//go:build http

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// browserChats is the set of conversations the extension port has started. It
// is used by handler-only tests. Running servers also persist ownership under
// their own home, so listener rebuilds and process restarts retain access.

var browserChats = &chatScope{started: make(map[string]struct{})}

// chatScope bounds what the extension key can reach. The key is documented to
// "start agent conversations", not to reach every conversation on the server:
// a leaked key must not be able to answer another operator's pending
// permission prompt or read their transcript. A conversation is in the set
// when this port started it, and the inner server says exactly that вЂ” it mints
// the id itself and answers with X-Coddy-Session-ID only on the request that
// created the session. A session id the extension simply names is never
// admitted, so it cannot claim one somebody else started.
type chatScope struct {
	mu      sync.Mutex
	started map[string]struct{}
	dir     string
}

func (c *chatScope) owns(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.started[id]
	if !ok && c.dir != "" && filepath.Base(id) == id {
		data, err := os.ReadFile(filepath.Join(c.dir, id))
		ok = err == nil && string(data) == "browser-chat\n"
	}
	return ok
}

func (c *chatScope) adopt(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dir != "" {
		if err := os.MkdirAll(c.dir, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(c.dir, id), []byte("browser-chat\n"), 0600); err != nil {
			return err
		}
	}
	c.started[id] = struct{}{}
	return nil
}

// chatStartResponse forwards a turn unchanged and records the conversation
// the server minted for it. It stays a pass-through: the turn streams as SSE,
// and the sender flushes after every frame.
type chatStartResponse struct {
	http.ResponseWriter
	scope     *chatScope
	minted    bool // the request carried no session id of the client's
	recordErr error
	recorded  bool
}

func (c *chatStartResponse) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *chatStartResponse) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *chatStartResponse) record() {
	if c.recorded {
		return
	}
	c.recorded = true
	if !c.minted {
		return
	}
	if id := strings.TrimSpace(c.Header().Get("X-Coddy-Session-ID")); id != "" {
		c.recordErr = c.scope.adopt(id)
	}
}

func (c *chatStartResponse) WriteHeader(code int) {
	c.record()
	if c.recordErr != nil {
		code = http.StatusInternalServerError
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *chatStartResponse) Write(b []byte) (int, error) {
	c.record()
	if c.recordErr != nil {
		return 0, c.recordErr
	}
	return c.ResponseWriter.Write(b)
}

// chatListResponse narrows the chat list down to the conversations this port
// started. Buffered, because the body has to be rewritten before it goes out;
// the list is a single JSON document, never a stream.
type chatListResponse struct {
	scope  *chatScope
	header http.Header
	code   int
	body   bytes.Buffer
}

func (c *chatListResponse) Header() http.Header {
	if c.header == nil {
		c.header = http.Header{}
	}
	return c.header
}

func (c *chatListResponse) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *chatListResponse) Write(b []byte) (int, error) {
	return c.body.Write(b)
}

func (c *chatListResponse) filter(w http.ResponseWriter) {
	out := c.body.Bytes()
	if c.code == 0 || c.code == http.StatusOK {
		if filtered, err := keepStartedChats(out, c.scope); err == nil {
			out = filtered
		}
	}
	for k, vs := range c.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	// The body is a different length now, so a forwarded Content-Length would
	// cut it short or leave the client waiting.
	w.Header().Del("Content-Length")
	if c.code != 0 {
		w.WriteHeader(c.code)
	}
	_, _ = w.Write(out)
}

// keepStartedChats rewrites a coddy.session_list body down to the rows whose
// conversation this port started. A body in an unrecognised shape goes back
// untouched rather than as a list the caller cannot use.
func keepStartedChats(body []byte, scope *chatScope) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	raw, ok := doc["sessions"]
	if !ok {
		return nil, json.Unmarshal(body, &[]json.RawMessage{})
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	kept := make([]map[string]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		var id string
		if err := json.Unmarshal(row["id"], &id); err != nil || !scope.owns(id) {
			continue
		}
		kept = append(kept, row)
	}
	sessions, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	doc["sessions"] = sessions
	return json.Marshal(doc)
}

// browserChatHandler exposes only conversation routes on the extension port.
// The internal credential never leaves this handler; the extension key cannot
// reach configuration, workspace files or any route omitted here вЂ” and within
// conversations it reaches only the ones this port started (chatScope).
func browserChatHandler(rt *serve.Runtime) (http.Handler, func()) {
	scope := browserChats
	if rt.Paths.Home != "" {
		scope = &chatScope{started: make(map[string]struct{}), dir: filepath.Join(rt.Paths.Home, "browser-chats")}
	}
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	token := hex.EncodeToString(raw[:])
	s := httpserver.NewHandlerOnly(rt.Cfg(), rt.Mgr, rt.Log, rt.Paths.CWD)
	s.SetExtraAuthTokens([]string{token})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		parts := strings.Split(strings.Trim(path, "/"), "/")
		scoped := len(parts) == 4 && parts[0] == "coddy" && parts[1] == "sessions" && strings.HasPrefix(parts[2], "sess_")

		allowed := r.Method == http.MethodGet && (path == "/v1/models" || path == "/coddy/sessions")
		allowed = allowed || r.Method == http.MethodPost && path == "/v1/responses"
		if scoped {
			allowed = allowed || r.Method == http.MethodGet && (parts[3] == "messages" || parts[3] == "activity")
			allowed = allowed || r.Method == http.MethodPost && (parts[3] == "cancel" || parts[3] == "permission" || parts[3] == "question")
		}
		if !allowed {
			http.NotFound(w, r)
			return
		}

		clientSession := ""
		if scoped {
			clientSession = parts[2]
		} else if r.Method == http.MethodPost && path == "/v1/responses" {
			clientSession = strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID"))
		}
		if clientSession != "" && !scope.owns(clientSession) {
			http.Error(w, `{"error":{"message":"this extension key did not start that conversation"}}`, http.StatusForbidden)
			return
		}

		clone := r.Clone(r.Context())
		clone.Header = r.Header.Clone()
		clone.Header.Set("Authorization", "Bearer "+token)
		clone.Header.Del("Origin")
		clone.Header.Del("Cookie")
		switch {
		case r.Method == http.MethodPost && path == "/v1/responses":
			s.Handler().ServeHTTP(&chatStartResponse{
				ResponseWriter: w,
				scope:          scope,
				minted:         clientSession == "",
			}, clone)
		case r.Method == http.MethodGet && path == "/coddy/sessions":
			list := &chatListResponse{scope: scope}
			s.Handler().ServeHTTP(list, clone)
			list.filter(w)
		default:
			s.Handler().ServeHTTP(w, clone)
		}
	}), s.CloseObservers
}
