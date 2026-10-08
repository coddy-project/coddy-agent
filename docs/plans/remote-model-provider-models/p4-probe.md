# p4-probe: the application probe of a vanished peer

Model `p4-probe.pml` (Promela, one call, one node, one client, an attacker; discrete time as a variable, the clock advances only when no process has an enabled action at the current tick). Engine `mcd` (0.2.0 for the first version, 0.3.1 for the revision; the verdicts of the first version were reproduced identically under 0.3.1), 2026-10-08, one property per run (`-D ASSERT_PROP=<latch>`), explicit state, every run complete (no state budget hit). The header comment of the model lists every switch and the abstractions. Plan: `../remote-model-provider-probe.md` (sections 2 and 3).

**Revision.** The first version armed the guard at the call's start (`ARM = 0`). A cross-review (three reviewers, every claim re-run) found that the model was kinder to the design than the system: a path that carries no ping at all would cut every live client, the first-ping time was not modelled, and the tie rule was not stated. The model now has `ARM = 1` (armed by the first accepted ping) as its default, the option `BROKEN` (every ping lost), the options `TIE` and `SLACK`, a latch for the unguarded case, and an `#error` for a parameter range it cannot express; the plan's section 3.1 records the decision.

Defaults: `I = 3` (the client's ping interval), `L = 1` (lost pings in a row), `DMAX = 2` (the longest a ping takes), `G = (L + 1) I + DMAX = 8` (the grace), `T = 24` ticks; a tick is a second in the plan's constants (I = 10, L = 2, DMAX = 5, G = 35).

Commands (`M=p4-probe.pml`, flags `--no-timing --budget-states 20000000 --budget-ms 550000 --budget-mem-mb 6000`; the result is of the `assert` property, `deadlock` is verified in every run):

```
# S1, a live client is never cut
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D VAN=0                          # verified (25077 states)
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D VAN=0 -D G=7                   # G - 1: violated
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D VAN=0 -D TIE=1                 # the cut wins the tie: violated; with G=9 verified
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D VAN=0 -D BROKEN=1              # a path that carries no ping, ARM=1: verified (1645)
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D VAN=0 -D BROKEN=1 -D ARM=0     # the same, armed at the start: violated
mcd check --promela $M -D ASSERT_PROP=gFalseCut                                   # with a vanish later in the run: verified (418031)
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D VAN=0 -D L=2 -D DMAX=1 -D G=10 -D T=30   # verified;  G=9: violated
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D VAN=0 -D I=4 -D G=10 -D T=30            # verified;  G=9: violated
# S2, a client that vanished after a ping is freed within DMAX + G + 1 ticks
mcd check --promela $M -D ASSERT_PROP=gLate                                       # verified (418031); also -D G=7 and -D SLACK=0
# S3, a call that did not opt in is not cut
mcd check --promela $M -D ASSERT_PROP=gCutNonOpt -D OPT=0                         # verified
mcd check --promela $M -D ASSERT_PROP=gCutNonOpt -D OPT=0 -D STRICT=1             # a guard on every call: violated
mcd check --promela $M -D ASSERT_PROP=gFalseCut -D NODE=0 -D VAN=0                # an old node never confirms: verified (1237)
# S4, the slot is released once
mcd check --promela $M -D ASSERT_PROP=gDouble                                     # verified
mcd check --promela $M -D ASSERT_PROP=gDouble -D ONCE=0                           # every path releases: violated
# S5, a foreign pinger
mcd check --promela $M -D ASSERT_PROP=gForeign -D ATT=0                           # no id: verified
mcd check --promela $M -D ASSERT_PROP=gForeign -D ATT=1 -D AUTH=1                 # the id with another credential: verified (11.3 M)
mcd check --promela $M -D ASSERT_PROP=gForeign -D ATT=1 -D AUTH=0                 # the id alone proves a ping: violated
mcd check --promela $M -D ASSERT_PROP=gForeign -D ATT=2 -D AUTH=1                 # the id and the call's credential: violated
# sanity: each latch is reachable (the assertion is violated), over 418031 states
mcd check --promela $M -D ASSERT_PROP=<sCut|sDel|sLost|sVan|sDone|sUnguarded>
```

## Verdicts

- **P1, who is cut: (a), only a call that opted in and was confirmed.** With a guard on every call a foreign client that never pings is cut (S3, and with it S1). A client of an old node, which never confirms, has no guard.
- **P2, when the guard is armed: (b), at the first accepted ping.** Armed at the start, a ping path that carries nothing (`BROKEN`) cuts every live client (S1 violated); armed by the first ping it cuts none (verified), and a client that vanished after its first ping is freed within `DMAX + G + 1` (S2 verified). What is given up is a client that vanishes before its first ping (`sUnguarded`, reachable by design): it falls back to the transport bound, as today.
- **P3, the grace: `G = (L + 1) I + DMAX` is the exact boundary.** Verified at (I, L, DMAX) = (3, 1, 2), (3, 2, 1) and (4, 1, 2) with G = 8, 10 and 10, each G - 1 violated: a live client whose pings are delayed by 0 and then by DMAX, with L lost between, has a gap of `(L + 1) I + DMAX`. The tie rule is part of the boundary: a guard that wins the tie (`TIE = 1`, a timer of exactly G) is the G - 1 case. The plan's constants follow: I = 10 s, L = 2, DMAX = 5 s give G = 35 s, implemented as G + 1 s on the monotonic clock so that a ping on the tick the grace ends wins; the bound after a vanish is G + DMAX + that second, 41 s (the model's discrete ticks put the last ping a tick before the vanish, which is why its latch also holds with `SLACK = 0`).
- **P4, what proves a ping: the id and the credential of the call on a direct listener; the id alone through a relay.** A foreign pinger without the id never re-arms a guard (verified). With the id and another credential it does when the id alone proves a ping (`AUTH = 0`) and does not when the credential is part of the proof (`AUTH = 1`, verified). With the id **and** the call's credential it does in both, which is the capability's meaning: through a relay every client shares the node's credential, so the id (128 random bits, in the response of the call only, carried in a header and not in a path) is the proof.
- **S4, exactly-once release.** A guard that fires at the same tick as the completion releases the slot once when `release()` is guarded (verified) and twice when every path releases (violated). The code keeps its existing guarded release.

## Not modelled

- A ping that arrives after the call ended: the node ignores it when the call is not running (a `404` in the code), so resurrection cannot be expressed here; the node's unit test holds it.
- Reordering beyond the delay choice. `DMAX >= I` (a ping delayed past the next one) is excluded by an `#error`, since the model's slots would drop it without counting a loss; the plan's constants keep DMAX at half of I.
- Guessing the id (128 random bits, a boolean in the model), a client that leaks it, and a balancer that sends the pings of one call to different instances (the plan's section 6).
- The stream's heartbeat and the transport bound; the probe is an additional bound on the calls that opted in.
- A longer horizon than T = 24: the model is invariant under translation of time, so a vanish after tick 12 adds no behaviour the earlier ones do not have.
- The decisions P1, P2 and P4 follow from how the switches are encoded as much as from a search; P3, the tie rule and S4 carry the search content. The wording of the verdicts says so.

## Found while modelling

- A first version of the model stored a free slot of the pings in flight backwards, so no ping was ever queued and S1 came out violated for every G; the counterexample (a live client cut with three pings sent and none delivered) was the defect of the model, not of the design.
- The cross-review found the fail-closed behaviour of the start-armed guard (`BROKEN`), the unspecified first-ping time and the tie rule (`TIE`), which moved the plan to P2 (b) and to a guard of G + 1 s.
