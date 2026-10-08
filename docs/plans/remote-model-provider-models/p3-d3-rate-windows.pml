/* p3-d3-rate-windows.pml - where the rate limits of a shared model live, what a refusal answers and when a token
   is spent (phase 3, dilemma D3).

   Decides  dilemma D3 of docs/plans/remote-model-provider-phase3.md (section 10) and the places that depend on it:
            5.3 (the node and the relay limits, the answers, the refund), 11 (stages H2b, H4, H5).
   Code     external/httpserver/shared_models_http.go (llmCompletionsPost: the slot is taken right after the gate, the
            window after the slot and before the body is read, a refused take gives the slot back), external/swarm/mount.go
            (handleMount: the relay's slot and window of a scoped client, forwarded attempts, a refund when the node
            answers busy), internal/llm/coddy.go (admit: the client waits out ONLY kind busy, sleeps max(Retry-After,1),
            under one budget; every other kind, rate and quota included, ends the call), internal/shareguard (the GCRA
            limiter, modelled as a token bucket that gains one token every P ticks, at most B).
   Class    finite, discrete time. TIME IS A VARIABLE (`now`, ticks): a client acts only at a tick at which it is
            ready (its wake tick has come), and the clock advances only when NO client has an enabled action at the
            current tick, so a client cannot be delayed past its own timer by the interleaving. Not a timed automaton.

   World
     callers   NCALL = 1, 2 or 3 callers, each making NC calls one after another; a caller belongs to a relay CLIENT:
               NCALL 1 {client 0}, NCALL 2 {client 0, client 1}, NCALL 3 {client 0, client 0, client 1} (one client of the
               relay runs two calls at once, which is what makes its relay slot limit bite). A call is a loop: attempt; refused -> sleep
               max(Retry-After, 1) ticks -> attempt again, under a budget of W ticks counted from the call's first attempt;
               admitted -> the call is served for D ticks, holding a node slot (and a relay slot of its client), then ends.
     node      NM stream slots, one token bucket (B tokens at most, one more every P ticks) per credential: the node sees
               the relay as ONE credential, so all clients of the relay share its slots and its bucket.
     relay     (PLACE 2 and 3) per client: RM slots and one token bucket (B tokens at most, one more every PR ticks).
     answers   a slot refusal is busy with Retry-After 1; a window refusal is busy with Retry-After = the ticks to the next
               token (KIND 0), or kind rate (KIND 1: terminal for the client, which waits out busy only).

   Options (the dilemma)
     PLACE   1 node only, 2 relay only, 3 both                                      (the plan's expected answer: 3)
     SPEND   0 (x) a token is taken at the attempt, before the slot is looked at,
             1 (y) a token is taken after the slot, at the admission                (expected: 1)
     KIND    0 (i) a window refusal is busy with a code, the client waits it out,
             1 (ii) a window refusal is kind rate, terminal for the client           (expected: 0)
     REFUND  1 the relay gives its token back when the NODE refuses (slot or window) a forwarded attempt, 0 it does not
             (only with PLACE 3; the plan's design: 1)

   Properties (ghost latches, monotone)
     A1 gA1      admitted calls per layer never exceed B + now/P at the node (when it has a layer) and B + now/PR for a client's
                 net tokens at the relay (refunds taken off)
     A1p gA1p    provider-wide: the calls the node admits in all never exceed B + now/P: ONLY the node protects the provider from
                 all the relay's clients together (expected violated without a node layer)
     A7 gA7      a client is held to its own contract: its admitted calls never exceed B + now/PR: ONLY the relay knows the
                 client (expected violated without a relay layer when PR > P)
     A2 gTimeout a call ends because its budget is spent. For NCALL = 1 and W >= P + 2 it must never happen: a lone waiting
                 caller is admitted within P + 1 ticks of a token existing
     A3 gA3      a refused request never reduces the tokens of a layer, the relay's refund counted
     A4 gA4      with PLACE 3 and the sum of the relay slot limits <= NM, a client held to its relay slots never causes a node
                 slot refusal (a relay refusal is not a node refusal)
     A5 gTerm    a client is never ended by a terminal error for a window refusal;  gStuck: every client is done at the horizon
     A6 gTimeout starvation under contention (NCALL >= 2) is REACHABLE (expected violated for this latch): documented, not promised away
     sanity      sAdm sNodeBusy sRelayBusy sWin sRefund  (reachable in the scenarios that exercise them: sNodeBusy needs NM = 1,
                 since with PLACE 3 the relay's slots cap what is forwarded at the node's NM when 2 * RM <= NM; so at NM = 2, RM = 1
                 gA4 is checked without sNodeBusy ever firing, which is the point of A4 and says only that 2 * RM <= NM holds)

   Abstractions (what the model is not): the bucket refills on the clock's boundaries (every P or PR ticks) instead of anchoring
     to the last use as a GCRA does; every property compares cumulative counts or placements, never the phase of a refill, and the
     Retry-After a refusal carries is exact for the model's own bucket. gStuck says "a client is not done at the horizon", which is
     a defect of the model only when T is too small for NC calls of W + D ticks each (the defaults leave room).
*/

#ifndef PLACE
#define PLACE 3
#endif
#ifndef SPEND
#define SPEND 1
#endif
#ifndef KIND
#define KIND 0
#endif
#ifndef REFUND
#define REFUND 1
#endif
#ifndef NCALL
#define NCALL 3
#endif
#ifndef T
#define T 32
#endif
#ifndef P
#define P 3
#endif
#ifndef PR
#define PR 3            /* the relay's token period; PR > P is a relay limit tighter than the node's */
#endif
#ifndef B
#define B 1
#endif
#ifndef NM
#define NM 2
#endif
#ifndef RM
#define RM 1
#endif
#ifndef D
#define D 2
#endif
#ifndef W
#define W 8
#endif
#ifndef NC
#define NC 2
#endif

byte now;
byte ntok = B;             /* node tokens                                              */
byte nuse;                 /* node slots in use                                         */
byte cl[3];                /* the relay client of a caller                              */
byte rtok[2];              /* relay tokens per client                                   */
byte ruse[2];              /* relay slots in use per client                             */
byte st[3];                /* 0 start of a call, 1 asleep until wake, 2 in service, 3 done */
byte wake[3];
byte endAt[3];
byte cstart[3];            /* the tick of the first attempt of the call                 */
byte calls[3];             /* calls finished                                            */
byte admN;                 /* calls admitted by the node                                */
byte netR[2];              /* tokens the relay holds spent for a client, refunds taken off */
byte admC[2];              /* calls admitted per client                                 */

bit gA1, gA1p, gA7, gA3, gA4, gTerm, gTimeout, gStuck;
bit sAdm, sNodeBusy, sRelayBusy, sWin, sRefund;

/* temporaries of one attempt, cleared at its end */
byte t_c, t_k, t_ref, t_orig, t_ra, t_bR, t_bN, t_sleep;
bit t_relSlot, t_relTok, t_nodeSlot, t_nodeTok;

inline relayLayer() {
#if PLACE == 2 || PLACE == 3
#if SPEND == 0
    /* (x) the token first: a call refused for a slot has spent it already */
    if
    :: rtok[t_k] > 0 -> rtok[t_k]--; netR[t_k]++; t_relTok = 1
    :: else -> t_ref = 2; t_orig = 1; t_ra = PR - (now % PR)
    fi;
    if
    :: t_ref == 0 ->
         if
         :: ruse[t_k] < RM -> ruse[t_k]++; t_relSlot = 1
         :: else -> t_ref = 1; t_orig = 1; t_ra = 1
         fi
    :: else -> skip
    fi
#else
    /* (y) the slot first, the token after it */
    if
    :: ruse[t_k] < RM -> ruse[t_k]++; t_relSlot = 1
    :: else -> t_ref = 1; t_orig = 1; t_ra = 1
    fi;
    if
    :: t_ref == 0 ->
         if
         :: rtok[t_k] > 0 -> rtok[t_k]--; netR[t_k]++; t_relTok = 1
         :: else -> t_ref = 2; t_orig = 1; t_ra = PR - (now % PR)
         fi
    :: else -> skip
    fi
#endif
    if :: netR[t_k] > B + now / PR -> gA1 = 1 :: else -> skip fi
#endif
}

inline nodeLayer() {
#if PLACE == 1 || PLACE == 3
    if
    :: t_ref == 0 ->
#if SPEND == 0
         if
         :: ntok > 0 -> ntok--; t_nodeTok = 1
         :: else -> t_ref = 2; t_orig = 2; t_ra = P - (now % P)
         fi;
         if
         :: t_ref == 0 ->
              if
              :: nuse < NM -> nuse++; t_nodeSlot = 1
              :: else -> t_ref = 1; t_orig = 2; t_ra = 1
              fi
         :: else -> skip
         fi
#else
         if
         :: nuse < NM -> nuse++; t_nodeSlot = 1
         :: else -> t_ref = 1; t_orig = 2; t_ra = 1
         fi;
         if
         :: t_ref == 0 ->
              if
              :: ntok > 0 -> ntok--; t_nodeTok = 1
              :: else -> t_ref = 2; t_orig = 2; t_ra = P - (now % P); nuse--; t_nodeSlot = 0
              fi
         :: else -> skip
         fi
#endif
    :: else -> skip
    fi
#endif
}

/* the outcome of the attempt of client t_c */
inline outcome() {
    if
    :: t_ref == 0 ->
         /* admitted: the call holds its slots until it ends */
         sAdm = 1;
         admN++; admC[t_k]++;
         if :: PLACE != 2 && admN > B + now / P -> gA1 = 1 :: else -> skip fi;
         if :: admN > B + now / P -> gA1p = 1 :: else -> skip fi;
         if :: admC[t_k] > B + now / PR -> gA7 = 1 :: else -> skip fi;
         endAt[t_c] = now + D;
         st[t_c] = 2
    :: else ->
         /* refused: what the attempt took and the refusal does not keep is given back */
         if :: t_ref == 1 && t_orig == 2 -> sNodeBusy = 1 :: else -> skip fi;
         if :: t_ref == 1 && t_orig == 1 -> sRelayBusy = 1 :: else -> skip fi;
         if :: t_ref == 2 -> sWin = 1 :: else -> skip fi;
         if :: t_ref == 1 && t_orig == 2 && PLACE == 3 && 2 * RM <= NM -> gA4 = 1 :: else -> skip fi;
         if :: t_relSlot == 1 -> ruse[t_k]--; t_relSlot = 0 :: else -> skip fi;
#if PLACE == 3 && REFUND == 1
         /* the node refused a forwarded attempt: the relay takes its token back */
         if
         :: t_orig == 2 && t_relTok == 1 ->
              if :: rtok[t_k] < B -> rtok[t_k]++ :: else -> skip fi;
              netR[t_k]--; t_relTok = 0; sRefund = 1
         :: else -> skip
         fi;
#endif
         /* A3: a refused request left every layer with no fewer tokens than it found */
         if :: rtok[t_k] < t_bR || ntok < t_bN -> gA3 = 1 :: else -> skip fi;
#if KIND == 1
         if
         :: t_ref == 2 -> gTerm = 1; calls[t_c]++; if :: calls[t_c] >= NC -> st[t_c] = 3 :: else -> st[t_c] = 0 fi
         :: else -> sleepOrGiveUp()
         fi
#else
         sleepOrGiveUp()
#endif
    fi
}

inline sleepOrGiveUp() {
    if :: t_ra < 1 -> t_sleep = 1 :: else -> t_sleep = t_ra fi;
    if
    :: now - cstart[t_c] >= W ->
         /* nothing is left of the budget: the call ends as the spent-wait error */
         gTimeout = 1;
         calls[t_c]++;
         if :: calls[t_c] >= NC -> st[t_c] = 3 :: else -> st[t_c] = 0 fi
    :: else ->
         /* the last sleep is cut to what the budget has left and one more attempt follows, as the real client does
            (internal/llm/coddy.go, admit: sleep = min(backoff, remaining)) */
         if :: now + t_sleep - cstart[t_c] > W -> t_sleep = W - (now - cstart[t_c]) :: else -> skip fi;
         wake[t_c] = now + t_sleep;
         st[t_c] = 1
    fi
}

inline clearAttempt() {
    t_c = 0; t_k = 0; t_ref = 0; t_orig = 0; t_ra = 0; t_bR = 0; t_bN = 0; t_sleep = 0;
    t_relSlot = 0; t_relTok = 0; t_nodeSlot = 0; t_nodeTok = 0
}

proctype Client(byte c) {
    do
    :: st[c] == 3 -> break
    :: atomic {
         (st[c] == 0 || (st[c] == 1 && wake[c] <= now)) ->
         if :: st[c] == 0 -> cstart[c] = now :: else -> skip fi;
         t_c = c; t_k = cl[c];
         t_bR = rtok[t_k]; t_bN = ntok;
         relayLayer();
         nodeLayer();
         outcome();
#ifdef ASSERT_PROP
         assert(ASSERT_PROP == 0);
#endif
         clearAttempt()
       }
    od
}

/* the clock moves only when no client can act at the current tick; then calls end, tokens come */
inline endCall(i) {
    if
    :: st[i] == 2 && endAt[i] <= now ->
         if :: PLACE == 2 || PLACE == 3 -> ruse[cl[i]]-- :: else -> skip fi;
         if :: PLACE == 1 || PLACE == 3 -> nuse-- :: else -> skip fi;
         calls[i]++;
         if :: calls[i] >= NC -> st[i] = 3 :: else -> st[i] = 0 fi
    :: else -> skip
    fi
}

/* the clock moves only when no caller can act at the current tick; then calls end, tokens come */
proctype Clock() {
    do
    :: atomic {
         now < T &&
         !(st[0] == 0 || (st[0] == 1 && wake[0] <= now)) &&
         !(st[1] == 0 || (st[1] == 1 && wake[1] <= now)) &&
         !(st[2] == 0 || (st[2] == 1 && wake[2] <= now)) ->
         now++;
         endCall(0); endCall(1); endCall(2);
         if
         :: now % P == 0 ->
              if :: ntok < B -> ntok++ :: else -> skip fi
         :: else -> skip
         fi;
         if
         :: now % PR == 0 ->
              if :: rtok[0] < B -> rtok[0]++ :: else -> skip fi;
              if :: rtok[1] < B -> rtok[1]++ :: else -> skip fi
         :: else -> skip
         fi;
         if :: now >= T && (st[0] != 3 || st[1] != 3 || st[2] != 3) -> gStuck = 1 :: else -> skip fi;
#ifdef ASSERT_PROP
         assert(ASSERT_PROP == 0);
#endif
         skip
       }
    :: now >= T -> break
    od
}

init {
    atomic {
        rtok[0] = B; rtok[1] = B;
        cl[0] = 0;
#if NCALL == 1
        st[0] = 0; st[1] = 3; st[2] = 3;
#elif NCALL == 2
        cl[1] = 1;
        st[0] = 0; st[1] = 0; st[2] = 3;
#else
        cl[1] = 0; cl[2] = 1;
        st[0] = 0; st[1] = 0; st[2] = 0;
#endif
        run Client(0);
        run Client(1);
        run Client(2);
        run Clock()
    }
}
