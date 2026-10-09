# p5-registry: the relay's registry of one node name

Model `p5-registry.pml` (Promela; two honest nodes A and B and an operator contending for one name; the clock is the age of the lease in ticks since its last accepted registration, saturating one past TTL + GRACE, TTL = GRACE = 3 as in the code, whose registry has `grace == ttl`; reaping runs at the start of every registration, as `RegisterWithDial` does, and in a process of its own at any time, as `Len`, `Node` and `AttachTransport` do). Engine `mcd` 0.3.1. Code: `external/swarm/registry.go` (`RegisterWithDial`, `Pin`, `Delete`, `reapLocked`) and `external/swarm/server.go` (`seedUpstreams`, which discards the response of an operator's registration: nobody holds the secret of an operator's lease). It exists because stage H0 changed what an owner's renewal does to the stored token and `swarm.node_tls` made the dial of a lease something that is inherited.

Commands (`M=p5-registry.pml`, `F="-por --no-timing --budget-states 40000000 --budget-ms 1800000 --budget-mem-mb 6144"`, run locally after `wait-for-capacity.sh`; without the flags a run stops at the default budget of a million states and is inconclusive; the result is of the `assert` property; every run is exhaustive; `deadlock` is not claimed, because the clock is an always-enabled loop and no state can be a deadlock):

```
# R1 an accepted registration leaves the token the node carried
mcd check --promela $M $F -D ASSERT_PROP=gTokenTruth                            # verified (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=gTokenTruth -D ERASE=0                 # violated (1,360,584 states), an empty token keeps the old one: the code before H0
mcd check --promela $M $F -D ASSERT_PROP=gTokenTruth -D CLAIMNOTOKEN=1          # violated (1,599,050 states), a claim stores no token
# R2 a registration with a secret that is not the lease's changes nothing (token, dial, age, secret, owner, pin)
mcd check --promela $M $F -D ASSERT_PROP=gTakeover                              # verified (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=gTakeover -D STRANGER=1                # violated (4,904,436 states), the secret is not checked
mcd check --promela $M $F -D ASSERT_PROP=gTakeover -D STRANGER2=1               # violated (2,831,303 states), a refused registration still hands the stranger a secret and the lease
mcd check --promela $M $F -D ASSERT_PROP=gTakeover -D DISCLOSE=1                # verified (2,194,842 states), NOT caught here: a disclosed secret changes none of the fields R2 compares; R6 catches it
# R3 the operator's dial survives a node's registration (vacuous in the code's configuration, see Verdicts); R3b a node's lease dials with nodeDial when node_tls is set and with nothing when it is not
mcd check --promela $M $F -D ASSERT_PROP=gDialOwn                               # verified (1,319,742 states), vacuous in the code's configuration
mcd check --promela $M $F -D ASSERT_PROP=gDialOwn -D SECRETREUSE=1 -D INHERIT=0 # violated (835,834 states), a combined mutant
mcd check --promela $M $F -D ASSERT_PROP=gDialDefault                           # verified (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=gDialDefault -D NDIAL=0                # verified (1,319,742 states), no swarm.node_tls: the lease has no dial
mcd check --promela $M $F -D ASSERT_PROP=gDialDefault -D NDIAL=0 -D DIALLEAK=1  # violated (1,319,742 states), a claim takes a dial that node_tls did not supply
mcd check --promela $M $F -D ASSERT_PROP=gDialDefault -D INHERIT=0              # violated (1,442,706 states), a renewal replaces the dial with what it carries
# R4 a pinned lease is never reaped; R5 a live lease is not reaped before expiry + grace; R7 a lease past it is not still held when a registration begins
mcd check --promela $M $F -D ASSERT_PROP=gPinnedReaped                          # verified (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=gPinnedReaped -D REAPPIN=1             # violated (3,140,712 states)
mcd check --promela $M $F -D ASSERT_PROP=gLiveReaped                            # verified (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=gLiveReaped -D EARLY=1                 # violated (2,200,950 states), reaps past the expiry with no grace
mcd check --promela $M $F -D ASSERT_PROP=gLiveReaped -D RENEWNOAGE=1            # violated (2,984,526 states), a renewal does not extend the lease
mcd check --promela $M $F -D ASSERT_PROP=gLiveReaped -D LATE=1                  # verified (514,440 states), reaping too late: not what R5 guards
mcd check --promela $M $F -D ASSERT_PROP=gExpiredHeld                           # verified (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=gExpiredHeld -D NOREAPREG=1            # violated (2,399,706 states), RegisterWithDial does not reap first
mcd check --promela $M $F -D ASSERT_PROP=gExpiredHeld -D LATE=1                 # violated (514,440 states), a lease is reaped one tick late, at the saturated age never
# the verdicts do not depend on the length of the grace: the same with GRACE = 2
mcd check --promela $M $F -D GRACE=2 -D ASSERT_PROP=gLiveReaped                 # verified (1,159,452 states)
mcd check --promela $M $F -D GRACE=2 -D ASSERT_PROP=gLiveReaped -D EARLY=1      # violated (1,966,986 states)
mcd check --promela $M $F -D GRACE=2 -D ASSERT_PROP=gExpiredHeld                # verified (1,159,452 states)
mcd check --promela $M $F -D GRACE=2 -D ASSERT_PROP=gExpiredHeld -D NOREAPREG=1 # violated (2,113,344 states)
# R6 a node was accepted on a lease another node holds
mcd check --promela $M $F -D ASSERT_PROP=gStaleOwner                            # verified (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=gStaleOwner -D SECRETREUSE=1           # violated (652,646 states), a secret derived from the name: a stale secret is good again
mcd check --promela $M $F -D ASSERT_PROP=gStaleOwner -D DISCLOSE=1              # violated (2,194,842 states), a refused registration discloses the current secret
# sanity: each latch is reachable (the assertion is violated), over 1,319,742 states
mcd check --promela $M $F -D ASSERT_PROP=sTake                                  # violated (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=sErase                                 # violated (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=sOwnDial                               # violated (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=sReap                                  # violated (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=sRenew                                 # violated (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=sStale                                 # violated (1,319,742 states)
mcd check --promela $M $F -D ASSERT_PROP=sOwnRenew                              # verified (1,319,742 states), NOT reachable, by design
mcd check --promela $M $F -D ASSERT_PROP=sOwnRenew -D SECRETREUSE=1             # violated (652,646 states), reachable only when a stale secret is good again
```

## Verdicts

- **R1** holds for the code (the guard `if req.Token != ""` is gone) and fails for `ERASE=0` (the node that stopped sending a token kept the old one). Its claim half is a tautology (it checks `ltoken != tok` right after `ltoken = tok`); `CLAIMNOTOKEN` (a claim that stores no token) is the mutant that gives it content.
- **R2** holds: every registration on a held name needs the lease secret and changes nothing else when it has none (`STRANGER=1`, the check dropped, breaks it; `STRANGER2=1`, a refusal that still hands out a secret and so changes the owner, breaks it). The `pin` clause of the latch is an unexercised guard: no mutant touches the pin of a refused call. `DISCLOSE` (a refusal that hands the stranger the lease's current secret and changes nothing else) is **not** seen by R2, by construction of what R2 compares; **R6** is the latch that catches it.
- **R3** holds because R2 and R6 refuse the call first: nobody holds the secret of an operator's lease (`seedUpstreams` discards the response), so a node cannot renew it at all. The sanity latch `sOwnRenew` (an accepted renewal of a lease with the operator's dial) is **unreachable** in the code's configuration: this is not a defect but it means R3 itself exercises nothing; the retention rule (a renewal with no dial keeps the lease's) is shown by **R3b** and by the combined mutant that also lets a stale secret be good again (`SECRETREUSE=1 INHERIT=0`). **R3b** is the live rule: a lease a node holds dials with `nodeDial` when `swarm.node_tls` is set and with nothing when it is not; `DIALLEAK=1` (a claim takes a non-zero dial that `node_tls` did not supply, the stand-in for a stale `nodeDial` that survived a rebuild) breaks the second half, which the first version of the latch could not see.
- **R4, R5, R7** hold. R4 and R5 guard reaping too early; R5 is judged by a ghost `gsince`, which counts from the last accepted registration and which no mutant touches (the first version judged by the lease's own `age`, so a regression that stops a renewal extending the lease, `RENEWNOAGE`, corrupted the variable the latch read and went unseen). **R7** guards reaping that is missing or late: after the reap that opens a registration no unpinned lease older than expiry + grace is still held. It exists because the first version reaped inside the clock's own step, so "held" always meant "held after the reap" and a regression that stops reaping (`NOREAPREG`) or reaps late (`LATE`, which at the saturated age is never) was invisible to every latch; `LATE` leaves R5 verified, as it should.
- **R6** holds: a fresh secret at every claim means a node cannot be accepted on a lease another node holds (the secret of an earlier claim is refused, and no secret is disclosed); `SECRETREUSE=1` and `DISCLOSE=1` break it.
- R1 and R4 are post-conditions of assignments made in the same atomic step: they cannot fail in the code's configuration and discriminate only through their mutants. R6 is not of that kind: it follows from `gen` handing out a unique secret at every claim and from a refusal changing no secret. They are cheap and explicit, and are kept as regression guards.
- Every sanity latch but `sOwnRenew` is reachable, so the other verdicts are not vacuous in that sense; each mutant violates the latch it is written for, except `DISCLOSE`, whose latch is R6 (not R2).

## Not modelled

One name; a tunnel lease (its lifecycle is the net `p5-lease-net`, in `p5-nets.md`); the egress policy and a route that cannot be built (a heartbeat that cannot build its route leaves the lease as it was, apart from the reap that opens every call, so R1 to R3 are about accepted registrations); a registration is atomic because the code holds the registry's lock through it, and an owner's two simultaneous calls are two serial ones; the secret a node presents is read when its step runs, not captured when it sent the request (a node whose request carries a stale secret is refused in the code, while the model accepts the node's current secret: a superset of the reachable states, argued and not run to completion, the captured-secret variant with two processes per node being beyond the budget); the lease secret is a symbol, not a 256-bit value (its guessing is not an interleaving question); `Generation`, labels and URLs; `Pin` of an existing lease (its only caller, `seedUpstreams`, runs inside `New()` on an empty registry, and a name collision is a startup error); an operator who seeds again after a delete or a reap (the model allows it, which is more permissive than the code, in the safe direction); the age of a pinned lease on a renewal (the code moves `expiresAt` unconditionally; reachable only through `SECRETREUSE`, and no property reads a pinned lease's age). "Held" means held after the reap that opens a call.

## Found while modelling

Nothing in the code. The model forced two decisions into the open: the dial a renewal keeps is the lease's when it has one and `nodeDial` only when it has none, so a lease created while `nodeDial` was set keeps that value for its life (a change of the `swarm` block, `node_tls` included, rebuilds the relay and with it the registry: `swarmFingerprint` in `cmd/coddy/serve.go`, and `SetNodeDial` runs in `New()` before any registration, so a registry never outlives its `nodeDial` and the branch "no dial of its own while `nodeDial` is set" is dead code, which the report does not rely on); and R3 is held by the secret check, with the retention rule as a second line.

## Review

**Round 1** (two reviewers answered, a third timed out twice; every claim re-run). Verdict: approve with changes; no default property turned violated. Changed: the takeover latch now compares the secret, owner and pin as well (`STRANGER2`); R5 is judged by an independent ghost (`RENEWNOAGE`); the `NDIAL=0` row of R3b could not fail and now can (`DIALLEAK`); the claim that no verdict is vacuous is corrected for R3 (`sOwnRenew`); the quoted flags now include the memory budget and `-por`; the temporaries of one registration are cleared at its end, which takes the default from 10.5 M to 1.1 M states and leaves every verdict unchanged. Rejected with evidence: that an eager reap hides "R2 needs the secret on a held name", that the operator's register and `Pin` are two lock sections a node could slip between (`seedUpstreams` runs in `New()` before the handler exists), and that an authorised renewal of the operator's lease exists (nothing holds its secret).

**Round 2** (all three reviewers answered, one on a retry; all 26 verdicts and counts of the first fixed report reproduced). Verdict: approve with changes; no verdict flipped. Changed: the clock no longer reaps inside its own step: reaping runs at the start of every registration and in a process of its own, and R7 with `NOREAPREG` and `LATE` guards a reap that is missing or late, which R4 and R5 could not see (the hole was shown by making the line one larger: every latch stayed verified); the commands carry the flags they need (as written they were inconclusive at the default budget); the grace is the code's (`grace == ttl`), with the verdicts shown to hold at 2 as well; `DISCLOSE` and `CLAIMNOTOKEN` give R6 and R1 the mutants they lacked, and the report says R2 cannot see a disclosure; R3 is marked vacuous in the commands, the post-condition sentence is corrected for R6, and `DIALLEAK` is relabelled. Rejected with evidence: that deadlock is reachable (the clock loop is always enabled and every run reports it verified), that a failed route leaves the lease as it was although a reap ran first (the model's reap before a registration step produces the same state), and that `mysecret[who]` read at execution time misses a state (the model's reachable set is a superset, see Not modelled).
