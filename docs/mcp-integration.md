# MCP Server Integration

## Overview

The agent supports connecting to external MCP (Model Context Protocol) servers, which provide
additional tools and resources. MCP servers can be configured at three levels:

1. **Global** - defined in `config.yaml` (`mcp_servers`), connected for every session
2. **Project** - defined in `<workspace>/.coddy/mcp.json` (Cursor-compatible format), merged
   over the global list for sessions in that workspace; a project entry with the same name
   overrides the global definition
3. **Per-session** - provided by the ACP client in `session/new` parameters

Tools from all connected MCP servers are merged into the tool list passed to the LLM during
the ReAct loop (in **`agent`** and **`plan`** modes).

## Project config: `.coddy/mcp.json`

The project file uses the same shape as Cursor's `mcp.json`: a single `mcpServers` object
keyed by server name. `env` and `headers` are JSON objects (not the YAML name/value list),
and per-tool switches use `disabledTools`:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${CWD}"],
      "env": { "SOME_TOKEN": "value" },
      "disabled": false,
      "disabledTools": ["write_file"]
    }
  }
}
```

A broken `mcp.json` is logged and skipped; the session still starts with the global list.

## Enable / disable switches

Both config levels support switching off a whole server or individual tools without
removing their definitions:

- `config.yaml`: `disabled: true` and `disabled_tools: ["tool_a"]` per `mcp_servers` entry
- `.coddy/mcp.json`: `"disabled": true` and `"disabledTools": ["tool_a"]` per entry

Disabled servers are not connected for new sessions. Disabled tools (and all tools of a
disabled server) are hidden from the LLM's tool list and rejected at dispatch. The switches
are re-read on every agent turn, so toggling them (by editing the files or through the
HTTP API / web UI below) also applies to **already running** sessions on their next turn.

## Management API and UI

The HTTP gateway (build tag `http`) exposes the merged server list with probed tool
inventories and toggle endpoints under **`/coddy/mcp*`** (see `docs/http-api.md`), and the
bundled web UI shows them under **Settings -> MCP servers**: status dot per server,
expandable tool list with per-tool switches, and a Cursor-style JSON editor for
project-level entries. Toggles persist into the file that defines the server
(config.yaml or `.coddy/mcp.json`).

## Supported Transports

### stdio (supported)

The MCP server runs as a subprocess. Communication via stdin/stdout (newline-delimited
JSON-RPC 2.0).

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

### HTTP (declared, not yet implemented)

`type: http` with `url`/`headers` can be declared in both config levels and in ACP
`session/new` params, but the connector currently rejects non-stdio transports: the agent
advertises `mcpCapabilities.http: false`, sessions log a warning and skip such servers, and
the management API lists them with status `unsupported`. URL-only `.coddy/mcp.json` entries
are inferred as `http` and handled the same way.

## Tool Namespacing

To avoid conflicts when multiple MCP servers provide tools with the same name,
tools are namespaced using the server name:

- MCP server `filesystem` providing tool `read_file` -> available as `filesystem__read_file`
- Built-in tool `read_file` -> available as `read_file`

Because `__` separates the server and tool parts, server names must not contain `__`
(the management API rejects such names).

## Permission Model

MCP tool calls are currently dispatched without the built-in permission prompts that guard
filesystem writes and shell commands; the disable switches above are the mechanism for
restricting what a server may do. Prefer running MCP servers with least-privilege
credentials and disabling tools you do not need.

## Popular MCP Servers

### Filesystem access
```yaml
mcp_servers:
  - name: "filesystem"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-filesystem", "${CWD}"]   # session cwd when the server starts
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

1. On `session/new`, the agent connects every enabled server from the merged
   config.yaml + `.coddy/mcp.json` list, then any ACP client-supplied servers
2. The agent calls `tools/list` on each server and registers the tools
3. During the ReAct loop, when LLM calls an MCP tool, the agent forwards the call
   (unless the tool or its server has been disabled since)
4. Results are returned to the LLM as tool observations
5. On session end or `session/cancel`, MCP server connections are cleaned up

## Error Handling

- If an MCP server fails to start, the session still proceeds with a warning
- Failed MCP tool calls return an error observation to the LLM
- The LLM can decide to retry, use alternative tools, or inform the user
