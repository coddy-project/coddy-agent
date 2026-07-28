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

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
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

// rpcResult carries one JSON-RPC outcome to a pending call.
type rpcResult struct {
	result json.RawMessage
	err    error
}

// Client connects to a single MCP server and exposes its tools.
type Client struct {
	name string
	tr   transport
	log  *slog.Logger

	nextID  atomic.Int64
	pending map[interface{}]chan rpcResult
	mu      sync.Mutex

	tools []ToolInfo
	done  chan struct{}
}

// newClientWithTransport wraps a started transport, performs the MCP
// handshake, and caches the server's tool list. The transport is closed on
// handshake failure.
func newClientWithTransport(ctx context.Context, name string, tr transport, log *slog.Logger) (*Client, error) {
	c := &Client{
		name:    name,
		tr:      tr,
		log:     log,
		pending: make(map[interface{}]chan rpcResult),
		done:    make(chan struct{}),
	}

	go c.readLoop()

	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp %s: initialize: %w", name, err)
	}

	if err := c.listTools(ctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp %s: list tools: %w", name, err)
	}

	return c, nil
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
		name:    name,
		tools:   tools,
		log:     slog.Default(),
		pending: make(map[interface{}]chan rpcResult),
		done:    make(chan struct{}),
	}
}

// Tools returns the tools exposed by this MCP server.
func (c *Client) Tools() []ToolInfo {
	return c.tools
}

// Name returns the server name.
func (c *Client) Name() string {
	return c.name
}

// CallTool invokes a tool on the MCP server and returns the result.
func (c *Client) CallTool(ctx context.Context, toolName, argsJSON string) (string, error) {
	var args interface{}
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("mcp callTool parse args: %w", err)
		}
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

// Close stops the connection (and the subprocess for stdio transports).
func (c *Client) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	if c.tr != nil {
		return c.tr.Close()
	}
	return nil
}

// ---- internal ----

func (c *Client) initialize(ctx context.Context) error {
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

func (c *Client) listTools(ctx context.Context) error {
	result, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return err
	}

	var resp struct {
		Tools []struct {
			Name        string      `json:"name"`
			Description string      `json:"description"`
			InputSchema interface{} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("parse tools/list: %w", err)
	}

	c.tools = make([]ToolInfo, len(resp.Tools))
	for i, t := range resp.Tools {
		c.tools[i] = ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return nil
}

func (c *Client) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
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
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) notify(ctx context.Context, method string, params interface{}) error {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	return c.send(ctx, msg)
}

func (c *Client) send(ctx context.Context, v interface{}) error {
	if c.tr == nil {
		return fmt.Errorf("mcp %s: no transport (static client)", c.name)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.tr.Send(ctx, data)
}

func (c *Client) readLoop() {
	msgs := c.messagesOrNil()
	if msgs == nil {
		return
	}
	for {
		select {
		case data, ok := <-msgs:
			if !ok {
				// Transport died: fail every waiter instead of letting calls
				// hang until their ctx expires.
				c.failPending(fmt.Errorf("mcp %s: connection closed", c.name))
				return
			}
			c.dispatch(data)
		case <-c.done:
			return
		}
	}
}

// failPending resolves every in-flight call with err.
func (c *Client) failPending(err error) {
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

func (c *Client) messagesOrNil() <-chan []byte {
	if c.tr == nil {
		return nil
	}
	return c.tr.Messages()
}

// dispatch routes one server->client JSON-RPC message to its pending call.
func (c *Client) dispatch(data []byte) {
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

	var id interface{}
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

// stdioTransport speaks newline-delimited JSON-RPC with a subprocess.
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	msgs   chan []byte
	done   chan struct{}
	cancel context.CancelFunc
}

// newStdioTransport starts the subprocess on a transport-owned lifetime: the
// connect ctx only bounds the handshake (in Client.call), never the process.
// Tying the process to the caller's ctx would kill it as soon as the
// session-creating HTTP request finishes, breaking every later turn.
func newStdioTransport(_ context.Context, name, command string, args []string, env []string, log *slog.Logger) (*stdioTransport, error) {
	procCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, command, args...)
	cmd.Env = append(os.Environ(), env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("mcp %s: stdin pipe: %w", name, err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("mcp %s: stdout pipe: %w", name, err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("mcp %s: start: %w", name, err)
	}

	t := &stdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		msgs:   make(chan []byte, 16),
		done:   make(chan struct{}),
		cancel: cancel,
	}
	go func() {
		defer close(t.msgs)
		// Reap the subprocess once its stdout closes so it never lingers as
		// a zombie (probes and session teardown both end up here).
		defer func() { _ = cmd.Wait() }()
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
	_, err := t.stdin.Write(append(data, '\n'))
	return err
}

func (t *stdioTransport) Messages() <-chan []byte { return t.msgs }

func (t *stdioTransport) Close() error {
	select {
	case <-t.done:
	default:
		close(t.done)
	}
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	// Cancel the process lifetime; CommandContext kills it and the reader
	// goroutine reaps it.
	t.cancel()
	return nil
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
