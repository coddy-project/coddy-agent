# Coddy Swarm (relay) — design & work plan (v2.1, after two cross-review rounds)

Status: **CONSENSUS REACHED — awaiting operator approval; implementation not started.**
Final verdicts: Claude Fable 5 APPROVE (round 2); Cursor Grok 4.6 APPROVE_WITH_CHANGES
(round 2; both pins — swarm CORS with `Last-Event-ID` + `uuid` on `/swarm/info` — folded
in); Codex GPT-5.6 Sol APPROVE (round 3, after its four round-2 items were folded in:
lease-secret name ownership §3.2, leaf-agent diamond dedup §3.4, cwd via leaf matcher
extension §3.3, deterministic duplicate-id resolution §3.6, per-stage matrix rows §5).
Full review record: `swarm-review/round{1,2,3}-*.md`. Round-1 reviews live in `swarm-review/round1-*.md`
(Cursor Grok 4.6: APPROVE_WITH_CHANGES; Codex GPT-5.6 Sol: REWORK; Claude Fable 5:
APPROVE_WITH_CHANGES). v2 folds in every blocker/major and the consensus of the minors.
The three divergent "one thing to simplify" recommendations are reconciled as a
**two-milestone split** (§2.1) — nothing the operator asked for is cut, but v1 (M1) ships the
smallest honest core. Implementation has NOT started.

## 0. Operator's request (unchanged)

Relay ("swarm", рой) that multiple `coddy http` agents register into automatically; clients
(SPA, console, hypothetical mobile app) connect to the relay; all sessions from all hosts
visible centrally with host labels, host filter, and free-text search (node name, address,
task); connecting to a relay feels like `--remote`; the relay stores nothing; relays federate
into higher-level relays; UI gets host grouping and a topology graph; e2e tests for
everything including node switching and the UI. Monolith with build tags preferred
(`external/…` package + launch command); separate empty repo `coddy-relay` exists for
experiments, final role open (Q1).

## 1. Existing surfaces (facts, corrected in v2)

- `coddy http` (tag `http`, `external/httpserver`): OpenAI-compatible surface + `/coddy/*`
  REST (sessions list/messages/cancel/permission/question, `composer-stream` SSE re-attach
  with `Last-Event-ID` / `?last_event_id=`, plan, workspace, branches, stats, tool-calls,
  config, skills, MCP trust, server-wide `GET /coddy/events` SSE). Primary turn =
  `POST /v1/responses` (SSE over fetch, not EventSource). Bearer auth: single
  `httpserver.auth_token` + layered extras; protected-vs-public per mux pattern; the only
  query-token exception is `?access_token=` on the two SSE GETs. CORS per request; the
  allow-headers list today is `Authorization, Content-Type, X-Coddy-Session-ID`
  (`cors.go:20`) — note it lacks `Last-Event-ID`.
- **The listener is a bare `http.ListenAndServe`** (`listen.go:8-10`); the hardening designed
  in `docs/remote-control.md` §3.4 was deferred and does NOT exist. Swarm must introduce it
  (§3.5), not "reuse" it.
- Config secret redaction is driven by `configSecretPath` (`internal/config/path.go:518`),
  which matches `auth_token`/`token`/`secret` — it would NOT cover `pairing_tokens`; new
  secret fields must be added there explicitly.
- `internal/remote` implements the full session-handler surface over REST/SSE; **all state is
  keyed by bare session id** (`handler.go:44-47 sessions map[string]*sessionState`), one
  `BaseURL` per Handler; `Resolve` accepts a path-bearing base URL (rejects query/fragment/
  userinfo); `ensureModels` (`GET /v1/models`) doubles as the connectivity probe.
- ACP `SessionListInfo` has no node field; extensions ride `_meta` by ACP convention.
- SPA: env chip in the composer (`EnvironmentChip.tsx`), env in `localStorage`, fetch shim
  rewrites only `/v1/*|/coddy/*|/openapi*` (`remoteEnv.ts isApiPath`) to one global
  `env.baseUrl`; parallel background streams across sessions are a supported contract
  (multiple `POST /v1/responses` + per-session abort refs in `App.tsx`); health probes
  `baseUrl + "/v1/models"` (`activeHealth.ts:31`); `serverEvents.ts` subscribes
  `/coddy/events`; session hash route is `#/s/{id}` (no node); thumbnails load via
  `<img src>` (bypasses the shim); session list dedupes by bare `s.id`.
- `GET /coddy/sessions`: server-side `q` (title OR first user message,
  `filesystem.go:365-388` — **not cwd**), `include_activity`, `include_scheduler`,
  offset-string cursor (`coddy_coddy.go:679-694`), limit cap 100, sort `updatedAt` desc.
- Session ids: `^[A-Za-z0-9_-]{1,256}$` (`validate.go`), operator-choosable via
  `--session-id` ⇒ **cross-node id collisions are realistic**, not just theoretical.
- SPA embed is gated `//go:build http && ui` (`external/ui/embed.go`), mounted by
  `external/httpserver/spa_embed_ui.go`; Makefile builds UI assets only for `http,ui` combos.
- Naming hazard: "relay" already means two other things in-repo (deferred WS hub of
  remote-control.md §4; in-process `composerStreamRelay` SSE fan-out).
- BDD: repo-root `features/` + godog harnesses beside the owning package, stub runner,
  happy paths only; edge cases in unit tests. Live e2e harnesses in `examples/` (python).
- Prior art: remote-control.md §4 designs the **opposite** topology (NAT agents dialing OUT
  over WebSocket) and stays deferred; its §4.4 lease/generation semantics and §4.5
  two-trust-domain principle are adopted here.

## 2. Goals / non-goals

### 2.1 Two milestones (reconciliation of the three round-1 simplifications)

Round 1 produced three different "cut this from v1" recommendations: federation (Cursor),
relay-served SPA + topology graph (Codex), cross-node cursor (Fable). They are compatible;
the operator asked for all of the features, so nothing is dropped — the work is split:

- **M1 — one swarm level, full vertical**: registry + auto-registration, per-node mount,
  aggregated sessions with search/filter (first-page merge, **no cross-node cursor**),
  console swarm mode, SPA swarm mode (groups, search, badges) as a **remote environment**
  (SPA still served by `coddy http`/vite, pointed at the relay), live python e2e + browser
  e2e. No federation, no topology graph screen, no relay-served SPA.
- **M2 — federation + topology**: relay-joins-relay, recursive aggregation with loop/diamond
  handling, `/swarm/topology`, the UI topology graph, and (optional, each its own decision)
  relay-served SPA, cross-node pagination, `/swarm/events` fan-in, `agents.json` persistence.

M1 is useful alone (the operator's day-1 scenario: several reachable servers, one pane of
glass). M2 builds strictly on M1's API shapes: `kind: relay` and `node_path` are in the M1
DTOs from the start so M2 adds behavior, not migrations.

### 2.2 Non-goals (both milestones)

- No NAT reverse tunnel (remote-control.md §4 stays deferred; the lease carries `transport`
  so it can slot in later).
- No relay-side persistence of sessions or transcripts; no session moves; no load balancing;
  no OAuth/accounts.
- No per-node client authorization (ACLs): **one relay client token = fleet-admin over every
  registered agent, transitively in M2**. This blast radius is documented loudly (§3.5); ACLs
  are a named future item, not an accidental omission.
- No aggregated "create session anywhere": new sessions are created on an explicitly chosen
  node.
- No general remote config mutation via aggregated surfaces beyond what the mount already
  proxies (the mount is transparent by design; see §3.5 on why that makes client auth
  mandatory).

## 3. Architecture (v2)

### 3.1 Placement, naming, build tags (ADR-1, revised)

- **"Swarm" everywhere**: package `external/swarm`, build tag `swarm`, command `coddy swarm`,
  config block `swarm:`, API prefix `/swarm/`, docs `docs/swarm.md`. "Relay" survives only in
  prose as the architectural role. (All three reviewers concur; Q3 considered resolved unless
  the operator objects.)
- **Layering**: the agent-side join loop + shared DTOs live in a **shallow, untagged package**
  `internal/swarm` (config structs, register/heartbeat client, node-name validation, DTO
  types). `external/httpserver` (tag `http`) may import it; `external/swarm` (tag `swarm`,
  the relay server) imports it too. This respects the "default build must not import tagged
  packages" rule with no `http && swarm` stub gymnastics. The relay's *serve* side is
  `external/swarm` behind tag `swarm`; `cmd/coddy` gets the usual tagged command + stub pair.
- Test/build matrix rows added deliberately: `swarm` (unit), `http,swarm` (BDD harness — it
  boots in-process `coddy http` agents, so it needs both tags), `cli,http,swarm` (console
  feature), plus `make check-windows` combos. `swarm,ui` is **not** a row in M1 (no
  relay-served SPA; see M2).
- The `coddy-relay` repo is not the implementation home; it may become a thin deployment
  wrapper later (Q1 to operator).

### 3.2 Registry & registration (ADR-2, revised)

- **Node identity is operator-stable and lease-bound**: the joining node supplies `name`
  (validated `^[A-Za-z0-9_-]{1,64}$` — same discipline as session ids; reserved words
  rejected), and `node_id == name` within one relay. **Name ownership is proven by a
  per-lease secret, not by the shared pairing token** (round-2 Codex): the first successful
  registration of a name returns a relay-minted `lease_secret`; renewals and replacements
  must present it while the lease is alive — a valid pairing token alone gets 409 (a shared
  fleet credential must not allow claiming someone else's live name). The join client
  persists the secret under `CODDY_HOME` per relay, so an agent restart re-attaches
  immediately; without the secret the name frees up after expiry + grace, and an explicit
  authenticated `DELETE /swarm/nodes/{node}` is the administrative takeover path. Leases
  carry a generation counter (remote-control.md §4.4 semantics); unregister/replace acts
  only on the exact generation.
- Registration payload: `{name, kind: agent|relay, advertise_url, instance_uuid, token?,
  version, labels}`. `instance_uuid` is a per-process UUID minted by the joining node; it
  rides M1 DTOs (rows in federation responses and topology entries later dedupe by it, §3.4)
  so M2 needs no shape change.
  Heartbeat = idempotent re-register (with `lease_secret`). Defaults: TTL 90s, heartbeat 30s
  with ±20% jitter,
  one TTL of "offline" grace before the entry drops. Relay restart ⇒ empty registry that
  refills within one heartbeat interval; `/swarm/info` exposes `started_at` +
  `registry_warming` so clients can tell "just restarted" from "nothing exists". Divergence
  from remote-control.md §4.4 (persist known agents) is deliberate for statelessness; the
  cost (post-restart, "down" and "never existed" look alike until heartbeats arrive) is
  documented; optional `agents.json` persistence is an M2 decision.
- **`advertise_url` is an SSRF boundary** and is validated hard: `http(s)` only, no
  userinfo/query/fragment, host must not resolve to loopback/link-local/metadata ranges
  unless it appears in an explicit `swarm.allow_private_upstreams` allowlist (loopback
  allowed by default only when the relay itself binds loopback — the dev case); redirects
  are never followed by the proxy; re-resolve pinning (dial the resolved IP) guards DNS
  rebinding; registration body size capped (64 KiB).
- Static upstreams (`swarm.upstreams: [{name,url,token}]`) merge into the same registry with
  the same validation.
- Registry is in-memory only, guarded, with immutable snapshots handed out; `Online` is
  derived from lease freshness, never stored.

### 3.3 Relay HTTP surface (ADR-3, revised)

**Identity contract first**: the client-visible identity of a session in any aggregated
surface is **`(node_path, session_id)`** — `node_path` is a list of node names from this
relay downward (length 1 in M1). Raw session ids appear alone only on the per-node wire.
Aggregated rows carry `node_path`, `nodeName`, `nodeUrl`, plus the raw row fields.

1. **Per-node transparent mount** — `/swarm/nodes/{node}/…` reverse-proxies the node's full
   surface. Proxy contract (all verified against round-1 findings):
   - `{node}` segment is the validated node name; unknown/offline ⇒ 502 with a JSON error
     naming the node and its lease state.
   - **`Authorization` is replaced**, never forwarded: the client authenticates to the relay;
     the relay injects the node's bearer. Cookies and hop-by-hop headers are stripped;
     `Last-Event-ID`, `X-Coddy-Session-ID`, `Accept`, `Content-Type` pass through.
   - Relay auth accepts `?access_token=` on mounted SSE GET patterns (EventSource cannot set
     headers) and **strips the parameter** before proxying so the relay client token never
     reaches node logs.
   - **Relay CORS is its own config block** (`swarm.cors`, same shape as `httpserver.cors`):
     the SPA is cross-origin to the relay in M1 by construction (Q4), so this is mandatory,
     not inherited. Its `Allow-Headers` = `Authorization, Content-Type, X-Coddy-Session-ID,
     Last-Event-ID` — **`Last-Event-ID` included** (and `coddy http`'s own list gains it in
     the same stage): the composer-stream reattach sends that header and a missing entry
     fails the preflight (round-2 Fable + Cursor finding; lands in stage 5).
   - SSE pass-through with immediate flush (`FlushInterval: -1` or hand-rolled copy),
     **no total request timeout on streaming routes** (permission waits can be minutes);
     header/idle timeouts still apply. Response/request body limits on non-streaming routes.
   - Redirects from nodes are not followed; `Location` is left as-is (nodes don't redirect
     within the API surface today).
   - Works for the existing clients as-is **for single-node use**: `--remote
     https://relay/swarm/nodes/nas02` and the SPA env chip pointed at that base both work
     unchanged (path-bearing base verified in `resolve.go` / fetch-shim). This is the
     M1 fallback UX and the compatibility proof, not the whole story — aggregated UX is an
     explicit client mode (§3.6, §3.7).
2. **Aggregation & control API** (client auth required):
   - `GET /swarm/info` — `{swarm: true, name, uuid, version, node_count, started_at,
     registry_warming}`. `uuid` (the relay's runtime instance id) ships in M1 so M2's
     register-time cycle detection needs no shape change. **Public** (like the SPA shell): it is the detection/health probe
     for clients that hold no token yet; it leaks only the swarm's existence and name.
     Every other `/swarm/*` route is protected.
   - `POST /swarm/register` / heartbeat (pairing gated, §3.5); `DELETE /swarm/nodes/{node}`
     (client auth, exact-generation).
   - `GET /swarm/nodes` — registry snapshot; **tokens never serialized** (DTO has no token
     field at all — enforced by a canary test, not by omission discipline).
   - `GET /swarm/sessions?node=&q=&limit=&include_activity=&include_scheduler=` — fan-out to
     online agents (bounded concurrency, per-node deadline 3s): each node gets
     `limit` + `q` + passthrough params; **no cross-node cursor in M1**. The relay merges by
     `updatedAt` desc (tie-break node name, then id), truncates to `limit`, and reports
     per-node `hasMore` + `warnings` for nodes that failed/timed out (partial results beat a
     502). Deep history for one node = the mount's native pagination (drill-down path).
     Search semantics (fixes the round-1 inconsistency): a node whose **name or URL** matches
     `q` is queried **without** `q` (all its sessions match by node); other nodes get `q`
     pushed down. To make the "search by cwd" promise honest, the **node-side matcher is
     extended to cwd** in the same release (one-line change to
     `FilterSnapshotListForSearch` + unit test, ships in the aggregation stage) — the relay
     itself matches only node name/URL, never row contents (rows do not carry first-message
     text). Older nodes degrade gracefully (no cwd matching there). Documented as "search
     covers the fetched window", which in M1 is the merged first pages.
   - M1 has **no** `/swarm/topology` and no `/swarm/events`; activity badges in swarm mode
     come from `include_activity` polling (the SPA never marks server-events "connected" in
     swarm mode). `/coddy/events` per node stays reachable through the mount.
   - The swarm API ships **its own OpenAPI document** (`GET /swarm/openapi.yaml` + `/docs`
     behind the same public_docs-style knob), updated in the same commit as any handler
     change (repo workflow rule applies from stage 1, not stage 10).

### 3.4 Federation (ADR-4 — M2, design pinned now)

- A relay joins a parent via the same `internal/swarm` join client with `kind: relay`;
  the parent's mount composes paths (`/swarm/nodes/child/swarm/nodes/agentX/…`).
- **Loop/amplification guard applies to every proxied hop, not just fan-out**: each relay
  appends its runtime UUID to an internal, size-bounded `X-Coddy-Swarm-Path` header that is
  **stripped from inbound client requests** (untrusted values discarded) on mounts,
  `/swarm/sessions`, and `/swarm/topology`; seeing own UUID ⇒ 508 + warning; depth cap 4.
  Cyclic mounts are additionally rejected at register time when detectable (parent sees its
  own UUID in the child's `/swarm/info`).
- Diamond topologies (A→B, A→C, B→D, C→D) are DAGs, not cycles: aggregation dedupes rows by
  **`(terminal agent instance_uuid, session_id)`** — the identity of the *leaf agent* that
  owns the session, carried in rows from M1 on (§3.2) — never by relay identity, which would
  wrongly collapse equal session ids under different agents (round-2 Codex fix). Topology
  dedupes nodes by `instance_uuid`, keeping the first path encountered and listing
  alternates as extra edges.
- Child relays return sessions with their own `node_path`; the parent prefixes its child's
  name. Cursors are out of scope until cross-node pagination lands (same M2 decision).
- `GET /swarm/topology` (M2): tree + flat edges, `{name, kind, online, uuid, children}`,
  built from live child `/swarm/info`/`/swarm/nodes` with the same deadline discipline.

### 3.5 Security (revised — the round-1 hot zone)

- **Client auth to the relay is required for non-loopback binds**: binding beyond loopback
  with no `swarm.auth_token` refuses startup unless `swarm.allow_insecure: true` (stronger
  than `coddy http`'s warn-only default — deliberate, because the relay is a fleet-admin
  door, and this posture difference is documented). Constant-time comparison, same
  credential sources pattern (flag/env/config).
- **Trust flattening stated loudly**: whoever holds the relay client token controls every
  registered agent (and in M2, every transitive agent). `docs/swarm.md` gets a dedicated
  "blast radius" section; the recommended setup is agents minting a **dedicated token for
  the relay** (`coddy http` already layers extra tokens via `--auth-token`/env, so an
  operator can hand the relay a separate credential and rotate it independently), TLS in
  front of both, and the pairing default below.
- **Pairing required by default** for registration (`swarm.pairing_tokens`, non-empty
  required unless `swarm.insecure_open_registration: true`). Registration carrying a
  dial-back token over plaintext to a non-loopback relay is **rejected** (not warned) unless
  the same insecure flag is set.
- **Redaction**: `pairing_tokens`, `swarm.auth_token`, join `token`, and upstream `token`
  added to `configSecretPath` and the JSON DTO redaction; `/swarm/nodes` DTO physically has
  no token field; a **secret-canary test** plants unique token strings and greps them out of
  config echoes, `/swarm/*` responses, error bodies, and logs captured during the BDD run.
- **Hardened listener introduced, not assumed**: a shared `internal/httpx` (name TBD) helper
  producing `http.Server{ReadHeaderTimeout, IdleTimeout, MaxHeaderBytes}` with streaming
  routes exempt from write deadlines; the relay uses it from day one; `coddy http` adopts it
  in the same stage (small, behavior-preserving, closes a stale claim in two docs).
- Proxy hygiene recap (from §3.3): replace `Authorization`, strip cookies/hop-by-hop/
  `X-Coddy-Swarm-Path` inbound, strip `?access_token=`, never follow redirects, body caps,
  SSRF-validated upstreams only.
- Structured logs: node name, generation, request class, durations, disconnect reason;
  never tokens, prompts, or full query strings.

### 3.6 Console & ACP (revised)

- `--remote <url>`: `internal/remote` probes `GET /swarm/info` **before** `/v1/models` when
  the first models fetch 404s (ordering fix), or unconditionally on connect — cheap and
  public. On swarm detection the console enters **swarm mode**:
  - session list = `/swarm/sessions`; rows render `[node] title`; ACP carries the node in
    `SessionListInfo._meta["coddy/node_path"]` (additive, spec-legal);
  - the Handler keys local state by `(node_path, session_id)` and computes each session's
    mount base per request — **no global base mutation**; opening a listed session binds it
    to its node;
  - **duplicate-id resolution is deterministic, never first-match** (round-2 Codex):
    `--session-id` accepts a node-qualified form `<node>/<session_id>` (parsed client-side;
    the node never sees the slash). A bare id that matches sessions on several nodes is an
    **error listing the candidate nodes** (asking for `--node` or the qualified form); a
    unique match binds silently. `-c` (continue newest) picks the globally newest by
    `updatedAt` with the same node-name tie-break as the merge — deterministic. ACP:
    `SessionLoadParams` gains an optional `_meta` (additive, spec-legal) carrying
    `coddy/node_path` for clients that echo what listing gave them; ACP loads without it
    follow the same unique-or-error rule;
  - new session requires a node: `--node <name>` flag, `/node` command (lists nodes, sets
    the target for subsequent new sessions), or interactive picker on first prompt;
  - single-node fallback: `--remote relay/swarm/nodes/<n>` behaves exactly like today's
    remote mode (no code changes needed — this is the compatibility guarantee).
- ACP surface: same Handler, so editors get swarm listing + `_meta` node labels for free;
  everything else is per-session and rides the mount.

### 3.7 UI (revised — M1 scope)

- **The SPA is not served by the relay in M1** (Q4 resolved per review consensus): the SPA
  runs where it runs today (`coddy http` embedded build or vite dev) and connects to a relay
  as a **swarm environment**. M2 may add a relay-served swarm-specific SPA behind
  `(http || swarm) && ui` embed-tag work; that decision is deferred with its cost named
  (embed tag change + mount wiring + `/coddy/config`-less bootstrap).
- Env chip: relay entries in the existing menu; probe order `/swarm/info` → fall back to
  `/v1/models` (fixes the dead health dot); a swarm env sets `env.kind = "swarm"`.
- Fetch shim: `isApiPath` learns `/swarm/`; **the global base stays the relay origin for the
  whole app lifetime** (no per-session base flips). Session-scoped calls in swarm mode go
  through a small request router that prefixes `/swarm/nodes/{node}` onto `/coddy/...` and
  `/v1/...` paths based on the session's `node_path` — implemented as an explicit helper
  (`apiPathFor(session, path)`), not by mutating `env.baseUrl`. Parallel turns on different
  nodes therefore cannot cross-route (round-1 blocker resolved).
- Identity: all React keys, abort refs, activity maps, and the hash route become
  composite in swarm mode: `#/s/{node}/{id}` (plain `#/s/{id}` stays valid for non-swarm
  envs); the session list dedupes by `(node, id)`.
- Sidebar swarm mode: collapsible per-node groups (name, online dot, count), node filter
  chips, existing search input driving `/swarm/sessions?q=`; per-node `hasMore` renders as a
  "more on <node>…" affordance that switches the list to that node's drill-down (mount
  pagination). Session rows get a host badge; thumbnails and other `<img src>` URLs are
  generated through `apiPathFor` so they hit the mount.
- Activity: swarm mode never subscribes `/coddy/events`; it polls `include_activity` on the
  aggregated list (existing polling path; server-events stays off).
- New-session node picker in the composer (default: last used node).
- i18n: en+ru keys in the same change (parity test); DESIGN.md + docs/ui.md sections;
  screenshots per workflow.md step 5 (both widths, light+dark for new colors).
- Topology graph screen is **M2** (with `/swarm/topology`): hand-rolled SVG tiered tree,
  pure layout function + vitest; no new npm deps.

## 4. Test strategy (v2)

| Layer | Coverage | Where |
|---|---|---|
| Unit (Go, `swarm` tag; `-race` in CI rows) | lease renew/replace/expiry + generation races; name validation & 409s; advertise_url SSRF matrix (loopback/link-local/metadata/redirect/rebind); merge ordering + tie-breaks + per-node hasMore; search semantics incl. node-name-match-without-q and cwd; proxy path rewrite; header hygiene (Authorization replacement, cookie/hop-by-hop/swarm-path stripping, access_token strip); SSE flush + slow-consumer (no goroutine leak, node not stalled); body caps; config validation + redaction; secret canary | `external/swarm`, `internal/swarm` |
| BDD happy paths (godog, tag `http,swarm`) | `features/swarm_registry.feature` (join, heartbeat, expiry, replace-with-credential); `features/swarm_sessions.feature` (2 in-process agents with **one duplicated session id**, aggregated list with node labels, search by node name and by title, open + prompt via mount **including a permission round-trip and a question round-trip**, cancel mid-stream, composer-stream reattach with `Last-Event-ID`, node switch) | `external/swarm/bdd_*_test.go`, in-process `httptest` `coddy http` agents + stub runner (pattern `bdd_remote_client_test.go`) |
| Console BDD (tags `cli,http,swarm`) | `features/cli_swarm.feature`: relay detection, node-labeled list, `/node` + `--node`, prompt round-trip, single-node mount fallback | pattern of `bdd_cli_remote_test.go` |
| UI unit (vitest) | `apiPathFor` router; `isApiPath` with `/swarm/` and path-bearing base; composite keys/hash `(node,id)`; grouping/filter reducers; node picker; activity-poll gating | beside components |
| Live e2e (python) | `examples/swarm/swarm_e2e.py`: 2 real `coddy http` + 1 `coddy swarm`; register, aggregated list + search, prompt+stream through mount, permission answer, cancel, duplicate session ids, node kill → warnings + offline grace, relay restart → re-registration window, auth matrix (no token / bad pairing / access_token strip) | `examples/test_swarm.sh` |
| Browser e2e (python + Playwright, headless) | `examples/swarm/swarm_ui_e2e.py` against the live stack: env chip add relay → groups render, search narrows, switch node, two parallel turns on two nodes (no cross-routing), deep-link reload `#/s/{node}/{id}`, badges | reuses the repo's existing Playwright screenshot tooling conventions |
| Screenshots | every changed surface, before/after, 390/1280, light+dark | PR description |
| Matrix | `make test` += `swarm`, `http,swarm`, `cli,http,swarm` rows (M2 adds `http,swarm,ui` if relay-served SPA lands); `make check-windows` combos updated the same commit | Makefile/CI |
| M2 additions | federation BDD (parent/child, 2- and 3-relay cycles rejected, diamond dedup, depth cap, forged inbound swarm-path header ignored); topology endpoint + graph vitest; federation live e2e | — |

## 5. Staged commit plan (v2; every stage red→green, `make test` + `make lint`, docs in-stage)

M1:

1. **Decisions + skeleton docs**: `docs/swarm.md` (architecture, blast-radius section, this
   plan folded in), AGENTS.md/CLAUDE.md row, rules sync. Q1/Q2 answers from operator land here.
2. **Shared listener hardening + config**: `internal/httpx` hardened server helper (adopted
   by `coddy http` same commit); `swarm:` config block + validation + redaction
   (`configSecretPath` additions) + schema/reference/example/UISchema/configure-coddy sync.
3. **Registry + relay skeleton**: `external/swarm` registry (leases, lease secrets,
   generations, snapshots), `coddy swarm` command + stub, `/swarm/info` + `/swarm/nodes`;
   unit tests (incl. races); **Makefile/CI gain the `swarm` row in this stage** (matrix rows
   land with the stage that introduces each tagged package — round-2 Codex).
4. **Join + registration**: `internal/swarm` join client (register/heartbeat/backoff+jitter,
   client-side `lease_secret` persistence), `httpserver.swarm.join` wiring,
   `POST /swarm/register` with pairing + lease-secret ownership + SSRF validation + body
   caps; `swarm_registry.feature`; **`http,swarm` matrix row lands here**; swarm OpenAPI doc
   started (rule: spec moves with handlers every stage).
5. **Mount (the vertical spike)**: reverse proxy with the full §3.3 contract; BDD: open,
   prompt, **permission + question round-trips**, cancel, `Last-Event-ID` reattach, duplicate
   ids; **thin live python check joins this stage** (not stage 10) to catch flush/timeout
   reality early; auth matrix tests.
6. **Aggregation**: `/swarm/sessions` (fan-out, merge, search semantics, warnings, hasMore,
   include_activity/scheduler passthrough); rest of `swarm_sessions.feature`; unit tests.
7. **Console swarm mode**: detection ordering, `(node_path, id)` Handler state, `_meta` node
   labels, duplicate-id resolution rules, `--node`//node/picker; `cli_swarm.feature`;
   **`cli,http,swarm` matrix row lands here**.
8. **SPA swarm mode**: env kind, `/swarm/` in shim, `apiPathFor` router, composite
   keys/hash, groups/filter/search, badges, activity polling, node picker; vitest;
   screenshots; i18n parity.
9. **Full e2e + close-out**: `examples/swarm/` (API + browser), matrix/check-windows final
   verification (rows already landed per stage), docs/http-api.md cross-links, README,
   `docs/swarm.md` final pass.

M2 (starts only after M1 ships and the operator re-prioritizes):

10. **Federation**: `kind: relay` join, recursive `/swarm/sessions`, loop guard on every hop,
    diamond dedup, register-time cycle rejection; BDD + live e2e with 2 relays.
11. **Topology + graph UI**: `/swarm/topology`, SVG tree screen, node click → filter;
    vitest + screenshots + browser e2e.
12. **Optionals, each separately decided**: relay-served SPA (embed-tag change), cross-node
    pagination (same response shape), `/swarm/events` fan-in, `agents.json` persistence.

## 6. Risks (v2 deltas)

- Client-side composite identity touches many SPA call sites (abort refs, activity maps,
  hash) — mitigated by making `apiPathFor` + a `SessionRef {nodePath, id}` type the only
  door, and vitest on each converted site; the console Handler rework is bounded
  (state map key + base computation).
- First-page merge without cross-node cursor means "all sessions" is really "the newest
  `limit` per node, merged" — acceptable for a sidebar; documented; drill-down covers depth.
- SSRF validation needs care with IPv6 and dual-stack (unit matrix covers it).
- The hardened-listener adoption by `coddy http` is behavior-preserving but touches a shared
  path — kept to its own commit inside stage 2 with the full matrix run.
- Browser e2e adds a Playwright dependency to `examples/` (dev-only, mirrors the existing
  screenshot rig; not part of `make test`).

## 7. Open questions for the operator

- **Q1**: `coddy-relay` repo — experiment sandbox only, thin deployment wrapper importing
  the monolith, or archive?
- **Q2**: M1 reachability assumption confirmed? (Relay must reach each agent's
  `advertise_url`; NAT-hidden agents wait for the tunnel work.)
- **Q3** (soft): "swarm" naming everywhere as in §3.1 — objections?
- **Q4** (resolved by review, confirm): M1 ships SPA-as-client-of-relay; relay-served SPA is
  an M2 optional.
- **Q5**: milestone split acceptable — M1 without federation/topology-graph, M2 adds them?
  (Everything requested still lands; this is ordering, not scope removal.)

## 8. Round-2 questions to reviewers

1. Does v2 resolve each of your round-1 blockers/majors? List any that remain, with the v2
   section that fails.
2. Do you accept the two-milestone reconciliation (§2.1) of the three divergent
   simplifications? One paragraph.
3. Any NEW blockers introduced by v2 (identity contract §3.3, search semantics, first-page
   merge, security posture §3.5, stage order §5)?
4. Final verdict: APPROVE / APPROVE_WITH_CHANGES / REWORK.
