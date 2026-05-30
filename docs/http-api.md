# OpenAI-compatible HTTP API

The `coddy http` subcommand ships only when the binary is built with **`go build -tags=http`** (`make build TAGS=http`). It exposes OpenAI-shaped routes plus **`/coddy/*`** helpers, backed by the same session manager and ReAct agent as **`coddy acp`**.

The bundled SPA is included only when you also set the **`ui`** tag (for example **`make build TAGS="http ui"`** or **`go build -tags=http,ui`**). With **http** only, **`GET /`** returns **404** with a plain-text hint.

## OpenAPI and Swagger UI

Specs are regenerated on each request so they stay aligned with handlers.

- **`GET /openapi.yaml`** - OpenAPI 3.0 (YAML); **`GET /openapi.json`** mirrors it in JSON (both **`Content-Disposition: inline`**).
- **`GET /docs/`** - Swagger UI (static assets bundled in the binary, no CDN). **`GET /docs`** redirects to **`/docs/`**.

No authentication is enforced. Run behind appropriate network controls.

## Embedded web UI (**`-tags=http,ui`**)

- **`GET /`** serves **`index.html`**, **`styles.css`**, and **`app.js`** from **`external/ui/`** (`go:embed`) when **`ui`** is set at link time with **`http`**. Responses for **`/`**, **`/index.html`**, **`/app.js`**, and **`/styles.css`** include **`Cache-Control: no-cache`** so a normal reload picks up assets after you rebuild the binary (fixed URLs, no fingerprint).
- Recommended session URL pattern: **`#/s/<sessionId>`** (client-only history). For **`POST /v1/responses`** and **`POST /v1/chat/completions`**, send **`X-Coddy-Session-ID`** with a **`sess_<hex>`** id that satisfies server-side validation (`internal/session/validate.go`).
- **`GET /coddy/sessions`**, **`PATCH /coddy/sessions/{id}`**, and other **`/coddy/sessions/{id}/...`** routes identify the session **only** by the **`{id}`** path segment (no duplicate header in OpenAPI; clients may still send **`X-Coddy-Session-ID`**, it is not used to pick the resource for those URLs).
- The bundled UI calls **`POST /v1/responses`** with **`stream: true`**. If **`GET /coddy/sessions`** reports **`turnActive`** for the open session (for example after a full tab reload while another client or the same server process still runs the turn), the UI subscribes to **`GET /coddy/sessions/{id}/composer-stream`** (SSE) to replay bytes already emitted for that turn and continue receiving live chunks until **`data: [DONE]`** or an error event. Starting a new **`POST /v1/responses`** cancels that relay subscription client-side. For ReAct (**`model`** **`agent`** or **`plan`**), it sends optional **`metadata.model`** with the selected YAML **`models[].model`** **`id`** and optional **`metadata.runPlanSlug`** to implement a saved design plan (**`coddy.dev/runPlanSlug`** on ACP **`session/prompt`**) (from **`GET /v1/models`**, **`owned_by`** not **`coddy`**); default follows **`default_agent_model`**, persisted choice in **`coddy_llm_model`**. It derives **`attachments`** from **`@path`** mentions in **`input`** (files only); **`GET /coddy/workspace/files`** powers the **`@`** picker. SSE **default** chunks follow **`chat.completion.chunk`** deltas (**`delta.content`** for assistant text, **`delta.reasoning_content`** for streamed model reasoning so the UI can keep the **thinking** foldout separate). Named **`event:`** lines (**`tool_call`**, **`tool_call_update`**, **`plan`**, **`token_usage`**, **`available_commands`**) expose tool progress and incremental token totals between LLM rounds; when **`memory.enabled`** is **true**, the server also emits **`memory_phase`** (**`sessionUpdate`** **`memory_phase`**) and **`memory_chunk`** (**`memory_message_chunk`**) for the unified memory copilot (**one pass before the main model**, recall-or-persist), streamed model text deltas only, without adding rows to **`messages.json`**. **`GET /coddy/sessions/{id}/messages`** may include **`memoryTurns`** (array of persisted per-turn summaries for the bundled UI transcript). **`coddy_meta`** JSON follows the usual rule immediately before **`data: [DONE]`**. **Stop generation** in the composer calls **`POST /coddy/sessions/{id}/cancel`** with **`X-Coddy-Session-ID`** matching **`{id}`**, then **`AbortSignal`** tears down the streamed **`fetch`**. That maps to ACP **`session/cancel`** for the agent turn (**`TurnCtx`** propagation for direct **`models[].model`** completions uses the same cancel hook). Cancel mid-stream still **persists** accumulated assistant **`content`** for that turn; **`GET /coddy/sessions/{id}/messages`** can briefly trail that write, so the SPA **merges** fetched rows with its per-session shadow or on-screen transcript (**`mergeTranscriptPreferLocalSuffix`** in **`external/ui/src/ui/chat/transcriptServerSnapshot.ts`**). More than one session may stream in parallel (**`pickStreamMutationBase`** in **`streamMutationBase.ts`** keeps SSE mutations scoped per **`X-Coddy-Session-ID`**).
- On the first user message of a brand-new chat (no session in the rail yet), the UI **`POST`**s **`/coddy/describe`** in parallel with **`/v1/responses`**. When **`short`** arrives, the header and session list titles update **immediately**; **`PATCH /coddy/sessions/{id}`** still runs once headers expose the canonical session id (**retries PATCH** briefly if persistence lags describe).
- The bundled UI exposes **Settings** at **`#/settings`**. It loads **`GET /coddy/config/schema`** (JSON Schema for the editor) and **`GET /coddy/config`** (current values, including **API keys** and optional per-provider **`proxy`** URLs), then **`POST /coddy/config/validate`** and **`PUT /coddy/config`** on save. There is no separate auth on these routes - expose **`coddy http`** only on trusted networks.

## Endpoint summary

| Method | Path | Notes |
|--------|------|-------|
| GET | `/` | Embedded web UI (**`-tags=http,ui`**) or **404** with a hint (**`http` only**). **`Cache-Control: no-cache`** on **`/`**, **`/index.html`**, **`/app.js`**, **`/styles.css`** when the UI module is linked. |
| GET | `/openapi.yaml`, `/openapi.json` | Spec. |
| GET | `/docs`, `/docs/` | Swagger UI. |
| GET | `/v1/models` | Merged list: **`agent`**, **`plan`** (each **`owned_by`**: **`coddy`**), then every YAML **`models[].model`** row (**`id`** is the selector; **`owned_by`** is the provider prefix). Ordering is **`agent`**, **`plan`**, then **`models`** in configuration order (same ordering the server emits). Use any returned **`id`** as **`model`** on POST. The JSON object also includes optional **`default_agent_model`**, the configured **`agent.model`** (**`models[].model`**), when set; the bundled UI uses it as the default **`metadata.model`** for ReAct (**`agent`** / **`plan`**) requests. |
| POST | `/v1/chat/completions` | **`stream`**, **`messages`** (last **`user`**). **`409`** when another **agent** or **plan** turn already holds the session turn lock (exclusive per persisted bundle). |
| POST | **`/v1/responses`** | **`model`**, **`input`**, optional **`stream`**, optional **`attachments`** ( **`path`** workspace-relative under session cwd, **`agent`** / **`plan`** only; server rejects traversal, oversized, non UTF-8, and folder **`path`**). **`input`** remains the full composer text including **`@path`** echoes. Keeps history between turns when using headers. **`409`** when another **agent** or **plan** turn already holds the session turn lock. |
| GET | `/v1/responses/{id}` | Session metadata snapshot. |
| GET | **`/coddy/sessions`** | Pagination via **`limit`/`cursor`** (cursor is numeric offset token). Optional **`q`** substring filter (**case-insensitive**) over **`session.json` title OR the **first persisted `user`** message **`content`** in `messages.json` order (`system`, `assistant`, `tool`, etc. before that first `user` are ignored). **`q` is not full-text search** over the transcript. Pagination applies **after** filtering. Rows sort by **`session.json` `updatedAt`** (newest first), then **`id`** for stable ties; **`updatedAt`** moves forward on persistence (new turns, title pin, etc.), not when the server only loads a bundle to serve **GET** requests. **`include_scheduler=true`** includes session bundles created for **scheduler runs** (they set **`schedulerRun`** in **`session.json`** and are **omitted** from the default composer list). **`include_activity=true`** adds **`turnActive`**, **`activitySeq`**, **`readActivitySeq`**, and **`unreadComplete`** to each row for multi-tab UI. |
| POST | **`/coddy/describe`** | JSON **`{"text"}`** to get a short description phrase. |
| GET | **`/coddy/slash-commands`** | Paginated slash commands derived from configured skills (**`page`**, **`page_size`** required). Optional **`prefix`** filters by **`name`** (case-insensitive). Optional **`X-Coddy-Session-ID`** resolves **`${CWD}`** expansion for **`skills.dirs`** like other session-scoped lookups. JSON **`items[].name`** and **`items[].description`**, **`total`**, **`has_more`**. |
| GET | **`/coddy/workspace/files`** | Paginated workspace listing under session **cwd** (**`page`**, **`page_size`** required). Non-blank **case-insensitive** substring **`prefix`** on **`path_rel`**; omit or blank **`prefix`** yields empty **`items`**. **`dirs=true`** adds directory rows (**`kind`** **`dir`**, **`path_rel`** trailing **`/`**). JSON **`items[].name`**, **`items[].path_rel`**, **`items[].kind`** (**`file`** or **`dir`**), **`total`**, **`has_more`**. |
| GET | **`/coddy/config/schema`** | JSON Schema (draft 2020-12) for the settings editor. Describes the same logical fields as **`ConfigJSON`** with **`description`**, **`default`**, **`pattern`** on **`providers[].name`**, optional **`x-coddy-*`** hints (for example **`x-coddy-provider-api-key-env-placeholder`** on **`providers[].api_key`** for dynamic placeholders), and **`x-coddy-property-order`** for section order. **`httpserver`** is omitted (listen bind stays on the **`coddy http`** CLI). **`GET`** / **`PUT`** **`/coddy/config`** still return the full document including **`httpserver`** when present in YAML. |
| GET | **`/coddy/config`** | Current process configuration as JSON (same logical fields as **`config.yaml`**, including secrets). |
| POST | **`/coddy/config/validate`** | Body is **`ConfigJSON`**. **`200`** with **`{"ok":true}`** when valid, **`400`** with **`{"ok":false,"error":"..."}`** otherwise. |
| PUT | **`/coddy/config`** | Body is **`ConfigJSON`**. Validates, writes **`config.yaml`** atomically, reloads in-process config for HTTP handlers and new agent turns. **`200`** **`{"ok":true}`**; **`400`** on validation failure; **`500`** if the file was written but reload failed (server restores **`config.yaml.bak`** over the primary path when possible). Existing sessions keep their current **MCP** client connections until you open a new session. |
| GET | **`/coddy/sessions/{id}/activity`** | Composer activity snapshot **`turnActive`** (exclusive turn lock), **`activitySeq`**, **`readActivitySeq`**, **`unreadComplete`**. |
| GET | **`/coddy/sessions/{id}/tool-calls`** | Tool calls timeline; each **`resultPreview`** is capped at **19** content lines plus a final **`...`** row when truncated (20 visible preview rows total; see **`resultPreviewTruncated`**, **`resultTotalLines`**). |
| GET | **`/coddy/sessions/{id}/tool-calls/{toolCallId}`** | Always full persisted **`result`** plus **`meta`** and **`args`**. The bundled SPA requests this when the user activates **Load more results** after the streamed or list preview was truncated (not when merely expanding the disclosure). |
| GET | **`/coddy/sessions/{id}/stats`** | Session stats: token usage totals (and optional per turn list). |
| GET | **`/coddy/sessions/{id}/messages`** | OpenAI-shaped **`messages`**, optional **`memoryTurns`**, **`uiLog`**. After **`POST .../cancel`**, the last assistant row may appear on disk one persistence step later than the in-flight stream; clients that already showed partial text should merge with local state (bundled UI uses **`mergeTranscriptPreferLocalSuffix`** in **`external/ui/src/ui/chat/transcriptServerSnapshot.ts`**). |
| GET | **`/coddy/sessions/{id}/composer-stream`** | **`text/event-stream`** relay of the live **agent**/**plan** turn (same SSE as **`POST /v1/responses`** with **`stream: true`**). Replays bytes captured so far, then live tail until the turn ends. SSE comment **`: composer stream pending`** while waiting for a relay (about 30s max), then **`event: error`** if none appears. |
| POST | **`/coddy/sessions/{id}/permission`** | Resolves a tool permission prompt while a streamed ReAct turn is blocked. Body **`{"toolCallId","optionId"}`** where **`toolCallId`** matches **`event: permission`** on the composer SSE (**`toolCall.toolCallId`**). **`optionId`** is **`allow`**, **`allow_always`**, or **`reject`** (or send **`outcome`** **`allow`** / **`cancelled`**). **`204`** when accepted. **`404`** when nothing is waiting. Optional **`X-Coddy-Session-ID`** must match **`{id}`** when set. |
| POST | **`/coddy/sessions/{id}/question`** | Answers the interactive **`question`** tool while a streamed ReAct turn is blocked. Body **`{"requestId","answers"}`** where **`answers`** is an array of string arrays (one row per question, entries are option labels or custom text). **`requestId`** must match **`event: question`** on the composer SSE. **`204`** when accepted. **`404`** when nothing is waiting. Optional **`X-Coddy-Session-ID`** must match **`{id}`** when set. |
| PATCH | **`/coddy/sessions/{id}`** | **`{"title"}`** pins **`session.json.titlePinned`**. **`{"markActivityRead":true}`** advances **`readActivitySeq`** to the current **`activitySeq`** (clears unread dot in composer UI) **without** changing **`session.json.updatedAt`**, so the session does not jump in the history list. At least one field is required. |
| POST | **`/coddy/sessions/{id}/cancel`** | Best-effort cancel of an in-flight ReAct turn or streamed direct completion (**ACP `session/cancel`**). For persisted bundles, also writes a cross-process cancel marker so another **`coddy`** process holding the turn can stop cooperatively. When the LLM had already streamed assistant tokens, the agent **persists** that partial **`assistant`** row before ending the turn (**`StopReasonCancelled`**). Optional **`X-Coddy-Session-ID`** must match **`{id}`** when present. **`404`** when no live or persisted bundle exists under that **`id`** (session store required for cold loads). |
| DELETE | **`/coddy/sessions/{id}`** | Removes the entire persisted session directory (including `tool_calls/` and `stats.json`) plus in-memory MCP clients. |
| GET/PUT | **`/coddy/sessions/{id}/plan`** | Read or overwrite todo **`entries`** (ACP shape). |
| POST | **`/coddy/sessions/{id}/plan/archive`** | Archives active todos like **`coddy_todo_plan_archive`**. |
| GET/POST | **`/coddy/sessions/{id}/plans`** | List or create design plan files **`plans/<slug>.plan.md`** (bundled UI; not core ACP). |
| GET/PUT/DELETE | **`/coddy/sessions/{id}/plans/{slug}`** | Read, autosave, or discard a design plan file. |
| PATCH | **`/coddy/sessions/{id}/plans/{slug}`** | Body **`{"runPlan":true}`** runs **`RunPlan`** (sync). Prefer **`POST /v1/responses`** with **`metadata.runPlanSlug`** for streaming. |

After **`PUT`** **`/coddy/config`**, the next **`agent`** or **`plan`** turn uses the updated **`models`**, **`tools`**, and other fields from the swapped in-memory config. **`mcp_servers`** changes apply fully to **new** sessions only.

### Session memory REST (**`-tags=http,memory`**)

These **`/coddy/sessions/{id}/memory/*`** routes register only when **`coddy`** is built with **`memory`** (**`external/httpserver/memory_http.go`**). A plain **`http`** build without **`memory`** returns **`404`** (**`memory_http_stub.go`**).

| Method | Path | Notes |
|--------|------|-------|
| GET | **`/coddy/sessions/{id}/memory/tree`** | Without **`root`**, lists **`global`** and **`workspace`**. Otherwise lists allowed **`.md` / `.txt`** children (traversal guarded). |
| GET | **`/coddy/sessions/{id}/memory/file`** | Query **`root`** + **`path`**. UTF-8 content. |
| PUT | **`/coddy/sessions/{id}/memory/file`** | JSON **`{"root","path","content"}`**. |
| POST | **`/coddy/sessions/{id}/memory/dir`** | JSON **`{"root","path"}`** for new subdirectory. |
| DELETE | **`/coddy/sessions/{id}/memory/file`** | Query **`root`** + **`path`**. Removes a **`.md` / `.txt`** file or deletes a directory tree recursively. **`400`** when **`path`** targets the memory root itself. |

### Scheduler REST (**`-tags=http,scheduler`**)

Paths are **missing** from a plain **`http`** build and from OpenAPI when **scheduler** is not linked (client **404**). When linked, responses use the same **`ErrorEnvelope`** JSON as other **`/coddy`** routes. **`503`** when **`scheduler.enabled`** is false for that process. Handlers plus merged scheduler OpenAPI fragments live in **`external/httpserver/scheduler_http.go`** (**`http`**,**`scheduler`**); **`scheduler_http_stub.go`** registers nothing when **scheduler** is omitted.

| Method | Path | Notes |
|--------|------|-------|
| GET | **`/coddy/scheduler/jobs`** | JSON **`{ scheduler, jobs[] }`**. Envelope **`scheduler`** includes **`enabled`**, resolved **`dir`**, **`timeout`**, **`max_queue`**, **`runs_active`**, **`retain_sessions`**. Optional query **`include_body=true`** embeds each job instruction body. Each **`jobs[].running`** is **true** only while this server process tracks an in-flight agent run for that job (not from a stray **`basename.lock`** alone). Stale locks older than a grace window are removed during list and daemon ticks. |
| POST | **`/coddy/scheduler/jobs`** | Create job. JSON body **`job_id`**, **`description`**, **`schedule`** (5-field UTC cron), optional **`paused`**, **`cwd`**, **`model`**, **`mode`**, **`body`**. **`201`** + **`Location`**. **`409`** when **`job_id`** already exists. |
| GET | **`/coddy/scheduler/jobs/{job_id}`** | Full **`SchedulerJob`** including **`body`**. |
| PUT | **`/coddy/scheduler/jobs/{job_id}`** | Replace entire job file. |
| PATCH | **`/coddy/scheduler/jobs/{job_id}`** | Merge fields (e.g. **`paused`**, **`schedule`**, **`body`**). Optional **`job_id`** in the body renames the on-disk job (moves **`.md`**, **`.state`**, **`.lock`** when idle). Response **`job_id`** is the effective id after the patch. **`409`** if the target id exists or the job is busy. |
| DELETE | **`/coddy/scheduler/jobs/{job_id}`** | Remove **`.md`** and sidecars **`basename.state`**, **`basename.lock`** when idle. **`409`** when locked or a run is tracked. |
| POST | **`/coddy/scheduler/jobs/{job_id}/pause`** | Sets YAML **`paused: true`**. |
| POST | **`/coddy/scheduler/jobs/{job_id}/resume`** | Sets **`paused: false`**. |
| POST | **`/coddy/scheduler/jobs/{job_id}/run`** | Fire-and-forget manual run (**`202`**). Does **not** advance cron **`.state`** (cron timing stays independent). **`409`** when paused, busy, or locked. |
| POST | **`/coddy/scheduler/jobs/{job_id}/cancel`** | **`context.Cancel`** on the active tracked run when possible. If nothing is tracked but an old **`basename.lock`** remains (e.g. after a crash), the server may remove that lock and still set **`cancelled`** to **true**. JSON **`{ cancelled: bool }`**. |
| GET | **`/coddy/scheduler/jobs/{job_id}/runs`** | Metadata for persisted runs (**`session_id`**, timestamps, **`status`**). **`limit`** query (default **50**, max **100**). Read full transcript with **`GET /coddy/sessions/{session_id}/messages`**. |

Process logs for scheduler should stay short; full traces live under **`sessions.dir`** in normal session layout with **`schedulerRun`** metadata.

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

### Per-session model (bundled UI)

- **`GET /coddy/sessions/{id}/messages`** returns **`model`** (effective YAML backend for the session) and **`selectedModelId`** (stored override in **`session.json`**, may be empty).
- **`PATCH /coddy/sessions/{id}`** accepts **`selectedModelId`** to set the YAML **`models[].model`** selector for that session (unknown ids **400**).
- The SPA restores **Model** from **`model`** when opening a chat. Changing **Model** writes cookie **`coddy_llm_model`** (default for the next **New chat**) and **`PATCH`**es the active session.

## Memory roots

Matches `external/memory/README.md`: **`global`** uses configured **`memory.dir`** (fallback **`$CODDY_HOME/memory`**). **`workspace`** resolves to **`$CWD/memory`** for that session bundle. **`agentMemory`** placeholders remain agent-only (`session.json`), not REST-editable here.

Interactive tool permission prompts are bypassed whenever **`tools.permission_master_key`** is enabled. Otherwise the bundled UI shows **`event: permission`** during **`POST /v1/responses`** (**`stream: true`**) and completes the turn via **`POST /coddy/sessions/{id}/permission`** (same pattern as **`question`**).

## CLI flags

Equivalent to **`coddy acp`**: **`--config`**, **`--home`**, **`--cwd`**, **`--sessions-dir`**, **`--session-id`**, **`--log-*`**, optional **`--scheduler-enabled`**. Networking flags: **`-H` / `--host`**, **`-P` / `--port`**. Defaults fall back to YAML **`httpserver.host` / port** when left at **`0.0.0.0:12345`**.

## Build

```bash
# HTTP API only (no npm, no embedded SPA root)
make build TAGS=http

# HTTP API plus embedded SPA (**make ui-build** runs first)
make build TAGS="http ui"
```

`go test ./...` skips **`external/httpserver`** unless **`go test -tags=http`**. SPA-specific tests compile under **`go test -tags=http,ui`** (Makefile **`make test`** runs **`ui-build`** once, then **`http`** and **`http,ui`** and scheduler combinations).

For a manual gateway check against a disposable **`coddy http`** process, **`examples/test_httpserver.sh`** runs the Python demos under **`examples/httpserver/`** (see **`examples/README.md`**). Steps that call chat or responses expect a working **`models`** backend.
