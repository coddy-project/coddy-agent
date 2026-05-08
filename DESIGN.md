# Coddy Agent UI Specification

Purpose: authoritative reference for the embedded SPA built from `external/ui/`. Tokens and layouts live here before CSS tweaks land in production stylesheets.

## Design references

Store the design reference images under `docs/ui/assets/` and link to the specific file when describing a pixel sensitive UI detail.

## Foundations

### Color

| Token | Hex | Usage |
|-------|-----|-------|
| background | `#121212` | main canvas |
| nav rail | `#252525` | icon rail |
| session list | `#1E1E1E` | secondary column |
| accent | `#9333EA` | actions, pills, emphasis |
| text primary | `#FFFFFF` | default copy |
| text muted | `#9CA3AF` | captions, timestamps |
| user bubble | `#2D2D2D` | outgoing chat |

### Typography and spacing

- System stack: **`system-ui`**, `-apple-system`, **`Segoe UI`**, **`sans-serif`**
- Comfortable padding: **`12px`** grid, radius **`12px`** (pill buttons **`999px`**)
- Density tuned for dashboards: two-column desktop layout (**`52px`** rail + **`260px`** sessions + fluid chat)

### Token usage HUD

Muted footer row under composer shows **`input` / `output` / `total`** counts from streamed **`token_usage`** SSE payloads. Numbers update after each backend LLM pass (between tool executions), not per model token emitted over the wire.

## Layout

Left-to-right zones:

1. **Icon rail**: quick **New chat** button.
2. **Session sidebar**: chronological list, pagination (**Load more**), overflow menu placeholders for rename/delete.
3. **Chat canvas**: sticky header (**GitHub** + **API docs**), scrollable transcripts,composer with **Send** (**Enter** submits, **`Shift+Enter`** newline).

The right insights rail is removed for the current milestone.

### Session identifier in URL

`#/s/<sessionId>` survives reload/share as long as the browser hits the **same Coddy http instance** backing the **`sessions`** root hash. SPA keeps **`X-Coddy-Session-ID`** synced with whichever id anchors the fragment.

### Responsive breakpoints

- `<1100px`: hide insights column,maintain core chat+sessions stack.
- `<760px`: collapse rails for mobile-first readability (sessions accessible via wider layouts only for now).

## Components

### Repo links

Outlined text buttons inline in header (`RepoLink`): GitHub (**`https://github.com/coddy-project/coddy-agent`**) and relative **`/docs/`** anchor.

### Tool timeline

Captured via SSE (**`tool_call`**, **`tool_call_update`**). Rendered compact grey cards inside transcript.

Tool cards must include tool name, status, arguments, and result.

- Tool arguments arrive via `tool_call_update` status `in_progress` where `content[0].content.text` is raw JSON args.
- Tool result arrives via `tool_call_update` status `completed` or `failed` where `content[0].content.text` is the display result (possibly truncated by backend).

### Composer pill

Muted **Auto** pill tracks future modality toggles; UI copy stays English everywhere.

### Memory tree

Shows combined **global** (**`memory.dir`** / `$CODDY_HOME/memory`) and **workspace** (`<cwd>/memory`) hierarchies respecting backend filters (`.md` / `.txt` only).

Memory UI is removed for the current milestone.

### Component boundaries

The UI should be implemented as small React components with folder-enforced hierarchy.

- `ui/layout/Shell`
- `ui/nav/NavRail`
- `ui/sessions/SessionsSidebar`
- `ui/chat/ChatScreen`
- `ui/messages/MessageList`
  - `ui/messages/UserMessage`
  - `ui/messages/AssistantMessage`
  - `ui/messages/ToolCallMessage`

### Session overflow menu (`…`)

Opens lightweight rename/delete UX (prompt-first until richer modals arrive).

## States

- Idle composer: bordered textarea.
- Streaming assistant: progressively grows final assistant bubble; token HUD updates concurrently.
- Error streaming: surfaced inside assistant transcript with HTTP status text fallback.

## Non-goals for this milestone

Server-side SSR routes per session, BFF auth, CDN-hosted Swagger, and editing **`agentMemory`** via REST remain out-of-scope (`session.json` slot remains agent-managed).

## Dev workflow

To iterate on UI without rebuilding the Go binary:

Backend:

```bash
make build TAGS=http
./build/coddy http --config config.yaml --home /tmp/coddy-ui-dev-home --sessions-dir /tmp/coddy-ui-dev-sessions -H 127.0.0.1 -P 12345
```

Frontend:

```bash
npm --prefix external/ui install
npm --prefix external/ui run dev -- --host 127.0.0.1 --port 5173
```

