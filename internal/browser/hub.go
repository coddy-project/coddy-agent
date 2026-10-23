// Package browser connects agent tools to browser extensions. The extension
// makes the outbound connection; the server never opens a browser debug port.
package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const ProtocolVersion = 1

// MaxFileBytes bounds file transfers independently of websocket messages.
const MaxFileBytes = 32 << 20

type Tab struct {
	ID    int    `json:"id"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

type Command struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	SessionID string          `json:"session_id"`
	Action    string          `json:"action"`
	TabID     int             `json:"tab_id"`
	Params    json.RawMessage `json:"params,omitempty"`
	Grant     string          `json:"grant"`
}

type Reply struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type Snapshot struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	Tabs         []Tab    `json:"tabs"`
}

// Connection belongs to one websocket lifetime. A reconnect is never allowed
// to answer a previous connection's requests or inherit its tab leases.
type Connection struct {
	ID           string
	name         string
	capabilities []string
	tabs         map[int]Tab
	owners       map[int]string
	Commands     chan Command
	done         chan struct{}
	pending      map[string]chan Reply
	grant        string
}

type Hub struct {
	mu          sync.Mutex
	connections map[string]*Connection
}

func NewHub() *Hub { return &Hub{connections: make(map[string]*Connection)} }

var processHub = NewHub()

// Default is shared by the serve subsystem and the tools of its session manager.
func Default() *Hub { return processHub }

func randomID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func validID(id string) bool {
	if len(id) < 8 || len(id) > 80 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func (h *Hub) Connect(id, name string, capabilities []string) (*Connection, error) {
	if !validID(id) || strings.TrimSpace(name) == "" || len(name) > 100 || len(capabilities) > 32 {
		return nil, errors.New("invalid browser registration")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connections[id] != nil {
		return nil, errors.New("browser already connected")
	}
	if len(h.connections) >= 32 {
		return nil, errors.New("too many connected browsers")
	}
	c := &Connection{ID: id, name: name, capabilities: append([]string(nil), capabilities...), tabs: make(map[int]Tab), owners: make(map[int]string), Commands: make(chan Command, 32), done: make(chan struct{}), pending: make(map[string]chan Reply)}
	h.connections[id] = c
	return c, nil
}

func (h *Hub) Disconnect(c *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connections[c.ID] == c {
		delete(h.connections, c.ID)
		close(c.done)
	}
}

func (h *Hub) UpdateTabs(c *Connection, tabs []Tab, grant ...string) error {
	if len(tabs) > 100 {
		return errors.New("at most 100 shared tabs")
	}
	next := make(map[int]Tab)
	for _, t := range tabs {
		u, err := url.Parse(t.URL)
		if t.ID <= 0 || err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || len(t.URL) > 8192 || len(t.Title) > 1000 {
			return errors.New("invalid shared tab")
		}
		next[t.ID] = t
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connections[c.ID] != c {
		return errors.New("browser disconnected")
	}
	for id := range c.owners {
		if _, ok := next[id]; !ok {
			delete(c.owners, id)
		}
	}
	c.tabs = next
	// grant is variadic so tests can leave it out. The websocket handler
	// always passes one string, so len(grant) alone cannot tell "no grant in
	// this message" from "an empty grant" — and storing the latter would make
	// every later Call stamp a grant the extension no longer recognises.
	if len(grant) > 0 && grant[0] != "" {
		c.grant = grant[0]
	}
	return nil
}

func (h *Hub) List() []Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Snapshot, 0, len(h.connections))
	for _, c := range h.connections {
		s := Snapshot{ID: c.ID, Name: c.name, Capabilities: append([]string(nil), c.capabilities...), Tabs: make([]Tab, 0, len(c.tabs))}
		for _, t := range c.tabs {
			s.Tabs = append(s.Tabs, t)
		}
		sort.Slice(s.Tabs, func(i, j int) bool { return s.Tabs[i].ID < s.Tabs[j].ID })
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Release gives up this chat's leases without closing its pages.
func (h *Hub) Release(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.connections {
		for tab, owner := range c.owners {
			if owner == sessionID {
				delete(c.owners, tab)
			}
		}
	}
}

func (h *Hub) Stop(c *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c.tabs = make(map[int]Tab)
	c.owners = make(map[int]string)
	for id, reply := range c.pending {
		reply <- Reply{ID: id, Error: "browser stopped by user; select tabs again to resume"}
		delete(c.pending, id)
	}
}

func (h *Hub) Reply(c *Connection, reply Reply) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connections[c.ID] != c {
		return
	}
	if ch := c.pending[reply.ID]; ch != nil {
		ch <- reply
		delete(c.pending, reply.ID)
	}
}

// actionTimeout bounds one command by its class: an interaction answers in
// seconds, while download_file streams up to 32 MiB through the browser's own
// connection and needs the wider bound. The socket stays alive through it by
// the extension's 20 s pings against the 60 s read deadline.
func actionTimeout(action string) time.Duration {
	if action == "download_file" {
		return 10 * time.Minute
	}
	return 2 * time.Minute
}

// Call never retries an action. Losing a response to a click is an unknown
// outcome, not permission to click twice.
func (h *Hub) Call(ctx context.Context, sessionID, browserID string, cmd Command) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout(cmd.Action))
	defer cancel()
	h.mu.Lock()
	c := h.connections[browserID]
	if c == nil {
		h.mu.Unlock()
		return nil, errors.New("browser is offline; reconnect the extension")
	}
	if _, ok := c.tabs[cmd.TabID]; !ok {
		h.mu.Unlock()
		return nil, errors.New("tab is not shared; select it in the extension")
	}
	if owner := c.owners[cmd.TabID]; owner != "" && owner != sessionID {
		h.mu.Unlock()
		return nil, errors.New("tab is controlled by another chat; release it there first")
	}
	supported := false
	for _, capability := range c.capabilities {
		if capability == cmd.Action {
			supported = true
		}
	}
	if !supported {
		h.mu.Unlock()
		return nil, fmt.Errorf("browser does not support %q", cmd.Action)
	}
	c.owners[cmd.TabID] = sessionID
	cmd.ID, cmd.Type, cmd.SessionID = randomID(), "command", sessionID
	cmd.Grant = c.grant
	reply := make(chan Reply, 1)
	c.pending[cmd.ID] = reply
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(c.pending, cmd.ID); h.mu.Unlock() }()
	select {
	case c.Commands <- cmd:
	case <-c.done:
		return nil, errors.New("browser disconnected; action outcome may be unknown")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-reply:
		if r.Error != "" {
			return nil, errors.New(r.Error)
		}
		return r.Result, nil
	case <-c.done:
		return nil, errors.New("browser disconnected; action outcome may be unknown")
	case <-ctx.Done():
		select {
		case c.Commands <- Command{Type: "cancel", ID: cmd.ID}:
		default:
		}
		return nil, fmt.Errorf("browser action cancelled or timed out; inspect page before retrying: %w", ctx.Err())
	}
}
