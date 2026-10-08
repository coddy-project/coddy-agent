# Plan: the application probe of a vanished peer (phase 4a of the remote model provider)

**Status: proposed (2026-10-08); P1 to P4 decided by model check the same day and revised after its cross-review (3.1).** Design record, written before the code. It closes the item the phase 2 plan kept as O4 (`remote-model-provider-phase2.md`, 7.6 D3) and the phase 3 plan declined (`remote-model-provider-phase3.md`, section 9). Branch `feat/remote-model-provider`. Models and reports are in `remote-model-provider-models/` (`p4-*`).

## 1. Goal and non-goals

**Goal.** Free the slot of a shared-model call whose client vanished, **also behind an intermediary that acknowledges bytes for it** (a TLS terminator, an HTTP proxy), on a platform with no `TCP_USER_TIMEOUT`, and for a node that dials a direct peer through a CONNECT proxy: the three cases the transport bound of phase 2 (`B = 45 s`) cannot reach. The node cannot see the client there, so the **client has to say it is alive**: a small authenticated request, repeated while the stream runs.

**Non-goals.**

- A change for a client that does not speak the probe. Phase 2 found that a strict probe (the node cuts a call whose client does not answer) cuts every live foreign client, and a lenient one never detects anything (`p2-d3-vanished-peer`, O4). The probe here is **opt-in by the caller**: only a call that asked for it is cut by it.
- Replacing the transport bound. It stays for the legs it covers; the probe is an additional, earlier bound on the calls that opted in.
- A probe of the relay-to-node leg beyond what a call already carries (the relay forwards the ping like any request).
- A `protocol` bump: everything is additive on protocol 1 (an old node ignores the request header and never confirms; an old client ignores the response header and never pings).

## 2. Design

*(Revised the same day after a cross-review of the first version: the guard is armed by the first ping, the id travels in a header, the tie and the clock are specified. The reasons are in 3.1.)*

**Handshake.** The client adds the request header `X-Coddy-Probe: 1` to a completions call. A node that supports it answers the same call, in the headers that precede the first byte of the stream, with `X-Coddy-Probe: id=<call id>; every_ms=<I>; grace_ms=<G>` next to `X-Coddy-Request-ID`. Only an admitted call is confirmed (a refusal never carries it). No header from the node, no probe: an old node, and a client that did not ask, are both unaffected.

**The call id** is 128 random bits (`crypto/rand`), 32 lower-case hex digits, made for the probe (not the request id of the log). It is a capability: only the response to the call carries it.

**The ping.** `POST /coddy/llm/alive` with the header `X-Coddy-Probe-Id: <call id>` and the same credential as the call, empty body. The id is a header and not a path segment because a path is what proxies and the relay's mount write to their logs on a forwarding error, and through a relay the id alone is the proof (P4). The node answers `204` and records the ping, or `404 {"kind":"invalid","code":"unknown_call"}` when the id is unknown, malformed, finished, cut or belongs to another credential (the same answer for each). The route is a fourth shared-model route: the gate admits it for the same classes as the other three (a shared-model token, a main token, a certificate name, a sign-in) and the relay's closed table of D5 gains exactly one entry, `POST /coddy/llm/alive`, an exact match like the others. It takes no stream slot and no window token and is not counted by the relay or the node as a call; its cost is a map lookup.

**The node's guard: fail-open.** A call that asked and was confirmed has a guard, **armed by its first accepted ping**, not at the call's start. A ping path that does not work at all (a relay that does not carry the route, a proxy that filters it, a node behind a balancer that does not stick) then leaves the call exactly as it is today and cuts no live client; a client that vanishes before its first ping falls back to the transport bound B. From the first ping on, **more than G of silence cancels the call**: it is implemented as G + 1 s measured on the monotonic clock from the last accepted ping, so a ping that lands on the tick the grace ends still wins (the model's tie rule). Arming, renewing and expiring happen under one lock, and a ping that finds the guard fired answers `404`: a `204` always means the call was not cut at that moment. A fired guard cancels the call's context as a failed write does, writes a best-effort terminal error frame (`kind: invalid`, `code: probe_lapsed`: not transient, so a live client wrongly cut is told why and does not re-run a call that would be cut again), releases the slot through the existing guarded release, logs `client_gone` with `cause: probe` and counts `gone`. The guard stops on every exit of the call, and the id is forgotten with it.

**The client's side.** The local Coddy's `coddy` provider asks for the probe on every completions call. When the response confirms it, a goroutine posts the first ping **at once** and then every I until the stream ends or the context is cancelled. Each ping goes through the row's own HTTP client (so `providers[].proxy`, the private CA, the client certificate and the HTTP/1.1-only transport apply), under a deadline of the smaller of I and 10 s, one at a time. Only `404` with `code: unknown_call` stops the goroutine; any other answer, a transport error and a timeout are a failed ping, retried at the next interval, and never end or delay the stream. The interval is the remote's `every_ms` held to a floor of one second.

**Through a relay.** The relay forwards the ping like any other scoped request (the table entry above, the mount's usual credential replacement, the header unchanged) and does not count it: no slot, no window token, no row of `GET /swarm/stats`. A node reached through the tunnel gets it over the tunnel.

## 3. Dilemmas for model-check

Each has two defensible answers; the decision is made with the engine (`mcd`, explicit state, one property per run), and the verdict is about the model: the tests of section 5 are the conformance check.

- **P1. Who is cut.** (a) only a call that opted in and was confirmed; (b) every call, a call with no pings is cut (strict). The properties: a live client that pings is never cut (S1), a vanished opted-in client is freed within a bound (S2), a call that did not opt in is never cut by the probe (S3).
- **P2. When the guard is armed.** (a) at the call's start, G from then; (b) at the first accepted ping. The properties: a live client is never cut whatever happens to the pings, including a path that carries none (S1 with `BROKEN`), and a vanished client is freed within the bound (S2).
- **P3. How long G is against I.** The least G, in terms of I, the network delay DMAX and the consecutive pings that may be lost L, for which no live client is cut (S1): `G = (L + 1) I + DMAX` is expected to be the exact boundary (`G - 1` violates S1).
- **P4. What proves a ping.** (a) the id alone (a capability); (b) the id and the credential key of the call. Expected: (b) on a direct listener, (a) through a relay (every client of a relay shares the node's credential). The property: a pinger without the id never keeps a call alive (S5).

Also modelled: the slot is released exactly once when the guard fires at the same instant as the completion (S4). A ping after the end changes nothing, which the model cannot express and the node's unit test holds.

### 3.1 Verdicts (2026-10-08, `mcd`; model `p4-probe.pml`, report `remote-model-provider-models/p4-probe.md`)

- **P1:** (a), only a call that opted in and was confirmed; (b), a guard on every call, cuts a foreign client that never pings (S3, S1 violated).
- **P2: (b), armed by the first accepted ping.** The first version decided (a), at the call's start, on the argument that a client that vanishes before its first ping must still be cut. The cross-review showed the cost: a path that does not carry the ping at all (a relay without the route, a path-allowlisting proxy) makes every opted-in call be cut at G while its client is alive. With the model's `BROKEN` option (every ping lost) the start-armed guard violates S1 and the first-ping-armed one verifies. What (b) gives up is a client that vanishes before its first ping (a round trip, since the client pings at once): it is not guarded by the probe and falls back to the transport bound, which is today's behaviour, not a regression.
- **P3:** `G = (L + 1) I + DMAX` is the exact boundary, verified at three parameter sets with `G - 1` violated each time. The constants: **I = 10 s, L = 2, DMAX = 5 s, G = 35 s**; a client that vanished after a ping is freed within `DMAX + G + 1 s` = 41 s (the tie rule's second), under the transport bound B = 45 s, which stays the later bound for the legs it covers. The model's tie rule is "cut at the first tick past the deadline", which the code's G + 1 s mirrors.
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

- Node: a call that opted in is confirmed with the header and an id that a ping accepts at once; a call that did not is not; a refusal is not confirmed; **no ping, no guard**: a confirmed call whose client never pings is never cut; after the first ping a silent client has its call cancelled and its slot released after G + 1 s with the log cause `probe`, the counter `gone`, a terminal `probe_lapsed` frame; a client that pings is never cut; the tie: a ping on the tick the grace ends wins, a ping after the cut is `404`; a ping with a wrong, malformed, upper-case or finished id, with another credential **of the same class** and with a main token for a shared-token call is `404` and **does not refresh the guard** (the call is still cut on schedule); no credential is `401` and an open node's anonymous caller `403`; a certificate name and a main token ping their own calls; a ping after a normal completion is `404`; the slot is released exactly once when the guard and the completion fall together, shown by a second held call of the same credential keeping its own slot; the clock is monotonic (a wall-clock step does not cut a pinging call); the route is in the OpenAPI document.
- Client: asks with the header; pings at once and then at I while the stream runs, with the call's credential and the mount prefix of a relay, through the row's own HTTP client (the proxy request class of `features/provider_proxy.feature`); stops at the end, at the context's cancellation and at `404 unknown_call` only; a ping that fails, hangs or answers 5xx, 401 or a `404` without the code never ends the call and is retried; a node that does not confirm, or confirms in a form this client does not read, gets no ping; the interval has a floor.
- Relay: a scoped client's ping passes the table for a listed node only; an unlisted node, another method, another path are refused like an unknown route; the id header reaches the node; no slot, token or counter row is spent; the full class passes.
- Feature: a client behind an acknowledging intermediary vanishes after its first ping: the slot is free within the bound; a path that carries no ping cuts nothing.

## 6. Risks

- **A ping path that works and then stops** (a proxy that starts filtering, a node restart that forgets the ids: the client gets `404` and stops pinging) cuts the call after G: the guard cannot tell it from a vanished client, and the cut says why in the log and in the terminal frame. A node whose HTTP subsystem is rebuilt in place during a call behaves so too; the rebuild already cuts what is in flight.
- **A node behind a balancer that does not stick** sends the first ping to one instance and the next to another: the second answers `404`, the client stops, and the first instance cuts the call after G. The probe needs the pings to reach the instance that runs the call, as the stream does; the documentation says so.
- The id is a capability in a request header: it is as private as the call, and a header, unlike a path, is not in the logs of a proxy or of the relay's forwarding errors.
