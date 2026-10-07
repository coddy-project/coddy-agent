/* m5-guards.pml - the two guards and the heartbeat of the `coddy` provider stream.

   Spec: docs/plans/remote-model-provider.md  4.2 ("Stall and liveness are two guards", heartbeat, per-write
   deadline, stateless handler), 4.3 (a relay cut is a transport failure), 5a rows Server/Client (stall,
   heartbeat, stream:false).  Code facts the model encodes (read, not edited):
     internal/llm/transport.go   idleBody: the client guard counts ANY body byte, is ARMED by the first
                                 body byte read (arm() only on n > 0), fires when idle >= I, EOF without a
                                 terminal frame is a truncation (openai_stream.go: transport failure).
     internal/llm/blocking.go    a stream:false row answers in one piece: silent for the whole generation,
                                 then a burst (chunks + final).  provider.go: StreamIdleTimeout is not
                                 applied to a blocking row.
     internal/agent/react.go     the agent's FIRST-TOKEN timer (90 s) is armed at call start when the LOCAL
                                 row streams, stopped by the first chunk (a heartbeat is not a chunk).
     internal/llm/resilient.go   remote retry ladder (RetryMaxDelay 60 s per sleep): silent before output.

   CLASS: finite, untimed Promela.  TIME IS A DISCRETE-CLOCK ABSTRACTION (not a timed automaton): one `tick`
   is one atomic step of process World, every counter below counts ticks.  All timers are conservative at
   ties (an expiry at tick t wins over a byte arriving at tick t, so "gap == limit" counts as a fire).
   Transport delay and jitter are NOT modelled except as the heartbeat slack HJIT (the wire gap between two
   heartbeats is anywhere in [HPER, HPER+HJIT]); a real delay variation J is added to HPER+HJIT by hand.

   PARAMETERS (-D NAME=value, defaults are "scale A"; the mapping to the plan's seconds is in the report):
     HPER   H, heartbeat period                    CIDLE  I, client idle guard (0 = off)
     HJIT   heartbeat slack (jitter)               SSTALL S, remote model-stall guard
     DBACK  D, longest silent upstream phase       GGEN   G, stream:false generation time (silent, then burst)
     GMAXGAP longest gap between two tokens        NTOK   tokens of a streamed answer
     PPRE   longest pre-stream phase (slot held, body read, no byte sent)
     WDL    W, per-write deadline                  CAPB   send buffer in units (heartbeat = 1, chunk = CWT)
     FTMO   F, agent first-token timer (0 = local row is stream:false, timer not armed)
     PXL    L, idle limit of a proxy hop in the path (0 = none; nginx proxy_read_timeout, Cloudflare)
     PINGT  REMEDY under test: the server probes a silent peer (HTTP/2 PING with ReadIdleTimeout+PingTimeout, or
            TCP_USER_TIMEOUT) and drops it PINGT ticks after the peer vanished (0 = the spec as written)
     TMAX   horizon: a call alive at TMAX is reported as a hang, a slot held at TMAX as stuck
   SWITCHES:
     HB=1/0       heartbeat comments on / off (NOHB)
     FRAMEONLY=1  the client guard counts only event frames (the rejected idea)
     ARM=0/1/2    the guard is armed by the first counted byte (real code) / by the response headers / at
                  the request start
     SF=1         the remote row is stream:false          SARM=1  the remote stall guard is also armed
                  before the first upstream byte (spec statement "a stall before the first chunk")
     CRASHF CUTF FREEZEF MSTALLF USTALLF   enable a fault: remote crash (silent or with EOF), relay or proxy
                  cut (silent or with EOF), reader that stops reading, model stall mid-stream, upstream that
                  never answers
     LATEFIRST=1  MUTATION: the first heartbeat is sent HPER ticks after the headers instead of right after them
     BADEMIT=1    MUTATION: `emitted` is lost between the error frame and the client's retry decision (a server that
                  sends emitted=false, or a client that maps cause=stall to a bare streamStalledError, which has no
                  emitted field): anti-vacuity of the no-repeat rule
     SB           bound the slot-release property is checked against (default WDL, the naive reading)
     DB           bound of the dead-remote detection property (default CIDLE)

   PROPERTIES (all `--ctl 'AG (latch == 0)'`; `--ltl '[] (latch == 0)'` gives the same verdicts where cross-checked):
     fk_idle     P1  the client guard never fires while remote and path are healthy
     fk_proxy    P1b a proxy hop with idle limit PXL never fires while the remote is healthy
     fk_ft       P1c the agent's first-token timer never fires on a healthy remote
     undet / undet_armed / undet_hdr   P2  a silent crash or cut is detected within DB ticks (fault tick = 1)
     det_below / det_lb (EF)           P2  the earliest detection is I-H-HJIT+1 ticks and it is attained
     stall_lost / stall_slow / hang / slotstuck   P3  a model stall ends as error{upstream, stall}
     bad_emitted P4  the error frame after output says emitted=true
     wover / leak_stream                P5  a blocked write dies within WDL; the slot is released within SB
     deliv_after_cut                    P7  nothing is delivered after a cut
   Reachability sanity is asked with `--ctl 'EF (...)'` on rp, cl, seen, blocked, hb_up, hang.
*/

#ifndef HPER
#define HPER 2
#endif
#ifndef HJIT
#define HJIT 0
#endif
#ifndef CIDLE
#define CIDLE 5
#endif
#ifndef SSTALL
#define SSTALL 6
#endif
#ifndef DBACK
#define DBACK 6
#endif
#ifndef GGEN
#define GGEN 7
#endif
#ifndef GMAXGAP
#define GMAXGAP 2
#endif
#ifndef NTOK
#define NTOK 2
#endif
#ifndef PPRE
#define PPRE 3
#endif
#ifndef WDL
#define WDL 3
#endif
#ifndef CAPB
#define CAPB 2
#endif
#ifndef CWT
#define CWT 1
#endif
#ifndef FTMO
#define FTMO 0
#endif
#ifndef PXL
#define PXL 0
#endif
#ifndef PINGT
#define PINGT 0
#endif
#ifndef TMAX
#define TMAX 34
#endif
#ifndef HB
#define HB 1
#endif
#ifndef FRAMEONLY
#define FRAMEONLY 0
#endif
#ifndef ARM
#define ARM 0
#endif
#ifndef SF
#define SF 0
#endif
#ifndef SARM
#define SARM 0
#endif
#ifndef CRASHF
#define CRASHF 1
#endif
#ifndef CUTF
#define CUTF 1
#endif
#ifndef FREEZEF
#define FREEZEF 1
#endif
#ifndef MSTALLF
#define MSTALLF 1
#endif
#ifndef USTALLF
#define USTALLF 1
#endif
#ifndef LATEFIRST
#define LATEFIRST 0
#endif
#ifndef BADEMIT
#define BADEMIT 0
#endif
#ifndef SB
#define SB WDL
#endif
#ifndef DB
#define DB CIDLE
#endif

/* remote phases */
#define R_PRE   0   /* slot held, body being read and validated, nothing sent yet (client sees no byte) */
#define R_UP    1   /* headers + first heartbeat sent; upstream call, silent backoff periods              */
#define R_GEN   2   /* streamed generation: tokens with gaps                                              */
#define R_BLK   3   /* stream:false row: silent for the whole generation, then a burst                    */
#define R_DONE  4   /* terminal frame written                                                             */
#define R_ABORT 5   /* handler cancelled (client gone, write deadline)                                    */
#define R_DEAD  6   /* process crashed                                                                    */

/* client outcomes */
#define CL_WAIT  0
#define CL_OK    1   /* final received                                                                    */
#define CL_ESTALL 2  /* error{upstream, cause stall} received                                             */
#define CL_TRUNC 3   /* EOF / connection closed without a terminal frame: transport failure              */
#define CL_IDLE  4   /* the client byte-level guard fired (streamStalledError, retried if nothing emitted) */
#define CL_FT    5   /* the agent first-token timer fired ("model did not respond")                       */

#define T_FINAL 1
#define T_STALL 2

byte rp = R_PRE;
byte pa = 0;            /* ticks in the current phase (PRE, UP, BLK) or since the last token (GEN)          */
byte tok = 0;
byte hb_age = 0;
bit  hdr = 0;
bit  crashed = 0;  bit crash_silent = 0;
bit  cutp = 0;     bit cut_silent = 0;
bit  frozen = 0;
bit  mstall = 0;   bit ustall = 0;
bit  slot = 1;
byte t = 0;
bit  eof = 0;

byte cl = CL_WAIT;
bit  armed = 0;
byte idle = 0;
byte seen = 0;          /* chunks that reached the caller */
bit  err_emitted = 0;

byte wbuf = 0;
bit  blocked = 0;
byte wblk = 0;
bit  unreach = 0;       /* cut silently or reader frozen: nothing drains the send buffer */
byte stall_age = 0;
bit  fault = 0;         /* remote crashed silently or path cut silently: no trace for the client */
byte fault_age = 0;

byte pidle = 0;  bit proxy_cut = 0;
bit  fin = 0;

byte sent_hb = 0; byte sent_chunk = 0; byte sent_term = 0; byte sent_hdr = 0; byte term_kind = 0;
bit  was_armed = 0;

/* latches: a property is "AG (latch == 0)" */
bit fk_idle = 0;      /* client guard fired while remote and path were healthy (false kill)                */
bit fk_ft = 0;        /* first-token timer fired while remote and path were healthy                        */
bit fk_proxy = 0;     /* proxy idle limit fired while remote and path were healthy                         */
bit undet = 0;        /* silent fault older than DB ticks and the client still waits                       */
bit stall_lost = 0;   /* a model stall with a healthy remote did not end as error{upstream, stall}         */
bit bad_emitted = 0;  /* error frame after output says emitted=false: the client would repeat the call     */
bit leak = 0;         /* slot still held SB ticks after the peer became unreachable                        */
bit wover = 0;        /* a blocked write lived longer than WDL                                             */
bit hang = 0;         /* no terminal outcome at the horizon (client alive)                                 */
bit slotstuck = 0;    /* slot still held at the horizon                                                    */
bit hb_up = 0;        /* a heartbeat was sent while the remote was in UP (sanity)                          */
bit hdr0 = 0;         /* hdr at the start of the tick: did the client already have the headers                */
bit fh = 0;           /* the silent fault happened after the headers were sent                             */
bit undet_hdr = 0;    /* undet, for a fault after the headers (the window the first heartbeat must cover)  */
bit af = 0;           /* the client guard was armed when the silent fault happened                         */
bit undet_armed = 0;  /* undet, for a fault that happened after the guard was armed                        */
bit stall_slow = 0;   /* a model stall of a streamed row, healthy remote, older than SSTALL and unanswered  */
bit deliv_after_cut = 0; /* a terminal frame reached the client although the path had been cut              */
bit det_below = 0;    /* the guard fired earlier than I-H-HJIT+1 ticks after a silent fault (lower bound)    */
bit det_lb = 0;       /* the guard fired exactly at that lower bound (it is attained)                      */
bit uh = 0;           /* the peer vanished after the headers (the stream was already open)                 */
bit leak_stream = 0;  /* leak, for a peer that vanished after the headers                                  */
byte det_d = 0;       /* ticks from the silent fault to the cut by a client guard (inclusive count)          */
byte ms_age = 0;      /* ticks since the model (or the upstream) went silent for good                       */

inline stall_error() {
  sent_term = 1; term_kind = T_STALL;
#if BADEMIT
  err_emitted = 0;
#else
  if
  :: tok > 0 -> err_emitted = 1
  :: else -> err_emitted = 0
  fi;
#endif
  rp = R_DONE
}

inline enter_up() {
  rp = R_UP; pa = 0; hdr = 1; hb_age = 0; sent_hdr = 1;
  if
  :: (HB == 1 && LATEFIRST == 0) -> sent_hb = 1
  :: else -> skip
  fi;
  if
  :: USTALLF == 1 -> if :: ustall = 1 :: skip fi
  :: else -> skip
  fi
}

inline leave_up() {
  pa = 0;
  if
  :: SF == 1 -> rp = R_BLK
  :: else -> rp = R_GEN
  fi
}

inline emit_token() {
  sent_chunk = 1; tok = tok + 1; pa = 0;
  if
  :: tok >= NTOK -> sent_term = 1; term_kind = T_FINAL; rp = R_DONE
  :: else -> skip
  fi
}

inline burst() {
  sent_chunk = 1; tok = 1; sent_term = 1; term_kind = T_FINAL; rp = R_DONE
}

inline remote_step() {
  if
  :: rp == R_PRE ->
       if
       :: pa + 1 >= PPRE -> enter_up()
       :: pa + 1 < PPRE -> if :: pa = pa + 1 :: enter_up() fi
       fi
  :: rp == R_UP ->
       if
       :: ustall == 1 ->
            pa = pa + 1;
            if
            :: (SARM == 1 && pa >= SSTALL) -> stall_error()
            :: else -> skip
            fi
       :: else ->
            if
            :: pa + 1 >= DBACK -> leave_up()
            :: pa + 1 < DBACK -> if :: pa = pa + 1 :: leave_up() fi
            fi
       fi
  :: rp == R_GEN ->
       if
       :: (MSTALLF == 1 && mstall == 0 && tok < NTOK) -> if :: mstall = 1 :: skip fi
       :: else -> skip
       fi;
       if
       :: mstall == 1 ->
            pa = pa + 1;
            if
            :: pa >= SSTALL -> stall_error()
            :: else -> skip
            fi
       :: else ->
            if
            :: pa + 1 >= GMAXGAP -> emit_token()
            :: pa + 1 < GMAXGAP -> if :: pa = pa + 1 :: emit_token() fi
            fi
       fi
  :: rp == R_BLK ->
       if
       :: (MSTALLF == 1 && mstall == 0) -> if :: mstall = 1 :: skip fi
       :: else -> skip
       fi;
       if
       :: mstall == 1 -> pa = pa + 1
       :: else ->
            if
            :: pa + 1 >= GGEN -> burst()
            :: pa + 1 < GGEN -> if :: pa = pa + 1 :: burst() fi
            fi
       fi
  fi
}

inline hb_step() {
  if
  :: hb_age + 1 >= HPER + HJIT -> sent_hb = 1; hb_age = 0
  :: (hb_age + 1 >= HPER && hb_age + 1 < HPER + HJIT) ->
       if :: sent_hb = 1; hb_age = 0 :: hb_age = hb_age + 1 fi
  :: hb_age + 1 < HPER -> hb_age = hb_age + 1
  fi
}

inline client_step() {
  was_armed = armed;
  if
  :: eof == 1 -> cl = CL_TRUNC
  :: else ->
       if
       :: (armed == 1 && idle + 1 >= CIDLE && CIDLE > 0) ->
            cl = CL_IDLE;
            if :: fault == 1 -> det_d = fault_age + 1 :: else -> skip fi;
            if :: (crashed == 0 && cutp == 0) -> fk_idle = 1 :: else -> skip fi;
            if
            :: (fault == 1 && fault_age + HPER + HJIT < CIDLE) -> det_below = 1
            :: (fault == 1 && fault_age + HPER + HJIT == CIDLE) -> det_lb = 1
            :: else -> skip
            fi
       :: (FTMO > 0 && seen == 0 && t >= FTMO) ->
            cl = CL_FT;
            if :: fault == 1 -> det_d = fault_age + 1 :: else -> skip fi;
            if :: (crashed == 0 && cutp == 0) -> fk_ft = 1 :: else -> skip fi
       :: else ->
            if
            :: (unreach == 0 && cutp == 0) ->
                 if
                 :: (sent_hdr == 1 && ARM == 1) -> armed = 1; idle = 0
                 :: else -> skip
                 fi;
                 if
                 :: (sent_chunk == 1 || sent_term == 1 || (sent_hb == 1 && FRAMEONLY == 0)) ->
                      armed = 1; idle = 0
                 :: else ->
                      if :: was_armed == 1 -> idle = idle + 1 :: else -> skip fi
                 fi;
                 if :: sent_chunk == 1 -> seen = seen + 1 :: else -> skip fi;
                 if
                 :: sent_term == 1 ->
                      if
                      :: term_kind == T_FINAL -> cl = CL_OK
                      :: else -> cl = CL_ESTALL
                      fi
                 :: else -> skip
                 fi
            :: else ->
                 if :: was_armed == 1 -> idle = idle + 1 :: else -> skip fi
            fi
       fi
  fi;
  if
  :: (cl == CL_ESTALL && err_emitted == 0 && seen > 0) -> bad_emitted = 1
  :: else -> skip
  fi
}

inline tick() {
  t = t + 1; hdr0 = hdr;
  sent_hb = 0; sent_chunk = 0; sent_term = 0; sent_hdr = 0; term_kind = 0;

  /* a blocked write ages; its deadline cuts it */
  if
  :: blocked == 1 ->
       wblk = wblk + 1;
       if
       :: wblk >= WDL ->
            blocked = 0; slot = 0;
            if :: rp != R_DONE -> rp = R_ABORT :: else -> skip fi
       :: else -> skip
       fi
  :: else -> skip
  fi;

  /* faults, at most once each */
  if
  :: (CRASHF == 1 && crashed == 0 && cl == CL_WAIT && rp <= R_BLK) ->
       crashed = 1; slot = 0; rp = R_DEAD; blocked = 0;
       if :: crash_silent = 1 :: crash_silent = 0; eof = 1 fi
  :: (CUTF == 1 && cutp == 0 && crashed == 0 && cl == CL_WAIT && rp <= R_BLK) ->
       cutp = 1;
       if :: cut_silent = 1 :: cut_silent = 0; eof = 1; slot = 0; rp = R_ABORT; blocked = 0 fi
  :: (FREEZEF == 1 && frozen == 0 && cl == CL_WAIT) -> frozen = 1
  :: skip
  fi;
  if
  :: (((cutp == 1 && cut_silent == 1) || frozen == 1) && unreach == 0) -> unreach = 1; uh = hdr0
  :: else -> skip
  fi;

  /* remote */
  if
  :: (rp <= R_BLK && blocked == 0) -> remote_step()
  :: else -> skip
  fi;
  if
  :: (HB == 1 && hdr == 1 && sent_hdr == 0 && blocked == 0 && (rp == R_UP || rp == R_GEN || rp == R_BLK)) ->
       hb_step()
  :: else -> skip
  fi;
  if
  :: (sent_hb == 1 && rp == R_UP) -> hb_up = 1
  :: else -> skip
  fi;

  /* writer: bytes written while nothing drains the buffer pile up; a full buffer blocks the write */
  if
  :: (unreach == 1 && (sent_hb == 1 || sent_chunk == 1 || sent_term == 1)) ->
       wbuf = wbuf + sent_hb + sent_chunk * CWT + sent_term;
       if
       :: (wbuf > CAPB && blocked == 0) -> blocked = 1; wblk = 0
       :: else -> skip
       fi
  :: else -> skip
  fi;
  if
  :: (rp == R_DONE && blocked == 0 && slot == 1) -> slot = 0
  :: else -> skip
  fi;

  /* remedy under test: a server-side liveness probe that detects a vanished peer */
  if
  :: (PINGT > 0 && unreach == 1 && stall_age + 1 >= PINGT && slot == 1 && rp <= R_DONE) ->
       blocked = 0; slot = 0; if :: rp != R_DONE -> rp = R_ABORT :: else -> skip fi
  :: else -> skip
  fi;

  /* a proxy hop with an idle limit, counting every byte it relays from the remote */
  if
  :: (PXL > 0 && proxy_cut == 0 && cl == CL_WAIT && rp != R_DEAD) ->
       if
       :: pidle + 1 >= PXL ->
            proxy_cut = 1; eof = 1;
            if :: (crashed == 0 && cutp == 0 && rp <= R_BLK) -> fk_proxy = 1 :: else -> skip fi;
            if :: rp <= R_BLK -> rp = R_ABORT; slot = 0; blocked = 0 :: else -> skip fi
       :: else ->
            if
            :: (sent_hb == 1 || sent_chunk == 1 || sent_term == 1 || sent_hdr == 1) -> pidle = 0
            :: else -> pidle = pidle + 1
            fi
       fi
  :: else -> skip
  fi;

  /* client */
  if
  :: (cl == CL_WAIT && frozen == 0) ->
       client_step();
       /* the client closed the connection: a reachable remote notices at once */
       if
       :: (cl != CL_WAIT && unreach == 0 && cutp == 0 && rp <= R_BLK) -> rp = R_ABORT; slot = 0; blocked = 0
       :: else -> skip
       fi
  :: else -> skip
  fi;

  /* bookkeeping and latches */
  if
  :: (((crashed == 1 && crash_silent == 1) || (cutp == 1 && cut_silent == 1)) && fault == 0) -> fault = 1; af = armed; fh = hdr0
  :: else -> skip
  fi;
  if :: fault == 1 -> fault_age = fault_age + 1 :: else -> skip fi;   /* inclusive: the tick of the fault counts as 1 */
  if
  :: (cl == CL_WAIT && frozen == 0 && fault == 1 && fault_age > DB) -> undet = 1
  :: else -> skip
  fi;
  if
  :: (cl == CL_WAIT && frozen == 0 && fault == 1 && af == 1 && fault_age > DB) -> undet_armed = 1
  :: else -> skip
  fi;
  if
  :: (cl == CL_WAIT && frozen == 0 && fault == 1 && fh == 1 && fault_age > DB) -> undet_hdr = 1
  :: else -> skip
  fi;
  if
  :: det_d > DB -> undet = 1; if :: af == 1 -> undet_armed = 1 :: else -> skip fi; if :: fh == 1 -> undet_hdr = 1 :: else -> skip fi   /* detected, but late */
  :: else -> skip
  fi;
  if
  :: ((cl == CL_OK || cl == CL_ESTALL) && cutp == 1) -> deliv_after_cut = 1
  :: else -> skip
  fi;
  if
  :: (cl == CL_WAIT && SF == 0 && (mstall == 1 || ustall == 1) && crashed == 0 && cutp == 0 && frozen == 0
      && proxy_cut == 0 && ms_age > SSTALL) -> stall_slow = 1
  :: else -> skip
  fi;
  if :: (mstall == 1 || ustall == 1) -> ms_age = ms_age + 1 :: else -> skip fi;
  if
  :: (slot == 1 && unreach == 1 && stall_age > SB) -> leak = 1; if :: uh == 1 -> leak_stream = 1 :: else -> skip fi
  :: else -> skip
  fi;
  if :: unreach == 1 -> stall_age = stall_age + 1 :: else -> skip fi;
  if :: (blocked == 1 && wblk > WDL) -> wover = 1 :: else -> skip fi;

  if
  :: (slot == 0 && (cl != CL_WAIT || frozen == 1)) -> fin = 1
  :: t >= TMAX -> fin = 1
  :: else -> skip
  fi;
  if
  :: fin == 1 ->
       if :: (cl == CL_WAIT && frozen == 0) -> hang = 1 :: else -> skip fi;
       if :: slot == 1 -> slotstuck = 1 :: else -> skip fi;
       if
       :: ((mstall == 1 || ustall == 1) && SF == 0 && crashed == 0 && cutp == 0 && frozen == 0
           && proxy_cut == 0 && cl != CL_ESTALL) -> stall_lost = 1
       :: else -> skip
       fi
  :: else -> skip
  fi
}

active proctype World() {
  if
  :: ARM == 2 -> armed = 1
  :: else -> skip
  fi;
  do
  :: fin == 0 -> atomic { tick() }
  :: fin == 1 -> break
  od
}
