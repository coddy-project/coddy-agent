/* p5-pinger.pml - the application probe's two goroutines and the handler that joins them: the client's pinger and the node's guard
   (phase 4a, P1 and P2). The sequential design is p4-probe (arming, the grace, the tie); this model holds what that one abstracts away:
   the concurrency of the code.

   Code     external/httpserver/shared_probe.go (sharedProbe: ping() under the probe's lock answers false once the guard has cut or the call has
            stopped, arms the guard at the first accepted ping and starts run(); run() waits on the stop channel or the timer, and on the timer
            decides under the lock (a ping since the last arming re-arms, else it cuts unless stopped) and then, after the unlock, cancels the
            call; stop() forgets the id, marks stopped, closes the stop channel and waits for run() when it was armed; llmAlivePost: the id's
            lookup (a step of its own), the credential check, then ping()), internal/llm/coddy_probe.go (startProbe: a goroutine pings at once
            and on a ticker until its context is cancelled or a ping is answered 404 unknown_call; the returned stop() cancels the context and
            waits for the goroutine; attempt defers it, so a stream that ends for any reason ends the pinger),
            external/httpserver/shared_models_http.go (runSharedCall: the provider call, then the deferred stop()).
   Class    finite, no real time: the timer and the ticker are nondeterministic events; every interleaving of the steps is explored.

   World
     node call   the provider runs until it completes or the guard cancels the call; then the deferred stop() runs.
     guard       run(): select { the stop channel closed | the timer fired }; on the timer, under the lock: a ping since the last arming
                 (lastFresh; the arming ping itself is not one: the timer measures from it) re-arms, otherwise, unless stopped, the call is
                 cut; the cancel is called AFTER the unlock, as a step of its own.
     handler     the ping, in two steps: the id's lookup (is it still registered), then the lock: a cut or stopped call answers 404, else the
                 guard is armed (the first time) or refreshed and the answer is 204. stop() can run between the two steps.
     client      the pinger goroutine sends up to K pings; a request reaches the handler even if the client abandons it meanwhile (the answer is
                 then lost); an answer may be lost; a delivered 404 ends the pinger; the stream's end (the call was cut, or the provider
                 completed) cancels its context and waits for it.

   Options (the code is the default of each; a mutant changes one rule)
     CHECKCUT 1       the handler refuses a call the guard has cut (0: it answers 204 anyway)
     CHECKSTOPPED 1   the handler refuses a call that has stopped (0: it arms the guard of a call that is over)
     CHECKFRESH 1     the guard re-arms when a ping arrived since it was armed (0: it cuts at the first timer: the stale-timer race)
     UNLOCKEDSTOP 0   stop() waits for run() while holding the lock (1: a mutant that deadlocks with a run() that needs the lock)
     NOJOIN 0         stop() waits for run() (1: it does not wait: a goroutine may outlive the call)
     RETURNAFTERCUT 1 run() returns after the cut (0: it goes on, but a Go timer is one-shot and fires no more, so it cannot cancel again: an equivalent
                      mutant; 2: it goes on and its timer fires again, which is the return dropped and the Reset kept: it may cancel twice)
     NOCANCEL 0       the guard cuts and calls cancel (1: it sets cut and never cancels the call)
     STOP404 1        a delivered 404 ends the pinger (0: it keeps pinging)
     CWAIT 1          the client's stop waits for the pinger (0: it does not)
     NODELETE 0       stop() forgets the id (1: it does not: the registry keeps the call)
     CUTOUTSIDE 0     the guard sets cut under the lock together with its decision (1: it decides under the lock, unlocks, and sets cut after)
     ARMEDEARLY 0     stop() reads armed under the lock (1: it reads it before taking the lock)
     WAITFIRST 0      the client's stop cancels the pinger, then waits (1: it waits first, then cancels)
     TEV 3            timer events the environment fires at most (K = 3 needs 3: two re-arms and the cut)
     K                pings the client sends at most

   Properties (ghost latches, one asserted per run with -D ASSERT_PROP=<latch>, expected to stay 0; deadlock is checked in every run: every
     loop of the model is bounded (K pings, TEV timer events), so the state graph is finite and acyclic and "no invalid end state" means every
     goroutine terminates: a stop() or a stopProbe() that cannot return, a goroutine that is never released, is an invalid end state)
     N1 gAck204Cut     a ping was accepted (its 204 decided) on a call the guard had already cut. The 204 may still be WRITTEN after a cut
                       that follows the acceptance: that is harmless and reachable (s204AfterCut)
     N2 gIgnored       a timer event cut a call that had a ping accepted since the last arming. It assumes the timer is handled before the latest ping
                       is a grace old: a guard that is late may correctly cut by the code's elapsed arithmetic (elapsed >= limit), which p4-probe
                       owns and this model has no clock for (every outcome stays reachable: sCutAfterRefresh)
     N3 gCancelTwice   the guard's cancel ran twice
     N7 gCutNoCancel   the guard cut the call and it was never cancelled (the other half of "a cut cancels": N3 says at most once, N7 says at least once)
     N3b gCancelLate   the guard's cancel ran after stop() returned
     N4 gIdLeak        the id is still registered after stop() returned
     N5 gRunLeak       the guard goroutine is still alive after stop() returned
     N6 gArmAfterStop  the guard was armed by a call that had already stopped
     C1 gPingerLeak    the pinger goroutine is still alive after the client's stop returned
     C2 gAfter404      the pinger sent a ping after it was told 404
     N1, N2, C1 and C2 are true by the construction of the rule the mutant of each switches: the mutants carry the content. N4 can fail only through
     NODELETE, N5 through NOJOIN, ARMEDEARLY or CHECKSTOPPED=0 (a late arming leaves a run() nothing joins), N7 through NOCANCEL. UNLOCKEDSTOP and WAITFIRST are caught by the deadlock check only.
     sanity            sCut, sArm, s204, s404, sComplete (the call completes before any cut), sInFlightCancel (a request reached the handler
                       after the client's cancel), sLost (an answer lost), sRearm (the timer found a fresh ping), sRace (the lookup found the
                       id and stop() ran before the locked step), sCutAfterRefresh (a call pinged after its arming was cut: the next re-arm
                       found no newer ping), s204AfterCut (a 204 delivered for a ping accepted before a cut)

   Abstractions (what the model is not): one call; the credential check of the ping (a handler that refuses is a 404 like the stopped case); a
     404 that is not unknown_call, and an error while the client is alive, are an answer lost (the pinger goes on); the network beyond "the answer
     is delivered or lost"; the clock (the timer's expiry is an event, the grace's length is p4-probe); the heartbeat and the write deadlines of
     the stream; Timer.Reset with the remaining time (one more timer event); the handler of a ping is part of the pinger's process, so a handler
     that outlives the client's stop is not an interleaving here (p5-pinger-split.pml has the handler as a process of its own); the ping request is
     assumed to end, by cancel or by the DMAX bound (context.WithTimeout(ctx, every/2) in coddy_probe.go ping): a regression that unbinds it is not
     expressible without a clock, and "no invalid end state" means every goroutine terminates only on that assumption; the pings are
     bounded by K = 2 and the timer events by TEV = 3 (K = 3 needs TEV = 3: two re-arms and the cut), and the latches were also run at K = 3.
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
#define TEV 3
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
bit sCut, sArm, s204, s404, sComplete, sInFlightCancel, sLost, sRearm, sRace, sCutAfterRefresh, s204AfterCut;
bit refreshed;                 /* a ping after the arming one was accepted (ghost) */

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
              :: CHECKFRESH == 1 && lastFresh -> lastFresh = 0; sRearm = 1; timerOn = 1; lock = 0     /* re-arm for what is left: Reset */
              :: else ->
#if CUTOUTSIDE == 1
                   lock = 0;                      /* the mutant: the decision was taken under the lock, cut is set after it */
#endif
                   cut = 1; sCut = 1;
                   if :: lastFresh -> gIgnored = 1 :: else -> skip fi;
                   if :: refreshed -> sCutAfterRefresh = 1 :: else -> skip fi;
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
    :: !callCtx && !providerDone -> providerDone = 1; sComplete = 1; break
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
proctype Pinger() {
    bit seen;
    byte resp;      /* 2 stands for a 204, 4 for a 404 unknown_call */
    do
    :: cctx -> atomic { pingerDone = 1 }; break
    :: !cctx && pings < K ->
         pings++;
         if :: sent404 -> gAfter404 = 1 :: else -> skip fi;
         /* the request reaches the handler, even if the client abandons it meanwhile */
         if :: cctx -> sInFlightCancel = 1 :: else -> skip fi;
         /* the handler: the id's lookup ... */
         atomic { seen = idReg };
         if
         :: !seen -> resp = 4
         :: else ->
              /* ... then ping() under the lock; stop() can have run in between */
              if :: !idReg -> sRace = 1 :: else -> skip fi;
              atomic { lock == 0 -> lock = 1 };
              atomic {
                  if
                  :: CHECKCUT == 1 && cut -> resp = 4
                  :: CHECKSTOPPED == 1 && stopped -> resp = 4
                  :: else ->
                       if
                       :: !armed ->
                            armed = 1; timerOn = 1; lastFresh = 0; runAlive = 1; sArm = 1;
                            if :: stopped -> gArmAfterStop = 1 :: else -> skip fi;
                            run Run()
                       :: else -> lastFresh = 1; refreshed = 1
                       fi;
                       resp = 2
                  fi;
                  if :: resp == 2 && cut -> gAck204Cut = 1 :: else -> skip fi
              };
              lock = 0
         fi;
         latch();
         /* the answer: delivered, lost, or abandoned because the client cancelled */
         if
         :: cctx -> skip
         :: true -> sLost = 1
         :: !cctx ->
              if
              :: resp == 4 ->
                   s404 = 1; sent404 = 1;
#if STOP404 == 1
                   atomic { pingerDone = 1 }; break
#else
                   skip
#endif
              :: else -> s204 = 1; if :: cut -> s204AfterCut = 1 :: else -> skip fi
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
