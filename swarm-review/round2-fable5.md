# Round 2 — Claude Fable 5 reviewer

## Q1: Blocker/major resolution

1. False "hardened listener" premise — RESOLVED (v2 §1 facts, §3.5 "introduced, not assumed", §5 stage 2: `internal/httpx` helper, `coddy http` adopts it same stage).
2. Unvalidated `{node}` path segment — RESOLVED (v2 §3.2: name validated `^[A-Za-z0-9_-]{1,64}$`, reserved words rejected, `node_id == name`, credential-bound replacement with 409 otherwise).
3. Detection/health probes hardcoded to `/v1/models` — RESOLVED (v2 §3.6 console probes `/swarm/info` first; §3.7 env chip probe order; `/swarm/info` public by design §3.3).
4. SPA plumbing gaps (deep link, `isApiPath`, `/coddy/events`, per-session base) — RESOLVED (v2 §3.7: `apiPathFor` request router with the global base fixed at the relay origin, `#/s/{node}/{id}`, `(node,id)` keys/dedupe, `/swarm/` in `isApiPath`, server-events never "connected" in swarm mode, thumbnails through the router).
5. Join-loop build-tag placement — RESOLVED (v2 §3.1: shallow untagged `internal/swarm`, importable by `http`; verified this respects the CLAUDE.md tag rule with no stub gymnastics).
6. Trust flattening unstated — RESOLVED (v2 §2.2 named non-goal, §3.5 blast-radius section, refuse non-loopback bind without client token, dedicated-token recommendation, plaintext credential registration rejected).

All eight round-1 minors also addressed (loop guard on every hop §3.4; access_token strip §3.3; cwd/search semantics §3.3; TTL/heartbeat/`registry_warming` and the named §4.4 divergence §3.2; `swarm,ui` dropped from M1 §3.1; BDD under `http,swarm` §3.1/§4; permission+question round-trips in stage 5 §5; swarm naming locked §3.1).

## Q2: Milestone split

Accept. The three round-1 simplifications were orthogonal cuts of the same instinct — ship the smallest honest vertical — and §2.1 reconciles them without dropping anything the operator asked for: M1 is exactly the day-1 scenario with the riskiest transport work (mount, SSE, permission round-trips) proven early by stage 5's live check, while federation, topology, and the relay-served SPA move behind a shipped, tested base. Critically, `kind: relay` and `node_path` living in the M1 DTOs from the start means M2 is additive behavior, not a migration. The first-page merge without a cross-node cursor is the right M1 trade, honestly documented, with the mount's native pagination as the drill-down escape hatch.

## Q3: New blockers in v2

None. One critical minor: v2 §1 correctly notes CORS `Allow-Headers` lacks `Last-Event-ID` (verified, `external/httpserver/cors.go:20`), but no section assigns the fix — in M1 the SPA is cross-origin to the relay (§3.7) and the composer-stream reattach sends a `Last-Event-ID` header (`App.tsx:2860-2863`), so the preflight will reject reattach until the relay's (and ideally `coddy http`'s) allow-list includes it; name this explicitly in §3.3/§3.5. Verified v2's new factual claims: `configSecretPath` indeed misses `pairing_tokens` (`internal/config/path.go:511-528`); `--session-id` makes cross-node id collisions realistic (`cmd/coddy/main.go:182`).

## Q4: Final verdict

APPROVE — every round-1 blocker/major across all three reviews is resolved with verified facts, the identity contract and request-scoped routing are the correct keystone repairs, and the one remaining item (CORS `Last-Event-ID`) is a one-line, stage-5-visible fix that does not affect the architecture.

*(The CORS `Last-Event-ID` fix was folded into the plan §3.3 immediately after this review.)*
