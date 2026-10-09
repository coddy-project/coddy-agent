# p5-certloader: the client certificate loader

Model `p5-certloader.pml` (Promela; a pair of files, a rotator that replaces them, and NC concurrent handshakes whose `get()` reads each file as a step of its own outside the lock and compares, parses and publishes in one critical section). Engine `mcd` 0.3.1. Code: `internal/netx/clientcert.go` (`certLoader.get`, `ClientTLS.Key`). **It found a defect in the first loader** (stage H1), which cached the pair by size and modification time: a pair replaced with files of the same size and the same time was served from the cache. The fix (both files read at every handshake, parsed again only when their bytes changed, `Key()` over the content) is committed with its tests; the model's default is the new code, `CONTENT=0` the old one.

Commands (`M=p5-certloader.pml`; the default budget of `mcd` is enough for every run but the last two of the three-handshake group, which need `--budget-mem-mb 3000`; every run is exhaustive and `deadlock` is verified in every run):

```
# the code (CONTENT=1): every latch holds. ROT 0 replaces both files (ORDER 0 certificate first, 1 key first, 2 an atomic swap), ROT 1 only the certificate with the key reused; NC concurrent handshakes
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ROT=0 -D ORDER=0 -D NC=1 -D ASSERT_PROP=$l; done
    # all verified (464 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ROT=0 -D ORDER=0 -D NC=2 -D ASSERT_PROP=$l; done
    # all verified (74,769 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ROT=0 -D ORDER=1 -D NC=1 -D ASSERT_PROP=$l; done
    # all verified (445 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ROT=0 -D ORDER=1 -D NC=2 -D ASSERT_PROP=$l; done
    # all verified (67,916 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ROT=0 -D ORDER=2 -D NC=1 -D ASSERT_PROP=$l; done
    # all verified (193 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ROT=0 -D ORDER=2 -D NC=2 -D ASSERT_PROP=$l; done
    # all verified (23,763 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ROT=1 -D ORDER=0 -D NC=1 -D ASSERT_PROP=$l; done
    # all verified (135 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ROT=1 -D ORDER=0 -D NC=2 -D ASSERT_PROP=$l; done
    # all verified (12,658 states)
# three handshakes, two each (the hit and the parse of each can interleave with the other two)
mcd check --promela $M --no-timing -D ROT=0 -D ORDER=2 -D NC=3 -D NH=2 --budget-mem-mb 3000 -D ASSERT_PROP=gStale
    # verified (296,397 states)
mcd check --promela $M --no-timing -D ROT=1 -D ORDER=0 -D NC=3 -D NH=2 --budget-mem-mb 3000 -D ASSERT_PROP=gStale
    # verified (144,707 states)
mcd check --promela $M --no-timing -D ROT=0 -D ORDER=0 -D NC=3 -D NH=2 --budget-mem-mb 3000 -D ASSERT_PROP=gStale
    # verified (1,280,524 states)
mcd check --promela $M --no-timing -D ROT=0 -D ORDER=1 -D NC=3 -D NH=2 --budget-mem-mb 3000 -D ASSERT_PROP=gStale
    # verified (1,058,073 states)
# the first loader (CONTENT=0, keyed by the stamps): STAMP 0 the tick, 1 half the tick (a coarse file system), 2 a constant (a normalised time)
for l in gMismatch gStale gNoRecover; do mcd check --promela $M --no-timing -D CONTENT=0 -D STAMP=0 -D ORDER=0 -D ASSERT_PROP=$l; done
    # all verified (35,659 states)
for l in gMismatch gStale gNoRecover; do mcd check --promela $M --no-timing -D CONTENT=0 -D STAMP=0 -D ORDER=1 -D ASSERT_PROP=$l; done
    # all verified (35,953 states)
for l in gMismatch gStale gNoRecover; do mcd check --promela $M --no-timing -D CONTENT=0 -D STAMP=0 -D ORDER=2 -D ASSERT_PROP=$l; done
    # all verified (4,545 states)
for l in gMismatch gStale gNoRecover; do mcd check --promela $M --no-timing -D CONTENT=0 -D STAMP=1 -D ORDER=0 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale violated, gNoRecover verified (16,609 states)
for l in gMismatch gStale gNoRecover; do mcd check --promela $M --no-timing -D CONTENT=0 -D STAMP=1 -D ORDER=1 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale violated, gNoRecover verified (16,573 states)
for l in gMismatch gStale gNoRecover; do mcd check --promela $M --no-timing -D CONTENT=0 -D STAMP=1 -D ORDER=2 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale violated, gNoRecover verified (3,347 states)
for l in gMismatch gStale gNoRecover; do mcd check --promela $M --no-timing -D CONTENT=0 -D STAMP=2 -D ORDER=0 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale violated, gNoRecover verified (3,471 states)
for l in gMismatch gStale gNoRecover; do mcd check --promela $M --no-timing -D CONTENT=0 -D STAMP=2 -D ORDER=1 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale violated, gNoRecover verified (3,423 states)
for l in gMismatch gStale gNoRecover; do mcd check --promela $M --no-timing -D CONTENT=0 -D STAMP=2 -D ORDER=2 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale violated, gNoRecover verified (1,547 states)
# mutants of the code: a pair whose halves do not match is served (NOCHECK)
mcd check --promela $M --no-timing -D NOCHECK=1 -D ORDER=0 -D ASSERT_PROP=gMismatch
    # violated (451 states)
mcd check --promela $M --no-timing -D NOCHECK=1 -D ORDER=1 -D ASSERT_PROP=gMismatch
    # violated (425 states)
mcd check --promela $M --no-timing -D NOCHECK=1 -D ORDER=2 -D ASSERT_PROP=gMismatch
    # violated (193 states)
# the hit compares the certificate only (CMP=1), the key only (CMP=2), nothing (CMP=3)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=1 -D ROT=0 -D ORDER=0 -D ASSERT_PROP=$l; done
    # gStale verified, gMismatch verified, gMasked violated (482 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=1 -D ROT=0 -D ORDER=1 -D ASSERT_PROP=$l; done
    # gStale verified, gMismatch verified, gMasked violated (431 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=1 -D ROT=0 -D ORDER=2 -D ASSERT_PROP=$l; done
    # gStale verified, gMismatch verified, gMasked violated (199 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=1 -D ROT=1 -D ORDER=0 -D ASSERT_PROP=$l; done
    # all verified (135 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=2 -D ROT=0 -D ORDER=0 -D ASSERT_PROP=$l; done
    # gStale verified, gMismatch verified, gMasked violated (456 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=2 -D ROT=0 -D ORDER=1 -D ASSERT_PROP=$l; done
    # all verified (445 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=2 -D ROT=0 -D ORDER=2 -D ASSERT_PROP=$l; done
    # all verified (193 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=2 -D ROT=1 -D ORDER=0 -D ASSERT_PROP=$l; done
    # gStale violated, gMismatch verified, gMasked verified (152 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=3 -D ROT=0 -D ORDER=0 -D ASSERT_PROP=$l; done
    # gStale violated, gMismatch verified, gMasked violated (472 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=3 -D ROT=0 -D ORDER=1 -D ASSERT_PROP=$l; done
    # gStale violated, gMismatch verified, gMasked violated (452 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=3 -D ROT=0 -D ORDER=2 -D ASSERT_PROP=$l; done
    # gStale violated, gMismatch verified, gMasked violated (218 states)
for l in gStale gMismatch gMasked; do mcd check --promela $M --no-timing -D CMP=3 -D ROT=1 -D ORDER=0 -D ASSERT_PROP=$l; done
    # gStale violated, gMismatch verified, gMasked verified (152 states)
# the bytes are cached before the parse (ERRBYTES): after a failed parse the next handshake over the same bad bytes is served the old pair
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ERRBYTES=1 -D ORDER=0 -D NC=1 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale verified, gNoRecover verified, gMasked violated (478 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ERRBYTES=1 -D ORDER=1 -D NC=1 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale verified, gNoRecover verified, gMasked violated (454 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ERRBYTES=1 -D ORDER=2 -D NC=1 -D ASSERT_PROP=$l; done
    # all verified (193 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ERRBYTES=1 -D ORDER=0 -D NC=2 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale verified, gNoRecover verified, gMasked violated (124,954 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ERRBYTES=1 -D ORDER=1 -D NC=2 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale verified, gNoRecover verified, gMasked violated (98,400 states)
for l in gMismatch gStale gNoRecover gMasked; do mcd check --promela $M --no-timing -D ERRBYTES=1 -D ORDER=2 -D NC=2 -D ASSERT_PROP=$l; done
    # gMismatch verified, gStale verified, gNoRecover verified, gMasked violated (28,646 states)
# a failure remembered: under the same key as a good pair (ERRCACHE=1), and whatever the bytes are (ERRCACHE=2, a sticky error)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D ROT=0 -D ORDER=0 -D NC=1 -D ASSERT_PROP=gNoRecover
    # verified (523 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D ROT=0 -D ORDER=1 -D NC=1 -D ASSERT_PROP=gNoRecover
    # verified (471 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D ROT=1 -D ORDER=0 -D NC=1 -D ASSERT_PROP=gNoRecover
    # verified (135 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D ROT=0 -D ORDER=0 -D NC=2 -D ASSERT_PROP=gNoRecover
    # verified (103,833 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D ROT=0 -D ORDER=1 -D NC=2 -D ASSERT_PROP=gNoRecover
    # verified (80,593 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D ROT=0 -D ORDER=2 -D NC=2 -D ASSERT_PROP=gNoRecover
    # verified (24,825 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D CONTENT=0 -D STAMP=0 -D ORDER=0 -D ASSERT_PROP=gNoRecover
    # verified (39,593 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D CONTENT=0 -D STAMP=0 -D ORDER=1 -D ASSERT_PROP=gNoRecover
    # verified (37,493 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D CONTENT=0 -D STAMP=1 -D ORDER=0 -D ASSERT_PROP=gNoRecover
    # violated (17,171 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D CONTENT=0 -D STAMP=1 -D ORDER=1 -D ASSERT_PROP=gNoRecover
    # violated (15,981 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D CONTENT=0 -D STAMP=2 -D ORDER=0 -D ASSERT_PROP=gNoRecover
    # violated (3,159 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D CONTENT=0 -D STAMP=2 -D ORDER=1 -D ASSERT_PROP=gNoRecover
    # violated (2,905 states)
mcd check --promela $M --no-timing -D ERRCACHE=2 -D ROT=0 -D ORDER=0 -D ASSERT_PROP=gNoRecover
    # violated (453 states)
mcd check --promela $M --no-timing -D ERRCACHE=2 -D ROT=0 -D ORDER=1 -D ASSERT_PROP=gNoRecover
    # violated (395 states)
mcd check --promela $M --no-timing -D ERRCACHE=2 -D ROT=0 -D ORDER=2 -D ASSERT_PROP=gNoRecover
    # violated (193 states)
mcd check --promela $M --no-timing -D ERRCACHE=2 -D ROT=1 -D ORDER=0 -D ASSERT_PROP=gNoRecover
    # verified (135 states)
# sanity: reachable when violated
mcd check --promela $M --no-timing -D ASSERT_PROP=sMiss
    # violated (464 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sParse
    # violated (464 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sErr
    # violated (464 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sHit
    # violated (464 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sNew
    # violated (464 states)
mcd check --promela $M --no-timing -D ERRCACHE=1 -D ORDER=0 -D ASSERT_PROP=sErrHit
    # violated (523 states), reachable
mcd check --promela $M --no-timing -D ERRCACHE=1 -D ORDER=2 -D ASSERT_PROP=sErrHit
    # verified (193 states), not reachable with one handshake: the split pair (certificate 1, key 2) cannot be read twice
mcd check --promela $M --no-timing -D ERRCACHE=1 -D ORDER=2 -D NC=2 -D ASSERT_PROP=sErrHit
    # violated (24,825 states), reachable: the swap is atomic, the two reads are not
mcd check --promela $M --no-timing -D NC=2 -D ASSERT_PROP=sRegress
    # violated (74,769 states), reachable
mcd check --promela $M --no-timing -D NC=1 -D ASSERT_PROP=sRegress
    # verified (464 states), not reachable with one handshake
```

Legend: `STAMP` 0 the tick (precise), 1 half the tick (a coarse file system), 2 a constant (a normalised time: a reproducible archive, a layer, a store that sets every file to the epoch); `ORDER` 0 certificate then key, 1 key then certificate, 2 both in one step (an atomic directory swap); `ROT` 1 renews only the certificate, the key reused.

## Verdicts

- **L1 gMismatch** (a handshake is served a certificate and a key that do not match) holds for the code and for the first loader, because `tls.X509KeyPair` refuses a pair that does not match: under `NOCHECK` 0 the branch that serves a pair is reached only when the halves match, so L1 holds by construction. The `NOCHECK` mutant violates it for every order.
- **L2 gStale** (after the rotation finished, a handshake that started after it is served the old certificate) holds for the code for both kinds of rotation and for one, two and three handshakes in flight. It is checked on the hit branch as well as on a parse: the first version of the model set the latch on the parse only, which a hit branch that serves any cached pair (`CMP=3`) never reaches, so its "verified" proved nothing for the hit. Now `CMP=3` violates it in every configuration, a comparison of the **key only** (`CMP=2`) violates it exactly when the certificate is renewed under the same key (`ROT=1`), and a comparison of the certificate only (`CMP=1`) holds **for valid rotations**, because a key change then implies a certificate change. The first loader violates L2 for `STAMP` 1 and 2 (6 of 9 combinations) and holds for the precise stamp: the defect found. The code never reads a stamp, so for the code L2 is stamp-independent by construction and the model carries none. Under `CMP=0` L2 also holds by construction (the cached pair is the parse of the cached bytes, and a read returns the current content); the compare mutants are what show it can fail.
- **L3 gNoRecover** (after the rotation, a handshake over files that match is refused) cannot fail while a failure caches nothing, and the code caches none: it holds by construction. `ERRCACHE=1` (a failure remembered under the same key as a good pair) is a mutant, not a behaviour of the code, and over the code's byte keys it is **safe and no latch can catch it**: a failure remembered under its bytes is never served for bytes that match, because `tls.X509KeyPair` is a pure function of its bytes. Under the stamps of the first loader it is violated for the coarse and constant stamps. `ERRCACHE=2` (a sticky failure) violates L3 in every rotation that can show a mismatch; with `ROT=1` no pair ever mismatches, so there is no failure to remember and it is not caught: a vacuity of the scenario, not of the property.
- **L4 gMasked** (a handshake over files that do not match is served a cached pair instead of an error) holds for the code. It is the latch for what L1 to L3 cannot see: `CMP=1` (the certificate only compared) violates it for every order of a two-file rotation, `CMP=2` and `CMP=3` for the orders that show a mismatch, and `ERRBYTES` (the bytes cached before the parse: a failed parse leaves the new bytes next to the old pair) for orders 0 and 1 with one handshake and for every order with two. `CMP=1` holds L2 only for valid rotations; an operator who replaces the key alone with an unrelated one has the error hidden by a loader that compares the certificate only, and the two Go tests below hold exactly that.
- **Concurrency.** The reads are outside the lock in the code. With two or three handshakes a slow one can publish the old files over the new pair (`sRegress` is reachable with two): the next handshake parses again, so the cost is one more parse, and L1 to L4 hold.
- All sanity latches are reachable where they should be. The selection of the model's `Client` is exclusive where the code's is (the hit, the remembered failure and the mismatch are mutually exclusive branches), so the model does not contain an error outcome that the code cannot produce.

## Not modelled

One leg; the files hold a version number, not bytes; one rotation, 1 to 2 (the comparison depends only on content, so a second rotation or an ABA is symmetric); a failed read (a missing file, a torn write in place, a permission) is not a step, since a failure caches nothing and the next handshake reads again (held in Go by `TestClientCertificateRotatesAndFailsOnlyTheHandshake`); a read returns the current content of the file (a network mount whose attribute or read cache lags a write is outside the model); a resumed TLS session does not call the loader (the outbound legs set no session cache); the first loader read the pair inside `LoadX509KeyPair` under its lock and the model reads outside it for both loaders, which cannot affect the `CONTENT=0` runs (one handshake); `ClientTLS.Key()`, which reads the CA, the certificate and the key one after another outside the loader and decides only when a new transport is built: a half-rotated read can yield up to two further keys per rotation (an old certificate with a new key and the reverse), one idle transport each, kept for the life of the process; the server pair and `client_ca_file`, which are startup state, and `ca_file` of a dialling side, which is read when a transport is built.

## Found while modelling

**The stale pair (fixed).** With `CONTENT=0` and a stamp that does not change (`STAMP` 1 or 2), L2 is violated with a short trace: the pair is cached, the rotator replaces both files within one stamp, the next handshake serves the old certificate. The fix is in `internal/netx/clientcert.go`, with its tests written first (`TestClientCertificateReplacedWithTheSameSizeAndTimeIsPickedUp`, `TestClientTLSKeyFollowsTheContentNotJustSizeAndTime`) and the wording of `docs/operate/certificates.md`, `swarm.md`, `shared-models.md` and `AGENTS.md` follows it.

**Regression tests the review asked for.** The kind of renewal that keeps the key (`ROT=1`) was not in the model, and a loader that compared the key alone passed every test of the package; `TestClientCertificateRenewedWithTheSameKeyIsPickedUp` now covers it. The second round found that a loader that compared the certificate only, or that cached the bytes before parsing them, also passed every test: `TestClientCertificateThatStopsMatchingIsRefusedEveryTime` holds both (a key replaced alone by an unrelated key, and a certificate replaced alone, must be an error at every handshake, never the previous pair). Both tests pass on the code. Each mutation was applied to `clientcert.go` and the file restored: a comparison of the key only fails the renewal test and the certificate-alone case of the second; a comparison of the certificate only fails the key-alone case of the second and nothing else; a loader that serves any cached pair fails five tests (the second in both cases); the bytes cached before the parse fail the second in both cases and nothing else.

## Review

**Round 1** (two reviewers answered, a third timed out twice; every claim re-run, all 43 configurations of the first report reproduced). Verdict: approve with changes; no property turned violated for the code. Changed: the hit branch is instrumented for L2 and a mutant that serves any cached pair is added (the property was blind there); the rotator can renew the certificate alone and a key-only comparison is a mutant, with its Go test; a sticky failure is a mutant of the code's structure; the reads are outside the lock and a second handshake is modelled (`NC`); the stamps are carried only where the loader reads them; the unrunnable command lines and the "stat of the two files" and "key no later read shares" sentences are corrected; the assumption about network mounts is stated. Rejected with evidence: moving the sampling of `startRot` after the reads (the latch stays vacuous), removing the mismatch branch of the parse (it is the `NOCHECK` mutant), and the claim that sizes are not guaranteed equal (equal sizes are the worst case for the old loader).

**Round 2** (two reviewers answered, a third stalled twice on the first request; all 99 runs of the first fixed report reproduced by a script that parsed the commands). Verdict: approve with changes; no verdict flipped. Changed: two guards of the model's selection overlapped (a hit and a mismatch, a remembered failure and a mismatch), which let it produce an error outcome the code cannot: they are exclusive now, which changes 17 state counts and no verdict; the explanation that an atomic swap never shows a mismatch was false (the two reads are not atomic: `sErrHit` is reachable with two handshakes); `ERRCACHE=1` over the code is a mutant that no latch can catch, and L1, L2 under `CMP=0` and L3 hold by construction, which the report now says; a regression no latch and no test caught (a key replaced alone, bytes cached before the parse) has a latch `gMasked`, a mutant `ERRBYTES` and two Go tests; the `Key()` sentence, the commands (a shell redirect in `<latch>`, no `M`, no `--no-timing`) and the sizes of EC certificates are corrected; `sParse` is added because `sMiss` was set at every read; three handshakes are run. Rejected with evidence: that a sticky read error is uncovered (the Go test of the failed handshake catches it), that the first report had no raw engine output (every line reproduced), and the severity "high" of two findings that flip no verdict. Not worth fixing: the lock scope of the first loader (one sentence in Not modelled), torn reads that still parse (the bytes are compared), a second rotation, and the claim of "all ten tests" (the package had seven tests of the loader; the sentence is gone).
