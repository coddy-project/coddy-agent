# MCP Server Integration

## Overview

The agent supports connecting to external MCP (Model Context Protocol) servers, which provide
additional tools and resources. MCP servers can be configured at two levels:

1. **Global** - defined in `config.yaml`, always connected for every session
2. **Per-session** - provided by the ACP client in `session/new` parameters

Tools from all connected MCP servers are merged into the agent's tool registry and made
available to the LLM during the ReAct loop.

## Supported Transports

### stdio (always supported)

The MCP server runs as a subprocess. Communication via stdin/stdout.

Configuration in `session/new`:
```json
{
  "name": "my-server",
  "command": "/path/to/mcp-server",
  "args": ["--stdio"],
  "env": [
    { "name": "API_KEY", "value": "secret" }
  ]
}
```

Configuration in `config.yaml`:
```yaml
mcp_servers:
  - name: "filesystem"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/projects"]
    env: []
```

### HTTP (optional capability)

The agent advertises `mcpCapabilities.http: true` if HTTP MCP transport is supported.

Configuration in `session/new`:
```json
{
  "type": "http",
  "name": "api-tools",
  "url": "https://my-mcp.example.com/mcp",
  "headers": [
    { "name": "Authorization", "value": "Bearer token123" }
  ]
}
```

## Tool Namespacing

To avoid conflicts when multiple MCP servers provide tools with the same name,
tools are namespaced using the server name:

- MCP server `filesystem` providing tool `read_file` -> available as `filesystem__read_file`
- Built-in tool `read_file` -> available as `read_file`

The LLM is informed of both names in the system prompt.

## Permission Model

MCP tool calls follow the same permission model as built-in tools:
- File reads: no permission required by default
- File writes: configurable, default no permission required
- Command execution: always requires permission (configurable)
- Any tool tagged as `destructive`: always requires permission

## Popular MCP Servers

### Filesystem access
```yaml
mcp_servers:
  - name: "filesystem"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-filesystem", "${WORKSPACE}"]
```

### GitHub
```yaml
mcp_servers:
  - name: "github"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      - name: "GITHUB_PERSONAL_ACCESS_TOKEN"
        value: "${GITHUB_TOKEN}"
```

### Postgres database
```yaml
mcp_servers:
  - name: "postgres"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-postgres", "${DATABASE_URL}"]
```

### Brave Search
```yaml
mcp_servers:
  - name: "brave-search"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-brave-search"]
    env:
      - name: "BRAVE_API_KEY"
        value: "${BRAVE_API_KEY}"
```

## MCP Server Lifecycle

1. On `session/new`, the agent spawns / connects to all configured MCP servers
2. The agent calls `tools/list` on each server and registers the tools
3. During the ReAct loop, when LLM calls an MCP tool, the agent forwards the call
4. Results are returned to the LLM as tool observations
5. On session end or `session/cancel`, MCP server connections are cleaned up

## Error Handling

- If an MCP server fails to start, the session still proceeds with a warning
- Failed MCP tool calls return an error observation to the LLM
- The LLM can decide to retry, use alternative tools, or inform the user
