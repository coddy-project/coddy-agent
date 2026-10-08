# p2-d4-busy-notice: the end of a `remote_busy` notice next to a real usage snapshot (phase 2, dilemma D4)

Spec: `docs/plans/remote-model-provider-phase2.md` sections 2 (W4), 4.5, 4.6, 7 (D4), 8. Model: `p2-d4-busy-notice.pml`
(this directory; header comment lists every switch). Engine: `mcd 0.2.0` CLI (`mcd-report/1`). The CLI has no `mc_simulate`
and no `mc_lint_property`: the pilot walk was skipped (sanity is the `reach` properties r1..r6 and the deadlock check),
properties were classified by hand (`properties-ltl-ctl.md`). Nothing in this report is a statement about the real code
except through the mapping table; the plan's code was read, not run.

## 1. Intake card

- **Boundary.** One session, one subject (`coddy` provider row + alias), one surface. Inside: the agent's `busyWaitNotice`
  (report/clear, three clear sites), the manager's usage cache (versions, `fetchedAt`), the two delivery paths, the surface's
  state machine. Outside: the remote, the HTTP call itself, other rows, the 5-minute unsupported memory, D2 polling, the
  limit-wait notice (`limit_wait.go`, same `resuming` flag, other blockers, not touched).
- **State.** `clk`; cache `(cacheV, cacheF)`; turn stream (queue 0, FIFO unless REORD); side channel (events stream and REST
  answers, a snapshot in flight = its version); call phase and deadline; surface: SURF 0 one slot `(ck, cv, cs)`, SURF 1 two
  slots `(sv, ss)` + `(bon, bs, bend, bexp)`; ghosts `maxdel, maxshown, regress, bdelcur`.
- **Atomic step.** One frame applied by the surface (a React state reducer / the console's UI goroutine applies one update at
  a time); one event of the agent; one fetch completion.
- **Class.** Finite, untimed, with an explicit discrete-event clock abstraction (declared; not a timed automaton). Acyclic: every
  maximal run ends, so `<>[] p` is a statement about final states; no fairness is needed (a `--fairness weak` run gives the same
  LTL verdicts, section 4).
- **Assumptions (all visible switches).** A1 stamps are RFC 3339 seconds, so stamps of different processes may tie; A2 the agent's
  consecutive events are at least one tick apart (`busySleep` floor 1 s), except TIEEND; A3 real fetches are strictly ordered
  (15 s floor); A4 the turn stream is FIFO (SSE) unless REORD; A5 the side channel is unordered; A6 any turn-stream frame may be
  lost (LOSS: relay `desynced` subscriber, closed stream after a cancel); A7 the relay may replay frames (REPLAY: `serveSubscriberAfter`);
  A8 the end of the turn is reliably signalled to the surface (`turnEpoch`, `turn_ended`); A9 the ticker never passes a call's deadline
  while it waits. **Bounds.** NW<=2 calls, NV<=2 versions, MAXB<=2 notices, 5..8 ticks (scales A, W, W2, T in the header). Result
  carries over to larger bounds only by the argument that the rules compare stamps, never counts; not proved here.

Mapping (model -> code): `K_BUSY` = `Resuming:true, Blocked:true, Blockers:[remote_busy]` (`coddy_provider.go:128-140`); clear `CLEAR=0`
= today's `Unsupported:true`; `K_SNAP` = a usage update with `fetchedAt`; `ck/cs` = `usage` state of `useProviderUsage.ts:61`, `usageIsNewer`
= `providerUsage.ts:486`; `CLEAR=1/2` = N1; `EXEMPT` = N4's rule; `SURF 1` = N3, console = `a.remoteBusy` beside `footer.usages`;
`NETEND` = `turnEpoch` (SPA) / `turnDone` (`updates.go:48`); `ARRIVAL` = `footer.SetUsage` (no `fetchedAt` rule).

## 2. Properties

| id | text (LTL over globals) | class / logic | meaning |
|---|---|---|---|
| L1 | `<>[] (bshown == 0)` | liveness, LTL (final state) | after the call ends the countdown is eventually gone, for ADMIT and FAIL alike |
| PR | `[]((wph != 1 && qn[0] == 0) -> bshown == 0)` | safety, LTL | gone as soon as the call left waiting and its frames arrived (promptness; cannot hold after a lost frame) |
| S1 | `[]((maxdel > 0 && bshown == 0) -> sview == maxdel)` | safety | whenever no countdown is shown, the newest delivered real snapshot is the one shown (never lost) |
| S1b | `[]((maxdel > 0) -> sview == maxdel)` | safety | same also during a countdown (informational: popover keeps the numbers) |
| S2 | `[](regress == 0)` | safety | an older snapshot never replaces a newer one (needs NV=2: scale W) |
| S3 | `[]((wph == 1 && bdelcur == 1) -> bshown == 1)` | safety | no flicker: once a notice of the current wait arrived, the countdown stays until the call leaves waiting |
| S1e | `<>[](sview == maxdel)` | liveness | at the end the surface shows the newest delivered snapshot |
| r1..r6 | `EF` of: countdown shown; countdown shown with a snapshot delivered; clean end with snapshot; snapshot in flight during a wait; `regress`; countdown still shown after the turn ended | reachability, CTL | sanity (non-vacuity). r4/r5/r6 are *expected* false in a good design (r5 needs NV=2, r6 is the stuck state) |
| deadlock, assert | model's own | safety | the model never gets stuck (clock horizon handled), queues never overflow |

Logic choice: all requirements are linear (universal) -> LTL; reachability by CTL `EF`. S2/S3/S1 are `[]` invariants of the stored state,
checked as LTL (CLI has no `invariant` kind); L1/S1e use `<>[]` (no CTL equivalent).

## 3. Results

Verdict codes are the engine's: `ok` = `verified`/`exhaustive`, `VIOL` = `violated`/`exhaustive` (decoded counterexample exists), `incon` =
`inconclusive`. Every `ok` has `complete=true`. Base config = no loss, FIFO, no replay. `.` = not asked in that run. Names are
`out` files of the scratch run (the `-D` set is in section 5).

**3.1 Base config, scale A (NV=1: snapshot v1; clear races with its delivery) and scale W (warm cache, NV=2: older/newer).**

| variant (plan option) | A: L1 PR S1 S1b S3 S1e | W: L1 PR S1 S1b S2 S3 S1e | states A / W |
|---|---|---|---|
| N2: `Unsupported` clear, one slot (phase 1) | ok ok VIOL VIOL VIOL ok | ok ok VIOL VIOL VIOL VIOL ok | 193780 / 231338 |
| N1 as is: cached snapshot, own `fetchedAt`, no exemption | VIOL VIOL VIOL VIOL VIOL VIOL | VIOL VIOL ok VIOL ok VIOL VIOL | 195136 / 195552 |
| N1 re-stamped | ok ok VIOL VIOL VIOL ok | ok ok VIOL VIOL VIOL VIOL ok | 194316 / 193644 |
| **N1 + N4 (plan default)**: cached snapshot + non-busy supersedes busy | ok ok VIOL VIOL VIOL ok | ok ok VIOL VIOL VIOL VIOL ok | 193132 / 174364 |
| N4: end marker, one slot, supersede rule | ok ok VIOL VIOL VIOL ok | ok ok VIOL VIOL VIOL VIOL ok | 191207 / 197564 |
| N3 + `Unsupported` clear (split slots) | ok ok VIOL VIOL ok ok | . | 213824 / . |
| N3 + snapshots end the countdown (supersede rule in split) | ok ok ok . VIOL ok | . | 548951 / . |
| N3 + end marker, no nets | ok ok ok ok ok ok | . | 219719 / . |
| N3 + no wire clear, nets only | ok VIOL ok ok ok ok | . | 390326 / . |
| **N3 + end marker + nets (NETEXP, NETEND) = decision** | ok ok ok ok ok ok | ok ok ok ok ok ok ok | 521960 / 388488 |
| console model: split + end marker + `NETEND` + arrival order (`ARRIVAL`) | . | ok ok VIOL VIOL VIOL ok VIOL | . / 232319 |
| the same console model with no wait at all (NW=0) | . | S1 S1b S2 S1e: all VIOL | 30003 |

Reachability (sanity): r1, r2, r3, r4 hold in every row, so the interesting states exist (countdown with a delivered snapshot, a snapshot in flight during a wait, a clean end). r5 (older after newer) holds
in the W rows N2, N1 re-stamped, N1+N4, N4 and console, and not in N3 (W): the same model and property fail in the rejected designs, so S2 is not vacuous. r6 (countdown still shown after the turn ended) holds in
every row without nets and not in N3 with nets.

**3.2 Stress configs, scale A, properties L1 PR S1 S3 S1e (`core`).** `LOSS` = any turn-stream frame may be dropped; `REPLAY=1` = one frame
delivered a second time later.

| config / variant | L1 | PR | S1 | S3 | S1e | note |
|---|---|---|---|---|---|---|
| LOSS, N3 end marker, no nets | VIOL | VIOL | ok | ok | ok | lost end: countdown shown forever |
| LOSS, N3 + `NETEXP` only | ok | VIOL | ok | ok | ok | r6 reachable: countdown outlives the turn until its deadline |
| LOSS, N3 + `NETEND` only | ok | VIOL | ok | ok | ok | r6 unreachable |
| LOSS, N3 + both nets | ok | VIOL | ok | ok | ok | r6 unreachable. PR cannot hold after a lost frame by construction |
| LOSS, N3, no wire clear, both nets | ok | VIOL | ok | ok | ok | eventual, not prompt |
| LOSS, N2 / N4 (one slot) | VIOL | VIOL | VIOL | VIOL | VIOL / ok | even `eventually gone` fails without nets |
| REPLAY, N3 + both nets, strict tombstone | ok | ok | ok | ok | ok | 1664056 states |
| REPLAY, N3, **no tombstone** (mutant TOMB=0) | ok | VIOL | ok | ok | ok | a replayed notice revives the countdown until the net removes it |
| REPLAY, N2 and N4 (one slot) | VIOL | VIOL | VIOL | VIOL | VIOL / ok | the revived countdown stays for good |
| tie of stamps (TIEEND), scale T, REPLAY: strict (`>`) tombstone | ok | ok | ok | ok | ok | end wins a tie |
| same, non-strict (`>=`) tombstone (mutant TOMB=2) | VIOL | VIOL | ok | ok | ok | a tied replayed notice revives it even after `turn_ended` |
| no clear on the FAIL path (mutant CLEARFAIL=0), both nets | ok | VIOL | ok | ok | ok | with `NETEND` only: same; nets save L1, not promptness |
| end stamped with the cache's `fetchedAt` (mutant RESTRICT=0) | ok | VIOL | ok | ok | ok | an end older than the countdown is ignored |
| console as it is (`ARRIVAL=1`, no tombstone), REPLAY + tie, scale T | VIOL | VIOL | ok | ok | ok | a replayed or tied notice revives the status row |
| console as recommended (`ARRIVAL=2`: snapshots by arrival, countdown by stamp + tombstone, `NETEND`), REPLAY + tie, scale T | ok | ok | ok | ok | ok | 145026 states |
| `--fairness weak`, N3 + both nets | ok | ok | ok | ok | ok | same as `none`: fairness irrelevant (acyclic); the CTL columns of that run read `x` because fairness is not wired into CTL and are not used |

**3.3 Two waits in one turn (scale W2, NW=2, N3 + end marker + `NETEND`, strict tombstone), `core`.** The end of wait 1 must not touch wait 2 and a stale copy of wait 1 must not revive.

| config | L1 | PR | S1 | S3 | S1e | states |
|---|---|---|---|---|---|---|
| `REPLAY=1` | ok | ok | ok | ok | ok | 1373772 |
| `REORD=1` (the turn stream may deliver in any order) | ok | ok | ok | ok | ok | 1024230 |
| `REORD=1 TOMB=0` (mutant, no tombstone), L1 PR S3 only | VIOL | VIOL | . | ok | . | 1000806 |

## 4. Counterexamples, decoded and classified

(`n` = step index in the `mcd` report; every trace is a run of the model; "classification" per `counterexamples.md`.)

1. **N2, S1/S3 (A).** Countdown stamps 1,2 shown (`ck=2`); a real snapshot v1 published during the wait arrives with a stamp >= the last notice's:
   `usageIsNewer` accepts it, the countdown disappears while the call still waits (S3); after the end, `Unsupported` sets `ck=0`, v1 is gone
   though delivered (S1). *System defect* (W4 of the plan): the one-slot state and the `Unsupported` clear.
2. **N1 as is, L1/PR (A).** The clear carries the cached snapshot with its own stamp 1 < countdown stamp 2: rejected by `usageIsNewer`; the
   post-turn REST read (stamp 1) is rejected too; the countdown is shown for good. *System/spec defect* of N1 without an exemption.
3. **N1 + N4, S1 (A).** The cache was empty when the clear was built, so it fell back to `Unsupported`; the fetch finished right after, its
   snapshot overtook the clear on the side channel, the clear then wiped it. *Spec defect* (the fallback), recovered at the next read (S1e ok).
4. **N1 + N4, S2 (W).** v2 shown, the countdown shadows it (single slot forgets v2), the session-open REST answer v1 arrives during the wait and,
   by the exemption "non-busy supersedes busy whatever its `fetchedAt`", replaces the countdown: v1 is shown after v2. *System defect of the
   exemption on a one-slot state*; a flicker too (S3).
5. **N1 re-stamped, S2 (W).** The re-stamped copy of v1 carries the clock of the clear, which ties or outranks the genuine stamp of v2 fetched meanwhile: v1 is shown after v2 (the
   same re-stamp is why S1 fails in A). *Spec defect*: it misstates the read time.
6. **N4 marker only, one slot, S1.** The marker ends the countdown, but the snapshot it replaced is gone. *Spec defect*: N4 alone does not
   answer the question without a second slot.
7. **N3 + snapshots end the countdown, S3.** A snapshot of the same subject (another session's turn end, a poll) ends a wait still in progress.
   *Spec defect* of the supersede rule: a snapshot proves nothing about the end of a wait.
8. **N2/N4 + REPLAY, L1.** A copy of a notice replayed after the end finds an empty slot and is accepted: stuck. *System defect* (no tombstone).
9. **N3 no nets + LOSS, L1.** The end frame is lost; nothing else ends the countdown. *Environment fault the design must survive* (not an artefact:
   `composer_stream_relay.go` marks a slow subscriber `desynced` and drops frames).
10. **Tie, TOMB=2.** A notice and the end share one RFC 3339 second; the non-strict comparison accepts a replayed notice. *Mutant*: the rule must be strict.
11. **Console model, S1/S2 (with and without a wait).** `footer.SetUsage` applies snapshots in arrival order: an older REST answer overwrites a newer
    push. *Pre-existing system defect, independent of D4* (reproduced with NW=0); not part of this decision, recorded in the risks.

## 5. Commands (reproduce)

Property arguments (`core` = L1, PR, S1, S3, S1e + r1, r3, r6; `full` = all; `order` = S1, S1b, S2, S1e + r5):
```
P='--ltl "<>[] (bshown == 0)" --ltl "[]((wph != 1 && qn[0] == 0) -> bshown == 0)" --ltl "[]((maxdel > 0 && bshown == 0) -> sview == maxdel)"
   --ltl "[]((maxdel > 0) -> sview == maxdel)" --ltl "[](regress == 0)" --ltl "[]((wph == 1 && bdelcur == 1) -> bshown == 1)" --ltl "<>[](sview == maxdel)"
   --ctl "EF (bshown == 1)" --ctl "EF (wph == 1 && bdelcur == 1 && maxdel > 0 && bshown == 1)" --ctl "EF (agdone == 1 && bshown == 0 && maxdel > 0)"
   --ctl "EF (wph == 1 && bdelcur == 1 && scn > 0)" --ctl "EF (regress == 1)" --ctl "EF (turnended == 1 && bshown == 1)"'
A="-D NW=1 -D NV=1 -D NR=1 -D MAXB=2 -D BUDGET=2 -D TMAX=6"      W="-D NW=1 -D NV=2 -D NR=1 -D MAXB=1 -D BUDGET=1 -D TMAX=5 -D WARM=1"
mcd check --promela p2-d4-busy-notice.pml $A -D SURF=1 -D CLEAR=3 -D NETEXP=1 -D NETEND=1 $P --no-timing   # decision, scale A
mcd check --promela p2-d4-busy-notice.pml $W -D SURF=1 -D CLEAR=3 -D NETEXP=1 -D NETEND=1 $P --no-timing   # decision, scale W
```
Variant -D sets (append to `$A` or `$W`; defaults are 0): N2 `SURF=0 CLEAR=0`; N1 as is `CLEAR=1`; N1 re-stamped `CLEAR=2`; N1+N4 `CLEAR=1 EXEMPT=1`;
N4 `CLEAR=3 EXEMPT=1`; N3+Unsupported `SURF=1 CLEAR=0`; N3+supersede `SURF=1 CLEAR=3 EXEMPT=1 NETEXP=1 NETEND=1`; N3 no nets `SURF=1 CLEAR=3`;
N3 no wire clear `SURF=1 CLEAR=4 NETEXP=1 NETEND=1`; console `SURF=1 CLEAR=3 NETEND=1 ARRIVAL=1`. Stress: add `LOSS=1`, `REPLAY=1`, `REPLAY=1 TOMB=0`,
`CLEARFAIL=0`, `RESTRICT=0`; tie runs use scale T (`NW=1 NV=1 NR=0 MAXB=1 BUDGET=1 TMAX=5`) with `REPLAY=1 TIEEND=1 TOMB=1|2 SURF=1 CLEAR=3 NETEND=1`;
fairness: add `--fairness weak`. Two waits: `NW=2 NV=1 NR=0 MAXB=1 BUDGET=1 TMAX=8 SURF=1 CLEAR=3 NETEND=1` with `REPLAY=1` or `REORD=1`.
Model sha256 of the runs differs from the final file only by the header comment, `TIEEND`/`TOMB=2` (added after the A/W runs, default off) and `WARM` defaults.

## 6. Statements (CONFIRMED / CONTRADICTED / NOT DECIDABLE)

- **CONTRADICTED** (plan 4.6 default, "N1 with the supersede rule of N4"): `fetchedAt` order plus an exemption on a one-slot state loses or misorders snapshots
  (S1, S2) and flickers (S3); the fallback to `Unsupported` is a wipe race. Counterexamples 3, 4.
- **CONTRADICTED** (W4 consequence "the clear is `Unsupported`"; N2): wipes the snapshot (1). Plan sentence "at most one turn" is right for recovery (S1e ok) but not for
  "the latest snapshot is never lost" (S1).
- **CONTRADICTED** (N1 as is: "re-sends it as it is"): never clears (2). N1 re-stamped misstates the read time and misorders (5).
- **CONFIRMED** (on the model, exhaustive, scales A/W/T/W2 as listed): with two slots, an end marker that carries no data, a strict stamped tombstone and the two local nets, L1, PR, S1, S1b, S2, S3, S1e hold
  in the base config and under REPLAY; L1, S1, S3, S1e under LOSS; the same with `--fairness weak`; no deadlock.
- **CONFIRMED**: the clear on the FAIL/cancel path is needed for promptness (CLEARFAIL=0), and a stamp taken from the clock, not the cache, is needed (RESTRICT=0).
- **NOT DECIDABLE here**: wall-clock bounds (the 20 s heartbeat, the deadline in seconds, clock skew between browser and server: the nets use receipt-relative
  time by design, the model only has one clock); React rendering order inside one batch; several tabs/sessions on one subject (modelled as one surface); the
  `resuming` notices of `limit_wait.go` for a `coddy` row (same surface, other blocker: they keep today's rule, replaced by the next turn-end read; only
  `Model` must be set on them); scale beyond the bounds of section 1; real code conformance.

## 7. Decision

**Hybrid: N3 (the countdown is state of its own on every surface) + N4's explicit end carried by the fields that exist, + two local nets.
N1 (as is and re-stamped) is rejected, N2 is rejected, and the ordering exemption of N4 is rejected.** Evidence: section 3.1 is the matrix: every one-slot
variant violates S1 or S3 (and S2 on the warm scale), N1 as is violates L1, N3 with an `Unsupported` clear violates S1, N3 with the supersede rule violates S3,
and exactly N3 + end marker + tombstone + nets violates nothing, also under LOSS (nets), REPLAY (tombstone) and ties (strict `>`).

- **Fields on `ProviderUsageUpdate`: none new.** The family of the countdown is named by the blocker `remote_busy` (not by `Blocked`/`Resuming`): `Resuming: true` + `Blocked: true`
  = renew; `Resuming` absent + `Blocked: false` + same blocker = end; both with `Provider`, `ProviderType: "coddy"`, `Model` = alias, `FetchedAt` = clock at sending.
  The agent no longer needs the manager's cache: `CachedProviderUsage` and its interface are dropped (S3 shrinks).
- **Who owns the busy state.** SPA: `useProviderUsage` (a `busy` state and a tombstone ref beside `usage`; `applyPushed` routes by the blocker; `UsageBanner` takes `busy` as its own prop).
  Console and remote console: `App.remoteBusy` (already apart from `footer.usages`) plus a tombstone string; nothing in the manager, nothing in the usage cache or its schedule.
  The popover and the footer show the real snapshot during a wait.
- **Rules** are in the replacement text of 4.6 below; the model covers them as follows (not modelled, a design addition: the drop of slot and tombstone at a change of subject, the 2 s grace of the expiry (the model has 1 tick),
  and a stamped tombstone on the console, whose model row uses arrival order for both slots and holds only in the base config): the model is the proof for them: accept a notice iff `stamp > tombstone && stamp >= slot.stamp`; accept an end iff `stamp >= slot.stamp`
  (then clear the slot, raise the tombstone); a snapshot never touches the slot; nets: turn end and receipt-relative expiry.
- **Net choice.** `NETEND` alone gives L1 and keeps the countdown from outliving the turn; `NETEXP` alone gives L1 but the countdown outlives the turn (r6); the plan keeps both (expiry bounds a lost end
  during a turn that goes on streaming).

## 8. Recommended spec changes (exact text, `docs/plans/remote-model-provider-phase2.md`)

**Section 2, row W4, last two cells (replace):**
`| `busyWaitNotice.clear` sends `Unsupported: true` (`internal/agent/coddy_provider.go:148-163`). Once a `coddy` row has usage, the SPA (`useProviderUsage.ts:162-175`) and the console (`external/cli/usage.go:641-660`) take that as "drop this row's usage", wiping the real snapshot; a clear that re-sends the cached snapshot loses to the busy update on `fetchedAt` order (`providerUsage.ts:486-496`); and a rule that lets a non-busy update supersede a busy one lets an old REST answer replace a newer snapshot (`p2-d4-busy-notice.md`) | The countdown is surface state of its own, ended by an explicit end update, never by a usage answer: B.6 (decided) |`

**Section 4.5, SPA bullet: append; console bullet: append:**
- SPA, append: `The countdown is not part of this state: `useProviderUsage` keeps a `busy` slot and a tombstone beside `usage` (B.6), `applyPushed` routes an update whose `blockers` contain `remote_busy` to it and every other update to the snapshot path, and `UsageBanner` takes the slot as its own prop; the usage popover keeps showing the real snapshot during a wait.`
- Console, append: `applyProviderUsage` routes the same way: `a.remoteBusy` (apart from `footer.usages` already) gets the end branch and a tombstone (B.6); the `Unsupported` branch no longer ends a wait.`

**Section 4.6 (replace whole):**

> ### 4.6 B.6 The busy countdown next to a real snapshot **[D4, decided]**
>
> Decided by `docs/plans/remote-model-provider-models/p2-d4-busy-notice.md` (exhaustive runs of the model): the countdown is state of its own on every surface (N3), ended by an explicit end update that
> reuses the countdown's own fields (N4's end, without N4's ordering exemption), with a strict tombstone and two local safety nets. N1 (as is and re-stamped) and N2 are rejected.
>
> **Wire, no new field.** An update whose `blockers` contain `remote_busy` belongs to the countdown of the call that sent it, never to the usage snapshot; every other update is a snapshot.
> `Resuming: true` with `Blocked: true`, `RetryAt`, `RetryInSec` and `FetchedAt` = now renews the countdown (as today). The same update with `Resuming` absent, `Blocked: false`, no retry fields and
> `FetchedAt` = the clock at the moment of sending is its **end**: it carries no usage data and no `Unsupported`. Both set `Provider`, `ProviderType: "coddy"` and `Model` = the alias (from
> `llmTransport.model`, the subject of 4.4), so one alias's countdown never shows or ends another's. `FetchedAt` is an RFC 3339 UTC string and is compared as a string.
>
> **Agent.** `busyWaitNotice.clear` sends that end and nothing else, at the three sites it has today (the admission report, the first chunk, after `Stream` returns), so an error and a cancel end the
> countdown as well. It reads no cache: `Manager.CachedProviderUsage` and the interface on the session state are dropped. The stamp is the clock, never the cache's `fetchedAt`.
>
> **Surfaces (SPA hook, console, remote console).** The snapshot path is B.5's and no countdown update touches it; no snapshot, and no `Unsupported` read answer, touches the countdown. The countdown slot
> holds the last accepted countdown update and a tombstone (the largest `FetchedAt` of an end or of a dropped countdown): a countdown update is accepted iff its `FetchedAt` is greater than the tombstone
> and not older than the slot's; an end is accepted iff it is not older than the slot's, and then empties the slot and raises the tombstone. An end wins a tie; an old copy of a countdown replayed after
> the end (relay replay, mirror, reconnect) is ignored. Two local nets: the slot is dropped, with the tombstone raised to its stamp, when the viewed session's turn ends (SPA: the `turnEpoch` of
> `useProviderUsage`; console: `turnDone`, already there) and when `RetryInSec` after receipt plus 2 s has passed on the surface's own clock (never against the server's absolute `RetryAt`). A change
> of subject drops slot and tombstone.

**Section 7, first paragraph, the parenthesis (replace):** `(D1: R1, D2: U1, D3: O1 with T1; D4 is decided, see 4.6)`; the `**Depends**` sentence of D4 is replaced by `Depends: 4.6, S3 (the end update only), S7, S8, the agent file.`

**Section 7, D4 (replace whole):**

> ### D4. The busy countdown and the real usage snapshot on one surface **[decided]**
>
> **Question.** How does the end of a `remote_busy` notice avoid wiping, or being shadowed by, the usage snapshot of the same row?
> **Decision.** N3 with the explicit end of N4 on the existing fields, a strict stamped tombstone, and the two nets of 4.6 (`p2-d4-busy-notice.md`).
> **Evidence (exhaustive on the model; scales A, W, W2, T).** One-slot surfaces fail one of: the latest snapshot is kept (N2: wiped; N1 and N4: wipe race and loss), no flicker while the call waits (a snapshot with a later
> `fetchedAt` replaces the countdown; the supersede rule ends it), an older snapshot never replaces a newer (the supersede rule, the re-stamp); N1 as is never clears the countdown (`fetchedAt` order);
> a split surface with an `Unsupported` clear wipes the snapshot, and with the supersede rule it flickers. Split slots + end update + tombstone satisfy all properties; without the tombstone a replayed
> notice revives the countdown, without the nets a lost end leaves it forever, and a non-strict comparison fails on a tie of two RFC 3339 seconds.
> **Side result.** The console's `footer.SetUsage` orders snapshots by arrival, so an older answer can overwrite a newer one (independent of this decision).

**Section 8, replace in the table:** S3 row, column "Owns": `... internal/agent/coddy_provider.go (+ test: the end update, `Model` on both)`, and its tag `**[D2][D4]**` becomes `**[D2]**`; S7 row "After": `contract of 4.4, 4.5 and the wire of 4.6 only`; S8 row "Owns" adds `(the end branch and tombstone of `a.remoteBusy`, 4.6)`.
**Section 8a, row "`acp.ProviderUsageUpdate.Model`, the subject form ..., `Manager.CachedProviderUsage`":** delete `Manager.CachedProviderUsage` and add: `the countdown family and its end update (4.6)` in "Used by": S7, S8, agent.
**Section 8, last paragraph:** replace `D1 to D4 ... (m7 to m10)` by `... D4 is done: p2-d4-busy-notice (the others m7 to m9)`.
**Section 4.8, Surfaces sentence (append):** `Countdown (B.6): a snapshot arriving during a wait leaves the banner; the end leaves the snapshot; an end older than the countdown, a replayed countdown after the end and a tied end (end wins) behave as the tombstone says; turn end and expiry drop the slot; an `Unsupported` read answer leaves the countdown alone; `Model` on both updates; the agent test asserts the clear is the end update (no `Unsupported`, stamp after the last notice) on admission, error and cancel; the existing console tests that clear with `Unsupported` are rewritten to the end update.`
**Section 9 (Risks), add:** `Wire: the end update is a usage update without data and with `blocked: false`; a client older than this change shows nothing for it and keeps a stale countdown until its turn ends (same as for any unknown update).`

## 9. Inconclusive / not done

The large `REPLAY=1 TIEEND=1` run at scale A (`RP1T`) stopped on the engine's 1 GiB memory estimate for L1 (`inconclusive`/`unknown`) and `RP2` on the 40 min wall limit with no report: the tie property was
re-run at scale T (3.2). The first-batch runs `N3snap` (split + snapshot clear + nets), `N3b`, `N3x` (full set) produced no report (process ended without output, cause unknown) and were not repeated; `N3x` was re-run as `core` (3.1), the net-ablations are in 3.2. Model sha256 of the final file: see `sha256sum p2-d4-busy-notice.pml` (the runs used earlier copies that differ in the comment header, the default-off switches `TIEEND`/`TOMB=2`/`ARRIVAL=2` and `WARM`).
