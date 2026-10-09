# p7-nets: a Petri net for the lock of `coddy tls ensure`

Net in the JSON format of `mcd` 0.3.1 (`mcd check --petri <net>.json --no-timing -ctl <formula>...`), with five mutants beside it. A net holds no data (which file, which generation): it is the place for the question that is conservation and reachability of one resource, the directory lock. It **encodes**, as arcs, what `p7-pki-plan` and `internal/pki/store.go` assume of `platform.LockFile`: one holder at a time, and a lock that the operating system releases when its holder dies (`flock(2)` on Unix, `LockFileEx` on Windows: both are held by an open file description or handle, which dies with the process, and the file is opened close-on-exec so a child does not inherit it). That release is an axiom of the net (the arc `crash -> lockFree`), not something the net derives. On a platform with neither (`filelock_other.go`, a no-op lock), the real behaviour is the `nolock` mutant.

## p7-pki-lock-net

Code: `internal/pki/store.go` (`ensure`, `Trust` and `IssueClientFiles` take `dir/.lock` and `defer unlock()`; A and B below stand for any two of them, an installer, an update, a service start or an operator), `internal/platform` (`LockFile`). Places: `lockFree`; for each of two runs `idle`, `holds`, `done`, `dead`; `gone` (a run that has ended or died, either way); the recovery run `idleR`, `holdsR`, `doneR`; `dirty` (a run died while holding the lock: the state `p7-pki-plan` has to inspect and repair); `leakedDead` (a lock held by a dead process) and `leakedDone` (a lock a finished run did not return). Transitions: `acquire` takes `idle` and `lockFree`; `finish` returns the lock; `crash` returns the lock too and leaves `dirty`; `acquireR` takes `idleR`, the lock and **two tokens of `gone`**. The `gone` guard is the **scenario**, not a property of the lock: `ensure` waits for the lock and nothing else, so a recovery run could take it after one death and before the other run. The net models the run that follows the deaths, which is what `p7-pki-plan`'s `Recover` is.

- **N1** mutual exclusion: `AG (holdsA + holdsB + holdsR <= 1)`
- **N2** the lock is somewhere (conservation): `AG (lockFree + holdsA + holdsB + holdsR + leakedDead + leakedDone == 1)`
- **N3** no lock held by the dead: `AG (leakedDead == 0)`; **N3b** no lock a finished run did not return: `AG (leakedDone == 0)`
- **N4** the lock can always become free again (reachability): `AG EF (lockFree >= 1)`; the `AG AF` reading gives the same column in every variant and is N5's
- **N5** after a death (its antecedent is `dirty`, which every death while holding sets, whether or not it left a write half done), the recovery run runs to its end on every path: `AG (dirty >= 1 -> AF (doneR >= 1))`. The effect on the files (that the plan repairs what the death left) is `p7-pki-plan`'s, not this net's: `dirty` is never consumed here.
- **N6** the recovery run never overlaps another holder: `AG (holdsR >= 1 -> (holdsA + holdsB == 0))`, a corollary of N1 (the case of N1 for the recovery run against A and B: any variant that breaks N6 breaks N1), kept as the recovery run's own statement
- **N7** the recovery run starts only when the others have ended or died: `AG (holdsR >= 1 -> (idleA + idleB == 0))`, which checks the order the `gone` guard sets
- sanity: `EF (dirty >= 1)`, `EF (holdsA >= 1 && idleB >= 1)` (the interleaving N1 is about is reachable, so N1 is not vacuous; it is reachable in `nolock` too, so it does not by itself show that B waits: that is N1's job), `EF (doneR >= 1)`, `EF (deadA >= 1 && doneB >= 1 && doneR >= 1)` (A dies, B completes, the recovery completes): all reachable in the net; the last one is unreachable under `crashkeep`, which is how that mutant differs from `finishkeep`.

N1 and N2 are place invariants of the arcs, N3 and N3b hold because no arc of the net puts a token on a `leaked` place, N6 is a corollary of N1 and N7 follows from the `gone` guard (`mcd` itself flags the atoms of N1 to N3b as never false in the reachable states): all hold **by construction**, and the evidence that the formulas can fail, and mean something, is the mutant columns below. N4 and N5 hold because the net is acyclic and every holder fires `finish` or `crash`; `LockFile` blocks with no timeout, so a holder that hangs without dying holds the others off, and the net has no transition for it.

| variant | N1 | N2 | N3 | N3b | N4 | N5 | N6 | N7 | states |
|---|---|---|---|---|---|---|---|---|---|
| the net | ok | ok | ok | ok | ok | ok | ok | ok | 23 |
| `crashkeep` | ok | ok | **VIOLATED** | ok | **VIOLATED** | **VIOLATED** | ok | ok | 14 |
| `nolock` | **VIOLATED** | **VIOLATED** | ok | ok | ok | ok | ok | ok | 24 |
| `finishkeep` | ok | ok | ok | **VIOLATED** | **VIOLATED** | **VIOLATED** | ok | ok | 18 |
| `earlyrecover` | ok | ok | ok | ok | ok | ok | ok | **VIOLATED** | 39 |
| `recoverfree` | **VIOLATED** | **VIOLATED** | ok | ok | ok | ok | **VIOLATED** | **VIOLATED** | 45 |

The mutants, each as the arcs it changes: `crashkeep`: `crashA` and `crashB` put their token on `leakedDead` instead of `lockFree` (a lock that is not the operating system's: a lock file that a crashed process leaves behind, a PID file). `nolock`: acquire, finish and crash of A and B neither take nor return `lockFree` (no lock at all). `finishkeep`: `finishA` puts its token on `leakedDone` (a missing unlock). `earlyrecover`: `acquireR` does not take the two `gone` tokens. `recoverfree`: `acquireR` takes neither the lock nor `gone`, and `finishR` returns no lock (a recovery run that neither waits nor locks).

Each mutant is caught by the latch written for it, and `crashkeep` is told from `finishkeep` by N3 against N3b. Two observations the table makes: `earlyrecover` still satisfies N1 and N6, so a recovery that does not wait for the others is safe **because of the lock** (only N7 notices the order changed); and N6, being a corollary of N1, is killed only together with it, by `recoverfree`, which removes the lock as well as the barrier.

The built-in `deadlock` verdict is violated in every variant, by design: the net terminates (every run finishes or dies), and a terminal marking has no enabled transition (under `crashkeep` and `finishkeep` some terminal markings are real stuck waiters, which that verdict cannot tell from a normal end: N4 and N5 are what catch those mutants). The built-in `safe` verdict is violated too, by design: `gone` and `dirty` hold up to two tokens. Under `AF`, a terminal marking is the end of a path.

Commands (the nets are `p7-pki-lock-net.json` and `p7-pki-lock-net-mutant-<name>.json`):

```
for n in p7-pki-lock-net p7-pki-lock-net-mutant-crashkeep p7-pki-lock-net-mutant-nolock p7-pki-lock-net-mutant-finishkeep \
         p7-pki-lock-net-mutant-earlyrecover p7-pki-lock-net-mutant-recoverfree; do
  mcd check --petri $n.json --no-timing --budget-states 30000000 \
     -ctl 'AG (holdsA + holdsB + holdsR <= 1)' -ctl 'AG (lockFree + holdsA + holdsB + holdsR + leakedDead + leakedDone == 1)' \
     -ctl 'AG (leakedDead == 0)' -ctl 'AG (leakedDone == 0)' -ctl 'AG EF (lockFree >= 1)' -ctl 'AG (dirty >= 1 -> AF (doneR >= 1))' \
     -ctl 'AG (holdsR >= 1 -> (holdsA + holdsB == 0))' -ctl 'AG (holdsR >= 1 -> (idleA + idleB == 0))' \
     -ctl 'EF (dirty >= 1)' -ctl 'EF (holdsA >= 1 && idleB >= 1)' -ctl 'EF (doneR >= 1)' -ctl 'EF (deadA >= 1 && doneB >= 1 && doneR >= 1)'
done
```
