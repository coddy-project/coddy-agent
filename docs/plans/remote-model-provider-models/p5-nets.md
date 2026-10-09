# p5-nets: Petri nets for the resources of the relay and of the probe

Nets in the JSON format of `mcd` 0.3.1 (`mcd check --petri <net>.json --no-timing -ctl <formula>...`). A net holds no data (which credential, which token, which time): it is the place for the questions that are conservation and reachability of resources, and it re-derives, in the structure alone, what the Promela models `p5-registry`, `p3-d3-rate-windows` and `p4-probe` decide with data. Two kinds of claim are made and kept apart below. A **structural** formula (conservation of a token) holds by the way the net is built and can fail only if a mutant changes the structure: it is a guard on the design, not a discovery (`mcd` itself says so with its vacuity hints). A **reachability** formula (`AG EF`) says an outcome *can* be reached from every marking, not that it will be. A mutant that makes a place overflow its capacity is reported as `invalid-model`, shown here as **capacity**: the bound is the claim, and no formula was evaluated; the table says so each time.

Counts in the `states` column are those of the CTL searches; every one is exhaustive except a run marked *aborted*, where the search stopped on a capacity overflow and the count is where it stopped. The built-in `deadlock` verdict is in the text where it matters; the built-in `safe` (every place at most one token) is **violated by design** on the relay and busy nets, whose places hold two tokens, and is not claimed.

## p5-lease-net and p5-lease-unattended-net

Code: `external/swarm/registry.go` (`RegisterWithDial`, `Pin`, `Delete`, `reapLocked`). One safe place per state of the name (`free`, `liveA`, `offlineA`, `liveB`, `offlineB`, `pinned`) and a gate for the administrator, who asks (`adminAsk`) before any delete: every transition to `free` that is not the clock's reap consumes that request, so a property can tell whether a release has a request behind it. 8 places, 18 transitions.

- **L1** one state per name (structural): `AG (free + liveA + offlineA + liveB + offlineB + pinned == 1)`
- **L2** the name can be freed from every marking (reachability): `AG EF (free >= 1)`
- **L3, L3b** structural (guard on the design): a held lease of one node never passes to the other without being freed: `AG ((liveA + offlineA >= 1) -> AX (liveB + offlineB == 0))`, and the same the other way
- **L4** structural (guard on the design): a pinned lease stays pinned unless the administrator asked: `AG ((pinned >= 1 && adminReq == 0) -> AX (pinned >= 1))`
- **L5** a pinned lease can be released (reachability): `AG (pinned >= 1 -> EF (free >= 1))`
- **L6** structural (guard on the design): a live or pinned lease is never freed while no request is pending: `AG ((liveA + liveB + pinned >= 1 && adminReq == 0) -> AX (free == 0))`
- **L6b** nor while one is pending, unless it is consumed: `AG ((liveA + liveB + pinned >= 1 && adminReq >= 1) -> AX (free == 0 || adminIdle >= 1))`
- **L7** the administrator has one request at a time (structural): `AG (adminIdle + adminReq == 1)`

| variant | L1 | L2 | L3 | L3b | L4 | L5 | L6 | L6b | L7 | states |
|---|---|---|---|---|---|---|---|---|---|---|
| the net | ok | ok | ok | ok | ok | ok | ok | ok | ok | 12 |
| `steal` | ok | ok | ok | **VIOLATED** | ok | ok | ok | ok | ok | 12 |
| `nodelete` | ok | **VIOLATED** | ok | ok | ok | **VIOLATED** | ok | ok | ok | 12 |
| `reappinned` | ok | ok | ok | ok | **VIOLATED** | ok | **VIOLATED** | **VIOLATED** | ok | 12 |
| `reaplive` | ok | ok | ok | ok | ok | ok | **VIOLATED** | **VIOLATED** | ok | 12 |
| `reappinnedreq` | ok | ok | ok | ok | ok | ok | ok | **VIOLATED** | ok | 12 |
| `reaplivereq` | ok | ok | ok | ok | ok | ok | ok | **VIOLATED** | ok | 12 |

`steal`: a node takes the offline lease of the other without the name being freed (a stranger's registration applied). `nodelete`: the operator can no longer delete its pinned lease. `reappinned`: the clock reaps a pinned lease (the code drops `if l.pinned { continue }`). `reaplive`: the clock reaps a live lease. These two are the regressions the first version of this net could not see, because every state had a legitimate way out through an administrator's delete; the request gate makes them visible (L4, L6, L6b). `reappinnedreq` and `reaplivereq`: the same releases while a request is pending and without consuming it, which L4 and L6 let through and L6b catches (not very plausible regressions, since `reapLocked` never consults an administrator; kept because the claim "a release has a request behind it" is otherwise false).

**Unattended.** A second net has no revival, no renewal and no administrator: only the clock can free an offline lease. U1 is its conservation and U2 is `AG ((offlineA + offlineB >= 1) -> EF (free >= 1))`.

| variant | U1 | U2 | states |
|---|---|---|---|
| the net | ok | ok | 5 |
| `noreap` | ok | **VIOLATED** | 5 |

`noreap`: `reapA` and `reapB` removed (an offline lease is never dropped): U2 is violated and the built-in `deadlock` is reached. In `p5-lease-net` this mutant is invisible, since revival plus an administrator's delete is always a way out: that is why the second net exists.

## p5-relay-limits-net and p5-relay-limits-busy-net

Code: `external/swarm/limits.go` (`clientLimits.acquire`: the client's slot (`max_streams`) first, then a window token (`rate_per_minute`, `rate_burst`); a refusal holds nothing, and a window refusal releases the slot at once; `clientLease.refund`), `mount.go` (`lease.refund()` when the node answers 429 before any stream started; `defer lease.release()`), `internal/shareguard/limiter.go` (`Refund`: the next-allowed time moves back one interval and never before now), the node's `external/httpserver/shared_limiter.go` (the node sees the relay as one credential, so another client of the relay can hold its slot). Two ready calls of one client, one relay slot (`max_streams` = 1), a window of two tokens (`rate_burst` = 2), the node's slot that the client's call or another client holds. A busy answer from the node refunds the token (`nodeBusyRefund`); when the window has meanwhile refilled completely the refund changes nothing (`nodeBusyWindowFull`, enabled only with both tokens present). The clock returns one token per firing (`clockRefill`), as the continuous refill does. 9 places, 10 transitions.

- **R1** window tokens conserved (structural): `AG (wtok + wempty == 2)`
- **R2** calls conserved (structural): `AG (ready + hasSlot + forwarding + inService == 2)`
- **R3** the client's relay slot is held by one call or free (structural): `AG (rslotFree + hasSlot + forwarding + inService == 1)`
- **R4** the node's slot is held by this client's call, by another client, or free (structural): `AG (nodeFree + inService + otherHolds == 1)`
- **R5** the net can return to idle with the slot free (reachability): `AG EF (ready >= 2 && rslotFree >= 1)`
- **R6** the window can return to full (reachability, through the clock): `AG EF (wtok >= 2)`
- **R7** the refund is immediate: when the call is refused by a node that another client holds and the window is empty, the next step gives back the token: `AG ((forwarding >= 1 && wtok == 0 && otherHolds >= 1) -> EX (ready == 2 && rslotFree == 1 && wtok == 1 && otherHolds >= 1))` (the antecedent is reachable)

| variant | R1 | R2 | R3 | R4 | R5 | R6 | R7 | states |
|---|---|---|---|---|---|---|---|---|
| the net | ok | ok | ok | ok | ok | ok | ok | 21 |
| `norefill` | ok | ok | ok | ok | ok | **VIOLATED** | ok | 18 |
| `refundalways` | **capacity** | **capacity** | **capacity** | **capacity** | **capacity** | **capacity** | **capacity** | 14 (aborted) |
| `rateleak` | ok | ok | **VIOLATED** | ok | **VIOLATED** | ok | ok | 27 |
| `norefund` | ok | ok | ok | ok | ok | ok | **VIOLATED** | 21 |

`norefill`: no clock; R6 is violated (the window never returns to full). `refundalways`: a refund that creates a token from nothing: the window grows past its size, **capacity** (the bound is the claim here: raising the capacity of `wtok` to 3 still overflows, since the false refund repeats without limit, so no formula would decide it). `rateleak`: the window refusal does not give the slot back (the code drops `le.release()` at `limits.go:97`): R3 and R5 are violated. `norefund`: a busy answer does not refund the token. R1 to R6 cannot see it (all verified, 21 states), since the clock returns every spent token sooner or later; **R7 sees it**, because it looks at the very next step. The second net checks the same without the clock, as a state formula.

**Busy.** The node is held by another client for good (`otherHolds` marked, never released) and no time passes (no clock), so no call is ever admitted and a token can only be lost by a busy answer. B1, B2 and B3 are conservation (`AG (wtok + wempty == 2)`, `AG (ready + hasSlot + forwarding == 2)`, `AG (rslotFree + hasSlot + forwarding == 1)`) and B4 is `AG ((hasSlot + forwarding == 0) -> wtok >= 2)`: whenever no call is in flight, the window is full.

| variant | B1 | B2 | B3 | B4 | states |
|---|---|---|---|---|---|
| the net | ok | ok | ok | ok | 3 |
| `norefund` | ok | ok | ok | **VIOLATED** | 8 (aborted) |
| `tokenfirst` | ok | ok | ok | **VIOLATED** | 7 (aborted) |

`norefund`: the busy answer returns the slot and keeps the token: B4 is violated (a client that waits out the node's busy drains its window), which is deleting `lease.refund()` at `mount.go:302`. `tokenfirst`: the order of `acquire` is the slot first and then the token, so a call refused for the slot spends no token (`limits.go`); the mutant spends one in the refusal: B4 is violated.

## p5-probe-slot-net

Code: `external/httpserver/shared_probe.go`, `shared_models_http.go` (`runSharedCall`). One call, one slot, the guard unarmed until the first ping. The call ends by completing, by the guard's cut (`cutCancelling`), or by any other cancel (`cancellingOther`: the client's disconnect, a failed write, the stall guard, the bound of a blocking call), and the provider returns in each case and the slot is released once. 10 places, 8 transitions.

- **S1** one slot, four states of the call (structural): `AG (slotFree + running + cutCancelling + cancellingOther == 1)`
- **S2** structural (only `armatstart` can break it): a cut only after an accepted ping: `AG (cutFlag >= 1 -> pingedFlag >= 1)`
- **S3** `cutCancelling` only after a cut (structural): `AG (cutCancelling >= 1 -> cutFlag >= 1)`
- **S4** every call can reach its end (reachability; the single release is S1): `AG EF (done >= 1)`
- **S5** a finished call is in no running state (structural): `AG (done >= 1 -> (running + cutCancelling + cancellingOther == 0))`
- **S6** structural, S2 and S3 together: a guard cancel only after a ping: `AG (cutCancelling >= 1 -> pingedFlag >= 1)`

| variant | S1 | S2 | S3 | S4 | S5 | S6 | states |
|---|---|---|---|---|---|---|---|
| the net | ok | ok | ok | ok | ok | ok | 9 |
| `armatstart` | ok | **VIOLATED** | ok | ok | ok | **VIOLATED** | 6 |
| `cutnoconsume` | **VIOLATED** | ok | ok | ok | ok | ok | 10 (aborted) |
| `doublerelease` | **VIOLATED** | ok | ok | ok | ok | ok | 9 (aborted) |
| `leakcut` | **VIOLATED** | ok | ok | ok | ok | ok | 9 |
| `leakcomplete` | **VIOLATED** | ok | ok | ok | ok | ok | 11 |
| `noreturn` | ok | ok | ok | **VIOLATED** | ok | ok | 8 |
| `leakother` | **VIOLATED** | ok | ok | ok | ok | ok | 11 |
| `doubleother` | **VIOLATED** | ok | ok | ok | ok | ok | 11 (aborted) |

`armatstart`: the guard is armed with no ping (`guardOn` marked at the start, no `pingedFlag`): S2 and S6 are violated (the cut comes with no ping; the first version of this mutant also marked `pingedFlag`, which made S2 true by construction). `cutnoconsume`: the cut keeps the call running and releases the slot (`slotFree` has capacity 2 here so that the formula, not the bound, decides): S1 is violated. `doublerelease`: the provider's return after a cut releases twice (the guarded `defer slot.release()` gone): S1 is violated. `leakcut` and `leakcomplete`: a path ends the call without releasing the slot: S1 is violated. `noreturn`: the provider never returns after a cut: S4 is violated. `leakother` and `doubleother`: the return after any other cancel (a disconnect, a failed write, the stall guard, the bound of a blocking call) leaks the slot or releases it twice: S1 is violated. The built-in `deadlock` of the base net is its terminal marking `done`: it is a one-shot, and S4 is its liveness claim.

## What the nets do not say

No data: which credential, which token value, which time; the relay's window is two tokens refilled one at a time by the clock, which is `p3-d3-rate-windows`' question; the probe's grace is `p4-probe`; the node's own rate window as a cause of busy is abstracted into the node's slot being held (a busy answer is one transition whichever the cause); the relay's and the node's releases of their slots happen together at the end of a call (the code releases them independently, both at the end); one call at a time per client (`max_streams` = 1), so exactly-once release across two competing calls of one client is not exercised; which of the two exits of a call that is both cut and disconnected is recorded (`probe.fired()` is tested before the request context, so a disconnect followed by a cut counts as a probe cut: outcome attribution, not a slot question; the net's exits are exclusive, and a net that lets them overlap keeps S1 to S6 verified because the release is one guarded step); the lease net knows no secret or identity (that is `p5-registry`), only who may free a lease; a tunnel lease's dependence on its connection is not in the net (the end-to-end tests of `external/swarm` hold it).

## Review

**Round 1** (three reviewers, every claim re-run by the orchestrator). Verdict: approve with changes; all numbers of the first version reproduced, and what was wrong was what the nets could detect. Changed: (1) the refund was untested, since the clock returns every token: the busy net, with `norefund`; (2) `nodeBusyWindowRefilled` was enabled right after a token was taken and so lost it without any refill: its guard is now both tokens present; (3) the rate refusal path was missing, which made the old `norefill` deadlock an artefact: `rateRefuse` and `rateleak`, and R5 now also asks for the slot free; (4) the node never contended, which made R4 trivial: `otherHolds`; (5) the lease net could not tell who frees a lease: the administrator's request, L4 and L6, `reappinned`, `reaplive`, and the unattended net for `noreap`; (6) `invalid-model` rows hid which formula fails and their counts were of the wrong search: the formula-detected mutants above, the capacity rows labelled, the counts of the CTL searches; (7) `cancelling` meant only the guard's cut: `cancellingOther`; (8) wording: structural invariants and reachability claims are told apart, the refill is one token per firing, and the claim that the nets "carry what must stay true whatever the data is" is gone. Rejected with evidence: that L4's antecedent is unreachable (`EF (pinned >= 1)` holds), that the nets have 14 transitions (16 in the first version), that the refund can strand a slot.

**Round 2** (three reviewers, one of them on a retry; all 21 nets of the fixed version re-run and every verdict and count reproduced). Verdict: approve with changes; one verdict changed. Changed: the sentence that no property of the clock net can see the refund was false: R7, a one-step formula, is violated by `norefund` (the busy net stays as the isolated check, without the clock); the claim that a property can tell the actor of a release was too strong, since a release while a request is pending and not consumed passed L1 to L7: L6b and two conditional mutants; the exit of a call after any other cancel had no leak mutant (`leakother`, `doubleother`); the order of `acquire` had no mutant (`tokenfirst`); the structural formulas are all labelled structural, S4 no longer claims the single release, B1 to B3 are written out, `refundalways` is marked aborted with the reason the bound is its claim, and the built-in `safe` is said to be violated by design. Rejected with evidence: that `steal` is credited with L3 (the table has it violating L3b), that no property distinguishes an immediate refund from the clock's (R7 does), and that `operatorSeed` is unfaithful to the code (`seedUpstreams` runs once on an empty registry).
