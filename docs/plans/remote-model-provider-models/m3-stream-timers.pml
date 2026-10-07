/*
 * m3-stream-timers.pml - the "stall and liveness are two guards" bullet of plan 4.2, as a DISCRETE-CLOCK
 * abstraction (the engine checks untimed models only: time is the counter clk, one tick = one atomic world step;
 * this is NOT a timed automaton and the constants are ticks, not seconds).
 *
 * Question: which timer ends a call whose remote is alive but silent of chunks (a long remote backoff, a row
 * with stream: false that generates for minutes, a hung upstream)?
 *   guard 1  the CLIENT's byte-level idle guard (llm_stream_idle_timeout_ms, default 300 s, armed by the first
 *            body byte, re-armed by every byte, heartbeat comments included)   -> IDLE ticks
 *   guard 2  the REMOTE's model stall guard (error{upstream, cause: stall}); in the plan it exists only after
 *            the first chunk, and not for a row with stream: false
 *   guard 3  the AGENT's first-token timer (agent.llm_first_token_timeout_ms, default 90 s, internal/agent/
 *            react.go line ~908): armed when the LOCAL row streams, stopped only by a text/reasoning/tool chunk;
 *            a heartbeat comment is not a chunk                                  -> FT ticks
 * The plan names two guards; the code has three.
 *
 * Constants (ticks): H = max gap between bytes of a live remote (plan: 15 s), IDLE (300 s), FT (90 s),
 * SLOWMAX = longest silence of a live remote, PT = the remote's own request timeout of a blocking row.
 * Defaults keep the real ordering H < FT < IDLE (15 s < 90 s < 300 s) scaled to 2 < 4 < 6; SLOWMAX is
 * larger than FT (six minutes of generation against 90 s).
 *
 * VARIANTS (-D): none = the plan as written; FIX_A = a heartbeat re-arms the agent's first-token timer
 * (the client reports liveness); FIX_AB = FIX_A plus the remote applies a first-token guard of FT ticks to a
 * streaming row (a blocking row is bounded only by PT). IDLE=2 (= H) shows the byte guard cutting a live remote.
 *
 * PROPERTIES (violation flags and result codes, each `[] !x`): guard_on_live, ft_on_live, ft_on_hb, the unbounded wait
 * (clk == 14 && res == R_WAIT), and the reach checks res == R_OK / R_GUARD / R_FT / R_REMOTE_ERR.
 * REPRODUCE: mcd check --promela m3-stream-timers.pml [-D FIX_A|-D FIX_AB|-D IDLE=n] --ltl '[]!guard_on_live'
 *   --ltl '[]!ft_on_live' --ltl '[]!ft_on_hb' --ltl '[]!(clk==14&&res==R_WAIT)' --ltl '[]!(res==R_OK)' --ltl '[]!(res==R_GUARD)'
 *   --ltl '[]!(res==R_FT)' --ltl '[]!(res==R_REMOTE_ERR)' --no-timing
 */
#ifndef H
#define H 2
#endif
#ifndef IDLE
#define IDLE 6
#endif
#ifndef FT
#define FT 4
#endif
#ifndef SLOWMAX
#define SLOWMAX 7
#endif
#ifndef PT
#define PT 9
#endif
#define CLKMAX 14

#define R_WAIT 0
#define R_OK 1
#define R_GUARD 2
#define R_FT 3
#define R_REMOTE_ERR 4
byte res = R_WAIT;
byte clk = 0, since_byte = 0, since_srv = 0, ft_start = 0, slow = 0;
bool alive = true, hung = false, blocking = false, got_chunk = false, got_bytes = false;
/* flags for the properties */
bool guard_on_live = false;     /* the client guard cut a remote that was alive and within H */
bool ft_on_live = false;        /* the first-token timer cut a live remote that was about to answer */
bool ft_on_hb = false;          /* ... while heartbeats were arriving */

proctype World() {
  do
  :: res == R_WAIT && clk < CLKMAX ->
       atomic {
         clk++; since_byte++; since_srv++;
         /* the client's idle guard looks first (the adversarial order inside one tick) */
         if
         :: got_bytes && since_byte >= IDLE ->
              res = R_GUARD;
              if :: alive && since_srv <= H -> guard_on_live = true :: else -> skip fi
         :: else -> skip
         fi;
         /* the agent's first-token timer: until the first chunk */
         if
         :: res == R_WAIT && !got_chunk && clk - ft_start >= FT ->
              res = R_FT;
              if
              :: alive && !hung -> ft_on_live = true; if :: got_bytes -> ft_on_hb = true :: else -> skip fi
              :: else -> skip
              fi
         :: else -> skip
         fi;
         if
         :: res == R_WAIT && alive ->
              if
              :: alive = false                                    /* the remote process dies: no byte any more */
              :: !hung && clk >= slow ->                          /* the answer is ready: the first chunk, then the final */
                   got_chunk = true; res = R_OK
              :: since_srv >= H ->                                /* the heartbeat is due */
                   since_srv = 0; since_byte = 0; got_bytes = true;
#ifdef FIX_A
                   ft_start = clk;
#endif
#ifdef FIX_AB
                   ft_start = clk;
#endif
              :: since_srv < H ->                                 /* an early heartbeat is allowed */
                   since_srv = 0; since_byte = 0; got_bytes = true;
#ifdef FIX_A
                   ft_start = clk;
#endif
#ifdef FIX_AB
                   ft_start = clk;
#endif
              :: since_srv < H -> skip                            /* nothing to write yet */
              fi
         :: else -> skip
         fi;
#ifdef FIX_AB
         /* the remote's own first-token guard of a streaming row; a blocking row only meets its request timeout */
         if
         :: res == R_WAIT && alive && !got_chunk && !blocking && clk >= FT -> res = R_REMOTE_ERR
         :: res == R_WAIT && alive && !got_chunk && blocking && clk >= PT -> res = R_REMOTE_ERR
         :: else -> skip
         fi
#endif
       }
  :: else -> break
  od
}

init {
  /* the environment's choices, made once */
  if :: blocking = true :: blocking = false fi;
  if :: hung = true :: hung = false fi;
  slow = 0;
  do
  :: slow < SLOWMAX -> slow++
  :: break
  od;
  /* the first heartbeat comes right after the headers */
  got_bytes = true;
  run World()
}
