# Coddy Swarm — design & work plan (v3)

Status: **implemented and committed on this branch.** Ten commits from `db0d39e` to
`73617cf`; `make test` green; the guide moved to `docs/swarm.md`, which is now the
maintained document. This file is kept as the record of how the design was reached and what
four rounds of cross-review changed.

Shipped: registry with per-lease name ownership, self-registration over proxies and TLS, the
per-node mount with control-plane isolation, aggregation with recursion through child relays,
the reverse HTTP/2 tunnel for closed contours, ring-aware topology with shortest-route
selection, and the SPA swarm screen. Verified live, not only in tests: a real turn and a
subagent run on a node two hops away with no inbound port, today's ACP client driving a node
through a mount unchanged, and a three-relay ring choosing the short way.

Deferred, and said so in `docs/swarm.md`: cross-node pagination, per-node client ACLs,
multi-replica relays, and per-session routing inside the SPA transcript view.

Original status line: **v3 out for cross-review, implementation starting on approved groups.**
v1→v2.1 history and all six review documents are in `swarm-review/`; v2.1 reached a
three-way consensus (Fable 5 APPROVE, Cursor APPROVE_WITH_CHANGES, Codex APPROVE).
v3 changes two things: the branch was **synced with `main`** (facts re-verified, §1.1), and
the operator added a **new first-class requirement — multi-hop relay chaining across
isolated network contours** (§0.1), which promotes federation out of "later" and introduces
a second transport (§3.3). Everything the three reviewers agreed on in v2.1 is preserved.

## 0. Operator's request

Relay ("swarm", рой) that multiple `coddy http` agents register into automatically; clients
(SPA, console, mobile app) connect to the relay; all sessions from all hosts visible
centrally with host labels, host filter, and free-text search (node name, address, task);
connecting to a relay feels like `--remote`; the relay stores nothing; relays federate into
higher-level relays; UI gets host grouping and a topology graph; e2e tests for everything.
Monolith with build tags, `external/…` package + launch command.

### 0.1 New requirement — multi-hop chaining across contours

> Подключиться к любому релею и использовать его как ретранслятор до агентов, подключённых к
> нему: зашёл на релей 1 → следующим хопом релей 2 → релей 3 → и там уже агент, и управляем
> им. Агенты могут сидеть в разных контурах.

Two consequences:

1. **Chaining is a product feature, not an M2 nicety.** A client attached to relay 1 must be
   able to drive an agent that only relay 3 can see, with relay 2 in between.
2. **"Different contours" means the inner side is often unreachable from the outer side.**
   The v2.1 model (relay dials the node's `advertise_url`) covers only reachable nodes. A
   node — agent *or* relay — sitting behind NAT or a one-way firewall can still make
   **outbound** connections, so the swarm needs a transport where the node dials out and the
   relay reuses that same connection to drive it.

### 0.2 The transport hypothesis, tested before planning around it

Because every swarm surface is plain HTTP, an outbound-dialed node does not need a bespoke
frame protocol (the design deferred in `docs/remote-control.md` §4). It needs one thing: an
`http.RoundTripper` pointing the wrong way down a connection the node opened. Go gives that
for free — `golang.org/x/net/http2` is **already a direct dependency** (`go.mod`), and its
`Server.ServeConn` / `Transport.NewClientConn` pair speaks prior-knowledge HTTP/2 over any
`net.Conn`.

A spike (`swarm-review/spike-reverse-http2/`) proved it end to end:

| Property | Result |
|---|---|
| Node dials out, relay hijacks, roles invert | works |
| Unary request, headers preserved (`Authorization`) | works |
| SSE streaming, no buffering | first chunk in **10.8 µs** |
| Concurrent multiplexing on one conn | **8 parallel requests**, no crossing |
| New dependencies | **none** |

So the mount, aggregation, permission round-trips, and federation designs are all transport
agnostic: they compose HTTP requests and hand them to a `RoundTripper` the registry owns.

## 1. Existing surfaces (facts)

### 1.1 Re-verified after the `main` merge (5fadd23)

Everything v2.1 depended on still holds:

- `external/httpserver/listen.go` is still a bare `http.ListenAndServe` — the hardening in
  `docs/remote-control.md` §3.4 remains **unimplemented**, swarm must introduce it.
- `cors.go:20` `Allow-Headers` is still `Authorization, Content-Type, X-Coddy-Session-ID` —
  still missing `Last-Event-ID`.
- `FileStore.FilterSnapshotListForSearch` (`internal/session/filesystem.go:456`) still
  matches **title or first user message only, not cwd**.
- `configSecretPath` (`internal/config/path.go:512`) matches `auth_token`/`token`/`secret`
  suffixes — `pairing_tokens` would **not** be redacted without an explicit addition.
- `internal/remote.Resolve` still preserves a path-bearing base URL; the SPA fetch shim
  (`remoteEnv.ts:158`) still rewrites only `/v1/`, `/coddy/`, `/openapi` — `/swarm/` must be
  added.

New since the branch forked, and relevant:

- **Subagents shipped** (`internal/subagents`, `internal/session/subagent.go`, tool
  `spawn_agent`, UI agent cards). `GET /coddy/sessions` gained `include_subagents`, rows can
  carry a `subagent` link, and subagent runs are hidden from the list by default
  (`ListOptions.IncludeSubagents`). Swarm aggregation must pass this parameter through and
  preserve the field — and it is exactly the mechanism for the operator's "покажи на пачке
  сабагентов" demo (§8).
- New routes registered: `GET /coddy/workspace/file`, `registerSubagentRoutes`,
  `registerHookRoutes`. The mount is path-agnostic, so they proxy for free; the point is that
  **the mount must never carry a route allowlist** or it will rot on every new route.
- `internal/remote` grew `usage.go` (`GET /coddy/providers/{name}/usage`) — one more reason
  the console's swarm mode must route per node rather than per process.

### 1.2 Unchanged load-bearing facts

- Turn = `POST /v1/responses` SSE over `fetch`; the only `EventSource` routes are
  `GET /coddy/sessions/{id}/composer-stream` and `GET /coddy/events`, which are also the only
  routes accepting `?access_token=`.
- Session ids are folder-safe `^[A-Za-z0-9_-]{1,256}$` and operator-choosable
  (`--session-id`) ⇒ **cross-node collisions are realistic**.
- The SPA supports parallel background turns across sessions; its env is one global
  `baseUrl` + fetch shim; the session hash route is `#/s/{id}`; `<img>` thumbnails bypass the
  shim.
- BDD lives in repo-root `features/`, godog harnesses next to the owning package, stub runner,
  happy paths only; live e2e harnesses are python under `examples/`.

## 2. Milestones

The v2.1 two-milestone split survives, with federation and the tunnel moved forward because
§0.1 made them load-bearing. Work proceeds in **groups**, each a full `/rpa-feat` cycle
(failing spec → implementation → `make test` → `make lint` → commit).

| Group | Content | Enables |
|---|---|---|
| G1 | hardened listener, `swarm:` config, registry with leases, `coddy swarm`, `/swarm/info`, `/swarm/nodes` | anything |
| G2 | outbound join from a node (register + heartbeat + lease secret), TLS and proxy legs | auto-registration |
| G3 | per-node mount `/swarm/nodes/{node}/…`, **direct** transport, control-plane isolation | drive one node through a relay |
| G4 | `/swarm/sessions` aggregation, node labels, warnings | one pane of glass |
| G5 | search across nodes (node name, title, cwd) | finding work |
| G6 | **multi-hop**: relay-in-relay, chaining, peer channel, loop guard | §0.1 |
| G7 | **tunnel** transport (reverse HTTP/2, dial-out nodes) | closed contours |
| G8 | topology graph, ring discovery, shortest-route BFS, `/swarm/topology` | rings |
| G9 | console/ACP swarm mode | `--remote <relay>` |
| G10 | SPA transport routing (`apiPathFor`, composite identity) | correctness |
| G11 | SPA swarm UI: node groups, filter, search, badges, node picker | UI |
| G12 | SPA topology view | graph |
| G13 | live e2e stack, screenshots, video, subagent demo across relays | proof |

Both round-4 reviewers judged the original nine groups too coarse to review safely, so
aggregation is split from search, the SPA work is split into transport, list, and graph, and
a minimal multi-hop slice lands before the tunnel rather than after it — the tunnel is the
highest production risk and benefits from arriving once routing is already proven.

Non-goals unchanged: no relay-side persistence, no session moves, no load balancing, no
OAuth, no per-node client ACLs (the blast radius of one relay token is documented, §6),
no aggregated "create session anywhere".

## 3. Architecture

### 3.1 Placement, naming, tags

- **Swarm everywhere**: `external/swarm` (relay server, tag `swarm`), `internal/swarm`
  (**untagged** shared package: DTOs, node-name validation, join client, tunnel dialer —
  so `external/httpserver` under tag `http` may import it without dragging in the tag),
  command `coddy swarm`, config block `swarm:`, API prefix `/swarm/`, docs `docs/swarm.md`.
  "Relay" stays a role in prose only — the repo already has two other "relay"s
  (the deferred WS hub; the in-process `composerStreamRelay`).
- **The whole feature switches off with its build tag**, the way `ui` and `cli` do, and a
  binary built without it is exactly today's binary:
  - `coddy swarm` exists only under the tag; without it the command prints the same
    "built without support" message the other optional commands use (`cmd/coddy/swarm.go`
    plus `swarm_stub.go`);
  - the agent-side join loop lives behind `//go:build http && swarm` with an `http && !swarm`
    stub that registers nothing, so a plain `http` build starts no goroutine, opens no
    connection, and serves no swarm route;
  - `internal/swarm` stays untagged but is deliberately inert — pure types, validation and a
    client that only runs when something calls it — so it costs a tagless build nothing but
    a few kilobytes of unreferenced code;
  - the SPA's swarm mode is dead code unless a swarm environment is selected, and the config
    block simply sits unused, exactly like `gateways:` in a binary built without gateways.
  - a CI row builds and tests **without** the tag to keep that promise honest.
- Matrix rows land with the group that introduces them: `swarm` (G1), `http,swarm` (G2 — the
  BDD harness boots in-process `coddy http` agents), `cli,http,swarm` (G7).

### 3.2 Node identity, registry, leases

- `node_id == name`, validated `^[A-Za-z0-9_-]{1,64}$`, reserved words rejected — the name is
  a routing path segment, so it must never contain `/`, `..`, or an encoded separator.
- **Name ownership is proven by a per-lease secret, not the shared pairing token.** First
  registration of a name mints a relay-side `lease_secret`; renewals and replacements must
  present it while the lease is alive (a valid pairing token alone gets 409 — a fleet
  credential must not let one node steal another's name). The join client persists the secret
  under `CODDY_HOME` per relay, so a restart re-attaches instantly. Expiry + grace frees the
  name; authenticated `DELETE /swarm/nodes/{node}` is the admin takeover path.
- Registration document: `{name, kind: agent|relay, transport: direct|tunnel, advertise_url?,
  instance_uuid, token?, version, labels}`. `instance_uuid` is per process and is what
  federation dedupes on (§3.6) — it ships from G1 so nothing reshapes later.
- Liveness by transport: **direct** = heartbeat every 30 s (±20 % jitter), TTL 90 s, one TTL
  of offline grace; **tunnel** = the connection itself, plus HTTP/2 pings — a dropped conn
  marks the node offline immediately.
- In-memory only. `/swarm/info` reports `started_at` + `registry_warming` so a client can
  distinguish "relay just restarted" from "nothing registered". The deliberate divergence
  from `docs/remote-control.md` §4.4 (which recommended persisting known agents) is documented
  with its cost; optional `agents.json` stays a follow-up.
- `advertise_url` (direct transport only) is an **SSRF boundary**: `http(s)` only, no
  userinfo/query/fragment, no loopback/link-local/metadata targets unless allow-listed in
  `swarm.allow_private_upstreams` (loopback permitted by default only when the relay itself
  binds loopback — the dev case), resolved-IP pinning against DNS rebinding, redirects never
  followed, registration body capped at 64 KiB.

### 3.3 Two transports behind one seam

The registry hands the proxy a `NodeTransport` — nothing above it knows which is in play:

```go
type NodeTransport interface {
    RoundTripper() http.RoundTripper // how to reach the node
    TargetURL() *url.URL             // scheme+host used to rewrite the outbound request
    Alive() bool
}
```

- **direct** — `advertise_url` + a pooled `http.Transport`. For nodes the relay can reach.
- **tunnel** — the node dials out; the relay hijacks the connection and wraps it with
  `http2.Transport.NewClientConn`; the node serves its own handler over the same conn with
  `http2.Server.ServeConn`. `TargetURL()` is a synthetic `http://<node>.swarm.invalid` (the
  authority is never resolved — the RoundTripper is already bound to the conn).

Handshake, two plain steps so both transports share one registration path:

1. `POST /swarm/register` (pairing token) → `{node_id, lease_secret, ttl}` over ordinary HTTP.
2. `POST /swarm/tunnel` with the lease secret → relay validates, replies `200`, hijacks, and
   both sides switch to prior-knowledge HTTP/2 with roles inverted.

Lifecycle details the spike does **not** prove and which therefore have to be specified and
tested rather than assumed (round-4, both reviewers):

- **Buffered bytes at the hijack.** `Hijack()` hands back a `bufio.ReadWriter` that may
  already hold bytes the peer sent after its request. Those bytes are the start of the HTTP/2
  preface, and dropping them corrupts the connection. The relay must splice anything already
  buffered ahead of the raw conn. The spike is silent on this because its client waited for
  the response before speaking; a real client will not. **This is a known bug to fix, not a
  detail to remember.**
- **Reconnect.** Exponential backoff with jitter, so a relay restart does not draw a
  synchronised reconnect storm from every node at once; the relay may answer `Retry-After`
  and bound admissions.
- **Connection replacement.** A new connection is accepted only after authentication, then
  swapped atomically with a generation bump; the old one is closed rather than left
  half-open. In-flight streams on the old connection fail loudly.
- **No transparent replay.** A request whose response never arrived is reported as
  indeterminate, never re-sent: replaying a partially transmitted turn or a permission answer
  would duplicate an irreversible action.
- **Liveness.** HTTP/2 PING with a bounded round-trip plus a read-idle timeout detects a
  half-open connection instead of waiting for TCP to notice.
- **One connection is one failure and flow-control domain.** Explicit
  `MaxConcurrentStreams` and window sizes, and a test proving a bulk response does not starve
  a live SSE stream on the same tunnel.
- **Deployment constraint.** The upgrade needs an end-to-end raw connection (see §3.6.2).

Reconnect leaves the lease intact; the registry swaps the transport in place, so a client
sees a short failure rather than a vanished node.

A node chooses its transport by config: `advertise_url` set ⇒ direct; omitted ⇒ tunnel.

### 3.4 The mount (per-node proxy)

`/swarm/nodes/{node}/…` reverse-proxies a node under a **prefix allowlist**, not a route
allowlist: `/v1/*`, `/coddy/*`, the read-only swarm routes (`/swarm/info`, `/swarm/nodes`,
`/swarm/sessions`, `/swarm/topology`), and nested `/swarm/nodes/*` for chaining. New node
routes under those prefixes work the day they ship; the **control plane never proxies**.

Control-plane isolation is the first thing both round-4 reviewers demanded, and for the same
reason: the proxy replaces the caller's credential with the node's own, so any route reachable
through a mount is a route the relay authorises on the caller's behalf. `POST /swarm/register`,
`POST /swarm/tunnel`, and `DELETE /swarm/nodes/{node}` are therefore refused at the mount —
otherwise a client could register a node, or evict one, inside a relay it merely reads from.

Contract:

- unknown or offline `{node}` ⇒ a structured JSON error naming **the failing hop**, its lease
  state, and the remaining path, so a client three hops away learns which node broke rather
  than reading an opaque 502;
- **`Authorization` is replaced, never forwarded**; cookies, hop-by-hop headers, inbound
  `Forwarded` / `X-Forwarded-*` / `X-Real-ID` and any inbound `X-Coddy-Swarm-*` are stripped;
  `Last-Event-ID`, `X-Coddy-Session-ID`, `Accept`, `Content-Type` pass through;
- relay auth accepts `?access_token=` on mounted SSE GET patterns (EventSource cannot set
  headers) and **strips the parameter** before proxying, so the relay's client token never
  lands in node logs;
- **no wall-clock deadline on a proxied response.** Streaming routes flush immediately, the
  relay sets no `WriteTimeout` and no client `Timeout`, and cancellation propagates from the
  client down every hop. A turn or a permission prompt that blocks for minutes is the normal
  case, not the exception — this is the single most likely way to ship a broken relay;
- **failures split at the header boundary**: before headers, a hop failure becomes a
  structured error response; after headers, the stream is already committed, so the relay
  closes it and the outcome is reported as indeterminate. Non-idempotent requests are never
  transparently retried — a duplicated turn or a duplicated permission answer is worse than a
  visible failure;
- **path handling is canonical, not textual**: the mount matches on decoded segments but
  forwards `RawPath`, rejects encoded separators and dot segments before routing, preserves
  the target's own base path and query, and rewrites a same-target `Location` back under the
  mount rather than following it;
- **relay CORS is its own config** (`swarm.cors`) because the SPA is cross-origin to the
  relay by construction, and its `Allow-Headers` **includes `Last-Event-ID`** (`coddy http`'s
  list gains it in the same group) or composer-stream reattach fails preflight; the relay
  owns the CORS headers rather than passing a node's through;
- request and response body caps on non-streaming routes; per-node concurrency limits.

Compatibility guarantee: `--remote https://relay/swarm/nodes/nas02` drives one node with
**today's** client, unchanged — `Resolve` already keeps a path-bearing base. Aggregated UX is
an explicit client mode on top (§3.7, §3.8), never a mutation of the global base.

### 3.5 Aggregation

`GET /swarm/sessions?node=&q=&limit=&include_activity=&include_scheduler=&include_subagents=`

- fan-out to online children with bounded concurrency and a per-node deadline (3 s), under a
  **decreasing end-to-end budget** carried down the chain, so a deep topology cannot multiply
  one client's request into an unbounded wave of work; visited relays and total nodes are
  capped, and nested warnings are prefixed with the hop that produced them;
- **each child is asked for at least the caller's `limit`**, otherwise a top-N merge over
  short pages silently drops rows that belonged in the answer;
- **no cross-node cursor in v1**: each node returns its first page, the relay merges by
  `updatedAt` desc (tie-break node name, then id), truncates to `limit`, and reports per-node
  `hasMore` plus a `warnings` array for nodes that failed or timed out — partial results beat
  a 502. Deep history for one node is the mount's own native pagination (drill-down);
- rows gain `agent_uuid` (identity), `node_path` and `alternate_paths` (routes), `nodeName`,
  `nodeUrl`, `kind`, and carry through `subagent`; every returned path segment is validated
  before it is trusted as a route;
- **search**: a node whose *name or URL* matches `q` is queried **without** `q` (all its
  sessions match by node); other nodes get `q` pushed down. To make "search by task" cover
  cwd honestly, the **node-side matcher is extended to cwd** in the same group (one predicate
  in `FilterSnapshotListForSearch` + unit test). The relay itself never matches row contents.

Activity: swarm mode never subscribes `/coddy/events` (it does not exist at a relay root);
badges come from `include_activity` polling. `/swarm/events` fan-in stays a follow-up.

### 3.6 Multi-hop chaining (the §0.1 requirement)

A relay joins a parent exactly like an agent, with `kind: relay` and either transport. Then:

- **Routing composes syntactically.** `relay1/swarm/nodes/relay2/swarm/nodes/relay3/swarm/nodes/agent7/coddy/sessions`
  — each hop strips its own prefix and proxies the rest. No hop needs to know the topology
  beyond its direct children, and no hop rewrites bodies.
- **Aggregation recurses.** For a child of kind `relay`, the parent calls the child's
  `/swarm/sessions` (not its node list) and prefixes the child's name onto every returned
  `node_path`; for a child of kind `agent`, it calls `/coddy/sessions`. So one relay only ever
  talks to its direct children, at any depth.
- **Contours work in both directions.** An inner relay that cannot be reached dials out
  (tunnel transport); an outer relay that is reachable is dialed. A chain may mix them.
- **Identity and route are different things** (round-4 Codex): a session's identity is
  `(terminal agent instance_uuid, session_id)` — stable across renames, failover and relay
  restarts — while `node_path` is merely *a* route to it, which can change or be one of
  several. Rows carry both; clients cache by identity and re-resolve the route.
- **Loop and amplification guard on every hop** — mount, aggregation, and topology alike:
  each relay appends its `instance_uuid` to an internal `X-Coddy-Swarm-Path` header. The
  header is **accepted only on peer-authenticated traffic** (a request arriving with a
  child's own peer credential), and **stripped from every client request**, which is what
  makes it both unforgeable and preservable — a distinction the first draft got wrong, since
  a header cannot be simultaneously trusted and discarded unless the two sources are
  distinguishable. Size-bounded, depth capped, own-uuid ⇒ refuse rather than loop.

#### 3.6.1 Rings, and choosing the shortest route

Relays are not required to form a tree. Three relays may each join the other two, or a chain
may be closed into a ring for redundancy, and then a node is reachable by more than one
route — possibly by a very long one.

- **Topology discovery tolerates cycles.** Each relay walks its children keeping a visited
  set keyed by `instance_uuid`, so a ring is traversed once and reported as a graph
  (`nodes` + `edges`), never as an infinite tree. This is discovery, where a cycle is normal
  and expected — distinct from the per-request guard above, where a cycle is a fault.
- **Routes come from a breadth-first search over that graph.** Because BFS visits by
  increasing hop count, the first route it finds to a node is a shortest one. The relay
  publishes, per node, the shortest route as `node_path` plus any equal-or-longer
  `alternate_paths`, so a client that finds one hop down can fail over without re-deriving
  the topology itself.
- **Ties break deterministically** (lexicographically by the hop names), so two clients
  asking the same relay get the same route and a cached route stays valid.
- **Aggregation dedupes by identity, keeps the shortest route.** A ring delivers the same
  agent through several children; rows collapse on `(agent instance_uuid, session_id)` and
  retain the shortest `node_path`, with the alternates attached rather than discarded.
- Route computation is a pure function over the discovered graph, so it is unit-tested
  directly with ring, diamond, and disconnected fixtures instead of through live relays.

`GET /swarm/topology` returns that graph — `nodes` (`{name, kind, uuid, online, transport}`),
`edges` (`{from_uuid, to_uuid}`), and `routes` (`{uuid: {path, alternates}}`) — built from
live children under the same deadline discipline, so the UI can draw the operator's graph and
the console can resolve a route without guessing.

### 3.6.2 Encryption and proxies between relays

Relays are expected to sit in different networks, so the link between them is rarely a bare
LAN socket. Both directions have to survive TLS and an intervening proxy.

- **Serving TLS.** `swarm.tls.{cert_file,key_file}` makes the relay serve HTTPS; either both
  or neither, one alone is a startup error. Minimum TLS 1.2. Certificates are startup state
  and a rotation needs a restart in v1. A relay behind somebody else's terminator sets
  nothing and stays plain, with the same insecure-bind rules as everywhere else.
- **Dialling TLS.** A node joining an `https://` relay verifies the chain normally; a
  private CA goes in `ca_file`, and `insecure_skip_verify` exists but is an explicit,
  logged opt-in rather than a quiet fallback.
- **Proxies both ways.** Every outbound leg — a relay dialling a direct node, a node dialling
  out to open a tunnel, a relay joining a parent — honours a `proxy` setting supporting
  `http`, `https`, `socks5` and `socks5h`, plus the standard environment variables when no
  explicit proxy is configured. The repository already has this logic in
  `external/gateway/proxyutil`, but it is locked behind the gateway build tags; it moves to a
  neutral, untagged package (`internal/netx`) and the gateway keeps working through it, which
  is the extraction `docs/remote-control.md` §6.7 anticipated.
- **The tunnel composes with both.** Opening a tunnel through a proxy to a TLS relay is:
  dial the proxy, `CONNECT` to the relay, wrap the resulting stream in TLS, send the upgrade
  request, then invert roles on the same stream. Nothing in the HTTP/2 layer above cares that
  the byte stream came from a proxy or a TLS session.
- **ALPN.** The tunnel dial advertises `http/1.1`, because the upgrade it performs is an
  HTTP/1.1 mechanism; the connection is only repurposed for HTTP/2 *after* the upgrade
  succeeds, by prior knowledge rather than by negotiation.
- **Known incompatibility, stated rather than discovered.** The upgrade needs an end-to-end
  raw connection. A layer-7 proxy that re-frames requests, or an HTTP/2-only terminator in
  front of the relay, will break the tunnel while leaving the direct transport untouched.
  `docs/swarm.md` says so, the handshake fails with an error that names this cause, and the
  live e2e covers the proxy topologies that do work.

### 3.7 Console and ACP swarm mode

- On connect, `internal/remote` probes `GET /swarm/info` (public, cheap) **before** the model
  catalog, because a relay root serves no `/v1/models` and today's probe would report the
  relay as down.
- In swarm mode the Handler keys state by `(node_path, session_id)` and computes each
  session's mount base **per request** — no global base mutation, so parallel turns on
  different nodes cannot cross-route.
- **Duplicate ids resolve deterministically, never first-match**: `--session-id` accepts a
  qualified `<node>/<id>` (parsed client-side; the node never sees the slash); a bare id
  matching several nodes is an error listing the candidates; `-c` picks the globally newest by
  `updatedAt` with the same tie-break as the merge. ACP `SessionLoadParams` gains an optional
  `_meta` carrying `coddy/node_path`, and loads without it follow the same unique-or-error
  rule.
- New sessions need a node: `--node <name>`, a `/node` command, or a picker.
- Single-node fallback (`--remote relay/swarm/nodes/x`) keeps working with no code path of
  its own.

### 3.8 SPA swarm mode

- The relay does **not** serve the SPA in v1; the SPA runs where it does today and treats a
  relay as an environment (`env.kind = "swarm"`). Probe order `/swarm/info` → `/v1/models`
  fixes the permanently-down health dot.
- The fetch shim learns `/swarm/`; **the global base stays the relay origin for the app's
  lifetime**. Session-scoped calls go through an explicit `apiPathFor(session, path)` helper
  that prefixes `/swarm/nodes/{…node_path}` — including `<img>` thumbnail URLs, which bypass
  the shim entirely.
- Identity is composite in swarm mode: React keys, abort refs, activity maps, and the hash
  route `#/s/{node}/{id}` (plain `#/s/{id}` still valid off-swarm); the list dedupes by
  `(node, id)`.
- Sidebar: collapsible per-node groups (name, online dot, transport icon, count), node filter
  chips, the existing search box driving `/swarm/sessions?q=`, host badges on rows, per-node
  `hasMore` as a drill-down affordance.
- Topology screen: hand-rolled SVG tiered graph from `/swarm/topology`, online/offline and
  transport styling, node click filters the session list. Pure layout function, unit-tested,
  no new npm dependency.
- i18n keys land in en and ru together (parity test); DESIGN.md and docs/ui.md updated;
  screenshots of every changed surface at 390 and 1280, light and dark.

## 3.9 Review record

Round 4 reviewed v3 against the merged `main`: Cursor APPROVE_WITH_CHANGES, Codex REWORK,
coddy pending. Their two shared blockers — proxying the control plane, and wall-clock
deadlines killing long streams — are folded into §3.4, and the rest of both lists into §3.3,
§3.5, §3.6 and §4. Full texts in `swarm-review/round4-*.md`. The architecture survived
review; what changed is that a set of "obvious later" details are now specified, most
importantly that identity and route are different things, that the loop header needs an
authenticated peer channel to be both unforgeable and preservable, and that the hijack must
splice already-buffered bytes.

## 4. Security

- **Client auth required off-loopback**: binding beyond loopback without `swarm.auth_token`
  refuses to start unless `swarm.allow_insecure: true`. Stronger than `coddy http`'s
  warn-only default, deliberately — a relay is a fleet-wide door.
- **Pairing required by default** for registration and for opening a tunnel; plaintext
  registration carrying a dial-back credential to a non-loopback relay is **rejected**, not
  warned.
- **Blast radius is documented, not hidden**: one relay client token controls every
  registered node, transitively through every hop. `docs/swarm.md` says so plainly and
  recommends each node mint a dedicated token for its relay (`coddy http` already layers
  extra tokens), TLS in front, and pairing kept secret. Per-node ACLs are a named future item.
- **Redaction**: `swarm.auth_token`, `pairing_tokens`, join `token`, upstream `token`, and
  lease secrets added to `configSecretPath` and the JSON DTO; `/swarm/nodes` has no token
  field in its DTO at all; a **secret-canary test** plants unique strings and greps them out
  of config echoes, `/swarm/*` responses, error bodies, and captured logs.
- **Hardened listener introduced** (`internal/httpx` helper: `ReadHeaderTimeout`,
  `IdleTimeout`, `MaxHeaderBytes`, streaming routes exempt from write deadlines), adopted by
  both `coddy swarm` and `coddy http` in G1.
- **Egress policy, not just URL validation** (round-4 Codex): the direct transport dials
  through a custom dialer that connects only to IPs validated against an allow/deny CIDR
  policy — refusing loopback, link-local, metadata and, by default, private ranges — keeps
  TLS hostname verification intact, ignores environment proxies unless one is configured
  explicitly, and re-validates on every dial rather than only at registration, so DNS
  rebinding between check and connect has nothing to exploit.
- **TLS** on both legs (§3.6.2), with a private CA option and an explicit, logged
  `insecure_skip_verify` rather than a silent fallback.
- **A client credential is generated by default even on loopback** rather than left empty:
  every local process would otherwise inherit the relay's transitive authority over the whole
  fleet.
- **Protocol version negotiation at registration**: peers exchange a swarm protocol major
  version and capability set, and an incompatible peer is refused before it is published,
  so a rolling upgrade cannot silently break aggregation or identity.
- Structured logs carry node name, generation, transport, request class, durations, and
  disconnect reasons — never tokens, prompts, or full query strings.

## 5. Test strategy

| Layer | Coverage |
|---|---|
| Go unit (`swarm`, `-race`) | lease renew/replace/expiry + generation races; lease-secret ownership; name validation and 409s; SSRF matrix (loopback, link-local, metadata, redirect, rebinding); merge ordering and tie-breaks; per-node `hasMore`; search semantics including node-name-match-without-`q` and cwd; proxy path rewrite; header hygiene (Authorization replacement, cookie/hop-by-hop/swarm-path stripping, `access_token` strip); SSE flush and slow consumer (no goroutine leak); body caps; config validation and redaction; secret canary |
| Tunnel unit | handshake with and without a valid lease secret; reconnect swaps the transport and bumps generation; conn drop marks offline and fails in-flight cleanly; SSE through the tunnel; concurrent multiplexing; oversized frame and ping-timeout behavior |
| BDD (`http,swarm`) | `swarm_registry.feature` (join, heartbeat, expiry, credentialed replacement); `swarm_sessions.feature` (two in-process agents, **one duplicated session id**, aggregated list with node labels, search by node name and by title, open and prompt through the mount **with permission and question round-trips**, cancel mid-stream, `Last-Event-ID` reattach, node switch); `swarm_multihop.feature` (client → relay → relay → agent prompt; recursion; loop guard rejects a cycle; diamond dedupe) |
| Console BDD (`cli,http,swarm`) | `cli_swarm.feature`: relay detection, node-labeled list, `/node` and `--node`, qualified `--session-id`, prompt round-trip, single-node fallback |
| UI unit (vitest) | `apiPathFor`; `isApiPath` with `/swarm/` and a path-bearing base; composite keys and hash; grouping and filter reducers; topology layout; activity-poll gating |
| Live e2e (python) | `examples/swarm/`: real `coddy swarm` + real `coddy http` nodes — registration, aggregation, search, prompt and stream through the mount, permission answer, cancel, duplicate ids, node kill → warnings, relay restart → re-registration, auth matrix; **multi-hop**: three relays chained, one node reachable only by tunnel; **subagent demo** across nodes |
| Browser e2e | groups render, search narrows, node switch, two parallel turns on two nodes without cross-routing, deep-link reload, topology graph |
| Screenshots and video | every changed surface (before/after, 390 and 1280, light and dark) plus a recorded run of the multi-relay stack |

## 6. Open questions

- **Q1**: fate of the empty `coddy-relay` repo — sandbox, thin deployment wrapper, or archive.
- **Q2**: default relay port (proposal: 12346, next to `coddy http`'s 12345).
- **Q3**: should the tunnel also be offered for *reachable* nodes as a firewall-friendly
  default, or stay opt-in for closed contours only (proposal: opt-in, direct stays default).
