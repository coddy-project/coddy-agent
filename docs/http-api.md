# OpenAI-compatible HTTP API

The `coddy http` subcommand ships only when the binary is built with **`go build -tags=http`** (`make build TAGS=http`). It exposes OpenAI-shaped routes plus **`/coddy/*`** helpers and a static SPA from **`GET /`**, backed by the same session manager and ReAct agent as **`coddy acp`**.

## OpenAPI and Swagger UI

Specs are regenerated on each request so they stay aligned with handlers.

- **`GET /openapi.yaml`** - OpenAPI 3.0 (YAML); **`GET /openapi.json`** mirrors it in JSON (both **`Content-Disposition: inline`**).
- **`GET /docs/`** - Swagger UI (static assets bundled in the binary, no CDN). **`GET /docs`** redirects to **`/docs/`**.

No authentication is enforced. Run behind appropriate network controls.

## Embedded web UI

- **`GET /`** serves **`index.html`**, **`styles.css`**, and **`app.js`** from **`external/ui/`** (`go:embed`).
- Recommended session URL pattern: **`#/s/<sessionId>`** (client-only history). For **`POST /v1/responses`** and **`POST /v1/chat/completions`**, send **`X-Coddy-Session-ID`** with a **`sess_<hex>`** id that satisfies server-side validation (`internal/session/validate.go`).
- **`GET /coddy/sessions`**, **`PATCH /coddy/sessions/{id}`**, and other **`/coddy/sessions/{id}/...`** routes identify the session **only** by the **`{id}`** path segment (no duplicate header in OpenAPI; clients may still send **`X-Coddy-Session-ID`**, it is not used to pick the resource for those URLs).
- The bundled UI calls **`POST /v1/responses`** with **`stream: true`**. SSE **default** chunks follow **`chat.completion.chunk`** deltas (**`delta.content`** for assistant text, **`delta.reasoning_content`** for streamed model reasoning so the UI can keep the **thinking** foldout separate). Named **`event:`** lines (**`tool_call`**, **`tool_call_update`**, **`plan`**, **`token_usage`**) expose tool progress and incremental token totals between LLM rounds.
- On the first user message of a brand-new chat (no session in the rail yet), the UI **`POST`**s **`/coddy/describe`** in parallel with **`/v1/responses`**. When **`short`** arrives, the header and session list titles update **immediately**; **`PATCH /coddy/sessions/{id}`** still runs once headers expose the canonical session id (**retries PATCH** briefly if persistence lags describe).

## Endpoint summary

| Method | Path | Notes |
|--------|------|-------|
| GET | `/` | Web UI (`index.html`). |
| GET | `/openapi.yaml`, `/openapi.json` | Spec. |
| GET | `/docs`, `/docs/` | Swagger UI. |
| GET | `/v1/models` | **`agent`** / **`plan`** (`owned_by=coddy-mode`). |
| POST | `/v1/chat/completions` | **`stream`**, **`messages`** (last **`user`**). |
| POST | **`/v1/responses`** | **`model`**, **`input`**, optional **`stream`**. Keeps history between turns when using headers. |
| GET | `/v1/responses/{id}` | Session metadata snapshot. |
| GET | **`/coddy/sessions`** | Pagination via **`limit`/`cursor`** (cursor is numeric offset token). |
| POST | **`/coddy/describe`** | JSON **`{"text"}`** to get a short description phrase. |
| GET | **`/coddy/sessions/{id}/messages`** | Serialized conversation snapshot. Cold loads require **`session.json`**. |
| GET | **`/coddy/sessions/{id}/tool-calls`** | Tool calls timeline for the session (previews). |
| GET | **`/coddy/sessions/{id}/tool-calls/{toolCallId}`** | Tool call details: meta, args JSON, result Markdown. |
| GET | **`/coddy/sessions/{id}/stats`** | Session stats: token usage totals (and optional per turn list). |
| PATCH | **`/coddy/sessions/{id}`** | **`{"title"}`** pins **`session.json.titlePinned`**. |
| DELETE | **`/coddy/sessions/{id}`** | Removes the entire persisted session directory (including `tool_calls/` and `stats.json`) plus in-memory MCP clients. |
| GET/PUT | **`/coddy/sessions/{id}/plan`** | Read or overwrite todo **`entries`** (ACP shape). |
| POST | **`/coddy/sessions/{id}/plan/archive`** | Archives active todos like **`coddy_todo_plan_archive`**. |
| GET | **`/coddy/sessions/{id}/memory/tree`** | Without **`root`**, lists **`global`** and **`workspace`**. Otherwise lists allowed **`.md` / `.txt`** children (traversal guarded). |
| GET | **`/coddy/sessions/{id}/memory/file`** | Query **`root`** + **`path`**. UTF-8 content. |
| PUT | **`/coddy/sessions/{id}/memory/file`** | JSON **`{"root","path","content"}`**. |
| POST | **`/coddy/sessions/{id}/memory/dir`** | JSON **`{"root","path"}`** for new subdirectory. |
| DELETE | **`/coddy/sessions/{id}/memory/file`** | Query **`root`** + **`path`**. |

## Sessions and headers

### Without **`X-Coddy-Session-ID`**

`POST /v1/chat/completions` / **`POST /v1/responses`** allocate a random session bundle for that flow (ACP **`session/new`**).

### With **`X-Coddy-Session-ID`**

1. Active in-memory bundle wins.
2. Otherwise load from disk when **`session.json`** exists.
3. Otherwise pin **`session/new`** with the supplied identifier (fresh empty bundle folders).

Malformed ids (**HTTP 400**). Dedicated **`/coddy/*`** helpers return **503** if persistence is unavailable (`Manager` lacked a **`FileStore`**, primarily in tests).

## Memory roots

Matches `external/memory/README.md`: **`global`** uses configured **`memory.dir`** (fallback **`$CODDY_HOME/memory`**). **`workspace`** resolves to **`$CWD/memory`** for that session bundle. **`agentMemory`** placeholders remain agent-only (`session.json`), not REST-editable here.

Interactive tool permission prompts are still bypassed whenever **`tools.permission_master_key`** is enabled; otherwise guarded tools behave like CLI sessions without confirmations.

## CLI flags

Equivalent to **`coddy acp`**: **`--config`**, **`--home`**, **`--cwd`**, **`--sessions-dir`**, **`--session-id`**, **`--log-*`**, optional **`--scheduler-enabled`**. Networking flags: **`-H` / `--host`**, **`-P` / `--port`**. Defaults fall back to YAML **`httpserver.host` / port** when left at **`0.0.0.0:12345`**.

## Build

```bash
make build TAGS=http
```

`go test ./...` skips this tree unless **`go test -tags=http`** (Makefile **`make test`** already covers it).
