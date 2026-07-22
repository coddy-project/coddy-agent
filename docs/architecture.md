# Architecture: Coddy Agent

## Overview

Coddy is a **distroless-friendly ACP harness** written in Go. At its core it is protocol plumbing
(STDIO JSON-RPC server, sessions, configuration, MCP wiring) plus a **ReAct** execution loop backed
by pluggable LLM providers. Ship it as one binary suitable for scratch or distroless images,
sidecars, CI sandboxes, or local installs.

The default toolset and prompts are tuned so the harness presents as an **interactive coding agent**
(ACP clients spawn `coddy acp`; users get filesystem, commands, MCP, project rules from `.coddy`/`.cursor`/`.claude`/`.codex` rule trees plus nested `AGENTS.md` files under session cwd, and skills from `skills.dirs`).
That coding-agent surface is **a productized profile on top of the harness**, not the only way to run Coddy.

## High-Level Architecture

```
┌──────────────────────────┐   ┌──────────────────────────────────┐
│   ACP client (editor)    │   │  Messenger (Telegram, …)         │
│      Zed / scripts       │   │  (build tag: gateway.telegram    │
└────────────┬─────────────┘   │             or gateway)          │
             │ JSON-RPC 2.0    └─────────────┬────────────────────┘
             │ over stdio                    │ long-polling
             ▼                               ▼
┌────────────────────────┐    ┌────────────────────────────────────┐
│   ACP Server Layer     │    │  Gateway Hub (external/gateway/)   │
│  initialize            │    │  one goroutine per adapter         │
│  session/new           │    │  auto-restart on error             │
│  session/prompt        │    └──────────────┬─────────────────────┘
│  session/cancel        │                   │
└────────────┬───────────┘                   │
             │                               │
             └──────────────┬────────────────┘
                            │
                            ▼
            ┌───────────────────────────────┐
            │        Session Manager        │
            │  per-session state, mode,     │
            │  history, rules, skills       │
            └───────────────┬───────────────┘
                            │
                            ▼
            ┌───────────────────────────────┐
            │      ReAct Agent Loop         │
            │  [THINK] → [ACT] → [OBSERVE]  │
            │  → loop or [ANSWER]           │
            └──────┬──────────┬─────────────┘
                   │          │
        ┌──────────┘    ┌─────┴──────┐   ┌─────────────┐
        ▼               ▼            ▼   ▼             │
   ┌─────────┐    ┌──────────┐ ┌──────────────┐        │
   │   LLM   │    │  Tools   │ │  MCP Clients │        │
   │Provider │    │Registry  │ │  (external)  │        │
   └─────────┘    └──────────┘ └──────────────┘        │
                                               ┌───────┘
                                               │
                                    ┌──────────▼────────────┐
                                    │  optional external/   │
                                    │  memory  scheduler    │
                                    └───────────────────────┘
```

## Component Descriptions

### ACP Server Layer (`internal/acp`)

Implements the JSON-RPC 2.0 server that speaks the ACP protocol over stdio.
Handles:
- `initialize` - version negotiation, capability exchange
- `session/new` - create session, connect MCP servers, return modes and Session Config Options (model + mode selectors)
- `session/load` - restore a persisted bundle from disk (**`$CODDY_HOME/sessions`** by default, usually **`~/.coddy/sessions`**), replay history via `session/update`
- `session/list` - enumerate persisted sessions (ACP `sessionCapabilities.list`)
- `session/prompt` - receive user message, start ReAct loop
- `session/cancel` - cancel in-progress turn
- `session/set_mode` - switch between `agent` and `plan` modes (legacy, kept in sync with config options)
- `session/set_config_option` - change mode or model for the session (preferred ACP API)

### Session Manager (`internal/session`)

Maintains the state for each conversation session:
- Conversation history (messages, tool results)
- Current operating mode (`agent` / `plan`)
- Optional model override per session (when the user selects a model via ACP)
- Connected MCP server clients
- Working directory
- Active context (skills + project rules in separate prompt sections)
- In-memory plan entries for todo tools (**`session.Plan`**), mirrored to **`todos/active.md`** when persistence is enabled (**`filesystem.go`**)

### ReAct Agent Loop (`internal/agent`)

The core reasoning engine (**`react.go`**):

1. Loads tool definitions from **`internal/tooling.Registry.AllToolDefinitions`**, applies the session **`ToolSet`** from **`internal/agent/toolsets.go`** (empty set means no filter), then appends MCP tool definitions from connected servers when the mode is **`agent`** or **`plan`**.
2. Builds the system prompt from **`internal/prompts.Render`**: embedded defaults or files under **`prompts.dir`** named by **`prompts.agent_prompt`** and **`prompts.plan_prompt`** (defaults **`agent.md`** and **`plan.md`**). Template data includes **`CWD`**, tools markdown, skills markdown, rules markdown (**`{{.Rules}}`** via **`internal/rules`**), optional **`TodoList`** and **`Memory`**, plus **`UTCNow`** (RFC3339 UTC refreshed on every render). Coddy then appends an **`<environment_context>`** block containing **`<os>`**, **`<arch>`**, and the detected **`<shell>`**, even when a custom prompt template is used.
3. Prepends that system message to the session message list and appends the newest user turn.
4. **Before every LLM invocation** inside one **`session/prompt`**, refreshes the **`system` message content** so **`TodoList`** and other template fields match state after prior tool calls in the same episode.
5. Streams the LLM response, executes tool calls, appends assistant and tool messages.
6. Loops until there are no tool calls, **`max_turns`** is exceeded, or cancellation.
7. On **`session/cancel`** (or HTTP **`POST /coddy/sessions/{id}/cancel`**) while the LLM stream is active, stream providers return **`context.Canceled`** together with any **`Response`** body accumulated so far; **`react.go`** appends that assistant **`content`** to session history when non-empty, then ends the turn with **`StopReasonCancelled`**. **`GET /coddy/sessions/{id}/messages`** can briefly trail that append until the filesystem bundle is read again.

### LLM Provider (`internal/llm`)

Abstracted interface for LLM backends. Configured via `config.yaml`.
Supported backends (see **`docs/config.md`** for shapes):
- OpenAI and OpenAI-compatible HTTP APIs (**`type: openai`**)
- Anthropic (**`type: anthropic`**)
- Ollama and other local OpenAI-compatible stacks (**`api_base`**)

### Tools Registry (`internal/tools`)

The **tool types and registry mechanics** live in **`internal/tooling`** (`Tool`, `Env`,
`Registry`, JSON `ParseArgs`, **`AllToolDefinitions`**). The **`internal/tools`** package is the
composition root (`NewRegistry` wires everything) and exposes the same APIs via type aliases so
call sites such as **`internal/agent`** keep importing **`tools`** only.

- **`internal/tools/web`** - **`websearch`** (DuckDuckGo text search) and **`webfetch`** (fetch public `http(s)` pages, readability + Markdown; SSRF guards)

Built-in implementations are grouped in subfolders under **`internal/tools/`**:

- **`internal/tools/fs`** - path helpers (`paths.go` with `ResolvePath`, `CheckInsideCWD`,
  `PathEscapesCWD`, `ToolPathsEscapeCWD`) and tools (`read.go` **`read`**, **`glob.go`** **`glob`**,
  **`grep.go`** **`grep`**, **`print_tree.go`** **`print_tree`** (directory tree, read-only),
  **`write.go`** **`write`**, **`edit.go`** **`edit`**, **`patch.go`**
  **`apply_patch`**, **`mkdir`**, **`rmdir`**, **`touch`**, **`rm`**, **`mv`**).
  **`grep`** uses a system **`rg`** when one is available (the pattern is passed to it untouched)
  and otherwise falls back to the built-in Go walker/matcher in **`search.go`**. **`glob`** uses
  the same built-in walker when **`rg`** is unavailable, so filesystem discovery also works in
  Windows and distroless binaries without sidecar executables.
- **`internal/platform`** - shared host shell detection: **`pwsh` → `powershell` → `cmd`** on Windows and **`bash` → `sh`** elsewhere; also renders the prompt environment context.
- **`internal/tools/shell`** - **`run_command`**, bound to the shared detected shell and documented to the model with platform-appropriate command examples.
- **`internal/tools/todo`** - todo/plan list (**`coddy_todo_plan_read`**, **`coddy_todo_plan_replace`**,
  **`coddy_todo_plan_archive`**, **`coddy_todo_item_add`**, **`coddy_todo_item_remove`**,
  **`coddy_todo_item_update`**, **`coddy_todo_item_move`**)

**Tool exposure** - **`internal/agent/toolsets.go`** defines a **`ToolSet`** name allowlist per mode. An **empty** `ToolSet` means **no filtering** (all tools registered in the session registry, plus MCP definitions). **Plan** mode uses a fixed allowlist on **registry** builtins (**`read`**, **`glob`**, **`grep`**, **`print_tree`**, **`websearch`**, **`webfetch`**, **`run_command`**, **`question`**, **`plan_exit`**), then MCP tools from connected servers are appended the same way as in agent mode.

Agents see:

- **`agent`** mode - every built-in registered by **`internal/tools.NewRegistryFor`** (filesystem, shell, todo, optional scheduler tools, **`websearch`**, **`webfetch`**, **`question`**, **`plan_exit`**, etc.) plus MCP tools from connected servers.
- **`plan`** mode - the allowlisted builtins above plus MCP tools. Built-in writes, todo tools, scheduler, and memory tools are not advertised to the LLM.

`run_command`, optional write paths, out-of-tree paths, and interactive **`question`** flows still coordinate with the client (**`session/request_permission`** for destructive paths; HTTP streaming uses **`event: question`** plus **`POST /coddy/sessions/{id}/question`**).

### Messenger Gateway (`external/gateway`)

The gateway is a separate process entry point (`coddy gateway`) that lets messenger bots (Telegram today, others via the same interface) drive the same session manager and ReAct loop used by `coddy acp` and `coddy http`.

Compiled only when built with **`-tags gateway.telegram`** (Telegram) or **`-tags gateway`** (all adapters). Without these tags the `coddy gateway` subcommand is present but returns a "not compiled" error.

**Key packages:**

| Package | Role |
|---------|------|
| `external/gateway` | `Adapter` interface, `Hub`, `Start()` entry point |
| `external/gateway/access` | Access control: `CanAccess`, `EffectiveAccess`, `EffectiveIsolation` |
| `external/gateway/sessionstore` | `Store`: maps stable chat/user keys to Coddy session IDs; `Reset` on `/clear` |
| `external/gateway/telegram` | `Bot` (polling, trigger rules, ACL), `Sender` (implements `acp.UpdateSender`) |

**Data flow for one incoming message:**

1. Adapter receives raw update, normalises it to `IncomingMessage`.
2. `access.CanAccess` rejects the message if the user fails the configured access level.
3. `sessionstore.SessionKey` derives a deterministic string key from gateway name, chat ID, user ID, and isolation mode.
4. `store.Get(key)` returns the current Coddy session ID for that key (creating one on first use).
5. `manager.EnsureHTTPSession` loads or creates the session bundle.
6. `manager.HandleSessionPromptWithSender` runs the ReAct loop with the adapter's `Sender`.
7. `sender.Flush()` sends accumulated text back to the chat.

**Extending with a new adapter** — implement `gateway.Adapter`, add a `Sender` that satisfies `acp.UpdateSender`, tag files with `//go:build gateway || gateway.<name>`, append to `Start()`. See [`docs/gateway.md`](gateway.md) for the full walkthrough.

### Optional `external` tool packages (scheduler, memory)

Some features live under **`external/`** and define tools that are **not** registered through **`internal/tools.NewRegistry`**, but still use the same **`internal/tooling.Tool`** shape as the core harness.

**Contract (mirror `external/scheduler/tools/job_get.go`):**

1. **One tool per file** - a package-local constructor returns **`*tooling.Tool`** with **`Definition`** (name, description, **`InputSchema`**) and **`Execute`** in one place. **`Execute`** takes **`context.Context`**, JSON args as a string, and **`*tooling.Env`** (use **`CWD`** or other fields when the tool needs session context; pass **`&tooling.Env{}`** when unused).
2. **JSON schema maps** - prefer **`map[string]interface{}`** for **`InputSchema`** and **`[]interface{}`** for **`required`** and enum lists so OpenAI and Anthropic marshaling stay consistent with existing scheduler tools.
3. **`register.go`** - collects constructors. **`external/scheduler/tools`** exposes **`RegisterTools`** for the main agent registry. **`external/memory/tools`** exposes **`PersistTools`**, **`RecallTools`**, **`ToolDefinitions`**, and **`Exec`** because the memory copilot runs a separate LLM loop in **`external/memory/copilot.go`**.
4. **Naming** - scheduler files use the **`job_*.go`** prefix; memory tool bodies use the **`mem_*.go`** prefix; **`external/memory/tools`** keeps **`env.go`**, **`names.go`**, **`register.go`** without the **`mem_`** prefix.

### MCP Client (`internal/mcp`)

Connects to external MCP servers specified in `session/new`. Supports:
- stdio transport (always available)
- HTTP transport (capability: `mcpCapabilities.http`)

Tools from MCP servers are appended to the LLM tool list in **`agent`** and **`plan`** modes (see **`internal/agent/react.go`**).

### Skills loader (`internal/skills`)

Loads `SKILL.md` from configured `skills.dirs` (see `docs/skills.md`). Default dirs (lowest → highest priority): **`~/.agents/skills`** (global, shared with `npx skills`/`npx skillsbd`), **`~/.coddy/skills`** (coddy-specific), **`${CWD}/.coddy/skills`** (project-local). Later dirs override earlier ones when the same skill name appears in multiple locations. Bundled **`/generate-rules`** is always prepended.

### Rules engine (`internal/rules`)

Discovers `.mdc` / `.md` rules from `.coddy/rules`, `.cursor/rules`, `.claude/rules`, `.codex/rules`, plus nested `**/AGENTS.md` files ([agents.md](https://agents.md/) convention), under session CWD. Injected into **`{{.Rules}}`** separately from skills; see **`docs/rules.md`**.

Activation uses globs, **`alwaysApply`**, **`@mention`**, and sticky auto rules (see **`docs/rules.md`**).

### Config (`internal/config`)

YAML-based configuration. Resolution uses **`CODDY_HOME`** (default **`~/.coddy`**), **`CODDY_CWD`**, **`CODDY_CONFIG`**, optional **`config.yaml`** in the process working directory when **`$CODDY_HOME/config.yaml`** is absent, and CLI flags (see **`docs/config.md`** and **`README.md`**).

## Session Modes

### `agent` mode (default)
- Full tool access (read, write, run commands)
- Executes tasks end-to-end
- Requests permission before destructive operations
- Suitable for: code generation, refactoring, debugging

### `plan` mode
- Narrow **registry** tool surface enforced by **`internal/agent.ToolSetForMode("plan")`**
- **`read`**, **`glob`**, **`grep`**, **`print_tree`**, **`websearch`**, **`webfetch`**, **`run_command`**, **`question`**, **`plan_exit`**, plus any **MCP** tools from configured servers
- No built-in workspace writes or **coddy** todo tools in the advertised set (switch to **agent** for those)
- Suitable for: design docs, specs, architecture planning, external research, and light shell or MCP inspection without offering full mutating builtins

Mode switching:
- Client calls `session/set_config_option` with `configId` `mode` (preferred) or `session/set_mode` with `agent` or `plan`
- Agent sends `current_mode_update` and `config_option_update` when mode changes

## Directory Structure

Top level after **`git clone`** (folder name is arbitrary; **`coddy-agent`** is common):

```
.
├── cmd/coddy/                   # CLI entry (acp, http, sessions, skills)
├── internal/                    # core harness (acp, session, agent, config, tools, …)
├── external/
│   ├── memory/                  # long-term memory copilot (`-tags memory`)
│   ├── httpserver/              # optional REST gateway (build tag http)
│   ├── ui/                      # Vite SPA sources (embedded when built with http+ui)
│   ├── scheduler/               # optional cron runner (build tag scheduler)
│   └── gateway/                 # messenger gateway (build tag gateway | gateway.telegram)
│       ├── access/              # ACL: CanAccess, EffectiveIsolation
│       ├── sessionstore/        # chat/user → session ID mapping
│       └── telegram/            # Telegram bot adapter (tgbotapi v5)
├── examples/                    # ACP and HTTP Python harnesses
├── docs/                        # guides (see docs/README.md)
├── Dockerfile
├── docker-compose.yml
├── docker-compose.dev.yml
├── config.example.yaml
├── go.mod
├── go.sum
└── README.md
```

Optional layers **`external/httpserver`**, **`external/ui`**, **`external/scheduler`**, and **`external/memory`** are omitted from the binary unless you pass the matching **Go build tags**; see **`docs/build.md`** and **`README.md`**. Long-term memory runtime behavior is toggled with **`memory.enabled`** when the binary was built with **`memory`**.
