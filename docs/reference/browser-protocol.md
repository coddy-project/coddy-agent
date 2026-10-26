# Browser protocol

The wire between `coddy serve` and the [Coddy for Browser](../features/browser.md) extension. Two repositories
implement it: the listener and the `browser` tool in `external/browser` of this repository, and the extension in
[coddy-project/coddy-for-browser](https://github.com/coddy-project/coddy-for-browser). A change to the protocol
lands in both, and this page changes with it.

The current version is **1**.

## Transport

The extension opens one WebSocket to the browser listener and keeps it open. The listener never connects to the
browser: everything Coddy asks of it travels down this socket, so Coddy reaches a browser it has no route to.

| | |
| --- | --- |
| Address | `ws://<host>:<port>/browser/v1/connect`, or `wss://` when the listener has `cert_file` and `key_file` or sits behind an HTTPS proxy |
| Origin | `chrome-extension://<id>`; the listener refuses the upgrade with any other `Origin` header, or none |
| Messages | One JSON-RPC 2.0 object per text frame, in both directions |
| Size | 16 KiB until `initialize` is answered, 8 MiB after |
| Liveness | The extension sends `$/ping` every 20 s; the listener closes a socket silent for 60 s |

Both sides send requests, answers and notifications. Coddy's request ids are strings of 48 hex digits; the
extension's ids are its own. An answer to an id the other side never sent is ignored.

## Handshake

The first message is the extension's `initialize` request. The key travels in it because a browser cannot set
headers on a WebSocket. The listener answers anything else first with `-32600` and closes the socket.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": 1,
    "key": "<browser.token>",
    "browser": { "id": "6f1c0a9e-2d4b-4b8e-9a51-3c7e0f2d9b10", "name": "Chrome - laptop", "version": "0.1.0" },
    "actions": ["open_tab", "close_tab", "release", "snapshot", "screenshot", "navigate", "click", "type", "press", "hover", "scroll", "drag", "upload_file", "download_file"]
  }
}
```

- `browser.id` is stable for one installation: 8 to 80 characters of letters, digits and `-`. A second connection
  with the same id replaces the first one, whose requests still waiting fail as an unknown outcome;
- `browser.name` (1 to 100 characters) tells browsers apart when several are connected;
- `actions` lists what the extension implements. Coddy sends no other action.

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
| `-32002` | Another protocol version; `data.supported` lists the versions the listener speaks. |
| `-32600` | The first message was not `initialize`. |
| `-32602` | Invalid parameters, a malformed browser id or name among them. |

After an error the listener closes the socket. Right after the answer, the extension sends `browser/tabs`, an empty
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
  `sessionId` belongs to that chat, and the listener refuses another chat's command for it before sending it;
- at most 100 tabs, `http` and `https` pages only, a URL of at most 8192 characters and a title of at most 1000. A
  list that breaks these rules ends the connection;
- `grant` names the current selection. The extension replaces it when the user presses Stop, and refuses a command
  that carries an older one. An empty `grant` keeps the previous one;
- the extension sends `browser/tabs` before it answers a command that changed the list, so the answer to `open_tab`
  never names a tab Coddy does not know yet.

In Coddy for Browser the list is the content of its Chrome tab groups: the shared **Coddy** group, and one group per
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

| Action | Parameters | Result |
| --- | --- | --- |
| `open_tab` | `url` | `tabId`, `url`, `title` of the new tab, once it loaded; the tab starts in the chat's group |
| `close_tab` | - | `closed` |
| `release` | - | `released`: how many of the chat's tabs went back to the shared group |
| `snapshot` | - | `url`, `title`, `text` (up to 20000 characters), `elements` (up to 500: `ref`, `tag`, `name`, `x`, `y`, and `role`, `type`, `href`, `value`, `checked`, `disabled`, `offscreen` when they apply), `viewport` |
| `screenshot` | - | `dataUrl` (`data:image/png;base64,...`, one image pixel per CSS pixel), `width`, `height` |
| `navigate` | `url`, or `back` / `forward` | `url`, `title` after the load |
| `click` | `ref`, or `x` and `y`; `button` (`left`, `right`, `middle`), `count` (1 to 3) | `ok` |
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
- an action fails with an error answer, code `-32000`, a message the chat reads and a `data.reason` among
  `not_shared`, `other_chat`, `stopped`, `site_declined`, `site_prompt_timeout`, `page_changed`, `cancelled`,
  `unsupported`, `invalid_params`, `too_large`, `transfer_failed`, `download_failed`.

Coddy waits 2 minutes for an answer, 10 for `download_file`. When a chat's turn is cancelled or the wait runs out,
Coddy sends `$/cancelRequest` and reports to the chat that the outcome is unknown; it never repeats an action by
itself.

```json
{ "jsonrpc": "2.0", "method": "$/cancelRequest", "params": { "id": "9a1f..." } }
```

## Website approval

Before the first action on a website (scheme, host and port), the extension asks the user in a window of its own:
allow for this chat, always allow, or decline. `open_tab`, `navigate` and `download_file` ask about the site they go
to. A question unanswered for 110 s fails the command with `site_prompt_timeout`, a refusal with `site_declined`. The
question never reaches Coddy: the user's answer stays with the browser.

## Files

`upload_file` and `download_file` carry no file content on the socket. Coddy opens a one-time ticket for the length
of the call and sends its id as `transfer_id`; the extension moves the bytes over HTTP on the same listener, with the
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
| `$/ping` | extension to Coddy | - | Keeps the socket and Chrome's service worker alive. |
| `browser/turnEnded` | Coddy to the extension | `sessionId` | The chat's turn ended; the extension takes its working marks off. Coddy sends it only to a browser holding a tab of that chat. |
| `$/cancelRequest` | Coddy to the extension | `id` | Stop the command with that id. |

A side ignores a notification it does not know, and answers a request it does not know with `-32601`, so either side
can be upgraded first within one protocol version.
