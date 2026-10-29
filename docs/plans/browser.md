# Coddy for Browser: where the browser lives and who drives it

Design record of how the extension, the sessions of every surface and the swarm connect. Decided
after a design quorum of three models (Codex gpt-6.1, Devin swe-2, Qwen 3.8) on 2026-10-27, with the
operator's answers to the questions it left open.

## Requirements

- Two directions. The browser is a surface: the person chats with Coddy in the extension's side panel
  about the page they look at. And the browser is a capability: a chat on any surface - the console,
  an editor over ACP, the web UI, Telegram, a scheduled job, a subagent - drives a browser.
- Across machines: a session on one node of the swarm drives a browser on another machine.
- A module like the others: build tag `browser`, switched on in `config.yaml`.

## Decisions

### A browser has one home node

The extension connects to one `coddy serve`, its home node, normally the one on the same machine,
over the WebSocket it already opens: `GET /browser/v1/connect` on that node's HTTP server, JSON-RPC
2.0 both ways (`docs/reference/browser-protocol.md`). The socket is never stretched across the swarm:

- a relay mount carries no `/browser/v1/` (the prefix allowlist of `external/swarm/mount.go`), and
  the relay's `Rewrite` deletes `Upgrade`;
- a tunnelled node is reached over HTTP/2, where an Upgrade does not exist, and `x/net/websocket`
  needs a hijackable connection;
- the relay holding sockets would make a stateless relay keep browser state and the person's tabs.

A machine whose browser must be reachable from elsewhere runs `coddy serve` and joins the swarm with
`swarm.join`; the tunnel transport covers machines that accept no inbound connection. Driving a
browser on a machine with no Coddy at all is out of scope (operator's answer). If it is ever needed,
the same JSON-RPC can run over POST plus an SSE stream read with `fetch` (both pass a relay mount),
behind a relay credential narrower than its client token.

The endpoint is a route of the HTTP server rather than a listener of its own: the extension needs one
address, which is also the web UI's, and a tunnelled node serves only its HTTP handler.

### Other processes reach a browser through its home node's HTTP API

A session that does not run in the home node's process reaches its browsers through
`/coddy/browser/v1/`:

| Route | Does |
| --- | --- |
| `GET /coddy/browser/v1/browsers?node=&session_id=` | the connected browsers and their tabs as that caller's chat sees them |
| `POST /coddy/browser/v1/call` | one action: `{browser_id, action, tab_id, params, caller {node, session_id, title}, file?}`; the answer `{result, file?}` |

`/coddy/` is carried by every relay mount, so a session on node A reaches node B's browsers at
`<relay>/swarm/nodes/B/coddy/browser/v1/...` with the relay's client token, on both transports, and
a console or an editor on B's own machine reaches them at B's HTTP address. The routes sit behind the
node's ordinary auth gate and answer 404 unless the home node opts in with `browser.serve_remote`.

- **Who may call.** Whoever holds a credential of the node's API can already run turns on it and
  answer its permission prompts, through a relay mount too; the routes add convenience, not
  authority. The decision that belongs to the person at the browser is therefore taken in the
  extension (below).
- **Owners.** A remote chat owns tabs as `<node>/<session_id>`. A local session id never holds a `/`,
  so the two never collide. The node name is what the caller says about itself (its `swarm.join` name,
  else its host name): it is for display and consent, not authentication, since the relay forwards
  no caller identity.
- **Files.** `upload_file` and `download_file` carry the bytes inside the call, base64, 32 MiB at most:
  the transfer tickets of `/browser/v1/files/` live in the home node's memory and authenticate with the
  extension key, which a caller on another node does not have and must not get.
- **Cancellation.** The caller drops its request; the relay drops the hop; the home node's call
  context ends and its hub sends `$/cancelRequest`, as for a local turn. An action is never replayed.
- **End of a turn.** A remote chat's turn ends where the home node cannot see it; the "working" marks
  of a remote chat's tabs clear after a quiet interval instead (`remoteTurnIdle`).

### The browser tool finds browsers in three places

- **this process**, when it serves the extension endpoint (`coddy serve` with `browser.enable` and a
  token): the in-process hub;
- **the server of this machine**, when this process does not serve it (the console, `coddy acp`) and
  the shared configuration has `browser.serve_remote`: the HTTP API at the configured
  `httpserver.host`/`port` with `httpserver.auth_token`;
- **`browser.remotes`**: names of `httpserver.remotes` or server addresses, a relay mount included,
  resolved like `--remote` (`internal/remote.Resolve`), so a mount takes the relay entry's token.

A browser of a remote is named `<browser_id>@<remote>`; the selection the tool keeps in the session
bundle stores that name.

### The person at the browser decides

- The first action of a chat that came through the HTTP API asks the person in the extension, naming
  the chat and the node: allow this chat, or decline.
- "Always allow this website" covers chats of the home node's own process only; for a remote chat
  every new website asks again, allowed for that chat alone.
- The tab group of a remote chat is titled with the chat and its node.

### The side panel is the home node's web UI

The panel shows the web UI of its home node in a frame, signed in with a one-time ticket the socket
hands out (`panel/open`, `POST /coddy/auth/browser`), and before each turn the web UI asks the panel
for the tab the person writes from (`postMessage`, both origins checked) and sends it as
`metadata.browser`. One address serves both, and every composer feature of the web UI is in the panel
the day it ships. A native chat over the socket was the alternative: it would be a second composer
that lags the web UI forever (the operator first chose it, then took the frame once it was clear that
the frame needs no second port). The panel's own strip follows the web UI's theme, which the web UI
reports through the same bridge.

A chat of another node opened in the panel's web UI (an environment of `httpserver.remotes`) gets no
`metadata.browser`: the page's tab is in the home browser, and that node reaches it only through its
own `browser.remotes`.

## Known limits

- One `browser.token` for every browser of a node: a holder of the key can connect under another
  browser's id and take its commands. Per-browser keys issued by pairing would close it; the key
  already equals an API credential of the node (the panel ticket opens a web UI session), so the
  documentation says so plainly.
- A scheduled job or a background subagent that drives the browser while nobody watches waits for the
  extension's question like any other chat; an unanswered question declines after its timeout.
