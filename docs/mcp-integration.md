# MCP Server Integration

## Overview

The agent supports connecting to external MCP (Model Context Protocol) servers, which provide
additional tools and resources. MCP servers can be configured at these levels:

1. **Global** (scope `global`) - `mcp_servers` in `config.yaml` and the user-global
   `~/.coddy/mcp.json` (the analogue of Cursor's `~/.cursor/mcp.json`), connected for every
   session; entries in `~/.coddy/mcp.json` override same-named `config.yaml` entries
2. **Local** (scope `local`) - `<workspace>/.coddy/mcp.json`, merged over the global list for
   sessions in that workspace; a local entry with the same name overrides the global definition
3. **Per-session** - provided by the ACP client in `session/new` parameters

Tools from all connected MCP servers are merged into the tool list passed to the LLM during
the ReAct loop (in **`agent`** and **`plan`** modes).

## mcp.json (global and local)

Both mcp.json files use the same shape as Cursor's: a single `mcpServers` object keyed by
server name. `env` and `headers` are JSON objects (not the YAML name/value list), and
per-tool switches use `disabledTools`:

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

A broken `mcp.json` is logged and skipped; the session still starts with the remaining levels.

## Workspace trust for project-local servers

Added in response to
[issue #80](https://github.com/coddy-project/coddy-agent/issues/80): before this gate existed,
creating or restoring a session for a checkout ran whatever `<workspace>/.coddy/mcp.json`
asked for, with no trust decision anywhere on the path to `cmd.Start()`.

The project file lives inside the repository, so whoever wrote the checkout picks the
`command`, `args`, and `env` of a process Coddy would start while bootstrapping a session -
before the model runs, before any tool permission prompt exists. Coddy therefore treats
`<workspace>/.coddy/mcp.json` entries as untrusted by default and starts them only after
the operator approves that exact declaration for that workspace.

- `config.yaml` and `~/.coddy/mcp.json` are operator-authored and are **not** gated;
- the policy is `mcp.project_trust` in `config.yaml`: `ask` (default), `allow` (start
  project servers automatically; only for workspaces you already trust), `deny` (never
  load them, no approval path). `coddy acp` and `coddy http` also take
  `--mcp-project-trust ask|allow|deny`, which overrides the config for that process only -
  the flag is what a CI job or a container entrypoint uses instead of editing config.yaml.
  An unknown value fails the launch rather than falling back to a default;
- approvals live in `~/.coddy/mcp-trust.json`, keyed by the canonical workspace path and by
  a SHA-256 digest of the command-bearing declaration (transport, command, args, env, url,
  headers). Each record is a receipt naming what was approved - env and header **names**
  only, never their values;
- rewriting an approved entry changes the digest and withdraws the approval, so the next
  session asks again. Enable/disable and `disabledTools` do not: they are operational
  switches, not a trust boundary;
- the gate is re-checked immediately before the process is spawned or the URL is contacted,
  on both the session path and the management probe, so listing servers never starts a
  command that has not been approved;
- every approval surface prints the **effective declaration first**: transport, the command
  with its arguments (or the URL), the **names** of the environment variables and headers it
  carries, the workspace the process would start in, and the file it was read from. Values of
  env vars and headers are never printed and never stored in the receipt - the decision is
  about which variables reach the child, not about what is in them. This is deliberately more
  than the name-only prompts of comparable agents: an approval you cannot read is not one.

An unapproved server is skipped with a warning naming the server, the workspace, the digest,
and the command to approve it; the session itself starts normally with the remaining servers.

Approve it on whichever surface you are using:

```bash
coddy mcp list
```

```bash
coddy mcp trust <name>
```

`coddy mcp list` prints each merged server with its scope, its trust state, and the command
it would run; `coddy mcp trust <name>` shows the same detail once more and then records the
approval. `coddy mcp untrust <name>` withdraws it. Both accept `--cwd DIR` to act on a
workspace other than the current directory. Over HTTP the same decisions are
**`POST /coddy/mcp/{name}/trust`** and **`POST /coddy/mcp/{name}/untrust`**, and the bundled
UI shows a shield button plus the declaration under **Settings -> MCP servers**. The policy
itself is edited in that same tab (**`POST /coddy/mcp/project-trust`**), next to the servers
it governs. A server added through the management API or the UI editor is approved by the act
of writing it - the operator typed the command themselves.

Under `allow` and `deny` there is nothing left to decide per server, so the per-server
approval control disappears from the UI entirely: `allow` starts every project server anyway
and `deny` starts none of them. The shield is offered only under `ask`.

## Enable / disable switches

Every config level supports switching off a whole server or individual tools without
removing their definitions:

- `config.yaml`: `disabled: true` and `disabled_tools: ["tool_a"]` per `mcp_servers` entry
- `~/.coddy/mcp.json` and `./.coddy/mcp.json`: `"disabled": true` and
  `"disabledTools": ["tool_a"]` per entry

Disabled servers are not connected for new sessions. Disabled tools (and all tools of a
disabled server) are hidden from the LLM's tool list and rejected at dispatch. The switches
are re-read on every agent turn, so toggling them (by editing the files or through the
HTTP API / web UI below) also applies to **already running** sessions on their next turn.

## Management API and UI

The HTTP gateway (build tag `http`) exposes the merged server list with probed tool
inventories and toggle endpoints under **`/coddy/mcp*`** (see `docs/http-api.md`), and the
bundled web UI shows them under **Settings -> MCP servers**: status dot per server, a
`global` / `local` scope badge, expandable tool list with per-tool switches, and a
Cursor-style JSON editor for mcp.json entries with a scope picker (global writes
`~/.coddy/mcp.json`, local writes `./.coddy/mcp.json`). Toggles persist into the file that
defines the server; `config.yaml` entries are toggle-only here and edited in Settings.

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

### Streamable HTTP (supported)

`type: http` connects to `url` over the
MCP streamable HTTP transport (2025-03-26 spec): JSON-RPC messages are POSTed to the
endpoint and answered as `application/json` bodies or `text/event-stream` chunks; the
`Mcp-Session-Id` issued on initialize is echoed on subsequent requests. `headers` are sent
with every request (e.g. `Authorization`). URL-only entries (no `command`, no `type`)
default to `http`. When the endpoint rejects the handshake (legacy servers answer POST
with 4xx), the client automatically falls back to the legacy SSE transport at the same
URL, mirroring Cursor and Claude Code behavior. The agent advertises
`mcpCapabilities.http: true` and `mcpCapabilities.sse: true`. In YAML the `type` value
must be `stdio`, `http`, or `sse`; mcp.json and ACP entries additionally accept the
`streamable-http` / `streamable_http` aliases for `http`.

```yaml
mcp_servers:
  - name: "remote-tools"
    type: "http"
    url: "https://mcp.example.com/mcp"
    headers:
      - name: "Authorization"
        value: "Bearer ${MCP_TOKEN}"
```

### SSE (supported, legacy)

`type: sse` forces the 2024-11-05 HTTP+SSE transport: a GET stream at `url` announces the
POST endpoint in its first event and then carries every server-to-client message. Use it
for servers that only implement the older protocol; `type: http` reaches them too via the
automatic fallback.

In `.coddy/mcp.json` the same entries look like Cursor's:

```json
{
  "mcpServers": {
    "remote-tools": { "url": "https://mcp.example.com/mcp" },
    "legacy-tools": { "type": "sse", "url": "https://old.example.com/sse" }
  }
}
```

## Tool Namespacing

To avoid conflicts when multiple MCP servers provide tools with the same name,
tools are namespaced using the server name:

- MCP server `filesystem` providing tool `read_file` -> available as `filesystem__read_file`
- Built-in tool `read_file` -> available as `read_file`

Because `__` separates the server and tool parts, server names must not contain `__`
(the management API rejects such names).

## How tools reach the model

MCP tools are **not** injected into the system prompt text (unlike skills, which render
into a prompt section). They join the built-in tools in the native function-calling
`tools` array of every LLM request, one definition per enabled tool: name
`server__tool`, the server's own `inputSchema`, and the description prefixed with
`[server]` so the model can tell providers apart. When the model emits a tool call whose
name contains `__`, the agent routes it to the owning server over its transport and
returns the MCP result to the model as a regular tool observation - the same loop as
built-in tools, and the same approach Claude Code, Codex, and Cursor use. The end-to-end
happy path (two servers over stdio and streamable HTTP, the model picking one by its
namespaced tool, the result landing in the final answer) is specified in
`features/mcp_tool_calls.feature` for both the OpenAI-compatible HTTP surface and the
ACP session flow.

## Permission Model

Whether a server may **start** is decided by the workspace trust gate above. What a running
server may **do** is not prompted for: MCP tool calls are dispatched without the built-in
permission prompts that guard filesystem writes and shell commands, and the disable switches
are the mechanism for restricting them. Prefer running MCP servers with least-privilege
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
   config.yaml + `~/.coddy/mcp.json` + `./.coddy/mcp.json` list that the workspace
   trust gate admits, then any ACP client-supplied servers
2. The agent calls `tools/list` on each server and registers the tools
3. Saving settings with a changed `mcp_servers` list reconnects the configured
   servers for every active session; ACP client-supplied per-session servers
   stay connected. The reconnect is a **fresh trust evaluation**, not a replay of
   what the session started with: an unapproved project declaration stays cold,
   and one whose approval was withdrawn in the meantime does not come back. A
   session with a **turn in flight** is not swapped mid-turn — that turn already
   handed the model a tool list, so the reload is parked and applied the moment
   the turn releases its lock
4. During the ReAct loop, when LLM calls an MCP tool, the agent forwards the call
   (unless the tool or its server has been disabled since)
5. Results are returned to the LLM as tool observations
6. On session end or `session/cancel`, MCP server connections are cleaned up

## Error Handling

- If an MCP server fails to start, the session still proceeds with a warning
- Failed MCP tool calls return an error observation to the LLM
- The LLM can decide to retry, use alternative tools, or inform the user
