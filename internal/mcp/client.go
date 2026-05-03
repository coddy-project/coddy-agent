// Package mcp implements an MCP (Model Context Protocol) client.
// It connects to MCP servers via stdio transport and exposes their tools.
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

// Client connects to a single MCP server and exposes its tools.
type Client struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	log    *slog.Logger

	nextID  atomic.Int64
	pending map[interface{}]chan json.RawMessage
	mu      sync.Mutex

	tools []ToolInfo
	ready chan struct{}
}

// NewStdioClient starts an MCP server subprocess and connects to it.
func NewStdioClient(ctx context.Context, name, command string, args []string, env []string, log *slog.Logger) (*Client, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append(os.Environ(), env...)

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

	c := &Client{
		name:    name,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		log:     log,
		pending: make(map[interface{}]chan json.RawMessage),
		ready:   make(chan struct{}),
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

	text := ""
	for i, p := range parts {
		if i > 0 {
			text += "\n"
		}
		text += p
	}

	if resp.IsError {
		return "", fmt.Errorf("mcp tool error: %s", text)
	}
	return text, nil
}

// Close stops the MCP server subprocess.
func (c *Client) Close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
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
	return c.notify("notifications/initialized", nil)
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
	ch := make(chan json.RawMessage, 1)

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

	if err := c.send(msg); err != nil {
		return nil, err
	}

	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) notify(method string, params interface{}) error {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	return c.send(msg)
}

func (c *Client) send(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

func (c *Client) readLoop() {
	for {
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				c.log.Error("mcp read error", "server", c.name, "error", err)
			}
			return
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			c.log.Warn("mcp invalid json", "server", c.name, "data", string(line))
			continue
		}

		// It's a response if it has an id and result/error.
		idRaw, hasID := raw["id"]
		_, hasResult := raw["result"]
		_, hasError := raw["error"]

		if hasID && (hasResult || hasError) {
			var id interface{}
			if err := json.Unmarshal(idRaw, &id); err != nil {
				continue
			}

			c.mu.Lock()
			ch, ok := c.pending[id]
			c.mu.Unlock()

			if !ok {
				continue
			}

			if hasResult {
				ch <- raw["result"]
			} else {
				// On error, send empty result.
				ch <- json.RawMessage(`null`)
			}
		}
		// Ignore notifications from MCP servers for now.
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
