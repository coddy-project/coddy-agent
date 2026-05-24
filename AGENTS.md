# Agent Notes for Coddy

Short map for automation-friendly contributors.

## Repository navigation

| Area | Responsibility |
|------|------------------|
| `cmd/coddy` | CLI entry (`acp`, `http`, `sessions`, `skills`, `rules list`, `update`). |
| `internal/agent` | ReAct orchestration, MCP/tool wiring. |
| `internal/session` | Session manager, Filesystem persistence, Acp hooks, rules catalog. |
| `external/httpserver` | **`coddy http`** when built with **`tags=http`** (SSE bridge,Swagger statics,`/coddy` REST,ServeMux wiring). |
| `external/ui` | Embedded SPA (`go:embed`) when built with **`tags=http,ui`**. |
| `external/memory` | Long-term memory copilot (**`-tags memory`**; see README there). |

## Builds

Run **`make build TAGS=http`** for the HTTP gateway only (**`coddy http`** REST and **`/docs`**, no **npm**). Run **`make build TAGS="http ui"`** to link the embedded SPA (**Makefile** runs **ui-build** before **go build**). Recommended full image matches **`Dockerfile`** (**`make build TAGS="http ui scheduler memory"`**). Default **`make build`** omits HTTPServer, scheduler, and memory to keep dependency surface lean.

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
- For UI changes, update sources under **`external/ui/src/`** and rebuild embedded assets via **`make build TAGS="http ui"`** (runs **npm** via **make ui-build**).
- Run full regression `make test`, then `make lint`.

## UI sources (`external/ui/`)

**`DESIGN.md`** is the contract for layout, tokens, and SPA component behavior. After changing **`external/ui/src/`**, rebuild embedded assets with **`make build TAGS="http ui"`** before relying on **`go:embed`**.

The composer exposes **`Mode`** (**`agent`** / **`plan`**) and a separate **`Model`** YAML backend selector (**`metadata.model`**; list rows with **`owned_by`** other than **`coddy`** from **`GET /v1/models`**). Default YAML id comes from **`default_agent_model`**; persisted preference uses cookie **`coddy_llm_model`**. Parallel **`POST /v1/responses`** per session, **Stop** (**cancel** + partial assistant persistence), and transcript merge after **`GET .../messages`** are specified in **`DESIGN.md`** (**Multi-session streaming and Stop**) and **`docs/ui/spec.md`**.

**`MarkdownLineEditor`** (`external/ui/src/ui/markdown/`) is the shared markdown body editor (line gutter, wrap-aware numbering, active-line highlight, content-driven height). Used in the plan document card and scheduler job body. Visual and behaviour contract: **`DESIGN.md`** (**Markdown line editor**, **Plan mode plan document card**); functional checklist: **`docs/ui/spec.md`**.

## Python samples (`examples/`)

See **`examples/README.md`** for layout (**`examples/httpserver/`**, **`examples/acp/`**, **`examples/shared/`**). Scripts may use a project-local interpreter (`.venv` recommended); follow each script header for prerequisites.

- **`examples/build_coddy.sh`** - runs **`make build TAGS="http scheduler memory"`** (override **`TAGS`** as needed) and prints **`coddy -v`**.
- **`examples/test_acp.sh`** - wrapper that runs **`examples/acp/test_acp.sh`** (all ACP **`acp_*.py`** demos in one pass).
- **`examples/test_httpserver.sh`** - wrapper that runs **`examples/httpserver/test_httpserver.sh`** (temp **`coddy http`**, all HTTP harnesses including **`http_e2e_scheduler_api`** and **`http_e2e_scheduler_agent`**).

Example HTTP scripts that call completion endpoints expect a reachable provider and return non-zero on HTTP errors.
