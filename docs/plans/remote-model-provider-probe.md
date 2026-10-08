# Plan: the application probe of a vanished peer (phase 4a of the remote model provider)

**Status: proposed (2026-10-08); P1 to P4 decided by model check the same day (3.1).** Design record, written before the code. It closes the item the phase 2 plan kept as O4 (`remote-model-provider-phase2.md`, 7.6 D3) and the phase 3 plan declined (`remote-model-provider-phase3.md`, section 9). Branch `feat/remote-model-provider`. Models and reports are in `remote-model-provider-models/` (`p4-*`).

## 1. Goal and non-goals

**Goal.** Free the slot of a shared-model call whose client vanished, **also behind an intermediary that acknowledges bytes for it** (a TLS terminator, an HTTP proxy), on a platform with no `TCP_USER_TIMEOUT`, and for a node that dials a direct peer through a CONNECT proxy: the three cases the transport bound of phase 2 (`B = 45 s`) cannot reach. The node cannot see the client there, so the **client has to say it is alive**: a small authenticated request, repeated while the stream runs.

**Non-goals.**

- A change for a client that does not speak the probe. Phase 2 found that a strict probe (the node cuts a call whose client does not answer) cuts every live foreign client, and a lenient one never detects anything (`p2-d3-vanished-peer`, O4). The probe here is **opt-in by the caller**: only a call that asked for it is cut by it.
- Replacing the transport bound. It stays for the legs it covers; the probe is an additional, earlier bound on the calls that opted in.
- A probe of the relay-to-node leg beyond what a call already carries (the relay forwards the ping like any request).
- A `protocol` bump: everything is additive on protocol 1 (an old node ignores the request header and never confirms; an old client ignores the response header and never pings).

## 2. Design

**Handshake.** The client adds the request header `X-Coddy-Probe: 1` to a completions call. A node that supports it answers the same call with `X-Coddy-Probe: id=<call id>; every=<I s>; grace=<G s>` next to `X-Coddy-Request-ID`. No header from the node, no probe: an old node, and a client that did not ask, are both unaffected.

**The call id** is 128 random bits, hex, made for the probe (not the request id of the log). It is a capability: only the response to the call carries it.

**The ping.** `POST /coddy/llm/calls/{id}/alive` with the same credential as the call, empty body. The node answers `204` and re-arms the call's alive timer, or `404 {"kind":"invalid","code":"unknown_call"}` when the call has ended or the id is unknown (the client stops pinging). The route is a fourth shared-model route: the gate admits it for the same classes as the other three (a shared-model token, a main token, a certificate name, a sign-in) and the relay's closed table of D5 gains exactly one entry, with the id segment matched by `^[0-9a-f]{32}$` on the decoded remainder. It takes no stream slot and no window token; its cost is a map lookup.

**The node's guard.** When a call opted in and was confirmed, the node arms an alive timer of **G** at the call's start (before the provider is called). A ping re-arms it. If it fires, the node cancels the call's context exactly as a failed write does: the upstream call ends, the slot is released through the one guarded release, the log line says `client_gone` with `cause: probe`, and the counters count it as `gone`. The timer is stopped on every exit of the call, and a ping that arrives after the call ended is a `404`.

**The client's side.** The local Coddy's `coddy` provider asks for the probe on every completions call. When the response confirms it, a goroutine pings every **I** until the stream ends or the context is cancelled; a failed ping (a transport error, a 5xx) is retried at the next interval and never ends the call; a `404` stops it. A ping never delays or blocks the stream.

**Through a relay.** The relay forwards the ping like any other scoped request (the route table entry above, the mount's usual credential replacement) and does not count it against the client's slots, window or counters beyond the existing `ok` row of the mount. A node reached through the tunnel gets it over the tunnel.

## 3. Dilemmas for model-check

Each has two defensible answers; the decision is made with the engine (`mcd`, explicit state, one property per run), and the verdict is about the model: the tests of section 5 are the conformance check.

- **P1. Who is cut.** (a) only a call that opted in and was confirmed; (b) every call, a call with no pings is cut (strict). Expected: (a). The properties: a live client that pings is never cut (S1), a vanished opted-in client is freed within G (S2), a call that did not opt in is never cut by the probe (S3).
- **P2. When the guard is armed.** (a) at the call's start, G from then; (b) at the first ping, G from it. Expected: (a): with (b) a client that vanishes before its first ping is never cut (S2 violated).
- **P3. How long G is against I.** The least G, in terms of I, the network delay d and the consecutive pings that may be lost L, for which no live client is cut (S1) and the vanished one is freed within G (S2): the model finds the boundary, and `G = (L + 1) I + d` is expected to be the exact one (`G - 1` violates S1).
- **P4. What proves a ping.** (a) the id alone (a capability); (b) the id and the credential key of the call. Expected: (b) on a direct listener, (a) through a relay (every client of a relay shares the node's credential, so the key adds nothing there). The property: a pinger without the id never keeps a call alive (S5); with the id it can, which is the capability's meaning and is stated, not modelled away.

Also modelled, as properties rather than options: the slot is released exactly once when the guard fires at the same instant as the completion (S4), and a ping after the end never resurrects a call (S6).

### 3.1 Verdicts (2026-10-08, `mcd 0.2.0`; model `p4-probe.pml`, report `remote-model-provider-models/p4-probe.md`)

- **P1:** (a), only a call that opted in and was confirmed; (b), a guard on every call, cuts a foreign client that never pings (S3, S1 violated).
- **P2:** (a), armed at the call's start; armed at the first ping, a client that vanishes before it is never cut (S2 violated).
- **P3:** `G = (L + 1) I + DMAX` is the exact boundary, verified at three parameter sets with `G - 1` violated each time. The constants: **I = 10 s, L = 2, DMAX = 5 s, G = 35 s**; a vanished client is freed by `G + DMAX + 1 s` = 41 s after it vanished (under the transport bound B = 45 s, which stays the later bound for the legs it covers).
- **P4:** the id and the credential of the call; through a relay the credential is shared by every client of the relay, so the id alone is the proof there, and a pinger that holds the id can keep a call alive (the id is a capability, stated in the documentation).
- S4 (exactly-once release) holds with the guarded release the code already has, and fails without it.

## 4. Stages and file ownership

| Stage | Owns | After |
|---|---|---|
| **P0** the model | `docs/plans/remote-model-provider-models/p4-probe.{pml,md}`, this plan's verdicts | none |
| **P1** node | `external/httpserver/{shared_probe.go (new), shared_models_http.go (the header, the guard), server.go and openapi*.go (the route), auth.go (the pattern)}` and tests | P0 |
| **P2** client | `internal/llm/{coddy_probe.go (new), coddy.go (the header, the goroutine)}` and tests | P0, the wire of P1 |
| **P3** relay | `external/swarm/{scope.go, mount.go}` (the table entry, the isSharedCompletions family) and tests | P1 |
| **P4** specifications and documents | `features/shared_models_probe.feature` and its harness, `docs/features/shared-models.md`, `docs/operate/swarm.md`, `docs/reference/http-api.md`, `AGENTS.md`, this plan | P1 to P3 |

Each stage is test-first and goes to cross-review before the next one starts.

## 5. Tests

- Node: a call that opted in is confirmed with the header; a call that did not is not; a vanished (silent) opted-in client has its call cancelled and its slot released after G with the log cause `probe`; a client that pings is never cut; a ping with a wrong id or after the end is `404`; a ping of a call that did not opt in is `404`; the slot is released exactly once under a race of the guard and the completion; a ping needs the same credential class as the call; the route is registered in the OpenAPI document.
- Client: asks with the header; pings at I while the stream runs; stops at the end, at the context's cancellation and at a `404`; a failed ping never ends the call; a node that does not confirm gets no ping.
- Relay: a scoped client's ping passes the table for a listed node only, an unlisted node and a malformed id are refused like an unknown route; no slot or token is spent; the full class passes.
- Feature: a client behind an acknowledging intermediary (a proxy that keeps the connection and reads the heartbeat) vanishes: the slot is free within G.

## 6. Risks

- A ping path that fails while the stream path is fine (a proxy that buffers `POST`s differently) would cut a live call: G tolerates `L` consecutive losses, a larger `L` costs a later detection, and the log cause says why.
- The id is a capability that travels in a response header: it is as private as the call, which a proxy in the middle already sees whole.
