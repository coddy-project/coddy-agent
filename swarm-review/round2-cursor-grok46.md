## Q1: Blocker/major resolution

1. **[blocker] client routing / "unchanged clients"** - RESOLVED (v2 §3.3 single-node mount fallback; §3.6 Handler keyed by `(node_path, session_id)`, per-request mount, no global base flip; §3.7 `apiPathFor` + composite keys/hash).
2. **[blocker] federation cursor / diamond / proxy loops** - RESOLVED (v2 §2.1 M1 drops federation and cross-node cursor; §3.3 first-page merge + per-node `hasMore`/drill-down; §3.4 hop-wide `X-Coddy-Swarm-Path`, diamond dedup, cursors deferred to M2 optional).
3. **[blocker] fleet-admin auth, SSRF, unstable ids, composite UI identity** - RESOLVED (v2 §3.2 credential-bound `node_id == name` + generation; §3.5 required client auth off-loopback, pairing default, blast-radius docs, SSRF/pinning; §3.3/§3.7 `(node_path, session_id)`).
4. **[major] tag layering, redaction, fake listener hardening, proxy header hygiene** - RESOLVED (v2 §3.1 untagged `internal/swarm`; §3.5 `configSecretPath` + canary, `internal/httpx` introduced; §3.3 Authorization replace, `access_token` strip, `Last-Event-ID` pass-through).
5. **[major] activity freeze, `/v1/models` health, thumbnails, Q4 SPA host** - RESOLVED (v2 §3.7 SPA-as-swarm-env, `/swarm/info` probe, no `/coddy/events` in swarm mode, `include_activity` poll, `apiPathFor` for `<img>`).
6. **[major] permission/question, live python too late, matrix/cli, path-base vitest** - RESOLVED (v2 §4; §5 stage 5 mount spike + thin live python; `cli,http,swarm`; vitest on `apiPathFor`/`isApiPath`).

## Q2: Milestone split

Accept. Cutting federation (my round-1 simplify), relay-served SPA/topology (Codex), and cross-node cursor (Fable) into M2 is compatible ordering, not scope removal. M1 is the honest one-level vertical (registry, mount, first-page aggregate, console + SPA client mode, live/browser e2e). Pinning `kind: relay` and `node_path` on M1 DTOs is the right no-migration bet, as long as M2 cycle detection does not later reshape `/swarm/info`.

## Q3: New blockers in v2

- Swarm CORS is unspecified after Q4 (SPA on `coddy http`/vite talking to `coddy swarm` is cross-origin). Without copying httpserver CORS and adding `Last-Event-ID` to Allow-Headers (`cors.go:20`), stage 8/9 browser path fails. Also add `uuid` to M1 `GET /swarm/info` (it is absent in §3.3 but required by §3.4 register-time cycle checks), or §2.1 "no migrations" is false.
- Not a blocker: cwd search still cannot find cwd-only rows when `q` is pushed to the node; either fetch-first-page-without-`q` and filter locally, or drop cwd from the search promise.

## Q4: Final verdict

APPROVE_WITH_CHANGES - round-1 blockers/majors are folded in; pin swarm CORS (+ `Last-Event-ID`) and `uuid` on `/swarm/info` before coding.
