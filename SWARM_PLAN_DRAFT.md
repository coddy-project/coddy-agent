# Coddy Swarm — design & work plan (v3)

Status: **v3 out for cross-review, implementation starting on approved groups.**
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
| G2 | outbound join from a node (register + heartbeat + lease secret) | auto-registration |
| G3 | per-node mount `/swarm/nodes/{node}/…`, **direct** transport | drive one node through a relay |
| G4 | **tunnel** transport (reverse HTTP/2, dial-out nodes) | closed contours |
| G5 | `/swarm/sessions` aggregation, search, warnings | one pane of glass |
| G6 | **multi-hop**: relay-in-relay, path chaining, loop guard, `/swarm/topology` | §0.1 |
| G7 | console/ACP swarm mode | `--remote <relay>` |
| G8 | SPA swarm mode: node groups, search, badges, node picker, topology graph | UI |
| G9 | live e2e stack, screenshots, video, subagent demo across relays | proof |

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

Reconnect: the node redials with exponential backoff + jitter, presenting the same lease
secret; the registry swaps the transport in place and bumps the lease generation, so
in-flight requests fail cleanly rather than being routed to a dead conn.

A node chooses its transport by config: `advertise_url` set ⇒ direct; omitted ⇒ tunnel.

### 3.4 The mount (per-node transparent proxy)

`/swarm/nodes/{node}/…` reverse-proxies the node's **entire** surface — no route allowlist,
so new node routes (subagents, hooks, workspace/file, usage) work the day they ship.

Contract:

- unknown or offline `{node}` ⇒ 502 with a JSON error naming the node and its lease state;
- **`Authorization` is replaced, never forwarded** — the client authenticates to the relay,
  the relay injects the node's own credential; cookies and hop-by-hop headers are stripped;
  `Last-Event-ID`, `X-Coddy-Session-ID`, `Accept`, `Content-Type` pass through;
- relay auth accepts `?access_token=` on mounted SSE GET patterns (EventSource cannot set
  headers) and **strips the parameter** before proxying, so the relay's client token never
  lands in node logs;
- SSE passes through with immediate flush and **no total request deadline** on streaming
  routes (a permission prompt can block for minutes); header/idle limits still apply;
  non-streaming routes get body caps;
- **relay CORS is its own config** (`swarm.cors`) because the SPA is cross-origin to the
  relay by construction, and its `Allow-Headers` **includes `Last-Event-ID`** (`coddy http`'s
  list gains it in the same group) or composer-stream reattach fails preflight;
- redirects from nodes are not followed.

Compatibility guarantee: `--remote https://relay/swarm/nodes/nas02` drives one node with
**today's** client, unchanged — `Resolve` already keeps a path-bearing base. Aggregated UX is
an explicit client mode on top (§3.7, §3.8), never a mutation of the global base.

### 3.5 Aggregation

`GET /swarm/sessions?node=&q=&limit=&include_activity=&include_scheduler=&include_subagents=`

- fan-out to online children with bounded concurrency and a per-node deadline (3 s);
- **no cross-node cursor in v1**: each node returns its first page, the relay merges by
  `updatedAt` desc (tie-break node name, then id), truncates to `limit`, and reports per-node
  `hasMore` plus a `warnings` array for nodes that failed or timed out — partial results beat
  a 502. Deep history for one node is the mount's own native pagination (drill-down);
- rows gain `node_path`, `nodeName`, `nodeUrl`, `kind`, and carry through `subagent`;
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
- **Loop and amplification guard on every hop** — mount, aggregation, and topology alike:
  each relay appends its `instance_uuid` to an internal `X-Coddy-Swarm-Path` header that is
  **stripped from inbound client requests** (untrusted values discarded), size-bounded, depth
  capped at 4. Seeing its own uuid ⇒ 508 + warning, never an infinite proxy chain.
- **Diamonds are DAGs, not cycles** (A→B, A→C, B→D, C→D): aggregation dedupes rows by
  `(terminal agent instance_uuid, session_id)` — the identity of the *leaf that owns the
  session*, never a relay's — and topology dedupes nodes by `instance_uuid`, keeping the
  first path and listing alternates as extra edges.
- `GET /swarm/topology` returns the tree plus flat edges (`{name, kind, uuid, online,
  transport, children}`), built from live children under the same deadline discipline, so the
  UI can draw the graph the operator asked for.

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
