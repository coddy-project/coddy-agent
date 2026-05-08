# Coddy embedded UI specification

This page captures the original UI requirements and the intended end state. It is a functional spec and a design contract.

## Constraints

- UI ships as static assets embedded into the `coddy` binary (build tag `http`).
- Runtime has no auth and no API key checks for the UI.
- UI must work over the same origin as `coddy http`.
- UI copy is English.

## Layout

Desktop layout

- Left rail with a new chat action
- Sessions list is a drawer overlay with pagination and per session actions
- Main chat area with streamed assistant output
- Right rail is out of scope for the current milestone

Wide screens

- On desktop and wide screens, navigation is consistent and does not have a width toggle.

Mobile layout

- On mobile the left rail becomes a top bar to preserve horizontal space.
- On mobile the brand stays on a single line.

Header links

- GitHub link to `https://github.com/coddy-project/coddy-agent`
- API docs link to `/docs/`
- Links live in the nav rail for this milestone.

## Sessions

- Session id is generated client side only after the first message is sent from a new chat.
- Session id is persisted in the URL fragment.
  - Recommended format `#/s/<sessionId>`
- Session id is sent in the `X-Coddy-Session-ID` header for chat transport.
- Session id validation matches `internal/session/ValidateFolderSessionID`.

Session title

- UI shows the session title in the chat header.
- When the title is missing, UI shows `New chat`.
- Title is editable inline. On blur the UI saves via `PATCH /coddy/sessions/{id}`.

## Session list

- Left column lists chats via `GET /coddy/sessions`.
- Pagination uses `limit` and `cursor`.
- CRUD
  - Rename via `PATCH /coddy/sessions/{id}` setting `title`.
  - Delete via `DELETE /coddy/sessions/{id}`.
  - Create new chat starts on the home screen. Session id is created only on first send.

Session rename UX

- Title rename is done only in the chat header.
- On blur the UI saves via `PATCH /coddy/sessions/{id}`.

Session delete UX

- Each row has a trash icon button.
- Clicking delete shows one confirm dialog and then calls `DELETE /coddy/sessions/{id}`.

## Chat transport

- Primary transport is `POST /v1/responses`.
- `stream: true` uses SSE.

Mode selection

- UI lets the user select a mode from `GET /v1/models` (at minimum `agent` and `plan`).
- Selected mode is sent as `model` field in `POST /v1/responses`.

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

## Markdown rendering

- User and assistant messages may contain Markdown.
- UI renders Markdown with fenced code blocks and syntax highlighting.
- Each code block has a copy button that copies only that block content.

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

## UI test scenarios

These scenarios are intended to be automated via Playwright against the Vite dev server.

- Desktop navigation has no width toggle
  - Given viewport width is at least 1024px
  - When the app loads
  - Then `data-testid="nav-menu"` is visible
  - And `data-testid="nav-toggle-width"` is not present

- Sessions are drawer only
  - Given any desktop viewport
  - When the app loads
  - Then `data-testid="sessions"` is not visible
  - When user clicks `data-testid="nav-menu"`
  - Then `data-testid="sessions"` becomes visible
  - When user clicks `data-testid="sessions-close"`
  - Then the sessions drawer is hidden

- Mobile uses top bar and single line brand
  - Given viewport width is at most 760px
  - When the app loads
  - Then the nav width toggle is not present
  - And the nav rail height is 78px
  - And sessions can still be opened from the menu button
