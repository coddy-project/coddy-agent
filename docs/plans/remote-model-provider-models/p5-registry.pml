/* p5-registry.pml - the relay's registry of one node name: who may change a lease, what a registration does to its token, how the
   dial settings (swarm.node_tls) are inherited, and when a lease is reaped (phase 3, stage H0; swarm.node_tls).

   Code     external/swarm/registry.go: Registry.RegisterWithDial (a held name: the lease secret decides, ErrNameTaken otherwise; an
            owner's renewal replaces the token with the one it carries, an empty one included; a claim stores what it carries), the
            effective dial (a lease with no dial of its own takes nodeDial, one with its own keeps it, and a node's registration can never
            supply a dial), Pin (an operator's lease is never reaped), Delete, reapLocked (a lease is dropped when now is past its
            expiry plus the grace).
   Class    finite, discrete time. The clock is the AGE of the lease in ticks since its last accepted registration (saturating one past
            TTL + GRACE); it may advance at any point, so every interleaving of registrations and ticks is explored. Not a timed automaton.
            The clock only advances. Reaping (reapLocked) runs at the START of every registration, as RegisterWithDial does, and in a process
            of its own at any time (Len, Node, AttachTransport), so a lease past expiry + GRACE can be held when a registration begins only
            if the registration forgot to reap. GRACE is the code's: the registry's grace equals its ttl (NewRegistry), so TTL = GRACE = 3.

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
     EARLY 0 a lease is reaped past expiry + GRACE (1: a mutant that reaps past the expiry with no grace)
     STRANGER2 0 (1: a refused registration still hands the stranger a fresh secret and the lease: token, dial and age untouched)
     DIALLEAK 0 (1: a claim gives the lease a non-zero dial that swarm.node_tls did not supply: with no node_tls the code's nodeDial is the zero Options,
                so a claim taking it would be harmless; this stands for a stale nodeDial that survived a rebuild)
     RENEWNOAGE 0 (1: an owner's renewal does not extend the lease: expiresAt is not moved)
     NOREAPREG 0 RegisterWithDial reaps first (1: it does not: a lease past expiry + grace is still held when the next registration comes)
     LATE 0 a lease is reaped past expiry + GRACE (1: one tick later, which at the saturated age is never)
     DISCLOSE 0 (1: a refused registration hands the stranger the lease's current secret and changes nothing else)
     CLAIMNOTOKEN 0 (1: a claim does not store the token it carries)
     NDIAL 0 no swarm.node_tls, 1 set

   Properties (ghost latches, monotone; one is asserted per run with -D ASSERT_PROP=<latch>, expected to stay 0)
     R1  gTokenTruth   after an accepted registration (a claim or an owner's renewal) the lease's token is not the one it carried
     R2  gTakeover     a registration with a secret that is not the lease's changed a held lease (token, dial, age, secret, owner or pin)
     R3  gDialOwn      a lease that had the operator's dial (2) no longer has it after a node's registration
     R3b gDialDefault  a lease a node holds has a dial other than nodeDial when swarm.node_tls is set, and a dial when it is not
     R4  gPinnedReaped a pinned lease was dropped by a reap
     R5  gLiveReaped   a lease whose last accepted registration is no older than TTL + GRACE was dropped by a reap (judged by the ghost
                       gsince, which counts from the last accepted registration and which no mutant touches)
     R6  gStaleOwner   a node was accepted on a lease another node holds (it came with a secret of an earlier claim, or a disclosed one)
     R7  gExpiredHeld  a lease past expiry + GRACE and not pinned was still held when a registration began, after the reap that opens it
                       (R4 and R5 guard reaping too early; R7 guards reaping that is missing or late)
     sanity            sTake (a node takes the name once the lease is gone), sErase (a token is erased), sOwnDial, sReap, sRenew, sStale,
                       and sOwnRenew, which is NOT reachable by design: a node cannot renew the operator's lease (nobody holds its secret:
                       seedUpstreams discards the response), so R3 holds because R2 and R6 refuse the call first; the retention rule is
                       exercised by R3b and by SECRETREUSE=1 INHERIT=0

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
#ifndef STRANGER2
#define STRANGER2 0      /* 1: a refused registration hands the stranger a fresh secret (the lease changes hands, token, dial and age untouched) */
#endif
#ifndef DIALLEAK
#define DIALLEAK 0       /* 1: a claim gives the lease nodeDial even when swarm.node_tls is not set */
#endif
#ifndef RENEWNOAGE
#define RENEWNOAGE 0     /* 1: an owner's renewal does not extend the lease (expiresAt not moved) */
#endif
#ifndef NOREAPREG
#define NOREAPREG 0      /* 1: RegisterWithDial does not reap first */
#endif
#ifndef DISCLOSE
#define DISCLOSE 0       /* 1: a refused registration hands the stranger the lease's current secret, nothing else changes */
#endif
#ifndef CLAIMNOTOKEN
#define CLAIMNOTOKEN 0   /* 1: a claim does not store the token it carries */
#endif
#ifndef NDIAL
#define NDIAL 1
#endif
#ifndef TTL
#define TTL 3
#endif
#ifndef GRACE
#define GRACE 3
#endif
#ifndef LATE
#define LATE 0           /* 1: a lease is reaped one tick late, which at the saturated age is never */
#endif
#if EARLY == 1
#define RG 0
#else
#if LATE == 1
#define RG (GRACE+1)
#else
#define RG GRACE
#endif
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

bit gExpiredHeld;
bit gTokenTruth, gTakeover, gDialOwn, gDialDefault, gPinnedReaped, gLiveReaped, gStaleOwner;
bit sTake, sErase, sOwnDial, sReap, sRenew, sStale, sOwnRenew;
byte gsince;               /* ghost: ticks since the last accepted registration, kept apart from age so a mutant that stops resetting age cannot hide behind it */

byte o_tok, o_dial, o_age, o_sec, o_own;   /* temporaries of one registration: the lease before it (cleared at its end) */
bit o_pin;

inline latch() {
#ifdef ASSERT_PROP
    assert(ASSERT_PROP == 0);
#endif
    skip
}

inline reap() {
    if
    :: held && (REAPPIN == 1 || !lpinned) && age > TTL + RG ->
         if :: lpinned -> gPinnedReaped = 1 :: else -> skip fi;
         if :: gsince <= TTL + GRACE -> gLiveReaped = 1 :: else -> skip fi;
         held = 0; lsecret = 0; ltoken = 0; ldial = 0; lpinned = 0; age = 0; gsince = 0; sReap = 1
    :: else -> skip
    fi
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
#if NOREAPREG == 0
        reap();
#endif
        if :: held && !lpinned && gsince > TTL + GRACE -> gExpiredHeld = 1 :: else -> skip fi;
        if
        :: held ->
             o_tok = ltoken; o_dial = ldial; o_age = age; o_sec = lsecret; o_own = lowner; o_pin = lpinned;
             if
             :: mysecret[who] != lsecret ->
                  /* ErrNameTaken */
                  if :: mysecret[who] != 0 -> sStale = 1 :: else -> skip fi;
#if STRANGER == 1
                  ltoken = tok; ldial = 0; age = 0;
#endif
#if STRANGER2 == 1
                  newsecret(); lsecret = gen; mysecret[who] = gen; lowner = who;
#endif
#if DISCLOSE == 1
                  mysecret[who] = lsecret;
#endif
                  if :: ltoken != o_tok || ldial != o_dial || age != o_age || lsecret != o_sec || lowner != o_own || lpinned != o_pin -> gTakeover = 1 :: else -> skip fi
             :: else ->
                  /* the owner's renewal (or, with SECRETREUSE, a stale node's) */
                  if :: lowner != who -> gStaleOwner = 1 :: else -> skip fi;
                  sRenew = 1; gsince = 0; if :: o_dial == 2 -> sOwnRenew = 1 :: else -> skip fi;
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
#if RENEWNOAGE == 0
                  if :: !lpinned -> age = 0 :: else -> skip fi;
#endif
                  if :: ltoken != tok -> gTokenTruth = 1 :: else -> skip fi;
                  if :: o_dial == 2 && ldial != 2 -> gDialOwn = 1 :: else -> skip fi
             fi
        :: else ->
             /* a claim */
             newsecret();
             lsecret = gen; mysecret[who] = gen; lowner = who;
#if CLAIMNOTOKEN == 1
             ltoken = 0;
#else
             ltoken = tok;
#endif
             if :: NDIAL == 1 || DIALLEAK == 1 -> ldial = 1 :: else -> ldial = 0 fi;
             age = 0; gsince = 0; lpinned = 0; held = 1;
             if :: ltoken != tok -> gTokenTruth = 1 :: else -> skip fi;
             if :: gen > 1 -> sTake = 1 :: else -> skip fi
        fi;
        if :: held && lowner != 3 && ldial != NDIAL -> gDialDefault = 1 :: else -> skip fi;
        o_tok = 0; o_dial = 0; o_age = 0; o_sec = 0; o_own = 0; o_pin = 0;
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
                  age = 0; gsince = 0; lpinned = 1; held = 1; sOwnDial = 1
             :: held ->
                  /* deletes the lease outright (the administrative takeover) */
                  held = 0; lsecret = 0; ltoken = 0; ldial = 0; lpinned = 0
             fi;
             n++; latch()
         }
    :: n >= 3 -> break
    od
}

/* the clock may advance at any point; the age saturates one past the reap line */
proctype Clock() {
    do
    :: atomic {
         if :: held && age < TTL + GRACE + 1 -> age++ :: else -> skip fi;
         if :: held && gsince < TTL + GRACE + 1 -> gsince++ :: else -> skip fi;
         latch()
       }
    od
}

/* Len, Node, AttachTransport: reapLocked at any time */
proctype Lener() {
    do
    :: atomic { reap(); latch() }
    od
}

init {
    atomic { run Node(1); run Node(2); run Operator(); run Clock(); run Lener() }
}
