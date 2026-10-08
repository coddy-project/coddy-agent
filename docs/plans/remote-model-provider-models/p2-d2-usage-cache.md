# Model check p2-d2-usage-cache: dilemma D2, the remote's usage per alias on the local side

Plan: `docs/plans/remote-model-provider-phase2.md`, sections 2 (W10), 4.1, 4.3, 4.4, 4.5, 7-D2, 8. Models: `p2-d2-usage-cache.pml`
(the three options U1/U2/U3 and the mutants behind `-D` switches) and `p2-d2-usage-cache-skew.pml` (the `reset_in_s` question).
Engine `mcd 0.2.0`, CLI, explicit-state, no MCP session. The CLI has no simulate and no lint: properties were classified by hand.
Nothing in the repository other than the two models and this file was touched. Code read (read-only):
`internal/session/provider_usage*.go`, `internal/acp/types.go`, `external/httpserver/providers_usage_http.go`,
`external/ui/src/ui/chat/{providerUsage,useProviderUsage}.ts`, `external/cli/usage.go`, `internal/remote/usage.go`,
`internal/session/context_window.go`, `internal/llm/coddy_errors.go`, `external/swarm/mount.go`.

## 1. Summary and decision

**DECISION: U1 (one local entry per (provider, alias)), with no account tag, no next-read hint, `reset_in_s` kept (and the absolute
`resets_at` dropped from the wire), a one-shot follow-up read after a turn end, and a 60 s memory of `supported: false`.**

| Group | Verdict on the model |
|---|---|
| P1/P1b/P2a the lender's upstream reads: at most one per floor for any number of clients and aliases; a client-driven one only after the TTL; a local GET per alias at least one floor after the previous | CONFIRMED for U1, U2, U3, U1+FOLLOW (also with failures for U1). Mutants (no gate, per-alias remote cache, `refresh=true`, no local floor) each violate the property they should |
| P2 node requests per client per 30 s window | CONFIRMED bounded: U1 = N_active x ceil(W/floor); measured 4 for N=2 (N=3: 6 derived, run not completed). U2 3 (N=2) / 4 (N=3): **not N-fold**. U3 4 (N=2): no gain in the worst case |
| P3 no non-stale entry shows another account than its alias's after TTL | CONFIRMED for U1 (bound exactly TTL) and U3 (needs the clamp); **CONTRADICTED for U2**: unbounded, a shared entry kept fresh by another alias; the guard restores a bound but only RV (the alias's own revalidation period) |
| P3s the same for entries marked stale | **CONTRADICTED** for every option: after a failed read the old account's numbers stay, marked stale, until a successful read (unbounded) |
| P4 a shown non-stale snapshot is built from a remote upstream read at most `2(TTL-1)` ticks earlier (a block shown, a lift shown) | CONFIRMED for U1/U2, tight (6 ticks = 30 s of ticks; 40 s as a continuous bound); U3 5 ticks |
| P4b a turn's own spend is shown without any further trigger | **CONTRADICTED for plain U1** (unbounded: warm remote cache, quiet surface); CONFIRMED with the one-shot follow-up (6 ticks) and for U3 (5 ticks) |
| P5 the memory of `supported: false` hides a supported alias for at most M ticks (mechanism), and on the surface at most M + P - 1 given a read every P ticks | CONFIRMED, tight at two scales; unbounded on the surface without a read |
| P6 `reset_in_s` | CONFIRMED needed: with relative resets the lift shows within 7 ticks for any skew; with only `resets_at` a 60 s skew makes the lift never show (to the horizon) and the countdown wrong by the skew |

Status vocabulary below is the engine's; every `verified` is `exhaustive` on the model, a finite untimed model with a discrete clock.
Fairness: not used, there is no liveness formula (every property is a bounded-time safety property with urgent timers).

## 2. Intake card

- **Boundary.** Inside: the lender's per-account cache and its answer to `GET /coddy/llm/models/{alias}/usage`; one local manager's
  entry table; the relay request counter of that one client. Environment (adversarial, no fairness): any number of other clients
  reading any alias at any time (`OTHERS`); the lender's own refresh of its account (`LENDER`); the true account state; the operator
  repointing an alias (the remote's alias-to-account map); the lender enabling a reader (`ENABLE`); a failing GET (`FAILS`);
  the local triggers per alias: auto read KA (session ready, SPA poll, cache read), forced read KF (REST `refresh=1`, a reset),
  turn end KT (`publishProviderUsageAsync`), deferred fire KD, manual refresh KM (variant). Excluded: unauthorized stickiness, config
  swaps and fingerprint changes, a failing upstream on the lender, GET latency, more than two accounts, a second modelled client.
- **State.** Accounts 0..1; `map[alias]`; remote per account (`r_has`, value, `r_age`); local entries (`e_has`, `e_who`, `e_age`,
  `e_att`, `e_bo`, `e_stale`, `e_pend*`, OPT 3 `e_hold`), per alias `a_uns`, `atag`, `a_att`, `a_val`; ghosts per property slice.
- **Atomic step.** One event of one process (a trigger with its GET, a fire, a tick, an environment event). A GET is atomic: relay
  and node latency below one tick (declared).
- **Time: DISCRETE-CLOCK ABSTRACTION, not a timed automaton.** One tick = 5 s; the `Clock` process advances saturating counters
  together and refuses to tick while a deferred fetch is due (urgency) or, with `PTRIG=n`, while an alias has gone n ticks without a read.
  Constants: TTL 20 s = 4, floor 15 s = 3 (as `provider_usage.go:34-40`), M (unsupported memory) 6 and 12 as scaled values (the
  real values 5 min = 60 and the recommended 60 s = 12 follow the linear result), backoff cap BO 3, cost window W 6 (30 s).
- **Fairness.** None; all properties are safety, checked on the complete graph.
- **Relation to the implementation.** Hand-written from `internal/session/provider_usage.go`; no conformance argument is claimed.

## 3. Model classes and the engine

Finite, untimed with the explicit clock above. Interleaving; no channels; atomic blocks for the events (every temporary is zeroed
before an atomic step ends so no state carries it). Ghost counters multiply states, so each property is checked on a slice that carries
only its own ghosts (`G_SRC`, `G_REP`, `G_PT`, `G_UNS`, `G_BLK`, `COST`); a ghost that is off stays 0.
**Engine defect found.** `mcd 0.2.0` panics (`index out of range` in `cycleSearch.stateOf`, `explore/cycle.go:998`, called from `lasso`) when
an LTL property of a model that uses `atomic` is violated; reproducer: `bool f; byte c; active proctype P(){ end: do :: atomic { c < 3
-> c = c + 1 } :: atomic { c == 3 -> c = 0; f = true } od }` with `--ltl '[]!f'` (the same model with `d_step`, with `assert`, or with
`--ctl 'AG !f'` works). `verified` LTL runs are fine. All safety flags are therefore checked as `assert` (`-D T_<FLAG>`: the flag
becomes `assert(false)` where it is set) or as CTL `AG !flag`; both are complete-graph checks with `exhaustive` evidence.

## 4. Properties

| ID | Text | Class / logic | Flag, check |
|---|---|---|---|
| P1 | an upstream fetch of an account at least FLOOR after the previous one, for any clients/aliases | safety / invariant | `f_floor`; CTL `AG !f_floor` |
| P1b | a client-driven upstream fetch at least TTL after the previous one (the route never forces) | safety | `f_ttlc` |
| P2a | a local GET of an alias at least FLOOR after the previous GET of that alias | safety | `f_gap` |
| P2 | node requests of the one local client in any window of W ticks (exact maximum) | safety / measured | `maxc`; CTL `AG !(maxc>=k)` |
| P3 | a non-stale snapshot shown for an alias carries its alias's account, WB ticks after a repoint | safety | `f_wrong` (WB=TTL) |
| P3s | the same for stale-marked snapshots | safety | `f_wrongs` |
| P4 | a non-stale snapshot shown is at most AGEB ticks older than the remote's upstream fetch | safety | `f_age`, `maxsrc` |
| P4b | after a turn end a snapshot built from an upstream fetch after the turn is shown within PTK ticks, no further read assumed | bounded-time safety | `f_pt` |
| P5 | a supported alias answered "unsupported" by the memory for at most M ticks (mechanism); on the surface at most M+PB (needs `PTRIG`) | safety | `f_hid`, `f_hidd` |
| P6 | the lift of a block shows within LB ticks of the actual lift, for any skew; the countdown error | safety | skew model `f_lag`, `maxlag`, `maxderr` |
| S | sanity: block shown, lift shown, unsupported shown, cache hit, deferral, client-driven and lender upstream fetches reachable | reachability | `seen_*`, CTL `AG !seen_x` |

Atoms are model variables; vacuity guards: the sanity row (all seven `violated` = reachable, 111209 and 581 states) and the mutants of section 6.

## 5. Commands (all with `--no-timing`; the shell helper only summarises the JSON)

```
M=docs/plans/remote-model-provider-models/p2-d2-usage-cache.pml
mcd check --promela $M -D OPT=1 -D NL=2 -D OTHERS -D LENDER --ctl 'AG !f_floor' --ctl 'AG !f_ttlc' --ctl 'AG !f_gap'      # P1 P1b P2a, 7841 states
mcd check --promela $M -D OPT=1 -D NL=2 -D W=6 -D COST --ctl 'AG !(maxc>=4)' --ctl 'AG !(maxc>=5)'                          # P2 (OPT 2, 3: other k)
mcd check --promela $M -D OPT=1 -D NL=1 -D WB=4 -D RSTATELESS -D G_REP -D REPOINT0 -D T_WRONG --bfs                        # P3 (OPT=2 NL=2, GUARD RV=8 WB=8, CLAMP, LIAR)
mcd check --promela $M -D OPT=1 -D NL=1 -D G_SRC -D OTHERS -D LENDER -D T_AGE --bfs                                        # P4 (AGEB=5 violates)
mcd check --promela $M -D OPT=1 -D NL=1 -D FOLLOW -D PTK=6 -D G_PT -D OTHERS -D LENDER -D T_PT --bfs                       # P4b
mcd check --promela $M -D OPT=1 -D NL=1 -D RSTATELESS -D G_UNS -D ENABLE -D REPOINT0 -D UNSM=6 -D HIDB=5 -D T_HID --bfs     # P5 mechanism
mcd check --promela $M ... -D UNSM=6 -D PTRIG=2 -D PB=1 -D T_HIDD --bfs                                                     # P5 surface
mcd check --promela p2-d2-usage-cache-skew.pml [-D ABS] -D S=12 -D G=40 -D LB=100 --ctl 'AG !(maxlag>=k)'                   # P6
```
Bounds after `-D` are the tight pairs of section 7 (the bound that holds and the one that fails by one). Budgets: `--budget-states` up to
2e7, `--budget-ms` 240000-280000, DFS fails on depth for the unsliced model, so `--bfs` is used for the assert runs.

## 6. Results

### P1, P1b, P2a (remote pacing and the local floor)

| Config (`OTHERS LENDER`) | P1 | P1b | P2a | states |
|---|---|---|---|---|
| U1 NL=1 / NL=2 / NL=1 `FAILS` | verified | verified | verified | 2615 / 7841 / 193015 |
| U2 NL=2, with `GUARD RV=8` | verified | verified | verified | 7649, 23057 |
| U3 NL=2 `CLAMP`, `LIAR CLAMP` | verified | verified | verified | 16881, 30221 |
| U1 `FOLLOW` NL=2 | verified | verified | verified | 25217 |
| mutant `BUG_NOGATE` (remote always fetches) | **violated** | **violated** | - | 1543 |
| mutant `BUG_PERALIAS` (remote caches per alias) | **violated** | **violated** | - | 17217 |
| mutant `BUG_ROUTE_REFRESH` (route asks `refresh=true`) | verified | **violated** | - | 4380 |
| mutant `BUG_NOLOCALFLOOR` (forced reads ignore the floor) | - | - | **violated** (103 states) | 103 |

`BUG_ROUTE_REFRESH` shows what the plan's "never `refresh=true`" buys: the floor still holds (the manager gates it) but a client could
drive the lender's reader at 1 per 15 s instead of 1 per 20 s. The model's own `deadlock` is `verified` on every slice listed; on the
all-environment combinations (`REPOINT0 ENABLE TRUTH G_BLK FAILS` for U1+FOLLOW and for U2+GUARD, and U1+FOLLOW+FAILS NL=2) the
search was `inconclusive/unknown` (time budget, 3.6e5 to 7e5 states stored): not claimed.

### P2 (cost in a 30 s window, W = 6 ticks; exact maximum of `maxc`)

| Option | N = 2 aliases | N = 3 aliases |
|---|---|---|
| U1 | 4 (k=4 violated, k=5 verified; 20669 states, with `OTHERS`) | 6 = 3 x ceil(6/3) derived from P2a; the run did not finish in budget: **inconclusive** |
| U1 + FOLLOW | 4 (23028 states) | not run |
| U2 | 3 (4258 states) | 4 (72746 states) |
| U3 (`OTHERS`, `CLAMP`) | 4 (27621 states) | not run (budget) |

U2 does not give an N-fold saving: each alias still pays its first-contact GET (tag unknown), and a safe U2 needs the revalidation guard
(P3), which adds GETs back. U3 gives no saving in the worst case because the floor, not the hint, binds. U1's bound is per **active** alias:
an alias nobody reads (no turn, no surface) costs nothing.

### P3 (wrong account) and P3s

| Variant | WB | Result | states |
|---|---|---|---|
| U1 | 4 = TTL / 3 | verified / **violated** | 1281 / 1673 |
| U3 `CLAMP` | 4 / 3 | verified / **violated** | 1373 / 2195 |
| U3 `LIAR` (hint arbitrary, no clamp) | 4 | **violated** | 7641 |
| U3 `LIAR CLAMP` | 4 | verified | 1973 |
| U2 (tag, no guard) | 4 | **violated** | 29381 |
| U2 `GUARD RV=8` | 8 / 7 | verified / **violated** | 86691 / 151969 |
| U2 `GUARD RV=4` | 4 | verified | 22027 |
| U1 `FAILS`: non-stale (`f_wrong`) / stale-marked (`f_wrongs`) | 4 | verified / **violated** | 14549 |

**Decoded counterexample, U2 without guard (BFS, shortest).** (1) KA on alias 0: GET, tag 0, entry[0] = account 0. (2) the operator
repoints alias 0 to account 1 (`map[0] = 1`, `since_rep = 0`). (3) four ticks. (4) KA on alias 1 (tag unknown): GET, answer account 0, entry[0]
refreshed (age 0). (5) KA on alias 0: `atag[0] = 0`, entry[0] is fresh, served: alias 0 shows account 0's numbers, 4 ticks after its repoint.
Classification: **system defect of option U2** (the entry's freshness is maintained by other aliases, the alias itself never asks, so it never
learns its new tag). Not a model or property defect. **Proposed guard** (verified above, tight): an alias serves from a shared entry only while
its *own* last answer is younger than RV; the bound is RV, so the saving shrinks with the safety. The cheaper fence that suggests itself, the
listing `revision` (it already hashes the target model of the alias, `shared_models_http.go:113-121`), is too slow: the listing is trusted for
`contextWindowTTL` = 1 hour (`context_window.go:42`) and refreshed on a `stale_revision` answer, which only a call of that alias provokes.
(Code fact, not modelled.)

**U1 stale numbers (P3s).** A GET that fails keeps the entry's previous numbers marked `stale`; if the alias was repointed meanwhile they are
another account's numbers until a read succeeds. Classification: system behaviour inherited from `usageRecordFailureLocked`, flagged on the
wire to surfaces; the recommendation is to drop the numbers on `unknown_model` (4.3 text below).

### P4 (age of what is shown) and P4b (the turn's own spend)

| Variant | Bound | Result | states |
|---|---|---|---|
| P4 U1 (`OTHERS LENDER`) AGEB = 6 / 5 | 2(TTL-1) | verified / **violated** | 5143 / 4793 |
| P4 U2 NL=2 AGEB = 6 / 5 | | verified / **violated** | 53767 / 51997 |
| P4 U3 `CLAMP` AGEB = 5 / 4 (the plan's own TTL-1 = 3 also violated) | TTL+FLOOR-2 | verified / **violated** | 4617 / 4337 |
| P4 U1 with `FAILS`, with `FOLLOW` AGEB = 6 | | verified, verified | 37705, 8503 |
| P4b U1 plain, PTK = 4 / 12 | | **violated** both | 49033 / 115593 |
| P4b U1 `FOLLOW` PTK = 6 / 5 | TTL+FLOOR-1 | verified / **violated** | 33153 / 57472 |
| P4b U3 `CLAMP` PTK = 5 / 4 | | verified / **violated** | 12433 / 17377 |

**Decoded, P4b plain U1.** The surface reads (KA) at tick 0 and the remote fetches upstream at that GET; one tick later the turn ends (KT) and
spends quota; the forced read of the turn end is inside the local floor and is deferred two ticks; the deferred GET reaches the remote's
cache, which is still younger than TTL, and returns the pre-turn numbers (`ans_src = 3`); nothing reads again, so the post-turn state is not
shown for any bound. With `FOLLOW` the first fetch of the turn arms one more read TTL later, and the post-turn state shows within
TTL + FLOOR - 1 = 6 ticks (30 s). Classification: **system defect of the plan's D2 default** (property defect ruled out: the property is the
plan's "a block is shown within bounded time of the remote's own fetch" read for a quiet surface). U3's hold closes it in 5 ticks but only
because its deferral is also a follow-up.

### P5 (unsupported memory)

| Scale | `HIDB` (mechanism: ticks hidden while supported) | surface `PTRIG`, `PB` |
|---|---|---|
| M = 6 | 5 verified, 4 **violated** (also with `MANUAL`) | PTRIG 2: PB 1 verified, PB 0 **violated**; PTRIG 4: PB 3 verified, PB 2 **violated** (`PB=20` without `PTRIG`: **violated**, unbounded) |
| M = 12 | 11 verified, 10 **violated** | PTRIG 4: PB 3 verified, PB 2 **violated** |

So the mechanism hides a supported alias for at most M ticks (M-1 counted ticks), and the surface for at most M + P - 1 given a read of that
alias at least every P ticks; with no such read the surface hides it indefinitely (as it would hold any state, "nothing polls otherwise").
An earlier run flagged `f_hidd` for a lender that flaps support within one tick; that was a ghost artefact (the hidden clock now restarts at
each repoint) and is not a system defect. The `MANUAL` bypass leaves every bound unchanged (the adversarial scheduler never chooses it); it is a
remedy for the operator, not a bound. Layered memories (a surface mark on top of the manager's) add up, by the same argument; not modelled.

### P6 (`reset_in_s`), `p2-d2-usage-cache-skew.pml`

Skew sigma in {-S, -1, 0, 1, S} between the lender's and the borrower's clocks, lift at R in 2..8, a remote cache 0..3 ticks old, TTL 4,
GRACE 1, follow-up 6, only the schedule reads again. Maximum lag between the actual lift and the surface showing it (`maxlag`):

| Variant | S = 3 (15 s) | S = 12 (60 s) | countdown error `maxderr` (S = 12) |
|---|---|---|---|
| REL (`reset_in_s`, the plan) | 7 (= GRACE + FU) | 7 | 0 |
| ABS (only `resets_at`) | 8 | >= 30 = the horizon: the lift never shows | 12 = S |
| REL + `RESETAWARE` (remote refetches a block whose reset passed) | 1 | not run | - |
| ABS + `RESETAWARE` | 5 | >= 30 | - |

Cause of the ABS failure, decoded: with the local clock ahead by more than the time left, the client derives remaining 0, treats the reset as
passed, spends its single follow-up before the real reset and never reads again. Classification: system defect of a design that schedules
from a foreign absolute clock. REL's bound does not depend on the skew. `RESETAWARE` (not required) would cut the lag to one tick.

### Mutants and anti-vacuity, summary

Each property failed under its mutation or rejected design: P1/P1b (three remote mutants), P2a (`BUG_NOLOCALFLOOR`), P3 (U2, U3 `LIAR`, WB off by
one), P4 (AGEB off by one), P4b (plain U1, PTK off by one), P5 (HIDB off by one, no `PTRIG`), P6 (ABS). Sanity flags are all reachable.

## 7. Statements of the plan: CONFIRMED / CONTRADICTED / NOT DECIDABLE

- 4.1 step 4 "never `refresh=true`: a holder cannot make the lender read more often than TTL/floor": CONFIRMED (P1, P1b, mutants).
- D2 U1 "N aliases cost N GETs per cycle, all served from the remote's single per-account cache": CONFIRMED with a sharper bound: per active alias at
  most one GET per floor; the remote reads upstream at most once per TTL for clients.
- D2 U2 "N-fold fewer GETs": CONTRADICTED (4 -> 3 at N=2, 6 -> 4 at N=3 in the worst window). "A stale tag after a repointed alias would show one
  account's numbers under another": CONFIRMED, unbounded (decoded above).
- D2 U3 "reduces polls": CONTRADICTED in the worst case (4 = 4); freshness 6 -> 5 ticks; an untrusted hint breaks P3 unless clamped.
- "An alias never shows another account's numbers beyond a bounded time" (U1): CONFIRMED for non-stale numbers, bound TTL (tight);
  CONTRADICTED for stale-marked numbers.
- "How long a repointed alias keeps showing the old account": CONFIRMED <= TTL (20 s) in U1 from the cache; unbounded while reads fail (stale, marked).
- "Does the five-minute memory hide a lender that later enables its reader": CONFIRMED: <= M by mechanism, <= M + P - 1 on a read surface, unbounded
  on a quiet one.
- `reset_in_s` (W10): CONFIRMED needed (P6); the absolute `resets_at` then has no use on the client.
- "A block is shown within a bounded time of the remote's own fetch and lifted likewise": CONFIRMED for any delivery (P4), CONTRADICTED for a
  quiet surface after a turn (P4b) unless the manager arms the follow-up.
- NOT DECIDABLE here: relay latency under load, the real skew distribution, N=3 cost for U1/U3 (budget), the layered surface marks, any
  conformance of the Go code to the model.

## 8. Decision (evidence in sections 6 and 7)

1. **Option U1.** Safety (P3) with a bound that needs no new field; cost bounded by the floor per active alias and not worse than U2/U3 where it
   matters; no field that reveals which aliases share an account (equal numbers still reveal it to a careful reader, the plan accepts that, but a
   tag would make it certain and permanent); no clock to trust. U2 is refused (unsafe without a guard, the guard erases the saving); U3 is refused
   (no worst-case saving, one new field and a trusted number; the follow-up it contains is obtained without a field).
2. **Constants.** `providerUsageTTL` 20 s and `providerUsageFloor` 15 s unchanged, the backoff cap 5 min unchanged; `coddyUsageUnsupportedTTL` = 60 s
   (new, per subject; M = 12 ticks); follow-up delay = `providerUsageTTL`; shown age bound 40 s (6 ticks on the model); repoint exposure 20 s.
3. **Fields.** No account tag, no next-read hint. `windows[].reset_in_s` exists; `windows[].resets_at` leaves the wire (the client derives its own).
4. **Follow-up read** (P4b): adopted for turn-end reads of a `coddy` subject; no wire change; +1 GET per turn end at most, inside the floor (the cost
   maximum stays 4 at N=2).
5. **Memory**: 60 s, JSON answers only; a manual refresh bypasses it; the surfaces keep no memory of their own for a `coddy` row.

## 9. Recommended spec changes (exact replacement text; section numbers of `remote-model-provider-phase2.md`)

**4.1.** Append to step 4: "The route is served from the manager's per-account cache, so any number of clients, aliases and lender-side reads
together make at most one upstream read per `providerUsageFloor`, and a client-driven one only once the TTL has passed (model `p2-d2-usage-cache`,
P1 and P1b)." Replace the `windows[]` row by:
"| `windows[]` | only windows whose `id` matches `^(session\|week\|day\|acu\|window-[0-9]+)(-secondary)?$` (this drops the `codex:<i>:...` feature windows,
W9); `label` kept when it matches `^([0-9]{1,4}[smhd]\|week\|day\|ACU)$`, else the id; `used_percent` clamped to 0..100; `reset_in_s` = `ResetInSec` as
`ProviderUsage` delivers it, the remaining time relative to the moment the remote answers (age-corrected, 0 once passed, omitted when the window has no
reset clock); `exhausted`. **No absolute reset time is on the wire**: the two hosts' clocks differ and a client that schedules from the lender's
`resets_at` shows a lift late by the skew, or never (D2). The counters `used`, `limit`, `remaining` are dropped (they reveal the plan's size) |"
Add a row: "| account identity | none: no account tag, no next-read hint, no revision of the usage; two aliases of one account answer with equal numbers and nothing else
relates them (D2: U2 and U3 refused) |". Replace the DTO sentence by: "`WireUsage{Supported, AccountWide, Stale, Windows []WireUsageWindow, Blocked,
Blockers, RetryInS}`, `WireUsageWindow{ID, Label, UsedPercent, ResetInS, Exhausted}` ...; a reflection test fixes the JSON key set to the allowlist, so
neither `resets_at`, a tag nor a hint can slip in."  Also in section 2, W10, replace the last sentence by: "`windows[].reset_in_s` replaces the absolute
`resets_at` of the earlier plan on the wire; it leaks nothing and is skew-safe (D2, P6)."

**4.3** (replace the section):
"### 4.3 B.3 Mapping and the manager
`providerUsageSource` (`provider_usage.go:195`) gains `coddy`; `fetchProviderUsage` (`provider_usage_sources.go:37`) a `coddy` case calling
`CoddyUsageForProvider` and `mapCoddyUsage(w, provider, alias, fetchedAt)`: `Provider` = the row, `ProviderType: "coddy"`, `Model` = the alias (4.4), `FetchedAt` =
the receipt time on this host, windows mapped one to one with `ResetInSec = reset_in_s` and `ResetsAt = fetchedAt + reset_in_s` (the local clock: nothing absolute
is read from the wire), `Blocked`, `Blockers`, `RetryInSec = retry_in_s`, `RetryAt = fetchedAt + retry_in_s`, `Stale`; no plan, key name, wallet, rate; `!Supported` ->
`Unsupported: true`.
- **Unsupported memory.** A `Supported: false` answer, and only that (the 200 body, or the JSON `code: not_found` 404 of a phase-1 remote; an HTML or other 404
  is `unavailable` and takes the ordinary backoff), is remembered per subject for `coddyUsageUnsupportedTTL` = 60 s, not the five minutes of `usageUnsupportedTTL`:
  reads inside it answer it without a GET, a manual refresh (`refresh=1` asked by the user) ignores it, and the turn-end publishers (`publishProviderUsageAsync` `:698`,
  `publishProviderUsageOnReady` `:753`) stay silent for it. A lender that enables its reader is shown within the memory plus the time to the next read of that alias.
- **An alias that no longer exists.** `unknown_model` is `invalid` and drops the entry's numbers instead of keeping them stale (`usageRecordFailureLocked` treats
  `ProviderUsageInvalid` of a `coddy` row like a rejected key: an empty snapshot with the error); a transport failure keeps its stale numbers as today.
- **Follow-up read.** The remote answers from its own cache (TTL 20 s), so a read at a turn's end can return a snapshot older than the turn. When the first fetch of a
  `coddy` subject that follows a turn end (`publishProviderUsageAsync`) completes, the manager arms one deferred refresh at `fetchedAt + providerUsageTTL`
  (`usageArmPendingLocked`, announced as `refreshPending`/`refreshInSec` like any deferred refresh) unless one is already pending; a deferred fire, a backoff end or a
  follow-up does not arm another. Without it a quiet surface shows the numbers from before the last turn indefinitely; with it the turn's spend shows within
  `providerUsageTTL + providerUsageFloor` (model `p2-d2-usage-cache`, P4b)."

**4.4** (replace the closing paragraph "The remote's cache is per provider ..."):
"The remote's cache is per provider (the account); the local one is per subject `(provider, alias)` (**decided, D2: U1**): nothing the remote says about accounts keys an
entry, so nothing in the projection tells a client which aliases share one. An alias in use costs at most one GET per `providerUsageFloor` through the relay, all
served from the remote's single per-account cache, so a client with N aliases in use makes at most N x ceil(W / 15 s) requests in a window W (N = 2, W = 30 s: 4,
measured) and an alias nobody reads costs nothing. A repointed alias shows the old account's numbers for at most one `providerUsageTTL` (20 s) after the repoint;
numbers marked stale by a failed read stay until a read succeeds. A snapshot shown is built from a remote upstream read at most 40 s earlier. Entries are discarded by
the fingerprint rule (a rotated key, a renamed alias) and by `DropProviderUsage(provider)`."

**4.5** (one sentence, SPA bullet and `internal/remote/usage.go` bullet): replace "the unsupported marks ... are keyed by provider, or by provider and model for a provider
an answer reported as `providerType: "coddy"` (a learned set, ...)" by "the unsupported marks stay keyed by provider and are not recorded at all for a provider an
answer reported as `providerType: "coddy"` (a learned set); the manager's 60 s memory per alias is the only one, so memories do not add up".

**7-D2** (replace the whole block):
"### D2. The usage cache per alias, polling cost and coalescing: decided by `p2-d2-usage-cache.md`
**Decision.** U1: one local entry per `(provider, alias)`; no account tag (U2) and no next-read hint (U3). `windows[].reset_in_s` is on the wire and `resets_at` is not; the
client derives absolute times from its own clock. A turn-end read of a `coddy` subject arms one follow-up read after `providerUsageTTL`. `supported: false` is remembered for 60 s
per alias. **Evidence.** Upstream reads: at most one per floor for any number of clients and aliases (P1), a client-driven one only after the TTL (P1b, a route that forced
refresh violates it). Cost: at most one GET per floor per active alias (P2a), 4 in a 30 s window for two aliases; U2 saves 4 -> 3 (N = 2) and 6 -> 4 (N = 3), U3 saves
nothing in the worst case. Safety: a repointed alias shows the old account for at most the TTL (P3, tight); U2 without a guard shows it without bound (decoded: another alias
keeps the shared entry fresh), with a guard only for the revalidation period; an untrusted U3 hint breaks it unless clamped. Freshness: a snapshot shown is at most 6 ticks
(40 s) older than the remote's read (P4); a turn's spend shows within 30 s only with the follow-up (P4b, plain U1 unbounded). Unsupported memory: at most M, on a read surface M + P - 1 (P5).
`reset_in_s`: needed, a 60 s skew makes the lift never show without it (P6). **Depends:** 4.1, 4.3, 4.4, 4.5, S2, S3, S5, S7, S8; no new config key, no protocol bump."

**8** (stage notes): S2 owns "`coddy_wire.go (usage DTO: `WireUsageWindow{ID, Label, UsedPercent, ResetInS, Exhausted}`, no `resets_at`, no tag, no hint)`; `coddy_usage.go`
maps 404 `unknown_model` to `invalid` and a JSON `not_found` 404 to `Supported:false`, an HTML 404 to `unavailable`"; S3 gains "the 60 s `coddyUsageUnsupportedTTL`, the
follow-up arm after a turn-end fetch, the drop of numbers on `invalid` for a `coddy` row, tests for each (a fake clock; the follow-up fires once per turn end and does not
chain)"; S5 gains "the projection carries `reset_in_s` and no `resets_at`; the key-set reflection test"; S7 and S8 gain "no unsupported mark for a `coddy` row
(4.5); the countdown is computed from `resetInSec` as received". The file lists do not change; the last paragraph of section 8 gets: "D2 is decided (U1), see 7-D2."

## 10. Limitations

Hand-written model; latency below a tick; two accounts; one modelled client with an adversarial rest; the remote's upstream never fails; unauthorized stickiness and config swaps
are out. N = 3 cost for U1 and U3 was not completed (budget); U1's value follows from P2a. Layered surface memories and real clock-skew distributions are not modelled. The
`reset_in_s` result assumes the single-follow-up behaviour of the SPA and console (`USAGE_FOLLOW_UP_MS`). The engine's LTL counterexample panic (section 3) was avoided, not
fixed. Scaled constants: ticks of 5 s, M = 6 and 12 instead of 60; the results at two scales are linear in M.

## 11. Artifacts

`p2-d2-usage-cache.pml` (sha256 prefix 96721bc9), `p2-d2-usage-cache-skew.pml` (26c46724); the summaries above are the engine's own `counters` and `reason`; the
shell helpers and JSON traces are in the session scratch directory only.
