# p5-pinger: the guard goroutine and the client's pinger

Model `p5-pinger.pml` (Promela; five processes: the node's call that ends in the deferred `stop()`, the guard `run()`, the environment that fires the timer, the client's pinger and the client's stream, which cancels the pinger and waits for it) and its companion `p5-pinger-split.pml` (the node's handler of a ping as a process of its own). Engine `mcd` 0.3.1. Code: `external/httpserver/shared_probe.go` (`ping`, `run`, `stop`, `llmAlivePost`), `shared_models_http.go` (`runSharedCall`), `internal/llm/coddy_probe.go` (`startProbe`, `ping`). It is the concurrent half of the application probe, of which `p4-probe` is the timed half (arming, the grace, the tie): this model has no clock, only the order of steps. Bounded: K = 2 pings and TEV = 3 timer events; the timer is one-shot, as a Go timer is, and fires again only after a Reset (`timerOn`). Every loop is bounded, so the state graph is finite and acyclic and "no invalid end state" means every goroutine terminates, on the assumption that a ping request always ends, by cancel or by the DMAX bound; the ten latches and the deadlock check were also run at K = 3.

Commands (`M=p5-pinger.pml`, `F="--no-timing --budget-states 30000000 --budget-ms 900000 --budget-mem-mb 6144"`; without the flags a run stops at the default budget of a million states and is inconclusive; the runs were made on a 32-CPU host with the same `mcd` 0.3.1 binary as on the workstation (sha256 `c3c2a2c8...`), at most 60% busy, ten at a time; the result is of the `assert` property, and `deadlock` is verified in every run that does not say otherwise; a run that ends in a violation may stop at its first counterexample, which `complete` says):

```
# the code: every latch holds, no invalid end state
mcd check --promela $M $F -D ASSERT_PROP=gAck204Cut                                         # verified (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=gIgnored                                           # verified (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=gCancelTwice                                       # verified (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=gCancelLate                                        # verified (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=gIdLeak                                            # verified (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=gRunLeak                                           # verified (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=gArmAfterStop                                      # verified (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=gPingerLeak                                        # verified (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=gAfter404                                          # verified (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=gCutNoCancel                                       # verified (1,033,478 states)
# the same at K = 3 pings
mcd check --promela $M $F -D K=3 -D ASSERT_PROP=gAck204Cut                                  # verified (3,306,787 states)
mcd check --promela $M $F -D K=3 -D ASSERT_PROP=gIgnored                                    # verified (3,306,787 states)
mcd check --promela $M $F -D K=3 -D ASSERT_PROP=gCancelTwice                                # verified (3,306,787 states)
mcd check --promela $M $F -D K=3 -D ASSERT_PROP=gCancelLate                                 # verified (3,306,787 states)
mcd check --promela $M $F -D K=3 -D ASSERT_PROP=gIdLeak                                     # verified (3,306,787 states)
mcd check --promela $M $F -D K=3 -D ASSERT_PROP=gRunLeak                                    # verified (3,306,787 states)
mcd check --promela $M $F -D K=3 -D ASSERT_PROP=gArmAfterStop                               # verified (3,306,787 states)
mcd check --promela $M $F -D K=3 -D ASSERT_PROP=gPingerLeak                                 # verified (3,306,787 states)
mcd check --promela $M $F -D K=3 -D ASSERT_PROP=gAfter404                                   # verified (3,306,787 states)
mcd check --promela $M $F -D K=3 -D ASSERT_PROP=gCutNoCancel                                # verified (3,306,787 states)
# mutants, each against the latch it breaks
mcd check --promela $M $F -D CHECKCUT=0 -D ASSERT_PROP=gAck204Cut                           # violated (1,073,833 states), the handler answers 204 to a call the guard has cut
mcd check --promela $M $F -D CHECKFRESH=0 -D ASSERT_PROP=gIgnored                           # violated (916,934 states), the guard cuts at the first timer: the stale-timer race
mcd check --promela $M $F -D CUTOUTSIDE=1 -D ASSERT_PROP=gIgnored                           # violated (1,182,980 states), the guard decides under the lock, unlocks, and sets cut afterwards
mcd check --promela $M $F -D CUTOUTSIDE=1 -D ASSERT_PROP=gAck204Cut                         # verified (1,182,980 states), this latch is not the one it breaks
mcd check --promela $M $F -D RETURNAFTERCUT=0 -D ASSERT_PROP=gCancelTwice                   # verified (996,873 states), run() goes on after the cut but its one-shot timer fires no more: an equivalent mutant
mcd check --promela $M $F -D RETURNAFTERCUT=2 -D ASSERT_PROP=gCancelTwice                   # violated (3,354,764 states), run() goes on and its timer fires again: the return dropped and the Reset kept
mcd check --promela $M $F -D NOJOIN=1 -D ASSERT_PROP=gCancelLate                            # violated (1,966,129 states), stop() does not wait for run()
mcd check --promela $M $F -D NODELETE=1 -D ASSERT_PROP=gIdLeak                              # violated (433,636 states), stop() does not forget the id
mcd check --promela $M $F -D NOJOIN=1 -D ASSERT_PROP=gRunLeak                               # violated (1,966,129 states), the same, seen as a goroutine that outlives the call
mcd check --promela $M $F -D ARMEDEARLY=1 -D ASSERT_PROP=gRunLeak                           # violated, deadlock violated (74,498 states, stops at the counterexample), stop() reads armed before it takes the lock
mcd check --promela $M $F -D CHECKSTOPPED=0 -D ASSERT_PROP=gArmAfterStop                    # violated, deadlock violated (3,841 states, stops at the counterexample), a ping of a stopped call arms the guard
mcd check --promela $M $F -D CHECKSTOPPED=0 -D ASSERT_PROP=gRunLeak                         # violated, deadlock violated (77,473 states, stops at the counterexample), the armed run() is never joined
mcd check --promela $M $F -D NOCANCEL=1 -D ASSERT_PROP=gCutNoCancel                         # violated (917,920 states), the guard cuts and never cancels the call
mcd check --promela $M $F -D NOCANCEL=1 -D ASSERT_PROP=gIdLeak                              # verified (917,920 states), this latch is not the one it breaks (the nine others stay verified: one checked)
mcd check --promela $M $F -D CWAIT=0 -D ASSERT_PROP=gPingerLeak                             # violated (2,958,584 states), the client's stop does not wait for the pinger
mcd check --promela $M $F -D WAITFIRST=1 -D ASSERT_PROP=gPingerLeak                         # assert verified, deadlock violated (398,399 states), the client's stop waits for the pinger before it cancels it
mcd check --promela $M $F -D STOP404=0 -D ASSERT_PROP=gAfter404                             # violated (1,067,699 states), the pinger keeps pinging after a 404
mcd check --promela $M $F -D UNLOCKEDSTOP=1 -D ASSERT_PROP=gIdLeak                          # assert verified, deadlock violated (526,516 states), stop() waits for run() holding the lock
# sanity: each step is reachable (the assertion is violated), over 1,033,478 states
mcd check --promela $M $F -D ASSERT_PROP=sCut                                               # violated (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=sArm                                               # violated (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=s204                                               # violated (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=s404                                               # violated (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=sComplete                                          # violated (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=sInFlightCancel                                    # violated (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=sLost                                              # violated (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=sRearm                                             # violated (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=sRace                                              # violated (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=sCutAfterRefresh                                   # violated (1,033,478 states)
mcd check --promela $M $F -D ASSERT_PROP=s204AfterCut                                       # violated (1,033,478 states)

# the companion, the handler of a ping as a process of its own: M=p5-pinger-split.pml, the same F but --budget-mem-mb 3000 (its TEV defaults to 2)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=gAck204Cut                        # verified (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=gIgnored                          # verified (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=gCancelTwice                      # verified (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=gCancelLate                       # verified (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=gIdLeak                           # verified (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=gRunLeak                          # verified (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=gArmAfterStop                     # verified (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=gPingerLeak                       # verified (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=gAfter404                         # verified (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=gCutNoCancel                      # verified (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D CHECKCUT=0 -D ASSERT_PROP=gAck204Cut          # violated (4,094,542 states)
mcd check --promela p5-pinger-split.pml $F -D CHECKFRESH=0 -D ASSERT_PROP=gIgnored          # violated (3,605,664 states)
mcd check --promela p5-pinger-split.pml $F -D CUTOUTSIDE=1 -D ASSERT_PROP=gIgnored          # violated (4,517,214 states)
mcd check --promela p5-pinger-split.pml $F -D CUTOUTSIDE=1 -D ASSERT_PROP=gAck204Cut        # verified (4,517,214 states)
mcd check --promela p5-pinger-split.pml $F -D RETURNAFTERCUT=0 -D ASSERT_PROP=gCancelTwice  # verified (4,132,941 states)
mcd check --promela p5-pinger-split.pml $F -D RETURNAFTERCUT=2 -D ASSERT_PROP=gCancelTwice  # violated (7,308,882 states)
mcd check --promela p5-pinger-split.pml $F -D NOJOIN=1 -D ASSERT_PROP=gCancelLate           # violated (7,950,904 states)
mcd check --promela p5-pinger-split.pml $F -D NODELETE=1 -D ASSERT_PROP=gIdLeak             # violated (2,795,596 states)
mcd check --promela p5-pinger-split.pml $F -D NOJOIN=1 -D ASSERT_PROP=gRunLeak              # violated (7,950,904 states)
mcd check --promela p5-pinger-split.pml $F -D ARMEDEARLY=1 -D ASSERT_PROP=gRunLeak          # violated (351,599 states, stops at the counterexample)
mcd check --promela p5-pinger-split.pml $F -D CHECKSTOPPED=0 -D ASSERT_PROP=gArmAfterStop   # violated (52,672 states, stops at the counterexample)
mcd check --promela p5-pinger-split.pml $F -D CHECKSTOPPED=0 -D ASSERT_PROP=gRunLeak        # violated (581,798 states, stops at the counterexample)
mcd check --promela p5-pinger-split.pml $F -D NOCANCEL=1 -D ASSERT_PROP=gCutNoCancel        # violated (4,069,111 states)
mcd check --promela p5-pinger-split.pml $F -D NOCANCEL=1 -D ASSERT_PROP=gIdLeak             # verified (4,069,111 states)
mcd check --promela p5-pinger-split.pml $F -D CWAIT=0 -D ASSERT_PROP=gPingerLeak            # violated (11,878,934 states)
mcd check --promela p5-pinger-split.pml $F -D WAITFIRST=1 -D ASSERT_PROP=gPingerLeak        # assert verified, deadlock violated (1,573,878 states)
mcd check --promela p5-pinger-split.pml $F -D STOP404=0 -D ASSERT_PROP=gAfter404            # violated (4,212,975 states)
mcd check --promela p5-pinger-split.pml $F -D UNLOCKEDSTOP=1 -D ASSERT_PROP=gIdLeak         # assert verified, deadlock violated (2,230,629 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=sLateHandler                      # violated (4,110,672 states)
mcd check --promela p5-pinger-split.pml $F -D ASSERT_PROP=sHandlerAfterPinger               # violated (4,110,672 states)
```

## Verdicts

- **N1** no ping is accepted on a call the guard has already cut: the cut and the check are under one lock (the 204 may still be written after a cut that follows the acceptance, which is harmless and reachable: `s204AfterCut`). **N2** a timer event never cuts a call that had a ping accepted since the last arming: the decision is taken under the same lock as the ping, and the arming ping is not a fresh one; the time arithmetic is `p4-probe`'s. **N3, N3b** the cancel runs at most once, and never after `stop()` returned; **N7** a cut is always followed by the cancel (at least once: the other half). **N4, N5** the id is forgotten and the goroutine joined when `stop()` returns. **N6** no ping arms the guard of a call that has stopped, including the interleaving where the handler found the id and `stop()` ran before its locked step (`sRace` is reachable). **C1** the pinger is gone when the client's stop returns. **C2** the pinger never pings after a delivered 404.
- **What the mutants carry.** N1, N2, C1 and C2 are true by the construction of the rule that their mutant switches (the 404 is decided in the same step as the check; the `break` after a 404): the mutants carry the content. N4 can fail only through `NODELETE`, N5 through `NOJOIN`, `ARMEDEARLY` or `CHECKSTOPPED=0` (a late arming leaves a `run()` nothing joins), N7 through `NOCANCEL`. N2 assumes the timer is handled before the latest ping is a grace old: a guard that is late may correctly cut by the code's `elapsed >= limit` arithmetic, which `p4-probe` owns and this model has no clock for (every outcome stays reachable: `sCutAfterRefresh`). `RETURNAFTERCUT=0` (the return dropped) is an **equivalent mutant**: a Go timer is one-shot, so a `run()` that goes on waits for a stop channel and cannot cancel twice; the mutant of the code is the return dropped and the `Reset` kept (`RETURNAFTERCUT=2`), which violates N3. (The first version let the environment fire the timer whenever the guard was armed, and so showed a double cancel the code could not produce.) `CUTOUTSIDE` (the decision taken under the lock but `cut` set after the unlock) gives N2 a content that is not a tautology: it violates N2 and not N1. `UNLOCKEDSTOP` (a join under the lock, against a `run()` that needs it) and `WAITFIRST` (the client waits for a pinger it has not cancelled) are caught by the deadlock check only, and `CHECKSTOPPED=0` and `ARMEDEARLY=1` also by it (an armed `run()` that nothing joins).
- The deadlock check is clean for the code (every goroutine is released), at K = 2 and at K = 3, on the assumption that the ping request always ends (see Not modelled).
- All eleven sanity steps are reachable, among them a request that reaches the handler after the client cancelled (`sInFlightCancel`), a lost answer (`sLost`), a timer that finds a fresh ping and re-arms (`sRearm`), a call that is cut after a refresh because the next re-arm found no newer ping (`sCutAfterRefresh`), and a 204 delivered for a ping accepted before a cut.
- **The companion** (the handler as a process of its own, the pinger not waiting for it) gives the same verdicts: all nine latches and the deadlock check hold at 4,110,672 states, and every mutant is still caught by the latch it was written for. A handler that finishes after the client's stop returned (`sLateHandler`) and one that finishes after the pinger returned (`sHandlerAfterPinger`) are reachable, so the fused handler of the main model was more restrictive than the code in that respect, and not in a way that matters for any property.

## Not modelled

One call; the credential check of the ping (a handler that refuses is a 404, like the stopped case); a 404 that is not `unknown_call`, and an error while the client is alive, are an answer lost (the pinger goes on); the network beyond "the answer is delivered or lost"; the clock (the timer's expiry is an event, the length of the grace is `p4-probe`); the heartbeat and the write deadlines of the stream; `Timer.Reset` with the remaining time (one more timer event); in the main model the handler of a ping is part of the pinger's process, which the companion relaxes; a cancel that deadlocks with the probe's lock (`cancel` is a plain `context.CancelFunc` and takes no probe lock: a mutant that cancels under the lock survives every latch, an equivalent mutant); the end of the ping request itself (it is assumed to end by cancel or by the DMAX bound, `context.WithTimeout(ctx, every/2)` in `coddy_probe.go`: a regression that unbinds it needs a clock to express); a guard that handles its timer a whole ping-age late (see N2); the removal of `&& !p.stopped` in `run()`, which no latch catches and which is harmless (a cancel after the call ended is a no-op, and `fired()` is read before `stop()`).

## Found while modelling

Nothing in the code. The first version of the model was wrong in four ways that a review of the model, not of the code, found: `done` and `runAlive` were two steps, so the join could pass between them (a false `gRunLeak`); `Env` never ended when the guard was never armed, which made every run report a deadlock and hid the real one; `gPingerLeak` and `gAfter404` could not be set (a blocking wait before the latch, a `break` before the next ping), so their "verified" meant nothing; and the arming ping counted as fresh. Each was fixed and each now has the mutant that proves the latch can fail.

## Review

**Round 1** (three reviewers, one of whom had to be recovered from its session; the whole first matrix of 28 runs reproduced). Verdict: approve with changes; no verdict for the code turned violated under any stricter variant (K = 3, the handler as its own process, four more mutants). Changed: the id-leak latch N4 could not fail, because nothing set the id again and no mutant forgot to delete it, so the report's claim that no mutant was vacuous was false (`NODELETE`; the two rows that were labelled with it are relabelled and explained); K = 2 was not justified and the timer bound was hard-coded (`TEV`, the K = 3 run, the boundedness sentence); the fused handler was more restrictive than the code (the companion model); N1 and N2 were worded as time properties and as unconditional (reworded; `CUTOUTSIDE`); three mutants were missing (`ARMEDEARLY`, `WAITFIRST`, `CUTOUTSIDE`); two sanity steps were added. Rejected with evidence: that the model excludes a legitimate cut after a refresh (`sCutAfterRefresh` is reachable), that a cancel inside the lock deadlocks (`cancel` takes no probe lock; the mutant survives, an equivalent mutant), and that the deadlock check does not establish termination (every loop is bounded).

**Round 2** (two reviewers answered, a third stalled on the first request and could not be recovered; every figure of the first fixed report reproduced). Verdict: approve with changes; no verdict for the code changed. Changed: the mutant `RETURNAFTERCUT=0` showed a double cancel that the code cannot produce, because the environment fired the timer whenever the guard was armed: the timer is one-shot now (`timerOn`), `RETURNAFTERCUT=0` is an equivalent mutant and `RETURNAFTERCUT=2` (the return dropped, the Reset kept) is the real one, and every state count changed; a guard that cuts and never cancels was caught by no latch: N7 and `NOCANCEL`; the N5 and N2 sentences claimed more than the model shows (three mutants break N5; N2 assumes a timely timer); the commands carry the flags they need; the termination claim names its assumption; the companion model is made from the main one by script and was re-run. Rejected with evidence: that a cancel under the lock deadlocks (an equivalent mutant), that the model excludes a legitimate cut after a refresh (`sCutAfterRefresh` is reachable), and "needs rework" (the evidence supports wording and coverage changes). Not worth fixing: the removal of `!p.stopped` in `run()` (harmless), and a pending-request state with cancellation (a mutant would restate its own construction).
