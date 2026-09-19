# Session settings commands on every surface

Issues #260 (switch the model, thinking and reasoning level from the dialogue, for the session or for a number of turns) and #292 (switch the permission mode of the running session from the permission dialog). This record keeps the decisions as they were taken on 2026-09-19, after a plan review by Cursor, Coddy on gpt-oss-120b and Coddy on qwen3.8-27b and the operator's answers to its open questions.

## Where it started

A session already carried four overrides - model, reasoning level, operating mode, permission mode - and `Manager.HandleSessionSetConfigOption` validated them for ACP and the console. Everything else went around it:

- the web UI kept model, reasoning and mode in SPA state (and in cookies shared by every session) and sent them as `metadata` with every `POST /v1/responses`; the server applied them through `profileMetadataPatch` and `PATCH /coddy/sessions/{id}`, and nothing told a browser that a setting changed elsewhere (the bridge dropped `config_option_update`);
- `/model`, `/reasoning`, `/mode` existed only in the console, client-side; typed in the browser mid-turn, `/model X` was queued and reached the model as text;
- the provider and `toolEnv.PermissionMode` were fixed when a turn started;
- bypass was decided twice, by the agent's gate and by each surface's sender, and the HTTP bridge and `serve.defaultSender` read the config instead of the session - a session switched to `ask` on a server configured with `bypass` would still be auto-approved;
- there was no way to say "thinking off": Qwen3 never received `enable_thinking: false`;
- the permission-mode override was written to `meta.json` and restored after a restart.

## How the others do it

Claude Code applies `/model` and `/effort` mid-response from the next request; a skill's `model` and `effort` frontmatter lasts for the rest of the turn; its permission prompts can switch the session mode ("Yes, and switch to auto mode", "Yes, and bypass permissions"). Codex keeps settings on its app server and broadcasts a full `thread/settings/updated` snapshot to every client; `turn/settings/update` changes a running turn from its next step, never a child. opencode sends the model with every message, like our browser, and its web app re-implements the built-in commands. ACP 1.9.1 prefers `configOptions` (categories `mode`, `model`, `model_config`, `thought_level`), removed `session/set_model`, and has no per-prompt model field: a one-turn override over ACP is a command in the prompt text.

## Decisions

### One setter, one snapshot

`Manager.ApplySessionSettings(ctx, sessionID, SettingsChange)` (`internal/session/settings.go`) is the only way a setting changes: validation (configured model, a level the model offers, `off` where the provider has a real switch), the read-only refusal for child and job sessions, the write, `config_option_update` / `current_mode_update`, an info log line naming the source, an info row in the session's `uiLog`, and a notification to the settings observers with a full snapshot and a process-wide version. `HandleSessionSetConfigOption`, `PATCH /coddy/sessions/{id}`, the `/v1/responses` metadata, the console, the Telegram bot and the remote console all go through it. The HTTP server publishes the snapshot as `event: session_settings` on the turn stream and on `GET /coddy/events`; the browser mirrors it and stops overwriting the server with stale metadata.

### Scopes: session or a number of turns

A setting is changed for the session, or for the next N operator turns with `--once` (N = 1) or `--count=N`. Turn-scoped overrides are kept per setting with the number of turns left; a turn started by an operator consumes one from each and holds the values for its whole length, the queue's continuations and a permission resume included. A background wake and a subagent's turn consume nothing. A session-scoped change of a setting clears that setting's turn override, the running turn's included - the last explicit word wins. Overrides live in process memory: a restart forgets them.

A command followed by text applies to the turn that text starts. The override travels with the prompt and is installed after the turn is admitted, so a second tab or a wake cannot take it first.

### The commands

| Command | Setting | Argument |
|---|---|---|
| `/model <id>` | model | required |
| `/reasoning <level\|off\|default>` (alias `/effort`) | reasoning | required |
| `/think [level]` | reasoning on (the model's default level unless named) | optional |
| `/nothink` (alias `/no_think`) | reasoning off | none |
| `/agent`, `/plan`, `/ask` | operating mode | none |
| `/permissions <ask\|accept_edits\|bypass>` | permission mode | required in text; the console opens a picker without it |

Each takes `--once` or `--count=N` anywhere among its own words. `/mode` is removed from every surface, the console and the Telegram bot included, with no alias: next to `/model` in a menu it was a trap. Leading commands chain (`/model X --once /nothink --once review this`); the text after them is the prompt, and it may itself be `/compact` or a skill. A command mid-line is not a command. Only the text the operator typed is read, never a mention's attachment or a skill body. The registry wins over a skill of the same name.

The registry (`internal/session/commands.go`) is what `GET /coddy/commands`, ACP `available_commands_update` (with `input.hint`) and the console menu list; a row carries its kind, the setting, the argument hint, whether it may run during a turn, and its choices for the session.

### Where a command runs

The prompt path (`HandleSessionPromptWithSender`, the HTTP handler before it takes the turn lock, `POST .../queue`) takes leading settings commands off the text:

- nothing left: the settings are applied, no turn runs, nothing enters the model's history, and the surface gets a short notice;
- text left, session idle: session-scoped settings are applied, turn-scoped ones ride into the admitted turn;
- text left, a turn running: session-scoped settings apply at once and the text is queued as an ordinary message that is not parsed again; a turn-scoped command with text is refused, because the queue has no turn of its own.

A mid-turn change of model or reasoning takes effect at the next model request (the loop compares a settings revision before building a request, never inside a stream); the permission gate reads the mode on every tool call; the operating mode changes from the next turn. Running subagents keep what they were spawned with.

### The model may switch itself

`switch_model` (a tool, every mode, not for subagents) sets the model and/or the reasoning level for the rest of the turn or for the session, from the next request. A skill's frontmatter `model` and `reasoning` (alias `effort`) apply for the rest of the turn, whether the operator typed `/skill` or the model called `load_skill`. `spawn_agent` takes `model` and `reasoning`; a subagent definition takes `reasoning`; an id the config does not know is an error for the model to correct. The order for a child is argument, definition, the parent's model.

### Thinking off

`off` is offered where it maps to a real switch: Qwen3 on OpenAI-compatible servers (`chat_template_kwargs.enable_thinking: false`, no effort), Anthropic (no thinking block), the Codex backend (`none`), and a model whose configured levels include `none`. A gpt-5 model maps nothing to off (`minimal` is not off), o-series and gpt-oss have no switch. A stored level the new model does not offer falls back to its default visibly, in the snapshot. Claude 5 ids are detected as thinking models.

### #292: the permission dialog

While the effective mode is `ask`, every prompt that belongs to the session itself offers `allow_session_bypass`, and a file write also `allow_session_accept_edits` (both of kind `allow_always`). A relayed subagent prompt, a prompt a hook forced, and `config_commit` / `config_rollback` do not offer them; those two keep asking under a bypass switched on in the session, because a commit can rewrite the permission policy itself. The choice approves the call and switches the session, so the rest of the same turn runs without prompts. One helper decides the auto-approval in every sender - the child's stamp, then the session, then the config; the Telegram bot keeps approving its chat agent. The permission mode is no longer persisted: a restart returns to `tools.permission_mode`. The remote console may switch the server session's mode through the same `PATCH` as the browser.

### Surfaces

The console keeps its pickers, applies commands through the shared parser and the manager (local or remote), adds `/permissions`, and shows the permission mode and any turn override in its footer. The browser: the `/` menu lists the registry and offers the values of a setting after its name; picking one changes the composer's selector, which calls the setter; the selectors mirror the snapshot; a permission chip sits next to the mode. ACP editors run the commands as prompt text; the reasoning option moves to the `thought_level` category and the permission mode to `_permission_mode`. The Telegram bot passes the settings commands to the server instead of dropping them.
