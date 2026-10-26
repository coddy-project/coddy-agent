//go:build browser

// Package browsertest is a fake Coddy for Browser extension for tests: it
// dials a real listener over a real WebSocket, speaks the protocol of
// docs/reference/browser-protocol.md and lets a test answer every command.
package browsertest

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// Tab is a tab as the extension reports it. SessionID names the chat whose
// tab group holds it; empty is the shared group.
type Tab struct {
	ID        int    `json:"id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	SessionID string `json:"sessionId,omitempty"`
}

// Command is a browser/command request as the extension receives it.
type Command struct {
	ID      string          `json:"-"`
	Action  string          `json:"action"`
	TabID   int             `json:"tabId"`
	Params  json.RawMessage `json:"params"`
	Grant   string          `json:"grant"`
	Session struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"session"`
}

// Answer is what a handler replies with: a result, or an error message.
type Answer struct {
	Result any
	Error  string
}

// Extension is one connected fake browser.
type Extension struct {
	ws    *websocket.Conn
	mu    sync.Mutex
	grant string
	// Commands receives every command the server sent, in order, once its
	// answer has been written back.
	Commands chan Command
	// Handle answers a command. It runs on the reading goroutine; the default
	// answers {"ok":true}.
	Handle func(Command) Answer
	// Notes receives every notification the server sent ($/cancelRequest).
	Notes chan string
	done  chan struct{}
}

type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Options say how the fake introduces itself.
type Options struct {
	Key     string
	ID      string
	Name    string
	Actions []string
	// Version overrides the protocol version initialize asks for.
	Version int
	// Origin overrides the extension origin of the handshake.
	Origin string
}

// AllActions is every action the real extension implements.
var AllActions = []string{
	"open_tab", "close_tab", "release", "snapshot", "screenshot", "navigate",
	"click", "type", "press", "hover", "scroll", "drag", "upload_file", "download_file",
}

// RPCError is the error answer of initialize.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

// Connect dials baseURL (http://host:port) and completes initialize.
func Connect(baseURL string, opts Options) (*Extension, error) {
	if opts.ID == "" {
		opts.ID = "browser-test-0001"
	}
	if opts.Name == "" {
		opts.Name = "Chrome - test"
	}
	if opts.Actions == nil {
		opts.Actions = AllActions
	}
	if opts.Version == 0 {
		opts.Version = 1
	}
	if opts.Origin == "" {
		opts.Origin = "chrome-extension://coddyfortestsonly"
	}
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(baseURL, "http")+"/browser/v1/connect", "", opts.Origin)
	if err != nil {
		return nil, err
	}
	params, _ := json.Marshal(map[string]any{
		"protocolVersion": opts.Version, "key": opts.Key,
		"browser": map[string]string{"id": opts.ID, "name": opts.Name, "version": "0.1.0-test"},
		"actions": opts.Actions,
	})
	if err := websocket.JSON.Send(ws, frame{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "initialize", Params: params}); err != nil {
		_ = ws.Close()
		return nil, err
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var answer frame
	if err := websocket.JSON.Receive(ws, &answer); err != nil {
		_ = ws.Close()
		return nil, err
	}
	_ = ws.SetReadDeadline(time.Time{})
	if answer.Error != nil {
		_ = ws.Close()
		return nil, &RPCError{Code: answer.Error.Code, Message: answer.Error.Message}
	}
	e := &Extension{ws: ws, Commands: make(chan Command, 64), Notes: make(chan string, 64), done: make(chan struct{})}
	go e.read()
	return e, nil
}

func (e *Extension) read() {
	defer close(e.done)
	for {
		var f frame
		if err := websocket.JSON.Receive(e.ws, &f); err != nil {
			return
		}
		switch {
		case f.Method == "browser/command" && len(f.ID) > 0:
			var cmd Command
			_ = json.Unmarshal(f.Params, &cmd)
			_ = json.Unmarshal(f.ID, &cmd.ID)
			handle := e.Handle
			if handle == nil {
				handle = func(Command) Answer { return Answer{Result: map[string]bool{"ok": true}} }
			}
			a := handle(cmd)
			reply := frame{JSONRPC: "2.0", ID: f.ID}
			if a.Error != "" {
				reply.Error = &struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				}{Code: -32000, Message: a.Error}
			} else {
				reply.Result, _ = json.Marshal(a.Result)
			}
			e.mu.Lock()
			err := websocket.JSON.Send(e.ws, reply)
			e.mu.Unlock()
			if err != nil {
				return
			}
			e.Commands <- cmd
		case f.Method != "" && len(f.ID) == 0:
			e.Notes <- f.Method + " " + string(f.Params)
		}
	}
}

// Notify sends a notification to the server.
func (e *Extension) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return websocket.JSON.Send(e.ws, frame{JSONRPC: "2.0", Method: method, Params: raw})
}

// Send writes a raw frame, for tests of what the server does with one it did
// not expect.
func (e *Extension) Send(v any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return websocket.JSON.Send(e.ws, v)
}

// Share is the user changing what is shared: the tabs the agent may reach,
// under a fresh grant, so a command sent for the previous selection is
// refused by the extension.
func (e *Extension) Share(tabs ...Tab) error {
	e.mu.Lock()
	e.grant = fmt.Sprintf("grant-%d", time.Now().UnixNano())
	e.mu.Unlock()
	return e.Publish(tabs...)
}

// Publish announces the tabs under the current grant: what the extension
// does when a shared tab navigates, or when the agent opened one.
func (e *Extension) Publish(tabs ...Tab) error {
	if tabs == nil {
		tabs = []Tab{}
	}
	return e.Notify("browser/tabs", map[string]any{"tabs": tabs, "grant": e.Grant()})
}

// Grant is the grant of the latest Share.
func (e *Extension) Grant() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.grant
}

// Next waits for the next command the server sent.
func (e *Extension) Next(timeout time.Duration) (Command, error) {
	select {
	case c := <-e.Commands:
		return c, nil
	case <-e.done:
		return Command{}, errors.New("the server closed the socket")
	case <-time.After(timeout):
		return Command{}, errors.New("no command arrived")
	}
}

// Closed is closed once the server ends the socket.
func (e *Extension) Closed() <-chan struct{} { return e.done }

// Close ends the socket.
func (e *Extension) Close() error { return e.ws.Close() }
