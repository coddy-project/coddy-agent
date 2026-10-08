/*
 * p2-d2-usage-cache-skew.pml - the "reset_in_s" question of D2 (phase 2 plan, W10 and 4.1): must the usage projection carry a
 * RELATIVE reset time per window, or is the absolute `resets_at` of the remote enough?
 *
 * DISCRETE-TIME ABSTRACTION (not a timed automaton): g is the global tick (1 tick = 5 s), the remote's clock reads g, the
 * local clock reads g + sigma, sigma in [-S, S] fixed per run (clock skew between lender and borrower). A block lifts at the
 * global tick R (chosen per run). The remote caches its snapshot for TTL ticks (the manager's providerUsageTTL), the route
 * never forces a refresh. The local manager reads once at g = 0 (a surface opened), then ONLY the schedule the snapshot
 * implies reads again (usageNextReadMs: a read GRACE ticks after the window's reset, forced), and, when a read shows a reset
 * that has passed but the block still there, ONE follow-up FU ticks later (USAGE_FOLLOW_UP_MS, once per window). No other
 * trigger exists in this model: it isolates the quality of the schedule, which is all that makes a lift appear on an idle surface.
 *
 * VARIANTS (-D): REL (default) the wire carries reset_in_s = the remote's age-corrected remaining time; the client schedules
 *   from it, measured from the moment the answer arrives. ABS the wire carries only resets_at (the remote's clock); the client
 *   derives the remaining time against ITS OWN clock. RESETAWARE the remote treats a cached block whose reset has passed as
 *   expired (still under its floor): the first read after the reset is fresh.
 * Constants (ticks): TTL=4 (20 s), GRACE=1 (2 s rounded up), FU=6 (30 s), skew sigma in {-S,-1,0,1,S} (S=3: 15 s; S=12: 60 s), R in 2..8, a remote cache that is
 *   already 0..TTL-1 ticks old at g = 0.
 * PROPERTY: lag = ticks between the actual lift (g = R) and the first moment the surface shows "not blocked"; flag f_lag when it
 *   exceeds LB while the surface still shows the block (violated = the bound LB does not hold for some skew/R/cache age).
 *   Also derr = |remaining time the surface would show - true remaining time| at the delivery (the countdown error).
 * REPRODUCE: mcd check --promela p2-d2-usage-cache-skew.pml [-D ABS|-D RESETAWARE] -D LB=n -D T_LAG --bfs --no-timing
 */
#ifndef S
#define S 3
#endif
#ifndef TTL
#define TTL 4
#endif
#ifndef GRACE
#define GRACE 1
#endif
#ifndef FU
#define FU 6
#endif
#ifndef G
#define G 26
#endif
#ifndef LB
#define LB 8
#endif
#ifndef RMIN
#define RMIN 2
#endif
#ifndef RMAX
#define RMAX 8
#endif
#ifdef T_LAG
#define TRAP assert(false)
#else
#define TRAP skip
#endif

byte g = 0;
int sigma = 0;          /* local clock = g + sigma */
byte R = 0;             /* the true lift time, remote clock = global */
bool ready = false;
/* remote cache */
bool r_has = false; bool r_blk = false; int r_fetch = 0; byte r_pre = 0;   /* r_pre: age of the cache at g = 0 */
/* local */
bool disp_blk = false; bool shown_once = false;
bool timer_on = false; byte timer_at = 0; bool fu_used = false;
byte lag = 0, maxlag = 0, derr = 0, maxderr = 0;
bool f_lag = false;
/* temporaries */
int rin = 0; int rabs = 0; int delay = 0; bool ansb = false; int d2 = 0;

#define DUE (timer_on && timer_at <= g)

inline remote_get() {
  /* ans: ansb (blocked), rin (relative), rabs (absolute, remote clock) */
  if
  :: !r_has || (g - r_fetch >= TTL)
#ifdef RESETAWARE
     || (r_blk && R <= g)
#endif
     -> r_has = true; r_blk = (g < R); r_fetch = g
  :: else -> skip
  fi;
  ansb = r_blk; rabs = R;
  if :: R > g -> rin = R - g :: else -> rin = 0 fi
}

inline show(b) {
  disp_blk = b; shown_once = true;
}

inline local_read() {
  remote_get();
  show(ansb);
  d2 = 0;
  if
  :: ansb ->
#ifdef ABS
       delay = rabs - (g + sigma);
#else
       delay = rin;
#endif
       if :: delay < 0 -> delay = 0 :: else -> skip fi;
       /* the countdown the surface would show: remaining = delay; the true remaining is R - g (clamped) */
       if :: R > g -> d2 = R - g :: else -> d2 = 0 fi;
       if :: delay > d2 -> derr = delay - d2 :: else -> derr = d2 - delay fi;
       if :: derr > maxderr -> maxderr = derr :: else -> skip fi;
       if
       :: delay > 0 -> timer_on = true; timer_at = g + delay + GRACE
       :: else ->
            /* the snapshot says the reset has passed and the block still shows: one follow-up */
            if :: !fu_used -> fu_used = true; timer_on = true; timer_at = g + FU :: else -> timer_on = false fi
       fi
  :: else -> timer_on = false
  fi
}

proctype Local() {
  /* the first read, then only what the schedule implies */
  atomic { ready -> local_read(); delay = 0; rin = 0; rabs = 0; ansb = false; d2 = 0 };
  end: do
  :: atomic { DUE -> timer_on = false; local_read(); delay = 0; rin = 0; rabs = 0; ansb = false; d2 = 0 }
  od
}

proctype Clock() {
  end: do
  :: atomic { g < G && !DUE ->
       g = g + 1;
       if
       :: g >= R && shown_once && disp_blk ->
            lag = g - R;
            if :: lag > maxlag -> maxlag = lag :: else -> skip fi;
            if :: lag > LB -> f_lag = true; TRAP :: else -> skip fi
       :: else -> skip
       fi
     }
  :: atomic { g >= G -> skip }
  od
}

init {
  atomic {
    /* the environment's choices: skew, lift time, age of the remote cache at g = 0 */
    if
    :: sigma = 0 - S :: sigma = -1 :: sigma = 0 :: sigma = 1 :: sigma = S
    fi;
    if :: R = 2 :: R = 3 :: R = 4 :: R = 5 :: R = 6 :: R = 7 :: R = 8 fi;
    if
    :: r_pre = 0 :: r_pre = 1 :: r_pre = 2 :: r_pre = 3
    fi;
    /* a warm remote cache: fetched r_pre ticks before g = 0, saw the block (R > 0) */
    r_has = true; r_blk = true; r_fetch = 0 - r_pre;
    run Clock(); run Local();
    ready = true
  }
}
