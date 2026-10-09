/* p5-certloader.pml - the client certificate loader of an outbound leg: a pair of files read at each handshake while an operator rotates
   them (phase 3, H1: internal/netx). The model found the defect of the first loader, which cached the pair by size and modification
   time (CONTENT 0 below: a rotation that keeps the stamps is served stale); the code now compares the bytes.

   Code     internal/netx/clientcert.go (certLoader.get: the certificate file and the key file are read at each handshake, OUTSIDE the
            loader's lock; under the lock the two byte strings are compared with the ones cached, and when either differs tls.X509KeyPair
            parses them, refuses a pair that does not match, and publishes the new bytes and the parsed pair; a failed read or parse changes
            nothing and is not cached) and ClientTLS.Key (the transport cache key of a coddy row: a digest of the paths and of the content of
            the files, not modelled: it only decides when a new transport is built, at most one more per distinct intermediate content).
   Class    finite, discrete time (CONTENT 0 only): `now` ticks, the rotator and the handshakes interleave in every order.

   World
     files   a certificate file and a key file, each holding a version (1 old, 2 new). The model has no sizes: two EC certificates of one
             profile may have one (the signature's length varies by a byte or two): the model takes that worst case, in which only the stamps
             and the bytes can tell the versions apart.
     rotator replaces the files: ROT 0 both, one after the other (the certificate first, or the key first: ORDER) or both at once through an
             atomic swap of their directory (ORDER 2: one step); ROT 1 only the certificate, the key reused (certbot's reuse_key, a
             resubmitted request: tls.X509KeyPair accepts the new certificate with the old key).
     client  NC concurrent handshakes, NH each. A handshake is get(): the two reads are steps of their own, each between any two steps of the
             rotator or of another handshake; for the old loader a stat of the two files comes first. Compare, parse and publish are ONE
             critical section (the loader's lock), so a slow handshake can publish an older pair over a newer one (sRegress): it costs one
             more parse, never a wrong pair.
   Options (the code is the default of each; the others are the loader this stage replaced, or mutants)
     CONTENT 1  the two files are read at each handshake and the pair is parsed again only when its bytes differ from the cached ones
                (0: the cache is keyed by the stamps of the two files: the first loader. It reads no file on a hit, and stats before the loads)
     STAMP   how a write stamps a file, for CONTENT 0 only: 0 the tick (precise), 1 half the tick (a coarse file system: 1 or 2 s on some
             network mounts), 2 a constant (a normalised modification time: a reproducible archive, a layer, a store that sets every file to the
             epoch). The code never reads a stamp, so for CONTENT 1 the property is stamp-independent by construction and the model carries none.
     ROT, ORDER  the rotation, above
     CMP     what the hit compares (CONTENT 1): 0 both byte strings (the code), 1 the certificate only, 2 the key only, 3 nothing (any cached
             pair is served)
     NOCHECK 0 a pair whose halves do not match is refused (what tls.X509KeyPair does), 1 it is served (a mutant)
     ERRBYTES 0 a failed parse changes nothing, 1 the bytes are cached before the parse (a failed parse then leaves the new bytes next to the old
             pair: the next handshake over the same bad bytes is served the old pair instead of an error)
     ERRCACHE 0 a failure changes nothing (the code), 1 a failed pair is remembered under the same key as a good one (the stamps, or the bytes) and
             refused again (negative caching), 2 a failure is remembered and refused whatever the bytes are (a sticky error). Both are mutants: the
             code remembers no failure. Over CONTENT 1 the first is safe and no latch can catch it, since tls.X509KeyPair is a pure function of its bytes
     NC, NH  handshakes in flight, and each one's count
   Properties (ghost latches, one asserted per run with -D ASSERT_PROP=<latch>, expected to stay 0)
     L1 gMismatch  a handshake was served a certificate and a key that do not match (the pair was never parsed by tls.X509KeyPair)
     L2 gStale     after the rotation finished, a handshake that started after it was served the old certificate (on a hit and on a parse)
     L3 gNoRecover after the rotation finished, a handshake over files that match was refused (an error was served from the cache)
     L4 gMasked    a handshake over files that do not match was served a cached pair instead of an error (the hit branch hides a bad pair)
     By construction: L1 under NOCHECK 0, L3 while no failure is cached and L2 under CMP 0 hold because of the way the code is built; CMP, NOCHECK,
     ERRCACHE 2, ERRBYTES and CONTENT 0 are what show the latches can fail.
     sanity        sMiss (the files were read), sParse (the pair was parsed), sErr (a mismatch was refused), sErrHit (a cached error was served), sHit (the cache served), sNew
                   (the new certificate is served), sRegress (a handshake that read the old files published them over the new pair)

   Abstractions (what the model is not): one leg; the first loader read the pair inside LoadX509KeyPair under its lock, and the model reads outside
     the lock for both loaders, which cannot affect the CONTENT 0 runs (NC 1); the files hold a version number, not bytes; a failed read (a missing file, a torn write in
     place, a permission) is not a step, since a failure caches nothing and the next handshake reads again (held by the Go test
     TestClientCertificateRotatesAndFailsOnlyTheHandshake); a read returns the current content of the file (a network mount whose attribute or
     read cache lags a write is outside the model); a resumed TLS session does not call the loader (the outbound legs set no session cache);
     ClientTLS.Key(), which reads the same files outside the loader.
*/

#ifndef STAMP
#define STAMP 0
#endif
#ifndef CONTENT
#define CONTENT 1
#endif
#ifndef NOCHECK
#define NOCHECK 0
#endif
#ifndef ORDER
#define ORDER 0
#endif
#ifndef ROT
#define ROT 0
#endif
#ifndef CMP
#define CMP 0
#endif
#ifndef ERRCACHE
#define ERRCACHE 0
#endif
#ifndef ERRBYTES
#define ERRBYTES 0
#endif
#ifndef NC
#define NC 1
#endif
#ifndef NH
#define NH 4
#endif
#ifndef T
#define T 6
#endif

byte certV = 1, keyV = 1;      /* what the files hold                                   */
byte rotated;                  /* 0 not started, 1 first file written, 2 rotation finished */
#if CONTENT == 0
byte now;
byte certS, keyS;              /* their modification stamps                             */
byte cacheCS, cacheKS;         /* the stamps the cached pair was cached under            */
byte errCS, errKS;             /* ... and a remembered failure                          */
#endif
bit cached;
byte cacheC, cacheK;           /* the cached bytes (as versions): CONTENT 1 compares them */
byte cacheP;                   /* the parsed certificate's version: what a handshake is served */
bit errCached;
byte errC, errK;               /* the bytes a remembered failure was seen under           */

bit gMismatch, gStale, gNoRecover, gMasked;
bit sMiss, sParse, sErr, sErrHit, sHit, sNew, sRegress;

inline latch() {
#ifdef ASSERT_PROP
    assert(ASSERT_PROP == 0);
#endif
    skip
}

#if ROT == 1
#define MATCH(c, k) (1)
#else
#define MATCH(c, k) ((c) == (k))
#endif

/* what a hit compares against the cache (CONTENT 1) */
#if CMP == 0
#define SAME(c, k) ((c) == cacheC && (k) == cacheK)
#elif CMP == 1
#define SAME(c, k) ((c) == cacheC)
#elif CMP == 2
#define SAME(c, k) ((k) == cacheK)
#else
#define SAME(c, k) (1)
#endif

#if CONTENT == 0
inline stampOf(t, out) {
#if STAMP == 0
    out = t + 1
#elif STAMP == 1
    out = (t / 2) + 1
#else
    out = 1
#endif
}
#define RAT (now >= 1)
#else
#define RAT (1)
#endif

proctype Rotator() {
#if CONTENT == 0
    byte st;
#endif
    /* the rotator starts at some point; it writes the first file, then the second at a later or the same point */
#if ROT == 1
    atomic { RAT ->
#if CONTENT == 0
        stampOf(now, st); certS = st;
#endif
        certV = 2; rotated = 2;
        latch()
    }
#elif ORDER == 2
    atomic { RAT ->
#if CONTENT == 0
        stampOf(now, st); certS = st; keyS = st;
#endif
        certV = 2; keyV = 2; rotated = 2;
        latch()
    }
#else
    atomic { RAT ->
#if CONTENT == 0
        stampOf(now, st);
#endif
#if ORDER == 0
#if CONTENT == 0
        certS = st;
#endif
        certV = 2; rotated = 1;
#else
#if CONTENT == 0
        keyS = st;
#endif
        keyV = 2; rotated = 1;
#endif
        latch()
    };
    atomic { rotated == 1 ->
#if CONTENT == 0
        stampOf(now, st);
#endif
#if ORDER == 0
#if CONTENT == 0
        keyS = st;
#endif
        keyV = 2;
#else
#if CONTENT == 0
        certS = st;
#endif
        certV = 2;
#endif
        rotated = 2;
        latch()
    }
#endif
}

proctype Client() {
    byte n = 0;
    byte rc, rk, startRot;
#if CONTENT == 0
    byte cs, ks;
#endif
    do
    :: n < NH ->
#if CONTENT == 0
         /* the stat of the two files, then the cache check against their stamps */
         atomic { cs = certS; ks = keyS; startRot = rotated };
         if
         :: cached && cs == cacheCS && ks == cacheKS ->
              atomic {
                  sHit = 1;
                  if :: startRot == 2 && cacheP == 1 -> gStale = 1 :: else -> skip fi;
                  if :: cacheP == 2 -> sNew = 1 :: else -> skip fi;
                  latch()
              }
#if ERRCACHE >= 1
         :: errCached && cs == errCS && ks == errKS ->
              /* the remembered failure is served, the files are not read */
              atomic {
                  sErr = 1; sErrHit = 1;
                  if :: startRot == 2 -> gNoRecover = 1 :: else -> skip fi;
                  latch()
              }
#endif
         :: else ->
#else
         atomic { startRot = rotated };
         if
#if ERRCACHE == 2
         :: errCached ->
              /* a sticky failure: refused whatever the files hold */
              atomic {
                  sErr = 1; sErrHit = 1;
                  if :: startRot == 2 -> gNoRecover = 1 :: else -> skip fi;
                  latch()
              }
#endif
         :: else ->
#endif
              /* a miss, or CONTENT 1: the certificate is read, then the key, each a step of its own and outside the lock */
              atomic { rc = certV; sMiss = 1 };
              atomic { rk = keyV };
              /* the lock: compare, parse and publish are one critical section */
              atomic {
                  if
#if CONTENT == 1
                  :: cached && SAME(rc, rk) ->
                       /* the cached pair serves */
                       sHit = 1;
                       if :: !MATCH(rc, rk) -> gMasked = 1 :: else -> skip fi;
                       if :: startRot == 2 && cacheP == 1 -> gStale = 1 :: else -> skip fi;
                       if :: cacheP == 2 -> sNew = 1 :: else -> skip fi
#if ERRCACHE == 1
                  :: errCached && rc == errC && rk == errK ->
                       sErr = 1; sErrHit = 1;
                       if :: startRot == 2 && MATCH(rc, rk) -> gNoRecover = 1 :: else -> skip fi
#endif
#endif
#if CONTENT == 1
#if ERRCACHE == 1
                  :: !MATCH(rc, rk) && NOCHECK == 0 && !(cached && SAME(rc, rk)) && !(errCached && rc == errC && rk == errK) ->
#else
                  :: !MATCH(rc, rk) && NOCHECK == 0 && !(cached && SAME(rc, rk)) ->
#endif
#else
                  :: !MATCH(rc, rk) && NOCHECK == 0 ->
#endif
                       /* an error: nothing is cached (unless the mutant remembers it); a later handshake over matching files must succeed */
                       sErr = 1
#if ERRBYTES == 1
                       ; if :: cached -> cacheC = rc; cacheK = rk :: else -> skip fi
#endif
#if ERRCACHE >= 1
                       ; errCached = 1; errC = rc; errK = rk
#if CONTENT == 0
                       ; errCS = cs; errKS = ks
#endif
#endif
                  :: else ->
                       sParse = 1;
                       if :: !MATCH(rc, rk) -> gMismatch = 1 :: else -> skip fi;
                       if :: cached && cacheC == 2 && rc == 1 -> sRegress = 1 :: else -> skip fi;
                       cached = 1; cacheC = rc; cacheK = rk; cacheP = rc;
#if CONTENT == 0
                       cacheCS = cs; cacheKS = ks;
#endif
                       if :: startRot == 2 && rc == 1 -> gStale = 1 :: else -> skip fi;
                       if :: rc == 2 -> sNew = 1 :: else -> skip fi
                  fi;
                  latch()
              }
         fi;
         n++
    :: n >= NH -> break
    od
}

#if CONTENT == 0
proctype Clock() {
    do
    :: now < T -> atomic { now++ }
    :: now >= T -> break
    od
}
#endif

init {
    atomic {
#if CONTENT == 0
        /* the files were written together at the start */
        stampOf(0, certS); keyS = certS;
        run Clock();
#endif
        run Rotator();
        byte i = 0;
        do :: i < NC -> run Client(); i++ :: else -> break od
    }
}
