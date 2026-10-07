/* m2-busy-wait.pml - where the busy wait of a `coddy` provider lives.

   Spec: docs/plans/remote-model-provider.md, 4.1b (waiting for a slot, [decide at modeling]),
   4.3 (error table, "Classification and retries", the five predicates), 5a (Client rows), 6a.
   Code read: internal/llm/resilient.go (callWithRetry, isRetryableLLMError, httpStatusFromError,
   quotaReset, retryBudget, CallBudget), internal/agent/limit_wait.go, internal/agent/react.go
   (first-token timer ~l.898-915 and its handling ~l.1144-1166, turnProviderInput ~l.2671).

   CLASS. Finite, untimed engine run over an explicit DISCRETE-TIME abstraction (not a timed
   automaton): one tick is the unit of CB/CT/CH/RA/CEIL/FMAX; a request and its answer take 0 ticks,
   a sleep takes >= 1 tick, time to first token is Fd ticks, generation after it GEN ticks.
   ONE process (`world`) is the scheduler: per tick it applies, in a fixed order, (A) the user's
   Stop, (B) the first-token timer, (C) the reactions of agent -> wrapper -> provider -> remote.
   The only nondeterminism is the environment (init: slot-free tick S0, first-token time Fd, Stop
   tick ST; during the run: re-contention after our slot is released, upward jitter). Ties are
   resolved by the fixed order Stop > timer > first token; -D TIEFIRST=1 swaps timer and first token.
   Pitfall 9 of the skill (synchronous translation without a scheduler): the within-tick order is
   fixed and every tie between environment events is enumerated by the choice of the ticks, so no
   interleaving of the components inside a tick is lost that the property could observe.

   VARIANTS (-D VAR=...):
     0  P0   busy is a plain 429 `Retry-After: 1`; the outer resilient wrapper retries it by status.
     2  P2   the wait lives inside provider.Stream: one monotonic deadline pdl = start + Beff, sleeps
             RA, 2RA, ... up to CEIL (+ upward jitter, last sleep cut to the deadline), then an
             exhausted-wait error whose status is 0 (-D STATZERO=1) or 429 (-D STATZERO=0, the
             broken design: the wrapper retries it by status).
             -D TARM=0 first-token timer armed before the call (today); -D TARM=1 armed when a slot
             is held; -D CAP=1 Beff = min(CB, CT-CH).  P2cap of the brief = TARM=1 CAP=1.
     1  P1   the provider answers busy at once with a typed error (status 0 with STATZERO=1);
             the agent loop waits out the budget (sleeps, cancellable) and re-issues the call with
             a FRESH first-token timer, as limit_wait.go does for a quota reset; resetRetries on wake.
   MUTATIONS (anti-vacuity): STATZERO=0, NOCANCEL=1 (sleeps ignore Stop until they end),
     EXHTRANS=1 (the agent classes the exhausted error as transient: provider recovery re-waits, 2x),
     RECONTEND=1 (environment: after our slot is released another client may take it again),
     PREC_BROKEN=1 (precedence table: global wins).
   CONTEXT SWITCHES: HELPER=1 (a caller that is not the ReAct loop - compaction, direct HTTP completion,
     prompt enhancement - no agent loop, no first-token timer), NCALL=2 (two calls per turn),
     TURNLEDGER=1 (P1 budget taken from a per-turn ledger as limitWaitLedger does, not per call),
     REUSE=1 SUB=1 (P1 reusing limitResetToWaitFor as it is: a subagent, depth > 0, never waits).

   CONSTANTS (-D): CB budget (effective after precedence), CT first-token timeout, CH headroom,
     RMAX llm_retry_max, RA Retry-After, RB retry base, CEIL sleep ceiling, FMAX max first-token time
     tried, GEN generation ticks, SMAXE largest slot-free tick tried (and NEVER = beyond horizon),
     STMAX largest Stop tick tried, HOR horizon.  Default scale A: CB=4, CT=12 (1:3 as 30 s : 90 s),
     CH=1, RMAX=3, RA=1, RB=1, CEIL=2, FMAX=13, SMAXE=6, STMAX=20, HOR=100.  Spec ratios: CB=30 s,
     CT=90 s, CH=5 s, RA=1 s, CEIL=5 s.  Scales B and C are in m2-busy-wait.md.

   The (provider, global) precedence table is asserted in `init` (assert property, literals).
   Properties are passed on the command line, the list is in m2-busy-wait.md (atoms are globals):
   now over res reqs cuts exh exhreqs endt held busyspend ST S0 Fd cB cT busyseen stopinwait calls
   Srel started.  res codes: 0 none, 1 OK, 2 BUSYERR (busy error), 3 TOERR ("model did not respond"),
   4 CANCELLED.  The result mtype is a byte because the LTL parser does not resolve mtype names.
*/

#ifndef VAR
#define VAR 2
#endif
#ifndef TARM
#define TARM 0
#endif
#ifndef CAP
#define CAP 0
#endif
#ifndef STATZERO
#define STATZERO 1
#endif
#ifndef RECONTEND
#define RECONTEND 0
#endif
#ifndef NOCANCEL
#define NOCANCEL 0
#endif
#ifndef JIT
#define JIT 0
#endif
#ifndef EXHTRANS
#define EXHTRANS 0
#endif
#ifndef TIEFIRST
#define TIEFIRST 0
#endif
#ifndef PREC_BROKEN
#define PREC_BROKEN 0
#endif
#ifndef HELPER
#define HELPER 0
#endif
#ifndef NCALL
#define NCALL 1
#endif
#ifndef TURNLEDGER
#define TURNLEDGER 0
#endif
#ifndef REUSE
#define REUSE 0
#endif
#ifndef SUB
#define SUB 0
#endif
#ifndef CB
#define CB 4
#endif
#ifndef CT
#define CT 12
#endif
#ifndef CH
#define CH 1
#endif
#ifndef RMAX
#define RMAX 3
#endif
#ifndef RA
#define RA 1
#endif
#ifndef RB
#define RB 1
#endif
#ifndef CEIL
#define CEIL 2
#endif
#ifndef FMAX
#define FMAX 13
#endif
#ifndef GEN
#define GEN 1
#endif
#ifndef SMAXE
#define SMAXE 6
#endif
#ifndef STMAX
#define STMAX 20
#endif
#ifndef HOR
#define HOR 100
#endif

#define NEVER 200

/* phases of the single call */
#define PH_ISSUE 0
#define PH_PROV  1
#define PH_POLL  2
#define PH_SLEEP 3
#define PH_ERR   4
#define PH_RET   5
#define PH_FT    6
#define PH_GEN   7
/* who sleeps */
#define SL_PROV  0
#define SL_WRAP  1
#define SL_AGENT 2
#define SL_REC   3
/* kind of the error in flight */
#define E_NONE   0
#define E_BUSY429 1
#define E_BUSYTYPED 2
#define E_EXH    3

/* result of the call (formulas use the numbers: 0 none, 1 OK, 2 BUSYERR, 3 TOERR, 4 CANCELLED) */
#define R_NONE 0
#define R_OK 1
#define R_BUSYERR 2
#define R_TOERR 3
#define R_CANCELLED 4

byte now = 0;
byte ST = NEVER;       /* tick of the user's Stop; NEVER = none */
byte Fd = 0;           /* ticks from slot acquired to first token */
byte freeAt = 0;       /* remote: the slot is free for this key from this tick on */
byte S0 = 0;           /* first value of freeAt (the property antecedents read it) */
bit  held = 0;         /* we hold a slot of the remote */
byte cB = CB;
byte cT = CT;
byte Beff = 0;         /* effective busy budget after the (provider, global) precedence and CAP */
byte NREQ1 = 0;        /* requests of ONE uninterrupted wait, jitter-free schedule */

byte ph = PH_ISSUE;
byte sl = 0;
byte wake = 0;
byte etype = E_NONE;
bit  err429 = 0;       /* httpStatusFromError of the error in flight is 429 (else 0: not retryable) */
byte res = R_NONE;
bit over = 0;
byte endt = 0;
bit stopped = 0;
byte reqs = 0;         /* requests that reached the remote, whole call */
byte cuts = 0;         /* first-token timer cuts */
byte ftr = 0;          /* agent first-token retries used (max 1) */
byte att = 0;          /* wrapper attempt index */
byte allow = RMAX;     /* retry allowance shared by wrapper and agent */
byte busyspend = 0;    /* allowance units spent on busy */
byte k = 0;
byte pdl = 0;
byte exhreqs = 0;
bit  exh = 0;
byte recov = 0;
byte wstart = 0;
byte ak = 0;
byte adl = 0;
bit  adlset = 0;
byte ftick = 0;
byte gdone = 0;
byte tdl = 0;
bit  tarm = 0;
bit  emitted = 0;
bit  busyseen = 0;
bit  stopinwait = 0;
byte slots = 0;
bit  go = 0;
byte calls = 1;        /* index of the call of the turn being served (NCALL calls per turn) */
byte Srel = 0;         /* slot-free tick of the current call, relative to the clock at its start (NEVER = no) */
byte waited = 0;       /* ticks the agent loop has slept on busy this turn (P1, TURNLEDGER) */
bit  started = 0;      /* set once the environment constants are chosen (properties gate on it) */

/* sleep length: RA, 2RA, ... capped at CEIL (SPEC 4.1b); the second one adds upward jitter */
inline sched0(kk, dd) {
  dd = RA; tj = 0;
  do
  :: tj < kk && dd < CEIL -> dd = dd * 2; tj++
  :: else -> break
  od;
  if
  :: dd > CEIL -> dd = CEIL
  :: else -> skip
  fi
}
inline sched(kk, dd) {
  sched0(kk, dd);
#if JIT
  if
  :: skip
  :: dd = dd + 1
  fi
#endif
}

/* the remote frees our slot (client disconnect / provider return); with RECONTEND another client may take it */
inline release() {
  if
  :: held ->
       held = 0;
#if RECONTEND
       if
       :: freeAt = NEVER
       :: tj = 0; do :: tj < SMAXE -> tj++ :: break od; freeAt = now + tj
       fi
#endif
  :: else -> skip
  fi
}

/* effective budget from (provider busy_wait_ms, global agent.shared_busy_wait_ms); global absent = default */
inline precedence(p, gabsent, g, out) {
#if PREC_BROKEN
  if
  :: !gabsent -> out = g
  :: gabsent && p > 0 -> out = p
  :: gabsent && p == 0 -> out = 30
  fi
#else
  if
  :: p > 0 -> out = p
  :: p == 0 && gabsent -> out = 30
  :: p == 0 && !gabsent -> out = g
  fi
#endif
}

proctype world() {
  byte tj = 0; byte td = 0; byte tw = 0;
  do
  :: !over && now < HOR ->
     atomic {
       /* A. the user's Stop */
       if
       :: ST != NEVER && now >= ST && !stopped -> stopped = 1
       :: else -> skip
       fi;
       if
       :: stopped && !over && !(NOCANCEL && ph == PH_SLEEP && now < wake) ->
            if
            :: ph == PH_SLEEP -> stopinwait = 1
            :: else -> skip
            fi;
            release(); res = R_CANCELLED; over = 1; endt = now; tarm = 0
       :: else -> skip
       fi;
       /* B. the first-token timer cancels the stream context (react.go ~l.909, handled ~l.1147) */
       if
       :: !over && tarm && now >= tdl && !(TIEFIRST && ph == PH_FT && now >= ftick) ->
            cuts++; tarm = 0; release();
            if
            :: ftr < 1 && allow > 0 -> ftr++; allow--; ph = PH_ISSUE
            :: else -> res = R_TOERR; over = 1; endt = now
            fi
       :: else -> skip
       fi;
       /* C. reactions */
       go = 1;
       do
       :: go && !over ->
            go = 0;
            if
            :: ph == PH_ISSUE ->
                 /* the agent issues the call: arm the timer (react.go before the provider call) */
                 emitted = 0; att = 0; wstart = now;
                 if
                 :: HELPER -> tarm = 0
                 :: !HELPER && (TARM == 0 || VAR != 2) -> tarm = 1; tdl = now + CT
                 :: else -> tarm = 0
                 fi;
                 ph = PH_PROV; go = 1
            :: ph == PH_PROV ->
                 /* provider.Stream starts (the wrapper's attempt `att`) */
                 if
                 :: VAR == 2 -> pdl = now + Beff; k = 0
                 :: else -> skip
                 fi;
                 ph = PH_POLL; go = 1
            :: ph == PH_POLL ->
                 reqs++;
                 if
                 :: !held && now >= freeAt ->
                      held = 1; slots++; ftick = now + Fd;
                      if
                      :: VAR == 2 && TARM == 1 && !HELPER -> tarm = 1; tdl = now + CT
                      :: else -> skip
                      fi;
                      ph = PH_FT; go = 1
                 :: else ->
                      busyseen = 1;
                      if
                      :: VAR == 0 -> etype = E_BUSY429; err429 = 1; ph = PH_ERR; go = 1
                      :: VAR == 1 ->
                           etype = E_BUSYTYPED;
                           if
                           :: STATZERO -> err429 = 0
                           :: else -> err429 = 1
                           fi;
                           ph = PH_ERR; go = 1
                      :: VAR == 2 ->
                           if
                           :: now >= pdl ->
                                if
                                :: !exh -> exh = 1; exhreqs = reqs
                                :: else -> skip
                                fi;
                                etype = E_EXH;
                                if
                                :: STATZERO -> err429 = 0
                                :: else -> err429 = 1
                                fi;
                                ph = PH_ERR; go = 1
                           :: else ->
                                sched(k, td); k++;
                                tw = now + td;
                                if
                                :: tw > pdl -> tw = pdl
                                :: else -> skip
                                fi;
                                wake = tw; sl = SL_PROV; ph = PH_SLEEP
                           fi
                      fi
                 fi
            :: ph == PH_ERR ->
                 /* the resilient wrapper reads the error (callWithRetry) */
                 if
                 :: err429 ->
                      tw = RMAX - att;
                      if
                      :: allow < tw -> tw = allow
                      :: else -> skip
                      fi;
                      if
                      :: tw == 0 -> ph = PH_RET; go = 1
                      :: else ->
                           if
                           :: etype == E_BUSY429 -> td = RA            /* Retry-After named by the server */
                           :: else -> td = RB; tj = 0; do :: tj < att -> td = td * 2; tj++ :: else -> break od
                           fi;
                           if
                           :: td + (now - wstart) + CH > CT -> ph = PH_RET; go = 1   /* delay > CallBudget - elapsed - headroom */
                           :: else ->
                                allow--; busyspend++; att++;
                                wake = now + td; sl = SL_WRAP; ph = PH_SLEEP
                           fi
                      fi
                 :: else -> ph = PH_RET; go = 1
                 fi
            :: ph == PH_RET ->
                 /* the call came back to the agent with an error: the timer is stopped */
                 tarm = 0;
                 if
                 :: VAR == 1 && etype == E_BUSYTYPED && !HELPER && !(REUSE && SUB) ->
                      if
                      :: !adlset ->
                           adlset = 1; ak = 0;
#if TURNLEDGER
                           if
                           :: waited < Beff -> adl = now + Beff - waited
                           :: else -> adl = now
                           fi
#else
                           adl = now + Beff
#endif
                      :: else -> skip
                      fi;
                      if
                      :: now >= adl ->
                           if
                           :: !exh -> exh = 1; exhreqs = reqs
                           :: else -> skip
                           fi;
                           res = R_BUSYERR; over = 1; endt = now
                      :: else ->
                           sched(ak, td); ak++;
                           tw = now + td;
                           if
                           :: tw > adl -> tw = adl
                           :: else -> skip
                           fi;
                           waited = waited + (tw - now);
                           wake = tw; sl = SL_AGENT; ph = PH_SLEEP
                      fi
                 :: EXHTRANS && !HELPER && etype == E_EXH && recov < 2 ->
                      recov++; allow = RMAX; ftr = 0;
                      wake = now + RB * recov; sl = SL_REC; ph = PH_SLEEP
                 :: else -> res = R_BUSYERR; over = 1; endt = now
                 fi
            :: ph == PH_SLEEP ->
                 if
                 :: now >= wake ->
                      if
                      :: sl == SL_PROV -> ph = PH_POLL; go = 1
                      :: sl == SL_WRAP -> ph = PH_PROV; go = 1
                      :: sl == SL_AGENT -> allow = RMAX; ftr = 0; ph = PH_ISSUE; go = 1   /* resetRetries */
                      :: else -> ph = PH_ISSUE; go = 1
                      fi
                 :: else -> skip
                 fi
            :: ph == PH_FT ->
                 if
                 :: now >= ftick -> emitted = 1; tarm = 0; ph = PH_GEN; gdone = now + GEN; go = 1
                 :: else -> skip
                 fi
            :: ph == PH_GEN ->
                 if
                 :: now >= gdone && calls < NCALL ->
                      /* the next call of the same turn: the environment moves on (a fresh slot-free tick) */
                      held = 0; calls++; ph = PH_ISSUE; att = 0; ftr = 0; allow = RMAX; adlset = 0; emitted = 0;
                      if
                      :: freeAt = NEVER; Srel = NEVER
                      :: tj = 0; do :: tj < SMAXE -> tj++ :: break od; freeAt = now + tj; Srel = tj
                      fi;
                      go = 1
                 :: now >= gdone && calls >= NCALL -> held = 0; res = R_OK; over = 1; endt = now
                 :: else -> skip
                 fi
            :: else -> skip
            fi
       :: else -> break
       od;
       if
       :: !over -> now++
       :: else -> skip
       fi;
       tj = 0; td = 0; tw = 0; go = 0
     }
  :: else -> break
  od
}

init {
  byte o = 0; byte tj = 0; byte tk = 0; byte td = 0; byte tw = 0;

  /* precedence table (4.1b, 5a): rows are (provider busy_wait_ms, global absent?, global value) -> budget.
     expected values are literals, not computed by the same rule. */
  precedence(0, 1, 0, o);  assert(o == 30);   /* neither set: the default 30 */
  precedence(0, 0, 0, o);  assert(o == 0);    /* global explicit 0, provider unset: no waiting */
  precedence(0, 0, 3, o);  assert(o == 3);
  precedence(0, 0, 30, o); assert(o == 30);
  precedence(5, 1, 0, o);  assert(o == 5);    /* provider above zero wins over the default */
  precedence(5, 0, 0, o);  assert(o == 5);    /* ... and over a global "no waiting" */
  precedence(5, 0, 3, o);  assert(o == 5);
  precedence(5, 0, 30, o); assert(o == 5);
  precedence(10, 1, 0, o); assert(o == 10);
  precedence(10, 0, 0, o); assert(o == 10);
  precedence(10, 0, 3, o); assert(o == 10);
  precedence(10, 0, 30, o); assert(o == 10);

  /* effective budget of this run */
  Beff = CB;
#if CAP
  if
  :: CB > CT - CH -> Beff = CT - CH
  :: else -> skip
  fi;
#endif

  /* requests of one uninterrupted wait (jitter-free schedule): the intended bound per wait */
  NREQ1 = 1; tw = 0; tk = 0;
  do
  :: tw < Beff ->
       sched0(tk, td); tk++;
       tw = tw + td;
       if
       :: tw > Beff -> tw = Beff
       :: else -> skip
       fi;
       NREQ1++
  :: else -> break
  od;
  tj = 0; tk = 0; td = 0; tw = 0;

  /* environment: slot-free tick, first-token time, Stop tick */
  if
  :: S0 = NEVER
  :: S0 = 0; do :: S0 < SMAXE -> S0++ :: break od
  fi;
  freeAt = S0; Srel = S0;
  Fd = 0; do :: Fd < FMAX -> Fd++ :: break od;
  if
  :: ST = NEVER
  :: ST = 0; do :: ST < STMAX -> ST++ :: break od
  fi;
  o = 0;
  started = 1;
  run world()
}
