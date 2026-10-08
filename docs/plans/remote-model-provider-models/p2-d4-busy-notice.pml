/* p2-d4-busy-notice.pml - how the end of a `remote_busy` notice meets the real usage snapshot of the same row.

   Spec: docs/plans/remote-model-provider-phase2.md, 2 (W4), 4.5, 4.6 and 7 (dilemma D4: options N1..N4).
   Code read: internal/agent/coddy_provider.go (busyWaitNotice: report/clear), internal/agent/react.go (the three
   clear sites: admission report, first chunk, after Stream returns), internal/session/provider_usage*.go (the cache,
   fetchedAt of a fetch, the publishers), external/httpserver/{bridge.go,providers_usage_http.go,
   composer_stream_relay.go} (turn stream, events stream, replay and desync of a subscriber),
   external/ui/src/ui/chat/{useProviderUsage.ts,providerUsage.ts,UsageBanner.tsx}, external/cli/{usage.go,footer.go}.

   WHAT IS MODELLED. One session, one `coddy` row (one subject = provider row + alias). A call of the turn may be made
   to WAIT for a free slot (NW calls in sequence); a waiting call sends `remote_busy` notices (stamped with the clock,
   each carrying the deadline D of the wait) and ends by ADMIT (the remote took it) or FAIL (error, cancel, budget
   exhausted: no admission); both ends send the "clear" the variant chooses. The manager owns a cache of real snapshots
   (versions 1..NV, fetchedAt = the clock at completion, strictly increasing: the 15 s floor). Frames reach the surface
   over two delivery paths: the TURN STREAM (queue 0: busy notices, the clear, snapshots published to a waiting
   session; FIFO like an SSE stream unless REORD) and the SIDE CHANNEL (events stream and REST answers: a snapshot in
   flight is only its version, delivered in any order, possibly twice with DUPR). The surface is a state machine:
   SURF 0 = one slot with the SPA's rule `usageIsNewer` (accept iff next.fetchedAt >= current.fetchedAt), the busy
   notice replacing the snapshot; SURF 1 = snapshot slot and countdown slot apart (N3).

   CLASS. Finite, untimed engine run over an explicit DISCRETE-EVENT clock abstraction (not a timed automaton):
   `clk` ticks 1..TMAX but moves only when an event needs it (an event of the agent follows its previous one by at least
   one tick: the busySleep floor of 1 s; a fetch completes at a tick strictly after the previous one) or by the
   fetcher's free ticks; the ticks are refused while a call waits at its deadline D = start + BUDGET (the provider's own
   monotonic deadline). Every stamp is a clk value; RFC 3339 stamps have 1 s resolution, so stamps of different
   processes may tie (kept). The model is acyclic (every loop bounded) and every maximal run ends in a final state, so a
   `<>[] p` property is a statement about the final states of all runs and needs no fairness (checked by a --fairness
   weak run, which gives the same verdicts).

   VARIANT SWITCHES (-D), all default to the first value:
     SURF   0 one slot (useProviderUsage.ts as it stands)            1 two slots, countdown apart (N3)
     CLEAR  0 `Unsupported:true` (N2, phase 1)                        1 cached snapshot as it is, own fetchedAt (N1)
            2 cached snapshot re-stamped with the clock of the clear  3 end marker: busy-class update, no payload (N4)
            4 nothing is sent
            (CLEAR 1 and 2 send Unsupported when nothing is cached, as the plan says)
     EXEMPT 1: a non-busy update supersedes a busy one whatever its stamp (N4's ordering exemption)
     NETEXP 1: the surface drops a countdown at receipt + remaining + 1 tick of its own clock   (safety net)
     NETEND 1: the surface drops the countdown, with a tombstone, when the turn ends             (safety net)
     LOSS   1: any frame of the turn stream may be dropped (desynced subscriber, stream closed by a cancel)
     REPLAY n: up to n frames of the turn stream are delivered once more, later (relay replay, mirror)
     REORD  1: the turn stream may deliver out of order            DUPR n: n side-channel answers delivered twice
     WARM   1: version 1 is cached before the call and its REST answer (session open) is in flight
     ARRIVAL 1: the surface applies updates in arrival order, no fetchedAt rule (the console as it is);
             2: snapshots in arrival order, the countdown slot with the stamp and tombstone rules (the console as recommended)
     TIEEND 1: the end of a wait may take the same tick as the previous notice (a tie of two stamps)
     MUTANTS: CLEARFAIL 0 (no clear on the FAIL path), TOMB 0 (no tombstone) / 2 (non-strict tombstone),
              RESTRICT 0 (the end marker is stamped with the cache's fetchedAt instead of the clock)

   CONSTANTS (-D): NW calls, NV snapshot versions, NR extra REST reads (a read after the turn ends always happens),
   TMAX clock horizon, BUDGET wait budget in ticks, MAXB busy notices per wait.
   Scale A: NW=1 NV=1 NR=1 MAXB=2 BUDGET=2 TMAX=6.   Scale W: NW=1 NV=2 NR=1 MAXB=1 BUDGET=1 TMAX=5 WARM=1.
   Scale W2: NW=2 NV=1 NR=0 MAXB=1 BUDGET=1 TMAX=8.  Scale T: NW=1 NV=1 NR=0 MAXB=1 BUDGET=1 TMAX=5.

   OBSERVABLES (globals, used by the properties in p2-d4-busy-notice.md):
     bshown   the surface shows the countdown          sview    version of the real snapshot the surface shows (0 none)
     maxdel   newest real snapshot version delivered   maxshown newest version ever displayed
     regress  an older version was displayed after a newer one
     wph      call phase 0 idle/done, 1 waiting, 2 streaming           bdelcur a busy notice of the current wait arrived
     qn[0]    frames in flight on the turn stream      scn      snapshots in flight on the side channel
     turnended / turnepoch / agdone  the turn_ended event, the composer stream's end (turnEpoch), the call loop's end
*/

#ifndef SURF
#define SURF 0
#endif
#ifndef CLEAR
#define CLEAR 0
#endif
#ifndef EXEMPT
#define EXEMPT 0
#endif
#ifndef NETEXP
#define NETEXP 0
#endif
#ifndef NETEND
#define NETEND 0
#endif
#ifndef LOSS
#define LOSS 0
#endif
#ifndef REPLAY
#define REPLAY 0
#endif
#ifndef REORD
#define REORD 0
#endif
#ifndef CLEARFAIL
#define CLEARFAIL 1
#endif
#ifndef TOMB
#define TOMB 1
#endif
#ifndef RESTRICT
#define RESTRICT 1
#endif
#ifndef DUPR
#define DUPR 0
#endif
#ifndef ARRIVAL
#define ARRIVAL 0
#endif
#ifndef WARM
#define WARM 0
#endif
#ifndef TIEEND
#define TIEEND 0
#endif
#ifndef NW
#define NW 1
#endif
#ifndef NV
#define NV 2
#endif
#ifndef NR
#define NR 1
#endif
#ifndef TMAX
#define TMAX 8
#endif
#ifndef BUDGET
#define BUDGET 3
#endif
#ifndef MAXB
#define MAXB 2
#endif

#define K_SNAP  1
#define K_BUSY  2
#define K_END   3
#define K_UNSUP 4

#define QN 10
#define NQ 2

/* the clock starts at 1: a stamp of 0 means "none" */
byte clk = 1;

/* the three delivery paths; flat arrays, queue q occupies [q*QN, q*QN+QN) */
byte qk[20];
byte qv[20];
byte qs[20];
byte qd[20];
byte qw[20];
byte qc[20];
byte qn[2];

/* the side channel (events stream and REST answers): a snapshot in flight is a version, its content is fixed */
byte sc[4];
byte scn = 0;
byte fstamp[4];

/* the manager's cache of real snapshots */
byte cacheV = 0;
byte cacheF = 0;

/* the call (agent) */
byte wph = 0;
byte curw = 0;
byte wD = 0;
byte agdone = 0;
byte agdoneAt = 0;
byte fedone = 0;
byte turnended = 0;
byte turnepoch = 0;
byte postread = 0;
byte rddone = 0;
byte bdelcur = 0;
byte dups = 0;
byte dupr = 0;

/* the surface, one slot (SURF 0) */
byte ck = 0;
byte cv = 0;
byte cs = 0;
/* the surface, two slots (SURF 1) */
byte sv = 0;
byte ss = 0;
byte bon = 0;
byte bs = 0;
byte bend = 0;
/* expiry clock of the countdown shown (both) */
byte bexp = 0;

/* views and ghosts */
byte bshown = 0;
byte sview = 0;
byte maxdel = 0;
byte maxshown = 0;
byte regress = 0;

inline refresh() {
#if SURF == 0
  if
  :: ck == 2 -> bshown = 1
  :: else -> bshown = 0
  fi;
  if
  :: ck == 1 -> sview = cv
  :: else -> sview = 0
  fi
#else
  bshown = bon;
  sview = sv
#endif
}

/* queue q gets a frame (kind, version, stamp, deadline, wait, clear flag) */
inline enq(q_, k_, v_, s_, d_, w_, c_) {
  assert(qn[q_] < QN);
  qk[q_ * QN + qn[q_]] = k_;
  qv[q_ * QN + qn[q_]] = v_;
  qs[q_ * QN + qn[q_]] = s_;
  qd[q_ * QN + qn[q_]] = d_;
  qw[q_ * QN + qn[q_]] = w_;
  qc[q_ * QN + qn[q_]] = c_;
  qn[q_] = qn[q_] + 1
}

/* the frame at index i leaves queue q, the others keep their order */
inline deq(q_, i_) {
  j = i_;
  do
  :: j + 1 < qn[q_] ->
       qk[q_ * QN + j] = qk[q_ * QN + j + 1];
       qv[q_ * QN + j] = qv[q_ * QN + j + 1];
       qs[q_ * QN + j] = qs[q_ * QN + j + 1];
       qd[q_ * QN + j] = qd[q_ * QN + j + 1];
       qw[q_ * QN + j] = qw[q_ * QN + j + 1];
       qc[q_ * QN + j] = qc[q_ * QN + j + 1];
       j++
  :: else -> break
  od;
  qn[q_] = qn[q_] - 1;
  /* the vacated slot is zeroed: a stale copy would split equal states */
  qk[q_ * QN + qn[q_]] = 0; qv[q_ * QN + qn[q_]] = 0; qs[q_ * QN + qn[q_]] = 0;
  qd[q_ * QN + qn[q_]] = 0; qw[q_ * QN + qn[q_]] = 0; qc[q_ * QN + qn[q_]] = 0
}

/* one snapshot of the side channel reaches the surface */
inline side(v_) {
  s = fstamp[v_];
  if
  :: dupr < DUPR -> dupr++
  :: true -> sc[v_] = sc[v_] - 1; scn = scn - 1
  fi;
  apply(K_SNAP, v_, s, 0, 0);
  scrub()
}

/* the locals of the delivery process carry nothing between two deliveries */
inline scrub() {
  i = 0; j = 0; k = 0; v = 0; s = 0; d = 0; w = 0; c = 0; e = 0
}

/* the surface adopts one frame (the whole rule set of the variant) */
inline apply(k_, v_, s_, d_, w_) {
  if
  :: k_ == K_SNAP ->
       if
       :: v_ > maxdel -> maxdel = v_
       :: else -> skip
       fi
  :: else -> skip
  fi;
  if
  :: k_ == K_BUSY && w_ == curw && wph == 1 -> bdelcur = 1
  :: else -> skip
  fi;
#if SURF == 0
  if
  :: k_ == K_BUSY ->
       if
       :: ck == 0 || s_ >= cs ->
            ck = 2; cs = s_;
#if NETEXP == 1
            e = clk + (d_ - s_) + 1;
            if
            :: e < TMAX -> bexp = e
            :: else -> bexp = TMAX
            fi
#endif
       :: else -> skip
       fi
  :: k_ == K_SNAP ->
       if
       :: ck == 0 || s_ >= cs || (EXEMPT == 1 && ck == 2) ->
            if
            :: v_ < maxshown -> regress = 1
            :: else -> maxshown = v_
            fi;
            ck = 1; cv = v_; cs = s_
       :: else -> skip
       fi
  :: k_ == K_UNSUP -> ck = 0; cv = 0; cs = 0
  :: k_ == K_END ->
       if
       :: ck == 2 && s_ >= cs -> ck = 0; cv = 0; cs = 0
       :: else -> skip
       fi
  fi
#else
  if
  :: k_ == K_BUSY ->
       if
       :: ARRIVAL == 1 || ((TOMB == 0 || (TOMB == 1 && s_ > bend) || (TOMB == 2 && s_ >= bend)) && s_ >= bs) ->
            bon = 1; bs = s_;
#if NETEXP == 1
            e = clk + (d_ - s_) + 1;
            if
            :: e < TMAX -> bexp = e
            :: else -> bexp = TMAX
            fi
#endif
       :: else -> skip
       fi
  :: k_ == K_SNAP ->
       if
       :: ARRIVAL >= 1 || sv == 0 || s_ >= ss ->
            if
            :: v_ < maxshown -> regress = 1
            :: else -> maxshown = v_
            fi;
            sv = v_; ss = s_
       :: else -> skip
       fi;
       if
       :: EXEMPT == 1 && bon == 1 -> bon = 0
       :: else -> skip
       fi
  :: k_ == K_END ->
       if
       :: ARRIVAL == 1 || s_ >= bs ->
            bon = 0;
            if
            :: s_ > bend -> bend = s_
            :: else -> skip
            fi
       :: else -> skip
       fi
  :: k_ == K_UNSUP -> sv = 0; ss = 0; bon = 0
  fi
#endif
  ;
  refresh()
}

/* what a call sends when it stops being a waiting call (cl_ = the stamp of the clear) */
inline sendclear(w_) {
  if
  :: LOSS == 1 -> skip
  :: true ->
#if CLEAR == 0
       enq(0, K_UNSUP, 0, 0, 0, w_, 1)
#endif
#if CLEAR == 1
       if
       :: cacheV > 0 -> enq(0, K_SNAP, cacheV, cacheF, 0, w_, 1)
       :: else -> enq(0, K_UNSUP, 0, 0, 0, w_, 1)
       fi
#endif
#if CLEAR == 2
       if
       :: cacheV > 0 -> enq(0, K_SNAP, cacheV, clk, 0, w_, 1)
       :: else -> enq(0, K_UNSUP, 0, 0, 0, w_, 1)
       fi
#endif
#if CLEAR == 3
#if RESTRICT == 1
       enq(0, K_END, 0, clk, 0, w_, 1)
#else
       enq(0, K_END, 0, cacheF, 0, w_, 1)
#endif
#endif
#if CLEAR == 4
       skip
#endif
  fi
}

/* ---------- the call: the busy notice of busyWaitNotice ---------- */
/* TIME. The clock moves only when an event needs it: an event of the agent follows its own previous one by at
   least one tick (the sleep floor), so it advances the clock itself when nobody else has; a tick is refused while a
   call waits at its deadline. Events of different processes may share a tick (a tie of two RFC 3339 stamps). */
inline stepbody_hb(w_) {
  enq(0, K_BUSY, 0, clk, wD, w_, 0); nb++; lastT = clk
}

proctype agent() {
  byte w = 0;
  byte nb;
  byte lastT = 0;
  do
  :: w < NW ->
       w++;
       if
       :: skip   /* admitted at once: nothing shown, nothing to clear */
       :: atomic {
            clk + BUDGET + 2 <= TMAX && (clk > lastT || clk < TMAX) ->
            if
            :: clk > lastT -> skip
            :: clk == lastT -> clk++
            fi;
            curw = w; wD = clk + BUDGET; wph = 1; bdelcur = 0; nb = 1; lastT = clk;
            enq(0, K_BUSY, 0, clk, wD, w, 0)
          };
          do
          :: atomic {
               nb < MAXB && ((clk > lastT && clk < wD) || (clk == lastT && clk + 1 < wD)) ->
               if
               :: clk > lastT -> skip
               :: clk == lastT -> clk++
               fi;
               stepbody_hb(w)
             }
          :: atomic {
               (clk > lastT || TIEEND == 1 || (clk == lastT && clk + 1 <= wD)) ->
               if
               :: clk > lastT -> skip
               :: TIEEND == 1 && clk == lastT -> skip
               :: clk == lastT && clk + 1 <= wD -> clk++
               fi;
               /* ADMIT: the report with Admitted ends the countdown on the spot */
               sendclear(w); wph = 2; lastT = clk
             };
             break
          :: atomic {
               (clk > lastT || TIEEND == 1 || (clk == lastT && clk + 1 <= wD)) ->
               if
               :: clk > lastT -> skip
               :: TIEEND == 1 && clk == lastT -> skip
               :: clk == lastT && clk + 1 <= wD -> clk++
               fi;
               /* FAIL: error, cancel or budget exhausted; the clear after Stream returns */
#if CLEARFAIL == 1
               sendclear(w);
#endif
               wph = 0; lastT = clk
             };
             break
          od;
          /* the stream of an admitted call runs on for a tick or more */
          if
          :: wph == 2 ->
               atomic {
                 (clk > lastT || (clk == lastT && clk < TMAX)) ->
                 if
                 :: clk > lastT -> skip
                 :: clk == lastT -> clk++
                 fi;
                 wph = 0; lastT = clk
               }
          :: else -> skip
          fi;
          atomic { wD = 0; curw = 0; bdelcur = 0 }
       fi
  :: else -> break
  od;
  atomic { agdone = 1; agdoneAt = clk }
}

/* ---------- the manager: fetch completions and their publication ---------- */
inline publish(i_) {
  if
  :: skip   /* a fetch no session asked for through a publisher: REST only */
  :: sc[i_]++; scn++
  :: agdone == 0 -> enq(0, K_SNAP, i_, clk, 0, 0, 0); sc[i_]++; scn++
  :: agdone == 0 -> enq(0, K_SNAP, i_, clk, 0, 0, 0)
  fi
}

proctype fetcher() {
  byte i = WARM;
  do
  :: atomic {
       i < NV && clk > cacheF ->
       i++; cacheV = i; cacheF = clk; fstamp[i] = clk; publish(i)
     }
  :: atomic {
       i < NV && clk == cacheF && clk < TMAX && !(wph == 1 && clk >= wD) ->
       clk++;
       i++; cacheV = i; cacheF = clk; fstamp[i] = clk; publish(i)
     }
  :: i < NV && clk < TMAX && !(wph == 1 && clk >= wD) -> clk++   /* time passes: the only free tick of the model */
  :: break
  od;
  fedone = 1
}

/* ---------- the surface's REST reads ---------- */
proctype reader() {
  byte n = 0;
  do
  :: n < NR && cacheV > 0 && turnepoch == 0 ->
       atomic { sc[cacheV]++; scn++; n++ }
  :: turnepoch == 1 && postread == 0 && cacheV > 0 ->
       /* the refresh a finished turn asks for (turnEpoch) */
       atomic { sc[cacheV]++; scn++; postread = 1 }
  :: postread == 1 || (fedone == 1 && cacheV == 0) -> break
  od;
  rddone = 1
}

/* ---------- delivery to the surface ---------- */
proctype surf() {
  byte i;
  byte j;
  byte k;
  byte v;
  byte s;
  byte d;
  byte w;
  byte c;
  byte e;
  do
  :: atomic {
       /* the turn stream: head, or any frame under REORD */
       qn[0] > 0 ->
       i = 0;
#if REORD == 1
       do
       :: i + 1 < qn[0] -> i++
       :: break
       od;
#endif
       k = qk[i]; v = qv[i]; s = qs[i]; d = qd[i]; w = qw[i]; c = qc[i];
       deq(0, i);
       if
       :: dups < REPLAY -> dups++; enq(0, k, v, s, d, w, c)
       :: true -> skip
       fi;
       apply(k, v, s, d, w);
       scrub()
     }
  :: atomic {
       /* the side channel (events stream and REST answers): any order; an answer may be delivered twice */
       sc[1] > 0 -> side(1)
     }
  :: atomic {
       NV >= 2 && sc[2] > 0 -> side(2)
     }
  :: atomic {
       NV >= 3 && sc[3] > 0 -> side(3)
     }
  :: atomic {
       /* safety net: the countdown's own deadline passed */
       NETEXP == 1 && bshown == 1 && clk >= bexp ->
#if SURF == 0
       ck = 0; cv = 0; cs = 0;
#else
       bon = 0;
       if
       :: bs > bend -> bend = bs
       :: else -> skip
       fi;
#endif
       refresh()
     }
  :: atomic {
       /* the composer's stream of the turn is finished: every frame it carried is delivered (or was lost);
          the SPA bumps turnEpoch and asks for a refresh (a REST read) */
       agdone == 1 && qn[0] == 0 && turnepoch == 0 -> turnepoch = 1
     }
  :: atomic {
       /* the turn ended (turn_ended on the events stream): maybe a safety net */
       agdone == 1 && turnended == 0 ->
       turnended = 1;
#if NETEND == 1
#if SURF == 0
       if
       :: ck == 2 -> ck = 0; cv = 0; cs = 0
       :: else -> skip
       fi;
#else
       bon = 0;
       if
       :: agdoneAt > bend -> bend = agdoneAt
       :: else -> skip
       fi;
#endif
       refresh()
#else
       skip
#endif
     }
  :: atomic {
       /* time passes after everything else is done: a countdown shown can reach its own deadline */
       NETEXP == 1 && bshown == 1 && clk < TMAX &&
       agdone == 1 && fedone == 1 && rddone == 1 && turnended == 1 && turnepoch == 1 &&
       qn[0] == 0 && scn == 0 -> clk = TMAX
     }
  :: atomic {
       agdone == 1 && fedone == 1 && rddone == 1 && turnended == 1 && turnepoch == 1 &&
       qn[0] == 0 && scn == 0 &&
       !(NETEXP == 1 && bshown == 1) -> skip
     };
     break
  od
}

init {
  atomic {
#if WARM == 1
    /* the cache is warm before the call: version 1 fetched at tick 1, its REST answer (session open) in flight */
    cacheV = 1; cacheF = 1; fstamp[1] = 1; sc[1] = 1; scn = 1;
#endif
    run agent();
    run fetcher();
    run reader();
    run surf()
  }
}
