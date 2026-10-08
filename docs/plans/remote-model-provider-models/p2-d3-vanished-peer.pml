/*
 * p2-d3-vanished-peer.pml - dilemma D3 of docs/plans/remote-model-provider-phase2.md (section 5, area C, and 7-D3):
 * which mechanism frees the slot of a vanished peer on each leg, at which scope, and how it composes with the
 * heartbeat, the body deadline and the kept-alive connection.  Engine: mcd (CLI), explicit-state, untimed.
 *
 * TIME is a DISCRETE CLOCK (declared abstraction, not a timed automaton): one tick = one atomic step of World,
 * `clk` counts ticks, every constant below is a number of ticks.  Real seconds are only an argument of scale
 * (production: H = 15 s, U = 30 s, R = 15 s, TP = 30 s, B = 45 s, RTO0 = 0.2..1 s).  Default = scale A
 * (H=2 U=4 R=2 TP=4 B=6 RTO0=1, ratios H : U = 1 : 2, R : TP = 1 : 2); scale B H=3 B=9 R=3; scale C is
 * tick = 1 s (H=15 B=45 R=15, RTO0 = 1 or 2, RTOCAP=64).  U defaults to B - H, TP to B - R, RETR2 to 3 B.
 *
 * WHAT IS MODELLED (one streamed call, one slot, one peer):
 *  - peer: alive (reads, acks, says nothing) or VANISHED (never acks again).  The vanish happens at the END of a
 *    tick (every write of that tick was delivered), once, at any tick <= TVMAX.  tv = that tick.  With RELAY=1
 *    the node's peer is the relay; VLOC=0 vanishes the relay, VLOC=1 the relay's client behind it.
 *  - network: at most ONE outage (all packets lost, both directions) of 1..XOUT ticks, starting at the end of any
 *    tick <= TVMAX.  The peer is alive during it.
 *  - kernel conn (index 0 = the node's socket to its peer, 1 = a second conn that carries a browser SSE, 2 = the
 *    relay's socket to its client): first unacked byte (age kage), retransmission schedule (first retransmit after
 *    RTO0 ticks, then the interval doubles, capped at RTOCAP: instants RTO0*(2^k-1)), a retransmit that finds the
 *    path up acks everything at once (zero delay), an abort check at every timer expiry: age >= kuto (the
 *    TCP_USER_TIMEOUT, or RETR2 when kuto = 0, the kernel's own limit).  CLAMP=1: the retransmit timer is clamped
 *    to the user timeout so the abort happens exactly at age == kuto (measured on Linux 7.0, see the report);
 *    CLAMP=0: the abort waits for the first retransmit instant >= kuto (an older kernel, [unverified which]).
 *  - handler: heartbeat written whenever the last write is H ticks old (the gap is exactly H, the worst case for
 *    the bound), a provider call that never ends (PROV=0) or may end OK / ERR early (PROV=1), the slot and the
 *    credential counter inuse (a second caller holds one), the call context, the deferred cleanup.
 *  - h2 health check (tunnel T1, or the application probe O4): ping after R ticks without a frame from the peer
 *    (T1: the timer's phase against the call is any of 0..R-1, the tunnel is older than the call),
 *    closing the connection TP ticks after the ping if no answer; the peer answers iff it is alive (and, for O4,
 *    cooperates: COOP); a ping stuck behind unacked data is answered when the data gets through.
 *  - connection reuse: after an OK / ERR end the connection stays alive and a request for any other route may
 *    come; the sockopt must be 0 then (leak latch).
 * ABSTRACTIONS: zero delay on the wire (an ACK is the same tick); one outage and one vanish per run; the peer's
 * app answers a ping in the tick it receives it; no RTT or SACK/TLP/RACK detail (retransmissions at the
 * exponential schedule only); a write that finds a backlog is appended without a new timer; detection and the
 * release of the slot happen in the same tick; the body deadline P and the pre-stream phase are outside (bounded
 * by P, plan 5.3); the h2 frames of a live quiet relay are none (the worst case for a ping).
 *
 * VARIANTS (-D NAME=v, defaults in the #ifndef blocks):
 *  LEG 0 direct listener (the accepted socket) / 1 tunnel (the node's dialled socket)
 *  DIRECT (LEG 0) 1 O1 per-call user timeout, 2 O2 listener-wide, 3 O3 TCP_INFO probe (POLL ticks), 4 O4 application
 *         probe (COOP, STRICT), 5 O5 nothing, 6 O6 small send buffer + write deadline (CAPB writes, WD ticks)
 *  T1, T2 (LEG 1) node-side h2 ping / user timeout on the dialled socket;  PROXY=1: a CONNECT proxy acks for the
 *         peer (the kernel never sees the vanish)
 *  RESTORE 1 deferred restore (plan), 0 none, 2 only after an OK end;  RELONCE 1 guarded release (plan), 0 raw;
 *  PB (> 0: the request body is read first and the body deadline P = PB ticks frees the slot; 0: no body phase),
 *  EXTRAREL 1 a second release path on a detector end;  CLAMP;  RELAY, VLOC, RELAYPROBE (the relay applies the
 *  per-call option to its client), U (default B - H, at least 1), R, TP (default B - R), XOUT, C2, PROV, TVMAX.
 *
 * PROPERTIES (latches, checked as LTL `[] (x == 0)`; sanity as CTL `EF`):
 *  late    the slot is still held at the end of tick tv + B (it must be freed by then)  (P1: bound)
 *  cutlive a detector ended the call although the peer had not vanished     (P2: no false positive; the outage
 *          is at most XOUT ticks)
 *  leak    the next request on the kept-alive connection sees a sockopt != 0 (P3: restore)
 *  badrel  the credential counter was released more than once               (P4: once)
 *  c2cut   a second conn (browser SSE, not a shared call) was cut by the option while its peer is alive (P5: scope)
 * REPRODUCE: see the .md file; e.g. mcd check --promela p2-d3-vanished-peer.pml --no-timing --ltl '[] (late == 0)'
 */
#ifndef LEG
#define LEG 0
#endif
#ifndef DIRECT
#define DIRECT 1
#endif
#ifndef T1
#define T1 1
#endif
#ifndef T2
#define T2 0
#endif
#ifndef PROXY
#define PROXY 0
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
#ifndef C2
#define C2 0
#endif
#ifndef PROV
#define PROV 0
#endif
#ifndef PEMAX
#define PEMAX 3
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
#ifndef RELAY
#define RELAY 0
#endif
#ifndef VLOC
#define VLOC 0
#endif
#ifndef RELAYPROBE
#define RELAYPROBE 0
#endif
#ifndef COOP
#define COOP 1
#endif
#ifndef STRICT
#define STRICT 0
#endif
#ifndef POLL
#define POLL 1
#endif
#ifndef CAPB
#define CAPB 2
#endif
#ifndef WD
#define WD 3
#endif
#ifndef PB
#define PB 0
#endif
#ifndef TVMAX
#define TVMAX (H + R + 2)
#endif
#define TMAX (TVMAX + B + 2)
#define NC 3

#define C_KABORT 1
#define C_PING 2
#define C_PROBE 3
#define C_WDL 4
#define C_PEER 5
#define C_OK 6
#define C_ERR 7
#define C_BODY 8

/* the h2 health check / application probe is active, and the peer answers it */
#define PGACT ((LEG == 1 && T1 == 1) || (LEG == 0 && DIRECT == 4 && (COOP == 1 || STRICT == 1)))
#define COOPER ((LEG == 1) || (COOP == 1))

byte clk = 0;
bit rel_v = 0, cli_v = 0, vanished = 0;
byte tv = 0;
byte out_st = 0, out_cnt = 0;
bit net = 1;
bit rc[NC];
bit kua[NC];
byte kage[NC], kint[NC], kat[NC], kuto[NC], knw[NC];
bit kdead[NC];
byte lack = 0, lim = 0;
bit deliv0 = 0, wrote_hb = 0;
/* handler */
byte phase = 0;            /* 0 slot held (body phase, then streaming), 1 returned */
bit pre = 0;               /* 1 while the request body is being read (PB > 0): the body deadline P is PB ticks */
byte hbage = 0;
byte inuse = 2;            /* the credential's counter: this call and one other caller */
bit rel_done = 0;
byte rel_cnt = 0;
byte cause = 0;
bit conn_alive = 1;
bit blocked = 0;
byte wage = 0;
byte c1age = 0;
bit relay_cancel = 0;
/* h2 / application probe */
byte fr_age = 0, ping_age = 0;
bit ping_out = 0, ping_q = 0;
byte nreq = 0;
/* latches */
bit late = 0, cutlive = 0, leak = 0, badrel = 0, c2cut = 0, fired = 0, freed = 0;

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
            :: rc[i] == 1 ->
                 if :: i == 0 -> deliv0 = 1; lack = clk :: else -> skip fi
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
            lim = RETR2;
            if :: kuto[i] > 0 -> lim = kuto[i] :: else -> skip fi;
            if
            :: kage[i] >= lim -> kdead[i] = 1; kua[i] = 0
            :: else ->
                 if
                 :: rc[i] == 1 ->
                      kua[i] = 0; knw[i] = 0;
                      if :: i == 0 -> deliv0 = 1; lack = clk :: else -> skip fi
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

/* the handler returns: the deferred cleanup runs (release, restore), the call ends for `cause` */
inline end_call() {
  phase = 1;
  release_slot();
  if :: (cause != C_OK && cause != C_ERR && EXTRAREL == 1) -> release_slot() :: else -> skip fi;
  if
  :: (DIRECT == 1 && LEG == 0) ->
       if
       :: RESTORE == 1 -> kuto[0] = 0
       :: (RESTORE == 2 && cause == C_OK) -> kuto[0] = 0
       :: else -> skip
       fi
  :: else -> skip
  fi;
  if
  :: (cause == C_OK || cause == C_ERR) -> skip
  :: (cause == C_BODY) -> conn_alive = 0
  :: else ->
       conn_alive = 0; fired = 1;
       if :: vanished == 0 -> cutlive = 1 :: else -> skip fi
  fi;
  if :: rel_cnt >= 1 -> freed = 1 :: else -> skip fi
}

/* the body is complete: the per-call option is set, the headers and the first heartbeat go out */
inline start_stream() {
  if :: (LEG == 0 && DIRECT == 1) -> kuto[0] = U :: else -> skip fi;
  hbage = 0; kwrite(0); wrote_hb = 1
}

inline handler() {
  wrote_hb = 0;
  if
  :: (phase == 0 && pre == 1) ->
       if
       :: clk >= PB -> cause = C_BODY; end_call()
       :: (clk < PB && vanished == 0) ->
            if
            :: pre = 0; start_stream()
            :: skip
            fi
       :: else -> skip
       fi
  :: (phase == 0 && pre == 0) ->
       if
       :: blocked == 1 ->
            wage = wage + 1;
            if :: kua[0] == 0 -> blocked = 0; kwrite(0); wrote_hb = 1 :: else -> skip fi
       :: else ->
            hbage = hbage + 1;
            if
            :: hbage >= H ->
                 hbage = 0;
                 if
                 :: (DIRECT == 6 && LEG == 0 && kua[0] == 1 && knw[0] >= CAPB) -> blocked = 1; wage = 0
                 :: else -> kwrite(0); wrote_hb = 1
                 fi
            :: else -> skip
            fi
       fi
  :: else -> skip
  fi
}

inline conn_c2() {
  if
  :: (C2 == 1 && kdead[1] == 0) ->
       c1age = c1age + 1;
       if :: c1age >= H -> c1age = 0; kwrite(1) :: else -> skip fi
  :: else -> skip
  fi;
  if :: kdead[1] == 1 -> c2cut = 1 :: else -> skip fi
}

inline relay_fwd() {
  if
  :: (RELAY == 1 && VLOC == 1 && phase == 0 && wrote_hb == 1) -> kwrite(2)
  :: else -> skip
  fi;
  if :: kdead[2] == 1 -> relay_cancel = 1 :: else -> skip fi
}

inline h2chk() {
  if
  :: (PGACT == 1 && phase == 0) ->
       if
       :: (ping_out == 1 && ping_q == 1 && deliv0 == 1) ->
            ping_q = 0;
            if :: (COOPER == 1 && rel_v == 0) -> ping_out = 0; fr_age = 0 :: else -> skip fi
       :: else -> skip
       fi;
       if
       :: ping_out == 1 -> ping_age = ping_age + 1
       :: else ->
            fr_age = fr_age + 1;
            if
            :: fr_age >= R ->
                 ping_out = 1; ping_age = 0;
                 kwrite(0);
                 if
                 :: (kdead[0] == 0 && kua[0] == 0) ->
                      if :: (COOPER == 1 && rel_v == 0) -> ping_out = 0; fr_age = 0 :: else -> skip fi
                 :: (kdead[0] == 0 && kua[0] == 1) -> ping_q = 1
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

inline detect() {
  if
  :: phase == 0 ->
       cause = 0;
       if
       :: kdead[0] == 1 -> cause = C_KABORT
       :: (PGACT == 1 && ping_out == 1 && ping_age >= TP) -> cause = C_PING
       :: (DIRECT == 3 && LEG == 0 && clk % POLL == 0 && kua[0] == 1 && clk - lack >= U) -> cause = C_PROBE
       :: (blocked == 1 && wage >= WD) -> cause = C_WDL
       :: (relay_cancel == 1 && rc[0] == 1) -> cause = C_PEER
       :: else -> skip
       fi;
       if :: cause > 0 -> end_call() :: else -> skip fi
  :: else -> skip
  fi
}

inline nextreq() {
  if
  :: (phase == 1 && conn_alive == 1 && nreq == 0 && DIRECT == 1 && LEG == 0) ->
       if
       :: nreq = 1; if :: kuto[0] != 0 -> leak = 1 :: else -> skip fi
       :: skip
       fi
  :: else -> skip
  fi
}

inline env_end() {
  if
  :: (vanished == 0 && clk <= TVMAX && VLOC == 0) -> rel_v = 1; vanished = 1; tv = clk
  :: (vanished == 0 && clk <= TVMAX && VLOC == 1 && RELAY == 1) -> cli_v = 1; vanished = 1; tv = clk
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
  clk = clk + 1; deliv0 = 0;
  rc[0] = (net == 1 && (PROXY == 1 || rel_v == 0));
  rc[1] = net;
  rc[2] = (net == 1 && cli_v == 0);
  ktick(0); ktick(1); ktick(2);
  handler();
  conn_c2();
  relay_fwd();
  h2chk();
  provider_end();
  detect();
  nextreq();
  env_end();
  latches()
}

active proctype World() {
  if :: (PB > 0) -> pre = 1 :: else -> skip fi;
  /* the tunnel exists before the call: the phase of its read-idle timer against the call is any value 0..R-1 */
  if
  :: (LEG == 1 && T1 == 1) -> do :: fr_age < R - 1 -> fr_age = fr_age + 1 :: break od
  :: else -> skip
  fi;
  if :: (LEG == 0 && DIRECT == 1 && PB == 0) -> kuto[0] = U :: else -> skip fi;
  if :: (LEG == 0 && DIRECT == 2) -> kuto[0] = U; kuto[1] = U :: else -> skip fi;
  if :: (LEG == 1 && T2 == 1) -> kuto[0] = U :: else -> skip fi;
  if :: (RELAY == 1 && RELAYPROBE == 1) -> kuto[2] = U :: else -> skip fi;
  do
  :: clk < TMAX -> atomic { tick() }
  :: clk >= TMAX -> break
  od
}
