# OpenAI-compatible HTTP API

The `coddy http` subcommand ships only when the binary is built with **`go build -tags=http`** (`make build TAGS=http`). It exposes OpenAI-shaped routes plus **`/coddy/*`** helpers and a static SPA from **`GET /`**, backed by the same session manager and ReAct agent as **`coddy acp`**.

## OpenAPI and Swagger UI

Specs are regenerated on each request so they stay aligned with handlers.

- **`GET /openapi.yaml`** - OpenAPI 3.0 (YAML); **`GET /openapi.json`** mirrors it in JSON (both **`Content-Disposition: inline`**).
- **`GET /docs/`** - Swagger UI (static assets bundled in the binary, no CDN). **`GET /docs`** redirects to **`/docs/`**.

No authentication is enforced. Run behind appropriate network controls.

## Embedded web UI

- **`GET /`** serves **`index.html`**, **`styles.css`**, and **`app.js`** from **`external/ui/`** (`go:embed`). Responses for **`/`**, **`/index.html`**, **`/app.js`**, and **`/styles.css`** include **`Cache-Control: no-cache`** so a normal reload picks up assets after you rebuild the binary (fixed URLs, no fingerprint).
- Recommended session URL pattern: **`#/s/<sessionId>`** (client-only history). For **`POST /v1/responses`** and **`POST /v1/chat/completions`**, send **`X-Coddy-Session-ID`** with a **`sess_<hex>`** id that satisfies server-side validation (`internal/session/validate.go`).
- **`GET /coddy/sessions`**, **`PATCH /coddy/sessions/{id}`**, and other **`/coddy/sessions/{id}/...`** routes identify the session **only** by the **`{id}`** path segment (no duplicate header in OpenAPI; clients may still send **`X-Coddy-Session-ID`**, it is not used to pick the resource for those URLs).
- The bundled UI calls **`POST /v1/responses`** with **`stream: true`**. For ReAct (**`model`** **`agent`** or **`plan`**), it sends optional **`metadata.model`** with the selected YAML **`models[].model`** **`id`** (from **`GET /v1/models`**, **`owned_by`** not **`coddy`**); default follows **`default_agent_model`**, persisted choice in **`coddy_llm_model`**. SSE **default** chunks follow **`chat.completion.chunk`** deltas (**`delta.content`** for assistant text, **`delta.reasoning_content`** for streamed model reasoning so the UI can keep the **thinking** foldout separate). Named **`event:`** lines (**`tool_call`**, **`tool_call_update`**, **`plan`**, **`token_usage`**, **`available_commands`**) expose tool progress and incremental token totals between LLM rounds; when **`memory.enabled`** is **true**, the server also emits **`memory_phase`** (**`sessionUpdate`** **`memory_phase`**) and **`memory_chunk`** (**`memory_message_chunk`**) for the unified memory copilot (**one pass before the main model**, recall-or-persist), streamed model text deltas only, without adding rows to **`messages.json`**. **`GET /coddy/sessions/{id}/messages`** may include **`memoryTurns`** (array of persisted per-turn summaries for the bundled UI transcript). **`coddy_meta`** JSON follows the usual rule immediately before **`data: [DONE]`**. **Stop generation** in the composer calls **`POST /coddy/sessions/{id}/cancel`** with **`X-Coddy-Session-ID`** matching **`{id}`**, then **`AbortSignal`** tears down the streamed **`fetch`**. That maps to ACP **`session/cancel`** for the agent turn (**`TurnCtx`** propagation for direct **`models[].model`** completions uses the same cancel hook).
- On the first user message of a brand-new chat (no session in the rail yet), the UI **`POST`**s **`/coddy/describe`** in parallel with **`/v1/responses`**. When **`short`** arrives, the header and session list titles update **immediately**; **`PATCH /coddy/sessions/{id}`** still runs once headers expose the canonical session id (**retries PATCH** briefly if persistence lags describe).

## Endpoint summary

| Method | Path | Notes |
|--------|------|-------|
| GET | `/` | Web UI (`index.html`); **`Cache-Control: no-cache`** also on **`/app.js`** and **`/styles.css`**. |
| GET | `/openapi.yaml`, `/openapi.json` | Spec. |
| GET | `/docs`, `/docs/` | Swagger UI. |
| GET | `/v1/models` | Merged list: **`agent`**, **`plan`** (each **`owned_by`**: **`coddy`**), then every YAML **`models[].model`** row (**`id`** is the selector; **`owned_by`** is the provider prefix). Ordering is **`agent`**, **`plan`**, then **`models`** in configuration order (same ordering the server emits). Use any returned **`id`** as **`model`** on POST. The JSON object also includes optional **`default_agent_model`**, the configured **`agent.model`** (**`models[].model`**), when set; the bundled UI uses it as the default **`metadata.model`** for ReAct (**`agent`** / **`plan`**) requests. |
| POST | `/v1/chat/completions` | **`stream`**, **`messages`** (last **`user`**). |
| POST | **`/v1/responses`** | **`model`**, **`input`**, optional **`stream`**. Keeps history between turns when using headers. |
| GET | `/v1/responses/{id}` | Session metadata snapshot. |
| GET | **`/coddy/sessions`** | Pagination via **`limit`/`cursor`** (cursor is numeric offset token). Optional **`q`** substring filter (**case-insensitive**) over **`session.json` title OR the **first persisted `user`** message **`content`** in `messages.json` order (`system`, `assistant`, `tool`, etc. before that first `user` are ignored). **`q` is not full-text search** over the transcript. Pagination applies **after** filtering. |
| POST | **`/coddy/describe`** | JSON **`{"text"}`** to get a short description phrase. |
| GET | **`/coddy/slash-commands`** | Paginated slash commands derived from configured skills (**`page`**, **`page_size`** required). Optional **`prefix`** filters by **`name`** (case-insensitive). Optional **`X-Coddy-Session-ID`** resolves **`${CWD}`** expansion for **`skills.dirs`** like other session-scoped lookups. JSON **`items[].name`** and **`items[].description`**, **`total`**, **`has_more`**. |
| GET | **`/coddy/sessions/{id}/messages`** | Serialized conversation snapshot. Cold loads require **`session.json`**. Assistant messages may include **`reasoning`** plus optional **`reasoning_duration_ms`** so the UI can restore the thinking timer after reload, and optional **`model`** (YAML selector for that reply). When memory copilot has produced trace data, **`memoryTurns`** lists persisted recap rows (parallel to **`messages**`, omitted from Chat Completions when calling the backend model). Optional **`uiLog`** holds UI-only lines (e.g. failed request errors) keyed by **`userTurnIndex`**; they are not in **`messages`** and are not forwarded to the LLM. Persisted under **`ui_log.json`**. |
| GET | **`/coddy/sessions/{id}/tool-calls`** | Tool calls timeline; each **`resultPreview`** is capped at **19** content lines plus a final **`...`** row when truncated (20 visible preview rows total; see **`resultPreviewTruncated`**, **`resultTotalLines`**). |
| GET | **`/coddy/sessions/{id}/tool-calls/{toolCallId}`** | Always full persisted **`result`** plus **`meta`** and **`args`**. The bundled SPA requests this when the user activates **Load more results** after the streamed or list preview was truncated (not when merely expanding the disclosure). |
| GET | **`/coddy/sessions/{id}/stats`** | Session stats: token usage totals (and optional per turn list). |
| PATCH | **`/coddy/sessions/{id}`** | **`{"title"}`** pins **`session.json.titlePinned`**. |
| POST | **`/coddy/sessions/{id}/cancel`** | Best-effort cancel of an in-flight ReAct turn or streamed direct completion (**ACP `session/cancel`**). Optional **`X-Coddy-Session-ID`** must match **`{id}`** when present. **`404`** when no live or persisted bundle exists under that **`id`** (session store required for cold loads). |
| DELETE | **`/coddy/sessions/{id}`** | Removes the entire persisted session directory (including `tool_calls/` and `stats.json`) plus in-memory MCP clients. |
| GET/PUT | **`/coddy/sessions/{id}/plan`** | Read or overwrite todo **`entries`** (ACP shape). |
| POST | **`/coddy/sessions/{id}/plan/archive`** | Archives active todos like **`coddy_todo_plan_archive`**. |
| GET | **`/coddy/sessions/{id}/memory/tree`** | Without **`root`**, lists **`global`** and **`workspace`**. Otherwise lists allowed **`.md` / `.txt`** children (traversal guarded). |
| GET | **`/coddy/sessions/{id}/memory/file`** | Query **`root`** + **`path`**. UTF-8 content. |
| PUT | **`/coddy/sessions/{id}/memory/file`** | JSON **`{"root","path","content"}`**. |
| POST | **`/coddy/sessions/{id}/memory/dir`** | JSON **`{"root","path"}`** for new subdirectory. |
| DELETE | **`/coddy/sessions/{id}/memory/file`** | Query **`root`** + **`path`**. |

## **`model`**, profiles, and direct completion

- **`POST /v1/chat/completions`** and **`POST /v1/responses`** require **`model`** to be exactly one **`id`** from **`GET /v1/models`** (session operating profiles **agent** / **plan** or a **`models[].model`** entry).
- **agent** / **plan** selects the Coddy agent (ReAct, tools). Optional JSON **`metadata.model`** (**`models[].model`** selector) updates the session **`SelectedModelID`**. Omit **`metadata`** or omit **`metadata.model`** to follow **`EffectiveModelID`** (session **`selectedModelId`** then **`agent.model`**). **`metadata.model`** set to **`null`**, **`""`**, or an unknown selector is **400**.
- **`model`** listing a **`models[].model`** YAML selector runs **one direct LLM request** per call (no ReAct); **`metadata`** with a **`model`** key on that branch is rejected with **400** (use **`model`** as the routing field only).

Non-stream replies include **`metadata`** with **`model`** set to the effective YAML backend selector (**`api_model`** may expose the REST model id).

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

For a manual gateway check against a disposable **`coddy http`** process, **`examples/test_httpserver.sh`** runs **`http_smoke_basic.py`** and the bundled HTTP demo scripts (they expect a working **`models`** backend where they call the LLM).
