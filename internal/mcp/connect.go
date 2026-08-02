package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// EffectiveTransport reports the transport Connect will use for srv: the
// explicit type when set, else "http" for url-only entries, else "stdio".
func EffectiveTransport(srv config.MCPServerConfig) string {
	typ := strings.ToLower(strings.TrimSpace(srv.Type))
	if typ != "" {
		return typ
	}
	if strings.TrimSpace(srv.Command) == "" && strings.TrimSpace(srv.URL) != "" {
		return "http"
	}
	return "stdio"
}

// SupportedTransport reports whether Connect can handle the transport name.
func SupportedTransport(typ string) bool {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "stdio", "http", "sse", "streamable-http", "streamable_http":
		return true
	default:
		return false
	}
}

// Connect establishes a client for one configured MCP server, dispatching on
// its transport type: "stdio" (default) runs a local command, "http" (also
// accepted: "streamable-http", "streamable_http") speaks streamable HTTP with
// a legacy-SSE fallback, "sse" forces the legacy HTTP+SSE transport. ${CWD}
// placeholders in args, env, headers, and url resolve against cwd.
func Connect(ctx context.Context, srv config.MCPServerConfig, cwd string, log *slog.Logger) (*Client, error) {
	switch EffectiveTransport(srv) {
	case "stdio":
		if strings.TrimSpace(srv.Command) == "" {
			return nil, fmt.Errorf("mcp %s: command is required for stdio transport", srv.Name)
		}
		args := make([]string, len(srv.Args))
		for i, a := range srv.Args {
			args[i] = config.ExpandCWD(a, cwd)
		}
		env := make([]string, len(srv.Env))
		for i, e := range srv.Env {
			env[i] = e.Name + "=" + config.ExpandCWD(e.Value, cwd)
		}
		return NewStdioClient(ctx, srv.Name, srv.Command, args, env, log)
	case "http", "streamable-http", "streamable_http":
		return NewHTTPClient(ctx, srv.Name, config.ExpandCWD(srv.URL, cwd), expandHeaders(srv, cwd), log)
	case "sse":
		return NewSSEClient(ctx, srv.Name, config.ExpandCWD(srv.URL, cwd), expandHeaders(srv, cwd), log)
	default:
		return nil, fmt.Errorf("unsupported MCP transport: %s", srv.Type)
	}
}

func expandHeaders(srv config.MCPServerConfig, cwd string) map[string]string {
	if len(srv.Headers) == 0 {
		return nil
	}
	headers := make(map[string]string, len(srv.Headers))
	for _, h := range srv.Headers {
		headers[h.Name] = config.ExpandCWD(h.Value, cwd)
	}
	return headers
}
