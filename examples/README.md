# Examples and e2e harnesses

## Naming

Paired HTTP and ACP scripts share the same stem after the prefix:

| Stem | HTTP | ACP |
|------|------|-----|
| **`smoke_gateway`** | **`httpserver/http_smoke_gateway.py`** | **`acp/acp_smoke_gateway.py`** |
| **`e2e_models`** | **`httpserver/http_e2e_models.py`** | **`acp/acp_e2e_models.py`** |
| **`e2e_todo`** | **`httpserver/http_e2e_todo.py`** | **`acp/acp_e2e_todo.py`** |
| **`e2e_memory`** | **`httpserver/http_e2e_memory.py`** | **`acp/acp_e2e_memory.py`** |
| **`e2e_toolcalls_persist`** | **`httpserver/http_e2e_toolcalls_persist.py`** | **`acp/acp_e2e_toolcalls_persist.py`** |
| **`e2e_skills_slash`** | **`httpserver/http_e2e_skills_slash.py`** | **`acp/acp_e2e_skills_slash.py`** |
| **`e2e_scheduler_api`** | **`httpserver/http_e2e_scheduler_api.py`** | (REST is HTTP-only) |
| **`e2e_scheduler_agent`** | **`httpserver/http_e2e_scheduler_agent.py`** | **`acp/acp_e2e_scheduler_agent.py`** |

## Layout

| Path | Role |
|------|------|
| **`config.demo.yaml`** | Shared YAML for demos (models, scheduler, skills dirs, logger placeholder **`__E2E_LOG_PATH__`** where scripts rewrite it). |
| **`build_coddy.sh`** | **`make build TAGS="http scheduler"`** then **`./build/coddy -v`**. |
| **`httpserver/`** | HTTP Python harnesses, **`test_httpserver.sh`**, **`docker.sh`**. |
| **`acp/`** | ACP Python harnesses and **`test_acp.sh`**. |
| **`shared/`** | **`scheduler_e2e_common.py`** for **`e2e_scheduler_agent`** (HTTP and ACP). |
| **`skills_fixture/`** | Bundled skill for slash-command HTTP demo (copied into **`$CODDY_HOME/skills_fixture`** by **`test_httpserver.sh`**). |

## HTTP gateway

From the repository root:

```bash
./examples/build_coddy.sh
./examples/test_httpserver.sh
```

Optional port: **`./examples/test_httpserver.sh 19900`**.

**`test_httpserver.sh`** order: **`http_smoke_gateway`**, **`http_e2e_scheduler_api`** (REST CRUD plus on-disk **`$CODDY_HOME/scheduler/*.md`**), **`http_e2e_models`**, **`http_e2e_todo`**, **`http_e2e_memory`**, **`http_e2e_skills_slash`**, **`http_e2e_toolcalls_persist`**, **`http_e2e_scheduler_agent`** (model uses scheduler tools, waits for daemon tick and session side effects). All steps run every time and need a working models backend where the LLM is called.

Docker-only smoke:

```bash
./examples/httpserver/docker.sh
```

## ACP stdio

```bash
./examples/build_coddy.sh
./examples/test_acp.sh
```

Order: **`acp_smoke_gateway`**, **`acp_e2e_models`**, **`acp_e2e_todo`**, **`acp_e2e_skills_slash`**, **`acp_e2e_memory`**, **`acp_e2e_toolcalls_persist`**, **`acp_e2e_scheduler_agent`** (model plus scheduler daemon tick).

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
