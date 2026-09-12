# Operating modes

A session runs in one of three modes, `agent`, `plan` or `ask`. The mode decides which tools the model is offered on a turn and, for `ask`, which calls may run at all. This page lists what each mode allows, how plan mode hands its document over to implementation, how ask mode is enforced when a call executes, and how to switch on every surface. The mode is separate from the permission mode (`ask`, `accept_edits`, `bypass`), which decides when the agent prompts before a command or a file write; the two combine.

## The three modes

### `agent`

The default, made for implementing, refactoring, debugging and running things. The model is offered every built-in in the registry - the read tools of the other two modes plus file writes and edits, the shell with its background tasks, the todo checklist, the staged config tools, the plan tools and `spawn_agent` - and the tools of every connected MCP server. Which of those calls prompt you first is the permission mode's business, not the operating mode's.

### `plan`

Made for design documents, specs and the investigation that precedes a change. The model is offered a fixed allowlist:

- `read`, `keep_result`, `glob`, `grep` and `print_tree` for the repository;
- `websearch` and `webfetch` for the web;
- `run_command`, with `background_list`, `background_output`, `background_wait` and `background_stop`, so a slow read-only command need not be waited on;
- `question`, and the read-only `config_get` and `config_changes` (staging and committing a configuration change stay in agent mode);
- `plan_write`, `plan_list` and `plan_read` for the plan document;
- `load_skill`, and `spawn_agent`, whose child stays in plan mode;
- the tools of connected MCP servers.

No built-in file writes and no todo tools: once the plan is ready, implementation happens in agent mode.

### `ask`

Made for questions about the codebase, review, diagnosis and web research. The model is offered `read`, `keep_result`, `glob`, `grep`, `print_tree`, `websearch`, `webfetch`, `question` and `load_skill`, and nothing else: no shell, no plan, todo or config tools, no `spawn_agent`, no MCP tools. The same list is enforced again when a call runs, which is what separates ask from plan (see [Ask mode at execution time](#ask-mode-at-execution-time)).

### What they share

The allowlists are fixed in `internal/agent.ToolSetForMode`; agent mode is unrestricted. `load_skill` exists only while `skills.auto_discovery` is on, and `spawn_agent` only while `subagents.enable` is; a subagent definition's own `mode` applies only under an agent-mode parent. Each mode renders its own system prompt from the embedded `agent.md`, `plan.md` or `ask.md`, replaceable through `prompts.dir` with `prompts.agent_prompt`, `plan_prompt` and `ask_prompt` ([config reference](../reference/config.md#prompts)). The choice is stored with the session in `session.json`.

![The mode menu in the web UI composer](../assets/modes-menu-dark-1280.png)

*The Mode menu of the web UI composer lists Agent, Plan and Ask; the Model selector sits next to it.*

## Plan mode and the plan document

In plan mode the model records its design with `plan_write`, which writes `plans/<slug>.plan.md` into the session bundle: YAML frontmatter (`name`, `overview`, `todos`) and a markdown body. `plan_list` and `plan_read` find and reread those files. After every `plan_write` Coddy publishes a `plan` session update whose checklist entries come from the frontmatter, so any ACP client shows a preview, and persists a `plan_document` row in `messages.json` for the web UI.

The web UI renders that row as the plan document card in the chat column:

- **Collapsed**: the title (`name`, else the slug, with the file path as tooltip), a one-line description, and **Discard** and **Run plan** in the footer.
- **Expanded**: the rendered markdown by default; the eye toggle switches to the markdown line editor, which holds the body without the frontmatter and autosaves through `PUT /coddy/sessions/{id}/plans/{slug}` about 600 ms after the last keystroke. The pane grows with the document instead of scrolling inside itself.
- **Discard** (`DELETE` on the same route) marks the plan discarded: the card stays in the transcript, muted, with its controls disabled, and the plan leaves the plan-mode system prompt.
- **Run plan** sends the next prompt with `metadata.runPlanSlug` (`_meta` `coddy.dev/runPlanSlug` over ACP): Coddy switches the session to agent mode, injects the plan body into the system prompt and runs the turn. The session todo list is not filled from the plan.
- In the read-only transcript of a subagent child the card has no footer and its editor is read-only.

The portable route, for a client without that hook, is to switch to agent mode and mention `@plans/<slug>.plan.md` in the prompt. Details: [Web UI](../surfaces/web-ui.md#plan-document-card-plan-mode-transcript), [ACP protocol](../reference/acp-protocol.md#design-plans-plan-mode).

![The transition from plan mode to agent mode in the web UI](../assets/plan-exit-preview-dark.png)

*The card the web UI draws when a session leaves plan mode for agent mode (shown in the Russian locale).*

## Ask mode at execution time

Plan mode restricts what the model is offered; ask mode also restricts what may run. Every tool call is checked against the ask allowlist again when it is about to execute, because a model can replay a call from earlier history recorded in another mode, or name a tool it was never shown. A call outside the set is not executed:

- the tool result is `error: tool "write" is not available in Ask mode because it is not read-only`, and the model is re-prompted with that refusal;
- over HTTP the stream carries the call as cancelled with the same text;
- MCP tool names are refused the same way, since they are never in the list;
- approving such a pending call with *allow always* records no grant.

The rest of the boundary follows from it. A `metadata.runPlanSlug` on `POST /v1/responses` with `model` `ask` is answered with `409` before the turn starts, the same hook over ACP is refused with an error, and a `@plans/<slug>.plan.md` mention is inlined as reading material rather than run. The memory copilot recalls but never saves. `spawn_agent` is not offered, so an ask turn cannot delegate. The deterministic operator commands typed as the prompt (`/compact`, `/plugin`) are outside this boundary. The happy paths are `features/ask_mode.feature` and `features/ask_mode_http.feature`.

![The composer in ask mode](../assets/modes-pill-hero-dark-1280.png)

*The composer in ask mode: the pill takes the green outline of the mode, plan mode takes the orange one.*

## Switching on each surface

| Surface | How |
|---|---|
| Web UI | the **Mode** pill in the composer, next to **Model**; the choice travels as the top-level `model` of `POST /v1/responses` |
| Console | `/mode` in the chat, or `--mode agent\|plan\|ask` at launch, which also combines with `-c`, `--resume` and `-p`; in `--remote` mode `/mode` picks the profile per turn |
| ACP | `session/set_config_option` with `configId` `mode` and `value` `agent`, `plan` or `ask` (preferred), or the legacy `session/set_mode` with `modeId`; the agent answers with `current_mode_update` and `config_option_update`, and `session/new` advertises the three in `configOptions` and `modes` |
| HTTP API | `model` set to `agent`, `plan` or `ask` on `POST /v1/responses` or `POST /v1/chat/completions`; `GET /v1/models` lists the three with `owned_by` `coddy`, and `metadata.model` picks the backend |
| Telegram | `/mode` opens an inline keyboard with the three modes |
| Scheduler | `mode:` in the job file frontmatter, `agent` when omitted |

A one-shot run picks the mode the same way: `coddy --mode ask -p "..."` answers without touching the workspace. Surface guides: [Console (TUI)](../surfaces/console.md), [Web UI](../surfaces/web-ui.md), [ACP protocol](../reference/acp-protocol.md), [HTTP API](../reference/http-api.md), [Telegram gateway](../surfaces/gateway.md), [Scheduler](../operate/scheduler.md).
