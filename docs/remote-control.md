# Remote Control & local/remote operation (design)

Status: **partially shipped**. This document is the design/roadmap; the sections below keep the
full plan for context, with the note here on what actually landed on branch
`feature/remote-control`.

This design was cross-reviewed by two external code agents (Codex, Cursor) against the live
codebase; their corrections are folded in and called out where they changed the plan.

### Implemented (this branch)

The operator chose the **direct remote-API** direction: run `coddy http` on the remote box and
point the UI at it from another origin. Shipped:

- **Optional bearer auth** for `coddy http` — a single `httpserver.auth_token` (also
  `--auth-token` / `CODDY_HTTP_TOKEN`), per-request gate, config-token redaction, hot reload
  (§3, simplified to one token instead of a list).
- **CORS** (`httpserver.cors`) + a client **remotes** list (`httpserver.remotes`) + a
  route-scoped `?access_token=` on the composer-stream SSE GET.
- **UI environment selector** (Settings → Environment): a global `fetch` shim points the SPA at a
  remote `coddy http` + bearer token (stored client-side), or back to Local.
- **e2e parity proof**: `external/httpserver/features/remote_api.feature` (godog) +
  `examples/httpserver/http_e2e_remote.py` — remote == local for auth, cwd change, session load,
  streaming, config redaction, and local fallback.

### Deferred (not built here)

- **Optional TLS/encryption** for `coddy http` — designed in §3.4, kept for a follow-up.
- **Remote Control reverse tunnel** (`coddy rc` + hub + agent registry, §4) — deferred; the
  direct authenticated-API approach covers the operator's need without a relay service.
- **SSH environment** (§7) and the **multi-agent ACP client** (§8) — think-only, not scheduled.

## 1. Goal & the environment model

Mirror the Claude Desktop *environment selector* (`Local`, `Remote Control`, `SSH`) in
Coddy: one UI/API that can drive an agent living in different places, with **optional**
authentication and **optional** encryption throughout.

| Environment            | What it is                                                | Status       |
|------------------------|-----------------------------------------------------------|--------------|
| Local                  | `coddy http` on loopback, UI + API                        | exists       |
| Remote via API         | `coddy http` off-box + opt-in bearer auth + CORS; UI env selector | **shipped** (TLS deferred) |
| Remote Control (tunnel)| `coddy rc` dials OUT to a hub; drive from a remote client  | deferred §4  |
| SSH                    | drive a remote agent over SSH                             | think-only §7 |
| Multi-agent ACP client | Coddy UI orchestrates several ACP agent processes         | think-only §8 |

### 1.1 Precedent, stated correctly

The Telegram gateway (`external/gateway`) is an **outbound-adapter *supervision* precedent**,
not proof of a reverse-control architecture. It contributes:

- `gateway.Adapter` + `gateway.Hub` (goroutine-per-adapter, restart-on-error);
- the per-turn `acp.UpdateSender` bridge shape;
- `sessionstore` persistence mechanics (key → Coddy session ID).

It does **not** contribute: authenticated/multiplexed bidirectional transport, agent
registration, request correlation, flow control, or interactive permission round-trips
(Telegram auto-allows permissions and returns empty answers to questions). Two supervision
caveats that bite a relay adapter directly:

- `Hub.Start` re-invokes `Adapter.Start` in a tight loop if it returns `nil` while the
  context is still live — the relay `Start` must block until cancellation (or the hub must
  back off on every unexpected return).
- `SessionRunner` is currently declared *inside* `external/gateway/telegram/bot.go`, and
  `proxyutil` is tagged `gateway || gateway.telegram`; both must move to a neutral,
  independently-buildable location before a relay adapter can reuse them (§6.7).

## 2. Non-goals (v1) and scope guardrails

- **No transparent remote `coddy http`.** The bundled SPA calls dozens of `/coddy/*`
  routes (sessions, messages, cancel, permission, composer-stream, plan, workspace,
  branches, stats, tool-calls, config, scheduler). A small frame protocol cannot preserve
  the full SPA experience. **v1 Remote Control targets a capability-limited, ACP-shaped
  *programmatic* client**; full UI parity is deferred and gated by an explicit capability
  model (§6.5). The UI degrades by capability rather than pretending parity.
- **No per-session pluggable "backend" refactor in Phase 2.** The unifying "agent backend"
  idea (§8) is a *later* effort; the hub-side relay is not naturally a `Backend.Run` because
  the hub does not own the remote `session.State`. Phase 2 keeps relay routing distinct from
  that abstraction.
- **No general remote config mutation.** The relay MVP must not expose `PUT /coddy/config`;
  only per-session config options (`set_mode`, `set_config_option`).
- No OAuth / account system; no automatic self-signed certificates; no resume/replay of
  in-flight turns across reconnect in v1 (connection loss cancels owned work, §6.4).

## 3. Phase 1 — Optional authentication & encryption for `coddy http`

Goal: make the existing REST/SSE API safe to expose beyond loopback, entirely opt-in and
backward compatible (every new field defaults to "off"; existing tests stay green).

### 3.1 Config model (`internal/config/http.go`)

Explicit object (avoids the overlapping `auth_token`/`auth_tokens` ambiguity the reviews
flagged):

```yaml
httpserver:
  host: 0.0.0.0
  port: 12345
  auth:
    enabled: false          # opt-in
    tokens: []              # one or more accepted bearer tokens; "${ENV}" expanded
  tls:
    cert_file: ""
    key_file: ""            # both required together; one-only is a startup error
  public_docs: false        # when auth enabled, gate /docs + /openapi.* unless true
  allow_insecure: false     # silence the "reachable without auth" startup warning
```

- CLI/env overrides are **separate** inputs layered on top: `--auth-token` (repeatable) and
  `CODDY_HTTP_TOKEN`. `--auth-token` is visible in `ps`; document env/file/stdin as the
  preferred source.
- `Validate()`: `enabled && len(tokens)==0` → error; exactly one TLS file set → error;
  port range as today.
- **Secret redaction is mandatory.** `GET /coddy/config` (`config_http.go`) and
  `UISchemaMap()` must **never** return token values — represent them write-only or as
  `${ENV}` references. Otherwise an authenticated endpoint exfiltrates its own credential.
  This touches the config JSON DTO, `docs/config.schema.json`, `docs/config-reference.md`,
  `config.example.yaml`, and the schema-drift test.

### 3.2 Auth evaluation (per request, not frozen)

Wrap the whole handler (including SPA/Swagger mounts) but resolve the policy on **every
request** from the atomic config pointer, so `PUT /coddy/config` enable/rotate/disable takes
effect immediately:

```go
func (s *Server) authGate(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        pol := authPolicyOf(s.activeCfg())     // read current policy each request
        if !pol.enabled || pol.isPublic(r)     // route-group classification, not prefix
            { next.ServeHTTP(w, r); return }
        if !pol.accept(credentialOf(r)) {       // crypto/subtle constant-time
            w.Header().Set("WWW-Authenticate", `Bearer realm="coddy"`)
            http.Error(w, "unauthorized", http.StatusUnauthorized); return
        }
        next.ServeHTTP(w, r)
    })
}
```

- Public/protected is decided by **route group / exact pattern**, not a string-prefix
  allowlist. Default when auth is on: public = SPA shell + static assets (+ a health route);
  protected = `/v1/*`, `/coddy/*`, and `/docs` + `/openapi.*` unless `public_docs: true`.
- Credential sources: `Authorization: Bearer <t>` for programmatic clients; **no global
  `?access_token=`** (leaks to logs/history/referrer). See §3.3 for the browser/SSE story.

### 3.3 Browser & SSE story

The primary stream `POST /v1/responses` is a `fetch` call and carries `Authorization`
directly. The only EventSource route is `GET /coddy/sessions/{id}/composer-stream`
(re-attach). Options, in order of preference:

1. **Cookie session** — `POST /coddy/auth/session` validates a token and sets an
   `HttpOnly; Secure; SameSite` cookie; subsequent `fetch` and `EventSource` authenticate by
   cookie; state-changing routes enforce Origin/CSRF. (Recommended; keeps secrets out of JS
   and URLs.)
2. **Short-lived stream ticket** — an authenticated request mints a single-purpose,
   short-TTL ticket accepted only by the composer-stream GET.
3. If a query token is ever retained, it is SSE-GET-only, off by default, and redacted in
   logs.

Phase 1 can ship **API-only** (Bearer header) first; the cookie login + SPA unlock panel is
a later sub-commit so `-tags http,ui` stays green.

### 3.4 Transport encryption & listener hardening

- `ListenAndServe` gains TLS: when both `tls.cert_file`/`key_file` are set, validate them and
  `ListenAndServeTLS`; set a minimum TLS version. TLS is **startup state** — changing certs
  needs a restart (no hot cert reload in v1). No auto self-signed.
- Replace bare `http.ListenAndServe` with an `http.Server{ ReadHeaderTimeout, IdleTimeout,
  MaxHeaderBytes, ... }`. Streaming routes need permissive write behavior, but header/idle
  limits still apply.

### 3.5 Safety posture (no breaking change)

Keep the current default bind and startup behavior. If bound to a non-loopback address with
auth disabled and `allow_insecure` false, log a prominent `WARN`. Do not refuse startup.

### 3.6 Tests (`-tags http`, and `http,ui` for the panel)

401 on missing/invalid credential (header); 200 with valid Bearer; enable/rotate/disable via
`PUT /coddy/config` each take effect on the next request; `GET /coddy/config` never returns a
token; public routes reachable without a token, protected ones not; TLS serves over HTTPS
(generated cert via `httptest`); one-only TLS file errors; existing no-auth tests remain
green; `openapi.go` gains a `bearerAuth` security scheme and the served spec matches.

## 4. Phase 2 — Remote Control reverse tunnel (planned)

Topology: `coddy rc` (agent, behind NAT) dials OUT to a hub on `coddy http --relay`; a remote
client drives the agent through the hub. No inbound port on the agent.

### 4.1 Prerequisite refactors (land before the adapter)

1. **Extract `SessionRunner`** from `telegram/bot.go` into a neutral gateway package
   (`external/gateway/runner.go`) so both Telegram and relay reuse it.
2. **Atomic `EnsureSessionWithID`** in `internal/session` that does not mutate the
   process-global `preferredNewSessionID` (today `SetPreferredSessionID` + `EnsureHTTPSession`
   is a race under caller-chosen IDs).
3. **Session-scoped update delivery.** `HandleSessionSetMode`, `HandleSessionSetConfigOption`,
   `HandleSessionNew`, `HandleSessionLoad` currently emit via the manager's default sender
   (`m.server`), not the per-request sender. A relay cannot rely on those updates returning
   over its connection. Add sender-aware variants or a session-scoped dispatcher; never mutate
   `Manager.server` per request (unsafe under concurrency).
4. **Cancellation entry point.** Expose a manager-level cancel that runs the full path
   (`State.SetCancel` + cross-process `.coddy-cancel-request` + turn lock + partial-output
   persistence). The relay must call this, not merely cancel the socket context — the
   `SkipTurnLock` HTTP path deliberately uses `context.WithoutCancel`.

### 4.2 Protocol package (shallow, import-safe)

Put connection-neutral DTOs and interfaces in a shallow package (`internal/relay/protocol`),
reusing `internal/acp` DTOs where they are genuinely ACP-compatible (no near-duplicate JSON).
Layering: agent adapter implements the protocol via `session.Manager`; hub routes depend on
registry/protocol interfaces; **OpenAI-SSE mapping stays in `external/httpserver`** (relay
frames are ACP-shaped, not OpenAI-SSE-shaped); CLI packages do construction. `httpserver`
imports the relay hub package, never the reverse.

Protocol must specify up front: version negotiation; connection-level agent ID + authenticated
principal; a request ID distinct from session and tool-call IDs; typed request/result/error
envelopes; cancel by request/session ID; max frame size; serialized-writer discipline;
bounded queues + slow-consumer policy; ping/pong deadlines; duplicate/replay behavior after
reconnect; ordering guarantees.

### 4.3 Transport

`github.com/coder/websocket` (pure Go, context-aware). Both reviewers preferred it over a
zero-dep SSE+POST pair; the "zero deps" argument is weak since the repo already vendors the
Telegram SDK. Requirements: one reader goroutine + one serialized writer; frame limits;
ping/pong deadlines; bounded outbound queues; `ws://` to non-loopback only behind an explicit
insecure opt-in; **`Origin` validation** (cross-site WebSocket hijacking is real even with
bearer/cookie auth). Do not build the SSE+POST fallback in v1 (two transports double the
protocol and test surface); document it as an alternative only.

### 4.4 Agent registry (hub-side)

```go
type AgentInfo struct {
    ID       string    // stable, operator-configured; not derived from the socket
    Name     string    // length-limited, escaped
    Caps     Capabilities // structured, versioned (see §6.5)
    Version  string
    LastSeen time.Time
    // Online is NOT persisted; derive from a live lease.
}
```

- `Register` returns a **lease** carrying a generation/connection token; unregister removes
  only the exact current registration (avoids the A-replaced-by-B-then-A-releases race). Do
  not expose raw `*agentConn`; hand out immutable snapshots.
- **Persist known agents** (durable JSON under `$CODDY_HOME/relay/agents.json`), separate from
  session mappings; show offline agents distinctly in the selector; never persist `Online`.
- Registry entries (including `CWD`/paths, which leak host layout) go **only** to
  authenticated, authorized principals; minimize fields.

### 4.5 Identity, authorization, and two trust domains

- **Client auth** (remote UI/API → hub) and **agent pairing** (`coddy rc` → hub) are separate
  credentials. Pairing is **required by default**; "empty token = public registration" is
  unsafe (anyone reaching the hub could claim an ID, replace a connection, and read prompts).
- A hub-generated **opaque conversation ID** maps durably to `principal + agentID + remote
  session ID`. Never derive durable identity from a socket. Every prompt, cancel, permission
  answer, question answer, and transcript read reauthorizes that tuple. Only the owning
  principal (or an explicitly authorized collaborator) may answer a permission/question.
- Store hashes/keyed digests of durable tokens where practical; never return them via config.

### 4.6 Interactive round-trips (the real new capability)

The relay `Sender` (agent side) implements `acp.UpdateSender`:

- `SendSessionUpdate` → ACP-shaped update frame back to the hub; the **hub** re-encodes into
  OpenAI SSE for the remote client using the existing `bridge.go` mapping.
- `RequestPermission` / `RequestQuestion` → send a request frame and block on the operator's
  response (context-cancelable). Correlation key = connection generation + relay request ID +
  session ID + ACP request/tool-call ID. This must reproduce the HTTP permission contract
  (`WritePendingPermission`, resume-after-restart) semantics over the wire, not a naive
  round-trip.

### 4.7 Concurrency, reconnect, cancellation contract (v1)

- One active turn per remote session; surface lock contention (`ErrSessionTurnBusy`) as an
  explicit busy/conflict response rather than hanging.
- Connection loss cancels all turns and pending interactions owned by that connection, with
  partial-assistant persistence matching HTTP cancellation. Resume/replay is a later protocol
  version.
- Define behavior for: two clients prompting one session; one client answering another's
  permission; agent reconnect mid-turn; hub restart; duplicate request IDs; client disconnect
  while a turn continues; agent process shutdown.

### 4.8 CLI shape & build tags

- `coddy rc` — the outbound agent (dedicated command; mirrors `claude rc`). Not a messenger
  adapter and **not** folded into `coddy gateway` (that would make config/tags/help
  Telegram-centric).
- Hub routes on `coddy http --relay`, gated `//go:build http && relay`, with an
  `http && !relay` registration stub. Do **not** name a server `coddy hub` (`gateway.Hub`
  already names the in-process supervisor).
- Agent adapter under `external/gateway/relay` or a dedicated module, tagged so shared gateway
  code builds independently (updating the `gateway || gateway.telegram` constraints or
  refactoring the common pieces out).
- **Add relay/gateway tag combinations to `make test`/CI** (default, `http`, `http,relay`,
  `gateway.relay`, `gateway.telegram`, aggregate `gateway`) and note that gateway tests are
  excluded from the default matrix today.

### 4.9 Config (planned)

```yaml
# hub side (on coddy http)
relay:
  enabled: true
  require_pairing: true
  pairing_tokens: []          # accepted agent credentials (redacted like auth.tokens)

# agent side (coddy rc)
gateways:                     # or a top-level `remote:` block — decide during Phase 2a
  relay:
    hub_url: "wss://hub.example"
    agent_id: "laptop"
    token: "${CODDY_RC_TOKEN}"
    insecure: false
```

### 4.10 Observability & tests

Structured logs with connection ID, agent ID, principal ID, request ID, session ID, and
disconnect reason; never log credentials, full prompts, permission arguments, or secret-bearing
query strings. Tests: in-process WebSocket via `httptest`; register→list→prompt→stream ordering;
permission round-trip; reconnect cancels owned work; wrong pairing rejected; registry lease
race; oversized frame / duplicate ID / slow consumer; token redaction; plus a Python harness
under `examples/relay/` mirroring `examples/acp/acp_e2e_todo.py`.

## 5. Optionality matrix (operator requirements)

| Knob                     | Default | Off                        | On                              |
|--------------------------|---------|----------------------------|---------------------------------|
| Hub/API client auth      | off     | open API                   | Bearer (or cookie) required     |
| Agent↔hub pairing        | on*     | (opt-in insecure only)     | matching pairing token required |
| TLS (hub / API)          | off     | `http://` / `ws://`        | `https://` / `wss://` w/ certs  |
| Agent `insecure`         | off     | refuse plaintext off-box   | allow `ws://` to a remote hub   |

\* Pairing defaults to required; running fully open is an explicit `insecure` opt-in, unlike
client auth which defaults off for backward compatibility.

## 6. Resolved open questions (reviewer consensus)

1. **Transport** — `coder/websocket`; no SSE+POST fallback in v1.
2. **Routing** — frame protocol first, ACP-shaped, versioned, capability-limited; v1 remote =
   programmatic client, not full SPA parity; a read-only HTTP proxy for the long tail is a
   later option.
3. **CLI** — `coddy rc` + `coddy http --relay`; not `coddy hub`, not under `coddy gateway`.
4. **Auth** — two credentials (client token + agent pairing) from the start; ACL maps
   principals → permitted agent IDs; shared all-access tokens allowed as an explicit
   convenience; `owner` ACL deferred until identities exist but the field is reserved.
5. **Browser token** — cookie-based session exchange (preferred) over `localStorage`+query.
6. **Registry** — persist known agents, show offline; never persist `Online`; separate store
   from session mappings.
7. **Difficulty** — real: fixed single runner, non-session-scoped update delivery, global
   preferred-ID race, ACP-vs-SSE representations, HTTP-layer permission recovery, cancellation
   machinery, absent backend identity, gateway tag/layering. Resolve via §4.1 before Phase 2b.

## 7. Think-only A — SSH environment

- **Recommended, works today:** port-forward `coddy http` over SSH
  (`ssh -L 12345:localhost:12345 host coddy http`) gives the local UI the *full* REST/SSE
  surface of the remote agent with zero new code; Phase 1 auth+TLS makes it safe. The UI could
  automate the tunnel.
- **ACP-over-SSH stdio** (closer to Claude Desktop) needs an ACP *client* in Coddy (§8) and
  only exposes the ACP method set — the richer `/coddy/*` features are not ACP methods, so the
  UI would lose plans/branches/memory/scheduler unless bridged. This is the "difference" an
  operator senses: the UI is built on the `/coddy/*` superset, not bare ACP.

## 8. Think-only B — Coddy UI as a multi-agent ACP client

Ambition: let Coddy orchestrate several ACP agent processes (Coddy, Codex, any ACP server) in
parallel. Present state: `internal/acp` is a *server*; `session.Manager` already multiplexes
many sessions and the UI already streams parallel `POST /v1/responses`. Missing: an ACP
*client* (`internal/acpclient`) that spawns a subprocess, speaks JSON-RPC over its stdio,
negotiates `initialize`/`session/new`/`prompt`/`cancel`, and republishes the subprocess's
`session/update` notifications onto an `acp.UpdateSender`.

The clean shape is a per-session **agent backend** chosen at session creation (local ReAct,
`acp-subprocess`, `ssh`, and later the relay). But per the review, this requires real work in
`internal/session` (persist backend identity in `session.State`, select at creation not per
turn, forbid mid-turn changes, define recovery/ownership/lifetime), and the **hub-side relay
is not a `Backend.Run`** (it routes to a remote session it does not own). So the backend
registry is a **separate future effort**, deliberately kept out of Phase 2's critical path;
Phase 2 is designed so its registry and protocol slot in later without rework.

## 9. Staged commit plan (branch `feature/remote-control`)

1. This design note. ✅ stage 1
2. Phase 1a: `httpserver.auth` config model + per-request gate + Bearer + config-token
   redaction + tests (`-tags http`).
3. Phase 1b: TLS option + listener hardening + startup safety warning + tests.
4. Phase 1c: cookie login (`POST /coddy/auth/session`) + SPA unlock panel (`-tags http,ui`).
5. Phase 1d: openapi + `docs/http-api.md` + config schema/reference/example/UISchema sync.
6. Phase 2a: prerequisite refactors (§4.1) with tests.
7. Phase 2b: protocol package + registry (leases, persistence).
8. Phase 2c: `coddy http --relay` hub routes (`http,relay`) + SSE re-encode + tests.
9. Phase 2d: `coddy rc` agent adapter + relay `Sender` + permission/question round-trip + tests.
10. Phase 2e: Python `examples/relay/` harness + docs + openapi + `make test` tag matrix.
11. Final per phase: `make test` (full matrix incl. relay tags) + `make lint`.
```
