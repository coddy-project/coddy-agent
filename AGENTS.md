# Agent Notes for Coddy

Short map for automation-friendly contributors.

## Repository navigation

| Area | Responsibility |
|------|------------------|
| `cmd/coddy` | CLI entry (`acp`, `http`, `sessions`, `skills`). |
| `internal/agent` | ReAct orchestration, MCP/tool wiring. |
| `internal/session` | Session manager, Filesystem persistence, Acp hooks. |
| `external/httpserver` | **`coddy http`** when built with **`tags=http`** (SSE bridge,Swagger statics,`/coddy` REST,ServeMux wiring). |
| `external/ui` | Embedded SPA assets (`go:embed`), consumed only from httpserver builds. |
| `external/memory` | Long-term memory copilot tooling (see README there). |

## Builds

Run `make build TAGS=http` for OpenAI-compatible HTTP + UI. Default `make build` omits HTTPServer to keep dependency surface lean.

Primary conversational surface for bundled UI lives at **`POST /v1/responses`** with **`stream:true`**. Prefer it over **`POST /v1/chat/completions`** when shipping Coddy-hosted experiences.

Swagger lives at **`/docs/`**, OpenAPI YAML at **`/openapi.yaml`**.

## Documentation contract

Human prose for HTTP lives in **`docs/http-api.md`**. Visual spec for SPA lives in **`DESIGN.md`** (this repo root). Architectural narrative remains under **`docs/architecture.md`**.

All **code comments** plus **technical markdown authored for this repo** (including `docs/`, `DESIGN.md`, `AGENTS.md`) stay **English** unless an operator explicitly asks for another natural language.

## HTTP API development flow

When changing behavior for the OpenAI-compatible HTTP gateway or bundled UI:

- Add or update tests first (red), then implement (green).
- If the external HTTP surface changes, update `external/httpserver/openapi.go` so the served OpenAPI matches handlers in `external/httpserver/server.go`.
- Keep `docs/http-api.md` aligned with the live behavior.
- For UI changes, update sources under `external/ui/src/` and rebuild embedded assets via `make build TAGS=http` (this runs the UI build and sync step).
- Run full regression `make test`, then `make lint`.

## UI sources (`external/ui/`)

**`DESIGN.md`** is the contract for layout, tokens, and SPA component behavior. After changing **`external/ui/src/`**, rebuild embedded assets with **`make build TAGS=http`** before relying on **`go:embed`**.

The composer exposes **`Mode`** (**`agent`** / **`plan`**) and a separate **`Model`** YAML backend selector (**`metadata.model`**; list rows with **`owned_by`** other than **`coddy`** from **`GET /v1/models`**). Default YAML id comes from **`default_agent_model`**; persisted preference uses cookie **`coddy_llm_model`**.

## Python samples (`examples/`)

Scripts may bootstrap project-local interpreters (`.venv` recommended); follow each script header for prerequisites.

- **`examples/build_coddy.sh`** - runs **`make build TAGS=http`** and prints **`coddy -v`**.
- **`examples/test_acp.sh`** - drives **`coddy acp`** (`acp_smoke_basic.py`, **`acp_models_e2e_demo.py`**, then the longer ACP demos in that script).
- **`examples/test_httpserver.sh`** - starts a temporary **`coddy http`** and runs **`http_smoke_basic.py`**, **`http_models_e2e_demo.py`**, then the HTTP demos listed in that script (optional port argument).
- **`examples/test_httpserver_skills_slash.sh`** - same **`examples/config.demo.yaml`** as other HTTP demos; copies **`examples/skills_fixture/`** into **`${CODDY_HOME}/skills_fixture`** and runs **`http_skills_slash_e2e_demo.py`** ( **`GET /coddy/slash-commands`**, **`POST /v1/responses`** with **`/coddy_slash_demo`**, control turn).
- **`examples/run_full_test.sh`** - build step then **`test_acp.sh`** and **`test_httpserver.sh`**.

Example HTTP scripts that call completion endpoints expect a reachable provider and return non-zero on HTTP errors.
