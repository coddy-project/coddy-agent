# Coddy embedded UI specification

This page captures the original UI requirements and the intended end state. It is a functional spec and a design contract.

## Constraints

- UI ships as static assets embedded into the `coddy` binary (build tag `http`).
- Runtime has no auth and no API key checks for the UI.
- UI must work over the same origin as `coddy http`.
- UI copy is English.

## Layout

Three pane layout on desktop

- Left rail with a new chat action
- Sessions list with pagination and per session actions
- Main chat area with streamed assistant output
- Right rail with todo plan and long term memory file tree

Header links

- GitHub link to `https://github.com/coddy-project/coddy-agent`
- API docs link to `/docs/`

## Sessions

- Session id is generated client side.
- Session id is persisted in the URL fragment.
  - Recommended format `#/s/<sessionId>`
- Session id is sent in the `X-Coddy-Session-ID` header for chat transport.
- Session id validation matches `internal/session/ValidateFolderSessionID`.

## Session list

- Left column lists chats via `GET /coddy/sessions`.
- Pagination uses `limit` and `cursor`.
- CRUD
  - Rename via `PATCH /coddy/sessions/{id}` setting `title`.
  - Delete via `DELETE /coddy/sessions/{id}`.
  - Create new chat by generating a new id and switching the URL.

## Chat transport

- Primary transport is `POST /v1/responses`.
- `stream: true` uses SSE.

SSE payloads

- Default SSE lines stream OpenAI like deltas.
- Named SSE events
  - `tool_call`
  - `tool_call_update`
  - `plan`
  - `token_usage`

## Live token usage

- UI must show token counters while the agent is working.
- Counters update when SSE event `token_usage` arrives.
- Update granularity is per completed backend model call, not per generated token.

## Plan and todo list

- Right rail shows the current plan entries.
- Load via `GET /coddy/sessions/{id}/plan`.
- Save via `PUT /coddy/sessions/{id}/plan`.
- Archive via `POST /coddy/sessions/{id}/plan/archive`.

## Long term memory

Memory tree roots

- `global`
- `workspace`

Tree API

- `GET /coddy/sessions/{id}/memory/tree`
  - Without `root` returns the roots list.
  - With `root` and optional `path` lists children.
- Only `.md` and `.txt` files are listed.
- Path traversal must be rejected.

File API

- `GET /coddy/sessions/{id}/memory/file` reads.
- `PUT /coddy/sessions/{id}/memory/file` writes.

## Swagger

- Swagger UI is served under `/docs/`.
- OpenAPI spec is served under `/openapi.yaml` and `/openapi.json`.
- Swagger UI assets must be embedded, no CDN.

## Development workflow

- Edit TypeScript sources under `external/ui/src/`.
- Use `npm --prefix external/ui run dev` to iterate without rebuilding the Go binary.
- Build and sync embed assets with `npm --prefix external/ui run build:go`.
- `make build TAGS=http` runs the UI build step automatically.

## Reference images

Store the provided design reference images under `docs/ui/assets/`.

When describing a specific element, link to the relevant image file.

- Home layout: `assets/ref-home-1.png`, `assets/ref-home-2.png`, `assets/ref-home-3.png`
- Home scroll state: `assets/ref-home-scroll.png`
- Composer state: `assets/ref-home-composer.png`
- Left rail icon states: `assets/ref-rail-states.png`
- Chat history view: `assets/ref-history.png`
- Chat transcript view: `assets/ref-chat.png`
- Flow montage: `assets/ref-flow.png`
