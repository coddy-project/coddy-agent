# ReAct Agent: Design Specification

## Role in Coddy

Coddy is modeled as **harness plus execution engine**. This document specifies that engine -

- the **ReAct loop** in `internal/agent` that turns prompts and tools into streamed turns,
- default **coding-agent** behavior - tool registry, `agent` and `plan` modes, permission gates.

The same harness may use a narrower tool surface or different clients (automation, not only IDEs).

The ReAct flow and message contract below stay stable for any ACP-speaking session.

## What is ReAct?

ReAct (Reasoning + Acting) is an agent paradigm where the LLM alternates between:
- **Thought** - internal reasoning about what to do next
- **Action** - calling a tool or producing output
- **Observation** - receiving the result of the action

Reference: https://arxiv.org/abs/2210.03629

## ReAct Loop Implementation

### System Prompt Structure

Templates are **`internal/prompts/agent.md`** and **`plan.md`** (embedded by default or overridden via **`prompts.dir`**). They use Go **`text/template`**.

Rendered order matches the markdown files roughly as follows:

```
[Intro + Mode + How to work / How to plan]
Working directory: {{.CWD}}

{{if .Tools}}
## Available tools
{{.Tools}}
{{end}}

{{if .Skills}} ... active rules/skills markdown ... {{end}}

{{if .TodoList}}
### Current todo checklist
{{.TodoList}}
{{end}}

{{if .Memory}}
## Session memory
{{.Memory}}
{{end}}

## Current UTC time
{{.UTCNow}}
```

The **`TodoList`** body is markdown from **`internal/tools/todo.FormatTodoMarkdown`** applied to **`session.Plan`**. It is injected **only when** at least one entry exists. Embedded templates treat an empty **`TodoList`** as false for **`{{if .TodoList}}`**.

Immediately before **each** provider **`Stream`** call within a single **`session/prompt`**, Coddy reapplies **`Render`** so the **`system`** message reflects todo changes from tools executed earlier in that same episode. **`UTCNow`** is set to **`time.Now().UTC()`** formatted as RFC3339 on each render so the footer clock advances across ReAct iterations.

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
1. BUILD_MESSAGES
   - Load applicable skills/rules for current context (context files from prompt)
   - Build system prompt (template + TemplateData incl. TodoList snapshot)
   - Prepend system to session history (user turn already persisted on Run entry)

2. LLM_CALL
   - Send messages + tool definitions to LLM provider
   - Receive response: may contain text + tool_calls

3. STREAM_RESPONSE
   - For each text chunk: send session/update(agent_message_chunk)
   - For each tool_call: send session/update(tool_call, status=pending)

4. EXECUTE_TOOLS (if any tool calls)
   - For each tool_call sequentially inside one assistant message:
     a. Send session/update(tool_call_update, status=in_progress)
     b. If requires permission: session/request_permission -> wait for response
     c. Execute tool (built-in or MCP)
     d. Send session/update(tool_call_update, status=completed|failed, content=result)
     e. Append tool result to conversation history

5. REFRESH_SYSTEM
   - Next loop iteration repeats from step 2 after rewriting messages[0] with a fresh **`Render`** (same session state, potentially new Plan rows)

6. CHECK_COMPLETION
   - If no tool calls in last response -> DONE (stopReason: end_turn)
   - If turn_count >= max_turns -> DONE (stopReason: max_turns)
   - Otherwise -> back to step 2

7. FINAL_RESPONSE
   - Send session/prompt response with stopReason
```

## Mode-Specific Behavior

### Agent Mode

Embedded **`agent.md`** describes agent behavior (quality, shells, todos). Todo-related instructions reference **`create_todo_list`**, **`update_todo_item`**, **`done_todo_item`**, **`get_todo_list`**, etc., as surfaced in **`Tools`**.

Representative builtins (excluding MCP-namespaced tools):

- `read_file`, `write_file`, `list_dir`, `search_files`
- `run_command`, `apply_diff`
- Filesystem mutations: **`mkdir`**, **`rm`**, **`rmdir`**, **`touch`**, **`mv`** (subset may require permission paths)
- Session checklist: **`create_todo_list`**, **`update_todo_item`**, **`delete_todo_item`**, **`done_todo_item`**, **`undone_todo_item`**, **`get_todo_list`**, **`clean_todo_list`**
- All MCP server tools (names **`serverName__toolName`**)

Plan mode forbids **`write_file`** entirely; **`write_text_file`** is **`PlanOnly`** and hidden from **`agent`**.

### Plan Mode

Embedded **`plan.md`** restricts execution (no shell, no edits to arbitrary code suffixes).

Representative builtins:

- `read_file`, `list_dir`, `search_files`
- **`write_text_file`** (only **`.txt`**, **`.md`**, **`.mdx`** destinations)
- Full todo toolchain above (**`AllowedInPlanMode`** on each todo tool)

When ready to ship implementation work, prompts instruct switching the client to **`agent`** mode.

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

Clients receive `session/update` notifications whose **`sessionUpdate`** field equals **`plan`**, carrying structured **`entries`**. Todolist tooling also persists the active checklist under **`todos/active.md`** in the bundle when session persistence is on (mirrors **`FormatTodoMarkdown`** / **`ParseTodoMarkdown`**).

Example payload:

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
