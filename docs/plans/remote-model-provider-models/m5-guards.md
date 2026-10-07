# Model check m5-guards: the two guards and the heartbeat of the `coddy` stream

Plan sections: `docs/plans/remote-model-provider.md` 4.2 ("Stall and liveness are two guards", heartbeat, per-write
deadline, stateless handler), 4.3 (a relay cut is a transport failure), 5a rows Server/Client (stall, heartbeat,
`stream: false`). Model: `m5-guards.pml` (engine `mcd 0.2.0`, CLI, explicit-state, no MCP session; the CLI has no
simulate and no lint, so properties were classified by hand). Nothing in the repository other than the two
deliverables was touched.

## 1. Summary

| Group | Verdict on the model |
|---|---|
| P1 client guard never kills a healthy remote (SPEC: comments count, heartbeat right after headers) | CONFIRMED iff `H + J < I`; broken variants FRAMEONLY / NOHB / `H + J >= I` violate it |
| P1b a proxy hop with idle limit `L` never cuts a healthy stream | CONFIRMED iff `H + J < L` and `P < L` (`P` = time before the headers) |
| P1c the agent's first-token timer `F` never kills a healthy remote | **CONTRADICTED** for `stream: false` rows (and long backoff): holds iff `F > P + D + G` |
| P2 a dead remote is detected within a bound | CONFIRMED after the first body byte: `[I-H-J+1, I]` ticks after the crash; **CONTRADICTED** before it (guard not armed) |
| P3 a model stall becomes `error{upstream, stall}` | CONFIRMED for a stall after the first upstream byte; **CONTRADICTED** before it (hang); `stream: false`: hang, slot held |
| P4 stall after output is terminal, `emitted == true`, no repeat | CONFIRMED on the model; the mutation `BADEMIT` violates it (a client-side hazard is noted in 6) |
| P5 a stalled writer is cut within `W` and the slot freed | CONFIRMED for a *blocked* write; **CONTRADICTED** as a bound on slot hold of a vanished peer: `(CAP+1)(H+J)+W-2` ticks, hours in practice |
| P6 first heartbeat before `I` elapses | CONFIRMED (needs `P < I` only if the guard is armed at request start); matters only for arming at the first byte |
| P7 relay cut is a transport failure | CONFIRMED (EOF cut: client truncation and slot freed at once; silent cut: detected by `I`) |

Decision items: none in this scope (no `[decide at modeling]` is assigned to m5). Fairness: not used, no liveness
formula (see 2).

## 2. Intake card

- **Boundary.** Inside: one `POST /coddy/llm/completions` call as a byte stream. The remote handler as phases
  `PRE` (slot held, body read and validated, no byte sent; at most `P`), `UP` (headers and first heartbeat sent,
  upstream call, silent backoff, at most `D`), `GEN` (tokens, gaps at most `Gmax`) or `BLK` (a `stream: false` row:
  silent `G`, then one burst), `DONE`, `ABORT`, `DEAD`; the heartbeat ticker; the remote's stall guard `S`; the
  client byte guard `I`; the agent's first-token timer `F` (kept because it sees no heartbeat); an optional proxy
  hop with idle limit `L`; the writer with a send buffer `CAP` and the per-write deadline `W`. Outside: admission
  and `busy` (the server never queues, so a "slot wait" before the stream does not exist: only `PRE` does), the
  client's busy wait, retries, authentication, content, the usage routes.
- **State.** Phase and age counters of the remote, heartbeat age, client outcome (`CL_WAIT/OK/ESTALL/TRUNC/IDLE/FT`),
  client idle counter and "armed" bit, chunks seen, send buffer fill, blocked-write age, fault flags, latches.
- **Atomic step.** One tick of `World`: faults, remote step, heartbeat, writer, proxy, client, bookkeeping. Within a
  tick every timer is conservative at ties: an expiry at tick `t` wins over a byte that arrives at tick `t`
  (`gap == limit` fires, as `idle >= I` does in `transport.go`).
- **Class.** Finite, untimed Promela with a **discrete-clock abstraction** (one tick, declared, not a timed
  automaton). Time is bounded by the horizon `TMAX`; liveness questions are asked as bounded-time safety
  (`hang`, `slotstuck`, `undet` latches), so **no fairness is used and no run depends on it**. Composite model with all
  faults: 196,706 states (`SF=0`) and 1,272,571 (`SF=1`), `deadlock` verified exhaustive in both; the properties
  below were checked on scoped compositions (fault set named per row) because a CTL labelling of the 1.27 M graph
  did not fit a 5 minute budget with four formulas (reported as `inconclusive` there, not used).
- **Assumptions.** A1 zero transport delay, jitter only as the slack `HJIT` of the heartbeat gap (a real wire jitter
  is added to `HPER+HJIT` by hand); A2 the heartbeat is a ticker every `H` regardless of data; A3 the client guard
  is armed by the first counted body byte (`transport.go` `idleBody.arm()` runs only for `n > 0`), variants
  `ARM=1` (at the headers) and `ARM=2` (at request start); A4 the remote's stall guard is armed by the first
  upstream byte (the same `idleBody`), variant `SARM=1` arms it at call start; A5 a silent fault is a blackhole
  (no FIN/RST either way), the other fault mode is an EOF/RST both sides see at once; A6 `H2` PING liveness
  (client `http2ReadIdleTimeout` 30 s + `http2PingTimeout` 15 s) and TCP timeouts are **not** modelled (they only
  shorten detection on an end-to-end HTTP/2 path); A7 a blocked write stops the handler (the chunk callback writes
  synchronously); A8 `CAP` counts heartbeat-sized writes (chunk = `CWT`).
- **Scales (relation to the plan's seconds).** A: `H=2 I=5 S=6 D=6 G=7 Gmax=2 P=3 W=3 CAP=2`. B: `H=3 I=9 S=10 D=10
  G=12 Gmax=3 P=5 W=4 CAP=3 TMAX=48`. R (tick = 15 s): `H=1 I=20 S=20 D=4 G=24 Gmax=4 P=4 W=4 F=6 TMAX=90` (the
  plan's `H=15 s, I=300 s, D=P=W=60 s, F=90 s, G=6 min`). Every boundary below was found at A and B at the same
  linear form; R re-checks the headline ones at the plan's ratios. That the form carries to `I/H = 20` is an
  argument (the counters enter only through comparisons), supported by R, not a proof for all constants.

## 3. Mapping spec -> model

| Spec / code | Model |
|---|---|
| `idleBody`: any byte resets, armed by first read, fires at `idle >= I` (`transport.go` 129-186) | `armed`, `idle`, `client_step`; `ARM`, `FRAMEONLY`, `HB` |
| rejected idea "count only event frames" | `FRAMEONLY=1` |
| heartbeat "at most 15 s apart, first right after slot and headers" | `hb_step`, `enter_up` (`LATEFIRST=1` mutates it), `HPER`, `HJIT` |
| remote retry ladder (`RetryMaxDelay` 60 s, `resilient.go` 23) | silent phase `UP`, length up to `DBACK` |
| `stream: false` answers in one piece (`blocking.go`, `server.go` 259-267: guard 0) | `SF=1`: `BLK` silent `GGEN`, burst; no `S` |
| model stall | `MSTALLF` (mid-stream), `USTALLF` (upstream never answers) |
| first-token timer (`react.go` 898-913, armed iff local `rm.Stream`, stopped by a chunk only) | `FTMO` (0 = local row `stream: false`) |
| per-write deadline `W`, slot released by `defer` | `wbuf`, `CAPB`, `blocked`, `wblk`, `WDL` |
| relay cut, remote crash | `CUTF`, `CRASHF` with EOF or silent mode; `FREEZEF` reader that stops reading |
| nginx `proxy_read_timeout`, Cloudflare | `PXL` |

## 4. Properties (CTL `AG`/`EF` over latches; cross-checked in LTL `[]`, same verdicts)

| id | text | class / logic | latch |
|---|---|---|---|
| P1 | client guard never fires while remote and path are healthy | safety / CTL `AG`, LTL `[]` | `fk_idle` |
| P1b | proxy limit never fires while the remote is healthy | safety / CTL | `fk_proxy` |
| P1c | first-token timer never fires while the remote is healthy | safety / CTL | `fk_ft` |
| P2 | a silent crash or cut is cut by the client within `DB` ticks (fault tick = 1); earliest cut is `I-H-J+1`, and attained | safety (bounded time) + `EF` | `undet`, `undet_armed`, `undet_hdr`, `det_below`, `det_lb` |
| P3 | model stall with healthy remote ends as `error{upstream, stall}` within `S`; no hang, no stuck slot | safety (bounded time) | `stall_lost`, `stall_slow`, `hang`, `slotstuck` |
| P4 | error frame after output says `emitted == true` | safety | `bad_emitted` |
| P5 | a blocked write lives at most `W`; slot freed within `SB` of the peer vanishing | safety | `wover`, `leak`, `leak_stream` |
| P6 | first heartbeat before `I` elapses | safety | `fk_idle` (`ARM=2`, `P`), `undet_hdr` (`LATEFIRST`) |
| P7 | after a cut nothing is delivered; EOF cut gives truncation and frees the slot | safety | `deliv_after_cut`, plain `AG` formulas |
| S | every phase, outcome and mutation is reachable | reachability / CTL `EF` | `rp`, `cl`, `seen`, `blocked`, `hb_up`, `hang` |

## 5. Results

All rows `exhaustive`, `complete: true`. Row = `-D` set after the common prefix `CRASHF=0 CUTF=0 FREEZEF=0` unless a
fault is named; verdict is the engine's status (expectation matched in every row of the final file; sizes 71 to 197 k
states). Scale A unless noted.

**P1 (client guard).** `fk_idle`:

| row | variant | status |
|---|---|---|
| A1 A2 A3 | SPEC (`HB=1 FRAMEONLY=0`), `ARM=0` `SF=0/1`; `ARM=2 SF=1`; stalls enabled | verified |
| A4 A5 A6 | `FRAMEONLY=1`, `ARM=2` `SF=0/1`; `ARM=1 SF=1` (backoff `D>=I`, `stream: false` `G>=I`) | violated |
| A7 | `FRAMEONLY=1 ARM=0 SF=1` (guard armed only by the burst) | verified |
| A8 A9 | `HB=0`: `ARM=2 SF=1` violated; `ARM=0 SF=1` verified (armed by the burst) | violated / verified |
| A10 | `HB=0 ARM=0 SF=0`, model stall with `S > I` (client kills before the remote's guard) | violated |
| A11 | `ARM=0`, `I=5`: `H=4,J=0` verified; `H=4,J=1` violated; `H=5` violated | boundary `H+J < I` |
| B (scale B) | `H=3, I=9`: `J=5` verified, `J=6` violated; `FRAMEONLY=1 ARM=2` violated, SPEC `ARM=2` verified | boundary |
| R | `H=1, I=20`: SPEC `SF=0/1` verified; `FRAMEONLY=1 ARM=2` `SF=1` (`G=24>=20`, the plan's six minutes) violated, `SF=0` verified (`D=4<20`); `HB=0 ARM=2 SF=1` violated | as predicted |
| A12 A13 | `ARM=2`: `P=4` verified, `P=5` violated (`P < I`) | boundary |

LTL cross-check (`--ltl '[] (fk_idle == 0)'`, fairness `none` and `weak`): SPEC `verified`, `H=5` `violated`, same.

**P1b / P1c.** `fk_proxy` (`SF=1`, stalls on): `L=3` violated, `L=4` verified at `H=2,P=3` (boundary `L > max(H+J, P)`);
scale B: `L=5` violated, `L=6` verified; `HB=0 L=6` violated; R: `L=4` (60 s, `P=4`) **violated**, `L=5, 7` verified.
`fk_ft`: `SF=1` first byte at most at `P+D+G`: A `F=16` violated, `17` verified; B `27` violated, `28` verified;
`SF=0` bound `P+D+Gmax`: A `11/12`, B `18/19`; R `F=6` (90 s) violated for `SF=1`.

**P2 (dead remote).** Fault set `CRASHF=1 CUTF=1`, silent or EOF, `SF=0/1`:

| row | variant | status |
|---|---|---|
| D5 | `undet`, SPEC `ARM=0` (default `DB=I`) | **violated**: crash in `PRE`, guard not armed |
| D1 D8 | `undet_armed`, `undet_hdr` (fault after the headers), `DB=I` | verified |
| D2 | `undet_armed`, `DB=I-1` (bound is tight) | violated |
| D3 D4 | earliest cut never below `I-H-J+1` (`det_below`) and attained (`EF det_lb`); scale B same | verified / verified |
| D6 | `ARM=2` (armed at request start), `undet` | verified (needs `P < I`) |
| D7 | `ARM=0 FTMO=8`, `DB=max(I,F)` | verified (F covers the pre-byte phase) |
| D9 D10 | `LATEFIRST=1`: `undet_hdr` with `ARM=0` violated, with `ARM=1` verified | mutation / fix |
| D11 | `CIDLE=0` (guard off): `undet_armed` | violated (anti-vacuity) |
| D12 D13 | `HB=0`: `undet` violated, `undet_armed` verified | no heartbeat = no armed window before the first chunk |

**P3 (stall).** Faults `MSTALLF=1 USTALLF=1`, `ARM=0`: `stall_lost` SPEC code (`SARM=0`, `SF=0`): violated (E1) because of
`USTALLF` alone (E3: with `MSTALLF` only, verified); `SARM=1`: verified (E2, `stall_slow` E4 verified, bound `S`).
`SARM=0`: `hang` and `slotstuck` violated (E5 E6); with `FTMO=8` no hang (E8) but the outcome is the first-token timer, not
the error frame (E7). `SF=1` any `SARM`: `hang` and `slotstuck` violated (E9 E10); `FTMO=8`: no hang (E11), but `fk_ft` violated (E12).
**P4:** `bad_emitted` verified (F1), `BADEMIT=1` violated (F2).

**P5 (writer).** Faults `CUTF=1 FREEZEF=1`: `wover` verified (G1; a blocked write always dies within `W`); while a write
blocks the slot is still held (`EF (blocked == 1 && slot == 0)` violated). `leak_stream` = slot still held `SB` ticks
after the peer vanished once the stream was open (heartbeat-only traffic): at `H=2, CAP=2, W=3` `SB=6` violated and `SB=7`
verified (G2); the tight value is `SB = (CAP+1)(H+J) + W - 2` ticks (the slot is released one tick later), checked on
`J=1` with a silent upstream (`9` violated, `10` verified, G8), `CAP=1` (4/5), `CAP=3` (8/9), `W=2` (5/6) and scale B
(13 violated, 14 verified). The naive reading `SB = W` is violated (G3, `leak`); without a deadline (`WDL=99`) violated (G5).
Large `CAP` (`CAPB=30`, `SB=20`): a healthy upstream finishes first and frees the slot (G9a verified), but a silent upstream
holds it far beyond `W` (G9b violated): the deadline is irrelevant, only the end of the call or a liveness probe frees it.
Remedy `PINGT=3` (a server-side liveness probe): the hold is `<= PINGT` whatever `CAP` is (G6 verified, G7 `SB=0` violated,
G9c verified).

**P7.** `deliv_after_cut`, "an EOF cut ends in `CL_TRUNC`", "an EOF cut frees the slot" (H1-H3): verified. Silent cut: P2.

**Sanity (EF, all verified unless noted).** `PRE UP GEN` reached; `BLK` reached only with `SF=1` (and `EF rp==3` violated for
`SF=0`, as designed); heartbeat during `UP`; outcomes `OK`, `ESTALL` with `seen>0` and with `seen==0`, `TRUNC` with
and without output, `IDLE` with and without output, `FT`; `blocked`; `hang`; ABORT with slot freed after a frozen reader and
after a clean cut.

## 6. Decoded counterexamples and classification

- **CEX-1 `undet` (D5).** Silent crash at `t=1` in `PRE`; the client has received no body byte, so the guard is not armed;
  nothing fires until the horizon. *Spec/system gap*: the guard of 4.2 starts with the first body byte, `PRE` can last
  `P` (60 s body-read deadline) and a silent death there is covered only by `F` (if the local row streams), an end-to-end
  HTTP/2 PING (not modelled), or Stop.
- **CEX-2 `fk_idle`, FRAMEONLY (A5).** Heartbeats at `t=3` and `t=5` are not counted; the guard (armed at `t=0`, `I=5`) fires at `t=5` on a healthy remote in
  backoff or in `BLK`. *Rejected design confirmed* (not a defect of the plan).
- **CEX-3 `fk_idle`, `H=I=5` (A11).** Beats at `t=1` and `t=6`; the guard fires at `t=6` (the tie goes to the timer). *Spec gap*: no constraint
  `H < I` is stated; `agent.llm_stream_idle_timeout_ms` is user-settable.
- **CEX-4 `stall_lost` (E1).** `USTALLF`: remote in `UP` forever; heartbeats every 2 ticks keep `idle` at 0; the remote's guard is
  not armed (no upstream byte); at the horizon the call is alive and the slot held. *System gap vs 5a "a stall before the
  first chunk becomes upstream"*: `idleBody` arms on the first byte read (`transport.go` `arm()` only for `n > 0`), the
  comment of `provider.go` 237-243 says the first byte is the first-token guard's job, and the remote has no such guard.
- **CEX-5 `hang` `SF=1` (E9).** `BLK` entered at `t=9`, the model stalls at `t=10`, heartbeats flow, slot held at the horizon.
  *Spec gap*: "a row with `stream: false` is not guarded" leaves it unbounded.
- **CEX-6 `fk_ft` (E12).** `SF=1`, beats at `t=3, 5, 7`, the first-token timer fires at `t=8` (`F=8`) with the remote healthy:
  a heartbeat is not a chunk. *Spec contradiction at the agent level* with the scenario "a `stream: false` row that
  generates for six minutes is not cut" (`react.go` 908: armed because the local row streams; `retry_budget_ms` carries
  the same timer).
- **CEX-7 `leak_stream` (G2).** Silent cut at `t=2`; the client's guard fires at `t=6`; the handler keeps writing
  heartbeats (`t=3, 5, 7`) until the buffer overflows at `t=7`, the write blocks and the deadline frees the slot
  3 ticks later (`t=10`). A retry now needs a second slot while the first is held. *Spec over-claim*: `W` bounds a blocked write only.
- **CEX-8 `bad_emitted` (F2, mutation).** Token at `t=3`, stall, error frame at `t=9` with `emitted=false`: `seen=1`, the
  client would repeat the call and print the same text twice.

**Observation outside the model (4.3).** `streamStalledError` (`transport.go` 63) has no `emitted` field; the contract lives in
`streamTransportError{cause, emitted}` (`openai_stream.go` 494-498, checked first in `resilient.go` 459-463). A coddy client
that turns `cause: stall, emitted: true` into a bare `*streamStalledError` is retried by the `stalled` branch
(`resilient.go` 465-470): the repeat after output that P4 forbids. For `emitted: false` the same typed error is retried
locally although the 4.3 table says `upstream`: no local retry.

## 7. Spec statements

CONFIRMED: comments count and the heartbeat makes backoff and `stream: false` safe against the client guard iff `H+J < I`
(4.2, 5a "remote backoff longer than the client idle timeout does not trip the client guard"); rejection of frame-only
counting; a relay cut is a transport failure (4.3: EOF at once, silent by `I`); stall after output is terminal with
`emitted == true`; a blocked write is cut within `W`; the first heartbeat arms the guard.
CONTRADICTED: "a stall before and after the first chunk becomes `upstream`" (before the first upstream byte: hang);
"a stalled writer is cut by the per-write deadline" as a release of the slot (vanished peer); "a `stream: false` row of six minutes
is not cut" at the agent level (`F`); "the remote sends the first heartbeat right after the headers so ... does not look
like a dead peer" covers only the time after the headers.
NOT DECIDABLE here: real-time effects, TCP/HTTP2 PING detection, the race inside `idleBody` timers, ratios beyond A, B, R.

## 8. Numeric constraints the plan must state (plan values: `H=15 s I=300 s S=300 s P=W=60 s D<=60 s per sleep F=90 s`)

1. `H + J < I` (J = byte-gap jitter: scheduler, network, relay). Plan: `15 + J < 300`. Rule: `I = 0` or `I >= 2H` (30 s)
   for a `coddy` row, warned by `coddy -t`; the key is shared with direct rows, so a 20 s setting would kill every idle phase.
2. No relation between `S`, `D`, `G` and `I` is needed with a counted heartbeat (checked `D, G >= I`). Without it
   (`NOHB`, `FRAMEONLY`) `I > D+P, G, S` is required: the plan's six-minute row (`G = 360 s`) exceeds `I = 300 s`.
3. Detection of a dead remote: `I-H-J+1 <= d <= I` ticks after the crash, only after the first body byte; before it the
   bound is `F` if armed, else none; arming at request start needs `P < I`.
4. Proxies: `H + J < L` and `P < L` for every hop (checked: `L > max(H+J, P)` exactly, at A, B and R). nginx
   `proxy_read_timeout` 60 s between two successive reads (fetched from nginx.org), Cloudflare proxy read timeout 125 s
   today (developers.cloudflare.com, connection limits; 100 s in older documentation and in the brief). `H = 15 s` is
   **sufficient** for both (4x, 6.7x at 100 s, 8.3x at 125 s) and **necessary for neither**: letting `m` consecutive beats
   be late needs `(m+1)H < L`, so nginx allows `H < 60` for `m = 0`, `H < 20` for `m = 2`, Cloudflare 100 s `H < 33`. 15 s is the
   largest multiple of 5 s that survives two late beats under 60 s (`3H = 45 < 60`; `H = 20` would not). **Neither the plan
   nor the references state `m`, nor a limit below
   60 s**, so the plan should say "`H <= L_min / 3`, `L_min = 60 s`". Note `P = 60 s` equals nginx's 60 s: the body-read
   deadline must be below the smallest hop limit (nginx buffers the request body by default, so the real `P` is small, but
   the plan should say 30 s or "below L").
5. `F > P + D + G` (`stream: false`) and `F > P + D + Gmax` (streamed). Plan: `90 s` fails for `G = 360 s`.
6. `S`: armed at call start (proposal), larger than the longest healthy first-byte latency; `S` against `F`: with `F < S`
   (90 s < 300 s) the client's timer ends a stall before the first chunk, not the remote's error frame.
7. `W`: bounds a blocked write only. Slot hold after a vanished peer `<= (CAP+1)(H+J) + W` (CAP in heartbeat-sized writes:
   a 64 KiB window holds thousands, so hours at `H = 15 s`); with a probe `<= PINGT` (proposal 45 s = 30 + 15, the client's own
   constants).

## 9. Recommended spec changes (replacement text)

**4.2, replace the bullet "Stall and liveness are two guards" by:**
"- **Three timers, three jobs.** *Model progress* is the remote's: it applies its `StreamIdleTimeout` S to a streamed row from
the moment the provider call starts, so an upstream that accepts the request and never answers ends (changing the arming of the standard guard) as `error{upstream,
cause: stall, emitted: false}` after S (the standard byte guard arms only on the first byte read, so the handler arms it
explicitly); a `stream: false` row is not guarded by S, and its call is bounded by a remote setting `max_call_ms` **[proposal,
value to choose]** so that a hung blocking row cannot hold a slot for ever. *Liveness of the remote and of the path* is the
client's: the byte-level guard of `NewProvider`'s HTTP client, armed by the first body byte, counting **every** byte, comments
included and never only event frames, firing when no byte arrives for I. The remote writes the comment `: hb` and a blank line
as the first body bytes, right after the headers, and then so that no two bytes of the response are more than H = 15 s apart.
Constraints, repeated in the config reference and checked by `coddy -t`: `H + J < I` (the client warns when `0 < I < 2H`);
`H + J < L` and the body-read deadline `< L` for every hop of idle limit L (nginx 60 s, Cloudflare 100-125 s; H <= L_min / 3 lets two beats be late); a dead remote is
cut `I-H-J+1` to `I` after its last byte and only after the first body byte, and before the headers the call is bounded by the
first-token timer when armed or by a response-header timeout of `P + 15 s` **[proposal]**. *The agent's first-token timer*
is stopped by a chunk only, never by a heartbeat; for type `coddy` it is not armed (the remote owns model progress), or the
listing says the row is blocking **[decide]**."

**4.2, replace the write-deadline sentence by:** "The server has no `WriteTimeout`; each write gets its own deadline of about
60 s, refreshed before the write, so a write that blocks (the peer's window and the socket buffers are full) is cut, the upstream
call is cancelled and the slot is released within W. A peer that vanishes without a trace while only heartbeats are written never
blocks a write for hours, so the handler also probes the connection (HTTP/2 PING with 30 s read-idle and 15 s ping timeout where
the listener speaks HTTP/2, TCP keepalive and a user timeout otherwise **[mechanism per listener to confirm in the code]**) and
a failed probe cancels the call: the slot is held at most 45 s after the peer vanished."

**5a:** Server row: "a row with `stream: false` that generates for six minutes is not cut" gets "(provider level; at agent level it
needs the first-token timer off)"; add "an upstream that never answers ends as `error{upstream, stall}` after S (streamed) or
`max_call_ms` (blocking)", "a vanished peer frees the slot within the probe bound". Client row: "a `cause: stall, emitted: true`
frame is not retried (typed with `emitted`, not a bare `streamStalledError`)".

## 10. Commands to reproduce

`MC='mcd check --promela docs/plans/remote-model-provider-models/m5-guards.pml --no-timing --budget-ms 400000'`, scale A is the
file default; every row is `$MC -D <defs> --ctl '<formula>'`, with `H0='-D CRASHF=0 -D CUTF=0 -D FREEZEF=0'`:
- A1: `$MC $H0 -D MSTALLF=1 -D USTALLF=1 -D SF=1 -D HB=1 -D FRAMEONLY=0 -D ARM=0 --ctl 'AG (fk_idle == 0)'`;
  A5: `... -D MSTALLF=0 -D USTALLF=0 -D SF=1 -D FRAMEONLY=1 -D ARM=2 ...`; A11: `-D HPER=4 -D HJIT=1 ...`.
- B1: `$MC $H0 -D MSTALLF=1 -D USTALLF=1 -D SF=1 -D PXL=3 --ctl 'AG (fk_proxy == 0)'`; C1: `-D SF=1 -D FTMO=16 --ctl 'AG (fk_ft == 0)'`.
- D5: `$MC -D CRASHF=1 -D CUTF=1 -D FREEZEF=0 -D MSTALLF=0 -D USTALLF=0 --ctl 'AG (undet == 0)'`; D1: `... --ctl 'AG (undet_armed == 0)'`;
  D2: `-D DB=4 ...`; D4: `--ctl 'EF (det_lb == 1)'`; D9: `-D LATEFIRST=1 --ctl 'AG (undet_hdr == 0)'`.
- E1: `$MC $H0 -D MSTALLF=1 -D USTALLF=1 -D SF=0 -D SARM=0 --ctl 'AG (stall_lost == 0)'`; E9: `-D SF=1 --ctl 'AG (hang == 0)'`;
  F2: `-D SARM=1 -D BADEMIT=1 --ctl 'AG (bad_emitted == 0)'`.
- G2: `$MC -D CRASHF=0 -D CUTF=1 -D FREEZEF=1 -D MSTALLF=0 -D USTALLF=0 -D SB=6 --ctl 'AG (leak_stream == 0)'` (violated), `SB=7` verified;
  G6: `-D PINGT=3 -D SB=1`; G9a/b/c: `-D CRASHF=0 -D CUTF=1 -D FREEZEF=1 -D MSTALLF=0 -D SARM=0 -D CAPB=30 -D SB=20` with `-D USTALLF=0`
  (verified), `-D USTALLF=1` (violated), `-D USTALLF=1 -D PINGT=3` (verified), all `--ctl 'AG (leak_stream == 0)'`; H2: `--ctl 'AG ((cutp == 0) || (cut_silent == 1) || (frozen == 1) || (cl == 3))'`.
- Scale B prefix: `-D HPER=3 -D CIDLE=9 -D SSTALL=10 -D DBACK=10 -D GGEN=12 -D GMAXGAP=3 -D PPRE=5 -D WDL=4 -D CAPB=3 -D TMAX=48`;
  scale R: `-D HPER=1 -D CIDLE=20 -D SSTALL=20 -D DBACK=4 -D GGEN=24 -D GMAXGAP=4 -D PPRE=4 -D WDL=4 -D TMAX=90`.
Model file sha256 prefix `45361ef6d99b77b4` (final); `mcd 0.2.0`. Decode a counterexample with `--bfs` (shortest) and read `counterexample.steps` by tick (`t = t + 1` opens a tick).

## 11. Limitations

Discrete clock, zero delay, one call, one remote; a chunk is a unit; the retry loop after a cut or a stall is outside the model
(only the `emitted` it is given); the HTTP/2 PING path and TCP behaviour are assumed away; the listener's real behaviour
(TLS, h2, keepalive of `coddy serve`) was not read beyond `ListenAndServeTLS` in `external/swarm`; the model is of the plan, not
of code that does not exist yet (no `coddy` provider or handler in the tree), so conformance to the implementation needs tests of
the 5a rows listed above.
