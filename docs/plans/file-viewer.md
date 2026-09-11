# Plan: workspace file viewer panel

Status: design record written 2026-09-08 on branch `claude/rpa-file-viewer-ui-2bf4d3`,
not implemented. Cross-reviewed by Codex (`gpt-5.6-sol`), Cursor Agent (`auto`) and
Coddy (`neuraldeep/qwen3.8-27b`); every finding was re-verified against the code
before being accepted or rejected, and section 2 records the ones that did not hold.

## 1. What

A right-docked panel in the SPA that browses the session workspace and previews what is in
it: source with syntax highlighting, images, markdown (rendered or raw), audio, video and
PDF, with a readable fallback for everything else. The same surface other desktop agents
call "Files" or "show files".

The panel must work identically when the SPA is pointed at a remote `coddy http`, which is
the constraint that shapes almost every decision below.

## 2. Findings from cross-review that did **not** hold

Recorded so they are not re-raised.

- **`Vary: Origin` needs adding.** Already emitted — `external/httpserver/cors.go:18`.
- **Range responses will fight compression middleware.** There is no compression middleware
  anywhere in `external/httpserver`.
- **A tree root request conflicts with the path normalizer, which rejects empty paths.**
  `NormalizeWorkspaceRelativePath("")` returns `("", nil)`. Empty is the root; only empty
  *interior* segments are rejected. No sentinel needed.
- **The panel will desync when the session cwd moves under it.** Nearly closed already:
  `SetSessionWorkspace` is reachable only from `POST /coddy/sessions/{id}/workspace`, which
  answers 409 once the conversation has messages (`workspace_context.go:215`), and no tool
  changes cwd. The only window is a zero-message session, handled by refetching on that
  POST's own response.
- **Use `filepath.EvalSymlinks` for containment.** Right direction, superseded: `go.mod` is
  `go 1.25.0`, so `os.Root` is available and is traversal-safe by construction, which also
  survives the symlink-swap race that `EvalSymlinks` followed by `Open` does not.
- **`http.ServeContent` sets an ETag.** It does not; it only *consumes* one the handler has
  already set. (The conclusion drawn from it — set `Cache-Control` explicitly — still holds.)

## 3. What exists today

| Piece | Where | Notes |
|---|---|---|
| `GET /coddy/workspace/files` | `workspace_files.go` | Fuzzy search for the composer `@` picker. Walks the **whole** tree (cap 50000) on **every** call, then substring-filters. Skips `.git`, `node_modules`, `.coddy`. |
| `GET /coddy/workspace/file` | `workspace_file.go` | Text only, via `session.ReadWorkspaceUTF8`: **512 KiB hard cap**, legacy-encoding detection, line-split, `max_lines` but no `offset`. |
| `GET /coddy/sessions/{id}/assets/{name}/thumbnail` | `coddy_coddy.go:939` | Byte-serving precedent: `http.ServeContent`, fixed `image/png`, `nosniff`, `Cache-Control: private`. |
| Path guard | `internal/session/workspace_path.go` | Lexical only. Rejects `..`, absolute paths, empty segments; verifies containment with `filepath.Rel`. **No** symlink resolution. |
| Right-docked panel | `DESIGN.md` "Background tasks panel", `external/ui/src/ui/tasks/` | `.bgtasks-panel`, fixed right edge, 380px, route `#/s/<id>/tasks`, master-detail in one panel. |
| Markdown renderer | `external/ui/src/ui/markdown/Markdown.tsx` | react-markdown 10 + remark-gfm + rehype-highlight + highlight.js. No `rehype-raw`. No mermaid, no math. |

## 4. The remote constraint

Remote mode is a global `window.fetch` shim (`external/ui/src/ui/env/remoteEnv.ts`): requests
to `/v1/*`, `/coddy/*`, `/openapi*` are rewritten to the selected remote base with
`Authorization: Bearer <token>`. The SPA shell always loads from the local origin. Env and
per-remote tokens live in `localStorage`.

1. **Subresource loads bypass the shim.** `<img src>`, `<video src>`, `<audio src>`,
   `<iframe src>`, `<a download href>` and CSS `url()` are browser fetches, not
   `window.fetch`. A bare `src="/coddy/…"` in remote mode hits the **local** origin with no
   bearer token.
2. **They also lose `X-Coddy-Session-ID`**, which is how the workspace is currently selected.
   `resolveSessionCWD` (`slash_commands.go:77`) falls back to the **server default cwd** when
   the header is absent, so a native media load would silently read the wrong tree. This is
   what forces the route shape in section 5.
3. **This bug already ships.** `UserMessage.tsx:77` renders `<img src={f.previewUrl}>` on the
   bare path built at `coddy_coddy.go:894`. In remote mode attachment thumbnails resolve
   against the local origin and fail silently.
4. **The shim adds only base and bearer.** Every call must still send
   `X-Coddy-Session-ID` itself, as `Composer.tsx` and `tasks/api.ts` already do.
5. **There is no CSP on the SPA at all** (`external/ui/index.html` sets none, no handler adds
   one), so nothing but response typing stands between workspace bytes and the token store.
6. **CORS matters less than it looks.** Native media is a no-CORS request and needs no CORS
   headers to seek; `Content-Length` and `Last-Modified` are already safelisted, and a simple
   single `Range` is a safelisted request header. Expose-Headers only unlocks JS reading
   `Content-Range` / `ETag` / `Accept-Ranges` on the `fetch` path.
7. **The real transport trap is mixed content**: an HTTPS-served SPA cannot load subresources
   from an HTTP remote, and a static `media-src` / `frame-src` is impossible when the remote
   host is chosen at runtime.

## 5. Route shape

All three routes hang off the session segment, matching the existing
`/coddy/sessions/{id}/assets/{name}/thumbnail`:

- `GET /coddy/sessions/{id}/workspace/tree?path_rel=&cursor=&limit=&include_hidden=`
- `GET /coddy/sessions/{id}/workspace/raw?path_rel=&access_token=&download=`
- `GET /coddy/sessions/{id}/workspace/text?path_rel=&offset=&max_lines=`

The session in the **path** is what lets a native `<video src>` carry the workspace identity
it cannot carry in a header, and it makes cache keys, access logs and authorization explicit.
`/coddy/workspace/files` and `/coddy/workspace/file` stay untouched for the composer.

## 6. Freshness: the agent is a concurrent writer

The point that separates an agent's file viewer from an IDE's. A user opens `main.go`, the
agent rewrites it a second later, and a naive panel shows stale bytes with nothing to say so.
Here that is the normal flow, not an edge case, so freshness is tier 1:

- the preview header carries `mod_time` and a **Reload** control;
- an open file revalidates against its `ETag` when the panel regains focus, and after any
  transcript tool call whose arguments name that path (the SPA already parses tool-call
  arguments for the tool timeline), surfacing *this file changed* instead of silence;
- the tree refreshes on the same trigger.

## 7. Backend

### 7.1 Rooted, race-safe access

Every resolution goes through `os.Root` (`os.OpenRoot(sessionCWD)` then `root.Open(rel)`),
which cannot escape and is not vulnerable to symlink swaps.
`NormalizeWorkspaceRelativePath` stays as the input filter; `os.Root` is the real boundary.
Then `Stat` and **reject anything that is not a regular file** before MIME or `ServeContent`:
FIFOs block the handler indefinitely, device files disclose or destroy, sockets and
directories behave inconsistently.

This closes the pre-existing lexical-containment gap rather than inheriting it, which matters
because `raw` is a **new capability**, not a friendlier `workspace/file`: it drops the
512 KiB text-only boundary and adds binary streaming.

### 7.2 `raw`

- Content type from an **explicit extension allowlist map**, not `mime.TypeByExtension`
  (platform-dependent, yields aliases like `audio/x-wav`), cross-checked against
  `http.DetectContentType` on the first 512 bytes. **Extension and sniff disagreeing forces
  `application/octet-stream` + attachment**, so `evil.html` renamed `photo.png` never goes
  inline. Seek back to offset 0 after sniffing, or the response starts 512 bytes in.
- Inline allowlist: `image/png|jpeg|gif|webp|avif`, `video/mp4|webm`,
  `audio/mpeg|ogg|wav|flac`, `application/pdf`, `text/plain`. Everything else, explicitly
  including `text/html` and `image/svg+xml`, is `application/octet-stream` + attachment.
  With no CSP on the SPA and remote tokens in `localStorage`, serving workspace-controlled
  HTML or SVG from that origin is same-origin token theft.
- Explicit `ETag` (size, mtime, normalized path), `Cache-Control: private, no-cache`,
  `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`.
- Per-class CSP, not one blanket policy: `default-src 'none'` for downloads and images; PDF
  gets its own, verified in Chromium, Firefox and Safari, because viewers misbehave under
  both an iframe `sandbox` and a response-level CSP sandbox.
- `Content-Disposition` via a proper formatter (ASCII `filename` plus RFC 6266 `filename*`),
  never interpolated from a workspace name.
- `download=1` is a distinct mode forcing attachment, and the mode is bound into the
  capability so a view capability cannot be replayed as a download.

### 7.3 Capability token

When auth is off (`authPolicyNow().enabled == false`, the default) there is nothing to carry
and the plain URL is used. When auth is on:
`POST /coddy/sessions/{id}/workspace/media-token` → `{token, expires_at}`.

Payload binds `{v, session_id, normalized path_rel, disposition, exp}`, HMAC-SHA256,
constant-time verify, hard max TTL. Path-bound rather than session-wide, so a leaked URL
yields one file.

`authGate` must be **explicitly extended** for the raw route — `isSSETokenPattern` covers only
the two SSE routes today, so without a middleware change the request is rejected before the
handler sees it. Not a blanket exemption. **Check order is contract**: if `access_token` is
present it is validated as a capability, and only otherwise does the bearer check run;
bearer-first returns 401 to every media element, which is the exact case the capability
exists for. The handler resolves the workspace from the **verified session claim**.

TTL 1 hour, not 5 minutes: browsers issue fresh Range requests throughout playback, and a
short TTL breaks a long video mid-play. The HMAC key is random per process but **overridable
from config**, so a load-balanced or restarted deployment does not emit intermittent 403s that
read as flakiness. Leak mitigations: `Referrer-Policy: no-referrer`, redact `access_token`
from access logs, never redirect a token-bearing request, keep these URLs out of router state
and telemetry.

Rejected alternative, recorded: a same-origin proxy on the local server attaching the remote
bearer server-side would let every subresource keep a relative URL and would kill the
thumbnail bug without query tokens. Rejected because the local process would then have to hold
the remote token, which today lives only in the browser.

### 7.4 `text`

Adding `offset` to the existing route does **not** make long files reachable, because
`ReadWorkspaceUTF8` refuses anything over 512 KiB outright — a 2 MB source file cannot be
previewed at all today. So `text` is a **bounded streaming line-window reader**: open through
`os.Root`, detect encoding on a leading sample with the existing `textenc`, scan to `offset`,
emit `max_lines`, return `{next_offset, has_more, total_lines_known}`. Contract fixed up
front: line numbers **1-based**, `offset` applied **before** `max_lines`, a file changed
between requests reported by ETag mismatch rather than silently re-paged.

### 7.5 `tree`

One directory level. `Lstat`, do **not** follow, so `symlink` is reported honestly.
**Cursor pagination by name**, not page numbers: a mutating directory makes page numbers
duplicate and drop entries. `limit` capped server-side around 1000, with `has_more`.

`mime_type` is **omitted** from rows — determining it accurately means opening every file in
the directory, 800 opens per navigation step in a directory of 800. The client derives a
renderer hint from the extension; the authoritative type comes from `raw`. `mod_time` is
RFC 3339 UTC; `size_bytes` is `0` for directories.

Hidden entries, stated honestly: omitting `.git` from a listing is **not access control**,
because `raw` and `text` still serve `.git/config` by explicit path. v1 hides dotfiles and the
three ignored directories from navigation by default, documents that this is navigation
convenience only, and leaves "should any path be *forbidden*" as a separate explicit decision
rather than an accident of the listing filter.

### 7.6 CORS

Small: `Access-Control-Expose-Headers: Content-Range, Accept-Ranges, ETag` for JS that reads
them, and `Range` / `If-None-Match` in `Allow-Headers` for ranged `fetch()`. Nothing else;
see section 4 item 6.

## 8. Frontend (`external/ui/src/ui/files/`)

### 8.1 One authenticated-bytes primitive, wrapped twice

A session asset and a workspace file have different routes and lifetimes, so one helper cannot
serve both. Extract `authedObjectUrl(url, init)` — fetch through the shim, size-cap the
response **before** `.blob()`, abortable, construct a **typed** `Blob` (an octet-stream
response otherwise yields an untyped blob that will not render) — then wrap it separately for
workspace files and session assets. Object URLs are **reference counted**, not LRU-evicted:
evicting a URL that is still mounted breaks a visible image.

URL building goes through one builder using `location.origin` for local and the configured
base for remote (`new URL(path, "")` throws, so "empty base for local" is not a valid rule),
and it defines whether a remote base may carry a path prefix.

The session-asset wrapper fixes the shipped remote bug at `UserMessage.tsx:77`.

### 8.2 Renderers

- `TextPreview` — monospace, gutter reused from `markdownLineGutter.ts`, highlight.js
  (already bundled), with a **cap on highlighting** for very large files and pathological
  single-line minified input; wrap toggle; jump-to-line for deep links;
- `ImagePreview` — checkerboard, fit / 1:1, dimensions and size in the header, hard size cap
  with a metadata-and-download fallback above it;
- `MarkdownPreview` — reuses `Markdown.tsx` with a **custom async `img` component**;
  `urlTransform` is synchronous (`Markdown.tsx:199`) and cannot await a fetch. It resolves
  against the markdown file's directory, normalizes `../` **client-side** into a canonical
  workspace-relative path (the server guard rejects `..`), tracks loading and error, and
  revokes on unmount. `defaultUrlTransform` stays as the protocol gate. **External images
  blocked by default** behind click-to-load so a workspace `.md` cannot beacon; links get
  `rel="noopener noreferrer"`. A regression test asserts `rehype-raw` is never added;
- `MediaPreview` — native `<video>` / `<audio>`, `preload="metadata"`, capability URL;
- `PdfPreview` — capability URL in an `<iframe>`; the sandbox policy is **spiked across
  Chromium, Firefox and Safari before PDF is called ready**, because an empty `sandbox` breaks
  Chrome's viewer and loosening it weakens the boundary;
- `BinaryFallback` — type, size, Download through the `download=1` capability mode.
  `<a download>` alone is ignored by browsers for cross-origin URLs.

### 8.3 Shell

One **shared right-dock controller** owning width, responsive behaviour, focus restoration,
Escape handling and the active tab, with **Tasks and Files as tabs** rather than two mutually
unaware panels fighting over the same edge. Routes stay authoritative. Small refactor of Tasks
now; it removes an impossible "both open" class combination and fixes the worst workflow,
where a task log points at a file you cannot open without closing the task log.

Fixed responsive width for v1 (520px desktop, full-bleed narrow); 380px is too narrow for
code. Drag-resize with per-environment persistence is polish, and that budget goes to rooted
opening, session correctness and blob caps instead.

Entry points: a Files chip in the composer workspace-context row beside the folder / branch /
worktree chips; **a click on a path inside a tool-call card**, deep-linked to the line, which
is the highest-value one and what makes it feel like "show files"; and `@mention` chips.

Search in v1 filters **only the directories already loaded**. Reusing `/coddy/workspace/files`
(a 50000-entry walk per call) as a remote search box would ship a knowingly unusable path;
a real indexed, cancellable search with a minimum query length is a separate later piece.

## 9. Tiers

**Tier 1** — the three session-addressed routes; `os.Root`; regular files only; the capability
token **including** `download` (a viewer you cannot get a file out of is not done, and PDF and
download both need it); freshness (section 6); the bytes primitive and wrappers; the
Tasks/Files tabbed dock; text/code, image, markdown, binary fallback; the `UserMessage` remote
fix; the CORS additions. **No new npm dependency.**

**Tier 2** — audio, video, PDF, after the sandbox spike.

**Tier 3, needs a build change** — mermaid and math. The SPA is one file today:
`vite.config.ts` sets `inlineDynamicImports: true` and `chunkFileNames: "app.js"`,
`embed.go` lists assets by name, and `scripts-sync-to-go.mjs` copies exactly two files.
Take the cheap route first: ship `mermaid.js` / `temml.js` as **additional named embedded
assets** loaded by dynamic `import()` from a stable path, which works with today's explicit
`//go:embed` filenames. Only if that proves awkward, do the full chunk-splitting redesign.
Choose between temml (MathML, no font payload) and KaTeX from **actual production build output
and compressed transfer size**, not from npm figures that compare minified, unpacked and WASM
sizes as if equivalent.

**Out of scope** — LSP semantic highlighting. Shiki is a TextMate regex tokenizer, not a
parse, and supplies no outline; tree-sitter can support outlines but only after per-language
grammars plus highlight, injection and symbol queries are shipped, none of which is free.
A language server per language on the box that owns the workspace is a subsystem on the order
of the MCP integration, bought for type-versus-variable colouring in a read-only viewer. If
semantic *navigation* is what is wanted, the honest cheap version is a tree-sitter symbol
outline plus a symbol search over the existing grep tool. Also out: editing from the panel,
diff against HEAD, `.ipynb`, office documents, archives, a TUI file viewer, uploads.

**Recorded and rejected for v1** — serving workspace bytes from a **separate origin**. It is
the materially stronger XSS boundary, since a future MIME or CSP regression could not become
same-origin token theft. Rejected for the cost of a second listener plus remote deployment and
CORS complexity; mitigated instead by forced attachment for active formats and by regression
tests that navigate directly to malicious HTML and SVG.

## 10. Tests

Repo rules unchanged: Gherkin happy paths in `features/` run by godog, edge cases as ordinary
unit tests, `openapi.go` updated with `server.go`, `DESIGN.md` and `docs/ui.md` sections, a
screenshot per changed surface, `make build TAGS="http ui"` to regenerate embedded assets.

Beyond the happy paths, the cases most likely to become incidents: a symlink escaping the root
and a symlink swap where the platform allows it; FIFOs and device files; a `raw` request with
a capability and **no** `X-Coddy-Session-ID`; cache separation between two sessions;
capability path / session / disposition mismatch; an expired capability on a later Range
request; malicious and non-UTF-8 filenames; direct access to hidden paths; oversized blobs;
extension-versus-sniff mismatch; the 512-byte rewind; `HEAD` and `416`; and a preflight
assertion on `Allow-Headers` and `Expose-Headers`. Plus the remote end-to-end that points the
SPA at a second `coddy http` and asserts an image actually renders.

## 11. Sequencing

1. Backend only: the three routes, `os.Root`, capability token, CORS. Fully testable without
   the SPA.
2. Shared right-dock controller, Tasks converted to a tab.
3. Panel v1: tree, text/code, image, markdown, binary fallback, bytes primitive, freshness,
   the `UserMessage` remote fix.
4. Media: audio, video, PDF.
5. Build change, then mermaid and math.
6. Optional: tree-sitter highlighting and symbol outline.

## 12. Open for the operator to decide

- Hidden and ignored paths: navigation-only filtering (what this plan assumes), or a real
  server-side denylist that `raw` and `text` enforce too?
- PDF: accept the cross-browser sandbox spike, or ship PDF as download-only?
- Separate untrusted-content origin: accept the deployment cost for the stronger boundary, or
  stay with forced attachment plus regression tests?
