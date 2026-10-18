# Plan: Canvas - live UI frames the agent and the person build together

Status: cross-reviewed 2026-10-18 (Devin `swe-2`, Codex `gpt-6.1-sol`, Cursor `auto`, Coddy
`qwen3.8-27b`), decisions in section 13; branch `feat/canvas`.

## 1. What

A **canvas** beside every chat of the web UI: a board of live **frames**, each one a page of the
workspace (HTML, CSS, JS) rendered and running, at the size of a phone, a tablet or a desktop. The
agent builds a page as ordinary files and puts it on the canvas; the person sees it run, clicks
through it, points at an element and says what to change; the agent edits the files and the frame
reloads by itself. Several frames sit side by side, so one page is checked at three widths at
once, or three variants of a page (three subagents, one each) are compared on one board.

The prompt for this feature is LM Studio's Canvas in its Bionic harness (section 2). Coddy already
holds most of the parts - a workspace file viewer with a sandboxed HTML preview, the
`preview_server` tool, signed capabilities that a swarm relay carries to a node, the edits window
over git, the message queue, the shared events stream - so the canvas is mostly a new view that
joins them, plus one served route and two tools.

## 2. What exists elsewhere

Researched 2026-10-18 from the vendors' pages, changelogs, repositories and a frame-by-frame look at
LM Studio's demo video.

**LM Studio Bionic** (desktop agent for open and cloud models, macOS, Windows, Linux). Canvas came
in 1.1.6 (2026-09-23) as an "interactive board that both you and Bionic can edit", with export to
PNG, SVG and `.excalidraw` in 1.1.7 and a draggable panel in 1.1.8. It is an **Excalidraw** board:
the canvas opens as a tab named `pomodoro.excalidraw`, the agent draws a wireframe of the screen
("Here's the mockup. Move things around or remove anything you don't need"), the person deletes and
moves elements with the ordinary Excalidraw tools, writes "build it", and the agent reads the board
back ("You've moved progress below the timer and cut the extra text. I'll build it as shown") and
builds the app, which opens in a built-in browser tab on `127.0.0.1`. Inferred: the board is an
Excalidraw scene file of the project, read and written as JSON. Not seen: live HTML on the board,
pointing at an element of the running app, version history beyond undo.

**Others.** Two families:

- *Chat-side documents* - Claude Artifacts, ChatGPT Canvas (deprecated in 2026 for writing blocks),
  Gemini Canvas, Mistral Le Canvas, Open WebUI: a document beside the chat, rendered in a sandboxed
  frame on a separate origin or in `srcdoc`, edited by whole rewrites or targeted replacements, with
  version arrows.
- *Live previews of real files with a pointing layer* - v0 (Design Mode), Bolt.new (Visual Edits),
  Lovable (select, draw, pinned comments), Figma Make, Cursor's Design Mode, Devin Desktop's "Send
  element", Claude Code's preview pane, Codex's browser annotate, Hermes Agent's browser comment mode:
  the files of the workspace are the truth, the preview runs them, the person clicks an element and the
  tool packs its selector, HTML, computed styles and the note into one message for the model.
- *Boards of several frames* - Google Stitch (an infinite canvas of HTML screens), Onlook (a canvas of
  device-sized frames of a running app), tldraw make-real (drawings beside the HTML they became),
  Excalidraw's wireframe-to-code.

What this plan takes: workspace files as the truth (every coding tool above), a board of
device-sized frames (Stitch, Onlook), comment mode packaged as selector, HTML, styles and note
(Hermes `pack.ts`, Cursor, Lovable), console errors offered back (Devin Desktop, Gemini's "Show
console"), the frame's own response CSP `sandbox` (Claude), and Obsidian's JSON Canvas files as a
board the canvas opens and writes back (6.10), in place of Bionic's Excalidraw scene.

## 3. Principles

1. **Files are the source of truth.** A frame shows a file of the workspace; the agent writes it
   with the tools it already has (`write`, `edit`, `run_command`). Nothing about a page lives in a
   chat-side object: git sees every change, the edits window shows the diff, the console and ACP
   clients work on the same files, rewind and worktrees apply as they do to any file.
2. **The board is small state in the session bundle.** Which files are on the canvas, at what size,
   where on the board: `canvas.json` in the session's bundle, read and written through one store.
   A subagent's frames go to the board of the session at the top of its spawn tree, which is the one
   a person looks at.
3. **A frame runs, and runs nowhere near Coddy.** Scripts work (a prototype has to be clickable), in
   an opaque origin: the frame cannot read the web UI's storage, its tokens or its DOM, and nothing it
   sends reaches Coddy's API. The page's own response header enforces the sandbox, so a frame's
   address opened in a tab of its own is just as isolated.
4. **One address that works everywhere.** Locally, on a remote `coddy serve`, behind a sign-in form
   and through any number of swarm relays, with the same signed address; no new port, no new origin
   to configure.
5. **The person stays in charge of what is sent.** Picking an element produces text the person sees
   and edits before it goes out; nothing a frame says reaches the model unless the person sends it or
   the agent asks for it with `canvas_inspect`, whose result marks the page's lines as untrusted
   output of the page.
6. **Nothing else changes, with one deliberate exception.** No existing route, tool, config key or
   view changes behaviour, except that a request from an opaque origin no longer reaches the API
   (6.7). The canvas is off the screen until a frame exists, and `tools.canvas.enable: false` removes
   it.
7. **A page reads its root and may send it out.** A frame's script can read every file under the
   frame's root and can navigate itself anywhere, taking what it read along: no CSP directive governs a
   document's own navigation. The root is therefore kept tight (the page's folder by default), the
   address is revoked when the page leaves the canvas (6.5), and the documentation says it plainly.

## 4. User experience

### 4.1 Where the canvas is

The canvas is a **window over the chat**, framed like the Files and Edits windows (the same glass
sheet, head, Expand and close, the same Escape order), opened by a **Canvas** button among the view
buttons of the chat header (Files, Canvas, Tasks). The button appears once the chat's board has a
frame; the address is `#/s/<id>/canvas` (`#/s/<id>/canvas?frame=<frame id>` focuses one).

A window over the chat rather than a split beside it, because the chat column carries most of the
layout contracts of `DESIGN.md` (the transcript window, containment, the composer's height and
expand, the scroll-to-bottom circle), and a column that changes width under them would reopen every
one. The canvas brings what it needs from the chat instead (4.4): a prompt bar, the turn's status
and Stop.

The window opens by itself when the agent puts a frame on the board while the chat is on screen
(desktop and tablet), and the Canvas button marks a frame added while the window was closed. On a
phone it does not open by itself: the button is marked and the tool card in the transcript offers
**Open on canvas**.

### 4.2 The board

- Frames stand left to right in the order they were added, at their own size (Desktop 1280x800,
  Tablet 820x1180, Phone 390x844, or the width and height the agent or the person set), scaled
  together by the board's zoom. A new frame takes the next free place to the right.
- Pan by dragging the empty board (or the wheel), zoom with Ctrl+wheel, a pinch, the + / - buttons;
  **Fit** shows every frame. The camera math is the swarm map's (`swarm/graphViewport.ts`).
- Each frame has a title bar: its title (the file name unless one was given), the size menu
  (Desktop / Tablet / Phone / Custom), Reload, Remove from canvas. Dragging the
  title bar moves the frame.
- A frame whose files changed reloads by itself (4.5). A frame that reported errors shows a red
  count on its title bar; a click lists them.
- Two modes, a toggle in the window's head (no key: a key pressed in a frame belongs to the page):
  **Interact** (clicks go to the
  page, the default) and **Comment** (hovering outlines elements, a click picks one).

### 4.3 Pointing at an element

In Comment mode a click on an element of a frame picks it: the element is outlined, a popover opens
beside it with a field ("What should change here?"), and the pick becomes a chip in the prompt bar.
Several picks can be collected before sending. A pick carries the frame's file and size, a CSS
selector, the tag, a short text excerpt and the element's outer HTML cut to 2,000 characters. Sent,
it becomes plain Markdown in the prompt the person sees in the transcript:

````markdown
On the canvas, `site/index.html` (Desktop 1280x800), element `main > section.hero > h1`:

```html
<h1 class="hero-title">Welcome to Acme</h1>
```

Make the headline larger and use the accent colour.
````

Plain text keeps the model's input visible and editable, needs no new message type, survives every
surface (a console showing the session reads the same words) and is cheap: a path and a snippet, not
the file. The agent reads the file itself when it needs more.

### 4.4 The prompt bar

At the foot of the window: a one-line field that grows with its text, the chips of the picks, and
Send / Stop. Send goes through the same path as the composer's Send - the chat's mode, model and
reasoning level, the message queue while a turn runs - so the canvas adds no second way of starting
a turn. The bar shows the turn's live status line while the agent works ("Editing site/index.html")
and a **Chat** control that closes the window onto the transcript. Text typed in the bar is its own
draft, kept for the life of the page, not the composer's.

### 4.5 Live reload

Every frame has a **stamp**: a digest of the names, sizes and modification times of the files under
its root (bounded walk, dot-names, `node_modules` and `vendor` skipped). The board's read returns it,
and the window reads the board again on `canvas_changed`, on a finished tool call, on `turn_ended`,
on `session_changes` and when the page comes back into view - the moments the Files window already
re-reads - and every four seconds while the window is open and the page visible, since a background
build can rewrite a file after the last tool call. A frame whose stamp moved reloads; the others are left alone, so a page someone is clicking
through is not reset by an edit elsewhere.

### 4.6 Elsewhere in the web UI

- **Tool card**: `canvas_show` in the transcript shows the frame's title, file and size with **Open
  on canvas**.
- **Files window**: an HTML file's **More** menu gets **Open on canvas**, which adds a frame for it
  (Desktop).
- Phone (`max-width: 599px`): the window is full-screen as the Files window is there; frames start
  fitted; picking is by tap; the popover becomes a bottom sheet.

### 4.7 Other surfaces

The console, ACP editors and the messenger bots have no canvas. The tools still work there: the board
is kept with the session, and the web UI shows it the next time the session is opened there (a
Telegram Mini App included). The tool result tells the model so, and points at `preview_server` for a
person who works in a terminal.

## 5. The agent's side

### 5.1 Tools

Two tools, offered in `agent` mode, no permission prompt (like `preview_server`: they show the
person files of the workspace and change nothing on disk).

`canvas_show` - put a page on the canvas, or update a frame:

| Argument | Meaning |
|---|---|
| `path` | HTML file inside the working directory (required). |
| `title` | Title on the frame (default: the file name). |
| `device` | `desktop` (default), `tablet`, `phone`. |
| `width`, `height` | A custom size in CSS pixels instead of `device` (320-3840 x 320-4320). |
| `root` | Folder the page may load files from; default the file's own folder. Must contain `path` and stay inside the working directory. |
| `frame` | Id of a frame to update in place instead of adding one. |

The same file at the same size is never on the board twice: showing it again focuses the frame
(and reloads it). The result names the frame id, the board's frames in one line each, and what the
person sees.

`canvas_inspect` - what the browser saw:

| Argument | Meaning |
|---|---|
| `frame` | One frame; default all. |
| `wait_seconds` | Wait up to this long (max 15) for a report on the frame's current files. |

For each frame: the file, the size, and the newest report from a browser that has it open - loaded
or failed, the page's title, and the last 20 console errors and warnings and uncaught exceptions with
their source and line - or that no browser has it open now. This is how the agent learns that its
script threw without asking the person to read a console.

### 5.2 Guidance

The tool descriptions say when to use the canvas (a page, a screen, a component, a design, a
prototype the person wants to see and try) and how: a self-contained page first (one HTML file,
inline CSS and JS or the folder's own files, CDN libraries from the allowed list), several sizes for
a responsive check, `canvas_inspect` after an edit that touches scripts. The web UI's surface prompt
(`webui_prompt.go`) gets one sentence saying that the web UI has a canvas, only while
`tools.canvas.enable` is on; the turn's prefix changes only when the setting does.

## 6. Architecture

### 6.1 Layers

| Piece | Where | Build |
|---|---|---|
| Board model, store, stamp, frame root resolution | `internal/canvas` (new) | untagged |
| Static serving of a frame's files: types, dot-names, CSP, bridge injection | `internal/canvas/serve.go`, sharing the type table and dot-name filter with `internal/tools/preview` (moved to an exported helper) | untagged |
| Bridge script | `internal/canvas/bridge.js`, `go:embed` | untagged |
| Browser reports (console, load) | `internal/canvas/reports.go`, in memory, bounded | untagged |
| Tools `canvas_show`, `canvas_inspect` | `internal/tools/canvas` | untagged |
| Routes, capability signing, events | `external/httpserver/canvas_http.go` | `http` |
| Relay exception | `external/swarm/server.go`, `mount.go` | `swarm` |
| The window | `external/ui/src/ui/canvas/` | `ui` |

Nothing untagged imports a tagged package; the HTTP server and the relay import `internal/canvas`.

Where it plugs into the SPA, following the Files and Edits windows:

- `chat/ChatHeader.tsx`: `ViewId` gains `canvas`, between Files and Tasks, shown once the board has a
  frame;
- `scheduler/hashRoute.ts`: `#/s/<id>/canvas[?frame=]`, a parser branch and `setSessionCanvasHash`;
- `App.tsx`: `canvasOpen` beside `filesOpen` / `changesViewerOpen`, `openCanvasWindow` /
  `closeCanvasWindow` that put the other two windows away (one Escape never closes two layers),
  rendered next to `<FilesView>` / `<EditsView>`;
- `chat/serverEvents.ts`: `canvas_changed` in `ServerEvent`, `parseServerEvent`,
  `dispatchServerEvent`;
- `messages/StructuredToolCard.tsx`: `canvas_show` in `structuredNames` with a card, its parser in
  `chat/structuredToolDisplay.ts`, `tool.name.canvas_show` / `tool.name.canvas_inspect` labels;
- `files/FilesView.tsx`: **Open on canvas** in **More** for an HTML file;
- the prompt bar sends through the function the composer's Send calls in `App.tsx`, so the queue,
  the mode, the model and the reasoning level are the chat's.

### 6.2 The board

```json
{
  "version": 1,
  "revision": 7,
  "frames": [
    {
      "id": "fr_3k9q2c",
      "path": "site/index.html",
      "root": "site",
      "title": "Landing",
      "width": 1280,
      "height": 800,
      "x": 0,
      "y": 0,
      "added_by": "agent",
      "added_at": "2026-10-18T12:00:00Z"
    }
  ]
}
```

- `<session bundle>/canvas.json`, written through a temp file and rename under a per-session lock,
  `revision` bumped on every change. A missing file is an empty board; a file that does not parse is
  an error and is never overwritten.
- A frame records the absolute `workspace` it was shown from (the showing session's cwd) and its
  `path` and `root` relative to it, resolved once with links followed; a subagent in another folder or
  worktree shows its own files, and a later cwd move of the session does not re-point a frame.
- A frame has a `gen`; its address is signed for that generation, so `Rotate` revokes every address
  handed out before.
- The same page is one frame per (workspace, path, width, height); showing it again focuses it.
- At most 24 frames; a 25th is refused with a message that names `Remove from canvas`.
- A subagent's `canvas_show` writes to the board of the top session of its spawn tree: the bundle
  path is walked up past every `subagents/` level, so a grandchild's frame lands on the root's board,
  and the frame's `added_by` names the subagent.
- One process-wide change hook (`canvas.OnChange`) is what the HTTP server turns into the event.

### 6.3 Serving a frame

`GET|HEAD /coddy/canvas/{capability}/{path...}` serves the files of one frame's root.

- **Capability in the path**, not the query: the page's relative addresses (`./app.js`,
  `styles/main.css`, `fetch("data.json")`, `import "./mod.js"`) resolve against the frame's own
  address and keep it, which a query token would not survive. The token is
  `base64url(claim).base64url(hmac)`, the claim `{v:1, kind:"canvas", session, frame, root,
  workspace, gen, exp}`, the key derived like the workspace media key but domain-separated
  (`coddy-canvas-frame\x00`), so a media capability is never a canvas one and the other way round.
- **Valid while the frame is on the board at that generation**: the handler checks the claim against
  the board, so removing a frame, changing its root or rotating it revokes its address; `exp` is 12
  hours. The window keeps a frame's address until its stamp moves or the address is within 30 minutes
  of its end, then reloads the frame with a fresh one. The address appears in access logs and proxy
  logs like the media capability's; there is no Open in a new tab, so it stays out of the browser's
  history.
- **The files**: through an `os.Root` over the frame root; `GET` and `HEAD` only; dot-named elements
  404; **no symbolic links at all**: every element of the path is checked with `Lstat` through the root
  and the opened file must be the one inspected (`os.SameFile`), so a link swapped in between the check
  and the open is refused too; a folder answers its `index.html` (a redirect adds the trailing slash)
  or 404, never a listing; the type table of `preview_server` (shared through `internal/webfs`);
  `Cache-Control: no-store`.
- **Every response** carries `Content-Security-Policy: sandbox allow-scripts allow-forms allow-modals;
  default-src 'self' data: blob: 'unsafe-inline' 'unsafe-eval' <allowed sources>; connect-src 'self'
  <allowed sources>; form-action 'none'; base-uri 'self'`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer` (the capability is in the path, and a page loading a CDN script must
  not hand it on in a `Referer`), and `Access-Control-Allow-Origin: *` without credentials - a page in
  an opaque origin loads its own ES modules and `fetch`es its own files as cross-origin requests.
- **HTML responses** get the bridge: `<meta name="referrer" content="no-referrer">` and
  `<script src="../.coddy/bridge.js"></script>` (as many `../` as the page is deep in its root, so the
  address resolves inside the frame's own address on a node and behind any number of relay mounts)
  inserted after the `<head>` start tag, else the `<html>` one, else the doctype, else first - never
  before the doctype (quirks mode). The handler answers `.coddy/bridge.js` before the dot-name rule, so
  the name cannot collide with a file of the page. A page over 10 MB is refused by `canvas_show`, and
  one that grew past it is answered with a short page that says so, with the bridge, so the frame does
  not read as having left the canvas (final review).
- `sandbox ... allow-forms` with `form-action 'none'`: without `allow-forms` a sandboxed form never
  fires its `submit` event, so a prototype's script-handled form would not work; `form-action 'none'`
  keeps a submission from leaving. A form that posts somewhere does nothing, which the documentation
  says. `'unsafe-inline'` and `'unsafe-eval'` stay: the isolation is the opaque origin, the page is the
  workspace's own code, and prototypes rely on inline scripts and frameworks that compile templates.
- A page in an opaque origin has no `localStorage`, `sessionStorage` or cookies; the bridge puts an
  in-memory `Storage` in place of the first two, so a prototype that keeps state there works for the
  life of the frame. A classic `<script src>` is a cross-origin script there, so its uncaught errors
  read `Script error.`; the tool guidance asks for `type="module"` scripts, whose errors arrive whole.
- `'self'` in the policy of an opaque-origin document has to match the frame's own address in every
  engine the web UI supports; the browser check's module script (8.3) is the gate, in Chromium and
  WebKit.

### 6.4 The frame in the web UI

`<iframe sandbox="allow-scripts allow-forms allow-modals" referrerpolicy="no-referrer"
allow="" src="<base><frame address>">`. No `allow-same-origin`, no `allow-top-navigation`, no
`allow-popups`. The base is the environment's API base (local origin, a remote server, a relay
mount), the way the Files window builds a media element's signed address.

### 6.5 The bridge

A small script in the frame, talking to the window over `postMessage`:

- frame -> window: `ready` (empty, posted to `*`), then, after `hello`, `location` (the page's
  address), `loaded` (title), `console` (level, message cut to 1,000 characters, source, line; at most
  50 a second), `pick` (selector, tag, id, classes, text excerpt, outer HTML cut to 2,000 characters,
  the element's box, a few computed styles), `escape` (Escape pressed inside the page);
- window -> frame: `hello` (posted to `*`, the only target an opaque origin accepts; it carries the
  window's load generation, which the bridge echoes in every later message; the bridge learns the
  window's origin from the event and answers only that origin and `window.parent`), `mode` (interact /
  comment), `highlight` (a selector to outline after a reload).
- **Generations**: the iframe's `contentWindow` is the same object across navigations, so the source
  check alone cannot tell an old document from its replacement. Each load of the iframe gets the next
  load generation; messages carrying another one are dropped, and a report is posted with the stamp
  the frame was loaded for.
- **Leaving the canvas**: an iframe load that brings no `ready` within three seconds, or a `location`
  outside the frame's address, means the page navigated away. The window rotates the frame (revoking
  the address the page could have taken along), shows *This page left the canvas* over the frame and
  offers **Back to the page**; it never reloads by itself, so a page that blocks the bridge cannot
  loop.

The window accepts messages only whose `source` is that frame's `contentWindow` and whose
generation is current, treats every field as untrusted text (rendered as text, cut to its limit,
never as HTML) and forwards `console` and the load result to
`POST /coddy/sessions/{id}/canvas/frames/{frame}/report` (`{stamp, loaded, title, console[],
closed}`), coalesced to one request per frame every 500 ms whatever the page sends. Putting a frame
away (the window closing, the frame removed) posts `closed`, best effort; `canvas_inspect` says how
old the report is either way. A page that removes or fakes the bridge only spoils its own picks and
reports.

### 6.6 HTTP routes

| Route | Use |
|---|---|
| `GET /coddy/sessions/{id}/canvas` | The board, each frame with `stamp` and a fresh `url` (relative to the API base). |
| `POST /coddy/sessions/{id}/canvas/frames` | Add a frame (`path`, `title`, `device` / size): the Files window's Open on canvas. |
| `PATCH /coddy/sessions/{id}/canvas/frames/{frame}` | Move, resize, rename. |
| `DELETE /coddy/sessions/{id}/canvas/frames/{frame}` | Remove from the canvas (the files stay). |
| `POST /coddy/sessions/{id}/canvas/frames/{frame}/rotate` | Move the frame to its next generation (the page left the canvas). |
| `POST /coddy/sessions/{id}/canvas/frames/{frame}/report` | The browser's report (`stamp`, load, console, closed), kept in memory. |
| `GET\|HEAD /coddy/canvas/{capability}/{path...}` | The frame's files (6.3). Authenticated by the capability alone; a preflight `OPTIONS` there is answered with `Access-Control-Allow-Origin: *` before the auth gate. |

`GET /coddy/events` gets `canvas_changed` `{session_id, revision, frame_id, reason}` (`added`,
`updated`, `removed`, `shown`). Writes are cookie-CSRF checked like every other write; OpenAPI,
`docs/reference/http-api.md` move with them.

### 6.7 Opaque origins never reach the API

On a `coddy serve` without a token or a sign-in form, any page the browser runs can send requests to
the API; a canvas page is such a page, served from the API's own host. So the auth gate refuses,
before anything else, every request to a registered route whose `Origin` is `null` (an opaque origin:
a sandboxed frame, a `data:` or `file:` page), **whatever credential it carries**: a relay replaces
the caller's `Authorization` with its own node credential, and a page can read a token out of a
workspace file, so a bearer proves nothing here. The canvas route is the one exception; it serves
files only. The swarm relay refuses the same at its gate, so a relay without a client token cannot be
walked through by a frame. Requests without CORS (an `<img>`, a classic `<script>`, a stylesheet)
carry no `Origin`; their answers are opaque to the page, and JSON under `nosniff` is refused as a
script, a style or a picture.

This applies whether or not the canvas is enabled, and it is the one deliberate behaviour change of
the feature: a client in an opaque origin (a `file:` dashboard with a pasted token) stops working and
has to be served from an origin of its own. The pull request and the documentation of the HTTP API
say so.

### 6.8 Swarm

- **The relay carries the canvas route on the node's capability**, as it carries the workspace media
  route: `GET` / `HEAD` of `(<mount>)+/coddy/canvas/<capability>/...` - one or more mount hops, the
  pattern of the media route - with no `Authorization` header is handed to the node without the
  relay's own credential, and the node decides. A relay with a client
  token refuses everything else from a caller without one, as today.
- **The relay answers `Access-Control-Allow-Origin: *` itself for that route** (it strips the node's
  CORS headers on every route, and the page's module scripts and `fetch`es need it), and passes the
  node's `Content-Security-Policy`, `Referrer-Policy` and `X-Content-Type-Options` through unchanged.
- **The board is the node's**: the SPA reaches `/coddy/sessions/{id}/canvas` through the mount like
  every other session route, and `canvas_changed` rides the node's events stream through the relay.
- **A swarm of agents on one board**: subagents' frames land on the root session's board (6.2), so
  "three variants, one subagent each" ends as three frames side by side.

### 6.9 Configuration

```yaml
tools:
  canvas:
    enable: true            # false hides the tools, the routes answer 404, the view is gone
    allowed_sources:        # what a frame may load from outside its own files (CSP sources)
      - https://cdn.jsdelivr.net
      - https://unpkg.com
      - https://cdnjs.cloudflare.com
      - https://cdn.tailwindcss.com
      - https://fonts.googleapis.com
      - https://fonts.gstatic.com
```

An entry is a scheme-host source (`https://host`, `https://*.host`, an optional port and path);
`https:` alone, keywords, `*`, quotes, semicolons and whitespace are refused by the loader. An empty list keeps a frame to its own
files. Schema, `config.example.yaml`, `UISchemaMap`, the `configure-coddy` skill, the Russian
descriptions and the site's copy of the schema move together.

### 6.10 JSON Canvas files (Obsidian)

The operator asked for the canvas to work with Obsidian's canvases, so people move between the two
without a conversion (2026-10-18). Obsidian stores a canvas as a `.canvas` file in **JSON Canvas 1.0**
(jsoncanvas.org, MIT): `nodes` of type `text`, `file`, `link` and `group` with `x`, `y`, `width`,
`height` and an optional `color` (`"1"` to `"6"` or hex), `edges` between them with `fromSide`,
`toSide`, `fromEnd`, `toEnd`, `color` and `label`. This replaces the Excalidraw sketch frames planned
here before: Obsidian is what the operator's readers use, and its format is already a board of
positioned nodes, which is what the canvas is.

- **Linking.** A board can be **linked** to a `.canvas` file of the workspace (`Board.Link`, kept in
  `canvas.json`; the chat's own frames wait there until the link is undone). From then on the file is
  the board: every file node naming an HTML page of the workspace is a live frame (its page and root
  checked as `canvas_show` checks them, its size clamped to the frame bounds, its device inferred from
  its size), every other node is an **item** the web UI draws, and the edges are drawn between them.
  Linking writes nothing; a file that cannot be read shows nothing of it and says why.
- **The file stays Obsidian's.** `internal/canvas/jsoncanvas.go` reads the document into ordered
  objects, so a key Coddy does not know - a field of a later version, `styleAttributes`, another
  plugin's - and the order of the keys survive every write, and writes it the way Obsidian does, one
  node and one edge per line with tabs, so a change shows in git as the lines it changed. A frame's
  own settings that the standard has no field for and that are harmless to take from a file (a
  device that its size does not say, a title, who added it) go under a namespaced `coddy` key of its
  node, which Obsidian keeps; a node Coddy only moved gets no such key, and a node Coddy did not move
  keeps its own numbers (a node smaller than the frame bounds is drawn clamped and stays as it is).
  The generation behind a frame's address and a root wider than the page's folder are the bundle's
  (`Board.Gens`, `Board.Roots`), never the file's: a rotation writes nothing to the workspace, and a
  `.canvas` file that arrives with a checkout cannot widen what its pages read.
- **Writes.** The file is read again before every write, and only the board's frames and the
  geometry of the items it moved are applied to what is there, so an edit made in Obsidian in
  between is kept. A frame shown on a linked board becomes a file node with an id in Obsidian's shape
  (16 hex digits) right of everything on the board; a removed frame takes its edges along; a dragged
  group carries the nodes inside it in one write (`PATCH .../canvas/nodes`, all or none). Writes
  happen only when something changed (`dirty`), through a dot-named temporary file and a rename.
- **Limits.** The file is a regular file of the workspace outside dot-named folders, at most 8 MB;
  node paths are relative to the workspace, which is the vault's root; a page of another working
  directory (a subagent's worktree) is refused on a linked board, since its path means nothing in the
  vault; a node id outside `[A-Za-z0-9_-]{1,64}` is not a frame (it goes into addresses and bridge
  messages); at most 24 frames.
- **Surfaces.** `canvas_show` with a `.canvas` path links the board; the Files window offers
  **Open on canvas** for a `.canvas` file; the window's file menu saves the chat's own board as a new
  file (`Export`, which makes the folders it needs and then links it), opens one, shows it in Files
  and gives the chat its own board back. The web UI draws text cards as Markdown, `.md` file nodes as
  their note, pictures through the workspace media route, other files and links as cards (a link
  opens only an `http(s)` address), groups under everything with their label, and edges as Obsidian's
  curves from side to side with their arrows and labels; colours are taken only as a preset or a hex
  value. The board is read again every few seconds while open, so an edit in Obsidian shows without
  a reload.

## 7. Security summary

| Threat | Answer |
|---|---|
| Frame script reads the web UI's tokens or DOM | Opaque origin (iframe sandbox without `allow-same-origin`, and the CSP `sandbox` header for a top-level open). |
| Frame script drives Coddy's API | Opaque-origin refusal on the node and the relay (6.7); no bearer reachable. |
| Frame reads files outside its root | `os.Root` over the root; dot-names refused; the root is inside the workspace and never a dot path. |
| Capability leaks | Path token with `Referrer-Policy: no-referrer` header and meta; bound to session, frame, root, workspace; revoked with the frame; 12 hours. |
| Media and canvas tokens swapped | Domain-separated keys and `kind` in the claim. |
| Page exfiltrates over the network | CSP limits loads and connections to the allowed sources; WebRTC and DNS prefetch are not covered by CSP and are documented as such. |
| Picks or console text inject instructions | They reach the model only as text the person sends, or through `canvas_inspect` the agent calls; the window renders them as text. |
| Relay path confusion | The existing decoded-segment checks; the capability segment is base64url, no `%`. |

## 8. Tests

### 8.1 Specifications (`features/`, happy paths)

- `features/canvas_board.feature` (`internal/tools/canvas`, scripted model): the agent writes
  `site/index.html` and `site/app.js`, calls `canvas_show`; the board has one frame; showing it again
  keeps one frame; a subagent's frame lands on the parent's board.
- `features/canvas_http.feature` (`external/httpserver`): the board route lists the frame with a URL;
  the URL serves the page with the bridge, the CSP sandbox header and `no-referrer`; a relative module
  script is served with `Access-Control-Allow-Origin: *`; a report posted for the frame is what
  `canvas_inspect` returns; removing the frame revokes its address.
- `features/canvas_swarm.feature` (`external/swarm` with `http`): the same page through an
  authenticated relay, its relative script included, with no client token on the frame's requests.
- `features/web_ui_canvas.feature` (`external/ui`, vitest scenarios): the view button appears with
  the first frame, the window opens on `canvas_changed`, a pick becomes the prompt shown in 4.3.

### 8.2 Unit tests (edges)

Capability: tampered, expired, other session, other frame, a media token, a removed frame, a changed
root. Serving: traversal, `%2e%2e`, dot-names, a link to a dot-file and out of the root, directory
requests, `POST` 405, injection with and without doctype / head / html, BOM, a page over 10 MB is
served without injection. Opaque-origin guard: `Origin: null` with and without a bearer, the canvas
route exempt, the relay. Store: concurrent writes, a corrupt file, 24 frames, root resolution after a
cwd move. Reports: bounds, rate, stale stamps. SPA: the bridge protocol (message source check, limits,
text rendering), the pick-to-prompt formatter, the stamp-driven reload, the camera.

### 8.3 Browser check

`npm run check:canvas` (`external/ui/scripts/canvas-check.mjs`), on the pattern of `check:files`:
the real binary built with `TAGS="http ui swarm"`, a node with a bearer token and a relay with a
client token, a scripted OpenAI-compatible model whose turn writes a two-file page (an ES module
counter) and calls `canvas_show`. Through the relay only, it checks: the window opens; the page's
module script runs (a click increments the counter); `window.parent.document` and `localStorage` throw
inside the frame; the frame's address opened in a tab of its own is still sandboxed; a `fetch` from
the frame to `/coddy/sessions` is refused; a pick in Comment mode puts the selector in the prompt
bar; a script error reaches `canvas_inspect`; an edit by the scripted model reloads the frame alone;
the window at every tier of the grid in English and Russian; with `CODDY_SHOTS_DIR`, the screenshots
for the documentation. Chromium by default, `CODDY_ENGINE=webkit` once.

The opaque-origin refusal is proven where nothing else would stop the request: a node with no token
and no sign-in (direct), a node behind a sign-in form only, and a relay without a client token in
front of a node (the relay adds its node credential). In each the page's script first learns the API
address from its own location (the full mounted address through a relay), the same request from the
web UI is shown to succeed (a positive control), and the page's own `fetch` and form post are shown
to be refused. A page that navigates itself to another address shows *This page left the canvas* and
its old address stops working.

## 9. Documentation

`docs/features/canvas.md` (and `docs/ru/features/canvas.md`), entries in both `nav.yaml`, rows in
`docs/reference/tools.md`, the routes in `docs/reference/http-api.md`, the config table (`make docs`),
a section in `docs/surfaces/web-ui.md`, the canvas exception in `docs/operate/swarm.md`, the README
line, `DESIGN.md` (**Canvas window**), the `ui-spa` and `ui-verification` rules (both trees), the
`configure-coddy` skill, screenshots under `docs/assets/canvas/`.

## 10. Stages

1. `internal/canvas`: board, store, stamp, root resolution, serving, bridge, reports; unit tests.
2. `internal/tools/canvas`: the two tools, config, schema; the board feature.
3. HTTP: routes, capability, events, the opaque-origin guard, OpenAPI; the HTTP feature.
4. Swarm: the relay exception, CORS and header pass-through; the swarm feature.
5. SPA: the window, board, frames, bridge client, picks, prompt bar, tool card, Files menu item,
   i18n; vitest and the web UI feature.
6. `check:canvas`, screenshots, documentation in both languages, `DESIGN.md`, rules.
7. JSON Canvas files (6.10): the codec and the link in `internal/canvas`, the routes, `canvas_show`
   for a `.canvas` path, the nodes and edges in the window, their scenario, unit tests and browser
   check, documentation.
8. Final cross-review.

## 11. Found on the way

- **Live share_file cards.** `internal/agent/react.go` puts a shared file's metadata at
  `_meta.artifacts` of the `tool_call_update`, while the SPA reads `_meta.coddy.artifacts`
  (`consumeComposerSse.ts`, whose test fixture uses the SPA's shape), so a card appears only after a
  reload. Fixed in this change: the agent writes `_meta.coddy.artifacts`, a Go test holds the shape the
  SPA reads.

## 12. Not in this change

Drawing on the board and editing a text card in the web UI (the file is edited in Obsidian or by the
agent); Excalidraw sketch frames, which the JSON Canvas files replaced (6.10); a screenshot of a
frame for the model; frames
from a dev server (`npm run dev`) instead of files; version history beyond git; sharing a frame
outside Coddy (issue #480); React/JSX compiled in the browser without a CDN.

## 13. Review round 1

Reviewers: Devin `swe-2` and Codex `gpt-6.1-sol` (with read access to a detached worktree), Cursor
`auto` (the same), Coddy `qwen3.8-27b` (brief only). Every finding was checked against the code or
the specifications before a decision.

Accepted and folded into the sections above:

- a frame's page can navigate itself anywhere and take its address and its files along; CSP does not
  govern that (principle 7, 6.5 leaving the canvas, `Rotate`);
- the opaque-origin refusal must not trust a bearer: a relay substitutes its own node credential, and
  a page can read a token from a workspace file (6.7); it is a deliberate behaviour change, said so;
- checking dot-names on a resolved link and opening the path afterwards races a link swap: frames
  serve no links at all and verify the opened file (6.3);
- the bridge address must be relative to survive relay mounts (6.3);
- bridge messages and reports need a load generation and the stamp, or an old document's report
  passes for the new files (6.5);
- live reload needs polling while the window is open: a background build can change files after the
  last tool call (4.5);
- an open frame outlives its address unless it is renewed (6.3);
- the browser check cannot prove the opaque-origin refusal behind tokens; it needs a tokenless node,
  a sign-in-only node and a tokenless relay, the full mounted address and a positive control (8.3);
- `canvas_inspect` hands page-written text to the model: the result frames it as untrusted output of
  the page;
- the window coalesces reports, reports say when the frame was put away and how old they are;
- a preflight on the canvas route needs `Access-Control-Allow-Origin: *` before the auth gate, on the
  node and on the relay; the relay pattern takes any number of hops;
- keys of the mode toggle are dropped (a frame has the focus); the dedupe tuple is named; the hash
  route is `?frame=`; `https:` alone is refused as a source; Open in a new tab is dropped (the address
  would sit in the browser's history); `localStorage` / `sessionStorage` are shimmed in memory;
  classic scripts' muted errors are documented and module scripts recommended.

Rejected, with the reason:

- "`<img>` and `<script>` send `Origin` on every cross-origin GET": a no-cors GET carries no `Origin`
  (Fetch, "append a request Origin header"); only CORS requests and other methods do;
- "restrict the CSP by path to the capability": the node cannot know the origin and the mount path the
  browser sees through a relay, and a host-source path needs both;
- "drop `'unsafe-eval'` / `'unsafe-inline'`": the isolation is the opaque origin, not the CSP; the page
  is the workspace's own code;
- "`allow-forms` contradicts `form-action 'none'`": without `allow-forms` a sandboxed form never fires
  `submit`, so script-handled forms would break; `form-action` stops the navigation;
- "default `allowed_sources` to empty": with self-navigation possible the CDN list adds no channel an
  attacker reads, and an empty default breaks the commonest prototypes (Tailwind, a font, a chart
  library); the documentation names the trade-off;
- "ship the share_file fix in its own pull request": the operator's rule is that a fix found on the
  way lands in the change that found it, as its own commit.

## 14. Final review

Reviewers of the pull request (#556): Cursor `auto`, Codex `gpt-6.1-sol` (restarted once after a
provider error), Devin `swe-2`, all with the checkout to read; Coddy `qwen3.8-27b` on the Go part
without tools was stopped after 23 minutes of reasoning with no final list, under the operator's
standing rule, and its drafted points were checked as advisory. Every finding was checked against
the code; each accepted one has a test that fails without its fix.

Accepted and fixed:

- the root of a frame was reopened by its absolute path, so a root folder swapped for a link after
  the frame was shown led its address outside the workspace: the root is now opened beneath the
  workspace with no link on the way and the folder opened checked to be the one inspected (Codex);
- on Windows a backslash in a frame's path passed the dot-name checks and opened `.env`: frames refuse
  `\` and NUL, and `webfs.HasDotSegment` treats `\` as a separator, for the preview server too (Codex);
- relinking a file (and unlinking and linking again) dropped `Board.Gens`, so an address revoked by a
  rotation applied again: generations survive link changes, and a frame removed through Coddy keeps
  its generation one up (Codex, Devin);
- a linked write replayed the whole board, so two chats on one file, or an edit made in Obsidian in
  between, lost a move, and a stale board recreated a node deleted in the file: a write now applies
  only what this board changed since it read the file (`Board.read`, `Board.readItems`), leaves a node
  deleted meanwhile deleted, and takes a per-file lock (Codex);
- a `.canvas` file could widen a page's root through `coddy.root` (found while checking the above):
  roots wider than the page's folder are the session's (`Board.Roots`), never read from the file;
- a write rewrote the geometry of every frame node, so a node smaller than the frame bounds grew in
  the file: a node Coddy did not move keeps its numbers (found the same way);
- a frame pointed at another page of its root kept showing the old one (Codex);
- the canvas bar cleared its text and picks before the server took them, and a refusal lost them: they
  come back to the bar, ahead of anything written since, without touching the composer's draft (Codex);
- the renewal of a frame's address compared the server's `expires_at` with the page's clock, so with a
  clock half a day off the frame reloaded on every read of the board: it is timed on the page's own
  clock and takes only an address signed since (Cursor);
- a page over 10 MB was served without the bridge and read as having left the canvas: `canvas_show`
  refuses it, and one that grew past the limit gets a short page with the bridge that says so (Cursor);
- a `canvas_inspect` wait that timed out left its waiter behind (Cursor, qwen);
- a failed export left the new file behind, so a retry found it there (Cursor);
- the Files window offered Open on canvas for `.xhtml`, which the canvas does not run, and swallowed
  every refusal: the item follows the extensions the server takes and a refusal shows (Devin);
- a note card read its `.md` file only when the `.canvas` file changed: it is read again every
  10 seconds while the board is open, the last text kept on the card meanwhile (Devin).

Rejected:

- echoing `Access-Control-Request-Headers` on a frame's preflight is wider than needed (Cursor): the
  route carries no credential but the capability in its path, so an allowed header grants nothing;
- a HEAD of a frame's address goes through the auth gate (qwen): Go's `GET` pattern matches HEAD, and
  `TestCanvasFrameAnswersHEADWithoutACredential` holds it;
- `LinkFile` on a linked board switches files silently (qwen, Devin's question): opening another file
  is the intent, and the generations now carry over.

Code scanning (Semgrep, `wildcard-postmessage-configuration`) flagged the two `postMessage` calls with
`"*"`: the window's to a frame (`CanvasFrame.tsx`) and the bridge's first `ready` (`bridge.js`). Both
are suppressed inline with the rule id and the reason, not changed: an opaque-origin frame can be
posted to with `"*"` only, the parent's origin is unknown to the bridge until the hello (no referrer,
no `ancestorOrigins` in Firefox) and every later message of the bridge goes to that origin, and what
travels with `"*"` is the hello (a generation and the frame's id), the mode and the empty `ready`.
`what the window tells a frame names no address, token or file` (vitest) holds the window's side to
that. The same scan's `math-random-used` on `internal/llm/reasoning_test.go`, older than this change,
is a seeded source for `testing/quick` and is suppressed the same way.

Left as known limits: every request for a frame's file and every board read loads the board (for a
linked board, parses the file) and computes the stamps of up to 2000 files per frame (Devin); a cache
keyed on the modification times is the next step if a large workspace shows it. In interact mode an
Escape inside a page also steps the window back, by design (6.5).

