# ReAct Agent: Design Specification

## Role in Coddy

Coddy is modeled as **harness plus execution engine**. This document specifies that engine -

- the **ReAct loop** in `internal/agent` that turns prompts and tools into streamed turns,
- default **coding-agent** behavior - tool registry, `agent`, `plan`, and `ask` modes, permission gates.

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

Templates are **`internal/prompts/agent.md`**, **`plan.md`**, and **`ask.md`** (embedded by default or overridden via **`prompts.dir`**). They use Go **`text/template`**.

Rendered order matches the markdown files roughly as follows:

```
[Identity line — see "Agent identity" below; absent when the template opens with it]
[Intro + Mode + How to work / How to plan]
Working directory: {{.CWD}}

{{if .Tools}}
## Available tools
{{.Tools}}
{{end}}

{{if .Skills}} ... skill catalog + always-active/glob-matched bodies
          (invoked /name bodies are injected into the user message instead) ... {{end}}

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

<environment_context>
<os>...</os>
<arch>...</arch>
<shell>...</shell>
</environment_context>
```

The **`TodoList`** body is markdown from **`internal/tools/todo.FormatPlanMarkdown`** applied to **`session.Plan`**. It is injected **only when** at least one entry exists. Embedded templates treat an empty **`TodoList`** as false for **`{{if .TodoList}}`**. The environment block is appended outside the configurable template so OS and shell facts cannot be accidentally omitted by a custom prompt.

Immediately before **each** provider **`Stream`** call within a single **`session/prompt`**, Coddy reapplies **`Render`** so the **`system`** message reflects todo changes from tools executed earlier in that same episode. **`UTCNow`** is set to **`time.Now().UTC()`** formatted as RFC3339 on each render so the footer clock advances across ReAct iterations.

### Agent identity

**Every system prompt Coddy sends opens by naming the product.** The sentence lives in **`internal/prompts/identity.go`** as **`prompts.Identity`** (**`"You are Coddy, an AI coding agent."`**), and **`prompts.WithIdentity`** is what puts it there.

Why it exists: an LLM gateway cannot tell one OpenAI-compatible client from another by the wire protocol, so gateways attribute traffic by matching the **opening of the system prompt** against a table of known products (**`"You are Claude Code…"`**, **`"You are Cline…"`**). Coddy used to open with a generic sentence and was therefore invisible in that kind of analytics. Treat **`prompts.Identity`** as a published contract: gateways key on the **`you are coddy`** substring, and rewording it silently drops Coddy out of their reports until they catch up.

Where it is applied:

- **`buildSystemPrompt`** (`internal/agent/system_prompt.go`), last step before the context breakdown — covers agent, plan, and ask modes, a user's own **`prompts.dir`** template, and the render fallback;
- **`buildCompactionRequest`** (`internal/agent/compact.go`) — the summarizer is its own request with its own system prompt;
- the auxiliary HTTP prompts: chat-title generation (`external/httpserver/coddy_coddy.go`), prompt enhancement (`external/httpserver/enhance_prompt.go`) and the memory copilot (`external/memory/copilot.go`).

Two properties the tests lock (`internal/prompts/identity_test.go`, `internal/agent/identity_prompt_test.go`):

1. the marker lands within the **first 220 characters**, because that is the prefix a gateway inspects — a long **`{{.CWD}}`** must not push it out;
2. the line appears **exactly once**. The built-in templates already open with **`You are Coddy, …`**, so **`WithIdentity`** detects the marker and returns them untouched instead of stacking a second identity line on top.

A custom **`prompts.dir`** template does **not** need to name Coddy: the line is prepended for it automatically.

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
   - Load applicable skills and project rules for current context (separate prompt sections)
   - Build system prompt (template + TemplateData incl. TodoList snapshot)
   - For the last user message, detect `/name` invocations: prepend each matched skill's body to the message content before the LLM call. This augmentation is ephemeral — not persisted to session history, not shown in the chat transcript.
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

   Loop guard (**`agent.loop_guard`**, default on) can end the turn earlier:
   - A streamed response repeating the same passage **`loop_stream_repeat_cycles`** times
     in a row (answer text or reasoning) has its stream cancelled. The repeated run is
     stripped from the stored message, so it is never replayed to the model.
   - A tool call repeated **`loop_tool_repeat_limit`** times with identical canonical
     arguments is not executed; the model gets a result explaining why.
   - Either case first nudges the model to change course, up to **`loop_nudge_max`**
     times, then -> DONE (stopReason: agent_refused) with a notice.

7. FINAL_RESPONSE
   - Send session/prompt response with stopReason
```

## Mode-Specific Behavior

### Agent Mode

Embedded **`agent.md`** describes agent behavior (quality, shells, todos). Todo-related instructions reference **`coddy_todo_plan_*`** and **`coddy_todo_item_*`** tools surfaced in **`Tools`**.

Representative builtins (excluding MCP-namespaced tools):

- `read_file`, `write_file`, `write_text_file`, `list_dir`, `search_files`
- `search_web`, `extract_page_content`
- `run_command`, `apply_diff`
- Filesystem mutations: **`mkdir`**, **`rm`**, **`rmdir`**, **`touch`**, **`mv`** (subset may require permission paths)
- Session checklist: **`coddy_todo_plan_read`**, **`coddy_todo_plan_replace`**, **`coddy_todo_plan_archive`**, **`coddy_todo_item_add`**, **`coddy_todo_item_remove`**, **`coddy_todo_item_update`**, **`coddy_todo_item_move`**
- All MCP server tools (names **`serverName__toolName`**)

### Plan Mode

Embedded **`plan.md`** keeps the default **registry** surface read-oriented (no built-in writes or **coddy** todo tools in the advertised set). **`run_command`** and all **MCP** tools from configured servers are still available for inspection.

Representative builtins exposed to the LLM (registry allowlist):

- `read_file`, `list_dir`, `search_files`
- `search_web`, `extract_page_content`
- `run_command`

Plus MCP tools (**`serverName__toolName`**). When ready to ship implementation work, prompts instruct switching the client to **`agent`** mode.

### Ask Mode

Embedded **`ask.md`** describes a read-only assistant: it answers from the repository and the web and never mutates anything. The registry allowlist (**`internal/agent.ToolSetForMode("ask")`**) is **`read`**, **`keep_result`**, **`glob`**, **`grep`**, **`print_tree`**, **`websearch`**, **`webfetch`**, **`question`** and **`load_skill`**; there is no shell, no plan, todo or config tool, no **`spawn_agent`**, and **MCP** tools are never appended. Unlike plan mode the allowlist is also enforced at execution time, so a call replayed from history is refused with a read-only notice. A plan mention or **`runPlanSlug`** metadata never starts a plan run in ask mode, and the memory copilot runs recall-only. A subagent child never runs in ask mode unless its parent's turn was already in ask mode, which cannot spawn.

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

### `grep`
```json
{
  "name": "grep",
  "description": "Search file contents recursively (system ripgrep with a built-in fallback)",
  "parameters": {
    "pattern": { "type": "string", "description": "Regex or literal search pattern" },
    "path": { "type": "string", "description": "Directory to search in (default: cwd)" },
    "glob": { "type": "string", "description": "File glob filter (e.g. '**/*.go')" },
    "case_sensitive": { "type": "boolean", "default": false },
    "max_results": { "type": "integer", "default": 100 }
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

Clients receive `session/update` notifications whose **`sessionUpdate`** field equals **`plan`**, carrying structured **`entries`**. Todolist tooling also persists the active checklist under **`todos/active.md`** in the bundle when session persistence is on (mirrors **`FormatPlanMarkdown`** / **`ParsePlanMarkdown`**).

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
