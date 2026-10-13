// Package mcp implements an MCP (Model Context Protocol) client.
// It connects to MCP servers over stdio, streamable HTTP, or legacy SSE
// transports and exposes their tools.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// ToolInfo describes a tool provided by an MCP server.
type ToolInfo struct {
	Name        string
	Description string
	InputSchema interface{}
}

// transport delivers JSON-RPC messages to and from an MCP server. Send ships
// one client->server message; Messages streams server->client messages.
type transport interface {
	Send(ctx context.Context, data []byte) error
	Messages() <-chan []byte
	Close() error
}

// stopper is a transport whose Close starts a shutdown that ends later: a
// stdio server is given time to exit on its own before its process group is
// terminated, a streamable HTTP session is ended with a DELETE. Stopped is
// closed once that is over.
type stopper interface {
	Stopped() <-chan struct{}
}

// loser is a transport that can tell the connection is gone for good while
// its message stream stays open: a streamable HTTP server that no longer
// knows the session. Lost is closed then.
type loser interface {
	Lost() <-chan struct{}
}

// rpcResult carries one JSON-RPC outcome to a pending call.
type rpcResult struct {
	result json.RawMessage
	err    error
}

// conn is one live connection to an MCP server: the transport, the calls in
// flight and the tool list the server gave at the handshake. A Client is a
// handle on a conn; the Pool hands several handles out on one conn.
type conn struct {
	name string
	tr   transport
	log  *slog.Logger

	nextID  atomic.Int64
	pending map[interface{}]chan rpcResult
	mu      sync.Mutex

	tools []ToolInfo

	// done is closed by close. gone is closed by close as well, and when the
	// transport's stream ends on its own: the server exited or dropped the
	// connection, and nothing sent over it will be answered any more.
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
	gone      chan struct{}
	goneOnce  sync.Once
}

// Client is a handle on a connection to one MCP server. A client Connect (or
// NewStdioClient, NewHTTPClient, NewSSEClient) returns owns its connection:
// Close ends it. A client a Pool hands out is a lease on a connection other
// sessions may hold as well: Close gives the lease back, and the pool ends the
// connection once nothing holds it and the pool does not keep it. Either way a
// closed client refuses further calls.
type Client struct {
	name string
	conn *conn
	// release gives a pool lease back; nil for a client that owns its
	// connection.
	release func()
	closed  atomic.Bool

	// declared is the fingerprint of the declaration the client was started
	// from (TrustGate.Connect, Pool.Acquire), empty for one neither started.
	// A reconcile compares it to the declaration on disk, so a configured
	// server whose command was edited is started again.
	declared string
	// poolKey is the PoolKey a lease was taken under, empty for a client
	// that owns its connection.
	poolKey string
}

// newConn wraps a started transport, performs the MCP handshake, and caches
// the server's tool list. The transport is closed on handshake failure.
func newConn(ctx context.Context, name string, tr transport, log *slog.Logger) (*conn, error) {
	c := &conn{
		name:    name,
		tr:      tr,
		log:     log,
		pending: make(map[interface{}]chan rpcResult),
		done:    make(chan struct{}),
		gone:    make(chan struct{}),
	}

	go c.readLoop()

	if err := c.initialize(ctx); err != nil {
		c.closeAndWait()
		return nil, fmt.Errorf("mcp %s: initialize: %w", name, err)
	}

	if err := c.listTools(ctx); err != nil {
		c.closeAndWait()
		return nil, fmt.Errorf("mcp %s: list tools: %w", name, err)
	}

	return c, nil
}

// closeAndWait ends a connection whose handshake failed and returns once the
// server behind it is gone, so a failed start leaves no process behind for
// the caller's next try to run beside.
func (c *conn) closeAndWait() {
	_ = c.close()
	<-c.stopped()
}

// newClientWithTransport connects over a started transport and returns a
// client that owns the connection.
func newClientWithTransport(ctx context.Context, name string, tr transport, log *slog.Logger) (*Client, error) {
	c, err := newConn(ctx, name, tr, log)
	if err != nil {
		return nil, err
	}
	return &Client{name: name, conn: c}, nil
}

// NewStdioClient starts an MCP server subprocess and connects to it.
func NewStdioClient(ctx context.Context, name, command string, args []string, env []string, log *slog.Logger) (*Client, error) {
	tr, err := newStdioTransport(ctx, name, command, args, env, log)
	if err != nil {
		return nil, err
	}
	return newClientWithTransport(ctx, name, tr, log)
}

// NewStaticClient returns a Client carrying only a name and a fixed tool
// list, without a connection. Used by tests and stubs; CallTool fails.
func NewStaticClient(name string, tools []ToolInfo) *Client {
	return &Client{
		name: name,
		conn: &conn{
			name:    name,
			tools:   tools,
			log:     slog.Default(),
			pending: make(map[interface{}]chan rpcResult),
			done:    make(chan struct{}),
			gone:    make(chan struct{}),
		},
	}
}

// Tools returns the tools exposed by this MCP server.
func (c *Client) Tools() []ToolInfo {
	return c.conn.tools
}

// Name returns the server name.
func (c *Client) Name() string {
	return c.name
}

// Declared returns the fingerprint of the configured declaration the client
// was started from, or "" for one no trust gate started.
func (c *Client) Declared() string {
	return c.declared
}

// PoolKey returns the PoolKey a pool lease was taken under - what the server
// resolves to and, for one tied to a workspace, the workspace - or "" for a
// client that owns its connection.
func (c *Client) PoolKey() string {
	return c.poolKey
}

// Alive reports whether calls through the client can still reach the
// server: false once the client was closed, or the connection ended - the
// server exited or dropped it. A client without a connection
// (NewStaticClient) is alive until closed.
func (c *Client) Alive() bool {
	if c.closed.Load() {
		return false
	}
	select {
	case <-c.conn.gone:
		return false
	default:
		return true
	}
}

// CallTool invokes a tool on the MCP server and returns the result.
func (c *Client) CallTool(ctx context.Context, toolName, argsJSON string) (string, error) {
	if c.closed.Load() {
		return "", fmt.Errorf("mcp %s: client closed", c.name)
	}
	return c.conn.callTool(ctx, toolName, argsJSON)
}

// Close lets the server go: a client that owns its connection ends it, a
// pool lease is given back. Closing twice is a no-op.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if c.release != nil {
		c.release()
		return nil
	}
	return c.conn.close()
}

// ---- internal ----

// callTool invokes a tool over the connection and returns its text result.
func (c *conn) callTool(ctx context.Context, toolName, argsJSON string) (string, error) {
	// The arguments go to the server as the JSON the model wrote, checked
	// rather than decoded into an arbitrary value.
	var args json.RawMessage
	if argsJSON != "" {
		if !json.Valid([]byte(argsJSON)) {
			return "", fmt.Errorf("mcp callTool parse args: invalid JSON")
		}
		args = json.RawMessage(argsJSON)
	}

	result, err := c.call(ctx, "tools/call", map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}

	// Parse MCP tool result.
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return string(result), nil
	}

	var parts []string
	for _, c := range resp.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	text := strings.Join(parts, "\n")

	if resp.IsError {
		return "", fmt.Errorf("mcp tool error: %s", text)
	}
	return text, nil
}

// close ends the connection (and, for stdio, the server's process group) and
// fails the calls still waiting for an answer. Only the first call does
// anything: the pool and a lease can end one connection at the same moment.
func (c *conn) close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.markGone()
		c.failPending(fmt.Errorf("mcp %s: connection closed", c.name))
		if c.tr != nil {
			c.closeErr = c.tr.Close()
		}
	})
	return c.closeErr
}

// markGone records that nothing sent over the connection is answered any more.
func (c *conn) markGone() {
	c.goneOnce.Do(func() { close(c.gone) })
}

// stopped is closed once the server behind the connection no longer runs: at
// once for a remote connection, once its process group is gone for stdio.
func (c *conn) stopped() <-chan struct{} {
	if s, ok := c.tr.(stopper); ok {
		return s.Stopped()
	}
	done := make(chan struct{})
	close(done)
	return done
}

func (c *conn) initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "coddy-agent",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return err
	}
	// Send initialized notification.
	return c.notify(ctx, "notifications/initialized", nil)
}

// listTools asks the server for its tools at the handshake and keeps the list
// as the connection's: every session holding the connection is offered it.
func (c *conn) listTools(ctx context.Context) error {
	tools, err := c.fetchTools(ctx)
	if err != nil {
		return err
	}
	c.tools = tools
	return nil
}

// fetchTools asks the server for the tools it offers now, without touching
// the list the connection was opened with.
func (c *conn) fetchTools(ctx context.Context) ([]ToolInfo, error) {
	result, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Tools []struct {
			Name        string      `json:"name"`
			Description string      `json:"description"`
			InputSchema interface{} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}

	tools := make([]ToolInfo, len(resp.Tools))
	for i, t := range resp.Tools {
		tools[i] = ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return tools, nil
}

func (c *conn) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	// A connection that ended answers nothing: fail at once rather than
	// sending into it and waiting for the caller's context.
	select {
	case <-c.gone:
		return nil, fmt.Errorf("mcp %s: connection closed", c.name)
	default:
	}

	id := c.nextID.Add(1)
	ch := make(chan rpcResult, 1)

	c.mu.Lock()
	c.pending[float64(id)] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, float64(id))
		c.mu.Unlock()
	}()

	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}

	if err := c.send(ctx, msg); err != nil {
		return nil, err
	}

	select {
	case res := <-ch:
		return res.result, res.err
	case <-c.gone:
		// The connection went while the call waited; an answer that made it
		// in first still wins.
		select {
		case res := <-ch:
			return res.result, res.err
		default:
			return nil, fmt.Errorf("mcp %s: connection closed", c.name)
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *conn) notify(ctx context.Context, method string, params interface{}) error {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	return c.send(ctx, msg)
}

func (c *conn) send(ctx context.Context, v interface{}) error {
	if c.tr == nil {
		return fmt.Errorf("mcp %s: no transport (static client)", c.name)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.tr.Send(ctx, data)
}

func (c *conn) readLoop() {
	msgs := c.messagesOrNil()
	if msgs == nil {
		return
	}
	var lost <-chan struct{}
	if l, ok := c.tr.(loser); ok {
		lost = l.Lost()
	}
	for {
		select {
		case data, ok := <-msgs:
			if !ok {
				// Transport died: fail every waiter instead of letting calls
				// hang until their ctx expires.
				c.markGone()
				c.failPending(fmt.Errorf("mcp %s: connection closed", c.name))
				return
			}
			c.dispatch(data)
		case <-lost:
			c.markGone()
			c.failPending(fmt.Errorf("mcp %s: the server no longer knows this session", c.name))
			return
		case <-c.done:
			return
		}
	}
}

// failPending resolves every in-flight call with err.
func (c *conn) failPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		select {
		case ch <- rpcResult{err: err}:
		default:
		}
		delete(c.pending, id)
	}
}

func (c *conn) messagesOrNil() <-chan []byte {
	if c.tr == nil {
		return nil
	}
	return c.tr.Messages()
}

// dispatch routes one server->client JSON-RPC message to its pending call.
func (c *conn) dispatch(data []byte) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		c.log.Warn("mcp invalid json", "server", c.name, "data", string(data))
		return
	}

	// It's a response if it has an id and result/error.
	idRaw, hasID := raw["id"]
	_, hasResult := raw["result"]
	_, hasError := raw["error"]

	if !hasID || (!hasResult && !hasError) {
		// Ignore notifications from MCP servers for now.
		return
	}

	// Calls go out with numeric ids and wait under them as float64; an id
	// of any other kind answers nothing of ours.
	var id float64
	if err := json.Unmarshal(idRaw, &id); err != nil {
		return
	}

	c.mu.Lock()
	ch, ok := c.pending[id]
	c.mu.Unlock()

	if !ok {
		return
	}

	if hasResult {
		ch <- rpcResult{result: raw["result"]}
		return
	}
	// Propagate the JSON-RPC error object so callers see real failures
	// (unknown tool, invalid params) instead of a silent empty success.
	var rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw["error"], &rpcErr)
	ch <- rpcResult{err: fmt.Errorf("mcp %s: jsonrpc error %d: %s", c.name, rpcErr.Code, rpcErr.Message)}
}

// ---- stdio transport ----

// stdioExitGrace is how long a closed stdio server is given to exit on its
// own once its stdin is closed - the shutdown MCP asks a client to start with -
// before its process group is terminated, and stdioTermGrace how long the
// group then gets between SIGTERM and SIGKILL.
const (
	stdioExitGrace = 2 * time.Second
	stdioTermGrace = 3 * time.Second
)

// stdioTransport speaks newline-delimited JSON-RPC with a subprocess.
type stdioTransport struct {
	name  string
	log   *slog.Logger
	cmd   *exec.Cmd
	stdin io.WriteCloser
	// writeMu keeps one message whole on stdin while sessions sharing the
	// server send side by side (os.File serializes writes as well; the
	// transport does not lean on that). Close does not take it: closing the
	// pipe is what wakes a write stuck on a server that stopped reading.
	writeMu sync.Mutex
	msgs    chan []byte
	done    chan struct{}
	// exited is closed once the server process has been waited for, stopped
	// once Close has finished with its process group.
	exited    chan struct{}
	stopped   chan struct{}
	closeOnce sync.Once
	// exitGrace and termGrace are stdioExitGrace and stdioTermGrace for this
	// server; a test shortens them.
	exitGrace time.Duration
	termGrace time.Duration
	// closeJob ends the Windows job object the server runs in, and with it
	// whatever of its tree is left (platform.KillTreeOnClose); a no-op
	// elsewhere.
	closeJob func()
}

// newStdioTransport starts the subprocess on a transport-owned lifetime: the
// connect ctx only bounds the handshake (in conn.call), never the process.
// Tying the process to the caller's ctx would kill it as soon as the
// session-creating HTTP request finishes, breaking every later turn.
//
// The server runs in a process group of its own. A package runner (npx, uvx)
// starts the actual server as a child of its own, and Close has to stop that
// child as well, not only the process Coddy started.
func newStdioTransport(_ context.Context, name, command string, args []string, env []string, log *slog.Logger) (*stdioTransport, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(), env...)
	platform.DetachProcessGroup(cmd)
	platform.AdaptCommand(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp %s: stdin pipe: %w", name, err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp %s: stdout pipe: %w", name, err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s: start: %w", name, err)
	}
	closeJob, err := platform.KillTreeOnClose(cmd)
	if err != nil {
		log.Debug("mcp server runs without a kill-on-close job", "server", name, "error", err)
	}

	t := &stdioTransport{
		name:      name,
		log:       log,
		cmd:       cmd,
		stdin:     stdin,
		msgs:      make(chan []byte, 16),
		done:      make(chan struct{}),
		exited:    make(chan struct{}),
		stopped:   make(chan struct{}),
		exitGrace: stdioExitGrace,
		termGrace: stdioTermGrace,
		closeJob:  closeJob,
	}
	go func() {
		defer close(t.msgs)
		// Reap the subprocess once its stdout closes so it never lingers as
		// a zombie (probes and session teardown both end up here).
		defer func() {
			_ = cmd.Wait()
			close(t.exited)
		}()
		reader := bufio.NewReader(stdout)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err != io.EOF {
					log.Error("mcp read error", "server", name, "error", err)
				}
				return
			}
			select {
			case t.msgs <- line:
			case <-t.done:
				return
			}
		}
	}()
	return t, nil
}

func (t *stdioTransport) Send(_ context.Context, data []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err := t.stdin.Write(append(data, '\n'))
	return err
}

func (t *stdioTransport) Messages() <-chan []byte { return t.msgs }

// Close closes the server's stdin and returns; the rest of the shutdown
// (stop) runs in the background, and Stopped says when it is over.
func (t *stdioTransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.done)
		if t.stdin != nil {
			_ = t.stdin.Close()
		}
		go t.stop()
	})
	return nil
}

// Stopped is closed once the server and its process group are gone.
func (t *stdioTransport) Stopped() <-chan struct{} { return t.stopped }

// stop ends the server the way MCP asks a client to: with stdin closed the
// server gets stdioExitGrace to exit on its own, then its process group is
// terminated. The group is terminated whether the server exited by then or
// not, since what it started - the node a package runner spawned - can
// outlive it.
func (t *stdioTransport) stop() {
	defer close(t.stopped)
	select {
	case <-t.exited:
	case <-time.After(t.exitGrace):
	}
	if err := platform.TerminateProcessGroup(t.cmd, t.termGrace); err != nil {
		t.log.Warn("mcp server process group did not stop", "server", t.name, "error", err)
	}
	t.closeJob()
	select {
	case <-t.exited:
	case <-time.After(t.termGrace):
	}
}

// ToLLMToolDefinition converts an MCP ToolInfo to an LLM tool definition.
func (t ToolInfo) ToLLMToolDefinition(serverName string) llm.ToolDefinition {
	name := serverName + "__" + t.Name
	return llm.ToolDefinition{
		Name:        name,
		Description: fmt.Sprintf("[%s] %s", serverName, t.Description),
		InputSchema: t.InputSchema,
	}
}
