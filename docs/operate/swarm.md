# Swarm: relays, nodes, and one list of everything

https://github.com/user-attachments/assets/fd4837ae-23d0-400e-8e60-52706755bb4b

*A four-minute recording of a relay, two nodes and the aggregated session list, in the console and in the browser. The file is in the repository as [swarm.mp4](../assets/video/swarm.mp4).*

A **swarm** is a set of coddy nodes reached through one or more **relays**. A relay is a
stateless meeting point: nodes register into it, it carries requests to them, and it merges
their session lists into one. It stores nothing of its own, so what it reports is only ever a
live view of what the nodes told it.

Built with `-tags swarm`, which is part of the shipped set (`FULL_TAGS` in the **Makefile**), so
the release binaries, the Docker image, the Linux packages and the Homebrew formula all carry the
relay. Build it yourself with `make build TAGS="http ui scheduler memory cli gateway swarm"`.

The relay is not a command of its own: `coddy serve` runs it when `swarm.enable` is true, next to
whatever else the configuration enables. A binary built without the tag refuses `swarm.enable` at
startup and names the tag; the agent-side join hook is a stub that starts no goroutine and opens no
connection, and the binary otherwise behaves exactly as it did before the feature existed.

## Why

Running `coddy serve` on several machines already works, and `--remote` already drives one of
them. What it does not give you is one place to see everything, or a way in when the machine
you want is somewhere you cannot dial.

A relay answers both. It knows every node that joined it, so a client attached to it sees all
their sessions at once, labelled by owner. And it can be joined by a node that has no inbound
route at all, because that node opens the connection itself.

## The shape of it

```
    client ──▶ relay "outer" ──┬──▶ relay "middle" ──▶ agent8
                               │                   └──▶ relay3 ──▶ agent7
                               └──▶ relay3 (shortcut) ──────────┘
```

Three things are worth noticing in that picture.

A relay is a node from its parent's point of view. `middle` joined `outer` exactly the way
`agent8` joined `middle`, which is all that chaining requires - no second mechanism, and no
hop that knows how deep the chain goes.

`relay3` is reachable two ways. That is a ring, and it is allowed: the swarm dedupes it by
identity and picks the shorter route.

`agent7` never opened a port. It dialled `relay3` and is driven back down that same
connection.

## Running one

```yaml
# a relay
swarm:
  enable: true
```

```bash
coddy serve --swarm-auth-token "$CLIENT_TOKEN" --swarm-pairing-token "$PAIRING_TOKEN"
```

A relay that should do nothing else adds `--http=false`. One that is also an ordinary agent leaves
the API on, and then serves both: its own sessions on `httpserver.port`, the fleet on `swarm.port`.

A node joins by listing the relay in its own configuration:

```yaml
swarm:
  join:
    - url: "https://relay.example"
      name: "nas02"                        # becomes a URL path segment
      pairing_token: "${CODDY_SWARM_PAIRING_TOKEN}"
      advertise_url: "https://nas02:12345" # omit this to dial out instead
      token: "${NODE_OWN_TOKEN}"           # what the relay presents when it proxies
```

`swarm.join` is honoured by every `coddy serve` process, whether or not it runs a relay of its
own. That symmetry is how relays chain.

## Two transports

**Direct** — the relay dials the node's `advertise_url`. Use it when the relay can reach the
node.

**Tunnel** — leave `advertise_url` out. The node dials the relay, the relay takes over that
connection, and from then on the relay sends requests down it while the node answers them.
This is the only way in when a network accepts no inbound connections.

The tunnel is prior-knowledge HTTP/2 over a connection that started as an ordinary HTTP
request, so there is no bespoke frame protocol and no new dependency. Everything above the
transport composes plain HTTP and never learns which one is in play.

It does need an **end-to-end raw connection**. An intermediary that re-frames requests - a
layer-7 proxy, or an HTTP/2-only terminator in front of the relay - leaves nothing to take
over, and the handshake fails with an error saying exactly that. The direct transport is
unaffected.

## Reaching a node

Every node is mounted under the relay:

```
GET https://relay.example/swarm/nodes/nas02/coddy/sessions
```

Because that is a plain base URL with a path, **today's client works unchanged**:

```bash
coddy --remote https://relay.example/swarm/nodes/nas02 --remote-token "$CLIENT_TOKEN"
coddy acp --remote https://relay.example/swarm/nodes/nas02 --remote-token "$CLIENT_TOKEN"
```

Chains compose by writing the path out:

```
/swarm/nodes/middle/swarm/nodes/relay3/swarm/nodes/agent7/v1/models
```

Each hop strips its own prefix and forwards the rest.

### What a mount will and will not carry

It carries `/v1/*`, `/coddy/*`, the read-only swarm routes, and further `/swarm/nodes/*` hops.
That is a **prefix** allowlist, not a route list, so a node's new routes work the day they
ship.

It refuses `POST /swarm/register`, `POST /swarm/tunnel`, and `DELETE /swarm/nodes/{node}` -
that last one by method, since reading a child relay's node list is ordinary and evicting
from it is not. The proxy authenticates to the node on the caller's behalf, so anything
reachable through a mount is something the relay authorises for them; membership decisions
are not that.

A hand-written path is capped at the same hop budget the fan-out uses, so a client cannot
walk a ring indefinitely by writing hops out one after another.

## The aggregated list

```
GET /swarm/sessions?q=&node=&limit=&include_activity=
```

Every node is asked, in parallel and under a deadline, and the answers are merged newest
first. A child relay is asked for its own aggregate rather than its node list, so the merge
recurses through a chain while each relay still talks only to its direct children.

Each row carries **both** an identity and a route:

- `agent_uuid` + `id` - the agent that owns the session and the id that agent knows it by.
  This pair survives a rename, a failover, or a relay restart.
- `node_path` - how this relay could reach it just now. In a ring there may be several, and
  the shortest is the one reported.

Session ids are chosen per node and **do** collide, so nothing keyed on a bare id is safe.

A node that is slow or gone becomes an entry in `warnings` beside the results rather than an
error instead of them: one unreachable machine should not blank a fleet-wide list.

**Search** covers the work and the machine. A term matching a node's name or address returns
everything that node holds; otherwise the term is pushed down to each node, which matches it
against the session title, the first user message, and the working directory.

## Rings and routes

`GET /swarm/topology` returns the nodes, the edges between them, and a route per node.

Routes come from a breadth-first walk, which visits by increasing hop count - so the first
route found is a shortest one, and a node already seen is never expanded again, which is also
why a ring terminates instead of spinning. Ties break lexicographically, so two clients asking
the same relay are told the same route. A route never repeats a hop, so it cannot lap a cycle
on the way.

`alternates` are the other edges the walk saw arriving **at that node**, of any length - that
is where a client fails over when the short hop dies. They are not routes inherited from
another way into some ancestor: past a merge point only the shortest way through it is carried
forward, so a node behind a diamond has one route rather than two. Enumerating every k-shortest
path would fill the list with speculation; a failover list is more useful correct and short.

Per **request**, a relay appends its own id to an internal header and refuses a request that
already carries it, so a walk cannot go round forever. Depth is capped the same way for a
hand-written mount path.

A branch that closes back on the walk answers `looped: true` and contributes nothing, rather
than raising a warning. A chain that is merely **too long** is a different answer: something
is out there and cannot be reached, so that one does warn. In a ring that is the ordinary end of a branch, and a warning on
every request in a healthy swarm is how an operator learns to ignore warnings. What does
warrant one is a node the relay still knows but cannot reach - including a lease that went
stale, which would otherwise vanish silently and make "this machine is down" look exactly
like "this machine has no work".

## The UI

![The topology graph with a live relay](../assets/swarm/map-live-dark-1280.png)

*The topology graph with a live relay*

![A mounted node and its sessions](../assets/swarm/node-dark-1280.png)

*A mounted node and its sessions*

![A permission prompt relayed through two relays](../assets/swarm/permission-two-relays-dark-1280.png)

*A permission prompt relayed through two relays*

![A relay that needs a token before it lists anything](../assets/swarm/relay-needs-token-dark-1280.png)

*A relay that needs a token before it lists anything*

Connecting is the ordinary environment flow: the chip in the composer, **Add remote**, the
relay's address and its client token. Because the environment answers as a relay, a **Swarm**
entry appears in the rail - on a plain agent it is not there at all.

The Swarm screen shows the topology, a search box that goes to the relay, node filter chips,
and sessions grouped under the node that owns them.

**A relay's home screen is the swarm.** A relay serves no `/coddy/*` at all - no sessions, no
workspace, no model - so there is nothing for a composer to send to and nothing for a history
drawer to list. Pointed at a relay the app therefore drops the chat screen, hides History and
Scheduler in the rail, and shows the map instead. The environment selector moves into the
map's header, since the composer that usually carries it is not on screen. Enter a node and
all of it comes back, because the node does have those things.

**Working on a node.** Click a node on the map and the app points at that node's mount. From
there every screen that already existed drives it - the history drawer lists that node's
sessions, the composer shows its working directory and its model catalog - with a relay in the
middle and nothing aware of it. Coming back to the map, that node is marked *you are here* and
the route to it is drawn as one connected path.

**Watching from the map.** Each node says what it is doing, from the same aggregated session
list the search uses: how many sessions it holds, how many turns are in flight, and whether
something there is waiting on a permission prompt. Start work on several machines, come back to
the map, and it says which of them finished and which is asking you a question. Clicking the one
that is asking opens that very session rather than a blank chat.

**Going back and switching.** The Swarm entry stays in the rail while you are inside a node,
because the relay you came through is remembered; clicking it returns to the swarm, where
another node is one click away. Without that memory there would be no way back but to type
the relay's address again: from inside a node, the relay's own routes are no longer under the
base URL.

**Where the SPA comes from.** Built with `-tags "swarm ui"` the relay serves the console at its
own address, so a relay is something you open in a browser. It is the same bundle a node
serves, and it treats a relay as one more environment: opened same-origin on a relay that
requires a token, it will say so and ask for one rather than reporting an empty swarm. Built
without the `ui` tag the relay's root explains how to rebuild, and the console can still be
opened from any node and pointed at the relay.

## Where the settings live

| What | Where |
|---|---|
| Whether this process relays at all | `swarm.enable` in `config.yaml`, or `--swarm` / `--swarm=false` |
| Relay's own deployment: bind address, client and pairing tokens, TLS, static upstreams | `swarm:` in `config.yaml`, or the `--swarm-*` flags |
| Which relays this process joins | `swarm.join` in `config.yaml` - honoured whether or not this process relays |
| Relays offered in the UI environment menu | `httpserver.remotes` (name and URL only; tokens stay in the browser) |
| Credentials out of the file | `CODDY_SWARM_TOKEN`, `CODDY_SWARM_PAIRING_TOKEN`, `--auth-token`, `--pairing-token` |

There is deliberately **no Settings page for the relay**. It is a deployment - a bind address
and credentials for a whole fleet - and a page served by one of its own nodes is the wrong
place to edit that. Node credentials are never returned by a config read, and a save preserves
them by destination, so editing anything else in Settings cannot strip them.

## Security

Three credentials, three jobs:

| Credential | Who holds it | What it authorises |
|---|---|---|
| `swarm.auth_token` | clients | using this relay |
| `swarm.pairing_tokens` | nodes | joining this relay |
| per-node `token` | the relay | acting as that node |

**A relay is a fleet-wide door.** It holds every node's credential, so whoever holds its
client token controls every node it reaches, transitively through every hop. This is stated
rather than mitigated: there are no per-node client ACLs in this version. Give each node a
credential minted for its relay rather than your own, put TLS in front, and keep the pairing
token secret.

Binding off loopback without a client token **refuses to start** (`swarm.allow_insecure`
overrides). On loopback one is generated for the run rather than left absent, because an open
relay lends its authority to every local process.

A node's name is proven by a **per-lease secret** the relay mints, not by the shared pairing
token: a fleet credential must not let one node claim another's name, redirect its traffic, or
read the prompts meant for it. The secret is stored under the coddy home so a restart reclaims
the same name at once. `DELETE /swarm/nodes/{node}` is the administrative takeover path.

An advertised URL is an **SSRF boundary**. Only `http(s)`, with no credentials, query or
fragment; the host is then resolved and every address checked. Link-local, multicast,
unspecified and cloud metadata addresses are refused outright. Loopback is allowed only when
the relay itself is bound to loopback (the development case), and private ranges only when
`swarm.allow_private_upstreams` names hosts - which also allow-lists those names. The
addresses that passed are **pinned**, and the relay dials exactly them, because re-resolving
at dial time would reopen the window the check closed.

The proxy **replaces** the caller's `Authorization` rather than forwarding it, strips cookies,
hop-by-hop and forwarding headers, removes an SSE query token before the hop, and never
follows a redirect. Path segments are judged **after decoding**: `%2e%2e` passes any check of
the escaped form and becomes `..` the moment something decodes it, which is how a request
aimed at a node's API would climb back out into the relay's own routes.

Credentials are preserved across a config save by **destination**, not by label: renaming an
entry keeps its token, pointing it at a new address does not.

## Encryption and proxies

Relays usually sit in different networks.

```yaml
swarm:
  tls:
    cert_file: "/etc/coddy/relay.crt"
    key_file: "/etc/coddy/relay.key"
  join:
    - url: "https://parent.example"
      dial:
        proxy: "socks5://127.0.0.1:1080"   # http, https, socks5, socks5h
        ca_file: "/etc/coddy/internal-ca.pem"
```

Both files or neither; minimum TLS 1.2; certificates are startup state, so rotating them needs
a restart. `insecure_skip_verify` exists for a lab and is logged every time it is used.

Opening a tunnel through a proxy to a TLS relay composes: dial the proxy, `CONNECT` to the
relay, wrap in TLS, upgrade, invert roles. The HTTP/2 layer above is unaware of any of it.

## What is deliberately absent

- **No persistence.** The registry is memory; nodes make it true again by checking in. After a
  restart `/swarm/info` reports `registry_warming` so a client can tell "just started" from
  "nothing here".
- **No cross-node pagination.** Each node returns a first page and the merge truncates; deep
  history for one node uses that node's own pagination through its mount.
- **One tunnel is one connection.** A reconnect ends the streams that were running on the
  old one, and a bulk response shares flow control with a live turn. Both are inherent to a
  single HTTP/2 connection per node and are not worked around.
- **One relay process per endpoint.** Two replicas behind a load balancer would split the
  registry and the tunnels.
- **No per-node client authorisation.** See the blast radius above.

## Reference

- `coddy serve --help` - flags.
- `docs/reference/config.md` - the `swarm:` block.
- `features/swarm_*.feature` - the executable specifications.
- `examples/swarm/` - a live stand: relays, agents, a ring, and a node that only dials out.
- the tutorials: [a relay and its nodes in Docker](../tutorials/swarm-relay-and-nodes.md), [a chain of relays](../tutorials/swarm-multi-hop.md), [working with remote nodes](../tutorials/swarm-remote-nodes.md).
