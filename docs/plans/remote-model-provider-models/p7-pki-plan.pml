/* p7-pki-plan.pml - the built-in certificate authority's `ensure` (phase 4b, T2): the installer, an update, a service start and an operator
   run it at once, each may die at any write, the world moves under it (a CA reaches its end, a leaf ends, a name is wanted, a key
   is lost), and a reader loads the server pair at any moment.

   Code     internal/pki/plan.go (Plan: a pure function from the parsed state on disk and what is wanted to the steps), store.go
            (Ensure / applyLocked: one exclusive file lock (platform.LockFile: flock / LockFileEx, released by the OS when the process
            dies); up to four passes of load, plan, apply, in the order CA, server, client, bundle; a forced renewal acts once; every file
            is written to a temporary name and renamed, a key BEFORE its certificate (writePair); a failed write returns an error and
            leaves what was written), internal/netx/clientcert.go (the loader: a pair whose halves differ is refused, p5-certloader).
   Class    finite, discrete: the processes, the environment and the reader interleave at the granularity of one file write or one read.

   World
     files   ca.crt and ca.key (generations: 0 absent), server.crt / server.key and client.crt / client.key (a generation each, and the
             generation of the CA the certificate was signed by), bundle.pem (the generation of the CA it carries). A key and a
             certificate that came from one write pair have the same generation: that is "the key matches". A generation is a fresh number
             of one global counter.
     facts   caExpired (the CA is past its end), srvEnding / cliEnding (a leaf is expired or within the renewal window), nameMissing (the
             server certificate lacks a wanted name). Each is cleared by the write that cures it (a new CA, a new leaf).
     Ensure  NP processes. Each takes the lock, then loops: load (one atomic snapshot), plan; an empty plan ends the run (and is checked);
             otherwise the steps are applied, each write a step of its own with a crash possible before it, and the loop goes on, at
             most MAXP passes. F (force the CA) and R (renew the leaves) act on the first non-empty pass only (FORCEONCE).
     Recover one more run, started when every other run has ended or died, that cannot die: "a following complete run repairs it".
     Env     the events EVCA, EVLEAF, EVNAME and EVKEY enable, each at most once, at any time: the CA reaches its end, the leaves end, a name is wanted, the CA key
             file is lost (an operator removes it).
     Reader  NR loads of the server pair: the certificate file, then the key file (two steps), served only when the generations agree.
     Crash   a process dies before any write, CR deaths in all; the OS releases its lock (LOCKREL).

   Options (the code is the default of each; the others are mutants)
     KEYFIRST 1 a key is written before its certificate (0: the certificate first)
     LOCK 1 the lock is taken (0: none)        LOCKREL 1 the lock is released when its holder dies (0: it stays held)
     CAKEEP 1 a valid CA is replaced only by a force, by its end or by a lost key (0: also whenever a leaf is to be issued again)
     LEAFNEWCA 1 a new CA makes both leaves be issued again (0: not)
     SIGCHECK 1 a leaf not signed by the present CA is issued again (0: only a new CA does it)
     BUNDLEEQ 1 the bundle is rebuilt when it is not the present CA's (0: only when it is missing)
     FORCEONCE 1 a forced renewal acts on the first pass only (0: on every pass)
     NOCHECK 0 the reader refuses a pair whose halves differ (1: serves it)
     REC 1 the recovery run is part of the world (0: it is left out, for the mutants that need a smaller space)
     NP, NR, CR, MAXP, the events EVCA (the CA ends), EVLEAF (the leaves end), EVNAME (a name is wanted), EVKEY (the CA key is lost) (1 on, 0 off), F0 R0 F1 R1 (the inputs of runs 0 and 1)
   Properties (ghost latches, one asserted per run with -D ASSERT_PROP=<latch>, expected to stay 0)
     K1 gOrphan      a certificate file exists whose key file does not (the CA's, the server's or the client's)
     K2 gConverge    a run's plan is empty while the directory is not right: a certificate missing, a key that does not match, a leaf not signed
                     by the present CA (a leaf left under a CA that is gone), a bundle that is not the present CA's, a fact left standing
     K3 gCAReplaced  the CA certificate was replaced although the present CA was valid and its key present, and nobody forced it
     K4 gNotEmpty    a run left the loop with a non-empty plan after MAXP passes (the code's "this is a bug" error)
     K5 gNotIdem     a run started after a run that found everything right, with nothing moved since, found work to do
     K6 gLockExcl    two runs were inside the lock at once
     K7 gLockStuck   the lock is held by a process that is dead
     K8 gServed      the reader served a pair whose halves differ
     K9 gRecover     the recovery run ended with the directory not right (it errors out only when the CA is unusable, which it repairs by
                     making a CA: so it never does)
     K4 and K9 are bounded by the world: each change that lands in a pass needs a pass to repair and the loop ends with an empty-plan
     pass, so with MAXP = 4 they hold when at most two world changes land in one run (every pair of events verifies) and are violated
     by three (every triple of events: the counterexample puts one change in each pass). The code's answer is the error 'the plan is not
     empty after 4 passes', and the next run repairs; in the real world a name wanted or a clock moving cannot change under a run, whose
     Want is fixed, so only a lost key can.
     sanity   sCrash (a run died), sNewCA (a CA was made), sLeaf (a leaf was issued), sRepair (a run repaired what a death left), sWaited (a run
              waited for the lock), sServed (the reader served a pair), sErr (a run ended in the error "the CA is unusable")

   Abstractions (what the model is not): file contents are generations, not bytes; the parse of a file is part of the snapshot, and a file that
     does not parse is a file that is absent; Load reads the files one after the other and the model takes them as one atomic snapshot (only an
     operator's removal of a file could interleave, and the plan then fails or repairs next pass: held by the Go test
     TestACrashAtAnyWriteLeavesARepairableState and the replanning loop); `trusted/*.crt` and the names are not modelled (the bundle holds the CA
     and the trusted ones: the rebuild is the same step); the temporary file of a write is not a step (a rename is atomic, a crash before it leaves
     the file as it was: that is what "a crash before the write" is); the lock is a mutual exclusion over the directory, taken in a blocking
     way; time is the facts above, not a clock.
*/

#ifndef KEYFIRST
#define KEYFIRST 1
#endif
#ifndef LOCK
#define LOCK 1
#endif
#ifndef LOCKREL
#define LOCKREL 1
#endif
#ifndef CAKEEP
#define CAKEEP 1
#endif
#ifndef LEAFNEWCA
#define LEAFNEWCA 1
#endif
#ifndef SIGCHECK
#define SIGCHECK 1
#endif
#ifndef BUNDLEEQ
#define BUNDLEEQ 1
#endif
#ifndef FORCEONCE
#define FORCEONCE 1
#endif
#ifndef NOCHECK
#define NOCHECK 0
#endif
#ifndef NP
#define NP 2
#endif
#ifndef REC
#define REC 1
#endif
#ifndef NR
#define NR 1
#endif
#ifndef CR
#define CR 1
#endif
#ifndef MAXP
#define MAXP 4
#endif
#ifndef EVCA
#define EVCA 1
#endif
#ifndef EVLEAF
#define EVLEAF 1
#endif
#ifndef EVNAME
#define EVNAME 1
#endif
#ifndef EVKEY
#define EVKEY 1
#endif
#ifndef F0
#define F0 0
#endif
#ifndef R0
#define R0 0
#endif
#ifndef F1
#define F1 0
#endif
#ifndef R1
#define R1 0
#endif

byte caV, caKeyV;                 /* ca.crt, ca.key: generation, 0 absent                         */
byte srvC, srvK, srvS;            /* server.crt, server.key, and the CA generation srvC was signed by */
byte cliC, cliK, cliS;
byte bundleV;                     /* the CA generation bundle.pem carries, 0 absent               */
bit caExpired, srvEnding, cliEnding, nameMissing;
bit caKeyLost;                    /* an operator removed ca.key: the missing key is the world's doing, not a writer's */
byte lcaA[4], snapA[4], wA[4];     /* what each run holds: the CA it signs with, the CA of its snapshot, the generation it is writing */
bit lockHeld; byte inCS; byte nw;
byte crashes;                     /* deaths so far                                                */
byte alive;                       /* Ensure runs started and not yet ended or dead                 */
byte deadHolder;                  /* the lock is held by a dead process                           */
bit quiet;                        /* a run found everything right and nothing has moved since       */

bit gOrphan, gConverge, gCAReplaced, gNotEmpty, gNotIdem, gLockExcl, gLockStuck, gServed, gRecover;
bit sCrash, sNewCA, sLeaf, sRepair, sWaited, sServed, sErr;
bit dirtyByCrash;                 /* a death left a write half done (for sRepair) */

inline latch() {
#ifdef ASSERT_PROP
    assert(ASSERT_PROP == 0);
#endif
    skip
}

/* the invariant checked after every write */
inline chk() {
    if
    :: (caV != 0 && caKeyV == 0 && !caKeyLost) || (srvC != 0 && srvK == 0) || (cliC != 0 && cliK == 0) -> gOrphan = 1
    :: else -> skip
    fi;
    if :: deadHolder != 0 && lockHeld -> gLockStuck = 1 :: else -> skip fi;
    latch()
}

/* the directory is right: every certificate has its key, every leaf is signed by the present CA, the bundle is the present CA's, and no
   fact stands */
#define LCA lcaA[id]
#define SNAP snapA[id]
#define W wA[id]
#define CONV ((caV != 0) && (caKeyV == caV) && (srvC != 0) && (srvK == srvC) && (srvS == caV) && (cliC != 0) && (cliK == cliC) && (cliS == caV) && (bundleV == caV) && !caExpired && !srvEnding && !cliEnding && !nameMissing)

/* a fresh generation: the smallest positive number no file and no run holds, so equal histories give equal states (a number that is
   reused only when nothing can still name it) */
inline fresh() {
    nw = 1;
    do
    :: nw < 20 && (nw == caV || nw == caKeyV || nw == srvC || nw == srvK || nw == srvS || nw == cliC || nw == cliK || nw == cliS || nw == bundleV
                   || nw == lcaA[0] || nw == lcaA[1] || nw == lcaA[2] || nw == lcaA[3] || nw == snapA[0] || nw == snapA[1] || nw == snapA[2] || nw == snapA[3]
                   || nw == wA[0] || nw == wA[1] || nw == wA[2] || nw == wA[3]) -> nw++
    :: else -> break
    od
}

/* the planner (plan.go: Plan) */
inline plan(f, r) {
    pCA = 0; pS = 0; pC = 0; pB = 0; newCA = 0; sR = 0; cR = 0;
    caKeyOK = (caV != 0 && caKeyV == caV);
    if :: caV == 0 -> pCA = 1; newCA = 1 :: else -> skip fi;
    if :: !newCA && !caKeyOK -> pCA = 1; newCA = 1 :: else -> skip fi;
    if :: !newCA && f -> pCA = 2; newCA = 1 :: else -> skip fi;
    if :: !newCA && caExpired -> pCA = 2; newCA = 1 :: else -> skip fi;
    /* the server leaf */
    if :: srvC == 0 || srvK != srvC -> sR = 1 :: else -> skip fi;
    if :: !sR && newCA && LEAFNEWCA == 1 -> sR = 1 :: else -> skip fi;
    if :: !sR && r -> sR = 1 :: else -> skip fi;
    if :: !sR && !newCA && SIGCHECK == 1 && srvS != caV -> sR = 1 :: else -> skip fi;
    if :: !sR && srvEnding -> sR = 1 :: else -> skip fi;
    if :: !sR && nameMissing -> sR = 1 :: else -> skip fi;
    /* the client leaf */
    if :: cliC == 0 || cliK != cliC -> cR = 1 :: else -> skip fi;
    if :: !cR && newCA && LEAFNEWCA == 1 -> cR = 1 :: else -> skip fi;
    if :: !cR && r -> cR = 1 :: else -> skip fi;
    if :: !cR && !newCA && SIGCHECK == 1 && cliS != caV -> cR = 1 :: else -> skip fi;
    if :: !cR && cliEnding -> cR = 1 :: else -> skip fi;
#if CAKEEP == 0
    /* a mutant: the CA is replaced whenever a leaf is to be issued again */
    if :: !newCA && (sR || cR) -> pCA = 2; newCA = 1 :: else -> skip fi;
#endif
    pS = sR; pC = cR;
#if BUNDLEEQ == 1
    pB = (newCA || bundleV != caV);
#else
    pB = (newCA || bundleV == 0);
#endif
    SNAP = caV
}

/* one file write: a crash may come first; the new value appears in one step */
inline crashpoint() {
    if
    :: crashes < CR -> crashes++; sCrash = 1; goto died
    :: true -> skip
    fi
}

proctype Ensure(byte id; bit f0; bit r0; bit crashable) {
    byte pass = 0;
    bit f = f0, r = r0, firstNonEmpty = 1;
    byte pCA; bit pS, pC, pB, newCA, sR, cR, caKeyOK, emptyPlan;
    byte legit;
#if LOCK == 1
    if :: lockHeld -> sWaited = 1 :: else -> skip fi;
    atomic { (lockHeld == 0) -> lockHeld = 1; inCS++; if :: inCS > 1 -> gLockExcl = 1 :: else -> skip fi };
#else
    atomic { lockHeld = 1; inCS++; if :: inCS > 1 -> gLockExcl = 1 :: else -> skip fi };
#endif
    latch();
    do
    :: pass < MAXP ->
        atomic {
            plan(f, r);
            emptyPlan = 0;
            if
            :: !pCA && !pS && !pC && !pB ->
                 /* an empty plan: the run ends, and the directory must be right */
                 if :: !CONV -> gConverge = 1 :: else -> skip fi;
                 quiet = 1; emptyPlan = 1;
                 latch()
            :: else ->
                 if :: pass == 0 && quiet && !f && !r -> gNotIdem = 1 :: else -> skip fi;
                 quiet = 0;
                 if :: dirtyByCrash -> sRepair = 1 :: else -> skip fi;
                 latch()
            fi
        };
        if :: emptyPlan -> goto done :: else -> skip fi;
        if :: FORCEONCE == 1 && firstNonEmpty -> f = 0; r = 0; firstNonEmpty = 0 :: else -> skip fi;
        LCA = caV;
        /* the CA */
        if
        :: pCA ->
             /* it must have been replaceable when written: nothing valid and nobody forcing it */
             atomic { fresh(); W = nw };
             if
             :: crashable -> crashpoint()
             :: else -> skip
             fi;
             legit = (caV == 0 || caKeyV != caV || f0 || caExpired);
             if :: caV != 0 && !legit -> gCAReplaced = 1 :: else -> skip fi;
#if KEYFIRST == 1
             atomic { caKeyV = W; caKeyLost = 0 }; chk();
             if :: crashable -> crashpoint() :: else -> skip fi;
             caV = W; caExpired = 0; chk();
#else
             caV = W; caExpired = 0; chk();
             if :: crashable -> crashpoint() :: else -> skip fi;
             atomic { caKeyV = W; caKeyLost = 0 }; chk();
#endif
             LCA = W; sNewCA = 1
        :: else -> skip
        fi;
        /* the leaves */
        if
        :: pS ->
             if :: !pCA && !(caV != 0 && caKeyV == caV) -> sErr = 1; goto errexit :: else -> skip fi;
             if :: !pCA -> LCA = caV :: else -> skip fi;
             atomic { fresh(); W = nw };
#if KEYFIRST == 1
             if :: crashable -> crashpoint() :: else -> skip fi;
             srvK = W; chk();
             if :: crashable -> crashpoint() :: else -> skip fi;
             srvC = W; srvS = LCA; srvEnding = 0; nameMissing = 0; chk();
#else
             if :: crashable -> crashpoint() :: else -> skip fi;
             srvC = W; srvS = LCA; srvEnding = 0; nameMissing = 0; chk();
             if :: crashable -> crashpoint() :: else -> skip fi;
             srvK = W; chk();
#endif
             sLeaf = 1
        :: else -> skip
        fi;
        if
        :: pC ->
             if :: !pCA && !(caV != 0 && caKeyV == caV) -> sErr = 1; goto errexit :: else -> skip fi;
             if :: !pCA -> LCA = caV :: else -> skip fi;
             atomic { fresh(); W = nw };
#if KEYFIRST == 1
             if :: crashable -> crashpoint() :: else -> skip fi;
             cliK = W; chk();
             if :: crashable -> crashpoint() :: else -> skip fi;
             cliC = W; cliS = LCA; cliEnding = 0; chk();
#else
             if :: crashable -> crashpoint() :: else -> skip fi;
             cliC = W; cliS = LCA; cliEnding = 0; chk();
             if :: crashable -> crashpoint() :: else -> skip fi;
             cliK = W; chk();
#endif
             sLeaf = 1
        :: else -> skip
        fi;
        /* the bundle: the new CA, or the one the snapshot held */
        if
        :: pB ->
             if :: crashable -> crashpoint() :: else -> skip fi;
             if :: pCA -> bundleV = LCA :: else -> bundleV = SNAP fi;
             chk()
        :: else -> skip
        fi;
        pass++
    :: else ->
        /* the code's error: the plan is not empty after MAXP passes */
        gNotEmpty = 1; latch(); goto done
    od;
done:
    atomic { lockHeld = 0; inCS--; alive-- };
    goto fin;
errexit:
    atomic { lockHeld = 0; inCS--; alive-- };
    goto fin;
died:
    /* the process is gone; the OS releases its lock when the process dies (a mutant keeps it held) */
    atomic {
        inCS--; alive--; dirtyByCrash = 1;
#if LOCKREL == 1
        lockHeld = 0
#else
        deadHolder = 1
#endif
    };
    chk();
fin:
    skip
}

/* the recovery run: starts when no other run is alive, cannot die, and must leave the directory right */
proctype Recover() {
    byte pCA; bit pS, pC, pB, newCA, sR, cR, caKeyOK, emptyPlan;
    byte id = 3, pass = 0;
    atomic { alive == 0 && lockHeld == 0 -> lockHeld = 1; inCS++ };
    do
    :: pass < MAXP ->
        atomic {
            plan(0, 0);
            emptyPlan = 0;
            if
            :: !pCA && !pS && !pC && !pB -> if :: !CONV -> gRecover = 1 :: else -> skip fi; emptyPlan = 1; latch()
            :: else -> skip
            fi
        };
        if :: emptyPlan -> break :: else -> skip fi;
        LCA = caV;
        if :: pCA -> atomic { fresh(); W = nw }; atomic { caKeyV = W; caKeyLost = 0 }; caV = W; caExpired = 0; LCA = W; chk() :: else -> skip fi;
        if :: pS -> atomic { fresh(); W = nw }; srvK = W; srvC = W; srvS = LCA; srvEnding = 0; nameMissing = 0; chk() :: else -> skip fi;
        if :: pC -> atomic { fresh(); W = nw }; cliK = W; cliC = W; cliS = LCA; cliEnding = 0; chk() :: else -> skip fi;
        if :: pB -> if :: pCA -> bundleV = LCA :: else -> bundleV = SNAP fi; chk() :: else -> skip fi;
        pass++
    :: else -> gRecover = 1; latch(); break
    od;
    atomic { lockHeld = 0; inCS-- }
}

/* the world moves */
proctype Env() {
    bit e1 = 0, e2 = 0, e4 = 0, e8 = 0;
end:
    do
    :: EVCA && !e1 && caV != 0 -> atomic { e1 = 1; caExpired = 1; quiet = 0 }
    :: EVLEAF && !e2 && srvC != 0 -> atomic { e2 = 1; srvEnding = 1; cliEnding = 1; quiet = 0 }
    :: EVNAME && !e4 && srvC != 0 -> atomic { e4 = 1; nameMissing = 1; quiet = 0 }
    :: EVKEY && !e8 && caKeyV != 0 -> atomic { e8 = 1; caKeyV = 0; caKeyLost = 1; quiet = 0 }
    od
}

/* the reader: the certificate file, then the key file, served only when the two agree */
proctype Reader() {
    byte c, k, n = 0;
    do
    :: n < NR ->
        c = srvC;
        k = srvK;
        if
        :: c != 0 && k != 0 && (c == k || NOCHECK == 1) ->
             sServed = 1;
             if :: c != k -> gServed = 1 :: else -> skip fi;
             latch()
        :: else -> skip
        fi;
        n++
    :: else -> break
    od
}

init {
    atomic {
        alive = NP;
        run Ensure(0, F0, R0, 1);
#if NP >= 2
        run Ensure(1, F1, R1, 1);
#endif
#if NP >= 3
        run Ensure(2, 0, 0, 1);
#endif
        run Env();
        run Reader();
#if REC == 1
        run Recover();
#endif
        skip
    }
}
