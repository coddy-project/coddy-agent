# p4-probe: the application probe of a vanished peer

Model `p4-probe.pml` (Promela, one call, one node, one client, an attacker; discrete time as a variable, the clock advances only when no process has an enabled action at the current tick). Engine `mcd 0.2.0` CLI, 2026-10-08, one property per run (`-D ASSERT_PROP=<latch>`), explicit state, every run complete (no state budget hit) unless noted. The header comment of the model lists every switch and the abstractions. Plan: `../remote-model-provider-probe.md` (sections 2 and 3).

Defaults: `I = 3` (the client's ping interval), `L = 1` (lost pings in a row), `DMAX = 2` (the longest a ping takes), `G = (L + 1) I + DMAX = 8` (the grace), `T = 24` ticks.

Commands (`M=p4-probe.pml`, flags `--no-timing --budget-states 20000000 --budget-ms 550000 --budget-mem-mb 6000`):

```
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D VAN=0                    # S1: a live client is never cut            verified (25033 states)
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D VAN=0 -D G=7             # S1 at G - 1                                violated
mcd check --promela $M -D ASSERT_PROP=gFalseCut                             # S1 with a vanish later in the run          verified (417123)
mcd check --promela $M -D ASSERT_PROP=gLate                                 # S2: a vanished client is freed by DMAX+G+1 verified
mcd check --promela $M -D ASSERT_PROP=gLate -D ARM=1                        # S2, armed at the first ping                violated
mcd check --promela $M -D ASSERT_PROP=gCutNonOpt -D OPT=0                   # S3: a call that did not opt in is not cut  verified
mcd check --promela $M -D ASSERT_PROP=gCutNonOpt -D OPT=0 -D STRICT=1      # S3, every call has a guard                 violated
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D NODE=0 -D VAN=0          # an old node never confirms: no guard       verified
mcd check --promela $M -D ASSERT_PROP=gDouble                               # S4: the slot is released once              verified
mcd check --promela $M -D ASSERT_PROP=gDouble -D ONCE=0                     # S4, every path releases                    violated
mcd check --promela $M -D ASSERT_PROP=gForeign -D ATT=0                     # S5: no id, no ping                         verified
mcd check --promela $M -D ASSERT_PROP=gForeign -D ATT=1 -D AUTH=1          # S5: the id with another credential         verified (11.2 M states)
mcd check --promela $M -D ASSERT_PROP=gForeign -D ATT=1 -D AUTH=0          # S5: the id alone proves a ping             violated
mcd check --promela $M -D ASSERT_PROP=gForeign -D ATT=2 -D AUTH=1          # S5: the id and the call's credential       violated
```

## Verdicts

- **P1, who is cut: option (a), only a call that opted in and was confirmed.** With a guard on every call (`STRICT = 1`) a foreign client that never pings is cut (S3 violated, and with it S1: a live caller is cut), which is what the phase 2 evidence said of the strict probe. With (a) a call that did not ask is never cut, and a client of an old node, which never confirms, has no guard at all.
- **P2, when the guard is armed: at the call's start.** Armed at the first accepted ping (`ARM = 1`), a client that vanishes before its first ping holds the slot for the rest of the call (S2 violated); at the start, every vanished client is freed by `DMAX + G + 1` ticks after it vanished (S2 verified, also at G = 7: the bound is the model's own, `DMAX + G + 1` for any G the client's pings can keep alive).
- **P3, the grace: `G = (L + 1) I + DMAX` is the exact boundary.** It is verified at (I, L, DMAX) = (3, 1, 2), (3, 2, 1) and (4, 1, 2) with G = 8, 10 and 10, and each G - 1 is violated: a live client whose pings are delayed by DMAX once and not at all the next time, with L of them lost in between, is cut one tick early. The plan's constants follow: I = 10 s, L = 2, DMAX = 5 s give G = 35 s (the node's constant; the transport bound B = 45 s stays the later bound).
- **P4, what proves a ping: the id and the credential of the call on a direct listener; the id alone through a relay.** A foreign pinger without the id never keeps a call alive (S5 verified for ATT = 0). With the id and another credential it does when the id alone proves a ping (AUTH = 0) and does not when the credential is part of the proof (AUTH = 1). With the id **and** the call's credential it does in both, which is the capability's meaning: through a relay every client shares the node's credential, so the credential adds nothing there and the id (128 random bits, only in the response of the call) is the proof. The documentation says so.
- **S4, exactly-once release.** A guard that fires at the same tick as the completion releases the slot once when `release()` is guarded (`ONCE = 1`, verified) and twice when every path releases (violated). The code keeps the existing guarded release.

Sanity (each latch is reachable, so no verdict is vacuous): `sCut`, `sDel`, `sLost`, `sVan`, `sDone` are all violated as assertions, i.e. reachable, over 417123 states.

## Not modelled

- Reordering beyond the delay choice, a ping that is delayed past the next one (needs `DMAX >= I`, excluded by the plan's constants: DMAX 5 s against I 10 s).
- Guessing the id (128 random bits, a boolean in the model) and a client that leaks it.
- The stream's heartbeat and the transport bound; the probe is an additional bound on the calls that opted in.
- The client's goroutine being late by itself: its lateness is part of DMAX and the test of the client holds I with a margin.

## Found while modelling

A first version of the model stored a free slot of the pings in flight backwards, so no ping was ever queued and S1 came out violated for every G. The counterexample, a live client cut with three pings sent and none delivered, was the defect of the model, not of the design; the loop was corrected and every run above is of the corrected model.
