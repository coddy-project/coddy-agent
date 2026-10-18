# Canvas

A canvas is a `.canvas` file of the workspace, in JSON Canvas, the format of Obsidian's canvases, opened in the Files window of the web UI like any other file. Its tab shows a board: the HTML pages the file names run on it as live frames, and its cards, groups and edges are drawn as Obsidian draws them. You click through the pages, point at an element and say what to change in the prompt bar under the board, and the agent reads what the browser saw. Since the board is the file, it is in git, a board drawn in Obsidian opens in Coddy and a board made in Coddy opens in Obsidian.

Ask in your own words:

```text
Sketch a landing page for the shop and put it on the canvas, phone and desktop.
```

The agent writes the page with the file tools, in a folder of its own, and calls `canvas_show`. With no canvas in the chat yet it makes one, `canvas/<folder of the page>.canvas`, and the Files window opens on it at the new frame; on a phone the chat stays, and the tool's card opens the file.

![A .canvas file in the Files window: two frames of the same page, at desktop and phone size, the prompt bar under the board](../assets/canvas/canvas-board-dark-1280.png)

*One page at two sizes: each frame runs the page's own files.*

## Putting a page on the canvas

A frame is an HTML page of the session's workspace (`.html` or `.htm`, outside dot-named folders, at most 10 MB; data inlined past that belongs in files of its folder), and a file node of the `.canvas` file. It is put there two ways:

- the agent calls `canvas_show` with its path, and with `file` to name the canvas;
- you pick **Open on canvas** in the **More** menu of the Files window, for an HTML file.

A page shown without a file goes on the chat's canvas: the `.canvas` file a page was last shown on or the one you opened last in the Files window. A chat that has none gets a new `canvas/<folder>.canvas`, numbered when the name is taken. The canvas_show card in the transcript opens the file on the frame it put there.

| Size | Width × height |
|---|---|
| `desktop` (default) | 1280 × 800 |
| `tablet` | 820 × 1180 |
| `phone` | 390 × 844 |
| custom | 320-3840 × 320-4320, `width` and `height` together |

The same page at the same size is one frame: showing it again focuses that frame. Showing it at another size adds a second frame, which is how one page is checked at three widths at once. A canvas runs at most 24 pages; a node past that is drawn as a card. The address of a canvas is the Files window's, `#/s/<session>/files?path=<file>.canvas`.

A frame loads the files under its **root**, by default the page's folder. A page in the workspace's top folder has the whole workspace as its root and can read every file of it outside dot-named folders, so the tool warns about it: keep a page in a folder of its own.

## Working on the board

The switch at the board's top left says what a click does:

- **Interact** - clicks, keys and the wheel over a page go to the page;
- **Comment** - a click on an element of a page picks it.

The wheel over the board zooms it at the pointer, the way the picture viewer and the swarm map zoom: a turn of the same length doubles or halves what is under the pointer. Dragging the empty board pans it, two fingers pinch it. The zoom controls at the board's top right are the same as theirs: minus, the level (the percentage, or the fit glyph while the board is fitted; a click fits everything), plus; with the board focused, `+`, `-` and `0` do the same. A pan or a zoom moves the board as one layer and draws nothing on it again, so a board of many pages stays as smooth as an empty one. A frame's title bar moves it, and its buttons change its size, reload it and take it off the canvas (the files stay).

A picked element opens a note beside it. **Add** makes it a chip of the prompt bar under the board, and **Send** sends the chips and the text as one prompt of the chat: each element by its page, its size, a CSS selector, its text and its note. While a turn runs, the message waits in the [message queue](message-queue.md). A permission or a question the turn waits for shows above the bar, since the window covers the chat. **Chat** closes the Files window and goes back to the conversation; the bar keeps a half-written request, per file, for the life of the page.

![Comment mode: a heading of the page picked, with a note for the agent](../assets/canvas/canvas-comment-dark-1280.png)

*A picked element and its note: Add makes it a chip of the prompt bar.*

A frame reloads itself when a file under its root changes, and only that frame: the board is read again after every tool call, on every change the server announces and every 4 seconds while it is open, a frame whose files moved loads again, and a read that changed nothing draws nothing. Escape closes the note first, then leaves Comment, then closes the window. **Preview** in the window's **More** menu switches the tab to the file's JSON and back.

On a phone the Files window takes the screen below the top bar, the board takes the file's place, and the frames start fitted.

![A .canvas file on a phone: the frames fitted, a picked element as a chip of the prompt bar](../assets/canvas/canvas-board-dark-390.png)

*On a phone: the same board, fitted to the screen.*

## What the agent sees

The browser that shows a frame reports on it: whether it loaded, its title, the errors and warnings of its console and the exceptions nobody caught. `canvas_inspect` reads those reports, so the agent finds a broken script without asking you to open a console. After an edit it passes `wait_seconds` and gets the report for the new files, or says that none came. The console lines are the page's own output and are handed to the model as untrusted data, never as instructions. Reports exist only while someone has the canvas open; they are kept in memory, 20 lines per frame.

| Tool | Arguments | What it does |
|---|---|---|
| `canvas_show` | `path`, `file`, `title`, `device`, `width`, `height`, `root`, `frame` | Puts a page on the `.canvas` file `file` (made when missing), or on the chat's canvas, or points the frame `frame` at it; with a `.canvas` file as `path`, makes that file the chat's canvas |
| `canvas_inspect` | `file`, `frame`, `wait_seconds` | Reads the browser's reports of one frame or all of them, waiting up to 15 seconds for one on the current files |

A subagent working in the chat's folder shows its pages on the chat's canvas, so three variants of a page from three subagents are compared side by side. A subagent in a [worktree](worktrees.md) of its own is refused: its pages are not files of the folder the chat's canvas is in.

## Obsidian canvases

Obsidian keeps a canvas as a `.canvas` file in [JSON Canvas 1.0](https://jsoncanvas.org): nodes of four kinds - text, file, link and group - placed on a plane, and edges between them. Every canvas of Coddy is such a file, so a `.canvas` file of the workspace opens in the Files window as its board, and opening it makes it the chat's canvas: the pages the agent shows next go on it.

![An Obsidian canvas in the Files window: a group with a text card, a note and a link, an edge with a label into a live frame of the shop page](../assets/canvas/canvas-obsidian-dark-1280.png)

*A board drawn in Obsidian: its page runs as a frame, its cards, group and edges are drawn as they are.*

What the board shows of the file:

- a file node naming an HTML page of the workspace is a live frame, at the node's size;
- a text card is its Markdown, rendered;
- a file node of a `.md` note shows the note, read again every 10 seconds while the board is open, a picture shows the picture, any other file is a card with **Open in Files**;
- a link node is a card that opens the address in a new tab (only an `http` or `https` one);
- a group is a frame under the nodes, with its label;
- an edge is a curve from side to side, with its arrows, its label and its colour; colours are the six presets of JSON Canvas or a hex value.

What the canvas writes back to the file:

- a frame shown, moved, resized or taken off the canvas; a new frame becomes a file node right of everything on the board, a removed one takes its edges along;
- a card, a link or a group dragged on the board; a group carries the nodes inside it;
- nothing else. Every other node, edge and key stays as it was, keys Coddy does not know included, and the file is written byte for byte the way Obsidian writes it: a node or an edge of plain values on one line, one holding an object (`styleAttributes`, the `coddy` key) one key a line, the keys of a node Coddy changed in the order Obsidian gives a node it changed, characters escaped as Obsidian escapes them, and the file ending at its closing brace. Saved in Obsidian 1.14.4, such a file changes only on the lines of the edit made there, and Coddy writing it while it is open in Obsidian changes only the lines Coddy moved: Obsidian reads the change and keeps it on its next save. Opening or reading a file writes nothing.

Obsidian makes edits of its own as it draws a canvas: a picture node takes the picture's proportions, and an edge without sides gets the sides it is drawn with. They show in the file after Obsidian's next save, not after Coddy's.

`canvas_show` writes the file without a permission prompt: the node of the frame it shows and nothing else, or a new file with that one node under `canvas/`.

A frame's settings that JSON Canvas has no field for - a device its size does not say, a title - go under a `coddy` key of its node, which Obsidian keeps. A node Coddy did not move keeps its own numbers, even one smaller than a frame may be. A root wider than the page's folder, the generation behind a frame's address and which file is the chat's canvas stay with the session and never reach the file, so a `.canvas` file that arrives with a checkout cannot widen what its pages may read: a page reads its own folder unless `canvas_show` widened it in this chat. Two chats with the same file open take turns writing it, and each writes only what it changed.

The board reads the file again whenever it is read, so an edit in Obsidian, or one the agent makes with the file tools, shows on the open board within seconds. Text cards, edges and new links are edited that way, in the file: the canvas moves nodes, it does not write text.

Paths in a node are relative to the workspace, so the workspace is the Obsidian vault, or the file names its pages from the vault's root. The file must be a regular file of the workspace outside dot-named folders, at most 8 MB; one that does not parse says so on the board and is never written. A node whose id is not letters, digits, `-` and `_` (at most 64) is drawn as a card and does not run.

## What a page can do

A frame runs under a sandbox that grants it no origin of its own:

- the frame's address carries its own capability: `/coddy/canvas/<capability>/<page>`, signed by the server for that session, `.canvas` file, frame, root and generation, valid while the file holds the frame at that generation in the session's working directory, and for 12 hours at most;
- the server answers it with `Content-Security-Policy: sandbox allow-scripts allow-forms allow-modals` and the iframe has the same sandbox, so the page runs scripts in an opaque origin: no cookies, no storage of the web UI, no access to its API; `localStorage` and `sessionStorage` work in memory for the life of the frame, and forms post nowhere;
- every API route refuses a request whose `Origin` is `null` with 403, whatever credential it carries, so a page cannot drive the server even where the server has no token; the frame's own file route is the one exception;
- a page loads from its own address, from `data:` and `blob:` addresses, from the common CDNs while `tools.canvas.allow_cdn` is on and from the hosts of `tools.canvas.allowed_sources`; `fetch` and WebSocket reach its own address and the same hosts;
- the page can read every file under its root and send it to any of those sources: what it may read is what you put under its root;
- a page that navigates away from the canvas says so on its frame, and the frame's old address stops working; **Back to the page** loads it again under a new one;
- the frames serve no symbolic links and no dot-named path, and a folder answers its `index.html`, never a listing.

A frame's address works through a [remote server](../surfaces/console.md#remote-mode---remote) and through a [swarm relay](../operate/swarm.md) as it does locally: the relay carries it to the node without the relay's credential, and the capability in the path is all the node checks.

## Configuration

```yaml
tools:
  canvas:
    enable: true          # false hides the tools and the canvas
    allow_cdn: true       # cdn.jsdelivr.net, unpkg.com, cdnjs.cloudflare.com, cdn.tailwindcss.com, Google Fonts
    allowed_sources: []   # more hosts a frame may load from and connect to, as https://host or https://*.host
```

A CDN and a host in `allowed_sources` are places a page can send what it reads. The fields are listed in the [configuration reference](../reference/config.md). The routes of the canvas and of the frames are in the [HTTP API reference](../reference/http-api.md#canvas).

The canvas is a surface of the web UI. In the console and in an editor over ACP the tools are offered as well and the `.canvas` files are written, but nothing runs the frames; there the [preview server](preview-server.md) serves a page for a browser of your own.
