/*
 * m3-stream.pml - the SSE stream state machine of POST /coddy/llm/completions and the client's handling of it.
 *
 * Spec: docs/plans/remote-model-provider.md  4.2 (frames, chunk/final/error, exactly one terminal event,
 * partial Response, stall), 4.3 (status-ordered classification, the kind table, typed errors, the partial
 * Response on a failure after a chunk), 4.1b (busy budget, only as a counter), 5a rows Server/Client.
 * Code facts (read, not edited): internal/llm/resilient.go (isRetryableLLMError, httpStatusFromError,
 * callWithRetry), internal/llm/openai_stream.go (emitted, partial Response, unfinished tool calls dropped),
 * internal/llm/upstream_error.go (IsTransientProviderError), internal/llm/transport.go (streamStalledError has
 * NO emitted field), internal/agent/react.go (recovery path, partial persistence).
 *
 * PROCESSES
 *   Server : the remote. One handler per connection (request on `req`, frames on `net`). Nondeterministic:
 *            a pre-stream HTTP answer (relay/proxy/coddy statuses) or a cut; else SSE: heartbeat comments,
 *            remote-side upstream retries (up to R_REMOTE) while nothing is emitted, chunks (text, reasoning,
 *            tool_call_named / tool_call_delta / tool_call for ids 0 and 1), then exactly one terminal
 *            (final with the full response, or error{kind,cause,emitted}) or an abrupt EOF at any point;
 *            junk frames (up to J_MAX) after the terminal.
 *   Client : the outer resilient wrapper (R_LOCAL retries, the `emitted` contract), the inner stream reader
 *            (accumulates chunks, final = result, discards what follows the terminal), the typed-error layer,
 *            the busy wait as a counter, and the agent loop (executes tool calls of the RETURNED Response only,
 *            recovery re-issue after IsTransientProviderError, partial persistence).
 *   init   : first an exhaustive decision-table check of the status-ordered classification over all
 *            (status, body, emitted) triples, then it starts Server and Client.
 *
 * ABSTRACTIONS (all declared in m3-stream.md)
 *   - untimed; time-driven things (busy deadline, heartbeat period, idle guard) are NOT here (see
 *     m3-stream-timers.pml for a discrete-clock abstraction of the two stall guards and the first-token timer).
 *     The busy wait is a counter (BUSY_MAX requests answered `busy` before the wait budget is spent).
 *   - one model turn = one agent step plus at most REC_MAX recovery re-issues; the tool results are not modelled.
 *   - the network is a FIFO channel of capacity 1; a connection is closed by the client (frames the server
 *     writes afterwards are dropped, frames already buffered carry the old connection id and are ignored).
 *   - tool-call ids are {0,1}; text is counted in chunks (units), not characters.
 *   - scaled constants (defaults of the product in brackets): R_LOCAL=2 [3], R_REMOTE=1 [3], REC_MAX=1 [2],
 *     BUSY_MAX=1, HB_MAX=1, J_MAX=1, CH_MAX=3 chunks per connection.
 *
 * VARIANTS (-D): none = SPEC client (the semantics of the 4.3 table);
 *   BUG_A  tool calls are run when a tool_call chunk arrives and again from the final
 *   BUG_B  retries after output (the `emitted` flag is not propagated into the typed errors)
 *   BUG_C  a bare coddyAPIError: a stall after the first chunk is unknown to the five predicates and the
 *          partial Response is discarded
 *   BUG_D  frames after the terminal are not discarded
 *   BUG_E  the final is appended to what the chunks built
 *   OPT1   the FIRST alternative of 4.3 taken literally: *streamStalledError / *streamTruncatedError /
 *          *streamServerError{code,emitted} built from cause and emitted, plain HTTP statuses passed through
 *   HTTPPASS  every non-kind HTTP status is handed on as a provider status error (a plain 429 or 500 then
 *          matches isRetryableLLMError's by-status set)
 *   OPT1W  OPT1 with the stall wrapped as streamTransportError{cause: stalled, emitted} (as openai_stream.go does)
 *   FIX_PERSIST  (proposal) the registered coddyAPIError of kind upstream reports IsStreamTruncated after output
 *   BUSY_SCOPE=0|1  busy budget per Stream call (0) or per inner attempt (1, default)
 *
 * SWITCHES: BOUNDS (count client requests and upstream requests per step and check the bounds), TOTALS (the
 * same over the whole turn), SANITY (history flags seen_*, lost_vis_*, hit_* for reachability checks; they
 * multiply the state space, so they exist only in this build). Constants R_LOCAL, R_REMOTE, REC_MAX, BUSY_MAX,
 * HB_MAX, J_MAX, CH_MAX are -D overridable.
 *
 * PROPERTIES. Every property is a violation flag v_* set by check(); each is checked by its own LTL formula
 * `[] !v_x` (the engine's CLI has no `invariant` kind), plus `<> fin`, `[] (exec0 <= 1 && exec1 <= 1)` and
 * the engine's own `deadlock`. v_table = the decision table (init); v_once = Stream returns once; v_dupvis = the
 * output of two attempts is never both visible; v_bound = request bounds; v_term = one terminal consumed per
 * attempt; v_junk = frames after the terminal change nothing; v_part = the partial Response next to an error;
 * v_noret = no retry after output; v_forb = no retry the table forbids; v_compl = what the table retries is
 * retried; v_rec = transient failures are recovered, limits and refusals are not; v_cut = a cut or stalled
 * stream keeps its visible text; v_exec_nofin / v_exec_iff = a tool call runs once iff a final carrying it was
 * delivered; v_final = the result is the final, not final+chunks; v_saw = what the user saw is what is returned.
 *
 * REPRODUCE (slices; no single scale holds every dimension, see m3-stream.md section 5):
 *   mcd check --promela m3-stream.pml -D R_LOCAL=2 -D REC_MAX=1 -D BUSY_MAX=1 -D J_MAX=0 -D HB_MAX=0 -D CH_MAX=1 -D BOUNDS \
 *       --ltl '[]!v_table' ... (all v_*) --ltl '<>fin' --ltl '[](exec0<=1&&exec1<=1)'
 */

#ifndef R_LOCAL
#define R_LOCAL 2
#endif
#ifndef R_REMOTE
#define R_REMOTE 1
#endif
#ifndef REC_MAX
#define REC_MAX 1
#endif
#ifndef BUSY_MAX
#define BUSY_MAX 1
#endif
#ifndef HB_MAX
#define HB_MAX 1
#endif
#ifndef J_MAX
#define J_MAX 1
#endif
#ifndef CH_MAX
#define CH_MAX 3
#endif
#ifndef BUSY_SCOPE
#define BUSY_SCOPE 1
#endif

#ifndef WORST
#define WORST 0   /* 1: the server only fails transiently upstream and then cuts the connection; 2: it fails upstream and then answers error{upstream} (worst cases of the request counts) */
#endif

#if BUSY_SCOPE == 1
#define BOUND_REQ ((R_LOCAL + 1) * (BUSY_MAX + 1))
#else
#define BOUND_REQ ((R_LOCAL + 1) + BUSY_MAX)
#endif
#ifndef BOUND_UP
#define BOUND_UP ((R_LOCAL + 1) * (R_REMOTE + 1))
#endif

mtype = { HB, CT, CR, TN, TD, TC, FIN, ERR, EOFF, HERR };

/* error kinds and causes of the wire */
#define K_BUSY 0
#define K_RATE 1
#define K_QUOTA 2
#define K_UPS 3
#define K_INV 4
#define K_AUTH 5
#define C_STATUS 0
#define C_STALL 1
#define C_TRUNC 2

/* body classes of an HTTP answer; B_KIND + k is a coddy error JSON of kind k */
#define B_NONE 0
#define B_PLAIN 1
#define B_HTML 2
#define B_HOP 3
#define B_KIND 4

/* results of the status-ordered classification */
#define CL_NONE 0
#define CL_AUTH 1
#define CL_TRANSIENT 2
#define CL_INVALID 3
#define CL_NOOFFER 4
#define CL_OTHER 5
#define CL_BUSY 6
#define CL_RATE 7
#define CL_QUOTA 8
#define CL_UPS 9

/* the Go error types the client can hand to the wrapper and to the agent */
#define ET_NONE 0
#define ET_TRUNC 1      /* *streamTruncatedError{emitted}                         */
#define ET_STALL_BARE 2 /* *streamStalledError: has no emitted field              */
#define ET_TSTALL 3     /* *streamTransportError{cause: stalled, emitted}         */
#define ET_SRV 4        /* *streamServerError{code, emitted}                      */
#define ET_HTTP 5       /* a provider status error carrying StatusCode            */
#define ET_QUOTA 6      /* *QuotaResetError                                       */
#define ET_CODDY 7      /* coddyAPIError registered in the five predicates        */
#define ET_BARE 8       /* coddyAPIError no predicate knows, status zero          */

/* classes of a failed attempt (ground truth about what happened) */
#define F_NONE 0
#define F_EOF 1   /* the stream ended without a terminal event (transport failure) */
#define F_HERR 2  /* a pre-stream HTTP answer that is not a coddy error             */
#define F_ERR 3   /* a coddy error: terminal frame, or an HTTP answer with a kind    */

chan req = [0] of { byte };
chan net = [1] of { byte, mtype, short, byte };
byte curConn = 2;            /* connection ids alternate 1,2: at most one stale frame can be buffered */
bool connClosed = 1;

/* ground truth written by the server for the connection it serves */
byte srv_text = 0, srv_reason = 0, srv_t0 = 0, srv_t1 = 0;
byte srv_fin_text = 0, srv_fin_m0 = 0, srv_fin_m1 = 0;

/* counters of the current step (reset when a step starts) */
#ifdef TOTALS
byte up_tot = 0;                         /* upstream requests of the whole turn (all steps) */
#endif
#ifdef BOUNDS
byte reqs_step = 0, up_step = 0;        /* requests of the client / upstream requests of the remote in this step */
#endif
byte vis_att = 0, retcnt = 0, exec0 = 0, exec1 = 0, saw_text = 0;
bool fin = false;

#ifdef SANITY
/* sanity (reach) and information flags: history variables, so they exist only in the SANITY build */
bool seen_final = false, seen_err_after_out = false, seen_eof_retry = false, seen_recovery = false;
bool seen_hb = false, seen_stale = false, seen_junk = false, seen_busy = false, seen_exec = false;
bool seen_partial = false, seen_unfinished_dropped = false, seen_hop_retry = false, seen_ws = false;
bool lost_vis_status = false, lost_vis_other = false, lost_vis_cut = false;
bool hit_up = false, hit_req = false, hit_tot = false;
#endif

/* violation flags: one per property, each checked by its own LTL formula [] !v_x */
bool v_table = false, v_once = false, v_dupvis = false, v_bound = false, v_term = false, v_junk = false;
bool v_part = false, v_noret = false, v_forb = false, v_compl = false, v_rec = false, v_cut = false;
bool v_exec_nofin = false, v_exec_iff = false, v_final = false, v_saw = false;

inline check(vf, vc) {
  if
  :: (vc) -> skip
  :: else -> vf = true
  fi
}

/* classification outputs */
byte c_cls = 0;
bool c_retry = false;

/* ---------- the status-ordered classification of 4.3 ---------- */
inline classify(cs, cb, ce) {
  c_cls = CL_NONE;
  c_retry = false;
  if
  :: cb >= B_KIND ->
       /* a response with a coddy kind follows the table, whatever its status */
       if
       :: cb == B_KIND + K_BUSY -> c_cls = CL_BUSY
       :: cb == B_KIND + K_RATE -> c_cls = CL_RATE
       :: cb == B_KIND + K_QUOTA -> c_cls = CL_QUOTA
       :: cb == B_KIND + K_UPS -> c_cls = CL_UPS
       :: cb == B_KIND + K_INV -> c_cls = CL_INVALID
       :: else -> c_cls = CL_AUTH
       fi
  :: cb < B_KIND ->
       if
       :: cs == 401 || cs == 403 -> c_cls = CL_AUTH
       :: (cs == 408 || cs == 502 || cs == 503 || cs == 504) && ce == 0 ->
            c_cls = CL_TRANSIENT;
            c_retry = true
       :: cs == 404 && cb == B_HOP -> c_cls = CL_INVALID
       :: cs == 200 && cb == B_HTML -> c_cls = CL_NOOFFER
       :: else -> c_cls = CL_OTHER
       fi
  fi
}

/* the decision table, stated independently of classify(): one assert per row of 4.3 */
inline table_oracle(os, ob, oe) {
  check(v_table, c_cls != CL_NONE);                                   /* total */
  if
  :: ob >= B_KIND ->
       check(v_table, !c_retry);                                      /* a coddy kind is never retried by status */
       check(v_table, (ob == B_KIND + K_BUSY) == (c_cls == CL_BUSY));
       check(v_table, (ob == B_KIND + K_RATE) == (c_cls == CL_RATE));
       check(v_table, (ob == B_KIND + K_QUOTA) == (c_cls == CL_QUOTA));
       check(v_table, (ob == B_KIND + K_UPS) == (c_cls == CL_UPS));
       check(v_table, (ob == B_KIND + K_INV) == (c_cls == CL_INVALID));
       check(v_table, (ob == B_KIND + K_AUTH) == (c_cls == CL_AUTH))
  :: ob < B_KIND ->
       check(v_table, (os == 401 || os == 403) == (c_cls == CL_AUTH));
       check(v_table, c_retry == ((os == 408 || os == 502 || os == 503 || os == 504) && oe == 0));
       check(v_table, c_retry == (c_cls == CL_TRANSIENT));
       check(v_table, (c_cls == CL_INVALID) == (os == 404 && ob == B_HOP));
       check(v_table, (c_cls == CL_NOOFFER) == (os == 200 && ob == B_HTML));
       check(v_table, (c_cls == CL_OTHER) == (!(os == 401 || os == 403) && !((os == 408 || os == 502 || os == 503 || os == 504) && oe == 0) && !(os == 404 && ob == B_HOP) && !(os == 200 && ob == B_HTML)))
  fi;
  check(v_table, !(oe == 1 && c_retry))                               /* nothing is retried after output */
}

/* ---------- the remote ---------- */
inline emit(et, ea, eb) {
  if
  :: atomic { (!(connClosed || curConn != cid) && nfull(net)) -> net ! cid, et, ea, eb }
  :: (connClosed || curConn != cid) -> goto HEND
  fi
}

proctype Server() {
  byte cid, rem, hbl, jl, tot, tt, tr, s0, s1, m0, m1;
  end:
  do
  :: req ? cid ->
       rem = R_REMOTE; hbl = 0; jl = 0; tot = 0; tt = 0; tr = 0; s0 = 0; s1 = 0;
       /* before the stream: a cut, an HTTP answer of a hop or of the remote, or the 200 SSE answer */
       if
       :: WORST == 0 -> emit(EOFF, 0, 0); goto HEND
       :: WORST == 0 -> emit(HERR, 401, B_PLAIN); goto HEND                 /* plain-text 401 of authGate        */
       :: WORST == 0 -> emit(HERR, 408, B_NONE); goto HEND
       :: WORST == 0 -> emit(HERR, 502, B_HOP); goto HEND                   /* relay: node unreachable           */
       :: WORST == 0 -> emit(HERR, 404, B_HOP); goto HEND                   /* relay: hop error                  */
       :: WORST == 0 -> emit(HERR, 404, B_PLAIN); goto HEND
       :: WORST == 0 -> emit(HERR, 500, B_PLAIN); goto HEND
       :: WORST == 0 -> emit(HERR, 200, B_HTML); goto HEND                  /* SPA fallback on the POST route    */
       :: WORST == 0 -> emit(HERR, 429, B_KIND + K_BUSY); goto HEND
       :: WORST == 0 -> emit(HERR, 429, B_KIND + K_RATE); goto HEND
       :: WORST == 0 -> emit(HERR, 429, B_KIND + K_QUOTA); goto HEND
       :: WORST == 0 -> emit(HERR, 502, B_KIND + K_UPS); goto HEND
       :: WORST == 0 -> emit(HERR, 400, B_KIND + K_INV); goto HEND
       :: WORST == 0 -> emit(HERR, 401, B_KIND + K_AUTH); goto HEND
       :: WORST == 0 -> emit(ERR, K_BUSY, C_STATUS * 2); goto AFTERTERM     /* busy announced as a frame, before the provider is touched */
       :: skip
       fi;
       /* 200 text/event-stream: the first heartbeat comes right after the headers */
       if
       :: hbl < HB_MAX -> emit(HB, 0, 0); hbl++
       :: skip
       fi;
UPLOOP:
       /* the remote's own provider call; the remote retries while nothing was emitted */
#ifdef BOUNDS
       up_step++;
#endif
#ifdef TOTALS
       up_tot++;
#endif
       if
       :: WORST == 0 -> goto STREAM
       :: /* transient upstream failure */
          if
          :: rem > 0 ->
               rem--;
               if
               :: hbl < HB_MAX -> emit(HB, 0, 0); hbl++
               :: skip
               fi;
               goto UPLOOP
          :: rem == 0 -> goto UPTERM
          fi
       :: WORST == 0 -> emit(EOFF, 0, 0); goto HEND            /* the connection is cut at any point */
       :: WORST == 0 -> emit(ERR, K_INV, C_STATUS * 2); goto AFTERTERM      /* permanent: not retried by the remote */
       :: WORST == 0 -> emit(ERR, K_AUTH, C_STATUS * 2); goto AFTERTERM
       fi;
UPTERM:
       /* the remote spent its retries before emitting anything */
       if
       :: WORST == 0 -> emit(ERR, K_RATE, C_STATUS * 2); goto AFTERTERM
       :: WORST == 0 -> emit(ERR, K_QUOTA, C_STATUS * 2); goto AFTERTERM
       :: WORST != 1 -> emit(ERR, K_UPS, C_STATUS * 2); goto AFTERTERM
       :: WORST == 0 -> emit(ERR, K_UPS, C_STALL * 2); goto AFTERTERM
       :: WORST == 0 -> emit(ERR, K_UPS, C_TRUNC * 2); goto AFTERTERM
       :: WORST != 2 -> emit(EOFF, 0, 0); goto HEND
       fi;
STREAM:
       do
       :: tot < CH_MAX && tt < 2 -> emit(CT, 0, 0); tt++; tot++; srv_text++
       :: tot < CH_MAX && tr < 1 -> emit(CR, 0, 0); tr++; tot++; srv_reason++
       :: tot < CH_MAX && s0 == 0 -> emit(TN, 0, 0); s0 = 1; tot++; srv_t0 = 1
       :: tot < CH_MAX && s0 >= 1 && s0 < 3 -> emit(TD, 0, 0); s0 = 2; tot++; srv_t0 = 2
       :: tot < CH_MAX && s0 < 3 -> emit(TC, 0, 0); s0 = 3; tot++; srv_t0 = 3
       :: tot < CH_MAX && s1 == 0 -> emit(TN, 1, 0); s1 = 1; tot++; srv_t1 = 1
       :: tot < CH_MAX && s1 >= 1 && s1 < 3 -> emit(TD, 1, 0); s1 = 2; tot++; srv_t1 = 2
       :: tot < CH_MAX && s1 < 3 -> emit(TC, 1, 0); s1 = 3; tot++; srv_t1 = 3
       :: hbl < HB_MAX -> emit(HB, 0, 0); hbl++
       :: /* the provider returned: final = the full response, tool calls included */
          if
          :: s0 == 3 -> m0 = 1
          :: s0 < 3 -> m0 = 0
          :: s0 < 3 -> m0 = 1
          fi;
          if
          :: s1 == 3 -> m1 = 1
          :: s1 < 3 -> m1 = 0
          :: s1 < 3 -> m1 = 1
          fi;
          srv_fin_text = tt; srv_fin_m0 = m0; srv_fin_m1 = m1;
          if
          :: tr > 0 -> emit(FIN, tt, m0 + 2 * m1 + 4)
          :: tr == 0 -> emit(FIN, tt, m0 + 2 * m1)
          fi;
          goto AFTERTERM
       :: /* the provider failed after the remote's call began; emitted says whether a chunk went out */
          if
          :: tot > 0 ->
               if
               :: emit(ERR, K_RATE, C_STATUS * 2 + 1)
               :: emit(ERR, K_QUOTA, C_STATUS * 2 + 1)
               :: emit(ERR, K_UPS, C_STATUS * 2 + 1)
               :: emit(ERR, K_UPS, C_STALL * 2 + 1)
               :: emit(ERR, K_UPS, C_TRUNC * 2 + 1)
               :: emit(ERR, K_INV, C_STATUS * 2 + 1)
               :: emit(ERR, K_AUTH, C_STATUS * 2 + 1)
               fi
          :: tot == 0 ->
               if
               :: emit(ERR, K_RATE, C_STATUS * 2)
               :: emit(ERR, K_UPS, C_STALL * 2)
               :: emit(ERR, K_UPS, C_TRUNC * 2)
               :: emit(ERR, K_UPS, C_STATUS * 2)
               fi
          fi;
          goto AFTERTERM
       :: emit(EOFF, 0, 0); goto HEND                         /* abrupt EOF / reset at any frame boundary */
       od;
AFTERTERM:
       /* anything after the terminal event is junk; then the stream ends */
       do
       :: jl < J_MAX ->
            if
            :: emit(CT, 0, 0)
            :: emit(TC, 1, 0)
            :: emit(FIN, 1, 3)
            :: emit(ERR, K_UPS, C_STATUS * 2 + 1)
            fi;
#ifdef SANITY
            seen_junk = true;
#endif
            jl++
       :: break
       od;
       emit(EOFF, 0, 0);
HEND:
       skip
  od
}

/* ---------- the Go error layer: typed errors and the predicates of resilient.go / upstream_error.go ---------- */

/* isRetryableLLMError, restricted to the types the client can build */
inline p_retryable() {
  retry = false;
  if
  :: ety == ET_TRUNC -> if :: eem == 0 -> retry = true :: else -> skip fi
  :: ety == ET_STALL_BARE -> retry = true
  :: ety == ET_TSTALL -> if :: eem == 0 -> retry = true :: else -> skip fi
  :: ety == ET_SRV ->
       /* httpStatusFromError returns 0 once emitted, else the code */
       if
       :: eem == 0 && (ecode == 429 || ecode == 408 || ecode == 500 || ecode == 502 || ecode == 503 || ecode == 504) -> retry = true
       :: else -> skip
       fi
  :: ety == ET_HTTP ->
       if
       :: ecode == 429 || ecode == 408 || ecode == 500 || ecode == 502 || ecode == 503 || ecode == 504 -> retry = true
       :: else -> skip
       fi
  :: else -> skip
  fi
}

/* IsTransientProviderError */
inline p_transient() {
  trans = false;
  if
  :: ety == ET_SRV || ety == ET_HTTP ->
       if
       :: ecode == 408 || ecode == 500 || ecode == 502 || ecode == 503 || ecode == 504 || ecode == 529 -> trans = true
       :: else -> skip
       fi
  :: ety == ET_TRUNC || ety == ET_STALL_BARE || ety == ET_TSTALL -> trans = true
  :: ety == ET_CODDY -> if :: ekind == K_UPS -> trans = true :: else -> skip fi
  :: else -> skip
  fi
}

inline p_trunc() {
  istr = false;
  if
  :: ety == ET_TRUNC -> istr = true
  :: ety == ET_CODDY && ecause == C_TRUNC -> istr = true
#ifdef FIX_PERSIST
  :: ety == ET_CODDY && ekind == K_UPS && em_own > 0 -> istr = true   /* the partial of an upstream failure after output survives */
#endif
  :: else -> skip
  fi
}

inline p_stalled() {
  isst = false;
  if
  :: ety == ET_STALL_BARE || ety == ET_TSTALL -> isst = true
  :: ety == ET_CODDY && ecause == C_STALL -> isst = true
  :: else -> skip
  fi
}

/* the typed error the client builds from what happened */
inline mkerr() {
  ety = ET_NONE; ecode = 0; eem = 0; ekind = f_kind; ecause = f_cause;
  if
  :: f_class == F_EOF -> ety = ET_TRUNC; eem = em_own
  :: f_class == F_HERR ->
#ifdef HTTPPASS
       ety = ET_HTTP; ecode = f_st                                 /* every HTTP status handed on as a provider status error */
#else
       if
       :: c_cls == CL_TRANSIENT -> ety = ET_HTTP; ecode = f_st
       :: c_cls == CL_AUTH -> ety = ET_CODDY; ekind = K_AUTH
       :: c_cls == CL_INVALID -> ety = ET_CODDY; ekind = K_INV
       :: else -> ety = ET_BARE
       fi
#endif
  :: f_class == F_ERR ->
       eem = f_em;
#if defined(OPT1) || defined(OPT1W) || defined(BUG_B)
       if
       :: f_cause == C_STALL ->
#ifdef OPT1W
            ety = ET_TSTALL
#else
            ety = ET_STALL_BARE
#endif
       :: f_cause == C_TRUNC -> ety = ET_TRUNC
       :: f_cause == C_STATUS ->
#ifdef BUG_B
            if :: f_kind == K_QUOTA -> ety = ET_QUOTA :: else -> ety = ET_CODDY fi
#else
            if
            :: f_kind == K_QUOTA -> ety = ET_QUOTA
            :: f_kind == K_RATE || f_kind == K_BUSY -> ety = ET_SRV; ecode = 429
            :: f_kind == K_UPS -> ety = ET_SRV; ecode = 502
            :: f_kind == K_INV -> ety = ET_SRV; ecode = 400
            :: else -> ety = ET_SRV; ecode = 401
            fi
#endif
       fi
#else
       if :: f_kind == K_QUOTA -> ety = ET_QUOTA :: else -> ety = ET_CODDY fi
#endif
  fi;
#ifdef BUG_B
  eem = 0;
#endif
#ifdef BUG_C
  if
  :: f_class == F_ERR && f_kind == K_UPS && f_cause == C_STALL && em_own > 0 -> ety = ET_BARE
  :: else -> skip
  fi
#endif
}

/* the partial *Response next to the error: built from the chunks, unfinished tool calls dropped */
inline mkpartial() {
  has_resp = false; p_text = 0; p_reason = 0; p_m0 = 0; p_m1 = 0;
  if
  :: em_own > 0 && (acc_text > 0 || acc_reason > 0 || a0 == 3 || a1 == 3) ->
       has_resp = true; p_text = acc_text; p_reason = acc_reason;
       if :: a0 == 3 -> p_m0 = 1 :: else -> skip fi;
       if :: a1 == 3 -> p_m1 = 1 :: else -> skip fi
  :: else -> skip
  fi;
#ifdef BUG_C
  if
  :: ety == ET_BARE && f_class == F_ERR -> has_resp = false; p_text = 0; p_reason = 0; p_m0 = 0; p_m1 = 0
  :: else -> skip
  fi
#endif
}

/* what the 4.2/4.3 text promises about one finished step (success or failure) */
inline endstep() {
  check(v_once, retcnt == 1);                                   /* Stream returned exactly once            */
  check(v_dupvis, vis_att <= 1);                                  /* no output of two attempts is visible    */
#ifdef BOUNDS
  check(v_bound, reqs_step <= BOUND_REQ);                        /* requests of the client (busy included)  */
  check(v_bound, up_step <= BOUND_UP);                           /* remote upstream requests of one step    */
#ifdef SANITY
  if :: reqs_step == BOUND_REQ -> hit_req = true :: else -> skip fi;
  if :: up_step == BOUND_UP -> hit_up = true :: else -> skip fi;
#endif
#endif
#ifdef TOTALS
  check(v_bound, up_tot <= (recs + 1) * BOUND_UP);
#ifdef SANITY
  if :: up_tot == (REC_MAX + 1) * BOUND_UP -> hit_tot = true :: else -> skip fi;
#endif
#endif
  skip
}

inline stepreset() {
#ifdef BOUNDS
  reqs_step = 0; up_step = 0;
#endif
  vis_att = 0; retcnt = 0; exec0 = 0; exec1 = 0; saw_text = 0
}

/* flags exist only in the SANITY build */
#ifdef SANITY
#define FLAG(x) x = true
#else
#define FLAG(x) skip
#endif

proctype Client() {
  byte fcid; mtype ft; short fa; byte fb;
  byte retriesLeft, busyLeft, recs;
  byte acc_text, acc_reason, em_own, terms, a0, a1;
  byte f_class, f_kind, f_cause, f_em; short f_st; byte f_body;
  byte ety, eem, ekind, ecause; short ecode;
  bool retry, t_retry, trans, istr, isst, has_resp, g_trans, g_cut, reissue;
  byte p_text, p_reason, p_m0, p_m1, e0, e1;
  byte r_text, r_m0, r_m1, stored;
#ifdef BUG_D
  bool viahttp;                       /* the error came as an HTTP answer: no stream, nothing to drain */
  bool pre_ok; byte pre_text, pre_m0, pre_m1, pre_saw;
  bool post_ok; byte post_text, post_m0, post_m1, post_saw;
#endif

  recs = 0;
STEP:
  stepreset();
  retriesLeft = R_LOCAL;
  busyLeft = BUSY_MAX;
ATTEMPT:
#if BUSY_SCOPE == 1
  busyLeft = BUSY_MAX;
#endif
REQUEST:
#ifdef BOUNDS
  reqs_step++;
#endif
  curConn = 3 - curConn; connClosed = false;
  /* everything derived from the previous attempt is cleared: dead values would only multiply states */
  acc_text = 0; acc_reason = 0; em_own = 0; terms = 0; a0 = 0; a1 = 0;
#ifdef BUG_D
  viahttp = false;
#endif
  f_class = F_NONE; f_kind = 0; f_cause = 0; f_em = 0; f_st = 0; f_body = 0;
  c_cls = CL_NONE; c_retry = false;
  ety = ET_NONE; eem = 0; ekind = 0; ecause = 0; ecode = 0;
  retry = false; t_retry = false; trans = false; istr = false; isst = false; has_resp = false;
  g_trans = false; g_cut = false; reissue = false;
  p_text = 0; p_reason = 0; p_m0 = 0; p_m1 = 0; e0 = 0; e1 = 0;
  r_text = 0; r_m0 = 0; r_m1 = 0; stored = 0;
  fcid = 0; fa = 0; fb = 0;
  srv_text = 0; srv_reason = 0; srv_t0 = 0; srv_t1 = 0;
  srv_fin_text = 0; srv_fin_m0 = 0; srv_fin_m1 = 0;
  req ! curConn;
READ:
  do
  :: net ? fcid, ft, fa, fb ->
       if
       :: fcid != curConn -> FLAG(seen_stale)                  /* a frame of a connection already closed */
       :: fcid == curConn ->
            if
            :: ft == HB -> FLAG(seen_hb)                      /* a comment: neither a chunk nor an event */
            :: ft == CT -> acc_text++; em_own = 1; saw_text++
            :: ft == CR -> acc_reason++; em_own = 1
            :: ft == TN -> em_own = 1;
                 if
                 :: fa == 0 && a0 < 1 -> a0 = 1
                 :: fa == 1 && a1 < 1 -> a1 = 1
                 :: else -> skip
                 fi
            :: ft == TD -> em_own = 1;
                 if
                 :: fa == 0 && a0 < 2 -> a0 = 2
                 :: fa == 1 && a1 < 2 -> a1 = 2
                 :: else -> skip
                 fi
            :: ft == TC -> em_own = 1;
                 if
                 :: fa == 0 -> a0 = 3
                 :: else -> a1 = 3
                 fi;
#ifdef BUG_A
                 if
                 :: fa == 0 -> exec0++
                 :: else -> exec1++
                 fi;
                 FLAG(seen_exec)
#else
                 skip
#endif
            :: ft == FIN -> goto GOT_FIN
            :: ft == ERR -> goto GOT_ERR
            :: ft == EOFF -> goto GOT_EOF
            :: ft == HERR -> goto GOT_HERR
            fi
       fi;
       fa = 0; fb = 0; fcid = 0; ft = HB                        /* nothing of the consumed frame is kept */
  od;

GOT_FIN:
  terms++; check(v_term, terms == 1);                                 /* exactly one terminal event consumed */
  FLAG(seen_final);
  r_text = fa; r_m0 = fb % 2; r_m1 = (fb / 2) % 2;
#ifdef BUG_E
  r_text = acc_text + fa;                                      /* the final appended to what chunks built */
#endif
  fa = 0; fb = 0; fcid = 0; ft = HB;
#ifdef BUG_D
  pre_ok = true; pre_text = r_text; pre_m0 = r_m0; pre_m1 = r_m1; pre_saw = saw_text;
  post_ok = pre_ok; post_text = pre_text; post_m0 = pre_m0; post_m1 = pre_m1; post_saw = pre_saw;
  /* the reader goes on after the terminal event and applies what follows */
  do
  :: net ? fcid, ft, fa, fb ->
       if
       :: fcid != curConn -> skip
       :: fcid == curConn ->
            if
            :: ft == EOFF -> break
            :: ft == FIN -> post_ok = true; post_text = fa; post_m0 = fb % 2; post_m1 = (fb / 2) % 2
            :: ft == ERR -> post_ok = false
            :: ft == CT -> post_saw++; post_text++
            :: ft == TC -> post_m1 = 1
            :: else -> skip
            fi
       fi
  od;
  check(v_junk, post_ok == pre_ok && post_text == pre_text && post_m0 == pre_m0 && post_m1 == pre_m1 && post_saw == pre_saw);
#endif
  connClosed = true;
  goto OK;

GOT_ERR:
  terms++; check(v_term, terms == 1);
  f_class = F_ERR; f_kind = fa; f_cause = fb / 2; f_em = fb % 2;
  fa = 0; fb = 0; fcid = 0; ft = HB;
  goto COMMON_ERR;

GOT_HERR:
  terms++; check(v_term, terms == 1);
  f_st = fa; f_body = fb;
  fa = 0; fb = 0; fcid = 0; ft = HB;
  classify(f_st, f_body, em_own);
  if
  :: f_body >= B_KIND ->
       f_class = F_ERR; f_kind = f_body - B_KIND; f_cause = C_STATUS; f_em = 0;
#ifdef BUG_D
       viahttp = true;
#endif
       goto COMMON_ERR
  :: else ->
       f_class = F_HERR;
       goto CLOSE_FAILED
  fi;

GOT_EOF:
  fa = 0; fb = 0; fcid = 0; ft = HB;
  f_class = F_EOF;                                             /* EOF without a terminal: truncated */
  goto CLOSE_FAILED;

COMMON_ERR:
  /* the busy answer is waited out within its own budget and never touches llm_retry_max */
  if
  :: f_kind == K_BUSY && em_own == 0 && busyLeft > 0 ->
       busyLeft--; FLAG(seen_busy);
       connClosed = true;
       goto REQUEST
  :: else -> skip
  fi;
#ifdef BUG_D
  pre_ok = false; pre_text = acc_text; pre_saw = saw_text;
  post_ok = pre_ok; post_text = pre_text; post_saw = pre_saw;
  if
  :: viahttp -> goto SKIPDRAIN
  :: else -> skip
  fi;
  do
  :: net ? fcid, ft, fa, fb ->
       if
       :: fcid != curConn -> skip
       :: fcid == curConn ->
            if
            :: ft == EOFF -> break
            :: ft == FIN -> post_ok = true
            :: ft == CT -> post_saw++; post_text++
            :: else -> skip
            fi
       fi
  od;
  check(v_junk, post_ok == pre_ok && post_text == pre_text && post_saw == pre_saw);
SKIPDRAIN:
  viahttp = false;
#endif
  connClosed = true;
  goto FAILED;

CLOSE_FAILED:
  connClosed = true;

/* ---------- the attempt failed: type the error, decide the retry, build the partial ---------- */
FAILED:
  mkerr();
  mkpartial();
  p_retryable();
  /* ground truth, independent of the Go types: only a transport failure (EOF without a terminal, or a
     408/502/503/504 that is not a coddy kind) before any output is retried locally */
  if
  :: f_class == F_EOF && em_own == 0 -> t_retry = true
  :: f_class == F_HERR && c_cls == CL_TRANSIENT && em_own == 0 -> t_retry = true
  :: else -> skip
  fi;
  if
  :: em_own > 0 -> vis_att++; FLAG(seen_err_after_out)
  :: else -> skip
  fi;
  /* partial Response: after a failure with output it carries every text chunk, every reasoning chunk and the
     tool calls that were finished; unfinished ones are dropped */
  if
  :: em_own > 0 ->
       if
       :: srv_text > 0 || srv_reason > 0 || srv_t0 == 3 || srv_t1 == 3 ->
            check(v_part, has_resp);
            check(v_part, p_text == srv_text && p_reason == srv_reason);
            if :: srv_t0 == 3 -> e0 = 1 :: else -> e0 = 0 fi;
            if :: srv_t1 == 3 -> e1 = 1 :: else -> e1 = 0 fi;
            check(v_part, p_m0 == e0 && p_m1 == e1);
            FLAG(seen_partial)
       :: else -> skip
       fi;
       if
       :: (srv_t0 == 1 || srv_t0 == 2 || srv_t1 == 1 || srv_t1 == 2) && has_resp -> FLAG(seen_unfinished_dropped)
       :: else -> skip
       fi
  :: else -> check(v_part, !has_resp)
  fi;
  /* the outer wrapper: callWithRetry */
  if
  :: retry && retriesLeft > 0 ->
       check(v_noret, em_own == 0);                                    /* no retry after output: no duplicated output */
       check(v_forb, t_retry);                                        /* no retry for a kind the table forbids       */
       if :: f_class == F_EOF -> FLAG(seen_eof_retry) :: else -> FLAG(seen_hop_retry) fi;
       retriesLeft--;
       goto ATTEMPT
  :: else ->
       check(v_compl, !(t_retry && retriesLeft > 0));                  /* what the table retries is retried          */
       retcnt++
  fi;

/* ---------- the agent receives (partial Response, error) ---------- */
  p_transient();
  p_trunc();
  p_stalled();
  if
  :: f_class == F_EOF -> g_trans = true; g_cut = true
  :: f_class == F_ERR && f_kind == K_UPS -> g_trans = true; g_cut = (f_cause != C_STATUS)
  :: f_class == F_HERR && c_cls == CL_TRANSIENT -> g_trans = true
  :: else -> skip
  fi;
  if
  :: trans && recs < REC_MAX -> reissue = true
  :: else -> skip
  fi;
  check(v_rec, !(g_trans && recs < REC_MAX && !reissue));            /* a transient failure is recovered        */
  check(v_rec, !(reissue && !g_trans && f_class == F_ERR));          /* a limit, a refusal, busy are never recovered */
  /* what of the visible text survives */
  if
  :: reissue -> stored = saw_text                              /* keepInterruptedAnswer */
  :: else ->
       if
       :: has_resp && (istr || isst) -> stored = p_text        /* persisted next to the error */
       :: else -> skip
       fi
  fi;
#ifdef SANITY
  if
  :: !reissue && saw_text > stored ->
       if
       :: g_cut -> lost_vis_cut = true
       :: f_class == F_ERR && f_kind == K_UPS && f_cause == C_STATUS -> lost_vis_status = true
       :: else -> lost_vis_other = true
       fi
  :: else -> skip
  fi;
#endif
  check(v_cut, !(!reissue && g_cut && saw_text > stored));           /* a cut or stalled stream keeps its partial   */
  check(v_exec_nofin, exec0 == 0 && exec1 == 0);                            /* no tool call runs without a final           */
  endstep();
  if
  :: reissue ->
       FLAG(seen_recovery);
       recs++;
       goto STEP
  :: else -> fin = true
  fi;
  goto DONE;

/* ---------- success: the agent runs the tool calls of the RETURNED Response, and only those ---------- */
OK:
  if
  :: em_own > 0 -> vis_att++
  :: else -> skip
  fi;
  retcnt++;
  if
  :: r_m0 == 1 -> exec0++; FLAG(seen_exec)
  :: else -> skip
  fi;
  if
  :: r_m1 == 1 -> exec1++; FLAG(seen_exec)
  :: else -> skip
  fi;
  check(v_final, r_text == srv_fin_text);                              /* the final is authoritative: not appended    */
  check(v_saw, saw_text == r_text);                                  /* the user saw exactly what was returned      */
  check(v_exec_iff, exec0 == srv_fin_m0 && exec1 == srv_fin_m1);          /* once iff a final carrying the id arrived    */
  endstep();
  fin = true;
DONE:
  skip
}

short stl[13];

init {
  byte si, bi, ei;
  short s;
  stl[0] = 200; stl[1] = 400; stl[2] = 401; stl[3] = 403; stl[4] = 404; stl[5] = 408; stl[6] = 413;
  stl[7] = 429; stl[8] = 500; stl[9] = 502; stl[10] = 503; stl[11] = 504; stl[12] = 529;
  si = 0;
  do
  :: si < 13 ->
       s = stl[si]; bi = 0;
       do
       :: bi < 10 ->
            ei = 0;
            do
            :: ei < 2 -> classify(s, bi, ei); table_oracle(s, bi, ei); ei++
            :: ei >= 2 -> break
            od;
            bi++
       :: bi >= 10 -> break
       od;
       si++
  :: si >= 13 -> break
  od;
  /* the named rows of 4.3 */
  classify(401, B_PLAIN, 0); check(v_table, c_cls == CL_AUTH && !c_retry);            /* plain-text 401 of authGate -> auth */
  classify(401, B_HTML, 0); check(v_table, c_cls == CL_AUTH && !c_retry);             /* an HTML 401 of a proxy: same rule  */
  classify(403, B_NONE, 0); check(v_table, c_cls == CL_AUTH && !c_retry);
  classify(502, B_HOP, 0); check(v_table, c_cls == CL_TRANSIENT && c_retry);          /* relay 502 before output -> retried */
  classify(503, B_HTML, 0); check(v_table, c_cls == CL_TRANSIENT && c_retry);
  classify(408, B_NONE, 0); check(v_table, c_cls == CL_TRANSIENT && c_retry);
  classify(502, B_HOP, 1); check(v_table, c_cls == CL_OTHER && !c_retry);             /* ... never after output             */
  classify(404, B_HOP, 0); check(v_table, c_cls == CL_INVALID && !c_retry);           /* relay 404 hop error -> invalid     */
  classify(404, B_PLAIN, 0); check(v_table, c_cls == CL_OTHER && !c_retry);
  classify(200, B_HTML, 0); check(v_table, c_cls == CL_NOOFFER && !c_retry);          /* HTML 200 -> clear error            */
  classify(500, B_PLAIN, 0); check(v_table, c_cls == CL_OTHER && !c_retry);           /* other -> non-retryable             */
  classify(429, B_NONE, 0); check(v_table, c_cls == CL_OTHER && !c_retry);
  classify(502, B_KIND + K_UPS, 0); check(v_table, c_cls == CL_UPS && !c_retry);      /* a kind wins over a retryable status */
  classify(429, B_KIND + K_BUSY, 0); check(v_table, c_cls == CL_BUSY && !c_retry);
  run Server();
  run Client()
}
