# ACP Protocol Reference

## Overview

The Agent Client Protocol (ACP) standardizes communication between code editors and coding
agents. It uses **JSON-RPC 2.0** transported over **stdio** (local agents).

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

## Protocol Flow

```
Client                          Agent
  |                               |
  |-------- initialize ---------->|
  |<------- initialize resp -------|
  |                               |
  |-------- session/new --------->|
  |<------- session/new resp ------|
  |                               |
  |-------- session/prompt ------>|
  |<------- session/update --------|  (notifications: plan, chunks, tool_calls)
  |<------- session/update --------|
  |<------- session/update --------|
  |<------- session/prompt resp ---|  (stopReason: end_turn)
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
```json
{
  "sessionId": "sess_abc123def456",
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

Switch between agent modes.

**Request params:**
```json
{
  "sessionId": "sess_abc123def456",
  "modeId": "plan"
}
```

**Response result:** `null`

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
