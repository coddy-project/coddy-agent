# ReAct Agent: Design Specification

## What is ReAct?

ReAct (Reasoning + Acting) is an agent paradigm where the LLM alternates between:
- **Thought** - internal reasoning about what to do next
- **Action** - calling a tool or producing output
- **Observation** - receiving the result of the action

Reference: https://arxiv.org/abs/2210.03629

## ReAct Loop Implementation

### System Prompt Structure

```
[Base Instructions]
You are an AI coding assistant operating in {mode} mode.
Your task is to help with code generation and text file editing.
Working directory: {cwd}

[Available Tools]
{tool_list with descriptions and parameters}

[Active Skills/Rules]
{injected skill content}

[Mode-specific Instructions]
{agent_mode_instructions | plan_mode_instructions}
```

### Tool Calling via Function Calling API

Modern LLMs support native function/tool calling. The agent uses this instead of
text-based ReAct prompting:

1. Tools are defined as JSON Schema objects and passed to the LLM API
2. LLM returns structured tool call requests (not raw text)
3. Agent executes the requested tools
4. Results are appended to conversation as `tool` role messages
5. LLM continues reasoning with tool results in context

This approach is more reliable than text parsing and supported by all major providers
(OpenAI, Anthropic, Ollama with compatible models).

### Conversation Message Structure

```
messages: [
  { role: "system",    content: <system_prompt> },
  { role: "user",      content: <user_prompt> },
  { role: "assistant", content: "", tool_calls: [{ id: "call_1", name: "read_file", args: {...} }] },
  { role: "tool",      tool_call_id: "call_1", content: <file_contents> },
  { role: "assistant", content: "", tool_calls: [{ id: "call_2", name: "write_file", args: {...} }] },
  { role: "tool",      tool_call_id: "call_2", content: "OK" },
  { role: "assistant", content: <final_answer> }
]
```

### Loop Steps

```
1. BUILD_PROMPT
   - Load applicable skills/rules for current context
   - Build system prompt (base + mode + skills + tools)
   - Append user message to history

2. LLM_CALL
   - Send messages + tool definitions to LLM provider
   - Receive response: may contain text + tool_calls

3. STREAM_RESPONSE
   - For each text chunk: send session/update(agent_message_chunk)
   - For each tool_call: send session/update(tool_call, status=pending)

4. EXECUTE_TOOLS (if any tool calls)
   - For each tool_call in parallel (or sequential, configurable):
     a. Send session/update(tool_call_update, status=in_progress)
     b. If requires permission: session/request_permission -> wait for response
     c. Execute tool (built-in or MCP)
     d. Send session/update(tool_call_update, status=completed|failed, content=result)
     e. Append tool result to conversation history

5. CHECK_COMPLETION
   - If no tool calls in last response -> DONE (stopReason: end_turn)
   - If turn_count >= max_turns -> DONE (stopReason: max_turns)
   - Otherwise -> back to step 2

6. FINAL_RESPONSE
   - Send session/prompt response with stopReason
```

## Mode-Specific Behavior

### Agent Mode

System prompt addition:
```
You are in AGENT mode. You have full access to all tools including file operations
and command execution. Complete the task end-to-end, using tools as needed.
Always explain what you are doing before each tool call.
```

Available tools:
- `read_file`, `write_file`, `list_dir`, `search_files`
- `run_command` (requires permission)
- `apply_diff`
- All MCP server tools

### Plan Mode

System prompt addition:
```
You are in PLAN mode. Your goal is to plan and document, NOT to execute code.
You may read files to understand the codebase. You may write or edit text and
markdown files. Do NOT execute commands or make code changes.
When ready to implement, tell the user to switch the session to **agent** mode in the client tool strip or session settings.
```

Available tools:
- `read_file`, `list_dir`, `search_files` (read-only)
- `write_text_file` (.txt / .md / .mdx only in plan mode)

## Built-in Tools Specification

### `read_file`
```json
{
  "name": "read_file",
  "description": "Read the contents of a file",
  "parameters": {
    "path": { "type": "string", "description": "Absolute or relative (to cwd) path" },
    "start_line": { "type": "integer", "description": "First line to read (1-based, optional)" },
    "end_line": { "type": "integer", "description": "Last line to read (1-based, optional)" }
  },
  "required": ["path"]
}
```

### `write_file`
```json
{
  "name": "write_file",
  "description": "Write or create a file with the given content",
  "parameters": {
    "path": { "type": "string", "description": "Absolute or relative (to cwd) path" },
    "content": { "type": "string", "description": "Full file content to write" }
  },
  "required": ["path", "content"]
}
```

### `list_dir`
```json
{
  "name": "list_dir",
  "description": "List files and directories at the given path",
  "parameters": {
    "path": { "type": "string", "description": "Directory path (default: cwd)" },
    "recursive": { "type": "boolean", "description": "Include subdirectories" }
  }
}
```

### `search_files`
```json
{
  "name": "search_files",
  "description": "Search for a pattern in files (uses ripgrep)",
  "parameters": {
    "pattern": { "type": "string", "description": "Regex or literal search pattern" },
    "path": { "type": "string", "description": "Directory to search in (default: cwd)" },
    "glob": { "type": "string", "description": "File glob filter (e.g. '*.go')" },
    "case_sensitive": { "type": "boolean", "default": false }
  },
  "required": ["pattern"]
}
```

### `run_command`
```json
{
  "name": "run_command",
  "description": "Execute a shell command in the working directory",
  "parameters": {
    "command": { "type": "string", "description": "Shell command to execute" },
    "timeout_seconds": { "type": "integer", "default": 30 }
  },
  "required": ["command"]
}
```

### `apply_diff`
```json
{
  "name": "apply_diff",
  "description": "Apply a unified diff to a file",
  "parameters": {
    "path": { "type": "string", "description": "File to patch" },
    "diff": { "type": "string", "description": "Unified diff content" }
  },
  "required": ["path", "diff"]
}
```

## Plan Update Format

When the agent starts processing, it sends a plan via `session/update`:

```json
{
  "sessionUpdate": "plan",
  "entries": [
    { "content": "Read current auth module", "priority": "high", "status": "pending" },
    { "content": "Analyze JWT requirements", "priority": "high", "status": "pending" },
    { "content": "Write new auth implementation", "priority": "medium", "status": "pending" },
    { "content": "Update tests", "priority": "low", "status": "pending" }
  ]
}
```

Plan entries are updated as the agent progresses:
```json
{ "content": "Read current auth module", "priority": "high", "status": "completed" }
```

## Error Handling in ReAct Loop

- LLM API error: retry up to 3 times with exponential backoff, then fail turn
- Tool execution error: return error as observation, let LLM decide next step
- Permission denied: return "permission denied" observation
- Tool timeout: return "timeout" observation after configured timeout
- Context too long: summarize older messages, continue with summary
- Cancelled: abort all operations, return `cancelled` stop reason
