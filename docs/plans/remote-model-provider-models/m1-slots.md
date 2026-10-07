# m1-slots: the concurrency limiter of the shared-model route

Model: `m1-slots.pml` (one file, `-D` switches). Spec: `docs/plans/remote-model-provider.md` 4.1a, 4.2 (stateless handler,
body limit, per-write deadline, disconnect), the last bullet of 4.5 (the relay as one caller), rows Server/Config of 5a.
Scenarios: "Five streams run at once and a sixth is told to retry", "The relay's clients share the node's stream limit".
Engine: `mcd 0.2.0` (CLI; no `mc_simulate`, no `mc_lint_property`: properties were classified by hand with
`properties-ltl-ctl.md`, and no pilot random walk was run; the pilot was `--estimate` and `--sweep` state counts).
Model sha256: last section.

## 1. Intake card

| Field | Content |
|---|---|
| Boundary | One node's `POST /coddy/llm/completions` handler and its per-key limiter. Outside: the other routes, the client's busy wait (m2), the DTO and error vocabulary, authentication itself (a request arrives with a key), the provider's own behaviour beyond "completes / errors / stalls". |
| State | `active[2]` (running calls per key, the object under test); per request: pc `st`, scenario, `tries`; ghost `held[i]`, `heldA/heldB[i]` (owns a slot), `takes/rels`, `bufd`, `rdstart`; `bodies[k]` (buffered bodies, the abstraction of bytes); `finished`. |
| Atomic step | `take_or_busy` (test the count and increment, one step: the code has to hold a mutex or run a CAS loop, see MUT_NONATOMIC), `release` (drop the body, decrement, one step), "body buffered", every wait-state decision. Steps that touch only local variables are merged into the neighbouring visible step. |
| Composition | Interleaving of N request processes plus `init`; no channels; shared counters. |
| Environment | Per request, chosen by `init` among 5 scenarios: ok, validation failure (400/404/413-by-body) after the body, Content-Length 413 (before any read), slow-body staller (never completes the body, never disconnects), non-reader (never reads the response, never disconnects). Normal clients may disconnect at every wait state and may be cut by a spurious deadline. |
| Initial state | all counters 0, no request started. |
| Properties | section 3. |
| Fairness | none needed for the verdicts (no infinite behaviour: every request ends); weak fairness run anyway (section 3). |
| Budget | up to 40M states / 590 s per run; scaled LIMIT 2 and 3 (the plan's 5 is not run). |
| Expected evidence | `exhaustive` on every scaled instance; no claim about LIMIT = 5 beyond the argument in section 8. |

Mapping (spec -> model): "key" -> `kof[i]` in {0,1} (`-DONEKEY`: all 0, the relay as one caller); "slot" -> `held[i]`
and one unit of `active[k]`; "busy" -> the `else` branch of `take_or_busy`; "body read deadline" -> `NOBODYDL` off, the
slow-body staller's `FIRED` option; "per-write deadline" -> `NOWRITEDL` off, the non-reader's `ST_WR` option;
"`defer` release" -> `exit_slot` (every exit path ends in exactly one); "model stall turned to a terminal error" -> the
provider-error outcome (same step); "`stream: false` row" -> `UNGUARDED`; "bytes buffered" -> `bodies[k]`.

## 2. Model class

Finite, untimed, nondeterministic, interleaving. **Time is a discrete-time abstraction, not a timed automaton**: by default a
deadline is an always-enabled "expire" alternative in the state where it is armed (a superset of real behaviour: it can
fire early), `-DCLOCK` replaces it by a countdown of D ticks decremented by a `Tick` process with urgent expiry (D = 2 and 3).
Both give the same verdicts (section 5). Bounds: LIMIT in {2, 3}; N = 4 or 5 clients, more than LIMIT on one key (3 on key A and 1 on B
for LIMIT 2; 4 on A for LIMIT 3); TRIES in {1, 2}. Hand symmetry reduction: the clients of one key take their scenarios in
non-decreasing order, so only properties invariant under permuting clients of a key are asked.

## 3. Properties and results

Reading the engine words: for an LTL invariant `verified` / `exhaustive` means "holds on the model"; for `EF` (CTL) `verified` means
reachable. Where CTL was too slow (N = 4 with ghosts) reachability is asked as the LTL `[] !p`: `violated` there means "p is
reachable" and the witness is the counterexample.

Properties (S1..L2 are LTL, `HA` = `heldA[0] + ... + heldA[N-1]`, `HB` likewise):

| id | text | class / logic |
|---|---|---|
| S1 | `[] (active[0] <= LIMIT && active[1] <= LIMIT)` | safety, LTL invariant |
| S2 | `[] (active[0] == HA && active[1] == HB)`: the count equals the number of requests holding a slot (no leak, no double release in the counter) | safety, LTL |
| S3 | `[] (bodies[0] <= LIMIT && bodies[1] <= LIMIT)`: buffered bodies at once | safety, LTL |
| L1, L2 | `[] ((active[k] > 0) -> <> (active[k] == 0))` for k = 0, 1: no slot is held forever | liveness, LTL (`stutter_invariant`, `X`-free) |
| assert | in-model asserts: busy only if `active[k] == LIMIT` at the decision; busy before any body byte was read; the provider is called only while the slot is held; a release only of a held slot (exactly-once, no double release); at the end of each request nothing held, `takes == rels`, no body kept; at the end of all requests `active == 0` and `bodies == 0` | safety |
| deadlock | no request is stuck (every request ends) | safety |
| R_* | reach (CTL `EF`, ghost `-DGHOST`): LIMIT reached on a key, a busy refusal, a completed stream, each exit path 1..6 released (complete, provider error, disconnect, body timeout, 4xx, write timeout), a Content-Length 413 released before any body was read (`cl413rel`), LIMIT calls in the provider and a refusal at that moment (`busyfull`, the scenario), a refused client served after a slot freed (`servedafterbusy`, TRIES = 2), key A saturated while a key B call runs (`R_isolation`), all slots pinned by slow-body stallers and a refusal (`busystall`, `R_denial`) | reachability |

Anti-vacuity reach and the sanity of the trigger are in the R_* rows below; every liveness property has its trigger
(`active[k] > 0`) shown reachable (`R_limit`).

### 3.1 Default design (NEW: slot before the body; both deadlines; two keys unless noted)

| Run (`-D`) | states | assert | deadlock | S1 | S2 | S3 | L1 | L2 | evidence |
|---|---:|---|---|---|---|---|---|---|---|
| N=4 (A: 3, B: 1), LIMIT 2, 5 scenarios | 1,482,597 (L1: 2,764,200) | verified | verified | verified | verified | verified | verified | verified | exhaustive, complete |
| N=4 ONEKEY, LIMIT 2 | 791,322 | verified | verified | verified | verified | verified | verified | verified | exhaustive |
| N=4 ONEKEY, LIMIT 3 | 688,408 | verified | verified | verified | verified | verified | verified | verified | exhaustive |
| N=3 ONEKEY, LIMIT 2 | 47,281 | verified | verified | verified | verified | verified | verified | verified | exhaustive |
| N=5 (A: 4, B: 1), LIMIT 3, no stallers (`NO_SLOWBODY NO_NOREAD`) | 5,076,079 | verified | verified | verified | verified | verified | verified | verified | exhaustive; S1/S2 in one run, S3, L1 (9,393,299 product states) and L2 (7,800,144) each in its own run because the joint run hit the time budget (`inconclusive` / `unknown`, not a result) |
| N=3 ONEKEY, `CLOCK` D=2 | 65,945 | verified | verified | verified | verified | verified | verified | verified | exhaustive |
| N=3 ONEKEY, `CLOCK` D=3 | 75,720 | verified | verified | verified | verified | verified | verified | verified | exhaustive |
| N=4 ONEKEY, `CLOCK NO_SLOWBODY NO_NOREAD` | 285,481 | verified | verified | verified | verified | verified | verified | verified | exhaustive (no timers armed without stallers) |
| with `--fairness weak` (N=3 ONEKEY; N=3 ONEKEY CLOCK) | L1 product: 274,027; 463,888 | - | - | - | - | - | verified | verified | same as without fairness: the result does not depend on fairness |

Reach (all `exhaustive`):

| Run | result |
|---|---|
| N=3 ONEKEY `GHOST TRIES=2` (170,277 states, CTL `EF`) | `R_limit`, `R_busy`, `R_complete`, exit paths 1..6, `R_cl413rel`, `R_scenario` (`busyfull`), `R_servedafterbusy`: all `verified` = reachable |
| N=3 ONEKEY `GHOST` (87,239) | `R_denial` (`busystall`) and `R_scenario`: reachable |
| N=4 two keys `GHOST` (2,943,930; as LTL `[] !p`) | `active[0] == 2 && active[1] >= 1` (isolation), `active[0] == 2`, `busyfull`, `cl413rel`, `busystall`: each `violated` with a witness = reachable |
| N=3 NA=2 two keys `GHOST` (130,751, CTL) | `R_limit`, `R_isolation` reachable; `R_busy`, `R_scenario`, `R_denial` unreachable as expected (2 clients per key do not exceed LIMIT 2: no contention here, so nothing is claimed from this instance about busy) |

### 3.2 Variants and mutations (anti-vacuity). N=3 ONEKEY, LIMIT 2, 5 scenarios unless noted. `X` = `violated` / `exhaustive` with counterexample, `V` = `verified`

| Variant | assert | deadlock | S1 | S2 | S3 | L1 | What fails (decoded in 4) |
|---|---|---|---|---|---|---|---|
| default | V | V | V | V | V | V | - |
| `OLD` (slot after the body) | X | V | V | V | X | V | busy after the body was read; 3 bodies buffered with LIMIT 2. Same at N=4 ONEKEY (LIMIT 2 and 3) |
| `NOWRITEDL` | V | X | V | V | V | X | non-reader holds its slot in `ST_WR` forever. N=4 ONEKEY LIMIT 3 and CLOCK: same. Under `--fairness weak`: still X. With `NO_NOREAD`: V (the non-reader is the cause) |
| `NOBODYDL` | V | X | V | V | V | X | slow-body staller holds its slot in `ST_RD`. CLOCK, weak fairness: same. With `NO_SLOWBODY`: V |
| `UNGUARDED` (stream:false, hang) | V | X | V | V | V | X | model stall never ends; only a disconnecting (normal) client frees it. With `NO_SLOWBODY NO_NOREAD`: V. Weak fairness: still X |
| `MUT_HIGH` (take when count <= LIMIT) | V | V | X | V | X | V | S1 counterexample |
| `MUT_EARLY` (busy at LIMIT-1) | X | V | V | V | V | V | `assert(active[k] == LIMIT)` at the busy decision |
| `MUT_SHARED` (N=3 NA=2: one count for both keys) | X | V | V | V | V | V | key B refused while `active[1] == 0`; `R_isolation` becomes unreachable |
| `MUT_NONATOMIC` (check then increment) | V | V | X | V | X | V | three requests pass the check, count reaches 3 |
| `MUT_DOUBLEREL` (4xx path and defer both release) | X | V | V | X | V | X | `assert(held == 1)` fails, counter off by one |
| `MUT_BODYAFTER` (body dropped after the release) | V | V | V | V | X | V | transient extra body |
| `LEAK=1..6` (exit path p forgets to release) | X | V | V | V | V | X | `fin_check` and L1 for every p: each of the six exit paths is covered |

## 4. Decoded counterexamples (`mcd ... --bfs`; "decoded", not replayed: the CLI has no `mc_simulate`)

1. **NOWRITEDL, L1 / deadlock (system defect if the deadline is dropped; confirms the spec needs it).** The shortest run: three
   requests, all non-readers (`ct = 2`). Req 1 and Req 2 take the two slots (`active[0]: 0->1->2`), read their bodies, get a provider
   result, enter `ST_WR` (`st = 5`) where nothing is enabled; Req 3 is refused busy (`assert(active[k] == 2)` passes) and ends. Lasso:
   loop = the stutter step, `active[0] == 2` forever. Classification: the described system defect; with `NOWRITEDL` off the same
   scenario ends in the write-deadline option (path 6) and `deadlock` / L1 are `verified`.
2. **OLD, assert and S3.** Two requests hold the slots (both in the provider phase, two bodies buffered); the third reads its body (`bodies[0]: 2->3`, three
   bodies at once with LIMIT 2) and only then is refused (`assert(rdstart[i] == 0)` fails). Classification: the rejected design is
   worse in exactly the way the plan says (memory bound and "busy answers without reading").
3. **UNGUARDED, deadlock.** Reqs 1 and 2 finish; Req 3 (non-reader) took a slot, read its body, entered the provider phase and
   moved to `ST_HANG` (`st = 6`): no option is enabled for a client that never disconnects. Classification: spec gap (section 7, item 4):
   the hold time of a call is bounded only by `provider.Stream` returning; a `stream: false` row is not stall-guarded and
   `providers[].timeout_ms` defaults to 0 (`internal/llm/provider.go`, `Timeout`: "the turn context stays the only bound").
   Model assumption stated: the provider never returns (worst case); with normal clients only the model is `verified` because a normal
   client eventually disconnects, which is an environment assumption about the client (its own timers), not a property of the node.
4. **MUT_NONATOMIC, S1.** Reqs 1, 2, 3 each pass the `ROOM(k)` check (`st = 1`), then increment: `active[0]: 0->1`, `1->2`, `2->3`.
   Classification: mutation; the plan does not say the test and the increment are one operation (section 7, item 1).
5. **MUT_SHARED, assert.** Reqs 1 and 2 (key A) hold two slots; Req 3 (key B, `k = 1`) is refused with `active[1] == 0`: the assertion
   `active[k] == 2` fails. Classification: mutation; shows the isolation property can fail.
6. **Scenario witness (N=3 ONEKEY GHOST TRIES=2, `servedafterbusy`).** Req 2 and Req 3 hold the two slots, Req 1 is refused busy at
   `active[0] == 2` (`nbusy: 0->1`), Req 2 ends by the write deadline (path 6, `active[0]: 2->1`), Req 1 asks again, takes the slot,
   completes (path 1) and is counted `servedafterbusy`. This is the executable scenario of the main feature at LIMIT 2.

## 5. Commands to reproduce

`m1-slots.pml` is run with `-D` symbols; every command ends with
`--max-procs 5 --budget-states 40000000 --budget-mem-mb 24000 --budget-ms 590000 --no-timing`
(abbreviated `$B` below; `--max-procs 6` for N=5). Formulas for N = 4 (for N = 3 drop the index 3, for N = 5 add 4):

```
S1='[] (active[0] <= 2 && active[1] <= 2)'                 # LIMIT=3: replace 2 by 3
S2='[] (active[0] == (heldA[0] + heldA[1] + heldA[2] + heldA[3]) && active[1] == (heldB[0] + heldB[1] + heldB[2] + heldB[3]))'
S3='[] (bodies[0] <= 2 && bodies[1] <= 2)'
L1='[] ((active[0] > 0) -> <> (active[0] == 0))'
L2='[] ((active[1] > 0) -> <> (active[1] == 0))'
mcd check --promela m1-slots.pml -D N=4 --ltl "$S1" --ltl "$S2" --ltl "$S3" --ltl "$L1" --ltl "$L2" $B     # default, two keys
mcd check --promela m1-slots.pml -D N=4 -D ONEKEY [-D LIMIT=3] ... $B                                      # the relay
mcd check --promela m1-slots.pml -D N=3 -D ONEKEY -D OLD ... $B    # variants: OLD NOWRITEDL NOBODYDL UNGUARDED
                                                                  # MUT_HIGH MUT_EARLY MUT_NONATOMIC MUT_DOUBLEREL MUT_BODYAFTER LEAK=1..6
mcd check --promela m1-slots.pml -D N=3 -D NA=2 -D MUT_SHARED ... $B
mcd check --promela m1-slots.pml -D N=3 -D ONEKEY -D CLOCK [-D D=3] [-D NOWRITEDL] ... $B
mcd check --promela m1-slots.pml -D N=3 -D ONEKEY --fairness weak --ltl "$L1" --ltl "$L2" $B                # also with NOWRITEDL etc.
mcd check --promela m1-slots.pml -D N=3 -D ONEKEY -D GHOST -D TRIES=2 --ctl 'EF (active[0] == 2)' --ctl 'EF (nbusy > 0)' \
  --ctl 'EF (pr[1] == 1)' ... --ctl 'EF (pr[6] == 1)' --ctl 'EF (cl413rel == 1)' --ctl 'EF (busyfull == 1)' \
  --ctl 'EF (servedafterbusy == 1)' --ctl 'EF (busystall == 1)' $B
mcd check --promela m1-slots.pml -D N=4 -D GHOST --ltl '[] !(active[0] == 2 && active[1] >= 1)' --ltl '[] !(busyfull == 1)' ... $B
mcd check --promela m1-slots.pml -D N=3 -D ONEKEY --sweep $B      # state counts of the whole graph
```

Verdicts of the `assert` and `deadlock` properties come from the same runs (the properties of the model itself are always added:
no `--ltl`-only run replaced them). Properties passed with `--ltl` were checked in the same invocation as `assert` / `deadlock`.

## 6. Spec statements: CONFIRMED / CONTRADICTED / NOT DECIDABLE

| Statement (section) | Verdict | Evidence |
|---|---|---|
| At most LIMIT shared calls run at once per key (4.1a) | CONFIRMED | S1 verified, all default runs; S1 fails under MUT_HIGH and MUT_NONATOMIC |
| The count covers all aliases of a key | NOT DECIDABLE here | aliases are not modelled; one key = one counter by construction; the key's definition (bearer hash, `anonymous`) is code, not behaviour |
| A further call is refused at once with `busy` only when the key is full, before anything is sent to the provider | CONFIRMED | assert (busy only at `active[k] == LIMIT`, provider only with a held slot); MUT_EARLY and MUT_SHARED fail it |
| Saturation of key A never blocks key B | CONFIRMED | assert at every busy decision on N=4 two keys; `R_isolation` reachable; MUT_SHARED violates the assert and makes `R_isolation` unreachable |
| The slot is taken before the body is read, so busy answers without reading; buffered bodies are at most LIMIT per key (4.1a) | CONFIRMED | assert (`rdstart == 0` at busy) and S3 verified for NEW; OLD violates both (decoded, item 2) |
| A Content-Length above the limit answers 413 before any read | CONFIRMED (by construction) | the branch has no read; `R_cl413rel` reachable; the assert `kind != K_CL413` guards the read start. The 32 MiB limit itself is not modelled |
| A 400, 404 or 413 releases the slot the moment it is answered | CONFIRMED | exit path 5 reachable and released once; `R_p5_4xx`, `cl413rel`; MUT_DOUBLEREL shows the two-release hazard |
| The slot is released by `defer` on completion, provider error, client disconnect and body-deadline expiry | CONFIRMED | assert (exactly once, nothing left, `active == 0` at the end), S2, L1/L2 on every default run; paths 1..4 reachable; LEAK=1..4 each detected. The write-deadline exit (6) is also released |
| Through a relay the limit counts all clients together (4.5, last bullet) | CONFIRMED | `-DONEKEY`: all properties hold with 4 clients and LIMIT 2 and 3; a refusal at `active[0] == LIMIT` is reachable and does not depend on which client holds the slots |
| A client that stops reading would hold the slot forever without a per-write deadline; each write's deadline cuts it (4.2) | CONFIRMED | NOWRITEDL violates L1 and `deadlock` (also under weak fairness and with CLOCK); with the deadline both verified |
| The body read deadline releases a slot held by a client that stops sending (4.1a) | CONFIRMED | NOBODYDL violates L1/`deadlock`; default verified (also CLOCK) |
| "The slot is released by `defer` once `provider.Stream` has returned" is enough to bound the hold time | CONTRADICTED for a call that never returns | UNGUARDED: a `stream: false` row has no idle guard and `timeout_ms` defaults to 0; the slot is held until the provider answers or the client disconnects (item 3). Classification: spec gap, not a model defect |
| A stalled model is turned into a terminal error (4.2) | CONFIRMED (as assumption) | modelled as the provider-error outcome (same step); the guard itself is the remote's `StreamIdleTimeout`, not decided here |
| Real durations (60 s deadlines, 15 s heartbeat, `Retry-After`) | NOT DECIDABLE | untimed engine; the discrete-time abstraction shows only that every wait is eventually cut |
| Memory bound: live bodies at most LIMIT per key | CONFIRMED on the model, NOT DECIDABLE in Go | the body is dropped in the release step; real garbage lives until a GC cycle (MUT_BODYAFTER shows the ordering matters) |
| Every retrying client is eventually served | NOT DECIDABLE here | no infinite behaviour in the model; a witness exists (item 6) but a starved retrier is a question of the client's budget (`busy_wait_ms`, m2) |

## 7. Decision and recommended spec changes

There is no "[decide at modeling]" item in this task's scope. The comparison NEW against OLD supports the plan's choice
(slot before the body): NEW verifies S3 and "busy without reading", OLD violates both. The price of NEW is visible in the model:
`R_denial` (`busystall`) is reachable, that is, LIMIT slow-body clients of one key can pin every slot until the body deadline and
refuse the key's other clients with `busy`; in OLD such clients hold no slot but unboundedly many bodies are buffered (S3).

Recommended changes (replacement text, plan section numbers):

1. **4.1a**, append to the first bullet: "The count of a key is one value under a mutex (or a compare-and-swap loop): the test against
   the limit and the increment are one atomic step, otherwise concurrent calls overshoot the limit. A slot is released exactly once per
   successful take, by one release function guarded by a flag (or `sync.Once`) that the 400/404/413 paths and the `defer` both call.
   The body buffer is dropped no later than the release." Evidence: MUT_NONATOMIC (S1 violated), MUT_DOUBLEREL (assert and S2 violated).
2. **4.2** (or 4.1a, "slot is released by `defer`"), replace "The slot is released by `defer` once `provider.Stream` has returned" by:
   "The slot is released by `defer` once `provider.Stream` has returned. A call that never returns holds its slot until the client
   disconnects: a row with `stream: false` is not stall-guarded and `providers[].timeout_ms` is 0 by default. `coddy -t` warns about a
   shared row with `stream: false` and no `timeout_ms` (or the plan adds `httpserver.shared_models.max_call_ms`, above the longest
   legitimate call, cancelling the provider context into `error{kind: upstream, cause: stall}`)." Evidence: UNGUARDED.
3. **4.5**, last bullet, append: "The slot is held while the request body is read, so up to `max_streams` clients that send slowly can
   pin a credential for the length of the body deadline, repeatedly, and the credential's other clients get `busy` meanwhile; through a relay
   these are all clients of the relay. The limit protects the node and its provider, not clients from each other (per-client scopes: phase 3)."
   Evidence: `R_denial` reachable.
4. **5a**, row Server: add the unit cases "N concurrent calls above the limit, held by a barrier, never exceed the limit (race detector on)",
   "each exit path (complete, provider error, disconnect, body timeout, 4xx, write timeout) leaves the count at zero, and a second release
   cannot make it negative", "a stalled non-reader and a stalled slow-body client are each released by their deadline".

## 8. Why the scaled result carries over, and what does not

The invariants are threshold counters per key. Every proof obligation (S1, S2, "busy only at the limit", exactly-once release) compares a
counter with LIMIT and does not use its value; LIMIT 2 and 3 give the same verdicts for the default design, OLD and NOWRITEDL
(the only variants run at LIMIT 3; compare the LIMIT 3 rows) and the plan's 5 differs only in how many clients can queue before the refusal. Contention needs more clients
than LIMIT on one key, which every run has (3 against 2, 4 against 3, 4 against 2 on ONEKEY). This is an argument, not a run: LIMIT 5 was not
checked. The result is about the model: the real handler also has the authentication, the DTO, the 32 MiB read and the GC, which are outside it.

## 9. Limits and honesty notes

- Time is a discrete abstraction (section 2); durations, the 15 s heartbeat and TCP-level detection of dead peers are not modelled.
  Heartbeat comments are tiny and are not argued to replace the per-write deadline.
- Hand symmetry reduction: a property about one named client was not asked (L1/L2 are stated per key counter).
- `OLD` with `GHOST` is not supported (the staller ghost assumes the NEW slot order); `GHOST` runs are NEW only.
- CTL was too slow for N = 4 with ghosts (time budget, `inconclusive`); reachability there is the LTL `[] !p` form, same engine, complete witness.
- N=3 NA=2 `GHOST` shows no busy by design (2 clients per key); it is the isolation instance, not a contention instance.
- Engine facts: `mcd 0.2.0`, report schema `mcd-report/1`, DFS, no reduction (`--por` refused for the `atomic` blocks). No `mc_manifest`
  (CLI); the parameters are in the commands above. Model sha256 of `m1-slots.pml` as run:
  `181c39c7943a7fc6f72efd8acf8664c457ad3233efd9a860664445038ac29518`; run logs are in the agent's scratchpad (`m1-slots/r_*.txt`).
