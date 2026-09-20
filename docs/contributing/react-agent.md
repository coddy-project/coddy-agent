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

{{if .Memory}}
## Session memory
{{.Memory}}
{{end}}

<environment_context>
<os>...</os>
<arch>...</arch>
<shell>...</shell>
</environment_context>
```

The environment block is appended outside the configurable template so OS and shell facts cannot be accidentally omitted by a custom prompt.

### The system prompt is frozen for the turn

The system message is rendered **once per turn**, by **`buildSystemPromptParts`**
(**`internal/agent/system_prompt.go`**), and every step of the ReAct loop then sends that same
message back byte for byte. Nothing rewrites **`messages[0]`** between the steps.

The reason is the provider's prompt cache. A provider caches a request by its **prefix** - the tool
definitions, then the messages from the start - and the first byte that differs from the previous
request throws away everything cached behind it. The system message sits in front of the whole
conversation, so a wall clock with seconds in it, or a checklist row a tool had just rewritten, used
to make a fifty-thousand-token history look new on every single request. Alibaba's gateway caches in
blocks of 1024 tokens; a difference inside the first block leaves **`cached_tokens`** at zero no
matter how much identical history follows.

### The turn context block

What does move while a turn runs travels **after** the replayed history, as one trailing `user`
message built by **`buildTurnContext`** (**`internal/agent/turn_context.go`**):

```
<turn_context>
Runtime state refreshed by Coddy for this step. It is not a message from the user; do not answer it, just take it into account.

## Current UTC time
2026-09-15T11:39:03Z

## Current todo checklist
- [ ] ...

## Project rules activated by this turn
### go-files (Go style)
...

## Long-term memory
Already on disk:
- ...
</turn_context>
```

- the **wall clock**, always - stamped once when the turn's prompt was rendered and reused by every
  step of that turn, because the lane re-issues a step that produced nothing and that replay has to
  be the request that failed, byte for byte (*Lane replays* in
  [architecture.md](architecture.md));
- the **todo checklist** (markdown from **`internal/tools/todo.FormatPlanMarkdown`** over
  **`session.Plan`**), when the session has one - so a **`coddy_todo_*`** call in this turn is
  reflected on the very next step;
- the **rules a tool call activated** after the system prompt was frozen - a glob rule or a nested
  **`AGENTS.md`** that a filesystem tool reached mid-turn (**`activateScopedRulesForToolCall`**).
  They are **not** folded back into the frozen prompt; the next turn's prompt picks them up from
  the sticky set, and **`rules.Added`** is what keeps the block down to what the model has not been
  given yet;
- the **memory subagent's report** for this turn, when long-term memory is on
  ([memory.md](../features/memory.md)). A recall differs from turn to turn, so rendering it into
  the system message would make **`messages[0]`** a new one on every turn and cost the cached
  conversation each time. It joins the block on the first request when the run settled inside
  **`memory.wait_seconds`**, on a later step otherwise, and stays for the rest of the turn; the
  **`{{.Memory}}`** slot of the templates holds the session notes alone;
- the **background tasks still running** in this session, one line each in the wording of
  **`background_list`** (**`backgroundTasksSection`**, [background-tasks.md](../features/background-tasks.md)),
  so the model knows what it left running without a call. Finished and system tasks are left out,
  and the section is not written with **`tools.background.enable`** false.

The block is never persisted: it is appended at the **`provider.Stream`** send boundary, next to the
read/grep eviction projection, and the working message slice the loop keeps appending to never sees
it. Only the last few hundred tokens of a request are therefore uncached; the conversation behind
them is a cache hit.

**`UTCNow`** and **`TodoList`** stay available to a template under **`prompts.dir`**, which may still
render them - at the cost of that cache, on every request. Such a template gets no clock and no
checklist after the history; its block carries the two sections no template prints, the memory
report and the running background tasks, and is left out when there is neither.

### The other half: read/grep eviction

Freezing the system message is only half of a stable prefix. Read/grep result
eviction (**`internal/agent/result_eviction.go`**) writes placeholders **into the middle** of the
replayed history, which invalidates the cache from that point on just as surely. With the default
sliding working window that used to happen on nearly every step, so the conversation was reprocessed
each time even with the prompt frozen. **`compaction.result_eviction.start_percent`** (default 50)
holds the projection off until the estimated context reaches that share of the model's
**`max_context_tokens`**; below it the history goes out exactly as the provider already has it. The
decision is measured on the **unpruned** messages, so pruning cannot push the estimate back under the
mark and make the projection flap between two shapes. See
[compaction.md](../features/compaction.md).

### Reading the cache hit

Providers report the cached share of a request as
**`usage.prompt_tokens_details.cached_tokens`** (OpenAI-compatible) or
**`cache_read_input_tokens`** (Anthropic). Coddy carries it as
**`llm.Response.CachedInputTokens`** and logs it per call:

```
export CODDY_LOG_LEVEL=debug   # or logger.levels: {agent: debug}
# msg="llm call usage" input_tokens=6044 cached_input_tokens=5120 output_tokens=12
```

A long session whose second step shows **`cached_input_tokens`** near zero means something is
rewriting the prefix. Most OpenAI-compatible servers omit the field entirely, and then it reads
zero without meaning a miss.

### Agent identity

**Every system prompt Coddy sends opens by naming the product.** The sentence lives in **`internal/prompts/identity.go`** as **`prompts.Identity`** (**`"You are Coddy, an AI coding agent."`**), and **`prompts.WithIdentity`** is what puts it there.

Why it exists: an LLM gateway cannot tell one OpenAI-compatible client from another by the wire protocol, so gateways attribute traffic by matching the **opening of the system prompt** against a table of known products (**`"You are Claude Code…"`**, **`"You are Cline…"`**). Coddy used to open with a generic sentence and was therefore invisible in that kind of analytics. Treat **`prompts.Identity`** as a published contract: gateways key on the **`you are coddy`** substring, and rewording it silently drops Coddy out of their reports until they catch up.

Where it is applied:

- **`buildSystemPrompt`** (`internal/agent/system_prompt.go`), last step before the context breakdown — covers agent, plan, and ask modes, a user's own **`prompts.dir`** template, and the render fallback;
- **`buildCompactionRequest`** (`internal/agent/compact.go`) — the summarizer is its own request with its own system prompt;
- the auxiliary HTTP prompts: chat-title generation (`external/httpserver/coddy_coddy.go`) and prompt enhancement (`external/httpserver/enhance_prompt.go`); the memory subagent's template (`external/memory/prompts/memory_agent.md`) is rendered by `buildSystemPromptParts` through the `PromptTemplate` of its child session, so it opens with the identity line by itself.

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

   Recovery attempts and transport retries draw from the **`llm_retry_max`**
   shared budget (default 3). The initial model request for a step is not
   charged; each extra attempt or recovery consumes one slot; the budget resets
   when the model makes progress (a tool call or an answered followup) — it is
   not a lifetime cap on all LLM calls in a turn. Per-strategy limits apply
   independently of the budget:

   - **Transport retries.** The resilient wrapper repeats on HTTP 429, 408,
     5xx, and transport failures that left no output with the caller; each
     retry consumes one slot. A cancellation, an unknown host, and any failure
     after output was emitted stay final.
   - **First-token re-issue.** A streamed call the first-token guard
     (**`llm_first_token_timeout_ms`**) cuts with **nothing produced** is
     re-issued at most once, using a normal **`max_turns`** iteration. The
     re-issue is skipped after output was emitted or the caller cancelled.
     Consumes one slot.
   - **Empty-assistant re-issue.** A step that returns neither answer text nor
     a tool call is replayed once as the identical request; the empty assistant
     turn is removed from the LLM-facing message slice (the transcript keeps
     it, with signed thinking preserved). A reply stopped at `max_tokens` with
     only whitespace content is terminal — recovery is not attempted. Consumes
     one slot.
   - **Wording nudges.** If the re-issue also comes back empty, up to
     two nudges (**`maxEmptyAssistantContinuations`**) are sent, each consuming
     one slot and a normal **`max_turns`** iteration. The plain replay uses
     a normal iteration too; transport retries stay inside that iteration.
     The nudge projection also removes the empty assistant message from the
     LLM-facing history. If auto-compaction rebuilds that history between
     attempts, the pending recovery projection and its nudges are restored;
     signed reasoning stays in the transcript. After the budget or nudge limit is exhausted the turn
     ends with **`StopReasonRefused`**.

   An explicit **`llm_retry_max: 0`** disables all of the above. The
   `loop_guard`, Stop hooks, fallback models, and `wait_for_limit_reset` are
   independent policies, each governed by their own settings.

   At `logger.level: debug`, each LLM call completion logs
   `msg="llm call finished"` with `provider_attempts` (total inner adapter
   calls including transport-layer retries), `transport_retries`, `call_reason`
   (one of `step`, `empty_reissue`, `empty_nudge`, `first_token_retry`,
   `loop_guard`, `quota_reset_wait`, `stop_hook`, `queued_followup`), and
   `retries_remaining` (budget slots left after this call).

   Two guards bound a streamed call that stops answering. The first-token guard
   (**`llm_first_token_timeout_ms`**, 90 s) cuts a call that produced nothing;
   the stream idle guard (**`llm_stream_idle_timeout_ms`**, five minutes, in
   **`internal/llm/transport.go`**) cuts a response whose server sent nothing
   for that long after its first bytes. The text the user already watched
   stream in is persisted like a truncation, and the turn ends with the stall
   named. Neither guard applies to a **`stream: false`** model, whose answer
   arrives in one piece: **`providers[].timeout_ms`** is the only bound on
   such a call, next to the HTTP/2 liveness pings that close a connection
   whose far side stopped answering.

7. FINAL_RESPONSE
   - Send session/prompt response with stopReason
```

## Mode-Specific Behavior

### Agent Mode

Embedded **`agent.md`** describes agent behavior (quality, shells, todos, git worktrees). Todo-related instructions reference **`coddy_todo_plan_*`** and **`coddy_todo_item_*`** tools surfaced in **`Tools`**. The **Git worktrees** block names **`.coddy/worktrees/<branch>`** as the place a worktree goes, the same directory the workspace switch of the HTTP surface uses ([Sessions](../features/sessions.md#git-worktrees)); without it the model picks a spot of its own and leaves an untracked folder at the repository root.

Representative builtins (excluding MCP-namespaced tools):

- `read_file`, `write_file`, `write_text_file`, `list_dir`, `search_files`
- `websearch`, `webfetch`, `http_request`
- `run_command`, `apply_diff`
- Filesystem mutations: **`mkdir`**, **`rm`**, **`rmdir`**, **`touch`**, **`mv`** (subset may require permission paths)
- Session checklist: **`coddy_todo_plan_read`**, **`coddy_todo_plan_replace`**, **`coddy_todo_plan_archive`**, **`coddy_todo_item_add`**, **`coddy_todo_item_remove`**, **`coddy_todo_item_update`**, **`coddy_todo_item_move`**
- All MCP server tools (names **`serverName__toolName`**)

### Plan Mode

Embedded **`plan.md`** keeps the default **registry** surface read-oriented (no built-in writes or **coddy** todo tools in the advertised set). **`run_command`** and all **MCP** tools from configured servers are still available for inspection.

Representative builtins exposed to the LLM (registry allowlist):

- `read_file`, `list_dir`, `search_files`
- `websearch`, `webfetch`
- `run_command`

Plus MCP tools (**`serverName__toolName`**). When ready to ship implementation work, prompts instruct switching the client to **`agent`** mode.

### Ask Mode

Embedded **`ask.md`** describes a read-only assistant: it answers from the repository and the web and never mutates anything. The registry allowlist (**`internal/agent.ToolSetForMode("ask")`**) is **`read`**, **`keep_result`**, **`glob`**, **`grep`**, **`print_tree`**, **`websearch`**, **`webfetch`**, **`question`**, **`load_skill`**, **`coddy_docs_search`** and **`coddy_docs_read`**; there is no shell, no plan, todo or config tool, no **`spawn_agent`**, and **MCP** tools are never appended. Unlike plan mode the allowlist is also enforced at execution time, so a call replayed from history is refused with a read-only notice. A plan mention or **`runPlanSlug`** metadata never starts a plan run in ask mode, and the memory subagent runs recall-only. A subagent child never runs in ask mode unless its parent's turn was already in ask mode, which cannot spawn.

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

- LLM API error: draw from the `agent.llm_retry_max` shared per-step budget (default 3; explicit 0 disables); transport retries, first-token re-issues, and no-answer recoveries share the same pool; the budget resets on tool progress and is not a per-turn lifetime cap on all LLM calls
- LLM stream stalled (no bytes after the first ones for `llm_stream_idle_timeout_ms`): persist the partial answer, fail turn with the stall named; retried within the remaining shared budget only when nothing was delivered
- Tool execution error: return error as observation, let LLM decide next step
- Permission denied: return "permission denied" observation
- Tool timeout: return "timeout" observation after configured timeout
- Context too long: summarize older messages, continue with summary
- Cancelled: abort all operations, return `cancelled` stop reason
