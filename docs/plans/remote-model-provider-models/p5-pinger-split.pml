/* p5-pinger-split.pml - p5-pinger.pml with the node's handler of a ping as a process of its own (phase 4a, P1 and P2).

   Why: in p5-pinger.pml the handler's steps run to completion inside the pinger's process, so the client's stop (which cancels the pinger's
   context and waits for the goroutine) cannot return while a handler is half-way. In the code the server's handler is the node's goroutine:
   the client's cancel frees p.hc.Do and the join at once, and the handler can still be waiting for the probe's lock or running ping().
   This model spawns Handler(i) per ping (Pinger does not wait for it; the answer is an array slot it may read later or never), so a handler
   that outlives the client's stop, or runs while another handler does, is an interleaving.

   Everything else is p5-pinger.pml, made from it by script: the same options, the same latches, the same mutants (see there). The sanity
   ghosts of the guard are left out (they multiply the states) and two are added for the split: sLateHandler (a handler finished after the
   client's stop returned) and sHandlerAfterPinger (a handler finished after the pinger returned). Its TEV defaults to 2 (K = 2 needs one
   re-arm and the cut); run it with --budget-mem-mb 3000: about 4.1 million states for the code's configuration.
*/
#ifndef CHECKCUT
#define CHECKCUT 1
#endif
#ifndef CHECKSTOPPED
#define CHECKSTOPPED 1
#endif
#ifndef CHECKFRESH
#define CHECKFRESH 1
#endif
#ifndef UNLOCKEDSTOP
#define UNLOCKEDSTOP 0
#endif
#ifndef NOJOIN
#define NOJOIN 0
#endif
#ifndef RETURNAFTERCUT
#define RETURNAFTERCUT 1
#endif
#ifndef STOP404
#define STOP404 1
#endif
#ifndef CWAIT
#define CWAIT 1
#endif
#ifndef NODELETE
#define NODELETE 0
#endif
#ifndef CUTOUTSIDE
#define CUTOUTSIDE 0
#endif
#ifndef ARMEDEARLY
#define ARMEDEARLY 0
#endif
#ifndef WAITFIRST
#define WAITFIRST 0
#endif
#ifndef NOCANCEL
#define NOCANCEL 0
#endif
#ifndef TEV
#define TEV 2
#endif
#ifndef K
#define K 2
#endif

bit idReg = 1;                 /* the id is registered                                     */
bit armed, stopped, cut, lastFresh;
bit lock;                      /* the probe's mutex (held while a step says so)            */
bit stopCh, done, runAlive;
bit timerEv;                   /* the timer has fired and not yet been received            */
bit timerOn;                   /* the timer is pending: armed or Reset, and not yet fired  */
byte timerEvents;              /* how many times the environment fired it                  */
bit callCtx;                   /* the node call's context is cancelled                     */
byte cancels;
bit providerDone, callEnded, stopReturned;
bit cctx, pingerDone, clientStopped;
byte pings;
bit sent404;

bit gAck204Cut, gIgnored, gCancelTwice, gCancelLate, gIdLeak, gRunLeak, gArmAfterStop, gPingerLeak, gAfter404, gCutNoCancel;
bit sLateHandler, sHandlerAfterPinger;
byte ans[4];                   /* the answer of the handler of ping i: 0 not yet, 2 a 204, 4 a 404 */

inline latch() {
#ifdef ASSERT_PROP
    assert(ASSERT_PROP == 0);
#endif
    skip
}

/* the guard goroutine; started by the first accepted ping (runAlive is set by the starter, so a leak is seen from the spawn on) */
proctype Run() {
    do
    :: stopCh ->
         /* the timer is stopped and the goroutine returns */
         atomic { done = 1; runAlive = 0 }; break
    :: timerEv ->
         timerEv = 0;
         atomic { lock == 0 -> lock = 1 };
         if
         :: stopped ->
              /* nothing to decide: the call is over (the code's elapsed >= limit && !stopped fails); the timer is Reset */
              timerOn = 1; lock = 0
         :: else ->
              if
              :: CHECKFRESH == 1 && lastFresh -> lastFresh = 0; timerOn = 1; lock = 0     /* re-arm for what is left: Reset */
              :: else ->
#if CUTOUTSIDE == 1
                   lock = 0;                      /* the mutant: the decision was taken under the lock, cut is set after it */
#endif
                   cut = 1; if :: lastFresh -> gIgnored = 1 :: else -> skip fi;
                   #if CUTOUTSIDE == 0
                   lock = 0;
#endif
                   /* the cancel, after the unlock */
                   atomic {
                       cancels++;
#if NOCANCEL == 0
                       callCtx = 1;
#endif
                       if :: cancels > 1 -> gCancelTwice = 1 :: else -> skip fi;
                       if :: stopReturned -> gCancelLate = 1 :: else -> skip fi
                   };
                   latch();
#if RETURNAFTERCUT == 1
                   atomic { done = 1; runAlive = 0 }; break
#elif RETURNAFTERCUT == 2
                   timerOn = 1             /* the return dropped and the Reset kept: the timer fires again */
#else
                   skip
#endif
              fi
         fi;
         latch()
    od
}

/* the environment fires the timer: it may fire any number of times (up to a bound) while the guard is armed and has not returned */
proctype Env() {
    do
    :: timerOn && !done && !timerEv && timerEvents < TEV -> atomic { timerEv = 1; timerOn = 0; timerEvents++ }
    :: done || timerEvents >= TEV || callEnded -> break
    od
}

/* the node's call: the provider, then the deferred stop() */
proctype NodeCall() {
    bit wasStopped, wasArmed;
    do
    :: callCtx -> providerDone = 1; break
    :: !callCtx && !providerDone -> providerDone = 1; break
    od;
    /* stop(): forget the id, mark stopped under the lock, close the stop channel, wait for run() */
#if NODELETE == 0
    atomic { idReg = 0 };
#endif
#if ARMEDEARLY == 1
    wasArmed = armed;                 /* the mutant: read before the lock */
#endif
    atomic { lock == 0 -> lock = 1 };
#if ARMEDEARLY == 1
    wasStopped = stopped; stopped = 1;
#else
    wasStopped = stopped; stopped = 1; wasArmed = armed;
#endif
#if UNLOCKEDSTOP == 0
    lock = 0;
#endif
    if
    :: !wasStopped && wasArmed ->
         stopCh = 1;
#if NOJOIN == 0
         done == 1;
#endif
         skip
    :: else -> skip
    fi;
#if UNLOCKEDSTOP == 1
    lock = 0;
#endif
    atomic {
        stopReturned = 1;
        if :: idReg -> gIdLeak = 1 :: else -> skip fi;
        if :: runAlive -> gRunLeak = 1 :: else -> skip fi;
        if :: cut && !callCtx -> gCutNoCancel = 1 :: else -> skip fi
    };
    callEnded = 1;
    latch()
}

/* the client's pinger goroutine */
/* the node's handler of one ping: its own goroutine, it runs to its end whatever the client does meanwhile */
proctype Handler(byte i) {
    bit seen;
    byte resp;
    atomic { seen = idReg };
    if
    :: !seen -> resp = 4
    :: else ->
         atomic { lock == 0 -> lock = 1 };
         atomic {
             if
             :: CHECKCUT == 1 && cut -> resp = 4
             :: CHECKSTOPPED == 1 && stopped -> resp = 4
             :: else ->
                  if
                  :: !armed ->
                       armed = 1; timerOn = 1; lastFresh = 0; runAlive = 1;
                       if :: stopped -> gArmAfterStop = 1 :: else -> skip fi;
                       run Run()
                  :: else -> lastFresh = 1
                  fi;
                  resp = 2
             fi;
             if :: resp == 2 && cut -> gAck204Cut = 1 :: else -> skip fi
         };
         lock = 0
    fi;
    atomic {
        ans[i] = resp;
        if :: clientStopped -> sLateHandler = 1 :: else -> skip fi;
        if :: pingerDone -> sHandlerAfterPinger = 1 :: else -> skip fi
    };
    latch()
}

/* the client's pinger goroutine */
proctype Pinger() {
    byte me;
    do
    :: cctx -> atomic { pingerDone = 1 }; break
    :: !cctx && pings < K ->
         pings++; me = pings;
         if :: sent404 -> gAfter404 = 1 :: else -> skip fi;
         /* the request reaches a handler, even if the client abandons it meanwhile; the client does not wait for it */
         run Handler(me);
         /* the answer: delivered once the handler has produced it, lost, or abandoned because the client cancelled */
         if
         :: cctx -> skip
         :: true -> skip
         :: !cctx && ans[me] != 0 ->
              if
              :: ans[me] == 4 ->
                   sent404 = 1;
#if STOP404 == 1
                   atomic { pingerDone = 1 }; break
#else
                   skip
#endif
              :: else -> skip
              fi
         fi
    :: !cctx && pings >= K -> cctx == 1
    od
}

/* the client's stream: it ends when the provider completes or the call is cut, then the deferred stopProbe cancels the pinger and waits for it */
proctype Client() {
    do
    :: callCtx -> break         /* the cut: the stream ends with the terminal frame */
    :: providerDone -> break    /* the final frame */
    :: true -> break            /* the client's own end: a cancel, a read error */
    od;
#if WAITFIRST == 1
    pingerDone == 1;                  /* the mutant: wait, then cancel */
#endif
    cctx = 1;
#if CWAIT == 1 && WAITFIRST == 0
    pingerDone == 1;
#endif
    if :: !pingerDone -> gPingerLeak = 1 :: else -> skip fi;
    clientStopped = 1;
    latch()
}

init {
    atomic { run NodeCall(); run Pinger(); run Client(); run Env() }
}
