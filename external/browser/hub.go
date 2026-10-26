//go:build browser

package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProtocolVersion is the version of the wire this server speaks. An extension
// that asks for another one is refused in initialize with the list of the
// versions this server accepts.
const ProtocolVersion = 1

// MaxFileBytes bounds a file transfer independently of the socket's messages.
const MaxFileBytes = 32 << 20

// Actions that address no tab: open_tab opens one in the chat's tab group,
// release hands the chat's tabs back to the shared group.
var tablessActions = []string{"open_tab", "release"}

// Tab is a tab the extension lets the agent reach. The extension keeps every
// such tab in a Chrome tab group: the shared "Coddy" group the user drags tabs
// into, which any chat may take a tab from, or the group of one chat, which
// SessionID names and no other chat may act in. A chat takes a shared tab into
// its own group with its first action there, and every tab it opens starts in
// its group.
type Tab struct {
	ID        int    `json:"id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	SessionID string `json:"sessionId,omitempty"`
}

// BrowserInfo is what an extension says about itself in initialize.
type BrowserInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// TabView is a tab as one chat sees it: whether it is in this chat's group,
// or in another chat's and out of reach until that chat releases it or the
// user moves it.
type TabView struct {
	Tab
	Yours bool `json:"yours,omitempty"`
	Busy  bool `json:"busy,omitempty"`
}

// Snapshot is one connected browser as a chat sees it.
type Snapshot struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Version string    `json:"version,omitempty"`
	Actions []string  `json:"actions"`
	Tabs    []TabView `json:"tabs"`
}

// Connection belongs to one WebSocket lifetime. A reconnect is never allowed
// to answer a previous connection's requests.
type Connection struct {
	info    BrowserInfo
	actions []string
	tabs    map[int]Tab
	grant   string
	pending map[string]chan reply
	// out is what the socket's writer sends, in order.
	out  chan []byte
	done chan struct{}
}

// ID is the browser's own id, stable across its reconnects.
func (c *Connection) ID() string { return c.info.ID }

type reply struct {
	result json.RawMessage
	err    error
}

// Hub holds the connected browsers of the process. The listener feeds it and
// the browser tool calls through it.
type Hub struct {
	mu          sync.Mutex
	connections map[string]*Connection
	// title names a chat for the extension, which titles the chat's tab
	// group with it. Nil leaves groups named by the extension alone.
	title func(sessionID string) string
}

// SetTitles installs how a chat's title is looked up; coddy serve hands the
// session manager's.
func (h *Hub) SetTitles(title func(sessionID string) string) {
	h.mu.Lock()
	h.title = title
	h.mu.Unlock()
}

func NewHub() *Hub { return &Hub{connections: make(map[string]*Connection)} }

var processHub = NewHub()

// Default is the hub the listener of coddy serve and the tools of its session
// manager share.
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

// Connect registers a browser. A browser that connects again under the same
// id replaces its previous connection: a Chrome service worker that was torn
// down leaves its old socket behind for up to a read deadline, and the user
// must not wait that long to work again. The replaced connection's pending
// requests fail as an unknown outcome.
func (h *Hub) Connect(info BrowserInfo, actions []string) (*Connection, error) {
	info.Name = strings.TrimSpace(info.Name)
	if !validID(info.ID) || info.Name == "" || len(info.Name) > 100 || len(info.Version) > 40 || len(actions) > 64 {
		return nil, errors.New("invalid browser registration")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if old := h.connections[info.ID]; old != nil {
		h.dropLocked(old)
	} else if len(h.connections) >= 32 {
		return nil, errors.New("too many connected browsers")
	}
	c := &Connection{
		info: info, actions: slices.Clone(actions), tabs: make(map[int]Tab),
		pending: make(map[string]chan reply), out: make(chan []byte, 64), done: make(chan struct{}),
	}
	h.connections[info.ID] = c
	return c, nil
}

// Disconnect forgets c unless a newer connection of the same browser
// already took its place.
func (h *Hub) Disconnect(c *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connections[c.info.ID] == c {
		h.dropLocked(c)
	}
}

func (h *Hub) dropLocked(c *Connection) {
	delete(h.connections, c.info.ID)
	close(c.done)
}

func validTab(t Tab) bool {
	u, err := url.Parse(t.URL)
	return t.ID > 0 && err == nil && u.Hostname() != "" && (u.Scheme == "http" || u.Scheme == "https") &&
		len(t.URL) <= 8192 && len(t.Title) <= 1000
}

// UpdateTabs replaces the tabs the extension lets the agent reach and who
// holds each of them. An empty grant leaves the previous one in place.
func (h *Hub) UpdateTabs(c *Connection, tabs []Tab, grant string) error {
	if len(tabs) > 100 {
		return errors.New("at most 100 shared tabs")
	}
	next := make(map[int]Tab, len(tabs))
	for _, t := range tabs {
		if !validTab(t) {
			return errors.New("invalid shared tab")
		}
		next[t.ID] = t
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connections[c.info.ID] != c {
		return errors.New("browser disconnected")
	}
	c.tabs = next
	if grant != "" {
		c.grant = grant
	}
	return nil
}

// List is every connected browser as sessionID sees it.
func (h *Hub) List(sessionID string) []Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Snapshot, 0, len(h.connections))
	for _, c := range h.connections {
		s := Snapshot{ID: c.info.ID, Name: c.info.Name, Version: c.info.Version, Actions: slices.Clone(c.actions), Tabs: make([]TabView, 0, len(c.tabs))}
		for _, t := range c.tabs {
			s.Tabs = append(s.Tabs, TabView{Tab: t, Yours: t.SessionID != "" && t.SessionID == sessionID, Busy: t.SessionID != "" && t.SessionID != sessionID})
		}
		sort.Slice(s.Tabs, func(i, j int) bool { return s.Tabs[i].ID < s.Tabs[j].ID })
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Connected reports whether a browser with this id is connected.
func (h *Hub) Connected(browserID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.connections[browserID] != nil
}

// TurnEnded tells every browser holding a tab of the chat that its turn is
// over, so the extension takes the "working" marks off the chat's tabs and
// its tab group. A browser holding none of its tabs hears nothing.
func (h *Hub) TurnEnded(sessionID string) {
	frame, err := notification(methodTurnEnded, map[string]string{"sessionId": sessionID})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.connections {
		for _, t := range c.tabs {
			if t.SessionID == sessionID {
				select {
				case c.out <- frame:
				default:
				}
				break
			}
		}
	}
}

// Stop is the user pressing Stop in the extension: every tab is withdrawn and
// every request still waiting fails.
func (h *Hub) Stop(c *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connections[c.info.ID] != c {
		return
	}
	c.tabs = make(map[int]Tab)
	for id, ch := range c.pending {
		ch <- reply{err: errors.New("the user pressed Stop in the browser; ask them to share a tab again")}
		delete(c.pending, id)
	}
}

// Answer routes the extension's response to the request it answers. A
// response to a request this connection never sent, or one already answered,
// is dropped.
func (h *Hub) Answer(c *Connection, id string, result json.RawMessage, rpcErr *rpcError) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := c.pending[id]
	if ch == nil {
		return
	}
	delete(c.pending, id)
	if rpcErr != nil {
		ch <- reply{err: errors.New(strings.TrimSpace(rpcErr.Message))}
		return
	}
	ch <- reply{result: result}
}

// actionTimeout bounds one command by its class: an interaction answers in
// seconds, while download_file streams up to 32 MiB through the browser's own
// connection and needs the wider bound. The socket stays alive through it by
// the extension's pings against the read deadline.
func actionTimeout(action string) time.Duration {
	if action == "download_file" {
		return 10 * time.Minute
	}
	return 2 * time.Minute
}

// Request is one action a chat asks of a browser.
type Request struct {
	SessionID string
	// SessionTitle is shown by the extension next to the tab being worked on.
	SessionTitle string
	BrowserID    string
	Action       string
	TabID        int
	Params       json.RawMessage
}

type commandParams struct {
	Action  string          `json:"action"`
	TabID   int             `json:"tabId,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Grant   string          `json:"grant"`
	Session commandSession  `json:"session"`
}

type commandSession struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// Call sends one action and waits for its answer. It never retries: a lost
// answer to a click is an unknown outcome, not permission to click twice.
func (h *Hub) Call(ctx context.Context, req Request) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout(req.Action))
	defer cancel()
	h.mu.Lock()
	c := h.connections[req.BrowserID]
	if c == nil {
		h.mu.Unlock()
		return nil, errors.New("the browser is not connected; ask the user to open Coddy for Browser")
	}
	if !slices.Contains(c.actions, req.Action) {
		h.mu.Unlock()
		return nil, fmt.Errorf("this browser does not support %q", req.Action)
	}
	if !slices.Contains(tablessActions, req.Action) {
		tab, ok := c.tabs[req.TabID]
		if !ok {
			h.mu.Unlock()
			return nil, errors.New("the tab is not shared; call list, open a tab with open_tab, or ask the user to drag the tab into the Coddy tab group")
		}
		if tab.SessionID != "" && tab.SessionID != req.SessionID {
			h.mu.Unlock()
			return nil, errors.New("the tab is in another chat's tab group; it is free when that chat releases it or the user moves it")
		}
	} else {
		req.TabID = 0
	}
	if req.SessionTitle == "" && h.title != nil {
		req.SessionTitle = h.title(req.SessionID)
	}
	id := randomID()
	frame, err := request(id, methodCommand, commandParams{
		Action: req.Action, TabID: req.TabID, Params: req.Params, Grant: c.grant,
		Session: commandSession{ID: req.SessionID, Title: req.SessionTitle},
	})
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	answer := make(chan reply, 1)
	c.pending[id] = answer
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(c.pending, id); h.mu.Unlock() }()

	select {
	case c.out <- frame:
	case <-c.done:
		return nil, errors.New("the browser disconnected; the action did not start")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-answer:
		if r.err != nil {
			return nil, r.err
		}
		return r.result, nil
	case <-c.done:
		return nil, errors.New("the browser disconnected; the outcome of the action is unknown, inspect the page before trying again")
	case <-ctx.Done():
		if frame, err := notification(methodCancel, map[string]string{"id": id}); err == nil {
			select {
			case c.out <- frame:
			default:
			}
		}
		return nil, fmt.Errorf("the browser action was cancelled or timed out; inspect the page before trying again: %w", ctx.Err())
	}
}
