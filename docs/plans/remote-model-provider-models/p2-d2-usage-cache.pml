/*
 * p2-d2-usage-cache.pml - dilemma D2 of docs/plans/remote-model-provider-phase2.md (sections 4.1, 4.3, 4.4, 7-D2):
 * where the remote's account usage is cached and refreshed per alias on the LOCAL side, what it costs through a
 * relay, and what the allowlist projection tells a client about aliases that share one account.
 *
 * ONE MODEL, THREE OPTIONS (-D OPT=1|2|3) and a set of mutants:
 *   OPT=1  U1  one local entry per alias, each polling its own GET (the plan's default).
 *   OPT=2  U2  the answer carries an opaque account tag; the local entry is keyed by the tag, so aliases whose answers
 *              carry equal tags share one entry. -D GUARD -D RV=n adds the revalidation guard: an alias serves from the
 *              shared entry only while its OWN last answer is younger than RV ticks.
 *   OPT=3  U3  the answer carries a next-read hint (ticks until the remote's cache expires); the entry holds, no GET
 *              before the hold ends. -D CLAMP clamps the hint to TTL; -D LIAR makes the remote's hint arbitrary.
 *   -D FOLLOW  (OPT 1 or 2) after a turn-end read the manager arms ONE follow-up read TTL ticks later (no wire field).
 *   -D MANUAL  a manual refresh (trigger kind KM) bypasses the unsupported memory.
 *
 * DISCRETE-TIME ABSTRACTION (the engine checks untimed models only; this is NOT a timed automaton). One tick = 5 s.
 * The only time-like objects are small saturating counters advanced together by the process Clock; any number of
 * events may happen between two ticks (interleaving). Events that the real system makes due at a deadline are
 * URGENT: Clock refuses to tick while a deferred fetch (e_pendon && e_pend == 0) is due, and, with -D PTRIG=n, while an
 * alias has gone n ticks without a trigger (the assumption "every alias is read at least every n ticks").
 * Constants (real value -> ticks): providerUsageTTL 20 s -> TTL=4; providerUsageFloor 15 s -> FLOOR=3;
 * unsupported memory 300 s -> UNSM (default 6 = 30 s, scaled; the property is linear in UNSM and run at two values);
 * backoff cap 300 s -> BO (default 3). A GET (relay + node) is atomic: its latency is below one tick (declared).
 *
 * WHAT IS MODELLED (code facts, internal/session/provider_usage.go read in the worktree):
 *   remote   one cache entry PER ACCOUNT (= provider row), served by Manager.ProviderUsage(name, refresh=false):
 *            serve while age < TTL, else fetch upstream; a lender-own refresh (turn end on the lender, refresh=1) fetches
 *            at once when past the FLOOR. The route NEVER asks refresh=true (plan 4.1 step 4).
 *   local    triggers per alias: KA = auto read (session ready / SPA poll / refreshInSec cache read: TTL gated, inside the
 *            floor deferred), KF = forced read (REST refresh=1, SPA after a reset, deferred inside the floor),
 *            KT = turn end (publishProviderUsageAsync: forced, ignores the TTL, deferred inside the floor / backoff),
 *            KD = a deferred fire at the end of the floor/backoff/hold, KM = manual refresh (variant).
 *            Entry state as providerUsageEntry: value, fetchedAt (age), lastAttempt (att), backoffUntil (bo), stale,
 *            pending timer; unsupported memory per alias (a_uns).
 *   world    the account's true state flips (truth), the operator repoints an alias to another account (the remote's
 *            map), the lender enables its reader on an account (sup), the lender uses its own account (refresh), any
 *            number of OTHER clients read any alias at any time (stateless adversary on the remote's cache).
 *   failure  -D FAILS: a GET may fail (unavailable); the entry keeps its value marked stale, optionally with a backoff.
 * NOT MODELLED: unauthorized stickiness, config swaps and fingerprint changes, a remote upstream failure, GET latency,
 *   more than two accounts, a second local client (the other clients are the adversary above), clock skew (see
 *   p2-d2-usage-cache-skew.pml for reset_in_s).
 *
 * PROPERTIES (flags, each checked as  --ltl '[]!flag'  = safety; no fairness is needed anywhere):
 *   f_floor   P1  an upstream fetch of an account < FLOOR after the previous one (any number of clients and aliases)
 *   f_ttlc    P1b a client-driven upstream fetch < TTL after the previous one (the route never forces)
 *   f_gap     P2a a local GET on an entry < FLOOR after the previous attempt on it (per-entry cost bound)
 *   maxc      P2  node requests of the one local client inside a window of W ticks (-D COST), measured exactly
 *   f_wrong   P3  a non-stale snapshot shown for alias a carries another account than map[a], WB ticks after a repoint
 *   f_age     P4  a non-stale snapshot shown is older than AGEB ticks counted from the REMOTE's upstream fetch
 *   f_pt      P4b after a turn end, no snapshot built from an upstream fetch AFTER that turn is shown within PTK ticks
 *   f_hid     P5  a supported alias answered "unsupported" by the memory for more than UNSM ticks
 *   f_hidd    P5b ... on the SURFACE (display) for more than UNSM+PB ticks (needs -D PTRIG)
 *   sanity    seen_blocked, seen_lift, seen_uns, seen_cache, seen_def are reachable
 * OTHER -D SWITCHES: NL=1..3 aliases; OTHERS other clients read the remote at any time; LENDER the lender's own forced refresh;
 *   TRUTH/G_BLK the account's state flips and is carried in the snapshot; REPOINT / REPOINT0 the operator repoints every alias /
 *   alias 0; ENABLE the lender enables its reader on account 1 (alias 0 on account 1 is "unsupported" until then); SUP1 account
 *   1 supported from the start; RSTATELESS the remote answers the current account without a cache (property slices that do not
 *   look at the remote's cache); FAILS a GET may fail (stale, optional backoff BO); COST measures maxc over a window of W ticks;
 *   T_<FLAG> turns flag f_<flag> into assert(false); WB/AGEB/PTK/HIDB/PB the bounds of the properties; PTRIG=n the assumption
 *   "each alias is read at least every n ticks"; BUG_NOGATE, BUG_PERALIAS, BUG_ROUTE_REFRESH, BUG_NOLOCALFLOOR the mutants.
 * ENGINE NOTE: a violated LTL property of a model that uses atomic makes mcd 0.2.0 panic in the lasso renderer (cycle.go:998,
 *   stateOf); minimal reproducer in the report. Safety properties are therefore checked as assert (T_ switches) or CTL AG !flag.
 * REPRODUCE: see p2-d2-usage-cache.md, section "Commands".
 */
#ifndef OPT
#define OPT 1
#endif
#ifndef NL
#define NL 2
#endif
#define NA 2
#define NLM 3
#define NE 3
#define NR 3
#ifndef TTL
#define TTL 4
#endif
#ifndef FLOOR
#define FLOOR 3
#endif
#ifndef UNSM
#define UNSM 6
#endif
#ifndef BO
#define BO 3
#endif
#ifndef RV
#define RV 8
#endif
#ifndef PB
#define PB 1
#endif
#ifndef HIDB
#define HIDB UNSM
#endif
#ifndef PTK
#define PTK TTL
#endif
#ifndef WB
#define WB TTL
#endif
#ifndef HMAX
#define HMAX 12
#endif
#ifndef W
#define W 12
#endif
#ifndef AGEB
#if OPT == 3
#define AGEB (TTL - 1)
#else
#define AGEB (2 * (TTL - 1))
#endif
#endif

/* GHOST SLICES: a ghost counter multiplies the state space, so each property group is checked with only its own ghosts
 * (-D G_SRC age of the remote's fetch, G_REP repoint clock, G_PT turn-end clock [implies G_SRC], G_UNS hidden clocks,
 * G_BLK the value itself, -D TRUTH). A ghost that is off stays constant 0 and adds no state. */
#ifdef G_PT
#ifndef G_SRC
#define G_SRC
#endif
#endif

#ifndef SRCCAP
#define SRCCAP (AGEB + 2)
#endif
/* -D T_<NAME> turns the flag f_<name> into an assert(false) at the point it is set: the safety search then reports it
 * as the model's own 'assert' property (the engine's LTL lasso renderer panics on a violated LTL property of a model
 * that uses atomic, see the report; assert and CTL both work). */
#ifdef T_FLOOR
#define TRAP_FLOOR assert(false)
#else
#define TRAP_FLOOR skip
#endif
#ifdef T_TTLC
#define TRAP_TTLC assert(false)
#else
#define TRAP_TTLC skip
#endif
#ifdef T_GAP
#define TRAP_GAP assert(false)
#else
#define TRAP_GAP skip
#endif
#ifdef T_WRONG
#define TRAP_WRONG assert(false)
#else
#define TRAP_WRONG skip
#endif
#ifdef T_WRONGS
#define TRAP_WRONGS assert(false)
#else
#define TRAP_WRONGS skip
#endif
#ifdef T_AGE
#define TRAP_AGE assert(false)
#else
#define TRAP_AGE skip
#endif
#ifdef T_PT
#define TRAP_PT assert(false)
#else
#define TRAP_PT skip
#endif
#ifdef T_HID
#define TRAP_HID assert(false)
#else
#define TRAP_HID skip
#endif
#ifdef T_HIDD
#define TRAP_HIDD assert(false)
#else
#define TRAP_HIDD skip
#endif
#define NOWHO 2
#define NOTAG 7
#define KA 0
#define KF 1
#define KT 2
#define KD 3
#define KM 4

#define INC(x, cap) if :: x < cap -> x = x + 1 :: else -> skip fi
#define DEC(x) if :: x > 0 -> x = x - 1 :: else -> skip fi

/* ---- the world ---- */
bool truth[NA];            /* the account's true state: blocked or not */
bool sup[NA];              /* the lender's reader is enabled for the account */
byte map[NLM];             /* alias -> account (the remote's configuration; the operator can repoint) */
byte since_rep[NLM];       /* ghost: ticks since the alias was repointed, saturating at WB + 1 */

/* ---- the remote: one cache entry per account (per alias under BUG_PERALIAS) ---- */
bool r_has[NR]; bool r_blk[NR]; byte r_age[NR];   /* age since the upstream fetch, saturating at TTL */
bool up_has[NA]; byte up_age[NA];                 /* ghost: the account's own upstream history (for f_floor) */

/* ---- the local manager: entries (key = alias for OPT 1 and 3, key = tag for OPT 2) ---- */
bool e_has[NE]; byte e_who[NE]; bool e_blk[NE];
byte e_age[NE];            /* since the local fetchedAt, saturating at TTL */
byte e_src[NE];            /* ghost: ticks since the REMOTE's upstream fetch of the value, saturating at SRCCAP */
byte e_att[NE];            /* since the last attempt on the entry, saturating at FLOOR (FLOOR = "never" / free) */
byte e_bo[NE];             /* backoff remaining */
bool e_stale[NE];
bool e_pendon[NE]; byte e_pend[NE]; byte e_al[NE];   /* deferred fetch: armed, ticks left, the alias it asks for */
byte e_hold[NE];           /* OPT 3: ticks before the remote's cache can have changed */
byte a_uns[NLM];           /* the unsupported memory of the alias, ticks left */
byte atag[NLM];            /* OPT 2: the tag the alias learned from its last answer */
byte a_att[NLM];           /* per-alias last attempt (OPT 2: an alias without a tag has no entry to pace it) */
byte a_val[NLM];           /* ticks since the alias's own last successful answer, saturating at RV */
bool tflag[NLM];           /* a turn end whose read has not completed yet (arms the follow-up) */

/* ---- ghosts and flags ---- */
bool pt_open[NLM]; byte pt_age[NLM];
bool disp_uns[NLM]; byte hid[NLM]; byte sup_run[NLM]; byte since_trig[NLM];
bool seen_blk[NLM];
bool f_floor, f_ttlc, f_gap, f_wrong, f_wrongs, f_age, f_pt, f_hid, f_hidd;
bool seen_blocked, seen_lift, seen_uns, seen_cache, seen_def, seen_upc, seen_upl;
byte maxsrc;
byte gw[W]; byte winsum; byte maxc;

/* ---- temporaries: every atomic step zeroes them before it ends, so no state carries them ---- */
byte a, k, kd, gate, i, acc, rk, tgt, ans_who, ans_src, ans_hint, tmp;
bool ans_sup, ans_blk, cdv;

#ifdef PTRIG
#define NEEDTRIG ((since_trig[0] >= PTRIG) || (NL > 1 && since_trig[1] >= PTRIG) || (NL > 2 && since_trig[2] >= PTRIG))
#else
#define NEEDTRIG false
#endif
#define DUE ((e_pendon[0] && e_pend[0] == 0) || (e_pendon[1] && e_pend[1] == 0) || (e_pendon[2] && e_pend[2] == 0))

inline clr() {
  a = 0; k = 0; kd = 0; gate = 0; i = 0; acc = 0; rk = 0; tgt = 0; tmp = 0;
  ans_who = 0; ans_src = 0; ans_hint = 0; ans_sup = false; ans_blk = false; cdv = false
}

/* ================= the remote ================= */
inline upfetch(rkey, account, client) {
  if :: up_has[account] && up_age[account] < FLOOR -> f_floor = true; TRAP_FLOOR :: else -> skip fi;
  if :: client && up_has[account] && up_age[account] < TTL -> f_ttlc = true; TRAP_TTLC :: else -> skip fi;
  if :: client -> seen_upc = true :: else -> seen_upl = true fi;
  r_has[rkey] = true; r_age[rkey] = 0;
#ifdef G_BLK
  r_blk[rkey] = truth[account];
#endif
  up_has[account] = true; up_age[account] = 0
}

/* GET /coddy/llm/models/{alias}/usage: answers in ans_* ; client = driven by a client (not the lender) */
inline rget(al, client) {
  acc = map[al];
  if
  :: !sup[acc] -> ans_sup = false
  :: else ->
       ans_sup = true;
#ifdef BUG_PERALIAS
       rk = al;
#else
       rk = acc;
#endif
       if
#if defined(RSTATELESS)
       :: true -> skip
#elif defined(BUG_NOGATE)
       :: true -> upfetch(rk, acc, client)
#elif defined(BUG_ROUTE_REFRESH)
       :: (!r_has[rk] || r_age[rk] >= FLOOR) -> upfetch(rk, acc, client)
       :: else -> skip
#else
       :: (!r_has[rk] || r_age[rk] >= TTL) -> upfetch(rk, acc, client)
       :: else -> skip
#endif
       fi;
       ans_who = acc;
#ifdef G_BLK
       ans_blk = r_blk[rk];
#endif
#ifdef G_SRC
       ans_src = r_age[rk];
#endif
#ifdef RSTATELESS
       ans_hint = TTL;
#else
       ans_hint = TTL - r_age[rk];
#endif
#ifdef LIAR
       if :: ans_hint = 0 :: ans_hint = TTL :: ans_hint = HMAX fi
#endif
  fi
}

/* ================= the local side ================= */
inline deliver_uns(al) {
  disp_uns[al] = true; pt_open[al] = false; seen_uns = true
}

inline deliver(al, kk) {
  if
  :: e_has[kk] && e_who[kk] != NOWHO ->
       disp_uns[al] = false;
       if
       :: !e_stale[kk] ->
#ifdef G_REP
            if :: e_who[kk] != map[al] && since_rep[al] >= WB -> f_wrong = true; TRAP_WRONG :: else -> skip fi;
#endif
#ifdef G_SRC
            if :: e_src[kk] > AGEB -> f_age = true; TRAP_AGE :: else -> skip fi;
            if :: e_src[kk] > maxsrc -> maxsrc = e_src[kk] :: else -> skip fi;
#endif
#ifdef G_BLK
            if
            :: e_blk[kk] -> seen_blk[al] = true; seen_blocked = true
            :: else -> if :: seen_blk[al] -> seen_lift = true :: else -> skip fi
            fi;
#endif
#ifdef G_PT
            if :: pt_open[al] && e_who[kk] == map[al] && e_src[kk] <= pt_age[al] -> pt_open[al] = false :: else -> skip fi
#else
            skip
#endif
       :: else ->
            if :: e_who[kk] != map[al] -> f_wrongs = true; TRAP_WRONGS :: else -> skip fi
       fi
  :: else -> skip
  fi
}

/* the key of alias a into k (NOTAG: an OPT 2 alias that has no usable tag) */
inline getk(al) {
#if OPT == 2
  k = NOTAG;
#ifdef GUARD
  if :: atag[al] != NOTAG && a_val[al] < RV -> k = atag[al] :: else -> skip fi
#else
  if :: atag[al] != NOTAG -> k = atag[al] :: else -> skip fi
#endif
#else
  k = al
#endif
}

/* ticks before a GET on entry kk is permitted: floor, backoff, (OPT 3) hold */
inline gate_of(kk) {
  gate = 0;
#ifndef BUG_NOLOCALFLOOR
  if :: e_att[kk] < FLOOR -> gate = FLOOR - e_att[kk] :: else -> skip fi;
#endif
  if :: e_bo[kk] > gate -> gate = e_bo[kk] :: else -> skip fi;
#if OPT == 3
  if :: e_hold[kk] > gate -> gate = e_hold[kk] :: else -> skip fi
#endif
}

inline arm_pend(kk, al, d) {
  if
  :: !e_pendon[kk] -> e_pendon[kk] = true; e_pend[kk] = d; e_al[kk] = al; seen_def = true
  :: else -> skip
  fi
}

#ifdef COST
inline count_get() {
  gw[0] = gw[0] + 1; winsum = winsum + 1;
  if :: winsum > maxc -> maxc = winsum :: else -> skip fi
}
#else
inline count_get() { skip }
#endif

/* a GET failed: the entry keeps its value, marked stale (a first failure leaves an empty snapshot) */
inline failget(al, kk, bov) {
  if
  :: kk != NOTAG ->
       if :: !e_has[kk] -> e_has[kk] = true; e_who[kk] = NOWHO; e_blk[kk] = false; e_age[kk] = 0; e_src[kk] = SRCCAP :: else -> skip fi;
       e_att[kk] = 0; e_stale[kk] = true;
       if :: bov > e_bo[kk] -> e_bo[kk] = bov :: else -> skip fi;
       deliver(al, kk)
  :: else -> skip
  fi
}

/* the GET of alias al on behalf of entry kk (kk may be NOTAG) returned: build the entry */
inline okget(al, kk) {
  if
  :: !ans_sup ->
       a_uns[al] = UNSM;
#if OPT == 2
       atag[al] = NOTAG;
       if :: kk != NOTAG -> e_att[kk] = 0 :: else -> skip fi;
#else
       e_has[al] = false; e_who[al] = NOWHO; e_age[al] = TTL; e_src[al] = SRCCAP; e_att[al] = 0; e_stale[al] = false; e_bo[al] = 0; e_hold[al] = 0;
#endif
       deliver_uns(al)
  :: else ->
       a_val[al] = 0;
#if OPT == 2
       tgt = ans_who;
       atag[al] = tgt;
#else
       tgt = al;
#endif
       e_has[tgt] = true; e_who[tgt] = ans_who; e_age[tgt] = 0;
#ifdef G_BLK
       e_blk[tgt] = ans_blk;
#endif
#ifdef G_SRC
       e_src[tgt] = ans_src;
#endif
       e_att[tgt] = 0; e_bo[tgt] = 0; e_stale[tgt] = false; e_al[tgt] = al;
#if OPT == 3
       tmp = ans_hint;
#ifdef CLAMP
       if :: tmp > TTL -> tmp = TTL :: else -> skip fi;
#endif
       e_hold[tgt] = tmp;
#endif
       if
       :: tflag[al] ->
            tflag[al] = false;
#if OPT == 3
            if :: ans_hint < TTL -> arm_pend(tgt, al, e_hold[tgt]) :: else -> skip fi
#elif defined(FOLLOW)
            arm_pend(tgt, al, TTL)
#else
            skip
#endif
       :: else -> skip
       fi;
       deliver(al, tgt)
  fi
}

/* one GET for alias al, entry kk; the outcome is the environment's choice */
inline doget(al, kk) {
  if :: a_att[al] < FLOOR -> f_gap = true; TRAP_GAP :: else -> skip fi;
  a_att[al] = 0;
  count_get();
  rget(al, true);
  if
#ifdef FAILS
  :: true -> failget(al, kk, 0)
  :: true -> failget(al, kk, BO)
#endif
  :: true -> okget(al, kk)
  fi
}

/* one trigger of kind kd on alias al */
inline trig(al, kdv) {
#ifdef PTRIG
  since_trig[al] = 0;
#endif
  if
  :: kdv == KT ->
#ifdef G_PT
       pt_open[al] = true; pt_age[al] = 0;
#endif
       tflag[al] = true
  :: else -> skip
  fi;
  if
  :: a_uns[al] > 0 && kdv != KM ->
       /* the memory answers without a GET; the turn-end publishers stay silent for it */
       if :: kdv == KA || kdv == KF -> deliver_uns(al) :: else -> pt_open[al] = false fi
  :: else ->
       if :: kdv == KM -> a_uns[al] = 0 :: else -> skip fi;
       getk(al);
       if
       :: k == NOTAG ->
            if :: a_att[al] >= FLOOR -> doget(al, NOTAG) :: else -> skip fi
       :: else ->
            if
            :: else ->
                 gate_of(k);
                 if
                 :: e_bo[k] > 0 && kdv == KA -> deliver(al, k); seen_cache = true
                 :: else ->
                      if
#if OPT == 3
                      :: kdv == KA && e_hold[k] > 0 -> deliver(al, k); seen_cache = true
#else
                      :: kdv == KA && e_age[k] < TTL -> deliver(al, k); seen_cache = true
#endif
                      :: else ->
                           if
                           :: gate > 0 -> deliver(al, k); arm_pend(k, al, gate)
                           :: else -> doget(al, k)
                           fi
                      fi
                 fi
            fi
       fi
  fi
}

/* a deferred fetch of entry e fires */
inline fire(e) {
  e_pendon[e] = false; a = e_al[e];
  if
  :: a_uns[a] > 0 -> skip
  :: else ->
       gate_of(e);
       if
       :: gate > 0 -> arm_pend(e, a, gate)
       :: else -> doget(a, e)
       fi
  fi
}

inline tick() {
  i = 0;
  do :: i < NR -> INC(r_age[i], TTL); i = i + 1 :: else -> break od;
  i = 0;
  do :: i < NA -> INC(up_age[i], TTL); i = i + 1 :: else -> break od;
  i = 0;
  do
  :: i < NE ->
       INC(e_att[i], FLOOR);
       if
       :: e_has[i] -> INC(e_age[i], TTL); DEC(e_bo[i]); DEC(e_hold[i]);
#ifdef G_SRC
            INC(e_src[i], SRCCAP)
#else
            skip
#endif
       :: else -> skip
       fi;
       if :: e_pendon[i] && e_pend[i] > 0 -> e_pend[i] = e_pend[i] - 1 :: else -> skip fi;
       i = i + 1
  :: else -> break
  od;
  i = 0;
  do
  :: i < NL ->
       DEC(a_uns[i]); INC(a_att[i], FLOOR);
#ifdef GUARD
       INC(a_val[i], RV);
#endif
#ifdef G_REP
       INC(since_rep[i], WB + 1);
#endif
#ifdef PTRIG
       INC(since_trig[i], PTRIG);
#endif
#ifdef G_PT
       if :: pt_open[i] -> INC(pt_age[i], PTK + 1) :: else -> skip fi;
       if :: pt_open[i] && pt_age[i] > PTK -> f_pt = true; TRAP_PT :: else -> skip fi;
#endif
#ifdef G_UNS
       if :: disp_uns[i] && sup[map[i]] -> INC(hid[i], UNSM + PB + 2) :: else -> hid[i] = 0 fi;
       if :: hid[i] > UNSM + PB -> f_hidd = true; TRAP_HIDD :: else -> skip fi;
       if :: a_uns[i] > 0 && sup[map[i]] -> INC(sup_run[i], HIDB + 2) :: else -> sup_run[i] = 0 fi;
       if :: sup_run[i] > HIDB -> f_hid = true; TRAP_HID :: else -> skip fi;
#endif
       i = i + 1
  :: else -> break
  od;
#ifdef COST
  winsum = winsum - gw[W - 1];
  i = W - 1;
  do :: i > 0 -> gw[i] = gw[i - 1]; i = i - 1 :: else -> break od;
  gw[0] = 0;
#endif
  i = 0
}

/* ================= processes ================= */
proctype Clock() {
  end: do
  :: atomic { !DUE && !NEEDTRIG -> tick(); clr() }
  od
}

proctype Fire() {
  end: do
  :: atomic { (e_pendon[0] && e_pend[0] == 0) -> fire(0); clr() }
  :: atomic { (e_pendon[1] && e_pend[1] == 0) -> fire(1); clr() }
  :: atomic { (e_pendon[2] && e_pend[2] == 0) -> fire(2); clr() }
  od
}

/* the local manager's triggers: any alias, any kind, any time (the adversarial scheduler) */
proctype Trig() {
  end: do
  :: atomic {
       if :: a = 0
#if NL > 1
          :: a = 1
#endif
#if NL > 2
          :: a = 2
#endif
       fi;
       if
       :: kd = KA
       :: kd = KF
       :: kd = KT
#ifdef MANUAL
       :: kd = KM
#endif
       fi;
       trig(a, kd);
       clr()
     }
  od
}

/* the environment: the world, the lender, the other clients of the remote */
proctype World() {
  end: do
#ifdef TRUTH
  :: atomic { truth[0] = !truth[0] }
  :: atomic { truth[1] = !truth[1] }
#endif
#if defined(REPOINT) || defined(REPOINT0)
  :: atomic { map[0] != 1 -> map[0] = 1; hid[0] = 0; sup_run[0] = 0;
#ifdef G_REP
       since_rep[0] = 0
#else
       skip
#endif
     }
  :: atomic { map[0] != 0 -> map[0] = 0; hid[0] = 0; sup_run[0] = 0;
#ifdef G_REP
       since_rep[0] = 0
#else
       skip
#endif
     }
#endif
#ifdef REPOINT
#if NL > 1
  :: atomic { map[1] != 1 -> map[1] = 1;
#ifdef G_REP
       since_rep[1] = 0
#else
       skip
#endif
     }
  :: atomic { map[1] != 0 -> map[1] = 0;
#ifdef G_REP
       since_rep[1] = 0
#else
       skip
#endif
     }
#endif
#endif
#ifdef ENABLE
  :: atomic { !sup[1] -> sup[1] = true }
#endif
#ifdef LENDER
  :: atomic { sup[0] && (!r_has[0] || r_age[0] >= FLOOR) -> upfetch(0, 0, false) }
  :: atomic { sup[1] && (!r_has[1] || r_age[1] >= FLOOR) -> upfetch(1, 1, false) }
#endif
#ifdef OTHERS
  :: atomic { rget(0, true); clr() }
#if NL > 1
  :: atomic { rget(1, true); clr() }
#endif
#if NL > 2
  :: atomic { rget(2, true); clr() }
#endif
#endif
  :: atomic { skip }
  od
}

init {
  atomic {
    sup[0] = true;
#ifdef SUP1
    sup[1] = true;
#endif
    i = 0;
    do
    :: i < NE -> e_who[i] = NOWHO; e_age[i] = TTL; e_src[i] = SRCCAP; e_att[i] = FLOOR; i = i + 1
    :: else -> break
    od;
    i = 0;
    do :: i < NLM -> atag[i] = NOTAG; a_att[i] = FLOOR;
#ifdef GUARD
       a_val[i] = RV;
#endif
#ifdef G_REP
       since_rep[i] = WB + 1;
#endif
       i = i + 1 :: else -> break od;
    i = 0;
    do :: i < NR -> r_age[i] = TTL; i = i + 1 :: else -> break od;
    up_age[0] = TTL; up_age[1] = TTL;
#ifdef SPLIT
    map[2] = 1;
#endif
    i = 0;
    run Clock(); run Fire(); run Trig(); run World()
  }
}
