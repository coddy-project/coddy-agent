# ACP Protocol Reference

## Overview

Coddy implements ACP as the **wire contract for the harness**. ACP standardizes how clients (for example editors, scripts, or orchestrators) talk to an agent process. The stock configuration presents a **coding agent**, but transports and RPC methods are generic harness surface area - initialize, session lifecycle, `session/prompt`, permission flows, and MCP-related options.

Reference: https://agentclientprotocol.com/protocol/overview

## Transport

All messages are newline-delimited JSON objects sent via **stdin/stdout**.

```
stdin  -> messages from Client to Agent
stdout -> messages from Agent to Client (responses + notifications)
stderr -> agent logs (not protocol messages)
```

## Message Types

### Request (Client to Agent)

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "session/prompt",
  "params": { ... }
}
```

### Response (Agent to Client)

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": { ... }
}
```

### Error Response

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32600,
    "message": "Invalid request"
  }
}
```

### Notification (Agent to Client, no response expected)

```json
{
  "jsonrpc": "2.0",
  "method": "session/update",
  "params": { ... }
}
```

## Stdio clients (Coddy-specific)

Hand-written scripts that drive **`coddy acp`** over a pipe should implement the following behaviors. Reference harness: **`examples/acp-jsonrpc-session/acp_agent_todo_e2e_demo.py`**.

1. **Nil `result` and `omitempty`** - JSON-RPC success payloads are produced with `result` omitted when the Go handler returns a **`nil`** pointer (for example **`session/set_mode`**). A response line may contain only **`jsonrpc`**, **`id`**, and neither **`result`** nor **`error`**. Treat any object with a matching **`id`** and no **`method`** as the completion of your outstanding request.
2. **Interleaved `session/update`** - After **`session/prompt`**, the agent streams many notifications before the final response. Read stdout line by line until the line for your request **`id`** arrives; handle **`session/request_permission`** in between by writing a client response with the same **`id`**.
3. **Stdout buffering** - When stdout is not a TTY, output can be block-buffered. Wrap the binary with **`stdbuf -oL -eL`** (or equivalent) so lines appear as they are written.
4. **Concurrent request handlers** - Outstanding requests are dispatched asynchronously. Do not send a second RPC until you have consumed the response for the previous one if your client assumes strict ordering.

## Protocol Flow

```
Client                          Agent
  |                               |
  |-------- initialize ---------->|
  |<------- initialize resp ------|
  |                               |
  |-------- session/new --------->|
  |<------- session/new resp -----|
  |                               |
  |-------- session/prompt ------>|
  |<------- session/update -------|  (notifications: plan, chunks, tool_calls)
  |<------- session/update -------|
  |<------- session/update -------|
  |<------- session/prompt resp --|  (stopReason: end_turn)
  |                               |
```

## Methods

### `initialize`

Negotiate protocol version and exchange capabilities.

**Request params:**
```json
{
  "protocolVersion": 1,
  "clientCapabilities": {
    "fs": {
      "readTextFile": true,
      "writeTextFile": true
    },
    "terminal": true
  },
  "clientInfo": {
    "name": "cursor",
    "title": "Cursor",
    "version": "1.0.0"
  }
}
```

**Response result:**
```json
{
  "protocolVersion": 1,
  "agentCapabilities": {
    "loadSession": true,
    "sessionCapabilities": {
      "list": {}
    },
    "promptCapabilities": {
      "image": false,
      "audio": false,
      "embeddedContext": true
    },
    "mcpCapabilities": {
      "http": true,
      "sse": false
    }
  },
  "agentInfo": {
    "name": "coddy-agent",
    "title": "Coddy Agent",
    "version": "0.1.0"
  },
  "authMethods": []
}
```

### `session/new`

Create a new conversation session.

**Request params:**
```json
{
  "cwd": "/home/user/project",
  "mcpServers": [
    {
      "name": "my-mcp",
      "command": "/path/to/mcp-server",
      "args": ["--stdio"],
      "env": []
    }
  ]
}
```

**Response result:**

Coddy returns both **Session Config Options** (preferred by modern ACP clients) and the legacy **`modes`** field for compatibility. Clients that support `configOptions` should use them for mode and model selection.

```json
{
  "sessionId": "sess_abc123def456",
  "configOptions": [
    {
      "id": "mode",
      "name": "Session mode",
      "description": "Agent runs tools; Plan focuses on design without execution.",
      "category": "mode",
      "type": "select",
      "currentValue": "agent",
      "options": [
        {
          "value": "agent",
          "name": "Agent",
          "description": "Execute tasks with full tool access"
        },
        {
          "value": "plan",
          "name": "Plan",
          "description": "Plan and design without code execution"
        }
      ]
    },
    {
      "id": "model",
      "name": "Model",
      "description": "LLM used for this session.",
      "category": "model",
      "type": "select",
      "currentValue": "openai/gpt-4o",
      "options": [
        {
          "value": "openai/gpt-4o",
          "name": "gpt-4o",
          "description": "openai"
        }
      ]
    }
  ],
  "modes": {
    "currentModeId": "agent",
    "availableModes": [
      {
        "id": "agent",
        "name": "Agent",
        "description": "Execute tasks with full tool access"
      },
      {
        "id": "plan",
        "name": "Plan",
        "description": "Plan and design without code execution"
      }
    ]
  }
}
```

The `model` option is present only when the `models` list in the agent config is non-empty. The effective default model is `agent.model` until the user picks another model in the client, as described in [Configuration](config.md). Each listed `value` matches the YAML `models[].model` string (`provider_name/api_model_id`).

### `session/load`

Reloads a persisted session by `sessionId`. The agent restores `session.json` and `messages.json`, rebuilds skills and MCP connections from the request, replays prior user and assistant turns (and tool call summaries) via `session/update`, and sends a `plan` update if `todos/active.md` exists.

**Request params** (per ACP, `cwd`, `sessionId`, and `mcpServers` are required):

```json
{
  "sessionId": "sess_abc123def456",
  "cwd": "/home/user/project",
  "mcpServers": []
}
```

**Response result:** `modes` and `configOptions` like `session/new`.

### `session/list`

Lists persisted sessions found under the configured sessions root (see README). Optional `cwd` filters by the stored working directory. The response includes `sessionId`, `cwd`, `title`, and `updatedAt` per entry.

### Disk layout (Coddy)

When persistence is enabled (`coddy acp` defaults to `$HOME/coddy-agent/sessions`), each bundle is `<root>/<sessionId>/` with:

- `session.json` - id, cwd, mode, model override, agent memory, derived title, timestamps
- `messages.json` - LLM message history (roles user, assistant, tool)
- `assets/` - reserved for future session-scoped files
- `todos/active.md` - current todo checklist synced from plan tools
- `todos/archive/todo-<nanos>.md` - archived list when a completed list is replaced

CLI flag **`coddy acp --disable-session`** disables this layout entirely (`loadSession` and `session/list` are not advertised).

### `session/prompt`

Send a user message, starts the ReAct loop.

**Request params:**
```json
{
  "sessionId": "sess_abc123def456",
  "prompt": [
    {
      "type": "text",
      "text": "Refactor the auth module to use JWT"
    }
  ]
}
```

**Response result:**
```json
{
  "stopReason": "end_turn"
}
```

Stop reasons: `end_turn` | `max_tokens` | `max_turns` | `agent_refused` | `cancelled`

### `session/cancel`

Cancel an ongoing prompt turn (notification).

```json
{
  "jsonrpc": "2.0",
  "method": "session/cancel",
  "params": {
    "sessionId": "sess_abc123def456"
  }
}
```

### `session/set_mode`

Switch between agent modes (legacy API). Prefer `session/set_config_option` with `configId` `mode` when the client supports Session Config Options.

**Request params:**
```json
{
  "sessionId": "sess_abc123def456",
  "modeId": "plan"
}
```

**Response result:** `null`

When the mode changes, the agent also sends a `session/update` with `config_option_update` so clients using config options stay in sync (for example, the displayed default model may change if no session model override is set).

### `session/set_config_option`

Change a session configuration option (ACP Session Config Options). Used for **mode** and **model** when the client exposes selectors.

**Request params:**
```json
{
  "sessionId": "sess_abc123def456",
  "configId": "model",
  "value": "anthropic/claude-3-5-sonnet"
}
```

**Response result:** full `configOptions` array with updated `currentValue` fields:

```json
{
  "configOptions": [ ... ]
}
```

Unknown `configId` or a `value` not listed under that option yields a JSON-RPC error (invalid params).

## Notifications (Agent -> Client)

All sent via `session/update` method with a `sessionUpdate` discriminator field.

### `plan` - Agent execution plan

```json
{
  "sessionUpdate": "plan",
  "entries": [
    { "content": "Read auth module", "priority": "high", "status": "pending" },
    { "content": "Design JWT structure", "priority": "high", "status": "pending" },
    { "content": "Implement changes", "priority": "medium", "status": "pending" }
  ]
}
```

### `agent_message_chunk` - Text response chunk

```json
{
  "sessionUpdate": "agent_message_chunk",
  "content": {
    "type": "text",
    "text": "I'll start by reading the current auth module..."
  }
}
```

### `tool_call` - Tool call started

```json
{
  "sessionUpdate": "tool_call",
  "toolCallId": "call_001",
  "title": "Reading auth.go",
  "kind": "read",
  "status": "pending"
}
```

### `tool_call_update` - Tool call status update

```json
{
  "sessionUpdate": "tool_call_update",
  "toolCallId": "call_001",
  "status": "completed",
  "content": [
    {
      "type": "content",
      "content": { "type": "text", "text": "File contents: ..." }
    }
  ]
}
```

Tool call statuses: `pending` | `in_progress` | `completed` | `failed` | `cancelled`

### `current_mode_update` - Mode changed

```json
{
  "sessionUpdate": "current_mode_update",
  "modeId": "agent"
}
```

### `config_option_update` - Session config options changed

Sent after `session/set_config_option`, after `session/set_mode`, or whenever the agent updates session config options to match runtime state (so UI stays aligned).

```json
{
  "sessionUpdate": "config_option_update",
  "configOptions": [
    {
      "id": "mode",
      "name": "Session mode",
      "category": "mode",
      "type": "select",
      "currentValue": "agent",
      "options": [ ... ]
    },
    {
      "id": "model",
      "name": "Model",
      "category": "model",
      "type": "select",
      "currentValue": "openai/gpt-4o",
      "options": [ ... ]
    }
  ]
}
```

## Permission Requests (Agent -> Client, expects response)

```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "method": "session/request_permission",
  "params": {
    "sessionId": "sess_abc123def456",
    "toolCall": {
      "toolCallId": "call_002",
      "title": "Run: go build ./...",
      "kind": "run_command",
      "status": "pending",
      "content": [
        { "type": "text", "text": "Execute: go build ./..." }
      ]
    },
    "options": [
      { "optionId": "allow", "name": "Allow", "kind": "allow_once" },
      { "optionId": "allow_always", "name": "Allow always", "kind": "allow_always" },
      { "optionId": "reject", "name": "Reject", "kind": "reject_once" }
    ]
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "result": {
    "outcome": "allow",
    "optionId": "allow"
  }
}
```

## Client Filesystem Methods

The agent can call these methods on the client (if client supports them):

### `fs/read_text_file`

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "fs/read_text_file",
  "params": { "path": "/absolute/path/to/file.go" }
}
```

### `fs/write_text_file`

```json
{
  "jsonrpc": "2.0",
  "id": 6,
  "method": "fs/write_text_file",
  "params": {
    "path": "/absolute/path/to/file.go",
    "content": "package main\n..."
  }
}
```
