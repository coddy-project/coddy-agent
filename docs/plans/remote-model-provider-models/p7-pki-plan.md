# p7-pki-plan: `coddy tls ensure` under concurrency, deaths and a changing world

Model `p7-pki-plan.pml` (Promela). The installer, an update, a service start and an operator run `ensure` at once; each may die before any write (the operating system releases its lock); the world moves under them (the CA reaches its end, the leaves end, a name is wanted, the CA key is removed by an operator); a reader loads the server pair at any moment; a recovery run follows. Engine `mcd` 0.3.1, runs on `relay-huron` (32 cores) through the guarded runner (an admission at a time while the host is under 60% CPU), the same binary as the workstation's. Code: `internal/pki/plan.go` (`Plan`, a pure function from the parsed state on disk and what is wanted to the steps), `internal/pki/store.go` (`Ensure` / `applyLocked`: an exclusive file lock, up to four passes of load, plan, apply in the order CA, server, client, bundle, a forced renewal acting once, every file written to a temporary name and renamed, a key before its certificate), `internal/netx/clientcert.go` (the loader refuses a pair whose halves differ: `p5-certloader`).

**World.** Files are generations (a key and a certificate written as one pair carry the same one; a leaf also carries the generation of the CA that signed it); a new generation is the smallest number nothing holds, so equal histories give equal states. Facts the world sets: `caExpired`, `srvEnding`, `cliEnding` (expired or within the renewal window), `nameMissing`; each is cleared by the write that cures it. `Ensure` runs take the lock, then loop: one atomic snapshot and plan; an empty plan ends the run (and is checked); otherwise the steps are applied, each write a step of its own with a death possible before it, at most `MAXP` = 4 passes; `F`/`R` (force the CA, renew the leaves) act on the first non-empty pass only. `Recover` is one more run, started when every other run has ended or died, that cannot die. `Env` makes each enabled event at most once, at any time. `Reader` loads the certificate file, then the key file, and serves only when the generations agree. Abstractions (what the model is not): file contents are generations, not bytes; the snapshot of `Load` is one atomic step (only an operator's removal of a file could interleave: the replanning loop repairs it, and the Go test `TestACrashAtAnyWriteLeavesARepairableState` holds the writer); `trusted/*.crt` and the names are not modelled; the temporary file of a write is not a step (a rename is atomic: a death before it leaves the file as it was); time is the facts above, not a clock.

**Latches** (ghost bits, one asserted per run with `-D ASSERT_PROP=<latch>`, expected to stay 0). K1 `gOrphan`: a certificate file exists whose key file does not (a CA key removed by an operator is the world's doing and is not counted; the flag is set in one step with the world's removal and cleared in one step with the writer's key write). K2 `gConverge`: a run's plan is empty while the directory is not right (a certificate missing, a key that does not match, a leaf not signed by the present CA, a bundle that is not the present CA's, a fact left standing). K3 `gCAReplaced`: the CA was replaced although the present one was valid, its key present, and nobody forced it (judged at the write, not at the plan). K4 `gNotEmpty`: a run left the loop with a non-empty plan after four passes (the code's "this is a bug" error). K5 `gNotIdem`: a run that was asked for nothing, started after a run that found everything right with nothing moved since, found work to do. K6 `gLockExcl`: two runs inside the lock at once. K7 `gLockStuck`: the lock is held by a dead process. K8 `gServed`: the reader served a pair whose halves differ. K9 `gRecover`: the recovery run ended with the directory not right.

Groups of runs, each exhaustive (`deadlock` is verified in every default run):

- **code, one event** (`small`): two runs, one death, each of the four events alone, three inputs (I1 a forced run, I2 a renewing run, I3 a forced and a renewing run), every latch; and the reader group (the reader with a leaf ending, so a reissue happens under it).
- **code, two events and two deaths** (`mid`): every pair of events with one death; every single event with two deaths.
- **code, three runs, three and four events** (`heavy`): three runs with one death and one event; every triple of events and all four together with two runs.
- **mutants**: each one rule changed, against every latch, in the smallest world where it bites.
- **sanity**: each reachability marker in the smallest world that reaches it.

Results on the code (every row is exhaustive; the last column is the built-in deadlock verdict):

| world (`NP` runs, `CR` deaths, event(s), input) | latches verified | violated | undecided (budget) | most states | deadlock |
|---|---|---|---|---|---|
| NP2 CR1 EVCA I1 | 9/9 | - | - | 432,431 | verified |
| NP2 CR1 EVCA I2 | 9/9 | - | - | 388,575 | verified |
| NP2 CR1 EVCA I3 | 9/9 | - | - | 501,535 | verified |
| NP2 CR1 EVLEAF I1 | 9/9 | - | - | 387,935 | verified |
| NP2 CR1 EVLEAF I2 | 9/9 | - | - | 303,431 | verified |
| NP2 CR1 EVLEAF I3 | 9/9 | - | - | 444,407 | verified |
| NP2 CR1 EVNAME I1 | 9/9 | - | - | 282,075 | verified |
| NP2 CR1 EVNAME I2 | 9/9 | - | - | 218,227 | verified |
| NP2 CR1 EVNAME I3 | 9/9 | - | - | 318,451 | verified |
| NP2 CR1 EVKEY I1 | 9/9 | - | - | 454,627 | verified |
| NP2 CR1 EVKEY I2 | 9/9 | - | - | 397,371 | verified |
| NP2 CR1 EVKEY I3 | 9/9 | - | - | 528,507 | verified |
| reader NP1 CR1 EVLEAF | 2/9 | - | - | 363,948 | verified |
| reader NP2 CR1 EVLEAF | 2/9 | - | - | 7,494,059 | verified |
| NP2 CR1 EVCA+EVLEAF I1 | 9/9 | - | - | 3,499,055 | verified |
| NP2 CR1 EVCA+EVNAME I1 | 9/9 | - | - | 2,441,691 | verified |
| NP2 CR1 EVCA+EVKEY I1 | 9/9 | - | - | 3,462,511 | verified |
| NP2 CR1 EVLEAF+EVNAME I1 | 9/9 | - | - | 2,375,747 | verified |
| NP2 CR1 EVLEAF+EVKEY I1 | 9/9 | - | - | 3,821,623 | verified |
| NP2 CR1 EVNAME+EVKEY I1 | 9/9 | - | - | 2,597,775 | verified |
| NP2 CR2 EVCA I1 | 9/9 | - | - | 4,923,343 | verified |
| NP2 CR2 EVLEAF I1 | 9/9 | - | - | 3,959,711 | verified |
| NP2 CR2 EVNAME I1 | 9/9 | - | - | 2,899,163 | verified |
| NP2 CR2 EVKEY I1 | 9/9 | - | - | 4,795,011 | verified |
| NP3 CR1 EVCA I1 | 9/9 | - | - | 5,270,653 | verified |
| NP3 CR1 EVLEAF I1 | 9/9 | - | - | 4,720,573 | verified |
| NP3 CR1 EVNAME I1 | 9/9 | - | - | 3,423,955 | verified |
| NP3 CR1 EVKEY I1 | 9/9 | - | - | 5,519,247 | verified |
| NP2 CR1 EVCA+EVLEAF+EVNAME I1 | 7/9 | K4, K9 | - | 22,821,323 | verified |
| NP2 CR1 EVCA+EVLEAF+EVKEY I1 | 0/9 | K4, K9 | K1, K2, K3, K5, K6, K7, K8 | 30,000,000 | inconclusive |
| NP2 CR1 EVCA+EVNAME+EVKEY I1 | 7/9 | K4, K9 | - | 21,920,223 | verified |
| NP2 CR1 EVLEAF+EVNAME+EVKEY I1 | 7/9 | K4, K9 | - | 25,110,143 | verified |
| NP2 CR1 all4 I1 | 0/9 | K4, K9 | K1, K2, K3, K5, K6, K7, K8 | 30,000,000 | inconclusive |

Mutants (a latch listed as violated is a counterexample found; "undecided" means the run did not finish inside its budget, which is not a verdict):

| mutant | what it changes | violated | undecided |
|---|---|---|---|
| `KEYFIRST=0` | a certificate is written before its key | K1 | - |
| `LOCK=0` | no lock | K3, K4, K6 | K1, K2, K5, K7, K8, K9 |
| `LOCKREL=0` | a death does not release the lock | K7 | - (the built-in deadlock verdict too: every other run waits for the lock for ever) |
| `CAKEEP=0` | a valid CA is replaced whenever a leaf is to be issued again | K3, K4, K9 | - |
| `LEAFNEWCA=0` | a new CA does not make the leaves be issued again by itself (the signer check still does) | K4, K9 | - |
| `LEAFNEWCA=0 SIGCHECK=0` | a new CA does not reissue the leaves and a leaf not signed by the present CA is not noticed | K2, K9 | - |
| `SIGCHECK=0` | a leaf not signed by the present CA is not noticed (only a new CA reissues) | K2, K9 | - |
| `BUNDLEEQ=0` | the bundle is rebuilt only when it is missing | K2, K9 | - |
| `FORCEONCE=0` | a forced renewal acts on every pass | K4 | - |
| `NOCHECK=1` | the reader serves a pair whose halves differ | K8 | - |

Sanity (reachable when violated):

| marker | result |
|---|---|
| `sCrash` | violated (4,461 states) |
| `sNewCA` | violated (267 states) |
| `sLeaf` | violated (267 states) |
| `sRepair` | violated (38,603 states) |
| `sWaited` | violated (2,695 states) |
| `sServed` | violated (2,161 states) |
| `sErr` | violated (18,891 states) |

## What the runs found

- **Two latches were wrong in the first draft, not the code.** K5 (`gNotIdem`) fired on a run that had been asked to force the CA: a forced renewal is work, so the latch now exempts a run with an input. K1 (`gOrphan`) fired when an operator removed `ca.key`: a missing key that the world made is not a writer's orphan, so the flag is set in one step with the removal and cleared in one step with the writer's key write.
- **K4 and K9 are bounded by the number of world changes in one run, and the bound is real.** Every pair of events verifies all nine latches. Three events at once violate K4 and K9 on the code: the counterexample lands one change in each pass, and the empty-plan pass that ends a run needs one more than `maxPasses` = 4 allows, so `applyLocked` returns its error ("the plan is not empty after 4 passes") and the recovery model's loop runs out. The consequence is an error, not damage: the next run repairs. In the real world a name wanted or a clock moving cannot change under a run, whose `Want` is fixed and which takes milliseconds, so only a removed key can: the bound is not a defect, and `maxPasses` stays at four. Every other latch (K1 to K3, K5 to K8) holds in the runs that finished with three and with four events; a run that did not finish inside its budget is marked undecided in the table, never counted as verified.
- **Each rule of the code has a mutant that breaks the latch written for it**: key before certificate (K1), the lock (K6), the lock's release on death (K7 and the built-in deadlock verdict), a valid CA kept (K3), a new CA reissuing the leaves and the signer check (K2, K9), the bundle compared (K2, K9), a forced renewal that acts once (K4), the reader's check (K8). `LEAFNEWCA=0` alone is not caught by K2: the signer check repairs it one pass later, which costs a pass (K4 on a world with two events) and is what `SIGCHECK=0` removes. With `LOCK=0` the space is too large to decide most latches inside the budget; the ones that matter are violated (K3, K4, K6) and the rest are marked undecided.
