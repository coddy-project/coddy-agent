# Round 1 — Claude Fable 5 reviewer

## Verdict: APPROVE_WITH_CHANGES

## Findings

1. [major] (§3.1, §3.5) The plan "reuses the httpserver listener hardening" — no such hardening exists. `external/httpserver/listen.go:8-10` is a bare `http.ListenAndServe` (no ReadHeaderTimeout/IdleTimeout/MaxHeaderBytes); hardening was designed in `docs/remote-control.md` §3.4 and explicitly deferred. Recommendation: swarm stage 2 must *introduce* a hardened `http.Server` (header/idle limits, no WriteTimeout on streaming routes), ideally as a shared helper both servers adopt.

2. [major] (§3.3) `{node}` in `/swarm/nodes/{node}/...` is an unvalidated registrant-supplied name used as a routing path segment. Names containing `/`, `..`, `%2F`, or reserved words corrupt routing and path chaining. Recommendation: validate node names with the session-id pattern (`internal/session/validate.go:10`, `^[A-Za-z0-9_-]{1,256}$`), reject collisions at registration, state whether the segment is `node_id` or name.

3. [major] (§3.7, §3.6) Relay detection/health probing breaks as specified: SPA health probes `baseUrl + "/v1/models"` (`activeHealth.ts:31`), env menu dots too (`EnvironmentChip.tsx:84`), and console `ensureModels` doubles as the connectivity probe (`internal/remote/rest.go:173-183`) — a relay root serves no `/v1/models`, so relay envs show permanently "down". Recommendation: swarm-aware probing (fallback `GET /swarm/info`), detection-before-model-probe ordering in `internal/remote`, or relay answers a minimal `/v1/models`.

4. [major] (§3.7) "Swarm mode makes that base per-session" underestimates SPA plumbing. Fetch shim rewrites by path prefix only (`remoteEnv.ts:158-164,187-191`): `/swarm/*` not in `isApiPath`; hash routing `#/s/{id}` carries no node, so deep link/reload cannot resolve the owning mount; `serverEvents.ts:50` subscribes `/coddy/events` which does not exist at the relay root. Recommendation: add `/swarm/` to `isApiPath`, put the node in the session hash (`#/s/{node}/{id}`) or persist a session→node map, gate server-events off in swarm mode.

5. [major] (§3.2, §5 stage 3) Build-tag placement of the agent-side join loop is unspecified and collides with tag discipline: if the join client lives in `external/swarm` (tag `swarm`), `external/httpserver` (tag `http`) cannot import it. Recommendation: decide in stage 2 — a shallow untagged join client package (config + HTTP loop only) importable by httpserver, or explicit `//go:build http && swarm` wiring with a stub; add resulting combos to the Makefile matrix deliberately.

6. [major] (§3.5) Trust flattening unstated: registration hands the relay each agent's full-surface bearer token, so one `swarm.auth_token` grants any client full control of *every* agent, transitively across federation. Recommendation: state the blast radius in docs, recommend agents mint a dedicated token for the relay, name per-node client authorization as an explicit non-goal.

7. [minor] (§3.4) Loop guard covers only the `/swarm/sessions` fan-out; mount path chaining (`A/swarm/nodes/B/swarm/nodes/A/...`) and live `/swarm/topology` recursion can also cycle. Attach `X-Coddy-Swarm-Path` + depth cap to mount-proxied and topology requests too.

8. [minor] (§3.3) `?access_token=` through the mount: relay-client token lands in node access logs. Relay auth gate must accept the query token on mounted SSE patterns and *strip the parameter* before proxying; state whether `/swarm/info` is public.

9. [minor] (§2 vs §3.3) Search-scope contradiction: goals promise cwd search, node-side `q` matches title OR first user message only (`filesystem.go:365-388`). Rows carry `cwd` — match it relay-side or drop from the goal.

10. [minor] (§3.2) Missing lease numbers (TTL, heartbeat) and node_id determinism statement; the grace-then-drop behavior silently diverges from remote-control.md §4.4 persist-known-agents — name the divergence and its cost.

11. [minor] (§3.7, matrix, Q4) `swarm,ui` as written cannot compile the SPA: asset embed is gated `//go:build http && ui` (`external/ui/embed.go:1`), SPA mount lives in `external/httpserver/spa_embed_ui.go`. Relay-served SPA needs an embed-tag change (`(http || swarm) && ui`) plus its own mount wiring.

12. [minor] (§4) BDD harness tagging: "open and prompt via mount" needs an in-process `coddy http` agent, so `external/swarm/bdd_*_test.go` must build with `swarm && http`; matrix row should be `http,swarm`.

13. [minor] (§4, §8.4) Permission/question round-trip through the mount is not explicitly in any listed scenario; cheap via the scripted-runner + recording-sender pattern (`bdd_remote_client_test.go:31-50`).

14. [minor] (naming) Full "swarm" is right; "relay" already names two unrelated things in-repo (deferred WS hub in remote-control.md §4; in-process `composerStreamRelay`, `composer_stream_relay.go:28`).

## Answers (abridged)

1. Mount keystone verified sound (Resolve keeps path; all client calls compose BaseURL+path; SPA streams SSE over fetch so bearer injection covers it; permission answers POST to same base). Flaws peripheral: node-name validation, probes hardcoded to /v1/models, SPA deep-link/isApiPath/server-events gaps, relay must impose no total timeout on streaming routes. No case for WS tunnel or id namespacing in v1.
2. Federation recursion sound (each relay talks only to direct children); loop guard must extend to mount chaining + topology; pin header format + 508 behavior.
3. Statelessness acceptable; real losses are offline-node visibility after restart and cursor stability; name the divergence from §4.4.
4. Pyramid mostly right; missing permission round-trip via mount, browser-level e2e of swarm UI, relay auth-matrix, SSE slow-consumer.
5. Order correct; move Q3/Q4 decisions before stage 2; fold permission proof into stage 4.
6. Three-token posture correct; holes: trust flattening, access_token query leak, /swarm/info auth classification, false "hardened listener" premise.

## Test strategy gaps

- Permission/question round-trip through the mount (BDD);
- Relay auth-matrix: 401 on mount, pairing rejection, access_token accepted-and-stripped, CORS preflight;
- Browser-level e2e for swarm UI;
- SSE slow-consumer/backpressure on the proxy;
- Cursor invalidation when a node leaves mid-pagination;
- Deterministic cancel-through-mount assertion;
- Corrected tag combos (`http,swarm`; `swarm,ui` not viable as written).

## One thing to simplify

Drop the cross-node opaque cursor from v1. Let `/swarm/sessions` return the first page per node (bounded limit), merged and truncated, with per-node `hasMore` and node filter as the drill-down; deep pagination for one node goes through the transparent mount which already has working offset cursors. True cross-node pagination can land later behind the same response shape.
