# Examples and e2e harnesses

## Layout

| Path | Role |
|------|------|
| **`config.demo.yaml`** | Shared YAML for demos (models, scheduler, skills dirs, logger placeholder `__E2E_LOG_PATH__` where scripts rewrite it). |
| **`build_coddy.sh`** | `make build TAGS="http scheduler"` then `./build/coddy -v`. |
| **`httpserver/`** | All **`http_*.py`** probes and e2e demos plus **`test_httpserver.sh`** (starts a temp **`coddy http`**, runs the Python steps in order) and **`docker.sh`** (compose smoke). |
| **`acp/`** | All **`acp_*.py`** probes and e2e demos plus **`test_acp.sh`** (runs the Python steps against **`coddy acp`** with env from the script). |
| **`shared/`** | **`scheduler_e2e_common.py`** used by **`httpserver/http_scheduler_e2e_demo.py`** and **`acp/acp_scheduler_e2e_demo.py`**. |
| **`skills_fixture/`** | Bundled skill for slash-command HTTP demo (copied into **`$CODDY_HOME/skills_fixture`** by **`test_httpserver.sh`**). |

## HTTP gateway

From the repository root:

```bash
./examples/build_coddy.sh
./examples/test_httpserver.sh
```

Optional port: **`./examples/test_httpserver.sh 19900`**.

What runs (in order): smoke (**`/v1/models`**, chat, responses), scheduler REST (**`/coddy/scheduler/jobs`** CRUD smoke, no LLM), models metadata echo, agent todo, memory copilot, skills slash command, toolcalls persist. With **`SCHEDULER_AGENT_E2E=1`**, also **`http_scheduler_e2e_demo.py`** (needs a cooperative model and stable provider).

Docker-only smoke (no local **`coddy http`** binary on the host beyond compose build):

```bash
./examples/httpserver/docker.sh
```

## ACP stdio

```bash
./examples/build_coddy.sh
./examples/test_acp.sh
```

Order: smoke, models, agent todo, memory copilot, toolcalls persist. With **`SCHEDULER_AGENT_E2E=1`**, also **`acp_scheduler_e2e_demo.py`** (LLM plus cron, same caveats as HTTP).

Environment overrides are the same as before (see each script docstring): **`CODDY_BIN`**, **`CODDY_CONFIG`**, **`SESSION_ROOT`**, **`SESSION_ID`**, **`BASE_URL`**, **`MODEL`**, etc.

## Single demos

Run one Python file directly, for example:

```bash
export CODDY_BIN="$PWD/build/coddy"
export BASE_URL="http://127.0.0.1:19876/v1"
python3 examples/httpserver/http_smoke_basic.py
```

ACP demos live under **`examples/acp/`**; HTTP under **`examples/httpserver/`**.
