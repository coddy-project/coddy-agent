/* p5-certloader.pml - the client certificate loader of an outbound leg: a pair of files read at each handshake and cached by size and
   modification time, while an operator rotates the two files one after the other (phase 3, H1: internal/netx).

   Code     internal/netx/clientcert.go (certLoader.get: stat both files, serve the cached pair when both sizes and modification times are
            those it cached, else tls.LoadX509KeyPair, which reads the certificate then the key and refuses a pair that does not match; a
            failed load changes nothing and is not cached; the stamps cached are the ones read BEFORE the load) and ClientTLS.Key (the
            transport cache key of a coddy row, which includes the same stamps).
   Class    finite, discrete time: `now` ticks, the rotator and the handshakes interleave in every order.

   World
     files   a certificate file and a key file, each holding a version (1 old, 2 new) and a stamp (its size is the same for both versions, as
             two EC certificates of one profile are: only the modification time can tell them apart).
     rotator replaces the two files one after the other (the certificate first, or the key first: ORDER), each at the tick it chooses, or both
             at once through an atomic swap of their directory (ORDER 2: one step).
     client  handshakes: at each, get() runs as the code does: the stat of the two files, the cache check, and on a miss the two reads, each
             a step of its own (the rotator can write between them).
   Options
     STAMP   how a write stamps a file: 0 the tick (precise), 1 half the tick (a coarse file system: 1 or 2 s on some network mounts),
             2 a constant (a normalised modification time: a reproducible archive, a layer, a store that sets every file to the epoch)
     CONTENT 0 the cache is keyed by the stamps (the code before this stage), 1 the two files are read at each handshake and the pair is
             parsed again only when their bytes differ from the cached ones (the code after it)
     NOCHECK 0 a pair whose versions differ is refused (what tls.LoadX509KeyPair does), 1 it is served (a mutant)
     ORDER   0 certificate then key, 1 key then certificate, 2 both in one step

   Properties (ghost latches, one asserted per run with -D ASSERT_PROP=<latch>, expected to stay 0)
     L1 gMismatch  a handshake was served a pair of a certificate and a key of different versions
     L2 gStale     after the rotation finished (both files hold the new version) a handshake that started after it was served the old version
     L3 gNoRecover after the rotation finished, a handshake that found the files consistent was refused (an error is cached)
     sanity        sMiss (a load happened), sErr (a mismatch was refused), sHit (the cache served), sNew (the new version is served)

   Abstractions (what the model is not): one leg; a handshake is one call of get() (the transport calls it from several goroutines under the
     loader's lock, which makes the check and the load one critical section: the model keeps them one process); the files hold a version
     number, not bytes; the stat and the reads are separate steps, which is what lets a rotation fall between them.
*/

#ifndef STAMP
#define STAMP 0
#endif
#ifndef CONTENT
#define CONTENT 0
#endif
#ifndef NOCHECK
#define NOCHECK 0
#endif
#ifndef ORDER
#define ORDER 0
#endif
#ifndef T
#define T 6
#endif
#ifndef NH
#define NH 4
#endif

byte now;
byte certV = 1, keyV = 1;      /* what the files hold                                   */
byte certS, keyS;              /* their modification stamps (the size never differs)    */
byte rotated;                  /* 0 not started, 1 first file written, 2 both written    */
byte rotDoneAt;                /* the tick the rotation finished                         */
byte cacheV, cacheCS, cacheKS; /* the cached pair: its version and the stamps it was cached under (0: empty) */
bit cached;

bit gMismatch, gStale, gNoRecover;
bit sMiss, sErr, sHit, sNew;

inline latch() {
#ifdef ASSERT_PROP
    assert(ASSERT_PROP == 0);
#endif
    skip
}

inline stampOf(t, out) {
#if STAMP == 0
    out = t + 1
#elif STAMP == 1
    out = (t / 2) + 1
#else
    out = 1
#endif
}

proctype Rotator() {
    byte st;
    /* the rotator starts at some tick; it writes the first file, then the second at a later or the same tick */
    atomic { now >= 1 && rotated == 0 ->
#if ORDER == 2
        stampOf(now, st); certV = 2; keyV = 2; certS = st; keyS = st; rotated = 2; rotDoneAt = now;
        latch()
#elif ORDER == 0
        stampOf(now, st); certV = 2; certS = st; rotated = 1;
        latch()
#else
        stampOf(now, st); keyV = 2; keyS = st; rotated = 1;
        latch()
#endif
    };
#if ORDER != 2
    atomic { rotated == 1 ->
        stampOf(now, st);
#if ORDER == 0
        keyV = 2; keyS = st;
#else
        certV = 2; certS = st;
#endif
        rotated = 2; rotDoneAt = now;
        latch()
    }
#endif
}

proctype Client() {
    byte n = 0;
    byte cs, ks, rc, rk, startRot;
    bit hit, ok;
    do
    :: n < NH ->
         /* the stat of the two files */
         atomic { cs = certS; ks = keyS; startRot = rotated; hit = 0; ok = 0 };
         if
         :: CONTENT == 0 && cached && cs == cacheCS && ks == cacheKS ->
              /* the cache serves */
              atomic {
                  hit = 1; sHit = 1;
                  if :: startRot == 2 && cacheV == 1 -> gStale = 1 :: else -> skip fi;
                  if :: cacheV == 2 -> sNew = 1 :: else -> skip fi;
                  latch()
              }
         :: else ->
              /* a miss: the certificate is read, then the key, each a step of its own */
              atomic { rc = certV; sMiss = 1 };
              atomic { rk = keyV };
              if
              :: CONTENT == 1 && cached && rc == cacheV && rk == cacheV ->
                   /* the bytes are those cached: no parse, the cached pair serves */
                   atomic {
                       hit = 1; sHit = 1;
                       latch()
                   }
              :: rc != rk && NOCHECK == 0 ->
                   atomic {
                       sErr = 1;
                       /* an error: nothing is cached; a later handshake over consistent files must succeed */
                       if :: startRot == 2 && rc == 2 && rk == 2 -> gNoRecover = 1 :: else -> skip fi;
                       latch()
                   }
              :: else ->
                   atomic {
                       if :: rc != rk -> gMismatch = 1 :: else -> skip fi;
                       cached = 1; cacheV = rc; cacheCS = cs; cacheKS = ks;
                       if :: startRot == 2 && rc == 1 -> gStale = 1 :: else -> skip fi;
                       if :: rc == 2 -> sNew = 1 :: else -> skip fi;
                       latch()
                   }
              fi
         fi;
         n++
    :: n >= NH -> break
    od
}

proctype Clock() {
    do
    :: now < T -> atomic { now++ }
    :: now >= T -> break
    od
}

init {
    atomic {
        /* the files were written together at the start */
        stampOf(0, certS); keyS = certS;
        run Rotator(); run Client(); run Clock()
    }
}
