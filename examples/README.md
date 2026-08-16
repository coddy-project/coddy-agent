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
| **`e2e_memory`** | **`httpserver/http_e2e_memory.py`** | **`acp/acp_e2e_memory.py`** |
| **`e2e_background`** | **`httpserver/http_e2e_background.py`** (task list, live output, stop, 404) | **`acp/acp_e2e_background.py`** (persisted `background/<id>/meta.json` plus `output.log`) |
| **`e2e_toolcalls_persist`** | **`httpserver/http_e2e_toolcalls_persist.py`** | **`acp/acp_e2e_toolcalls_persist.py`** |
| **`e2e_compact`** | **`httpserver/http_e2e_compact.py`** (`/compact` prompt + REST endpoint) | **`acp/acp_e2e_compact.py`** (also auto threshold via tiny-window config) |
| **`e2e_skills_slash`** | **`httpserver/http_e2e_skills_slash.py`** | **`acp/acp_e2e_skills_slash.py`** |
| **`e2e_rules`** | **`httpserver/http_e2e_rules.py`** | **`acp/acp_e2e_rules.py`** |
| **`e2e_scheduler_api`** | **`httpserver/http_e2e_scheduler_api.py`** | (REST is HTTP-only) |
| **`e2e_scheduler_agent`** | **`httpserver/http_e2e_scheduler_agent.py`** | **`acp/acp_e2e_scheduler_agent.py`** |
| **`e2e_plan_files`** | **`httpserver/http_e2e_plan_files.py`** | **`acp/acp_e2e_plan_files.py`** |
| **`e2e_remote`** | **`httpserver/http_e2e_remote.py`** (self-boots authenticated + local servers) | (HTTP-only) |

## Layout

| Path | Role |
|------|------|
| **`config.demo.yaml`** | Shared YAML for demos (models, scheduler, skills dirs, logger placeholder **`__E2E_LOG_PATH__`** where scripts rewrite it). |
| **`build_coddy.sh`** | **`make build TAGS="http scheduler memory cli"`** then **`./build/coddy -v`**. |
| **`httpserver/`** | HTTP Python harnesses, **`test_httpserver.sh`**, **`docker.sh`**. |
| **`acp/`** | ACP Python harnesses and **`test_acp.sh`**. |
| **`cli/`** | Console TUI harnesses and **`test_cli.sh`** (pty-driven, Linux-only). |
| **`shared/`** | **`scheduler_e2e_common.py`**, **`plan_e2e_common.py`** for paired e2e harnesses. |
| **`skills_fixture/`** | Bundled skill for slash-command HTTP demo (copied into **`$CODDY_HOME/skills_fixture`** by **`test_httpserver.sh`**). |

## HTTP gateway

From the repository root:

```bash
./examples/build_coddy.sh
./examples/test_httpserver.sh
```

Optional port: **`./examples/test_httpserver.sh 19900`**.

**`test_httpserver.sh`** order: **`http_smoke_gateway`**, **`http_e2e_scheduler_api`** (REST CRUD plus on-disk **`$CODDY_HOME/scheduler/*.md`**), **`http_e2e_models`**, **`http_e2e_web`**, **`http_e2e_todo`**, **`http_e2e_memory`**, **`http_e2e_skills_slash`**, **`http_e2e_background`**, **`http_e2e_toolcalls_persist`**, **`http_e2e_compact`**, **`http_e2e_scheduler_agent`**, **`http_e2e_plan_files`** (plan mode **`plan_write`** to **`plans/e2e-plan.plan.md`**, then **`metadata.runPlanSlug`**). All steps run every time and need a working models backend where the LLM is called.

Docker-only smoke:

```bash
./examples/httpserver/docker.sh
```

## ACP stdio

```bash
./examples/build_coddy.sh
./examples/test_acp.sh
```

Order: **`acp_smoke_gateway`**, **`acp_e2e_models`**, **`acp_e2e_web`**, **`acp_e2e_todo`**, **`acp_e2e_skills_slash`**, **`acp_e2e_memory`**, **`acp_e2e_background`**, **`acp_e2e_toolcalls_persist`**, **`acp_e2e_compact`**, **`acp_e2e_scheduler_agent`**, **`acp_e2e_plan_files`** (plan file on disk plus run via **`_meta.coddy.dev/runPlanSlug`**).

Environment overrides: **`CODDY_BIN`**, **`CODDY_CONFIG`**, **`SESSION_ROOT`**, **`SESSION_ID`**, **`BASE_URL`**, **`MODEL`**, etc. (see each script docstring).

## Single demos

```bash
export CODDY_BIN="$PWD/build/coddy"
export BASE_URL="http://127.0.0.1:19876/v1"
export CODDY_HOME=...   # for http_e2e_scheduler_api when not using test_httpserver.sh
export WORK_DIR=...
python3 examples/httpserver/http_smoke_gateway.py
```

**`http_e2e_scheduler_agent.py`** expects an already running **`coddy http`** and **`BASE_URL`**, **`CODDY_HOME`**, **`WORK_DIR`** matching that process (as set by **`test_httpserver.sh`**).

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

CLI twins cover: smoke, models, web, todo, skills slash, rules, memory,
background, toolcalls persist, compact, plan files, scheduler agent, plus
console-unique permissions (ask-mode modal) and resume (transcript replay).
REST-only surfaces (`e2e_scheduler_api`, `e2e_remote`,
`e2e_background_reap`) have no console equivalent.
