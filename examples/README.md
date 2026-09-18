# Examples and e2e harnesses

## Naming

Paired HTTP, ACP, and console (CLI) scripts share the same stem after the
prefix; the CLI twins live in **`cli/`** and drive the interactive TUI in a
pty (see the CLI section below):

| Stem | HTTP | ACP |
|------|------|-----|
| **`smoke_gateway`** | **`httpserver/http_smoke_gateway.py`** | **`acp/acp_smoke_gateway.py`** |
| **`e2e_models`** | **`httpserver/http_e2e_models.py`** | **`acp/acp_e2e_models.py`** |
| **`e2e_web`** | **`httpserver/http_e2e_web.py`** | **`acp/acp_e2e_web.py`** |
| **`e2e_todo`** | **`httpserver/http_e2e_todo.py`** | **`acp/acp_e2e_todo.py`** |
| **`e2e_memory`** | **`httpserver/http_e2e_memory.py`** (a `remember` turn persisted by the memory subagent, the `memory` task row and its read-only child transcript) | **`acp/acp_e2e_memory.py`** (a seeded note recalled, a `remember` turn persisted, the fact asked again in a fresh session so it can only come from disk, the child bundle under `subagents/`) |
| **`e2e_background`** | **`httpserver/http_e2e_background.py`** (task list, live output, stop, 404) | **`acp/acp_e2e_background.py`** (persisted `background/<id>/meta.json` plus `output.log`) |
| **`e2e_background_wake`** | **`httpserver/http_e2e_background_wake.py`** (self-boots **`coddy serve`** on the scripted model of **`cmd/tgfake`**, no key: a failing command started with **`notify_on_finish`** wakes the agent; **`background_wake`** on **`GET /coddy/events`**, the woken turn's relay opening with the **`background_wake`** frame, the marker in **`GET .../messages`**, the task row's **`notify_on_finish`**; then the woken turn's permission prompt answered through **`POST .../permission`**) | **`acp/acp_e2e_background_wake.py`** (self-boots **`coddy acp`** on the same model: the **`background_wake`** update and the quoted note outside any **`session/prompt`**, no user message for the instruction, **`session/request_permission`** inside the woken turn, **`session/load`** replaying the wake) |
| **`e2e_subagents`** | **`httpserver/http_e2e_subagents.py`** (trust route, `spawn_agent` run as an `agent` task, read-only child transcript, `include_subagents`, catalog with declared bounds; then a detached child's permission prompt: `pending_permission` on the task row, `subagent_permission` on `GET /coddy/events`, answered against the child session) | **`acp/acp_e2e_subagents.py`** (`coddy agents trust`, persisted `agent` task plus the child bundle inside the parent's, with the parent link) |
| **`e2e_hooks`** | **`httpserver/http_e2e_hooks.py`** (catalog with the held project file, the notice row in the transcript, `POST /coddy/hooks/trust` then a turn that runs the approved hook, `untrust`) | **`acp/acp_e2e_hooks.py`** (user-scope `PreToolUse` / `PostToolUse` recorder hooks see a real `run_command`, the project-scope hook stays held until `coddy hooks trust .coddy/hooks.json`, then runs on the next turn) |
| **`e2e_toolcalls_persist`** | **`httpserver/http_e2e_toolcalls_persist.py`** | **`acp/acp_e2e_toolcalls_persist.py`** |
| **`e2e_compact`** | **`httpserver/http_e2e_compact.py`** (`/compact` prompt + REST endpoint); **`httpserver/http_e2e_compact_auto.py`** (self-boots a server whose model has no `max_context_tokens`: the `GET /v1/models` window equals the provider's listing and every `usage_update` size, and the session compacts by itself); **`httpserver/http_e2e_compact_clients.py`** (the sender, a second tab on the composer stream and an idle viewer all read the context usage fall after `/compact`, REST compact and an automatic compaction) | **`acp/acp_e2e_compact.py`** (also auto threshold via tiny-window config) |
| **`e2e_skills_slash`** | **`httpserver/http_e2e_skills_slash.py`** | **`acp/acp_e2e_skills_slash.py`** |
| **`e2e_rules`** | **`httpserver/http_e2e_rules.py`** | **`acp/acp_e2e_rules.py`** |
| **`e2e_mentions`** | **`httpserver/http_e2e_mentions.py`** (`GET /coddy/workspace/file`, `attachments[].source.startLine`/`endLine`, the typed `@file:N-M` grammar, `400` for lines past the end) | **`acp/acp_e2e_mentions.py`** (typed `@file:N-M`, a `resource` with a `#L5-5` URI fragment, refused range past the end) |
| **`e2e_config`** | **`httpserver/http_e2e_config.py`** (stage, confirm-commit, rollback; server config ends unchanged) | **`acp/acp_e2e_config.py`** (temp config copy; staged file, commit snapshot, rollback) |
| **`e2e_scheduler_api`** | **`httpserver/http_e2e_scheduler_api.py`** | (REST is HTTP-only) |
| **`e2e_scheduler_agent`** | **`httpserver/http_e2e_scheduler_agent.py`** | **`acp/acp_e2e_scheduler_agent.py`** |
| **`e2e_plan_files`** | **`httpserver/http_e2e_plan_files.py`** | **`acp/acp_e2e_plan_files.py`** |
| **`e2e_ask_mode`** | **`httpserver/http_e2e_ask_mode.py`** (ask profile reads, never writes; agent on the same session writes) | **`acp/acp_e2e_ask_mode.py`** (`session/set_mode` ask, then agent) |
| **`e2e_remote`** | **`httpserver/http_e2e_remote.py`** (self-boots authenticated + local servers) | (HTTP-only) |
| **`e2e_login`** | **`httpserver/http_e2e_login.py`** (self-boots servers behind the web UI sign-in: the account from `CODDY_HTTP_USER` / `CODDY_HTTP_PASSWORD` and from `coddy serve set-password`, the anonymous 401s, the cookie, the CSRF refusal, config redaction, sign-out, bearer parity, `login.enable: false`) | (HTTP-only) |

## Layout

| Path | Role |
|------|------|
| **`config.demo.yaml`** | Shared YAML for demos (models, scheduler, skills dirs, logger placeholder **`__E2E_LOG_PATH__`** where scripts rewrite it). |
| **`build_coddy.sh`** | **`make build TAGS="http scheduler memory cli gateway"`** then **`./build/coddy -v`**. |
| **`httpserver/`** | HTTP Python harnesses, **`test_httpserver.sh`**, **`docker.sh`**. |
| **`acp/`** | ACP Python harnesses and **`test_acp.sh`**. |
| **`cli/`** | Console TUI harnesses and **`test_cli.sh`** (pty-driven, Linux-only). |
| **`gateway/`** | **`tg_e2e_offline.sh`** (wrapper **`test_gateway.sh`**): the Telegram bot against the fake Bot API and scripted model of **`cmd/tgfake`**, no Telegram and no LLM involved (bash, Git Bash on Windows included). |
| **`shared/`** | **`scheduler_e2e_common.py`**, **`plan_e2e_common.py`**, **`ask_e2e_common.py`** for paired e2e harnesses; **`wake_e2e_common.py`**, the stand of the **`e2e_background_wake`** scripts (the scripted model of **`cmd/tgfake`** with a tool rule, a home and a config pointing at it, **`coddy serve`** on demand). |
| **`skills_fixture/`** | Bundled skill for slash-command HTTP demo (copied into **`$CODDY_HOME/skills_fixture`** by **`test_httpserver.sh`**). |
| **`agents_fixture/`** | Project-scope subagent definitions: **`.coddy/agents/marker-reporter.md`** (read-only, reports the `MARKER:` line of a named file), which each **`e2e_subagents`** script copies into its work dir and approves before the spawn, and **`.coddy/agents/echo-runner.md`** (`permission_mode: ask`, `background: true`, runs one named command), which the HTTP script approves for its detached-prompt phase. |

## Telegram offline stand

```bash
./examples/build_coddy.sh
./examples/test_gateway.sh                        # boots tgfake --llm and coddy serve --gateway, sends "hello", checks the reply, /clear then /resume back, then a background wake: the woken turn's note and answer in the chat
TG_E2E_KEEP=1 ./examples/test_gateway.sh             # leaves both running and prints the chat page URL
RICH_MESSAGES=true ./examples/test_gateway.sh
```

Knobs: **`TG_PORT`** (18790), **`LLM_DELAY`** (50ms), **`TG_VERBOSE`** (one line per Bot API call), **`CODDY_BIN`**. Guide: [Debugging against a fake Bot API](../docs/surfaces/gateway.md#debugging-against-a-fake-bot-api).

## HTTP gateway

From the repository root:

```bash
./examples/build_coddy.sh
./examples/test_httpserver.sh
```

Optional port: **`./examples/test_httpserver.sh 19900`**.

**`test_httpserver.sh`** order: **`http_smoke_gateway`**, **`http_e2e_scheduler_api`** (REST CRUD plus on-disk **`$CODDY_HOME/scheduler/*.md`**), **`http_e2e_models`**, **`http_e2e_web`**, **`http_e2e_todo`**, **`http_e2e_memory`**, **`http_e2e_skills_slash`**, **`http_e2e_rules`**, **`http_e2e_mentions`** (ranged `@file:3-4` attaches only those lines, both as `attachments[].source` and typed), **`http_e2e_background`**, **`http_e2e_subagents`** (`POST /coddy/subagents/marker-reporter/trust`, `spawn_agent` run as an `agent` task with its child session, read-only child transcript, `include_subagents`, catalog with declared bounds; then the `echo-runner` fixture asks for its command after the parent's reply, and the prompt is read from `pending_permission` on the task row, replayed by `GET /coddy/events`, answered against the child session and announced settled), **`http_e2e_toolcalls_persist`**, **`http_e2e_compact`**, **`http_e2e_compact_auto`** (a model without `max_context_tokens` compacts at the window its provider reports, the one the web UI ring shows; SKIP without a NeuralDeep key), **`http_e2e_compact_clients`** (three clients of one session read the context usage fall after every kind of compaction; SKIP without a key), **`http_e2e_scheduler_agent`**, **`http_e2e_plan_files`** (plan mode **`plan_write`** to **`plans/e2e-plan.plan.md`**, then **`metadata.runPlanSlug`**), **`http_e2e_ask_mode`** (**`model: ask`** reads the note and refuses the write, **`model: agent`** on the same session writes it), **`http_e2e_config`** (staged uci-like config edit, Russian confirm-commit, rollback from the snapshot; the server config ends the script unchanged), **`http_e2e_remote`** (auth gate, config redaction, workspace parity, local fallback), **`http_e2e_login`** (the web UI sign-in: the form from the environment and from `coddy serve set-password`, an anonymous browser refused, the cookie opening the API and the event stream, a cross-site write refused, sign-out, a bearer client unaffected), **`http_e2e_background_reap`** (a background process that outlived a killed coddy is found and reaped). All steps run every time and need a working models backend where the LLM is called.

Docker-only smoke:

```bash
./examples/httpserver/docker.sh
```

## ACP stdio

```bash
./examples/build_coddy.sh
./examples/test_acp.sh
```

Order: **`acp_smoke_gateway`**, **`acp_e2e_models`**, **`acp_e2e_web`**, **`acp_e2e_todo`**, **`acp_e2e_skills_slash`**, **`acp_e2e_rules`**, **`acp_e2e_mentions`** (typed `@file:3-4` and a `#L5-5` resource fragment hydrate only those lines), **`acp_e2e_config`** (staged config edit into a temp config copy, Russian confirm-commit, rollback), **`acp_e2e_memory`**, **`acp_e2e_background`**, **`acp_e2e_subagents`** (`coddy agents trust` before the spawn, persisted `agent` task plus the child bundle nested in the parent's, linked back to it), **`acp_e2e_hooks`** (user-scope recorder hooks around a real `run_command`, a held project hook approved with `coddy hooks trust` mid-session), **`acp_e2e_toolcalls_persist`**, **`acp_e2e_compact`**, **`acp_e2e_scheduler_agent`**, **`acp_e2e_plan_files`** (plan file on disk plus run via **`_meta.coddy.dev/runPlanSlug`**), **`acp_e2e_ask_mode`** (**`session/set_mode`** **`ask`**: read-only tool calls and no artifact, then **`agent`** writes it), **`acp_remote`** (the ACP client against a remote `coddy serve`), **`acp_e2e_remote_subagents`** (a project definition approved on the server through `POST /coddy/subagents/{name}/trust`, then a `spawn_agent` run whose call streams back to the remote ACP client and whose child session is persisted server-side only).

Environment overrides: **`CODDY_BIN`**, **`CODDY_CONFIG`**, **`SESSION_ROOT`**, **`SESSION_ID`**, **`BASE_URL`**, **`MODEL`**, etc. (see each script docstring).

## Single demos

```bash
export CODDY_BIN="$PWD/build/coddy"
export BASE_URL="http://127.0.0.1:19876/v1"
export CODDY_HOME=...   # for http_e2e_scheduler_api when not using test_httpserver.sh
export WORK_DIR=...
python3 examples/httpserver/http_smoke_gateway.py
```

**`http_e2e_scheduler_agent.py`** expects an already running **`coddy serve`** and **`BASE_URL`**, **`CODDY_HOME`**, **`WORK_DIR`** matching that process (as set by **`test_httpserver.sh`**).

## Console TUI e2e (`cli/`)

`./examples/test_cli.sh` runs every console scenario against a live model
(default **`MODEL=neuraldeep/qwen3.8-27b`**; the provider key resolves from
**`NEURALDEEP_API_KEY`**, or the runner copies that single line from
`~/.coddy/.env` into each script's temp `CODDY_HOME/.env`).

The driver (**`cli/cli_tui_driver.py`**) spawns `coddy cli --plain --theme
dark` in a pty via **pexpect** and emulates the screen with **pyte** —
Linux-only, and the python deps are mandatory (no silent skip):

```bash
python3 -m pip install -r examples/cli/requirements.txt
```

Every script gets an isolated temp `CODDY_HOME` and workdir; assertions
target persisted session artifacts (`sessions/<id>/...`) plus deterministic
screen chrome. Knobs: `CLI_E2E_ONLY=<stem>` (one script),
`CLI_E2E_CORE=1` (fast subset), `CLI_E2E_KEEP=1` (keep temp dirs),
`CLI_E2E_TIMEOUT` (per-script seconds, default 600).

CLI twins cover: smoke, models, web, todo, skills slash, rules, mentions (ranged `@file:3-4`), memory,
background, background wake (`cli_e2e_background_wake.py`: no model at all - the scripted model of
`cmd/tgfake` - the local console woken into a turn that shows nothing of its own and a `/tasks` row
that says `woke the agent`, its permission modal, then the same over `--remote` against a
self-booted `coddy serve`), subagents (`coddy agents trust` then a `spawn_agent` run), toolcalls
persist, compact, plan files, ask mode, scheduler agent, plus
console-unique permissions (ask-mode modal) and resume (transcript replay).
REST-only surfaces (`e2e_scheduler_api`, `e2e_remote`,
`e2e_background_reap`) have no console equivalent.

## `swarm/`

`swarm_e2e.py` boots a real swarm - three `coddy serve` relays wired into a ring, one agent
that the relay dials and one that can only dial out - and checks that a client drives a node
two relays away, that every session arrives in one labelled list, that a session id shared by
two agents stays two sessions, that search reaches both the work and the machine, that the
ring is reported once with the short route chosen, that the control plane stays off the
mounts, and that a node which dies becomes a warning rather than an error.

Run it with `examples/test_swarm.sh`. Needs `build/coddy` built with `-tags "http swarm"`; no
model or provider is involved.
