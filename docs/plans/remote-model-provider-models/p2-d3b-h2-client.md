# Model check p2-d3b-h2-client: dilemma O1, the slot of a vanished client that speaks HTTP/2 to a TLS relay

Plan sections: `docs/plans/remote-model-provider-phase2.md` 1 (goal C), 5.2 ("Through a relay"), 7.3, 7.5 (note 5a), 7.6 (D3 residual), 9;
follow-up of `p2-d3-vanished-peer.md` (its kernel-connection abstraction is reused). Model: `p2-d3b-h2-client.pml` (engine `mcd 0.2.0`, CLI,
explicit-state, `mcd parse` gives no warnings; the CLI has no simulate and no lint, so properties were classified by hand; sha256 prefix
`d351b047314f5767`). Code read (not edited): `internal/llm/transport.go`, `provider.go:297`, `model_list.go`, `coddy.go`; `external/swarm/{serve_swarm,mount,tunnel}.go`;
`internal/swarm/tunnel.go`; `internal/httpx/server.go`; `external/httpserver/shared_models_http.go`; Go 1.26.8 `net/http` (`HTTP2Config`, `Protocols`, `httputil/reverseproxy.go`)
and `golang.org/x/net@v0.58.0/http2/server.go`. One throwaway kernel experiment (section 7) lives in the scratchpad, not in the repository.

## 1. Summary

| Group | Verdict on the model |
|---|---|
| Facts (code) | The local Coddy's provider transport enables h2 with pings 30 s / 15 s (`transport.go:44-56,294`); the relay's TLS listener (`ListenAndServeTLS`, `serve_swarm.go:82-87`) negotiates h2 with any client that offers it and has **no** read-idle timeout, so nothing in the relay notices a silent h2 peer before the kernel gives up. Node tunnel dials are already pinned to `http/1.1` (`internal/swarm/tunnel.go:210`). |
| (d) propagation | CONFIRMED and **not the gap**: when the relay learns that its downstream is dead, `req.Context()` ends, the upstream request is cancelled and the node frees the slot in the same tick (model `PROP=1`, real run 0.00 s). With `PROP=0` no option helps. The hole is the relay's *detection* of a silent peer. |
| (b) hole | The slot is held until the call ends by itself or the relay's kernel gives up on the client socket: **H + first retransmission instant >= RETR2** (scale A: 25 of B = 6; real: about 15.4 min + 15 s at `tcp_retries2 = 15`). `late` **violated**. |
| (a) relay-wide h2 ping | Bound B = R + TP holds for any h2 client (`late` verified, tight) but a quiet **browser SSE** through the relay is cut by a silent link of more than the last retransmission instant before TP (15 s of 45 s at RTO 1 s, 30 s at RTO 2 s; counterexample), where today it survives minutes. Real run: 5 s outage cut, 3 s survived with ping 2 s / 3 s. **REJECTED.** |
| (c) h1-only listener | Bound holds (per-call option on every client) and no SSE is cut, but ALPN is per connection: every browser stream of the relay drops to HTTP/1.1 (six connections per origin, no multiplexing). Not modelled (arithmetic of a browser cap). **REJECTED.** |
| (e) the local Coddy offers only `http/1.1` to a `coddy` remote (hybrid with the relay's per-call option of p2-d3) | `late` verified (scale A, B, C, with provider end and outage), `cutlive` as p2-d3, **no effect on the relay's other routes** (`ssecut` verified), option restored on every exit. Mutants fail: relay without the option, a non-Coddy h2 client (hole = (b)). **DECIDED.** |
| (g) connection-scoped option on an h2 connection (refcounted) | Bound holds for any h2 client, but a stream that shares the connection shares the call's tolerance (`ssecut` violated, `SHARE=1`) and the refcount is load-bearing (`REFC=0` violated, `REFC=2` leaks). Kept as the fallback if a non-Coddy h2 client ever matters. |

DECISION: **(e)** plus the documented residual **(b)** for a client that is not the local Coddy and speaks h2 to a TLS relay (logged by the relay mount). No relay-wide h2 ping, no h1-only
listener, no new constant: B = H + U = R + TP = 45 s unchanged. Owners: `internal/llm` (S2) for the transport, `external/swarm/mount.go` (S4) for the log and the tests. Section 9. No fairness is used (bounded-time safety only).

## 2. Intake card

- **Boundary.** One shared call (slot, credential counter, heartbeat every H) made by a client through a relay, plus one browser SSE stream through the same relay. Inside: the client
  (alive-quiet or vanished), one network outage, the relay's two kernel sockets (to the call's client, to the browser), the relay's h2 health check (a), its per-call or per-connection option,
  the proxy's cancel of the upstream request, the node's handler (slot, context, deferred release and restore), the kept-alive connection. Outside: the relay-to-node leg (p2-d3), admission and `busy`,
  the provider, TLS, flow control, an acking intermediary, the browser's own behaviour.
- **State.** `clk`; `cli_v vanished tv`; outage state; per socket `kua kage kint kat kuto knw kdead deliv`; `phase hbage inuse rel_cnt cause nact oc_alive`; `fr_age ping_out ping_age ping_q hclosed`;
  `dsdead relay_cancel`; latches `late cutlive ssecut leak badrel`.
- **Atomic step.** One tick of `World`: kernel timers, node heartbeat forwarded to socket 0, the browser keepalive on socket 1, the h2 check of each connection, provider end, other-call end, the
  relay learning a dead downstream and cancelling upstream, the node's detection and cleanup, next request, then the environment (vanish, outage start or end) at the **end** of the tick.
- **Class.** Finite, untimed Promela with a **discrete-clock abstraction** (one tick, declared; not a timed automaton). Horizon `TMAX = TVMAX + B + 2`. Bounded-time safety, so no fairness. States 185 to 1.05 M.
- **Assumptions.** A1 zero delay on the wire; A2 heartbeat gap exactly H, a quiet peer sends no frame (worst case for a ping); A3 retransmissions at RTO0, 3 RTO0, 7 RTO0, ... (cap `RTOCAP`),
  abort at the first timer with age >= user timeout (`CLAMP=1` also at age U, Linux 7.0) or >= `RETR2` (the kernel's own limit); A4 a retransmit that finds the path up acks everything; A5 one outage, one vanish;
  A6 the relay forwards each node heartbeat at once and a cancel reaches the node in the same tick (relay-to-node leg healthy); A7 the browser answers a ping at once; A8 no acking intermediary.
- **Scales.** A: H=2 B=6 U=4 R=2 TP=4 RTO0=1 RETR2=18; B: H=3 B=9 R=3; C: tick = 1 s, H=15 B=45 R=15 TP=30 `RTOCAP`=64 (`RETR2` = 3B, not the real 925 s: hole sizes are formulas, section 5).

## 3. Mapping the options to the model

| Option | Model |
|---|---|
| (b) accept / (d) rely on propagation | `MECH=0`: only the kernel's `RETR2` on socket 0; `PROP=1` the proxy cancels upstream when its downstream dies, `PROP=0` the mutant |
| (a) h2 health check on the relay's TLS listener (`http.Server.HTTP2 = &http.HTTP2Config{SendPingTimeout: R, PingTimeout: TP}`, Go 1.26) | `MECH=1`: ping after R silent ticks, close TP ticks after an unanswered ping, on **every** h2 connection (call's client and browser) |
| (c) relay listener without h2 | `MECH=2`: the client speaks h1, relay's per-call option U = B - H (`RELOPT=1`); browser connection is h1 and per call, so untouched |
| (e) the local Coddy offers only `http/1.1` | `MECH=3`: as (c) for the local Coddy; `OTHER=1` a client that is not the local Coddy (speaks h2, nothing applies); `RELOPT=0` relay without the option |
| (g) option held on the h2 connection while a shared call is active | `MECH=4`: `kuto[0] = U` on the connection, `nact` calls active (`OTHERCALL=1` a second call of the client), `REFC=1` restore at the last end, `0` at the first, `2` never; `SHARE=1` the browser stream rides that connection |
| `UserTimeoutFor` refusing h2 | `OPTCALL = CP1 && RELOPT`: the per-call option exists only for an h1 client |
| browser SSE that must survive a quiet link of Q | `SSE=1`, `ssecut`, outage up to `XOUT` = Q ticks, keepalive every `HS` ticks (data in flight: worst case for the kernel) |

## 4. Properties (LTL `[] (x == 0)` over latches; sanity as CTL `EF`; the model's own `deadlock` is in every run and verified in every row)

| id | text | class / logic | latch |
|---|---|---|---|
| P1 | the slot is freed by the end of tick tv + B | safety (bounded time) / LTL | `late` |
| P2 | a detector never ends the call of a client that has not vanished (outage <= `XOUT`) | safety / LTL | `cutlive` |
| P3 | the next request on the kept-alive connection sees the option at 0 | safety / LTL | `leak` |
| P4 | the credential counter is released at most once | safety / LTL | `badrel` |
| P5 | the browser SSE (alive peer) is not cut by an outage up to Q | safety / LTL | `ssecut` |
| S | detection after a vanish reachable; provider end without detection; outage survived by a live stream; next request; other call ends while the call runs | reachability / CTL `EF` | `fired`, `phase`, `out_st`, `nreq`, `oc_alive` |

## 5. Results

All rows `exhaustive` and `complete`; `verified` is V, `violated` is X (with a counterexample, except a CTL `EF` that says "no reachable state"). State counts 185 to 1.05 M (scale C, (a) with outage). Row ids are the ones of section 9.

**P1, the bound per mechanism (scale A, B = 6, H = 2, U = 4, R = 2, TP = 4 unless noted).**

| row | variant | `late` |
|---|---|---|
| L0 L0p | (b) `MECH=0`; with `PROV=1` (the provider may end) | X, X: a provider that never ends exists in the nondeterminism |
| L0a..L0f | (b) hole size, `RETR2=18`: B = 22, 23, 24, 25, 26 | X X X V V: bound **H + 23 = 25** (retransmit instants 1, 3, 7, 15, 23; the first >= 18) |
| L0g | (b) `RETR2=60`: B = 64 / 65 | X / V: bound H + 63 = 65 (instants ... 55, 63) |
| L1 | (a) `MECH=1`; R=2 TP=4 / R=4 TP=2 / R=6 TP=12 B=18 / scale B / scale C R=15 TP=30 B=45 | V V V V V |
| L1t | (a) tight: R=2 TP=3 B=5 / R=2 TP=4 B=5; B=17 R=6 TP=12; scale C B=44 | V / X; X; X |
| L1b | (a) with the SSE stream, provider end, outage 2 and 3 | V (26 k and 36 k states) |
| L2 | (c) `MECH=2`; U=3 B=5; scale C with provider end (same path as L3) | V V V |
| L2t | (c) tight: U=3 B=4 / U=2 B=4; scale C B=44 U=30 | X / V; X |
| L3 | (e) `MECH=3`; scale B; scale C with provider end; with provider end and outage 2 | V V V V |
| L3m | (e) mutants: `RELOPT=0` (relay without the option) / `OTHER=1` (client not the local Coddy) / `CLAMP=0` | **X** / **X** / X (older kernel, as p2-d3 A3) |
| L3h | (e) `OTHER=1`, `RETR2=18`, B = 24 / 25 | X / V: the hole of a foreign h2 client is exactly L0 |
| L4 | (g) `MECH=4`; `OTHERCALL=1` `REFC=1`; scale B; scale C | V V V V |
| L4m | (g) `OTHERCALL=1 REFC=0`; tight U=3 B=4 | **X**; X |
| L5 | propagation mutant `PROP=0` for `MECH` 0 (B=25), 1, 2, 3, 4; with `EF (freed == 1)` | X in all five; `EF` violated in all five: the slot is never freed in the horizon |

**P2, what a live client survives (`cutlive`, `XOUT` = longest outage; boundary = the largest verified).**

| mechanism, scale | verified up to | violated from | reading |
|---|---|---|---|
| (a) (c) (e) (g), A (U = TP = 4, RTO0 = 1) | 3 | 4 | last retransmission instant before U (or TP): 1, 3, 7 |
| (e) and (a), C, RTO0 = 1 (U = TP = 30) | 15 | 16 | instants 1, 3, 7, 15 |
| RTO0 = 2: U = 30 (p2-d3, same code) / TP = 30 (here, row `SSEC_a1530_rto2`) | 14 / 30 | 15 / 31 | instants 2, 6, 14 / 2, 6, 14, 30 |
| no outage at all, (a) (e) (g), also with a provider end | no cut | none | `cutlive`, `ssecut` verified with `XOUT=0` |

**P5, the cost to a quiet browser SSE (`ssecut`, `CALL=0 SSE=1`, `XOUT` = Q).**

| mechanism, scale | verified up to | violated from | reading |
|---|---|---|---|
| (a) A (R=2 TP=4) | 3 | 4 | the ping waits behind the unacked keepalive for a retransmission |
| (a) B (R=3 TP=6) | 3 | 4 | |
| (a) C R=15 TP=30, RTO0 = 1 / RTO0 = 2 | 15 / 30 | 16 / 31 | **16 s of silence cut a live browser stream** (31 s at RTO 2 s), against B = 45 |
| (a) C R=30 TP=15, RTO0 = 1 | 15 | 16 | same tolerance, less margin per ping |
| (a) C R=5 TP=40, RTO0 = 1 / 2 | 31 / 30 | 32 / 31 | the best split tried at B = 45 tolerates 0.69 B: **tolerance < B by construction** (a silent link as long as the bound is indistinguishable from a vanish) |
| (a) relaxed bound B = 18 (R=6 TP=12) | 7 | 8 | tolerance is the last instant <= TP: 0.39 B |
| (b) (c) (e) (g `SHARE=0`), A, outage 2, 3, 4, 6, 8 | 8 (all tested) | none | the kernel never aborts inside the horizon; `EF (out_st == 2 && ssecut == 0)` verified: the outage happens and is survived |
| (b) at C, `RETR2=135`, outage 40 and 44 (the others share the code path) | 44 | none | |
| non-vacuity: (b) with `RETR2=10`, B = 20 | 7 | 8 | the property fails when the kernel limit is inside the horizon |
| (g) `SHARE=1` (browser stream on the call's connection) | 3 | 4 | shares the call's tolerance: the connection option is connection-wide |

**P3 restore** (`leak`): (e) `RESTORE=1` V; `RESTORE=0` X; `RESTORE=2` (success path only) X. (g) `REFC=1` V (also with a second call); `REFC=2` X. `EF (nreq == 1)` verified in all.
**P4 once** (`badrel`): guarded release V with one or two release paths and unguarded with one; unguarded plus a second path X (inherited p2-d3 D1 to D4).
**Sanity.** `EF (fired == 1 && vanished == 1)` verified for (a), (c), (e), (g); `EF (phase == 1 && cause == 6 && vanished == 0)` (a provider end after no vanish) verified; `EF (oc_alive == 0 && phase == 0)` (the other
call ends while the first runs) verified.

## 6. Facts from the code, and the reading of (d)

1. **Client.** `newProviderTransport` clones the default transport and calls `enableHTTP2Liveness` (`http2.ConfigureTransports`, `ReadIdleTimeout` 30 s, `PingTimeout` 15 s). `NewProvider` builds one client through `providerHTTPClient` for **every** provider type, `coddy` included (`provider.go:297`); the listing of a `coddy` row uses `HTTPClientForProviderProxy` (`model_list.go:120`).
2. **Relay listener.** `httpx.NewServer` sets `ReadHeaderTimeout`, `IdleTimeout` 120 s (an h2 connection with no stream), no `ReadTimeout`/`WriteTimeout`, no `HTTP2` config; `ListenAndServeTLS` enables h2 by default. x/net `serverConn` arms its ping only when `SendPingTimeout > 0`
   (`server.go:818-821`), pings when no frame was read for that long, and closes the connection when the ping is not answered within `PingTimeout` (`handlePingTimer`, `server.go:915-942`, ack in `processPing`, `:1484`). Go 1.26 exposes both as `http.Server.HTTP2` (`SendPingTimeout`, `PingTimeout`, `WriteByteTimeout`): (a) is five lines. `WriteByteTimeout` fires only when a write blocks (kernel buffer full), never for a heartbeat: the same verdict as O6 of p2-d3.
3. **A tunnel does not need h2 on the relay's listener.** The node dials with ALPN `http/1.1` and upgrades (`internal/swarm/tunnel.go:210`, `external/swarm/tunnel.go:86-125`): an h1-only listener (c) would not break it. The relay's own transport to a direct node has `ForceAttemptHTTP2` (`external/swarm/transport.go:49`), irrelevant for the client leg.
4. **(d), the reading.** `ReverseProxy.ServeHTTP` takes `ctx := req.Context()` and sends `outreq := req.Clone(ctx)` (`reverseproxy.go:409,435,564`); the mount sets no other context (`mount.go:162-196`, `Rewrite`, `FlushInterval: -1`). The downstream request context ends when the h2 stream is reset (`processResetStream` -> `st.cancelCtx()`,
   `server.go:1553`), when the h2 connection ends (`closeAllStreamsOnConnClose` -> `closeStream` -> `st.cancelCtx()`, `:737-741,:1601`) and, for h1, when the connection's background read fails. The upstream round trip then returns, `copyResponse` fails and `res.Body.Close()` runs (`:603-615`): a reset stream on the tunnel, a closed connection on a direct dial. On the node, `callCtx` derives from `r.Context()` and
   `defer slot.release()` runs (`shared_models_http.go:270,607,655`). So a node frees the slot in one round trip **once the relay knows**. What decides the hole is what makes the relay know: only the kernel's abort of its socket (writes of a few heartbeat bytes always succeed into the send buffer; the client's announced 4 MiB stream window (`transportDefaultStreamFlow`) is never exhausted by them; flow-control back-pressure
   needs the kernel buffers, megabytes, to fill) or the call's own end.
5. **How long the call lasts by itself.** A streaming row ends with its provider's final or error frame, or `S` after the last chunk (`startSharedStallGuard`); a blocking row is bounded by `max_call_ms` (default 30 min, `shared_models.go:24,123`). A vanished client therefore costs the lender the rest of the provider's work, and the credential up to five slots, until one of these or the relay's kernel (about 15.4 min at `tcp_retries2 = 15`, the kernel documentation; **[unverified here]**, the experiment below ran 5).

## 7. A kernel experiment (qualitative check of the model, Linux 7.0.0-34, loopback)

Throwaway Go program in the scratchpad (not committed; the opt-in kernel test of 5.4 can reproduce it): a client, a TLS relay (`httptest` with h2, `httputil.ReverseProxy`, `FlushInterval: -1`), a plain node streaming a heartbeat every 0.5 s; inside `unshare -Urn`, `iptables -I INPUT -p tcp --dport <client port> -j DROP`
makes the client vanish (the relay's segments are never acknowledged); the node reports when its request context ended. `tcp_retries2 = 5` where the kernel limit is the point (12.6 s of backoff), 15 elsewhere.

| client | relay variant | node context ended after the vanish |
|---|---|---|
| h2 | default (no option, no ping) | **13.94 s** (the kernel's limit 12.6 s + heartbeat gap + timer granularity): no earlier detector |
| h1 | default | 13.42 s |
| h2 | per-call option for h1 only (the p2-d3 mount) | **not freed** in 60 s (`tcp_retries2 = 15`): per-call refused for h2, "unprobed" |
| h1 | per-call option U = 3 s | **3.93 s** (U + heartbeat 0.5 s + 0.4 s) |
| h2 | relay-wide ping 2 s / 3 s (a) | **5.00 s** (R + TP) |
| h2 | connection-scoped option U = 3 s on the h2 connection (g) | 3.42 s: `ConnContext` reaches h2 streams, the `*tls.Conn` unwraps |
| h1 or h2 | abrupt close of the client connection (the relay learns at once) | **0.00 s** (propagation) |
| ALPN | `Protocols` = HTTP/1 only vs `ForceAttemptHTTP2` | the relay sees `HTTP/1.1` (no `h2` negotiated) vs `HTTP/2.0` |
| cost of (a), live quiet h2 client, outage X then the link returns | relay ping 2 s / 3 s: X = 1, 3 survived; **X = 5, 8 cut at 5.00 s**; default relay: all survived | the model's P5 on a real kernel (tolerance 3 s = the last retransmission instant <= TP = 3 s) |

## 8. Decoded counterexamples and classification

- **CEX-1 `late`, (b) `MECH=0` (L0).** The client vanishes at the end of tick t; the next forwarded heartbeat is the first unacknowledged byte; retransmits at ages 1, 3, 7, 15 are lost; the kernel aborts at age 23 (the first timer >= `RETR2` = 18); the relay learns, cancels, the slot is freed
  at tv + H + 23. *System defect (spec gap) of the plan's 5.2: "the node's slot is freed within B of the client's disappearance too" holds for an h1 client only.*
- **CEX-2 `ssecut`, (a) (SSE_M1_X4, decoded with `--bfs`).** R = 2, TP = 4, outage 4 ticks (2 to 5), link back at tick 6. Tick 2: the keepalive is the first unacknowledged byte; tick 3: the relay's ping (after R silent ticks) is queued behind it (`ping_q`); tick 5: the retransmit at age 3 is lost, the next
  would be at age 7 (tick 9); tick 7: `ping_age` reaches TP = 4 and the relay closes the connection of a browser that has been reachable since tick 6. *System defect of option (a): the cost the brief expected; the same defect P2 shows for a call, here on a route that today survives.*
- **CEX-3 `late`, `OTHERCALL=1 REFC=0` (L4m).** A second call of the same client ends first (its provider is done), the connection option is restored at that end, the vanished client's first call is left with the kernel's limit. *Design defect of a restore per call on a shared connection: the refcount is required.*
- **CEX-4 `late`, `PROP=0` (L5).** The relay learns the connection is dead and does not cancel the node's request: every mechanism frees nothing. *Mutant of the proxy; the `mount.go` test row below keeps the propagation pinned.*
- **CEX-5 `late`, `RELOPT=0` and `OTHER=1` (L3m).** (e) without the relay's per-call option, or with a client that offers h2, is the hole of (b). *Not a defect of (e): the two preconditions, one of them a documented residual.*
- **CEX-6 `leak`, `RESTORE=0` / `RESTORE=2` / `REFC=2`.** The kept-alive connection starts the next request with the option set (nothing restored; restored only after an OK end, not after an `error` frame; never restored on the connection). *As p2-d3 CEX-2 and CEX-3.*
- **CEX-7 `ssecut`, (g) `SHARE=1`.** A stream that shares the connection with an active shared call is cut at the call's tolerance. *System defect of (g) in scope, harmless only while no foreign stream shares a Coddy client's connection.*

## 9. Spec statements: CONFIRMED / CONTRADICTED / NOT DECIDABLE

CONFIRMED: the relay's per-call option frees the slot within H + U for an h1 client of the relay (L2, L3, kernel 3.93 s of U = 3 s + 0.9 s) and the propagation to the node is immediate (L5 vs L3; 0.00 s); a relay-wide h2 ping gives R + TP for any h2 client (L1, 5.00 s) and
a connection-scoped option gives H + U for an h2 client when refcounted (L4, 3.42 s); with nothing at the relay the hole is H + the first retransmission instant >= `RETR2` (L0a..L0g) and ends with the call's own end; (c), (e) and (g without a shared stream) leave the quiet SSE alone (P5 rows); one guarded release (P4);
restore on every exit (P3).
CONTRADICTED: "a client behind a relay is bounded because the relay applies O1" (plan 5.2) **for an h2 client** (L0, L3m `OTHER=1`, kernel row 3): `UserTimeoutFor` refuses an h2 stream and the relay's TLS listener negotiates h2 with any client that offers it; "a live client whose silence is shorter than the bound
is never cut" (again, on this leg: 15 to 30 s of 45 s, P2); "(a) is a connection-level health check mirroring T1 on the client leg" as a free choice: it applies to every h2 connection of the relay, browsers included (P5: 16 s of silence cuts a stream that survives minutes today; the best split of B = 45 tolerates 31 s).
NOT DECIDABLE here: how a real browser or an intermediary answers h2 pings behind a stall **[unverified]**; the cost of (c) for a browser (six connections per origin and no multiplexing is a property of browsers, not modelled); a non-Coddy h2 client behind an acking intermediary (uncovered on every platform, p2-d3); macOS, Windows, BSD kernels;
the real figure at `tcp_retries2 = 15` (the experiment ran 5; the model's `RETR2` is a parameter); a future Coddy client that re-enables h2 (a guard test pins the ALPN).

## 10. DECISION

| item | decision | evidence |
|---|---|---|
| client of a TLS relay that is the local Coddy | **(e)**: a `coddy` row's HTTP client (completions, listing, usage) offers only HTTP/1.1; the relay's per-call option of p2-d3 then applies; constants unchanged (B = 45 s, U = B - H) | L3, L3m, P5 rows, kernel rows 4 and 8 |
| not (a) | listener-wide: cuts quiet browser streams at 15 to 31 s of silence where today they survive minutes; bound R + TP, tolerance below B by construction (0.69 B for the best split tried) | P5 rows, CEX-2, kernel row 9 |
| not (c) | the same bound, but every browser stream of the relay loses h2; the local Coddy's change of (e) achieves the same ALPN outcome without touching the relay | facts 3, arithmetic |
| (d) | not a mechanism: the propagation exists and is immediate, it is the relay's detection that is missing | L5, kernel rows 1, 7 |
| not (g) | covers foreign h2 clients but adds a connection-scoped refcounted primitive that shares the call's fate with every co-resident stream; the fallback if a foreign h2 client matters | L4, L4m, CEX-3, CEX-7 |
| foreign h2 client of a TLS relay | **residual, documented and logged**: the call is served, unprobed; held until the call ends or the relay's kernel gives up (about 15.4 min, unverified) | L0, L3m, kernel row 3 |
| refusing h2 on the completions route | considered, **not decided for**: it turns the hole into an error but breaks `curl` (h2 by default) and h2 intermediaries that are uncovered anyway; a possible later switch | no model content: no call, no slot |

**Owner and files.** S2 (`internal/llm`): `transport.go` (an HTTP/1-only transport for `coddy` rows: `Protocols` HTTP/1 only, no `enableHTTP2Liveness`, its own cache key so `providers[].proxy` still applies), `provider.go:297` (the call line: `p.Type == "coddy"` takes it), `model_list.go` (the coddy listing),
tests. S4 (`external/swarm/mount.go`): the log line and the tests. S10: the documentation page. No other file moves; no config key, no flag, no wire change.

## 11. Recommended spec changes (replacement text, `docs/plans/remote-model-provider-phase2.md`)

**1, goal C, replace the bullet by:**

> - **C. Liveness of a vanished peer.** A client (or a relay) that disappears without a FIN or a RST while only heartbeats are written frees its slot within 45 s of its disappearance, on the leg the node sees, on a direct listener and on a tunnel. A client that vanishes behind a relay frees it as well when it speaks HTTP/1.1 to the relay, because the relay applies the same per-call option to its client connection (5.2); the local Coddy offers only HTTP/1.1 to a `coddy` remote on purpose, so every shared call the product makes is covered. A client that is not the local Coddy and speaks HTTP/2 to a TLS relay is bounded only by the call's own end and the relay's kernel, a documented residual (7.6).

**1, non-goals, append to the first line:** "a relay-wide HTTP/2 health check and an HTTP/1.x-only relay listener (both would reach every browser stream of the relay, 5.2)".

**5.2, in the first paragraph, replace** "One mechanism per leg: no listener-wide option, no `TCP_INFO` polling, no application probe, no user timeout on the tunnel socket (7.3)." **by:** "One mechanism per leg: no listener-wide option, no `TCP_INFO` polling, no application probe, no user timeout on the tunnel socket, no health check or protocol restriction on the relay's TLS listener (7.3)."

**5.2, in the `internal/httpx` bullet, replace** "that call goes on unprobed on this leg (the tunnel check below covers the tunnel)" **by** "that call goes on unprobed on this leg (the tunnel check below covers the tunnel; a relay's h2 client is the case of 'Through a relay')".

**5.2, replace the paragraph "Through a relay." by:**

> **Through a relay.** A relay that dials the node reaches the direct listener over a pooled HTTP/1.1 connection (the per-call option applies to it); a node that dialled out is the tunnel case; either way the node's peer is the relay. **A client that vanishes behind a relay is invisible to the node** (the relay acknowledges and answers everything) and would hold its slot until the relay's own request ends. The relay's mount (`external/swarm/mount.go:162`, `httputil.ReverseProxy`, `FlushInterval: -1`) therefore sets the same per-call option on the client's connection for `POST /swarm/nodes/{node}/coddy/llm/completions` (that route only), `U = B - H` from `httpx`, with a deferred restore. When the relay learns that the client's connection is dead (the option's abort, a reset, a close) the proxy's `req.Context()` ends, the upstream request is cancelled (a reset stream on a tunnel, a closed connection on a direct dial) and the node frees the slot in the same round trip; the propagation is not the gap, the relay's detection of a silent peer is (a write of heartbeat bytes always succeeds into the send buffer, and the relay's h2 server has no read-idle timeout).
> **The option exists only on an HTTP/1.x connection**: `UserTimeoutFor` refuses an h2 stream (one TCP connection carries many streams), and the relay's TLS listener (`serve_swarm.go:82-87`) negotiates h2 with any client that offers it. Decided (model `p2-d3b-h2-client`):
> 1. **The local Coddy offers only `http/1.1` to a `coddy` remote.** A `coddy` row's HTTP client (completions, listing, usage) is built by `NewProvider` / `ListModels` from a transport that sets `Protocols` to HTTP/1 only and does not call `enableHTTP2Liveness`, with its own cache key in `providerTransport` so `providers[].proxy` applies exactly as for every other row; other provider types keep their transport byte for byte. The relay (and a direct listener) then sees one HTTP/1.1 connection per call, which the per-call option covers; the client's own liveness of the relay rests on the heartbeat and the stall guard (m5, `StreamIdleTimeout`), not on h2 pings. A test pins the ALPN so a later change cannot re-enable h2 unnoticed.
> 2. **The relay's listener keeps h2 and gets no health check.** ALPN is per connection and a ping per connection: an h2 ping at 15 s / 30 s cuts a quiet browser stream through the relay on a silent link of more than about 15 to 30 s (a quiet stream survives minutes today, model rows `ssecut`, real run: 3 s survived and 5 s cut with ping 2 s / 3 s), and an HTTP/1.x-only listener takes multiplexing from every browser stream of the relay (six connections per origin).
> 3. **A client that is not the local Coddy and speaks h2 to a TLS relay is not probed.** The mount serves the call, `UserTimeoutFor` returns `ErrUserTimeoutUnsupported`, and the mount logs once per connection at info level `shared call over HTTP/2: a vanished client is not detected before the call ends`. The slot is then held until the call's own end (final or error frame, `S` after the last chunk for a stalled provider, `max_call_ms` for a blocking row) or until the relay's kernel gives up on the client's socket (about 15.4 minutes at `tcp_retries2 = 15` **[unverified here: the experiment ran 5]**). The credential's other slots are not affected, and the page says so.
> A client behind an intermediary that acknowledges for it (a TLS terminator, an HTTP proxy) stays uncovered on every platform, as before.

**5.4, add:** "`internal/llm`: a `coddy` row's requests (completions, listing, usage) reach a TLS server that offers h2 as `HTTP/1.1` (`r.ProtoMajor == 1`, no `h2` negotiated); a row of another provider type still negotiates `h2` and carries the liveness pings (the other types byte for byte); the h1 transport honours `providers[].proxy` (`features/provider_proxy.feature`, `TestProviderProxyGuard`). `external/swarm` (`mount.go`, `-tags http,swarm`): an h1 client of a TLS relay gets the option `B - H` during the call and 0 after it (every exit); an h2 client (`http2.Transport` against the relay's listener) is served, the option stays 0, one log line per connection; **the propagation**, for an h1 and for an h2 downstream: closing the client's connection while heartbeats are forwarded ends the node handler's context within 100 ms and `sharedLimit.inUse` is 0, and a downstream whose connection returns `ETIMEDOUT` from `Write` does the same. Opt-in kernel test (`CODDY_TEST_NETNS=1`): an h1 client behind a TLS relay in `unshare -Urn` with `iptables` DROP frees the node's slot at U after the first unacknowledged byte; an h2 client in the same setup **keeps** it (a negative test that documents the residual, with the scaled `tcp_retries2`)."

**7.3, D3, append to Decision:** "Client of a TLS relay (follow-up `p2-d3b-h2-client`): the local Coddy offers only HTTP/1.1 to a `coddy` remote so the relay's per-call option applies; a relay-wide h2 ping (cuts a quiet browser SSE at 15 to 31 s of silence), an h1-only relay listener (every browser stream loses h2) and a connection-scoped refcounted option (shares the call's fate with co-resident streams) were rejected; the propagation of a dead downstream to the node is immediate and is not the gap; a foreign h2 client of a TLS relay is a documented residual."

**7.5, note 5(a), replace** "(the model has no h2 client leg, 7.6)" **by** "(the h2 client leg is decided in 5.2 and 7.3: the local Coddy speaks HTTP/1.1)".

**7.6, D3 bullet, replace the bold sentence** "**A client behind a relay whose own connection is HTTP/2 (a TLS relay and an h2 client) is not covered, because `UserTimeoutFor` refuses an h2 stream (5.2): not decided; it needs an h1-only relay listener for that route or a probe.**" **by:** "**A client behind a TLS relay that is not the local Coddy and speaks HTTP/2 is not probed (`UserTimeoutFor` refuses an h2 stream): its slot is held until the call ends or the relay's kernel gives up on the client's socket (about 15.4 minutes at `tcp_retries2 = 15`, unverified; 13.9 s measured at 5), and the mount logs it. Decided against a relay-wide h2 ping, an h1-only listener and a refusal of h2 on the route (5.2, 7.3); a connection-scoped refcounted option is the fallback if such a client matters.** The local Coddy is covered because it offers only HTTP/1.1 to a `coddy` remote."

**8, stage rows.** S2 gets "`internal/llm/{transport.go, provider.go (the call line), model_list.go (the coddy listing)}` + their tests (the `coddy` transport: HTTP/1.1 only, ALPN pinned, proxy honoured)". S4 gets "`external/swarm/mount.go` (completions route only: the option on an h1 client, the info log for an h2 one)". S10 gets "the page's 'Not covered' list: an h2 client of a TLS relay that is not the local Coddy". No new edge in the stage graph (S2 and S4 stay parallel).

**9, add a bullet to Risks** (section 9 has no item named O1: this is the open item the cross-review raised, now decided): "**Clients of a TLS relay.** The relay's per-call option exists only on an HTTP/1.x client connection; the local Coddy offers only HTTP/1.1 to a `coddy` remote (the ALPN is pinned by a test, so re-enabling h2 for that provider fails a test), and a foreign h2 client is the documented residual (5.2). `internal/llm` gets one more transport (cache key by protocol and proxy setting): the other types are untouched, `TestProviderProxyGuard` and the proxy harness cover it. The Go version matters: `http.Transport.Protocols` is Go 1.24 or later (`go.mod` says 1.26)."

## 12. Commands to reproduce

`MC='mcd check --promela docs/plans/remote-model-provider-models/p2-d3b-h2-client.pml --no-timing --budget-ms 280000 --budget-states 9000000'`; scale A is the file default; a row is `$MC -D <defs> --ltl '<f>'` with
`L='[] (late == 0)'`, `C='[] (cutlive == 0)'`, `K='[] (leak == 0)'`, `D='[] (badrel == 0)'`, `S='[] (ssecut == 0)'`; sanity `--ctl 'EF (<cond>)'`.
- L0 `-D MECH=0 L` (L0p `-D PROV=1`); L0a..f `-D MECH=0 -D RETR2=18 -D B=22..26`; L0g `-D RETR2=60 -D B=64|65`.
- L1 `-D MECH=1` (`-D R=4 -D TP=2`; `-D B=18 -D R=6 -D TP=12`; `-D H=3 -D B=9 -D R=3`; scale C `-D H=15 -D B=45 -D RTOCAP=64 -D MECH=1 -D R=15 -D PROV=1`); L1t `-D B=5 -D R=2 -D TP=3|4`, `-D B=17 -D R=6 -D TP=12`, scale C `-D B=44 -D R=15 -D TP=30`; L1b `-D MECH=1 -D SSE=1 -D PROV=1 -D XOUT=2|3` with L, S and C.
- L2 `-D MECH=2` (`-D U=3 -D B=5`); L2t `-D U=3 -D B=4`, `-D U=2 -D B=4`, scale C `-D B=44 -D U=30`. L3 `-D MECH=3` (scale B `-D H=3 -D B=9 -D R=3`; scale C `-D PROV=1`; `-D PROV=1 -D XOUT=2`); L3m `-D RELOPT=0`, `-D OTHER=1`, `-D CLAMP=0`; L3h `-D MECH=3 -D OTHER=1 -D RETR2=18 -D B=24|25`.
- L4 `-D MECH=4` (`-D OTHERCALL=1`, scale B, scale C `-D OTHERCALL=1`); L4m `-D MECH=4 -D OTHERCALL=1 -D REFC=0`, `-D U=3 -D B=4`. L5 `-D PROP=0 -D MECH=0..4` (MECH 0 with `-D RETR2=18 -D B=25`) with L and `--ctl 'EF (freed == 1)'`.
- P2: `-D MECH=1|2|3|4 -D PROV=1 -D XOUT=1..6 C`; scale C `-D H=15 -D B=45 -D RTOCAP=64 -D MECH=3 -D TVMAX=17 -D XOUT=15|16 C` (and `-D MECH=1 -D R=15`).
- P5: `-D MECH=0..4 -D CALL=0 -D SSE=1 -D XOUT=2|3|4|6|8 S` with `--ctl 'EF (out_st == 2 && ssecut == 0)'`; scale B `-D MECH=1 -D H=3 -D B=9 -D R=3 -D CALL=0 -D SSE=1 -D XOUT=3|4|6|9`;
  scale C `-D H=15 -D B=45 -D RTOCAP=64 -D CALL=0 -D SSE=1 -D HS=15 -D MECH=1 -D R=15 -D TP=30 -D RTO0=1|2 -D XOUT=14..16|29..32` (also `-D R=30 -D TP=15`, `-D R=5 -D TP=40`; (b) `-D MECH=0 -D RETR2=135 -D XOUT=40|44`);
  non-vacuity `-D MECH=0 -D CALL=0 -D SSE=1 -D XOUT=7|8 -D RETR2=10 -D B=20`; relaxed `-D MECH=1 -D B=18 -D R=6 -D TP=12 -D CALL=0 -D SSE=1 -D XOUT=6..8`; scope `-D MECH=4 -D SSE=1 -D SHARE=1 -D PROV=1 -D XOUT=3|4|6`.
- P3 `-D MECH=3 -D PROV=1 -D RESTORE=1|0|2 K` and `-D MECH=4 -D PROV=1 -D OTHERCALL=1 -D REFC=1|0|2 K` with `--ctl 'EF (nreq == 1)'`; P4 `-D MECH=2 -D PROV=1 -D XOUT=2 -D RELONCE=a -D EXTRAREL=b D`.
- Decode a counterexample with `--bfs` and read `counterexample.steps` (the `command` field; `clk = clk + 1` opens a tick). The experiment: `unshare -Urn sh -c 'ip link set lo up; sysctl -qw net.ipv4.tcp_retries2=5; ./exp -mode vanish -client h2 -relay default'` (relay `ping|useropt|connopt`, client `h1|h2`, modes `alpn|close|outage -x N`).

## 13. Limitations

Discrete clock, zero wire delay, one call, one browser stream, one outage, one vanish, gap exactly H, a quiet peer that sends no frame (the worst case for a ping and the best for nothing else); the retransmission schedule is the exponential one, validated against one RTO by p2-d3 and by the experiment above, not against a real estimator;
the h2 timer tie and the kernel's tie are chosen as in p2-d3 (each moves a boundary by one tick); the relay-to-node leg is healthy and a cancel reaches the node at once (measured 0.00 s on loopback, not across a real tunnel); the cost of (c) is arithmetic, not state; no acking intermediary, no TLS, no flow control. The model is of the plan's design, not of code that exists:
conformance needs the test rows of 5.4 above. The decisions use `verified` / `violated` rows only; no `inconclusive` row was produced (budget 9 M states, largest run 1.05 M).
