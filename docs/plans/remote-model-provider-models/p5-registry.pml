/* p5-registry.pml - the relay's registry of one node name: who may change a lease, what a registration does to its token, how the
   dial settings (swarm.node_tls) are inherited, and when a lease is reaped (phase 3, stage H0; swarm.node_tls).

   Code     external/swarm/registry.go: Registry.RegisterWithDial (a held name: the lease secret decides, ErrNameTaken otherwise; an
            owner's renewal replaces the token with the one it carries, an empty one included; a claim stores what it carries), the
            effective dial (a lease with no dial of its own takes nodeDial, one with its own keeps it, and a node's registration can never
            supply a dial), Pin (an operator's lease is never reaped), Delete, reapLocked (a lease is dropped when now is past its
            expiry plus the grace).
   Class    finite, discrete time. The clock is the AGE of the lease in ticks since its last accepted registration (saturating one past
            TTL + GRACE); it may advance at any point, so every interleaving of registrations and ticks is explored. Not a timed automaton.

   World
     nodes     A and B, two honest nodes that want the same name. Each registers with the secret it was given by its last successful claim
               (0 before one) and a token it chooses at each call, from 0 (none) to 2. A node never supplies a dial.
     operator  seeds the name as a hand-written upstream (a lease with a dial of its own, pinned) while it is free, and deletes the lease
               outright (the administrative takeover); the relay's nodeDial (swarm.node_tls) is set or not for the whole run (NDIAL).
     registry  one name. A lease holds a secret (fresh at each claim), a token, a dial (0 none, 1 nodeDial, 2 the operator's own), an
               age (ticks since the last accepted registration) and a pinned flag. TTL and GRACE are ticks.

   Options (the code is the default of each; a mutant switches one off)
     ERASE 1 an owner's renewal replaces the token with the one it carries (0: an empty token keeps the old one, the code before H0)
     INHERIT 1 a renewal keeps the lease's dial (0: it replaces the dial with what the registration carries, nothing: a mutant)
     STRANGER 0 a registration with a secret that is not the lease's changes nothing (1: a mutant that applies it)
     SECRETREUSE 0 every claim gets a fresh secret (1: a mutant that reuses one, so a stale owner's secret is good again)
     REAPPIN 0 a pinned lease is never reaped (1: a mutant)
     EARLY 0 a lease is reaped past expiry + GRACE (1: a mutant that reaps at the expiry)
     NDIAL 0 no swarm.node_tls, 1 set

   Properties (ghost latches, monotone; one is asserted per run with -D ASSERT_PROP=<latch>, expected to stay 0)
     R1  gTokenTruth   after an accepted registration (a claim or an owner's renewal) the lease's token is not the one it carried
     R2  gTakeover     a registration with a secret that is not the lease's changed a held lease (token, dial or expiry)
     R3  gDialOwn      a lease that had the operator's dial (2) no longer has it after a node's registration
     R3b gDialDefault  a lease a node holds, while NDIAL is set, has a dial other than nodeDial
     R4  gPinnedReaped a pinned lease was dropped by the clock
     R5  gLiveReaped   a lease whose last accepted registration is no older than TTL + GRACE was dropped by the clock
     R6  gStaleOwner   a node whose secret came from an earlier claim was accepted on a lease another node holds
     sanity            sTake (a node takes the name once the lease is gone), sErase (a token is erased), sOwnDial, sReap, sRenew, sStale

   Abstractions (what the model is not): one name; direct nodes only (a tunnel lease lives as long as its connection and is not on a clock:
     its lifecycle is the net p5-lease-net); the egress policy and a route that cannot be built are not modelled (a heartbeat that cannot
     build its route leaves the lease as it was, so R1 to R3 are about accepted registrations); a registration is atomic (the code holds the
     registry's lock through it); an owner's two simultaneous calls are two serial ones.
*/

#ifndef ERASE
#define ERASE 1
#endif
#ifndef INHERIT
#define INHERIT 1
#endif
#ifndef STRANGER
#define STRANGER 0
#endif
#ifndef SECRETREUSE
#define SECRETREUSE 0
#endif
#ifndef REAPPIN
#define REAPPIN 0
#endif
#ifndef EARLY
#define EARLY 0
#endif
#ifndef NDIAL
#define NDIAL 1
#endif
#ifndef TTL
#define TTL 3
#endif
#ifndef GRACE
#define GRACE 2
#endif
#if EARLY == 1
#define RG 0
#else
#define RG GRACE
#endif
#ifndef NREG
#define NREG 3
#endif

bit held;                  /* a lease exists                                                  */
byte lsecret;              /* its secret (0 none)                                             */
byte ltoken;               /* its token                                                       */
byte ldial;                /* its dial: 0 none, 1 nodeDial, 2 the operator's own              */
byte lowner;               /* ghost: who claimed it (1 A, 2 B, 3 the operator)                */
byte age;                  /* ticks since the last accepted registration (the lease expires at TTL, is reaped past TTL + GRACE) */
bit lpinned;
byte gen;                  /* the secrets handed out                                          */
byte mysecret[3];          /* the secret each node holds (index 1 A, 2 B)                     */

bit gTokenTruth, gTakeover, gDialOwn, gDialDefault, gPinnedReaped, gLiveReaped, gStaleOwner;
bit sTake, sErase, sOwnDial, sReap, sRenew, sStale;

byte o_tok, o_dial, o_age;   /* temporaries of one registration: the lease before it */

inline latch() {
#ifdef ASSERT_PROP
    assert(ASSERT_PROP == 0);
#endif
    skip
}

inline newsecret() {
#if SECRETREUSE == 1
    if :: gen == 0 -> gen = 1 :: else -> skip fi
#else
    gen++
#endif
}

/* a node's registration: who (1 or 2) and the token it carries */
inline register(who, tok) {
    atomic {
        if
        :: held ->
             o_tok = ltoken; o_dial = ldial; o_age = age;
             if
             :: mysecret[who] != lsecret ->
                  /* ErrNameTaken */
                  if :: mysecret[who] != 0 -> sStale = 1 :: else -> skip fi;
#if STRANGER == 1
                  ltoken = tok; ldial = 0; age = 0;
#endif
                  if :: ltoken != o_tok || ldial != o_dial || age != o_age -> gTakeover = 1 :: else -> skip fi
             :: else ->
                  /* the owner's renewal (or, with SECRETREUSE, a stale node's) */
                  if :: lowner != who -> gStaleOwner = 1 :: else -> skip fi;
                  sRenew = 1;
#if ERASE == 1
                  if :: ltoken != 0 && tok == 0 -> sErase = 1 :: else -> skip fi;
                  ltoken = tok;
#else
                  if :: tok != 0 -> ltoken = tok :: else -> skip fi;
#endif
#if INHERIT == 1
                  if :: ldial == 0 && NDIAL == 1 -> ldial = 1 :: else -> skip fi;
#else
                  ldial = 0;
#endif
                  if :: !lpinned -> age = 0 :: else -> skip fi;
                  if :: ltoken != tok -> gTokenTruth = 1 :: else -> skip fi;
                  if :: o_dial == 2 && ldial != 2 -> gDialOwn = 1 :: else -> skip fi
             fi
        :: else ->
             /* a claim */
             newsecret();
             lsecret = gen; mysecret[who] = gen; lowner = who;
             ltoken = tok;
             if :: NDIAL == 1 -> ldial = 1 :: else -> ldial = 0 fi;
             age = 0; lpinned = 0; held = 1;
             if :: ltoken != tok -> gTokenTruth = 1 :: else -> skip fi;
             if :: gen > 1 -> sTake = 1 :: else -> skip fi
        fi;
        if :: held && lowner != 3 && NDIAL == 1 && ldial != 1 -> gDialDefault = 1 :: else -> skip fi;
        latch()
    }
}

proctype Node(byte who) {
    byte tok;
    byte n = 0;
    do
    :: n < NREG ->
         if :: tok = 0 :: tok = 1 :: tok = 2 fi;
         register(who, tok);
         n++
    :: n >= NREG -> break
    od
}

proctype Operator() {
    byte n = 0;
    do
    :: n < 3 ->
         atomic {
             if
             :: !held ->
                  /* seeds the name as a hand-written upstream: its own dial, pinned */
                  newsecret(); lsecret = gen; lowner = 3; ltoken = 1; ldial = 2;
                  age = 0; lpinned = 1; held = 1; sOwnDial = 1
             :: held ->
                  /* deletes the lease outright (the administrative takeover) */
                  held = 0; lsecret = 0; ltoken = 0; ldial = 0; lpinned = 0
             fi;
             n++; latch()
         }
    :: n >= 3 -> break
    od
}

/* the clock may advance at any point; the age saturates one past the reap line, and a lease past it is reaped */
proctype Clock() {
    do
    :: atomic {
         if :: held && age < TTL + GRACE + 1 -> age++ :: else -> skip fi;
         if
         :: held && (REAPPIN == 1 || !lpinned) && age > TTL + RG ->
              if :: lpinned -> gPinnedReaped = 1 :: else -> skip fi;
              if :: age <= TTL + GRACE -> gLiveReaped = 1 :: else -> skip fi;
              held = 0; lsecret = 0; ltoken = 0; ldial = 0; lpinned = 0; age = 0; sReap = 1
         :: else -> skip
         fi;
         latch()
       }
    od
}

init {
    atomic { run Node(1); run Node(2); run Operator(); run Clock() }
}
