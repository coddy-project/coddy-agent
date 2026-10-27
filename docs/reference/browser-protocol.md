# Browser protocol

The wire between `coddy serve` and the [Coddy for Browser](../features/browser.md) extension. Two repositories
implement it: the endpoint and the `browser` tool in `external/browser` of this repository, and the extension in
[coddy-project/coddy-for-browser](https://github.com/coddy-project/coddy-for-browser). A change to the protocol
lands in both, and this page changes with it.

The current version is **1**.

## Transport

The extension opens one WebSocket to the HTTP server of `coddy serve`, its home server, and keeps it open. The
server never connects to the browser: everything Coddy asks of it travels down this socket, so Coddy reaches a browser
it has no route to. The endpoint answers while `browser.enable` is on with a `browser.token`, and `404` otherwise.

| | |
| --- | --- |
| Address | `ws://<host>:<port>/browser/v1/connect` on the HTTP server, the address of the web UI; `wss://` behind an HTTPS proxy |
| Origin | `chrome-extension://<id>` (Chrome, Yandex Browser) or `moz-extension://<uuid>` (Firefox); the server refuses the upgrade with any other `Origin` header, or none |
| Messages | One JSON-RPC 2.0 object per text frame, in both directions |
| Size | 16 KiB until `initialize` is answered, 8 MiB after |
| Liveness | The extension sends `$/ping` every 20 s; the server closes a socket silent for 60 s |

The endpoint's routes are outside the HTTP API's own gate: the extension proves the key itself, in `initialize`
and on every file transfer. A chat of another Coddy process never uses this socket; it reaches the browser through
the home server's API ([Other Coddy processes](#other-coddy-processes)).

Both sides send requests, answers and notifications. Coddy's request ids are strings of 48 hex digits; the
extension's ids are its own. An answer to an id the other side never sent is ignored.

## Handshake

The first message is the extension's `initialize` request. The key travels in it because a browser cannot set
headers on a WebSocket. The server answers anything else first with `-32600` and closes the socket.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": 1,
    "key": "<browser.token>",
    "browser": { "id": "6f1c0a9e-2d4b-4b8e-9a51-3c7e0f2d9b10", "name": "Chrome - laptop", "version": "0.1.0" },
    "actions": ["open_tab", "close_tab", "release", "snapshot", "screenshot", "navigate", "click", "type", "press", "hover", "scroll", "drag", "upload_file", "download_file"],
    "grant": "b0f3a5d2-..."
  }
}
```

- `browser.id` is stable for one installation: 8 to 80 characters of letters, digits and `-`. A second connection
  with the same id replaces the first one, whose requests still waiting fail as an unknown outcome;
- `browser.name` (1 to 100 characters) tells browsers apart when several are connected;
- `actions` lists what the extension implements. Coddy sends no other action;
- `grant` names the extension's current selection, as `browser/tabs` does, so a command sent before the first tab
  list carries the right one.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": 1,
    "server": { "name": "coddy", "version": "1.4.0" },
    "limits": { "maxFileBytes": 33554432 }
  }
}
```

| Error | Meaning |
| --- | --- |
| `-32001` | The key does not match `browser.token`. |
| `-32002` | Another protocol version; `data.supported` lists the versions the server speaks. |
| `-32600` | The first message was not `initialize`. |
| `-32602` | Invalid parameters, a malformed browser id or name among them. |

After an error the server closes the socket. Right after the answer, the extension sends `browser/tabs`, an empty
list included.

## Tabs

The extension decides which tabs Coddy reaches and reports them in full whenever the list changes.

```json
{
  "jsonrpc": "2.0",
  "method": "browser/tabs",
  "params": {
    "tabs": [
      { "id": 41, "url": "https://example.com/", "title": "Example Domain" },
      { "id": 77, "url": "https://example.org/report", "title": "Report", "sessionId": "sess_8f2c..." }
    ],
    "grant": "b0f3a5d2-..."
  }
}
```

- A tab without `sessionId` is shared: any chat may act in it, and its first action moves it to that chat. A tab with
  `sessionId` belongs to that chat, and the server refuses another chat's command for it before sending it;
- at most 100 tabs, `http` and `https` pages only, a URL of at most 8192 characters and a title of at most 1000. A
  list that breaks these rules ends the connection;
- `grant` names the current selection. The extension replaces it when the user presses Stop, and refuses a command
  that carries an older one. An empty `grant` keeps the previous one;
- the extension sends `browser/tabs` before it answers a command that changed the list, so the answer to `open_tab`
  never names a tab Coddy does not know yet.

In Coddy for Browser the list is the content of its tab groups of the browser: the shared **Coddy** group, and one group per
chat titled after the chat.

## Commands

Coddy sends one `browser/command` request per action of a chat.

```json
{
  "jsonrpc": "2.0",
  "id": "9a1f...",
  "method": "browser/command",
  "params": {
    "action": "click",
    "tabId": 41,
    "params": { "ref": "4c1d9e2a:7" },
    "grant": "b0f3a5d2-...",
    "session": { "id": "sess_8f2c...", "title": "Check the release notes" }
  }
}
```

`params` holds the action's own parameters as the chat's model wrote them. `session.title` names the chat for the
tab group and the in-page indicator. `tabId` is left out for `open_tab` and `release`.

A chat of another Coddy process carries `session.node`, the machine it runs on as it names itself, and its
`session.id` is `<node>/<session id>`; a session id of the home server never holds a `/`. The extension asks the user
before such a chat's first action and adds the node to the title of its tab group.

```json
"session": { "id": "laptop/sess_31d0...", "title": "Plan the trip", "node": "laptop" }
```

| Action | Parameters | Result |
| --- | --- | --- |
| `open_tab` | `url` | `tabId`, `url`, `title` of the new tab, once it loaded; the tab starts in the chat's group |
| `close_tab` | - | `closed` |
| `release` | - | `released`: how many of the chat's tabs went back to the shared group |
| `snapshot` | - | `url`, `title`, `text` (up to 20000 characters), `elements` (up to 500: `ref`, `tag`, `name`, `x`, `y`, and `role`, `type`, `href`, `value`, `checked`, `disabled`, `offscreen` when they apply), `viewport` |
| `screenshot` | - | `dataUrl` (`data:image/png;base64,...`, one image pixel per CSS pixel), `width`, `height` |
| `navigate` | `url`, or `back` / `forward` | `url`, `title` after the load |
| `click` | `ref`, or `x` and `y`; `button` (`left`, `right`, `middle`), `count` (1 to 3) | `ok`; `trusted: false` when the input went through the page |
| `type` | `text`; `ref`, `clear` | `ok` |
| `press` | `key` (`Enter`, `Tab`, `Escape`, `Backspace`, `Delete`, arrows, `Home`, `End`, `PageUp`, `PageDown`, `Space`, a letter or digit, with `Ctrl+`, `Alt+`, `Shift+`, `Meta+`) | `ok` |
| `hover` | `ref`, or `x` and `y` | `ok` |
| `scroll` | `dx`, `dy`; `x`, `y` (the middle of the viewport when left out) | `ok` |
| `drag` | `x`, `y`, `to_x`, `to_y` | `ok` |
| `upload_file` | `ref` of a file input, `transfer_id`, `filename` | `filename`, `bytes` |
| `download_file` | `url`, `transfer_id`, `filename` | `bytes`, `filename` |

- Coordinates are CSS pixels of the viewport. A `ref` comes from the latest `snapshot` of the same page and stops
  working when the page changes;
- every result but `open_tab`, `close_tab` and `release` also carries `tab`: the `id`, `url` and `title` of the tab
  after the action;
- an extension without the browser's debugger (Firefox) dispatches the events of `click`, `type`, `press`, `hover`,
  `scroll` and `drag` in the page itself and adds `trusted: false` to their result: a page can tell them from a
  person's input;
- an action fails with an error answer, code `-32000`, a message the chat reads and a `data.reason` among
  `not_shared`, `other_chat`, `stopped`, `chat_declined`, `chat_prompt_timeout`, `site_declined`, `cross_origin_frame`,
  `site_prompt_timeout`, `page_changed`, `cancelled`, `unsupported`, `invalid_params`, `too_large`,
  `transfer_failed`, `download_failed`.

Coddy waits 2 minutes for an answer, 10 for `download_file`. When a chat's turn is cancelled or the wait runs out,
Coddy sends `$/cancelRequest` and reports to the chat that the outcome is unknown; it never repeats an action by
itself.

```json
{ "jsonrpc": "2.0", "method": "$/cancelRequest", "params": { "id": "9a1f..." } }
```

## Website approval

Before the first action on a website (scheme, host and port), the extension asks the user in a window of its own, or
in the side panel when it is open: allow for this chat, always allow, or decline. `open_tab`, `navigate` and
`download_file` ask about the site they go to. A question unanswered for 110 s fails the command with
`site_prompt_timeout`, a refusal with `site_declined`. The question never reaches Coddy: the user's answer stays with
the browser.

A chat that carries `session.node` is asked about first, once: allow this chat, or decline (`chat_declined`,
`chat_prompt_timeout`). "Always allow" covers the chats of the home server only, so every new website asks again for
a chat of another process, and Stop takes back what such chats were allowed.

The list of sites allowed always starts with host entries for `localhost`, `127.0.0.1` and `[::1]` (`*://<host>:*`,
any scheme and port), which the user revokes like any other. A host entry leaves out the home server's own port under
every local name, and a chat that carries `session.node` asks about local origins like any other.

## Files

`upload_file` and `download_file` carry no file content on the socket. Coddy opens a one-time ticket for the length
of the call and sends its id as `transfer_id`; the extension moves the bytes over HTTP on the same server, with the
key as a bearer token.

| Request | Direction | Answer |
| --- | --- | --- |
| `GET /browser/v1/files/<transfer_id>` | Coddy to the browser: the file to set on the input | `200` with the bytes |
| `POST /browser/v1/files/<transfer_id>` | the browser to Coddy: the bytes it downloaded | `204` |

A ticket serves one request within 2 minutes and then answers `410`; a wrong key answers `401`. A file is at most
32 MiB. The extension fetches a download with the browser's cookies and refuses a redirect, so the chat passes the
final address.

## Notifications

| Method | Direction | Parameters | Meaning |
| --- | --- | --- | --- |
| `browser/tabs` | extension to Coddy | `tabs`, `grant` | The tabs Coddy may reach, in full. |
| `browser/stop` | extension to Coddy | - | The user pressed Stop: every command still waiting fails and no tab is shared until the next `browser/tabs`. |
| `$/ping` | extension to Coddy | - | Keeps the socket alive, and Chrome's service worker with it. |
| `browser/turnEnded` | Coddy to the extension | `sessionId` | The chat's turn ended; the extension takes its working marks off. Coddy sends it only to a browser holding a tab of that chat. |
| `$/cancelRequest` | Coddy to the extension | `id` | Stop the command with that id. |

A side ignores a notification it does not know, and answers a request it does not know with `-32601`, so either side
can be upgraded first within one protocol version.

## Side panel

The extension's side panel shows the web UI of the home server in a frame. It asks for a ticket over the socket.

```json
{ "jsonrpc": "2.0", "id": 7, "method": "panel/open", "params": {} }
```

```json
{ "jsonrpc": "2.0", "id": 7, "result": { "ticket": "3f9c...64 hex digits" } }
```

A ticket is good for one sign-in within a minute and is bound to the key it was issued for. The panel loads
`<server>/?coddy_panel=<ticket>&coddy_panel_origin=<extension origin>` (`chrome-extension://<id>` or
`moz-extension://<uuid>`); the web UI takes both out of the address
and redeems the ticket at `POST /coddy/auth/browser` for a session cookie of its own, which ends when `browser.token`
changes ([HTTP API](http-api.md#web-ui-sign-in-optional)).

The frame and the panel then talk by `postMessage`, each to the other's origin only, and each takes a message only from
the other's window at that origin.

| Message | From | Fields | Meaning |
| --- | --- | --- | --- |
| `{source: "coddy-web-ui", type: "turn"}` | the web UI | `requestId`, `sessionId`, `title` | Before a turn is sent: which tab does the person write from? |
| `{source: "coddy-for-browser", type: "turn"}` | the panel | `requestId`, `tab` (`browser_id`, `tab_id`, `url`, `title`) or `null`, `error` | The tab, already in the chat's tab group. The web UI sends it as `metadata.browser` of `POST /v1/responses`. |
| `{source: "coddy-web-ui", type: "theme"}` | the web UI | `theme`: `scheme` and the colours `bg`, `surface`, `border`, `text`, `muted`, `accent` as computed colours, `rgb()` or `color(srgb ...)` | The panel's strip takes the web UI's theme; any other value is refused. |
| `{source: "coddy-for-browser", type: "open"}` | the panel | `sessionId` | Show this chat of the server (`#/s/<sessionId>`). Only an id of the form `sess_...` is taken, and only while the web UI shows the panel's own server. |

The web UI waits 3 s for the panel's answer and sends the turn without a tab when none comes. A chat of another
environment of the web UI gets no tab.

Opened on a tab that a chat of the home server holds, the panel loads the web UI on that chat:
`<server>/?coddy_panel=...&coddy_panel_origin=...#/s/<session id>`.

## Other Coddy processes

A chat that does not run in the home server's process - a console or an editor on the same machine, a node of the
swarm - reaches its browsers through the home server's HTTP API, behind the server's ordinary credentials, while
`browser.serve_remote` is on. Through a swarm relay the routes are carried as `<relay>/swarm/nodes/<node>/coddy/...`.

| Request | Does |
| --- | --- |
| `GET /coddy/browser/v1/browsers?node=&session_id=` | the connected browsers and their tabs as that chat sees them |
| `POST /coddy/browser/v1/call` | one action: `browser_id`, `action`, `tab_id`, `params`, `caller` (`node`, `session_id`, `title`), and `file` (`name`, base64 `data`) for `upload_file` and `download_file` |

The home server sends the action down the socket with `session.id` `<node>/<session id>` and `session.node`, and
answers with `result` and, for `download_file`, the bytes in `file`. A refused or failed action answers `409` with the
extension's message. The caller drops its request to cancel the action, and the home server sends
`$/cancelRequest`. The home server cannot see where a remote chat's turn ends, so it sends `browser/turnEnded` for
that chat 45 s after its last call. The fields and status codes are in the [HTTP API](http-api.md).
