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

## Python samples (`examples/`)

Scripts may bootstrap project-local interpreters (`.venv` recommended); follow each script header for prerequisites.
