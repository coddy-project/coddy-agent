/* p4-probe.pml - the application probe of a vanished peer (phase 4a, dilemmas P1 to P4 of
   docs/plans/remote-model-provider-probe.md).

   Decides  P1 who is cut (STRICT), P2 when the guard is armed (ARM), P3 how long the grace G is against the ping interval I
            (G, found as a boundary), P4 what proves a ping (AUTH against ATT), and checks the exactly-once release (ONCE).
   Code     external/httpserver (the call's alive guard, POST /coddy/llm/calls/{id}/alive), internal/llm (the client's pings);
            to be written after this model, the tests of the plan's section 5 are the conformance check.
   Class    finite, discrete time. TIME IS A VARIABLE (`now`, ticks): a process acts at the tick at which its action is due and
            the clock advances only when NO process has an enabled action at the current tick. Not a timed automaton.

   World (one call, one node, one client)
     client   opted in or not (OPT); an opted-in client, once confirmed, sends a ping every I ticks while the call runs. Each ping is
              lost (at most L in a row) or delivered after 0..DMAX ticks. The client vanishes at a tick chosen at the start (VAN) or
              never: from then on it sends nothing, and the node has no other way to see it (an intermediary acknowledges bytes).
     node     one call holding one slot. The provider completes the call at a tick chosen at the start, or never. The alive guard,
              when on, cancels the call at the first tick at which more than G ticks have passed since the guard's reference:
              the call's start (ARM 0) or the first delivered ping (ARM 1), re-armed by every accepted ping. Cancel and completion
              both release the slot through release().
     attacker (ATT) sends one ping at a tick chosen at the start: 0 knows nothing (no id), 1 knows the id but holds another credential,
              2 knows the id and holds the call's credential (a relay's clients share the node's credential).

   Options (the dilemmas)
     STRICT 0 only a call that opted in has a guard (the plan's answer), 1 every call has one            (P1)
     ARM    0 the guard is armed at the call's start (the plan's answer), 1 at the first accepted ping   (P2)
     G      the grace in ticks; the plan expects the boundary G = (L + 1) * I + DMAX                      (P3)
     AUTH   1 a ping needs the id and the credential (direct), 0 the id alone                             (P4)
     ONCE   1 release() is guarded (a slot is released once), 0 every path releases                       (S4)
     NODE   1 the node supports the probe, 0 an old node (never confirms, so never a guard and never a ping)

   Properties (ghost latches, monotone; one is asserted per run with -D ASSERT_PROP=<latch>, expected to stay 0)
     S1 gFalseCut  the guard cancelled a call whose client had not vanished
     S2 gLate      an opted-in client vanished and its call still held the slot more than DMAX + G + 1 ticks later (the provider
                   not having completed it)
     S3 gCutNonOpt the guard cancelled a call that had not opted in
     S4 gDouble    the slot was released more than once
     S5 gForeign   an attacker's accepted ping kept a call alive whose client had vanished
     sanity        sCut (the guard can fire), sDel (a ping can be delivered), sLost (a ping can be lost), sVan, sDone

   Abstractions (what the model is not): one call; a ping is one of loss or a delay in 0..DMAX with no reordering (a later ping may
     overtake an earlier one only through the delay choice, which is allowed); the id is a boolean (guessing it is not modelled, 128
     random bits); the stream's own heartbeat and the transport bound are not modelled (the probe is additional); the client's
     goroutine is never late by itself (its lateness is part of DMAX).
*/

#ifndef STRICT
#define STRICT 0
#endif
#ifndef ARM
#define ARM 0
#endif
#ifndef I
#define I 3
#endif
#ifndef L
#define L 1
#endif
#ifndef DMAX
#define DMAX 2
#endif
#ifndef G
#define G 8
#endif
#ifndef AUTH
#define AUTH 1
#endif
#ifndef ONCE
#define ONCE 1
#endif
#ifndef OPT
#define OPT 1
#endif
#ifndef NODE
#define NODE 1
#endif
#ifndef VAN
#define VAN 1
#endif
#ifndef ATT
#define ATT 0
#endif
#ifndef T
#define T 24
#endif

byte now;
byte vAt, compAt, aAt;          /* the tick the client vanishes, the provider completes, the attacker pings; T+1 is never */
bit vanished, state_ended;      /* state_ended: the call no longer holds the slot                                          */
byte rel;                       /* releases                                                                                 */
bit guardOn, guardFired, compDone, aDone;
byte deadline;
byte nextSend, lostRun;
byte infl[3];                   /* delivery ticks of the pings in flight, 255 empty                                         */

bit gFalseCut, gLate, gCutNonOpt, gDouble, gForeign;
bit sCut, sDel, sLost, sVan, sDone;

#define CONFIRMED (OPT == 1 && NODE == 1)
#define RUNNING   (state_ended == 0)

inline release() {
#if ONCE == 1
    if :: state_ended == 0 -> state_ended = 1; rel++ :: else -> skip fi
#else
    state_ended = 1; rel++
#endif
    ;
    if :: rel > 1 -> gDouble = 1 :: else -> skip fi
}

inline checkLate() {
    if
    :: CONFIRMED && vanished && RUNNING && !compDone && now > vAt + DMAX + G + 1 -> gLate = 1
    :: else -> skip
    fi
}

inline latch() {
    checkLate();
#ifdef ASSERT_PROP
    assert(ASSERT_PROP == 0);
#endif
    skip
}

inline slotFree(k) {
    k = 0;
    do
    :: k < 3 && infl[k] != 255 -> k++
    :: k < 3 && infl[k] == 255 -> break
    :: k >= 3 -> break
    od
}

proctype Client() {
    byte d, k;
    do
    :: atomic {
         CONFIRMED && !vanished && RUNNING && now >= nextSend && now <= T ->
         nextSend = now + I;
         if
         :: lostRun < L -> lostRun++; sLost = 1          /* this ping is lost; at most L in a row */
         :: true ->                                       /* or delivered after 0..DMAX ticks */
              d = 0;
              do :: d < DMAX -> d++ :: break od;
              slotFree(k);
              if :: k < 3 -> infl[k] = now + d; lostRun = 0 :: else -> skip fi
         fi;
         latch()
       }
    :: now >= T -> break
    od
}

proctype Node() {
    byte k;
    do
    :: atomic {   /* a ping due now reaches the node */
         (infl[0] == now || infl[1] == now || infl[2] == now) ->
         if :: infl[0] == now -> infl[0] = 255 :: infl[1] == now -> infl[1] = 255 :: else -> infl[2] = 255 fi;
         sDel = 1;
         if
         :: RUNNING ->
              if :: ARM == 1 -> guardOn = 1 :: else -> skip fi;
              deadline = now + G
         :: else -> skip
         fi;
         latch()
       }
    :: atomic {   /* the attacker's ping */
         ATT > 0 && !aDone && now == aAt ->
         aDone = 1;
         if
         :: RUNNING && (AUTH == 0 || ATT == 2) ->
              deadline = now + G;
              if :: ARM == 1 -> guardOn = 1 :: else -> skip fi;
              if :: vanished -> gForeign = 1 :: else -> skip fi
         :: else -> skip
         fi;
         latch()
       }
    :: atomic {   /* the alive guard */
         guardOn && !guardFired && RUNNING && now > deadline ->
         guardFired = 1; sCut = 1;
         if :: !vanished -> gFalseCut = 1 :: else -> skip fi;
         if :: OPT == 0 -> gCutNonOpt = 1 :: else -> skip fi;
         release();
         latch()
       }
    :: atomic {   /* the provider completes the call */
         !compDone && now >= compAt && now <= T ->
         compDone = 1; sDone = 1;
         release();
         latch()
       }
    :: now >= T -> break
    od
}

proctype Clock() {
    do
    :: atomic {
         now < T &&
         !(CONFIRMED && !vanished && RUNNING && now >= nextSend) &&
         !(infl[0] == now || infl[1] == now || infl[2] == now) &&
         !(ATT > 0 && !aDone && now == aAt) &&
         !(guardOn && !guardFired && RUNNING && now > deadline) &&
         !(!compDone && now >= compAt) ->
         now++;
         if :: now == vAt -> vanished = 1; sVan = 1 :: else -> skip fi;
         latch()
       }
    :: now >= T -> break
    od
}

init {
    byte x;
    atomic {
        infl[0] = 255; infl[1] = 255; infl[2] = 255;
        deadline = G;
#if STRICT == 1
        guardOn = (NODE == 1);
#else
        guardOn = (ARM == 0 && CONFIRMED);
#endif
    }
    /* the tick the client vanishes, the provider completes, the attacker pings: each chosen once, T+1 means never */
    x = 0; do :: x < T + 1 -> x++ :: break od; compAt = x;
#if VAN == 1
    x = 0; do :: x < T + 1 -> x++ :: break od; vAt = x;
#else
    vAt = T + 1;
#endif
#if ATT > 0
    x = 0; do :: x < T + 1 -> x++ :: break od; aAt = x;
#else
    aAt = T + 1;
#endif
    atomic {
        if :: vAt == 0 -> vanished = 1; sVan = 1 :: else -> skip fi;
        run Client(); run Node(); run Clock()
    }
}
