# Model check p2-d3-vanished-peer: dilemma D3, where and how a vanished peer is cut

Plan sections: `docs/plans/remote-model-provider-phase2.md` 2 (W1, W2), 5.1 to 5.4 (area C), 7 (D3), 8 (S4, S5, 8a), 9 (kernel
dependence); `docs/plans/remote-model-provider-models/m5-guards.md` (the three timers; slot hold after a vanished peer). Model:
`p2-d3-vanished-peer.pml` (engine `mcd 0.2.0`, CLI, explicit-state; the CLI has no simulate and no lint, so properties were
classified by hand; sha256 prefix `030152bc9219a4d9`). Code read (not edited): `serve_http.go`, `internal/httpx/server.go`,
`shared_stream.go`, `shared_models_http.go`, `shared_limiter.go`, `internal/swarm/tunnel.go`, `external/swarm/{tunnel,mount,serve_swarm}.go`,
x/net v0.58 `http2/server.go` (the health check). One throwaway kernel experiment (section 6) lives in the scratchpad, not in the repository.

## 1. Summary

| Group | Verdict on the model |
|---|---|
| P1 slot freed within B of the vanish | CONFIRMED for **O1** (B = H + U) and **T1** (B = R + TP, independent of H), exhaustive on three scales; tight (B-1 violated). O2, O3, O4 (cooperating client) meet it too; O5, O6 (uncontrollable), T2 behind a proxy, any option behind an acking intermediary, and a client vanishing behind a relay do **not** |
| P2 no cut of a live peer whose outage is shorter than U | **CONTRADICTED** as worded: the tolerated outage is the **last TCP retransmission instant before U** (about U/2 to U), not U. Measured the same way on Linux 7.0. A 1 s floor for U breaks even one-tick outages |
| P3 option restored before the next request | CONFIRMED with the plan's deferred restore; **violated** without a restore (O1 as such) and with a restore on the success path only (an `error` frame leaves it set) |
| P4 slot released once | CONFIRMED (one guarded release, `shared_limiter.go:119`); a second release path on detection double-counts and takes the other caller's slot |
| P5 no effect on other routes | CONFIRMED for O1 (and O5); **O2 violates** it (a browser SSE is cut by an outage between the tolerance and the kernel's limit) |
| P6 bound with H shortened and U following | CONFIRMED for every H below B with U = B - H; violated when U does not follow, when H reaches B, and (tolerance) when the 1 s floor applies |
| P7 composition through a relay | CONFIRMED when the relay is the vanished peer; **CONTRADICTED** for a client behind the relay unless the relay applies the same per-call option |
| Body deadline P against B | CONFIRMED iff P is at most B (one tick of slack); the tunnel's check frees the body phase too |

DECISION: direct leg **O1** (per-call `TCP_USER_TIMEOUT`, U = B - H, set after the body, deferred restore on every exit); tunnel **T1 only**
with **R = 15 s, TP = 30 s** (not the relay's 30/15); relay mount applies O1 to its client (recommended, one call site). Section 9. No
fairness is used and no result depends on it (bounded-time safety only).

## 2. Intake card

- **Boundary.** One streamed call (`POST /coddy/llm/completions`) of a credential that holds one of its slots, on the node. The node's
  peer is a direct client or a relay (`RELAY=1`); `VLOC` picks which of the two vanishes. Inside: the peer (alive and quiet, or
  vanished: never acknowledges again), one network outage, the node's kernel connections (0 = the call's socket, 1 = a second socket
  with a browser SSE, 2 = the relay's socket to its client), the handler (body phase, heartbeat, provider call, slot, call context, deferred
  cleanup), the h2 health check (T1) or an application probe (O4), the kept-alive connection. Outside: admission and `busy`, the
  provider, content, TLS, flow control, the client's own idle guard (m5), the relay's own pings toward a vanished node.
- **State.** `clk`; peer flags and `tv` (end of the tick in which the vanish happens); outage state and length; per connection
  `kua kage kint kat kuto knw kdead` (unacked data, age of its first byte, backoff interval, next retransmit age, user timeout,
  writes queued, aborted); `phase pre hbage inuse rel_cnt cause`; `fr_age ping_out ping_age ping_q`; latches.
- **Atomic step.** One tick of `World`: kernel timers, handler (heartbeat write), second connection, relay forward, h2 check, provider
  end, detection and cleanup, next request, then the environment (vanish, outage start or end) at the **end** of the tick, so every
  write of the tick was delivered before the vanish and `B` is exactly "last delivered write to slot freed".
- **Class.** Finite, untimed Promela with a **discrete-clock abstraction** (one tick, declared; not a timed automaton). Horizon
  `TMAX = TVMAX + B + 2`. Liveness is asked as bounded-time safety (`late` latch), so **no fairness** is used. States 150 to 7 M.
- **Assumptions.** A1 zero delay on the wire (an ACK is the same tick); A2 the heartbeat gap is exactly H (the worst case for both the
  bound and the outage alignment); A3 the kernel retransmits at RTO0, 3 RTO0, 7 RTO0, ... (doubling, capped by `RTOCAP`) and checks the
  user timeout at each timer, `CLAMP=1` also at age U (Linux 7.0, measured); A4 a retransmit that finds the path up acks everything;
  A5 one outage and one vanish per run; A6 detection and slot release are the same tick; A7 the peer answers a ping in the tick it
  gets it; a quiet relay sends no frames (the worst case for a ping); A8 an intermediary that acks (`PROXY=1`) hides the vanish from the
  kernel but not from an end-to-end ping; A9 the relay forwards every heartbeat at once (`FlushInterval: -1`, `mount.go:168`).
- **Scales** (tick to plan's seconds). A: H=2 B=6 (U=4) R=2 TP=4 RTO0=1; B: H=3 B=9 (U=6) R=3; C: tick = 1 s, H=15 B=45 (U=30) R=15
  TP=30, RTO0 = 1 and 2, RTOCAP=64. Boundaries are the same linear forms at A, B and C (sections 5 and 8).

## 3. Mapping spec and code to the model

| Spec / code | Model |
|---|---|
| `TCP_USER_TIMEOUT` per call (`UserTimeoutFor`, 5.2) | `DIRECT=1`: `kuto[0] = U` at `start_stream`, restored in `end_call` (`RESTORE=1`) |
| option on every accepted socket (O2) | `DIRECT=2`: `kuto[0] = kuto[1] = U` from the start |
| `TCP_INFO` probe (O3) | `DIRECT=3`: cancel when unacked data and `clk - lack >= U`, every `POLL` ticks, no kernel abort |
| application probe the client answers (O4) | `DIRECT=4`: ping after R silent ticks, close TP later; `COOP` (client answers), `STRICT` (unanswered = dead) |
| nothing off Linux (O5) | `DIRECT=5`: `kuto = 0`, the kernel's own limit `RETR2` |
| small send buffer + per-write deadline | `DIRECT=6` (not in the plan): `CAPB` writes fit, a blocked write dies after `WD` |
| node-side h2 `ReadIdleTimeout` / `PingTimeout` (T1) | `LEG=1 T1=1`: `fr_age >= R` sends a ping, `ping_age >= TP` closes; the tunnel's phase against the call is any of 0..R-1 |
| user timeout on the dialled socket (T2); egress proxy | `T2=1` sets `kuto[0]`; `PROXY=1` makes the kernel's ACKs independent of the peer |
| heartbeat gap, bound B, U = B - H | `H`, `B`, `U` (default `B - H`, at least 1) |
| body deadline P (30 s) | `PB`: slot held from tick 0, body done at any tick before `PB`, `PB` ends the call |
| `defer` restore; guarded `slot.release()` | `RESTORE` 1/0/2; `RELONCE`, `EXTRAREL` |
| relay mount, client behind it | `RELAY=1 VLOC=1`: conn 2 is the relay's socket to its client, `RELAYPROBE=1` puts `U` on it |

## 4. Properties (LTL `[] (x == 0)` over latches; sanity as CTL `EF`; the model's own `deadlock` is in every run)

| id | text | class / logic | latch |
|---|---|---|---|
| P1 | the slot is freed by the end of tick tv + B | safety (bounded time) / LTL `[]` | `late` |
| P2 | a detector never ends the call of a peer that has not vanished, for outages up to `XOUT` ticks | safety / LTL | `cutlive` |
| P3 | after an ordinary end the next request on the kept-alive connection sees the option at 0 | safety / LTL | `leak` |
| P4 | the credential's counter is released at most once, never below the other caller's slot | safety / LTL | `badrel` |
| P5 | a second connection (browser SSE) is not cut by the option while its peer is alive | safety / LTL | `c2cut` |
| S | detection reachable after a vanish; provider end reachable; next request reachable; relay cancel reachable; outage survived by a live call; body phase left | reachability / CTL `EF` | `fired`, `phase`, `nreq`, `relay_cancel`, `out_st`, `pre` |

## 5. Results

All rows `exhaustive`; `verified` rows are `complete`, `violated` rows carry a counterexample. State counts: 150 to 220 for bound rows without
outage, 1 k to 20 k with outages, up to 6.6 M at scale C for T1. Row ids are the ones in section 11.

**P1, mechanisms (scale A; B = 6 unless noted).**

| row | variant | `late` |
|---|---|---|
| A1 | O1, provider may end, outage up to 2 (11 926 states) | verified |
| A2 | O1 with B = 5 (U = 4, H = 2) | violated: the bound is tight at H + U |
| A3 A3b A3c | O1, `CLAMP=0` (kernel that does not clamp): B = 6 / 9 / 8 | violated / verified / violated: bound H + first retransmit instant >= U |
| A4 | O2, outage up to 2 | verified |
| A5 A5b A5c | O3 U = 4 B = 6 / U = 6 B = 6 / U = 6 B = 5 | verified / verified / violated: bound U (+ POLL) |
| A6 A6b A6c | O4 `COOP=1 STRICT=0` / `COOP=0 STRICT=1` / `COOP=0 STRICT=0` | verified / verified / **violated** (a legacy client is never probed) |
| A7 | O5, `RETR2` = 18 | violated; I1 I2: bound H + first instant >= RETR2 = 25 (B = 25 verified, 24 violated) |
| A8 | O6 (`CAPB=2 WD=3`): verified at B = 9, violated at 8 | bound (CAPB + 1) H + WD |
| A9 A9b A9c | T1 R=2 TP=4 / B = 5 / R=4 TP=2 (the relay's 30/15 ratio) | verified / violated / verified |
| A10 A10e | T2 only / T1 + T2 | verified / verified |
| A10b A10c A10d | `PROXY=1`: T2 only / T1 only / both | **violated** / verified / verified |
| A11 A11b | direct leg behind an acking intermediary: O1 / O4 `COOP=1` | **violated** / verified |
| I3 I4 | T1 with H = 1 and H = 5 (B = 6) | verified: T1's bound does not depend on H |
| H1 H2 | scale B: O1 (H=3 B=9), T1 (R=3) with outages and provider end | verified |
| K1 K2 K3 | scale C: O1 (H=15 B=45; 55 273 states), T1 R=15, T1 R=30 | verified |

**P2, outage tolerance** (`cutlive`, `XOUT` = longest outage, boundary = the largest verified).

| mechanism, scale | verified up to | violated from | predicted last retransmit instant |
|---|---|---|---|
| O1, A (U=4, RTO0=1) | 3 | 4 | 3 (instants 1, 3 < 4) |
| O1, B (H=3 B=9, U=6) | 3 | 4 | 3: **outages of 4 and 5 ticks < U cut the call** |
| O1, A, RTO0=2 | 2 | 3 | 2 |
| O1, `CLAMP=0` | 3 | 4 | 3 (the abort still comes before the retransmit) |
| O1, C (U=30), RTO0=1 / RTO0=2 | 15 / 14 | 16 / 15 | 15 / 14 (instants 1,3,7,15 / 2,6,14) |
| O3 (U=6=B), O4, T2 | 3 | 4 | same as O1 at equal B |
| T1 R=2 TP=4 (scale A) / R=3 TP=6 (B) | 3 / 3 | 4 / 4 | instants <= TP |
| T1 R=4 TP=2 (the relay's ratio) | 1 | 2 | 1 |
| T1 scale C, RTO0=1: R=15 TP=30 / R=30 TP=15 | 15 / 15 | 16 / 16 | tie at 15 |
| T1 scale C, RTO0=2: R=15 TP=30 / R=30 TP=15 | **30 or more** (30 verified) / 14 | not reached / 15 | 30 / 14: the longer ping timeout doubles the tolerance |
| O4 `STRICT=1 COOP=0` (A6b), no outage at all | none | 0 | a legacy live peer is cut by the first probe |
| F3: O1 with H=5 B=6 (U floor 1) | none | 1 | even a one-tick outage |

**P3 restore** (`leak`, provider may end OK or with an error frame): C1 `RESTORE=1` (deferred, plan) verified; C2 `RESTORE=0` violated;
C3 `RESTORE=2` (success path only) violated. **P4** (`badrel`): D1 D2 D3 verified (guarded release, even with a second release path;
unguarded with one path); D4 unguarded plus a second release path violated. **P5** (`c2cut`, outage up to 8 < `RETR2`): E1 O1 verified
(with `EF` the outage is survived by the second connection), E3 O5 verified, **E2 O2 violated**, H6 O2 at scale B violated; E4 O2 with an outage
within the tolerance (3) verified.

**P6 H shortened, U follows** (B = 6): F1_1 to F1_5 (H = 1..5, U = 5..1) `late` verified; F1_6 (H = 6 = B, U floors at 1) violated; F2 (H = 4, U fixed
at 4) violated; F3 H = 5 cut at one tick; F4 F5 F6 (H=1, U=5) outage 4, 5, 6 violated (tolerance 3).

**P7 relay.** G1 G2 (the relay vanishes: O1, T1) verified. **G3 G4 (the client behind the relay vanishes, relay alive: O1, T1) violated**; G5 G6
(`RELAYPROBE=1`, the relay applies U to its client connection, forwards heartbeats at the same cadence, cancels the node request) verified,
with `EF` the relay cancels and the call ends; G7 the same with B = 5 violated (tight).

**Body deadline** (O1, B = 6, H = 2): J1_6 J1_7 verified, **J1_8 violated** (PB = 8: a vanish during the upload frees the slot at P, later than tv + B);
J2 (T1, PB = 8) verified: the tunnel's check covers the body phase; J3 (O5, PB = 6) violated.

**Sanity (CTL `EF`, all verified).** detection after a vanish (A1 A9 H1 K1); provider end without detection (A1 A9); next request on the kept-alive
connection (A1); relay cancel and call end (G5); outage survived by a live second connection (E1); body phase left (J1).

## 6. A kernel experiment (qualitative check of the retransmission abstraction)

Throwaway Python in `unshare -Urn`, loopback, `iptables -I INPUT -i lo -j DROP` for X seconds while the server writes one byte every H
seconds on a socket with `TCP_USER_TIMEOUT = U` and a live peer that only reads; Linux 7.0.0-34, `tcp_retries2 = 15`, RTO about 0.2 s.
Not committed, not a test; the opt-in kernel test of 5.4 can reproduce it.

| U, H, outage start | outage X survived | outage X cut (at) |
|---|---|---|
| 10 s, 1 s, 3.5 s | 4, 5, 6, 6.5, 7 | 8, 9.5 (at 14.5 s) |
| 30 s, 15 s, 14.6 s (just before a heartbeat) | 22, 25 | 27, 29 (at 45.5 s) |

Reading: the call dies **U after the first unacknowledged byte, plus 0.4 s** (a rerun that logged the first write inside the outage: written at 4.02 s, cut at 14.4 s
with U = 10 s; written at 15.01 s, cut at 45.4 s with U = 30 s; the 0.4 s is the kernel's timer granularity, two initial RTOs here): the retransmit timer is
clamped to the user timeout (`CLAMP=1`), the abort does not wait for the next backed-off retransmission (which would have been at 16.6 s and 66 s); and a live peer is cut by an outage of 8 s against U = 10 s and 27 s against U = 30 s, matching the
last retransmission instant (about 0.2 x (2^k - 1) = 6.2 s and 25.4 s from the first lost byte). **This closes the "clamped on the current
kernel" item of 5.1 for Linux 7.0 and refutes "an outage shorter than U never cuts" (5.3).** Older kernels remain [unverified].

## 7. Decoded counterexamples and classification

- **CEX-1 `cutlive`, O1 scale B, outage 4 < U = 6 (H1 family).** The outage starts right before a heartbeat; the heartbeat is the first unacked
  byte; retransmits at ages 1 and 3 fall inside the outage; the next one would be at age 7 but the timer is clamped to U = 6 and the kernel aborts at age 6,
  although the link is back since age 4; the call ends `C_KABORT` with the peer alive. *Spec defect* (5.3 sentence), not a mechanism defect.
- **CEX-2 `leak`, `RESTORE=0`.** The provider ends OK at tick 1, the connection stays alive, the next request finds `kuto = 4`. *System defect* of O1 without
  a restore; the plan's deferred restore is the fix. **CEX-3 `RESTORE=2`.** The call ends with an `error` frame (`C_ERR`), nothing restored: *the fix must be a
  `defer` on every exit, not the success path.*
- **CEX-4 `late`, `RELAY=1 VLOC=1` (G3, G4).** The client behind the relay vanishes; the relay's socket to it holds unacked heartbeats; the node's
  socket to the relay is healthy and the relay answers every ping, so neither node-side mechanism fires and the slot is held to the horizon. *Spec gap*
  (5.2 "the relay is the node's peer": true, but the slot holder is the client).
- **CEX-5 `cutlive`, T1 with R=4 TP=2 (B4_2).** A 2-tick outage covers the ping; its ACK waits behind the unacked heartbeat for the next retransmission
  (after TP); the node closes the tunnel of a live relay. *Parameter defect*: the plan's 30/15 split has half the tolerance of 15/30 for the same bound.
- **CEX-6 `c2cut`, O2 (E2).** An 8-tick outage (below the kernel's own limit of 18) with U on every accepted socket aborts the browser's SSE connection.
  *System defect of O2* (the very regression the plan names).
- **CEX-7 `cutlive`, O4 `STRICT=1 COOP=0` (A6b).** No outage, no vanish: the first probe is never answered by a client that does not know it, the connection
  is closed at R + TP. *Design defect of O4.*
- **CEX-8 `badrel` (D4).** Detection releases, the deferred release releases again: `inuse` 2 -> 0, the other caller's slot is released. *Hazard of an
  extra release path* (the real `release()` is CAS-guarded, so only a second counter would suffer).
- **CEX-9 `late`, PB = 8.** The peer vanishes at tick 1 during the upload; the body deadline fires at tick 8 > tv + B = 7: the slot is held past B.
- **CEX-10 `late`, `PROXY=1` (A10b, A11).** The proxy acknowledges the node's heartbeats; the kernel never aborts, T2 and O1 never fire. T1 (a ping through the proxy) does.

## 8. Spec statements: CONFIRMED / CONTRADICTED / NOT DECIDABLE

CONFIRMED: per-call `TCP_USER_TIMEOUT` frees the slot within H + U on the direct leg (A1, K1; kernel 7.0 abort at U after the first unacked byte, section 6);
the node-side h2 check frees it within R + TP of the relay's last frame on the tunnel, also when the relay is behind an egress proxy (A9, A10c); the bound
is independent of H on the tunnel (I3 I4); the option is not set during the body and P bounds that phase (J); restore on every exit (C1); a guarded release (D);
per call leaves other routes alone (E1); a relay that vanishes is bounded on both legs (G1 G2); T2 adds nothing to T1 (A10d A10e).
CONTRADICTED: "no cut of a live peer whose outages are shorter than U" (5.3; CEX-1, kernel 6); "U = B - H, minimum 1 s, so a test that shortens the heartbeat shortens
U with it" (5.2: U grows when H shrinks; U = 1 s cuts a live peer on one tick, F3; H >= B is unbounded, F1_6; the wording needs B as a test seam); "the node is on the
leg that matters" for a client behind a relay (CEX-4); "an egress proxy does not change this" is true for T1 and false for T2 (A10b); the plan's 30/15 ping split is correct for the bound
and poor for tolerance (CEX-5).
NOT DECIDABLE here: the retransmission schedule of a real RTT-driven RTO (the tolerance numbers 15 to 25 s of 30 are RTO-dependent: the model holds the
exponential schedule, the experiment holds one RTO); kernels older than the clamp (CLAMP=0 gives the shape, not the version); macOS, Windows and BSD behaviour; SACK, TLP, RACK
effects on the ack of a retransmit; relay forwarding delay; the x/net timer's tie behaviour at the exact deadline (the model lets an answer at the deadline tick win); real-time cost of the
close and the handler return.

## 9. DECISION

| item | decision | evidence |
|---|---|---|
| direct leg | **O1**: per-call `TCP_USER_TIMEOUT`, `U = B - H`, set after the request body, unconditional deferred restore | A1 K1 (bound), C1 (restore), E1 vs E2 (scope), D (once), J (body) |
| not O2 | resets other routes on an outage in (tolerance, kernel limit) | E2 H6 |
| not O3 | same tolerance as O1 for the same B (B2 = B1), Linux only, a poll goroutine, a second cancel path, no kernel abort; only its bound is U instead of H + U | A5 B2 D4 |
| not O4 | needs a cooperating client: strict cuts every legacy live peer (A6b), lenient never detects it (A6c); helps no other client; the only mechanism that crosses an acking intermediary (A11b): a phase-3 candidate | A6 A11b |
| O5 off Linux | documented, not covered | A7 I1 |
| not O6 (small send buffer + write deadline) | bound (CAPB + 1) H + WD depends on kernel buffer accounting, a 15 s write deadline is itself a false-positive source | A8 |
| tunnel | **T1 only**: `ReadIdleTimeout` 15 s, `PingTimeout` 30 s | A9 A10c I3 I4 K2 |
| not T2 | Linux only; behind a CONNECT proxy it times the node-to-proxy leg (A10b); adds nothing to T1 (A10d A10e) | A10 |
| ping split 15/30, not 30/15 | same bound, doubled tolerance of a live relay (RTO 2 s: 30 s against 14 s) | B3 B4 K12 K13 |
| relay clients | the relay applies the same per-call option to its client connection for the completions route (recommended, `external/swarm/mount.go:162`); without it, a node's slot of a vanished client is held until the relay's own request ends | G3 G4 vs G5 G6 |

**Constants and relations.** `B = sharedLivenessBound = 45 s`. Direct: `B = H + U`, H = 15 s, U = 30 s, plus the heartbeat timer's lateness (ms) and the kernel's timer granularity (0.4 s measured), which the 45 s promise absorbs in test tolerances, not in the constants.
Tunnel: `B = R + TP`, R = `TunnelPingAfter` = 15 s, TP = `TunnelPingTimeout` = 30 s. `H <= B / 3` (clamped). `P = 30 s <= B`. m5's `I >= 2H` unchanged. The relay's own
30 s / 15 s pings stay. Both sums are pinned by tests, and B is a test seam of `sharedTimings()` (a short test shortens B, H and P together).
**What a live peer survives:** a silent link shorter than the last TCP retransmission instant before U (before TP + a tie for the ping): 15 to 25 s of 30 s in practice.
**Not covered, to be written on the documentation page:** (1) macOS, Windows, the BSDs on the direct leg (the operating system's retransmission limit, minutes);
(2) any peer behind an intermediary that acknowledges for it (TLS terminator, nginx, Cloudflare, an HTTP proxy in front of a direct listener) on every platform;
(3) a client behind a relay unless the relay applies the option; (4) a kernel that does not clamp the timer (bound H + first retransmit instant >= U, up to about H + 2U)
[unverified which versions]; (5) a node that is itself behind a CONNECT proxy is covered on the tunnel (the ping goes end to end) and not on a direct dial.

## 10. Recommended spec changes (replacement text)

**5.2, replace the whole section by:**

> ### 5.2 C.2 Design (decided by the D3 model, `remote-model-provider-models/p2-d3-vanished-peer.md`)
>
> One promise on both legs: **the slot of a peer that vanished is freed at most B = `sharedLivenessBound` = 45 s after it**, on the leg the node sees, with no effect on any other route and no cut of a peer whose
> link is silent for less than the last TCP retransmission instant before the timeout (5.3). One mechanism per leg; no listener-wide option, no `TCP_INFO` polling, no application probe, no user timeout on the tunnel socket.
>
> **Direct leg, Linux (Android builds the same file): `TCP_USER_TIMEOUT` per call, U = B - H.** The bound is H + U = 15 s + 30 s = **45 s** from the peer's disappearance (H from the last write it acknowledged, plus the heartbeat
> timer's lateness and under a second of kernel timer granularity): the first heartbeat written after it is the first unacknowledged byte, the kernel aborts U after that (clamped retransmit timer, measured on Linux 7.0).
> - `internal/platform/usertimeout_linux.go` ... *(first bullet of the old text, unchanged)*.
> - `internal/httpx`: `NewServer` sets `ConnContext` (the connection under an unexported key; a `*tls.Conn`, which the relay's listener produces (`external/swarm/serve_swarm.go:86`), is unwrapped with `NetConn()`), exported `httpx.ConnContext`
>   for servers built another way, `httpx.UserTimeoutFor(r, d) (restore func(), err error)` *(rest unchanged)*.
> - **Per call, not per listener**: a listener-wide option aborts a browser's SSE (`/v1/responses`, the events stream) on any outage between the tolerance and the kernel's own limit, where today it survives for minutes; per call keeps every other route byte for byte.
>   `runSharedCall` (`shared_models_http.go:595`) sets it **after the request body has been read** (the upload is bounded by P = 30 s, which must stay at or below B) and before `stream.start()` (`:632`) with `U = B - H`. `sharedTimings()` returns B as well (a test seam: a test that wants a short wait
>   shortens B and H together) and clamps H to at most B / 3, so U >= 2B / 3; the earlier 1 s minimum for U is dropped (a U of 1 s cuts a live peer on a one-tick outage, and H >= B can not be bounded).
>   The restore is a `defer` registered immediately after the set and runs on **every** exit (final frame, error frame, cancel, failed write), never only on the success path: a connection kept alive after an `error` frame must not carry the option into the next request.
>   The slot keeps its one release, the deferred `slot.release()` (CAS-guarded); the probe adds no handler logic and no second release.
> - On expiry the kernel resets the socket, Go's connection reader sees the error, `r.Context()` ends, `provider.Stream` is cancelled, `runSharedCall` returns through `case r.Context().Err() != nil` (`:655`) logged as `client_gone`, and the slot is released.
>
> **Tunnel leg, every platform: a node-side HTTP/2 health check, and nothing else.** `internal/swarm/tunnel.go:156` builds `http2.Server{MaxConcurrentStreams, IdleTimeout: 0}`; it gains `ReadIdleTimeout: TunnelPingAfter` = **15 s** and `PingTimeout: TunnelPingTimeout` = **30 s**
> (fields exist in the pinned x/net, `config.go:55-56`), `TunnelPingAfter + TunnelPingTimeout == sharedLivenessBound` pinned by a test. The node pings the relay when no frame arrived for 15 s, a relay that does not answer within 30 s is closed: **at most 45 s after the last frame the relay sent**, hence at most 45 s after its
> disappearance, whatever the heartbeat is. The split is not the relay's own 30 s / 15 s: the bound is the same, but a ping that waits behind unacknowledged heartbeats is answered only when a retransmission gets through, and the longer timeout doubles the outage a live relay survives. A closed connection ends `ServeConn`, every
> in-flight request context is cancelled, the slot is released. `activityConn`'s 150 s silence watchdog stays as it is. A user timeout on the node's dialled socket is **not** added: it is Linux only, and behind an egress proxy (`netx` CONNECT) it times the node-to-proxy leg, which the proxy acknowledges whatever happens behind it, while the h2 ping travels through it end to end.
> Constants are exported so a test can scale them. An idle tunnel now carries a node ping every 15 s next to the relay's own (a few bytes).
>
> **Through a relay.** A relay that dials the node (direct transport) reaches the direct listener over a pooled HTTP/1.1 connection: the per-call option applies to it. A node that dialled out is the tunnel case. Either way the node's peer is the relay. **A client that vanishes behind a relay is invisible to the node** (the relay acknowledges
> and answers everything): its slot is held until the relay's own request ends. The relay's mount (`external/swarm/mount.go:162`, a `httputil.ReverseProxy` with `FlushInterval: -1`) therefore sets the same per-call option on the client's connection for `POST /swarm/nodes/{node}/coddy/llm/completions`, with the same `U = B - H`
> and a deferred restore; the relay's request context ends at the abort, the proxy cancels the node request (a reset stream on a tunnel, a closed connection on a direct dial) and the node's slot is freed within B of the client's disappearance as well. **[recommended; if declined, 5.1, the documentation and the non-goals say "a client behind a relay is bounded only by the relay's kernel, about 15 minutes on Linux"]**
>
> **Other legs and platforms.** macOS, Windows, the BSDs: `SetTCPUserTimeout` is a stub, the direct leg keeps the operating system's retransmission timeout (minutes), and the page says so (the Darwin and Windows equivalents, `TCP_RXT_CONNDROPTIME` and `TCP_MAXRT`, are **[unverified]** and a follow-up behind the same signature). A peer behind an intermediary that
> acknowledges for it (a TLS terminator such as nginx or Cloudflare in front of a direct listener, an HTTP proxy) is not covered on any platform: the node's peer is the intermediary and its own limits apply. A kernel that does not clamp the retransmit timer to the user timeout aborts at the first retransmission instant at or after U: the bound is then H + that
> instant, at most about H + 2U **[unverified which kernels]**.

**5.3, replace the whole section by:**

> ### 5.3 C.3 Edge cases
>
> **A live peer whose link goes silent.** The user timeout counts from the first unacknowledged byte, and the kernel retransmits at backed-off instants (RTO, 3 RTO, 7 RTO, ...) and aborts at the first timer after U without trying again. A live peer therefore survives an outage only if a retransmission lands after the link is back and **before U**:
> the outage it survives is the **last retransmission instant before U**, between about U/2 and U (RTO 0.2 s: 25.4 s of 30 s; RTO 1 s: 15 s; RTO 2 s: 14 s). Measured on Linux 7.0 (loopback, DROP for X seconds): U = 30 s, outage 25 s survived, 27 s cut at 45.5 s. The client sees a cut stream, retried only before output (the existing `emitted` contract); the documentation says
> "a silent link of more than about 15 to 25 s", not "30 s". The tunnel has the same shape with TP: the node closes a live relay whose answer to a ping needs a retransmission that comes later than 30 s, and the tunnel's calls go with it, as the relay's own ping does today for an idle node.
> The heartbeat gap is part of the bound: B = H + U (+ the timer's lateness), `sharedHeartbeat` is clamped to B/3, the unit test pins H + U = 45 s and R + TP = 45 s. A peer that vanishes during the upload is bounded by P (30 s <= B: the option is set after the body); one that vanishes after the `final` frame costs nothing.
> The option is restored on every exit, so a kept-alive connection starts its next request (any route) with the system default. `Expect: 100-continue` is unaffected. IPv6 is the same socket option.

**5.4, add:** `external/httpserver`: the second request on a connection whose first call ended with an `error` frame (not only `final`) starts with sockopt 0; `sharedTimings` clamps the heartbeat to B/3; the sockopt is `45 s - heartbeat` during the call. `internal/swarm`: `TunnelPingAfter + TunnelPingTimeout` equals the bound; a relay that
answers pings late but within `TunnelPingTimeout` is not cut; one that never answers is closed within the scaled bound. Opt-in kernel test (`CODDY_TEST_NETNS=1`): with U = 10 s an outage of 5 s is survived and one of 9.5 s cuts the call at U after its first unacknowledged byte (the experiment of 5.1 and section 6 of the model report).

**7-D3, replace "Question ... Depends" by:** "**Decided** (model `p2-d3-vanished-peer`). Direct leg: **O1** per-call `TCP_USER_TIMEOUT` with U = B - H, set after the body, restored by a defer on every exit; **O2** rejected (it cuts a browser SSE on an outage between the tolerance and the kernel's limit), **O3** rejected (same tolerance, more code, no
gain), **O4** rejected (a strict probe cuts every client that does not answer, a lenient one never detects it; it is the only way across an acking intermediary and stays a phase-3 candidate), **O5** applies off Linux and is documented, a small send buffer with a write deadline was also rejected (its bound depends on kernel buffer accounting). Tunnel: **T1** with 15 s / 30 s; **T2** rejected (Linux only,
blind behind a CONNECT proxy, adds nothing to T1). Relay clients: the relay applies O1 to its client for the completions route. Constants: B = H + U = R + TP = 45 s, H <= B/3, P <= B. A live peer survives a silent link shorter than the last retransmission instant before U (about U/2 to U). Depends: 5.2, 5.3, S4, S5."

**Section 7 intro:** change "(D3: O1 with T1)" to "(D3: decided, O1 with T1 at 15 s / 30 s)".

**Section 8, stage S4 row:** Owns `internal/platform/usertimeout_{linux,other}.go` (+tests), `internal/httpx/*` (`ConnContext` unwraps `*tls.Conn`), `internal/swarm/tunnel.go` (`TunnelPingAfter` 15 s, `TunnelPingTimeout` 30 s, sum pinned) (+ `external/swarm` tunnel test) **and, with the relay clients of 5.2, `external/swarm/mount.go` (+ test; completions route only)**.
**S5 row:** adds `shared_stream.go`: `sharedLivenessBound`, `sharedTimings()` returns B and clamps H to B/3; `shared_models_http.go`: the option after the body read, the deferred restore on every exit.
**8a:** `swarm.TunnelPingAfter` = 15 s, `swarm.TunnelPingTimeout` = 30 s (node side, exported for scaled tests, sum pinned); `sharedLivenessBound` (45 s) and the B seam in `sharedTimings()`; `httpx.UserTimeoutFor` unchanged.
**9, "Kernel dependence", replace by:** "The 45 s bound is measured on Linux 7.0, where the retransmit timer is clamped to the user timeout; elsewhere it is H + the first retransmission instant at or after U **[unverified which kernels]**. The outage a live peer survives is the last retransmission instant before U (about 15 to 25 s of 30), not U."

## 11. Commands to reproduce

`MC='mcd check --promela docs/plans/remote-model-provider-models/p2-d3-vanished-peer.pml --no-timing --budget-ms 150000'`, scale A is the file default; a row is
`$MC -D <defs> --ltl '<f>'` with `L='[] (late == 0)'`, `C='[] (cutlive == 0)'`, `K='[] (leak == 0)'`, `D='[] (badrel == 0)'`, `X='[] (c2cut == 0)'`, and sanity `--ctl 'EF (<cond>)'`.
- A1 `-D LEG=0 -D DIRECT=1 -D PROV=1 -D XOUT=2` L plus `EF (fired == 1 && vanished == 1)`; A2 `-D U=4 -D B=5`; A3 `-D CLAMP=0` (A3b `-D U=4 -D B=9`); A4 `-D DIRECT=2`; A5 `-D DIRECT=3` (A5b `-D U=6 -D B=6`); A6 `-D DIRECT=4 -D COOP=1 -D STRICT=0` L and C
  (A6b `-D COOP=0 -D STRICT=1`, A6c `-D COOP=0 -D STRICT=0`); A7 `-D DIRECT=5`; A8 `-D DIRECT=6 -D U=4 -D B=9`; A9 `-D LEG=1 -D T1=1 -D PROV=1 -D XOUT=2` (A9b `-D B=5 -D R=2 -D TP=4`, A9c `-D R=4 -D TP=2`);
  A10 `-D LEG=1 -D T1=0 -D T2=1` (A10b `-D PROXY=1`; A10c `-D T1=1 -D T2=0 -D PROXY=1`; A10d `-D T1=1 -D T2=1 -D PROXY=1`; A10e `-D T1=1 -D T2=1`); A11 `-D LEG=0 -D DIRECT=1 -D PROXY=1` (A11b `-D DIRECT=4 -D COOP=1`).
- B: `-D LEG=0 -D DIRECT=1 -D XOUT=n` C for n = 1..5 (B1), `-D DIRECT=3 -D U=6 -D B=6` (B2), `-D LEG=1 -D T1=1` (B3), `-D LEG=1 -D T1=1 -D R=4 -D TP=2` (B4), `-D LEG=1 -D T1=0 -D T2=1` (B5), `-D DIRECT=4 -D COOP=1` (B6).
- C: `-D DIRECT=1 -D PROV=1 -D RESTORE=1|0|2` K. D: `-D DIRECT=1 -D PROV=1 -D XOUT=2 -D RELONCE=a -D EXTRAREL=b` D. E: `-D C2=1 -D XOUT=8 -D DIRECT=1|2|5` X; E4 `-D DIRECT=2 -D XOUT=3`.
- F: `-D DIRECT=1 -D B=6 -D H=h` L for h = 1..6 (F1_h); F2 `-D H=4 -D U=4`; F3 `-D H=5 -D XOUT=1` C; F4..F6 `-D H=1 -D XOUT=4..6` C.
- G: `-D RELAY=1 -D VLOC=0|1 -D RELAYPROBE=0|1` with `-D LEG=0 -D DIRECT=1` or `-D LEG=1 -D T1=1` L; G7 adds `-D U=4 -D B=5`.
- H (scale B): `-D H=3 -D B=9 -D R=3` plus the F, B, C, E rows; I1 `-D DIRECT=5 -D RETR2=18 -D B=25` (I2 `B=24`); I3 I4 `-D LEG=1 -D T1=1 -D H=1|5`; J `-D PB=6|7|8` (J2 `-D LEG=1 -D T1=1 -D PB=8`, J3 `-D DIRECT=5 -D PB=6`).
- K (scale C, `-D H=15 -D B=45 -D RTOCAP=64`): K1 `-D LEG=0 -D DIRECT=1 -D R=15 -D PROV=1` L; K2 `-D LEG=1 -D T1=1 -D R=15` L; K3 `-D R=30`; K4 K5 `-D RTO0=1 -D TVMAX=17 -D XOUT=15|16` C (O1); K10 K11 the same with `-D RTO0=2`, `XOUT=14|15`;
  K6 K7 `-D LEG=1 -D T1=1 -D R=15 -D TVMAX=17 -D XOUT=15|16` (`--budget-states 20000000`); K8 K9 `-D R=30 -D TVMAX=32`; K12 K12b `-D RTO0=2 -D R=30 -D TVMAX=32 -D XOUT=14|15`; K13 K14 K14b `-D RTO0=2 -D R=15 -D TVMAX=17 -D XOUT=15|29|30`.
Decode a counterexample with `--bfs` and read `counterexample.steps` (the `command` field; `t = clk + 1` opens a tick).

## 12. Limitations

Discrete clock, zero wire delay, one call, one outage, one vanish, gap exactly H; the retransmission schedule is the exponential one, validated against one RTO by the experiment, not against a real RTT estimator;
the h2 timer tie (answer at the deadline wins) and the kernel's tie (abort before retransmit) are chosen, each moves a boundary by one tick; the relay is modelled by its forwarding of heartbeats and its cancel; no TLS, no flow control, no
`Expect: 100-continue`; the body phase is a nondeterministic delay bounded by P. The model is of the plan's design, not of code that exists yet: conformance needs the tests listed under 5.4 (and the opt-in kernel test). The decisions use `verified` / `violated`
rows only; the budget-bound `inconclusive` rows of the first scale-C T1 attempt (1 M states) were rerun with a larger budget and are not used.
