## Verdict: REWORK

## Findings

1. [blocker] (§3.3, §3.6–3.7) - The transparent mount solves transport routing but not client identity. Two nodes may expose the same session ID; `internal/remote` stores state solely by ID (`sessions map[string]*sessionState`) and ACP exposes only a bare `sessionId`, while the SPA keys streams, cancellation, transcript shadows, and selection by bare ID. This will merge or misroute colliding sessions. Evidence: `internal/remote/handler.go:44-47`, `internal/remote/handler.go:103-112`, `internal/acp/types.go:219-230`, `external/ui/src/ui/App.tsx:750-758`. Define the client-visible identity as `(node_path, session_id)` from the outset, with an explicit reversible client key or ACP `_meta` contract; keep the raw session ID only on the node wire.

2. [blocker] (§3.7) - “Making the fetch-shim base per-session” is unsafe because it is global, while the SPA explicitly supports parallel background streams across sessions. Existing requests contain only `/coddy/.../{sid}` and the shim appends them to one active `env.baseUrl`; changing that base during navigation can send background reattach, cancel, permission, or question calls to the wrong node. Evidence: `external/ui/src/ui/env/remoteEnv.ts:166-192`, `external/ui/src/ui/App.tsx:750-758`, `external/ui/src/ui/App.tsx:2819-2867`, and the parallel-session contract in `AGENTS.md:101`. Introduce request-scoped routing—such as a fetch wrapper accepting `node_path`, or stable relay URLs stored with each session—not mutable global environment state.

3. [major] (§3.3) - Aggregated search is internally inconsistent. Forwarding `q` to every node removes sessions whose title/first message does not match, so the relay cannot subsequently return all sessions when the same `q` matches the node name/URL; it also cannot implement the stated CWD search because node search excludes CWD. Evidence: current filtering covers title and first user message only at `internal/session/filesystem.go:365-387`, and filtering precedes pagination at `external/httpserver/coddy_coddy.go:713-738`. When node metadata matches, query that node without `q`; for CWD search, either extend the node API deliberately or fetch and filter a sufficiently defined server-side index.

4. [major] (§3.3, §6) - The cursor design is underspecified and the stated `{node_id: node_offset}` map is not sufficient unless it records exactly how many rows from each fetched page were consumed by the global merge. Advancing every child to its returned `nextCursor` skips rows; advancing none duplicates rows. Existing cursors are mutable-list offsets (`external/httpserver/coddy_coddy.go:679-694`, `external/httpserver/coddy_coddy.go:766-774`), so node joins, expiry, federation changes, and concurrent session updates further destabilize them. Specify a k-way merge frontier, consumed count per child, query/filter fingerprint, deterministic tie-breaker, and registry generation; reject stale or mismatched cursors.

5. [major] (§3.4) - Header-plus-depth loop detection is necessary but insufficiently specified. Runtime UUIDs detect request recursion, but the plan does not define validation, trusted overwrite/append behavior, duplicate node paths, DAG deduplication, or how partial 508 responses compose through both sessions and topology. The earlier relay design requires generation-aware identities and immutable registry snapshots (`docs/remote-control.md:242-261`). Make each relay append its own ID to a parsed, size-bounded internal header after discarding untrusted client values; test cycles of two and three relays, diamonds, depth exhaustion, and partial failures.

6. [major] (§3.2, §3.5) - Registration creates an SSRF and node-takeover boundary that the three-token summary misses. A pairing-token holder can advertise loopback, link-local, metadata-service, or arbitrary internal URLs and obtain a client-accessible authenticated proxy; identity based on `advertise_url + name` also permits replacement and becomes unstable after relay restart. Require stable node-supplied IDs bound to pairing credentials, generation/lease replacement rules, strict URL parsing and scheme policy, configurable destination/CIDR policy, redirect restrictions, DNS-rebinding protection, request/body limits, and explicit stripping of inbound authorization, forwarding, cookie, and hop-by-hop headers.

7. [major] (§3.5) - “Optional tokens plus warnings” is too weak for relay-to-agent credentials. A token delivered through registration over plaintext gives the relay effective control of the agent; logging a warning does not protect it, and redacting config echoes alone does not cover request dumps, errors, metrics, panic output, or URL query tokens. Require pairing by default as proposed, reject credential-bearing non-loopback plaintext registration unless an explicit insecure flag is set, overwrite rather than forward client authorization, and add end-to-end secret-canary tests across logs, config, topology, errors, and proxy responses. Current HTTP auth correctly uses constant-time comparison and narrowly limits query tokens (`external/httpserver/auth.go:46-68`, `external/httpserver/auth.go:94-121`); swarm should preserve those properties.

8. [major] (§4–5) - The test strategy has no real browser e2e despite the operator requiring e2e coverage for UI grouping, switching, all-sessions, and search. Vitest plus screenshots cannot prove fetch rewriting, CORS preflight, authentication, concurrent streams, or routing after navigation. Add a Playwright/browser suite against two real agents and a relay, including duplicate session IDs, permission/question interaction, cancel, background streaming while switching nodes, federation, and narrow/wide UI assertions.

9. [major] (§5) - The riskiest integration checks arrive too late. Live e2e, build-tag combinations, SPA embedding, and OpenAPI decisions are deferred to stage 10, after nine stages may have committed to flawed boundaries. The Makefile currently builds UI assets only when both `http` and `ui` tags are present (`Makefile:47-50`), so the proposed `swarm,ui` build is not presently viable. Move a thin vertical spike—mount, authenticated SSE prompt, permission answer, duplicate IDs, and relay-served SPA bootstrap—to the beginning; update the matrix with each new tagged package.

10. [major] (§3.7) - Serving the existing SPA directly from a relay is not yet a defined compatibility contract. On direct load, the environment is “local,” while the SPA expects `/coddy/config`, `/v1/models`, `/coddy/events`, and other local APIs; swarm detection alone does not explain which node supplies global settings, commands, models, workspace, or scheduler views. Existing routes are broad (`external/httpserver/server.go:111-127`, `external/httpserver/coddy_coddy.go:121-155`). Either make the relay SPA swarm-specific, define relay-root compatibility endpoints and disabled surfaces, or serve the SPA elsewhere and treat swarm strictly as a remote environment.

11. [minor] (§3.1, §5) - Naming is still unresolved despite naming being part of package, build tag, config schema, API paths, logs, and docs. Use “swarm” consistently for the product/API/config/tag and reserve “relay” as the architectural role in prose. Resolve this before stage 1 to avoid permanent aliases and migrations.

12. [minor] (§5) - “OpenAPI note” at stage 10 conflicts with the repository’s API workflow. Externally visible routes and shapes must be specified alongside implementation, not decided after it; the repository requires handler/spec synchronization for HTTP surfaces (`.cursor/rules/workflow.mdc:40-41`, `AGENTS.md:72`). Decide early whether swarm has its own served OpenAPI document and update it in every API stage.

## Answers to the 6 judgment questions

1. The path mount is the correct transport primitive: `internal/remote.Resolve` preserves a URL path (`internal/remote/resolve.go:45-60`), and its requests append API paths directly (`internal/remote/rest.go:43-56`). It is not sufficient as the overall keystone because aggregated clients need collision-free identity and request-scoped node routing; no WebSocket tunnel is required to solve those problems.

2. Recursive federation and path chaining can work, but the current loop guard is only a sketch. It needs trusted header handling, stable relay identity, deterministic node-path semantics, cursor generation, DAG behavior, and tests for multi-relay cycles and diamonds.

3. An in-memory registry is acceptable for a v1 stateless relay if empty-after-restart is explicit, readiness reflects rebuild state, registrations use jittered backoff, and node IDs remain stable across rebuilds. Persistence is not mandatory, but without stable credential-bound identities, restart invalidates bookmarks, paths, cursors, and topology references.

4. The proposed pyramid is broad but lacks actual browser e2e and critical collision/concurrency scenarios. Python HTTP e2e cannot verify SPA routing, CORS, fetch-shim behavior, grouped interaction, or permission dialogs.

5. The ten stages put configuration and broad implementation before the highest-risk vertical proof, and defer e2e/matrix/OpenAPI too far. Begin with contracts and a narrow authenticated mount plus real-client/browser spike, then registry, aggregation, federation, and product UI.

6. Separating client, pairing, and upstream credentials is sound, and pairing-required is the correct default. The plan still needs credential-bound node identity, SSRF controls, secure transport enforcement, header/cookie sanitization, rotation/revocation semantics, payload limits, and broader redaction tests.

## Test strategy gaps

- Duplicate session IDs on two agents across list, open, prompt, cancel, permission, question, and background reattach.
- Real browser e2e for node switching, grouping, search, topology, new-session picker, and federation.
- Concurrent streams on different nodes while the viewed session and node filter change.
- CORS preflight including `Authorization`, `X-Coddy-Session-ID`, and `Last-Event-ID`; current allow-list omits the latter (`external/httpserver/cors.go:17-21`).
- Permission and question round-trips through both one relay and a federated path.
- Exact SSE behavior: immediate flush, cancellation propagation, disconnect, resume via `Last-Event-ID`, and `X-Accel-Buffering`.
- Pagination with uneven node pages, equal timestamps, node joins/leaves, expired leases, updates between pages, and stale cursors.
- Search by node name, URL, title, first message, and CWD, including node-name matches whose sessions do not match `q`.
- SSRF, redirects, DNS rebinding, path traversal/encoded separators, oversized registration bodies, malicious headers, and token leakage.
- Two- and three-relay cycles, diamonds, depth cap, forged loop headers, topology deduplication, and partial child failure.
- Relay restart during registration, aggregation, active streams, and federation; heartbeat jitter and thundering-herd behavior.
- Race-enabled Go tests for lease replacement, expiry, fan-out cancellation, and concurrent registration.
- Build and test combinations added incrementally, including a verified `swarm,ui` asset build and the recommended full binary.

## One thing you would simplify

Drop the relay-served full SPA and topology graph from the first vertical release. First ship a well-specified swarm API, stable `(node_path, session_id)` identity, authenticated path mounts, aggregation, and existing console/ACP operation; then add grouped sessions to the existing SPA as a remote environment. That removes the ambiguous relay-root `/coddy/*` compatibility surface and lets the team prove routing, security, pagination, and federation before investing in a second SPA hosting mode and topology visualization.