/*
 * p2-d3b-h2-client.pml - dilemma O1 of docs/plans/remote-model-provider-phase2.md (sections 1 goal C, 5.2 "Through a relay",
 * 7.6 D3 residual risk): which mechanism frees the node's slot when the CLIENT of a TLS relay vanished and speaks HTTP/2 to the
 * relay (UserTimeoutFor refuses an h2 stream: one TCP connection carries many streams).  Follow-up of p2-d3-vanished-peer.pml,
 * whose kernel-connection abstraction is reused.  Engine: mcd (CLI), explicit-state, untimed.
 *
 * TIME is a DISCRETE CLOCK (declared abstraction, not a timed automaton): one tick = one atomic step of World, `clk` counts
 * ticks, every constant is a number of ticks.  Production seconds are an argument of scale only (H = 15, U = 30, R = 15, TP = 30,
 * B = 45, RTO0 = 0.2 .. 2, kernel retransmission limit about 925 s).  Default = scale A (H=2 B=6 U=4 R=2 TP=4 RTO0=1 RETR2=18);
 * scale B H=3 B=9 R=3; scale C tick = 1 s (H=15 B=45 R=15 TP=30, RTOCAP=64).
 *
 * WHAT IS MODELLED.  One shared call (slot, credential counter, heartbeat every H ticks, provider that never ends or ends
 * early), made by a CLIENT through a relay, and one BROWSER server-sent-event stream through the same relay.
 *  - client: alive (reads, acks, says nothing) or VANISHED (never acks again); the vanish happens at the END of a tick, once,
 *    at any tick <= TVMAX; tv = that tick.  The browser is always alive.
 *  - network: at most ONE outage (all packets lost, both directions, to both connections) of 1..XOUT ticks.
 *  - kernel connection of the relay (index 0 = its socket to the call's client, 1 = its socket to the browser): first unacked
 *    byte (age kage), retransmissions at RTO0*(2^k-1) (interval capped at RTOCAP), a retransmit that finds the path up acks
 *    everything, an abort at a timer expiry once age >= kuto (TCP_USER_TIMEOUT) or RETR2 (the kernel's own limit, kuto = 0);
 *    CLAMP=1 the timer is clamped to kuto (measured on Linux 7.0, p2-d3 report section 6).
 *  - relay: forwards every node heartbeat at once to conn 0 (FlushInterval -1), writes a keepalive to the browser every HS
 *    ticks; learns that a downstream connection is dead (kernel abort, or its own h2 health check closing it) and then, as
 *    httputil.ReverseProxy does (req.Context() reaches the upstream request, mount.go: no detached context), cancels the node's
 *    request in the same tick (PROP=1; PROP=0 is the mutant "the proxy does not cancel upstream").
 *  - node: slot + credential counter inuse (a second caller holds one), the call ends when the relay cancels (cause C_PEER),
 *    or by the provider (PROV=1: OK / ERR frame at any tick <= PEMAX), deferred release (guarded once, RELONCE) and restore.
 *  - h2 health check of the relay (option a): per h2 connection a ping after R ticks without a frame from the peer (the
 *    peer is silent: the worst case), the connection is closed TP ticks after the ping if the peer did not answer it; the
 *    peer answers iff alive; a ping stuck behind unacked data is answered when the data gets through (as T1 of p2-d3).
 * ABSTRACTIONS: zero delay on the wire; one call, one browser stream, one outage, one vanish per run; the relay->node leg is
 * healthy (its own failure is p2-d3); propagation of a cancel to the node takes zero ticks; no TLS, no flow control; the
 * browser answers an h2 ping in the tick it gets it; no acking intermediary (p2-d3 A10/A11); h1 connection count (six per
 * origin) is arithmetic in the report, not state.
 *
 * VARIANTS (-D NAME=v):
 *  MECH  0 nothing at the relay (options b, d: only the kernel, the propagation of PROP)
 *        1 (a) the relay's TLS listener gets an h2 health check (R, TP) on EVERY h2 connection (browser included)
 *        2 (c) the relay's listener is HTTP/1.x only: the client speaks h1, the relay's per-call option (U = B - H) applies
 *        3 (e) the local Coddy forces http/1.1 for `coddy` rows: h1 + per-call option; OTHER=1 is a client that is not the
 *              local Coddy and speaks h2 (nothing applies)
 *        4 (g) the relay holds a connection-scoped option on an h2 connection while a shared call is active on it
 *              (refcount; REFC=0 restores at the first end; OTHERCALL=1 a second call of the same client on the connection)
 *  RELOPT 1 the relay applies the per-call option on an h1 client connection (the p2-d3 decision), 0 it does not
 *  PROP   1 the relay cancels the node request when the downstream dies, 0 it does not
 *  RESTORE 1 deferred restore on every exit, 0 none, 2 after an OK end only;  RELONCE 1 guarded release, 0 raw;  EXTRAREL 1 a
 *  second release path;  SHARE 1 the browser stream rides the call's connection (conn 0);  SSE 1 the browser stream exists;
 *  CALL 0 no shared call (browser stream only);  U, H, B, R, TP, RTO0, RTOCAP, RETR2, CLAMP, XOUT, PROV, PEMAX, TVMAX, OEMAX, HS.
 *
 * PROPERTIES (latches, LTL `[] (x == 0)`; sanity as CTL `EF`):
 *  late    the slot is still held at the end of tick tv + B                (bound B after the vanish)
 *  cutlive a detector ended the call although the client had not vanished   (no cut of a live client)
 *  ssecut  the browser stream was cut although its peer is alive            (cost to a quiet SSE; SHARE=1: conn 0 cut)
 *  leak    the next request on the kept-alive connection sees a sockopt != 0
 *  badrel  the credential counter was released more than once
 * REPRODUCE: see the .md; e.g. mcd check --promela p2-d3b-h2-client.pml --no-timing --ltl '[] (late == 0)'
 */
#ifndef MECH
#define MECH 0
#endif
#ifndef OTHER
#define OTHER 0
#endif
#ifndef RELOPT
#define RELOPT 1
#endif
#ifndef PROP
#define PROP 1
#endif
#ifndef REFC
#define REFC 1
#endif
#ifndef OTHERCALL
#define OTHERCALL 0
#endif
#ifndef SSE
#define SSE 0
#endif
#ifndef SHARE
#define SHARE 0
#endif
#ifndef CALL
#define CALL 1
#endif
#ifndef H
#define H 2
#endif
#ifndef B
#define B 6
#endif
#ifndef R
#define R 2
#endif
#ifndef TP
#define TP (B - R)
#endif
#ifndef U
#if B - H >= 1
#define U (B - H)
#else
#define U 1
#endif
#endif
#ifndef RTO0
#define RTO0 1
#endif
#ifndef RTOCAP
#define RTOCAP 8
#endif
#ifndef RETR2
#define RETR2 (3 * B)
#endif
#ifndef CLAMP
#define CLAMP 1
#endif
#ifndef XOUT
#define XOUT 0
#endif
#ifndef PROV
#define PROV 0
#endif
#ifndef PEMAX
#define PEMAX 3
#endif
#ifndef OEMAX
#define OEMAX 3
#endif
#ifndef HS
#define HS H
#endif
#ifndef RESTORE
#define RESTORE 1
#endif
#ifndef RELONCE
#define RELONCE 1
#endif
#ifndef EXTRAREL
#define EXTRAREL 0
#endif
#ifndef TVMAX
#define TVMAX (H + R + 2)
#endif
#define TMAX (TVMAX + B + 2)
#define NC 2

#define C_PEER 5
#define C_OK 6
#define C_ERR 7

/* the client of the call speaks HTTP/1.x to the relay */
#define CP1 (MECH == 2 || (MECH == 3 && OTHER == 0))
/* the relay's per-call option on that connection / its connection-scoped option on an h2 connection */
#define OPTCALL (CP1 && RELOPT == 1)
#define OPTCONN (MECH == 4)

byte clk = 0;
bit cli_v = 0, vanished = 0;
byte tv = 0;
byte out_st = 0, out_cnt = 0;
bit net = 1;
bit rc[NC];
bit kua[NC];
byte kage[NC], kint[NC], kat[NC], kuto[NC], knw[NC];
bit kdead[NC];
bit deliv[NC];
/* node handler */
byte phase = 0;            /* 0 slot held, 1 returned */
byte hbage = 0;
byte inuse = 2;            /* the credential's counter: this call and one other caller */
bit rel_done = 0;
byte rel_cnt = 0;
byte cause = 0;
bit conn_alive = 1;
byte nact = 0;             /* shared calls active on the client's connection (MECH 4) */
bit oc_alive = 0;
byte c1age = 0;
bit dsdead = 0, relay_cancel = 0;
/* relay's h2 health check, per connection */
byte fr_age[NC], ping_age[NC];
bit ping_out[NC], ping_q[NC], hclosed[NC];
byte nreq = 0;
/* latches */
bit late = 0, cutlive = 0, leak = 0, badrel = 0, ssecut = 0, fired = 0, freed = 0;

inline release_slot() {
  if
  :: (RELONCE == 1 && rel_done == 1) -> skip
  :: else ->
       rel_done = 1; rel_cnt = rel_cnt + 1; inuse = inuse - 1;
       if :: (rel_cnt > 1 || inuse < 1) -> badrel = 1 :: else -> skip fi
  fi
}

inline kwrite(i) {
  if
  :: kdead[i] == 1 -> skip
  :: else ->
       if
       :: kua[i] == 1 -> knw[i] = knw[i] + 1
       :: else ->
            if
            :: rc[i] == 1 -> deliv[i] = 1
            :: else ->
                 kua[i] = 1; kage[i] = 0; kint[i] = RTO0; kat[i] = RTO0; knw[i] = 1;
                 if :: (CLAMP == 1 && kuto[i] > 0 && kat[i] > kuto[i]) -> kat[i] = kuto[i] :: else -> skip fi
            fi
       fi
  fi
}

inline ktick(i) {
  if
  :: (kua[i] == 1 && kdead[i] == 0) ->
       kage[i] = kage[i] + 1;
       if
       :: kage[i] >= kat[i] ->
            if
            :: (kuto[i] > 0 && kage[i] >= kuto[i]) -> kdead[i] = 1; kua[i] = 0
            :: (kuto[i] == 0 && kage[i] >= RETR2) -> kdead[i] = 1; kua[i] = 0
            :: else ->
                 if
                 :: rc[i] == 1 -> kua[i] = 0; knw[i] = 0; deliv[i] = 1
                 :: else ->
                      kint[i] = 2 * kint[i];
                      if :: kint[i] > RTOCAP -> kint[i] = RTOCAP :: else -> skip fi;
                      kat[i] = kage[i] + kint[i];
                      if :: (CLAMP == 1 && kuto[i] > 0 && kat[i] > kuto[i]) -> kat[i] = kuto[i] :: else -> skip fi
                 fi
            fi
       :: else -> skip
       fi
  :: else -> skip
  fi
}

/* the connection-scoped option: restore rule at the end of a shared call on the connection */
inline conn_restore() {
  nact = nact - 1;
  if
  :: (REFC == 1 && nact == 0) -> kuto[0] = 0
  :: REFC == 0 -> kuto[0] = 0
  :: else -> skip
  fi
}

/* the node handler returns: deferred cleanup (release, restore), the call ends for `cause` */
inline end_call() {
  phase = 1;
  release_slot();
  if :: (cause != C_OK && cause != C_ERR && EXTRAREL == 1) -> release_slot() :: else -> skip fi;
  if
  :: OPTCALL == 1 ->
       if
       :: RESTORE == 1 -> kuto[0] = 0
       :: (RESTORE == 2 && cause == C_OK) -> kuto[0] = 0
       :: else -> skip
       fi
  :: OPTCONN == 1 -> conn_restore()
  :: else -> skip
  fi;
  if
  :: (cause == C_OK || cause == C_ERR) -> skip
  :: else ->
       conn_alive = 0; fired = 1;
       if :: vanished == 0 -> cutlive = 1 :: else -> skip fi
  fi;
  if :: rel_cnt >= 1 -> freed = 1 :: else -> skip fi
}

inline handler() {
  if
  :: phase == 0 ->
       hbage = hbage + 1;
       if :: hbage >= H -> hbage = 0; kwrite(0) :: else -> skip fi
  :: else -> skip
  fi
}

/* the relay's keepalive to the browser's server-sent-event stream (conn 1) */
inline sse() {
  if
  :: SSE == 1 ->
       c1age = c1age + 1;
       if :: c1age >= HS -> c1age = 0; kwrite(1) :: else -> skip fi
  :: else -> skip
  fi
}

inline h2chk(i) {
  if
  :: (MECH == 1 && kdead[i] == 0 && ((i == 0 && CALL == 1 && phase == 0) || (i == 1 && SSE == 1))) ->
       if
       :: (ping_out[i] == 1 && ping_q[i] == 1 && deliv[i] == 1) ->
            ping_q[i] = 0;
            if :: (i == 1 || cli_v == 0) -> ping_out[i] = 0; fr_age[i] = 0 :: else -> skip fi
       :: else -> skip
       fi;
       if
       :: ping_out[i] == 1 ->
            ping_age[i] = ping_age[i] + 1;
            if :: ping_age[i] >= TP -> hclosed[i] = 1; kdead[i] = 1; kua[i] = 0 :: else -> skip fi
       :: else ->
            fr_age[i] = fr_age[i] + 1;
            if
            :: fr_age[i] >= R ->
                 ping_out[i] = 1; ping_age[i] = 0;
                 kwrite(i);
                 if
                 :: (kdead[i] == 0 && kua[i] == 0) ->
                      if :: (i == 1 || cli_v == 0) -> ping_out[i] = 0; fr_age[i] = 0 :: else -> skip fi
                 :: (kdead[i] == 0 && kua[i] == 1) -> ping_q[i] = 1
                 :: else -> skip
                 fi
            :: else -> skip
            fi
       fi
  :: else -> skip
  fi
}

inline provider_end() {
  if
  :: (PROV == 1 && phase == 0 && clk <= PEMAX) ->
       if
       :: cause = C_OK; end_call()
       :: cause = C_ERR; end_call()
       :: skip
       fi
  :: else -> skip
  fi
}

/* a second shared call of the same client on the same h2 connection ends (its provider finished) */
inline other_call_end() {
  if
  :: (OPTCONN == 1 && oc_alive == 1 && clk <= OEMAX) ->
       if
       :: oc_alive = 0; conn_restore()
       :: skip
       fi
  :: else -> skip
  fi
}

/* the relay learns that a downstream connection is dead and (PROP) cancels the node's request */
inline relay_learn() {
  if :: (kdead[0] == 1) -> dsdead = 1 :: else -> skip fi;
  if :: (dsdead == 1 && PROP == 1) -> relay_cancel = 1 :: else -> skip fi;
  if
  :: (SSE == 1 && SHARE == 0 && kdead[1] == 1) -> ssecut = 1
  :: (SSE == 1 && SHARE == 1 && kdead[0] == 1 && cli_v == 0) -> ssecut = 1
  :: else -> skip
  fi
}

inline detect() {
  if
  :: phase == 0 ->
       cause = 0;
       if :: relay_cancel == 1 -> cause = C_PEER :: else -> skip fi;
       if :: cause > 0 -> end_call() :: else -> skip fi
  :: else -> skip
  fi
}

inline nextreq() {
  if
  :: (phase == 1 && conn_alive == 1 && nreq == 0 && nact == 0 && (OPTCALL == 1 || OPTCONN == 1)) ->
       if
       :: nreq = 1; if :: kuto[0] != 0 -> leak = 1 :: else -> skip fi
       :: skip
       fi
  :: else -> skip
  fi
}

inline env_end() {
  if
  :: (vanished == 0 && CALL == 1 && clk <= TVMAX) -> cli_v = 1; vanished = 1; tv = clk
  :: skip
  fi;
  if
  :: out_st == 1 ->
       out_cnt = out_cnt + 1;
       if
       :: out_cnt >= XOUT -> out_st = 2; net = 1
       :: out_cnt < XOUT -> skip
       :: out_cnt < XOUT -> out_st = 2; net = 1
       fi
  :: (out_st == 0 && XOUT > 0 && clk <= TVMAX) ->
       if
       :: out_st = 1; out_cnt = 0; net = 0
       :: skip
       fi
  :: else -> skip
  fi
}

inline latches() {
  if :: (vanished == 1 && phase == 0 && clk >= tv + B) -> late = 1 :: else -> skip fi
}

inline tick() {
  clk = clk + 1; deliv[0] = 0; deliv[1] = 0;
  rc[0] = (net == 1 && cli_v == 0);
  rc[1] = net;
  ktick(0); ktick(1);
  handler();
  sse();
  h2chk(0); h2chk(1);
  provider_end();
  other_call_end();
  relay_learn();
  detect();
  nextreq();
  env_end();
  latches()
}

active proctype World() {
  if :: (CALL == 0) -> phase = 1; conn_alive = 0 :: else -> skip fi;
  /* the h2 connections exist before the call: the phase of the relay's read-idle timer against it is any value 0..R-1 */
  if
  :: (MECH == 1 && CALL == 1) -> do :: fr_age[0] < R - 1 -> fr_age[0] = fr_age[0] + 1 :: break od
  :: else -> skip
  fi;
  if
  :: (MECH == 1 && SSE == 1) -> do :: fr_age[1] < R - 1 -> fr_age[1] = fr_age[1] + 1 :: break od
  :: else -> skip
  fi;
  if
  :: (CALL == 1 && (OPTCALL == 1 || OPTCONN == 1)) -> kuto[0] = U
  :: else -> skip
  fi;
  if :: OPTCONN == 1 -> nact = 1 + OTHERCALL; oc_alive = OTHERCALL :: else -> skip fi;
  do
  :: clk < TMAX -> atomic { tick() }
  :: clk >= TMAX -> break
  od
}
