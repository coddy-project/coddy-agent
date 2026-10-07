/* m6-capabilities.pml - how the capabilities of a `coddy` model row arrive.

   Models   docs/plans/remote-model-provider.md sections 4.2 (listing fields), 4.3 ([decide at modeling]
            bullet), 6 (signatures and alias reassignment), 6a, and the scenarios of
            features/remote_model_provider.feature about the context window, the reasoning levels,
            multimodal and "the requested reasoning level reaches the provider".
   Class    finite, untimed. TIME IS A DISCRETE-CLOCK ABSTRACTION (declared, not a timed automaton):
            the Tick process advances the clock; a tick is blocked while the user has not acted in
            it, while a request is in flight and while a listing fetch is overdue (urgency).
            So: one user action per tick, a request is delivered inside its tick, a listing fetch
            finishes at most FL ticks after it started. Nothing else is timed.

   Remote listing of an alias = (capability bits, revision), five bits
            d=0 lvA   reasoning level A offered       d=3 lvB  reasoning level B offered
            d=1 win   context window class (1 = large)
            d=2 mm    multimodal
            d=4 off   allow_reasoning_off (the level `off` is accepted only with it)
   ND keeps a prefix of the dimensions: ND=2 is {lvA, win}, ND=3 adds mm, ND=5 is everything. The
   dimensions behave alike (one atomic snapshot copies all of them), so timing results do not depend
   on ND; the refusal kinds do (LEVEL: lvA lvB off, OVERFLOW: win, IMAGE: mm).
   The operator process changes it at any time (flip one bit = narrowed or widened) or REASSIGNS the
   alias (new model epoch, new bits, the signatures of the old model become stale). Every change bumps
   the row revision rrev; only a reassignment bumps the model epoch rmodel. At most NOP changes.
   The local side has a view = override (YAML key, wins when set) else cache (copied from a listing).
   A user process acts once per tick: it picks a selection (level, image, conversation size), then
   peeks at the view, idles (ACTIVE=0) or sends. A send FILTERS the selection through the CURRENT view
   (what the session does with its selected reasoning level and with attachments), so the request
   only carries what the local view offers. The remote validates against ITS current row and refuses:
   STALE (kind 1, expected_revision, before any generation), LEVEL (2) and IMAGE (3) before generation,
   OVERFLOW (4, the window class is larger than the real one) and SIG (5, a signature of an older
   model) from the provider, that is as a FAILED GENERATION.
   Atomic steps are d_step blocks without nondeterminism: a choice (an operator change, the user's
   selection) is the choice of the transition, one option per alternative, so no intermediate state exists.
   (This also avoids a panic of mcd 0.2.0 when it renders a lasso through `atomic` sequences.)

   Mechanism switches (-D NAME=1):
     PICKCOPY  A: the picker copies the values once into the local row as ordinary keys (= overrides),
               nothing refreshes them.
     TTLREF    B: the cache is refreshed when a reader finds it older than TTL ticks (stale values
               keep serving while the fetch runs, as session/context_window.go does).
     ONREFUSE  C: a refusal makes the next reader refresh (forced); with TTLREF it is B+C.
     XREV      D: the listing has `revision`, the request carries expected_revision, the remote
               refuses a mismatch before generation.
     STAMP     E: no refusal for it; every response carries the revision and the client refreshes.
   Other switches: AWAIT (a reader waits for a forced refresh in flight; the wait takes no tick),
   ACTIVE (the user acts every tick; 0 lets it idle), OVR (the operator sets YAML overrides at start,
   arbitrary values), SIGS (history carries signatures), SIGFIX 0|1|2 (0 nothing; 1 the client sends
   only signatures tagged with the revision of its view, needs XREV or STAMP; 2 the remote wraps its
   signatures with the model epoch and drops foreign ones: works under any mechanism), BUGOVR
   (mutation: a refresh writes through the override keys), BUGLIST (mutation: the listing publishes
   level A inverted, so a view equal to the listing is still refused).
   Observation switches: LIVE (lastact / lastres for LTL), GHOST (reachability latches),
   CK / SB / SK / OVK (assert bounds: consecutive refusals, staleness, sends from a stale view, override).
   Constants: TTL 3, FL 1, NOP 3; CAP, CAPC saturate the counters and must exceed every bound asked.
*/

#ifndef ND
#define ND 5
#endif
#ifndef TTL
#define TTL 3
#endif
#ifndef FL
#define FL 1
#endif
#ifndef NOP
#define NOP 3
#endif
#ifndef PICKCOPY
#define PICKCOPY 0
#endif
#ifndef TTLREF
#define TTLREF 0
#endif
#ifndef ONREFUSE
#define ONREFUSE 0
#endif
#ifndef XREV
#define XREV 0
#endif
#ifndef STAMP
#define STAMP 0
#endif
#ifndef AWAIT
#define AWAIT 1
#endif
#ifndef ACTIVE
#define ACTIVE 1
#endif
#ifndef OVR
#define OVR 0
#endif
#ifndef SIGS
#define SIGS 0
#endif
#ifndef SIGFIX
#define SIGFIX 0
#endif
#ifndef BUGOVR
#define BUGOVR 0
#endif
#ifndef BUGLIST
#define BUGLIST 0
#endif
#ifndef LIVE
#define LIVE 1
#endif
#ifndef GHOST
#define GHOST 1
#endif
#ifndef CAP
#define CAP  7
#endif
#ifndef CAPC
#define CAPC 4
#endif

/* the remote row, the local cache, the local override keys (ovv0 = what the operator typed) */
bit rem[5];
bit cache[5];
bit ovs[5];
bit ovv[5];
bit ovv0[5];
byte rrev;       /* row revision of the remote = number of operator changes so far */
byte rmodel;     /* model epoch of the remote (bumped by reassignment only) */
byte vrev;       /* revision the cached listing carried (maintained only with XREV or STAMP) */
byte age;        /* ticks since the cache was synced, saturating at TTL */
bit fetching;
byte fleft;
bit forced;
byte since = CAP; /* ticks since the last operator change, saturating at CAP */
byte ssv;        /* sends built from a stale view since the last operator change, saturating at CAPC */
byte consec;     /* consecutive refused sends since the last operator change or success (CAPC) */
byte lastact;    /* 0 idle, 1 peek, 2 send */
byte lastres;    /* 0 none, 1 ok, 2 refused */
bit udone;
bit inflight;
/* the request in flight */
byte rl;         /* 0 no level, 1 lvA, 2 lvB, 3 off */
bit ri;
bit rs;
byte xrev;
bit rsg[4];      /* which history signatures (by the row revision of issue) the request carries */
bit beq;         /* ghost: the view equalled the published listing when the request was built */
byte chgb;       /* ghost: row revision when the request was built */
byte kind;
/* the history: hsig[r] = 0 none, 1 signature of the current model, 2 of an older model */
byte hsig[4];
/* ghosts for reachability */
bit gfs;         /* a generation failed although the view was stale at build */
bit gff;         /* a generation failed although the view dimensions were equal at build */
bit fval;        /* a request that would have succeeded was refused by the revision gate */
bit fdrop;       /* a signature that was still valid was dropped by the client (SIGFIX 1) */
bit sigref;      /* a signature refusal happened */
bit k2; bit k3; bit k4;  /* ghosts: a LEVEL, an IMAGE, an OVERFLOW refusal happened */
bit eqb;         /* derived: the view equals the published listing (EQ), kept for LTL atoms */

#define LVD(l) (((l)==1)*0 + ((l)==2)*3 + ((l)==3)*4)
#define V(d)   (ovs[d]*ovv[d] + (1-ovs[d])*cache[d])
#define PUB0   (BUGLIST*(1-rem[0]) + (1-BUGLIST)*rem[0])
#define EQ     (V(0)==PUB0 && V(1)==rem[1] && V(2)==rem[2] && V(3)==rem[3] && V(4)==rem[4])
#define OVRKEEP (ovv[0]==ovv0[0] && ovv[1]==ovv0[1] && ovv[2]==ovv0[2] && ovv[3]==ovv0[3] && ovv[4]==ovv0[4])
#define SENTBAD ((rsg[0] && hsig[0]==2) || (rsg[1] && hsig[1]==2) || (rsg[2] && hsig[2]==2) || (rsg[3] && hsig[3]==2))
#define STARTFETCH if :: !fetching -> fetching = 1; fleft = FL :: else -> skip fi
#define INV(r)  if :: hsig[r] == 1 -> hsig[r] = 2 :: else -> skip fi
#define COPY(d) cache[d] = rem[d]; if :: (BUGOVR && ovs[d]) -> ovv[d] = cache[d] :: else -> skip fi
#define SIGSEL(r) \
  if \
  :: (SIGFIX == 1 && hsig[r] != 0 && r == vrev) -> rsg[r] = 1 \
  :: (SIGFIX == 1 && hsig[r] == 1 && r != vrev) -> rsg[r] = 0; if :: GHOST -> fdrop = 1 :: else -> skip fi \
  :: (SIGFIX != 1 && hsig[r] != 0) -> rsg[r] = 1 \
  :: else -> rsg[r] = 0 \
  fi

/* eqb mirrors EQ, because a formula atom cannot use function-like macros */
inline seteq() { if :: EQ -> eqb = 1 :: else -> eqb = 0 fi }

inline ttlcheck() {
  if
  :: (TTLREF && age >= TTL && !fetching) -> fetching = 1; fleft = FL
  :: else -> skip
  fi
}

/* The operator, one atomic step per possible change: flip one bit (narrow or widen), or REASSIGN the
   alias (any pattern of the first min(ND,3) bits, the model epoch moves, signatures of the old model
   become stale). The choice is the choice of the transition, so no intermediate state exists. */
proctype Operator() {
  do
#if ND > 0
  :: rrev < NOP -> d_step { rem[0] = 1 - rem[0]; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
#endif
#if ND > 1
  :: rrev < NOP -> d_step { rem[1] = 1 - rem[1]; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
#endif
#if ND > 2
  :: rrev < NOP -> d_step { rem[2] = 1 - rem[2]; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
#endif
#if ND > 3
  :: rrev < NOP -> d_step { rem[3] = 1 - rem[3]; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
#endif
#if ND > 4
  :: rrev < NOP -> d_step { rem[4] = 1 - rem[4]; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
#endif
#if ND == 1 || (ND > 3 && 1 == 3)
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 0; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 1; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
#endif
#if ND == 2 || (ND > 3 && 2 == 3)
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 0; rem[1] = 0; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 0; rem[1] = 1; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 1; rem[1] = 0; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 1; rem[1] = 1; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
#endif
#if ND == 3 || (ND > 3 && 3 == 3)
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 0; rem[1] = 0; rem[2] = 0; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 0; rem[1] = 0; rem[2] = 1; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 0; rem[1] = 1; rem[2] = 0; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 0; rem[1] = 1; rem[2] = 1; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 1; rem[1] = 0; rem[2] = 0; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 1; rem[1] = 0; rem[2] = 1; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 1; rem[1] = 1; rem[2] = 0; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
  :: rrev < NOP -> d_step { if :: SIGS -> rmodel++ :: else -> skip fi; INV(0); INV(1); INV(2); INV(3); rem[0] = 1; rem[1] = 1; rem[2] = 1; rrev++; since = 0; consec = 0; ssv = 0; seteq() }
#endif
  :: rrev >= NOP -> break
  od
}

/* A listing fetch completes at any time while it runs; it samples the remote at that instant. */
proctype Fetcher() {
end:
  do
  :: fetching -> d_step {
       if :: BUGLIST -> cache[0] = 1 - rem[0] :: else -> cache[0] = rem[0] fi;
       if :: (BUGOVR && ovs[0]) -> ovv[0] = cache[0] :: else -> skip fi;
       COPY(1); COPY(2); COPY(3); COPY(4);
       if :: (XREV || STAMP) -> vrev = rrev :: else -> skip fi;
       age = 0; fetching = 0; forced = 0; seteq()
#ifdef OVK
       ; assert(OVRKEEP)
#endif
     }
  od
}

proctype Tick() {
end:
  do
  :: udone && !inflight && !(fetching && fleft == 0) -> d_step {
       udone = 0;
       if :: age < TTL -> age++ :: else -> skip fi;
       if :: (fetching && fleft > 0) -> fleft-- :: else -> skip fi;
       if :: since < CAP -> since++ :: else -> skip fi
#ifdef SB
       /* bound on the staleness of the view, in ticks after the last operator change */
       ; assert(EQ || since <= SB)
#endif
     }
  od
}

#ifdef SK
#define SKCHECK assert(ssv <= SK);
#else
#define SKCHECK
#endif
#define SEND(L,I,Z) d_step { \
  ttlcheck(); \
  if \
  :: (L == 1 && V(0)) -> rl = 1 \
  :: (L == 2 && V(3)) -> rl = 2 \
  :: (L == 3 && V(4)) -> rl = 3 \
  :: else -> rl = 0 \
  fi; \
  if :: (I == 1 && V(2)) -> ri = 1 :: else -> ri = 0 fi; \
  if :: (Z == 1 && V(1)) -> rs = 1 :: else -> rs = 0 fi; \
  xrev = vrev; \
  beq = EQ; \
  if :: (!EQ && ssv < CAPC) -> ssv++ :: else -> skip fi; \
  SKCHECK \
  chgb = rrev; \
  SIGSEL(0); SIGSEL(1); SIGSEL(2); SIGSEL(3); \
  if :: LIVE -> lastact = 2 :: else -> skip fi; \
  udone = 1; inflight = 1 }

proctype User() {
end:
  do
  :: !udone && !inflight && !(AWAIT && forced && fetching) ->
       if
       :: d_step { ttlcheck(); if :: LIVE -> lastact = 1 :: else -> skip fi; udone = 1 }
       :: (!ACTIVE) -> d_step { if :: LIVE -> lastact = 0 :: else -> skip fi; udone = 1 }
       :: SEND(0,0,0)
       :: SEND(1,0,0)
#if ND > 1
       :: SEND(0,0,1)
       :: SEND(1,0,1)
#endif
#if ND > 2
       :: SEND(0,1,0)
       :: SEND(1,1,0)
       :: SEND(0,1,1)
       :: SEND(1,1,1)
#endif
#if ND > 3
       :: SEND(2,0,0)
       :: SEND(2,0,1)
       :: SEND(2,1,0)
       :: SEND(2,1,1)
#endif
#if ND > 4
       :: SEND(3,0,0)
       :: SEND(3,0,1)
       :: SEND(3,1,0)
       :: SEND(3,1,1)
#endif
       fi
  :: inflight -> d_step {
       /* the remote validates against its CURRENT row, then generates */
       kind = 0;
       if
       :: (XREV && xrev != rrev) ->
            kind = 1;
            if
            :: (GHOST && !(rl > 0 && rem[LVD(rl)] == 0) && !(ri && rem[2] == 0) && !(rs > rem[1]) && !(SIGS && SIGFIX != 2 && SENTBAD)) -> fval = 1
            :: else -> skip
            fi
       :: else ->
            if
            :: (rl > 0 && rem[LVD(rl)] == 0) -> kind = 2
            :: else ->
                 if
                 :: (ri && rem[2] == 0) -> kind = 3
                 :: else ->
                      if
                      :: (rs > rem[1]) -> kind = 4
                      :: else -> if :: (SIGS && SIGFIX != 2 && SENTBAD) -> kind = 5 :: else -> skip fi
                      fi
                 fi
            fi
       fi;
       /* P1: a request built from a view equal to the published listing, with no operator change
          since, is never refused for a capability reason */
       assert(!(kind >= 2 && kind <= 4 && beq && rrev == chgb));
       if
       :: kind == 0 ->
            consec = 0;
            if :: LIVE -> lastres = 1 :: else -> skip fi;
            if :: SIGS -> hsig[rrev] = 1 :: else -> skip fi
       :: else ->
            if :: LIVE -> lastres = 2 :: else -> skip fi;
            if :: consec < CAPC -> consec++ :: else -> skip fi;
            if :: (GHOST && kind == 5) -> sigref = 1 :: else -> skip fi;
            if :: (GHOST && kind == 2) -> k2 = 1 :: (GHOST && kind == 3) -> k3 = 1 :: (GHOST && kind == 4) -> k4 = 1 :: else -> skip fi;
            if :: (GHOST && kind >= 4 && !beq) -> gfs = 1 :: else -> skip fi;
            if :: (GHOST && kind >= 4 && beq) -> gff = 1 :: else -> skip fi;
            if :: ONREFUSE -> forced = 1; STARTFETCH :: else -> skip fi
       fi;
       if :: (STAMP && vrev != rrev) -> forced = 1; STARTFETCH :: else -> skip fi;
#ifdef CK
       /* bound on the refusals the user sees in a row after one operator change */
       assert(consec <= CK);
#endif
       inflight = 0;
       rl = 0; ri = 0; rs = 0; xrev = 0; beq = 0; chgb = 0; kind = 0;
       rsg[0] = 0; rsg[1] = 0; rsg[2] = 0; rsg[3] = 0
     }
  od
}

init {
  byte d;
  d = 0;
  do
  :: d < ND ->
       if :: rem[d] = 0 :: rem[d] = 1 fi;
       cache[d] = rem[d];
       if
       :: (PICKCOPY) -> ovs[d] = 1; ovv[d] = rem[d]
       :: (!PICKCOPY && OVR) -> ovs[d] = 1; if :: ovv[d] = 0 :: ovv[d] = 1 fi
       :: (!PICKCOPY && OVR) -> ovs[d] = 0
       :: (!PICKCOPY && !OVR) -> skip
       fi;
       ovv0[d] = ovv[d];
       d++
  :: else -> break
  od;
  seteq();
  run Operator(); run Fetcher(); run Tick(); run User()
}
