/*
 * m4-auth-relay-relay.pml  -  MODEL 2 of 2: the relay's use of a node's token (registry + mount + sessions fan-out)
 *
 * Spec: docs/plans/remote-model-provider.md 4.5a (what a relay does with a node's token), 4.5 last bullet, 4.1 ack n/a.
 * Code read: external/swarm/registry.go (RegisterWithDial: `if req.Token != ""` keeps the old token; lease ttl/grace,
 *   reapLocked, DetachTransport, Pin), external/swarm/mount.go (rewriteFor: Authorization = node.Token or none;
 *   handleMount: refuses an offline node), external/swarm/sessions.go (askNode: Authorization = node.Token,
 *   non-200 -> warning "<node>: <status>"), internal/swarm/joinset.go + join.go (the node re-registers on every
 *   heartbeat / reconnect; the lease secret is persisted on the node, so a restarted node renews its own lease;
 *   no fallback to the relay token for agents).
 *
 * WHAT IS MODELLED
 *   node side:  n_join in {empty, main, shared} = the token the node's swarm.join[].token gives NOW;
 *               the node's own gate config nm (main token) nsh (shared tokens) nlg (login) nsas (shared_as) nins
 *               (allow_insecure), all hot-reloadable; the gate is the NEW policy of model 1 (4.5), copied here.
 *   relay side: one registry entry: has (lease exists), age (discrete ticks since last renewal), reg_tok (the token
 *               the relay holds). alive iff age < TTL; offline for TTL <= age <= TTL+GRACE; deleted (entry and token
 *               cleared) when age > TTL+GRACE. Reaping is lazy in the code (every registry call reaps first); it is
 *               eager here, which is observationally equivalent because no reader looks at the token without reaping.
 *   client:     one relay client request through the mount (route k in {models, usage, completions, other_api,
 *               v1_chat, v1_responses_agent}) or the relay's own aggregated session fan-out (k = 6).
 *               Authorization = reg_tok (none when empty); the node's gate decides; the result is
 *               ALLOW / 401 / 403 / 404 (alias unknown) / 502 (node offline or unreachable) / NONE (no such node).
 *
 * TIME is a DISCRETE-CLOCK ABSTRACTION (untimed engine): process `clock` ticks; TTL = 2, GRACE = 2 ticks.
 *   Heartbeat timing assumption (DIRECT): a node that is up renews before the lease expires (tick blocked when the
 *   next tick would end the alive phase while the node is up). TUNNEL: the lease follows the connection (age frozen
 *   while connected; a crash sets the lease offline at once, DetachTransport).
 *
 * SWITCHES (-D): TUNNEL  MUT_EMPTY_ERASES (an empty token on renewal erases the old one: the rejected alternative)
 *   FULLCFG (the node's login and allow_insecure also change; default keeps them 0 to bound the state space)
 *   LIVE (scenario for the liveness question: starts with a lease holding `main`, node joined with `main`, one single
 *        reconfiguration n_join := empty, nothing else changes)   NO_CRASH (node never goes down)
 *   NO_RESTART (a crashed node never returns)   OLD_NODE (the node's gate is the old `enabled = main || login`)
 *   MUT_FALLBACK (the relay presents its own main-privilege token when the node's token is empty: the rejected design)
 *
 * RUN (one mcd process per property):  safety  mcd check --promela m4-auth-relay-relay.pml [-D TUNNEL ...] --ctl 'AG !w[k]' --no-timing  (k = 1..7)
 *   reach   ... --ctl 'EF w[k]' (k = 8..10);  liveness  ... -D LIVE [-D NO_CRASH|NO_RESTART] --ltl '<phi>' --fairness none|weak
 *   phi1 = [] ((n_join == 0) -> <> (reg_tok == 0))     phi2 = (<>[] (node_up == 0)) -> phi1
 *   (no fairness: --ctl 'AG ((n_join == 0) -> AF (reg_tok == 0))', the engine's LTL lasso extraction panics without fairness)
 * PROPERTY IDS (flag = violation, except 8-10 which are reached with EF):  w1 privilege never exceeds the registered token (node auth on)
 *   w2 share-only join never reaches other_api / v1 through the mount (node auth on)  w3 node auth on + empty token => every mounted call 401
 *   w4 empty token + open node: LLM routes with shared_as and no allow_insecure answer 403  w5 sessions warning iff the node's token cannot
 *   reach the sessions route  w6 no lease, no token  w7 a share-only join token works on the LLM routes  w8 open node + empty token reaches other_api
 *   w9 stale main token keeps other_api reachable after the join config dropped it  w10 stale shared token, node open, other_api reachable
 */

#define TTL 2
#define GRACE 2

#define JT_EMPTY  0
#define JT_MAIN   1
#define JT_SHARED 2

#define K_MODELS 0
#define K_USAGE  1
#define K_COMP   2
#define K_OTHER  3
#define K_V1CHAT 4
#define K_V1RESP 5
#define K_FAN    6
#define LLMK(k) ((k) <= 2)

#define O_ALLOW 0
#define O_401   1
#define O_403   2
#define O_404   3
#define O_502   4
#define O_NONE  5

#define CL_NONE   0
#define CL_MAIN   1
#define CL_SHARED 2

/* node config */
byte n_join;
bit nm, nsh, nlg, nsas, nins;
bit node_up;
bit crashed;             /* ghost: the node has been down at least once */

/* relay registry entry */
bit has;
byte age;
byte reg_tok;

/* record of the last client request */
bit rec;
byte rk, rout, rwarn;
bit w[12];

/* scratch */
byte c_cls, c_out, c_tok;
bit c_any, c_alive, c_oracle, c_nonllm;

#ifdef OLD_NODE
#define NSHD 0
#else
#define NSHD nsh
#endif

inline node_dec(K, TOK, OUT) {
  c_any = (nm || nlg || NSHD);
  c_cls = CL_NONE;
  if
  :: (TOK == JT_MAIN && nm) -> c_cls = CL_MAIN
  :: (TOK == JT_SHARED && NSHD) -> c_cls = CL_SHARED
  :: (!(TOK == JT_MAIN && nm) && !(TOK == JT_SHARED && NSHD)) -> skip
  fi;
  if
  /* gate */
  :: (c_any && c_cls == CL_NONE) -> OUT = O_401
  :: (c_any && c_cls == CL_SHARED && !LLMK(K)) -> OUT = O_401
  :: (!(c_any && c_cls == CL_NONE) && !(c_any && c_cls == CL_SHARED && !LLMK(K))) ->
       /* handler */
       if
       :: (LLMK(K) && nsas && !nins && !c_any) -> OUT = O_403
       :: (!(LLMK(K) && nsas && !nins && !c_any) && (K == K_USAGE || K == K_COMP) && !nsas) -> OUT = O_404
       :: (!(LLMK(K) && nsas && !nins && !c_any) && !((K == K_USAGE || K == K_COMP) && !nsas)) -> OUT = O_ALLOW
       fi
  fi
}

#define PRESENT c_tok
inline do_register() {
  if
  :: (!has) -> has = 1; age = 0; reg_tok = n_join
  :: (has) ->
       age = 0;
#ifdef MUT_EMPTY_ERASES
       reg_tok = n_join
#else
       if
       :: (n_join != JT_EMPTY) -> reg_tok = n_join
       :: (n_join == JT_EMPTY) -> skip
       fi
#endif
  fi
}

#ifdef NO_CRASH
#define CRASHOK 0
#else
#define CRASHOK 1
#endif
#ifdef NO_RESTART
#define RESTARTOK 0
#else
#define RESTARTOK 1
#endif

/* ---------------- node: heartbeat (direct transport only) */
#ifndef TUNNEL
proctype hb() {
  do
  :: (!rec && node_up) -> atomic { do_register() }
  od
}
#endif

/* ---------------- node: config changes */
proctype reconf() {
#ifdef LIVE
  /* the one reconfiguration of the scenario: the join token is removed from the config */
  atomic { n_join = JT_EMPTY }
#else
  do
  :: (!rec) ->
       atomic {
         if
         :: n_join = JT_EMPTY
         :: n_join = JT_MAIN
         :: n_join = JT_SHARED
         :: nm = !nm
         :: nsh = !nsh
         :: nsas = !nsas
#ifdef FULLCFG
         :: nlg = !nlg
         :: nins = !nins
#endif
         fi
       }
  od
#endif
}

/* ---------------- node: crash / restart */
proctype crash() {
  do
  :: (!rec && node_up && CRASHOK) ->
       atomic {
         node_up = 0; crashed = 1;
#ifdef TUNNEL
         if :: (has && age < TTL) -> age = TTL :: else -> skip fi
#endif
       }
  od
}

proctype restart() {
  do
  :: (!rec && !node_up && (RESTARTOK || !crashed)) -> atomic { node_up = 1; do_register() }
  od
}

/* ---------------- relay: discrete clock and lazy reaping */
proctype clock() {
  do
  :: (!rec && has
#ifdef TUNNEL
        && !node_up
#else
        && !(node_up && age + 1 >= TTL)
#endif
     ) ->
       atomic {
         age = age + 1;
         if
         :: (age > TTL + GRACE) -> has = 0; reg_tok = JT_EMPTY; age = 0
         :: (age <= TTL + GRACE) -> skip
         fi
       }
  od
}

/* ---------------- a relay client / the fan-out */
proctype client() {
  byte k;
  do
  :: (!rec) ->
       atomic {
         if :: k = 0 :: k = 1 :: k = 2 :: k = 3 :: k = 4 :: k = 5 :: k = 6 fi;
         rk = k;
         c_alive = (has && age < TTL);
         c_nonllm = (k >= K_OTHER && k <= K_V1RESP);
         rwarn = 0;
#ifdef MUT_FALLBACK
         if :: (reg_tok == JT_EMPTY) -> c_tok = JT_MAIN :: (reg_tok != JT_EMPTY) -> c_tok = reg_tok fi;
#else
         c_tok = reg_tok;
#endif
         if
         :: (!has) -> rout = O_NONE
         :: (has && !c_alive) -> rout = O_502; rwarn = 1
         :: (c_alive && !node_up) -> rout = O_502; rwarn = 1
         :: (c_alive && node_up) ->
              if
              :: (k == K_FAN) -> node_dec(K_OTHER, PRESENT, rout); if :: (rout != O_ALLOW) -> rwarn = 1 :: else -> skip fi
              :: (k != K_FAN) -> node_dec(k, PRESENT, rout)
              fi
         fi;
         c_any = (nm || nlg || nsh);
         c_oracle = (!c_any || (reg_tok == JT_MAIN && nm));    /* the token can reach the sessions route */
         /* W1  privilege never exceeds that of the registered token (node auth on) */
         w[1] = (k != K_FAN && rout == O_ALLOW && c_any && (reg_tok == JT_EMPTY || (c_nonllm && reg_tok == JT_SHARED)));
         /* W2  a share-only join token never reaches other_api / v1_chat / v1_responses through the mount (node auth on) */
         w[2] = (c_nonllm && rout == O_ALLOW && reg_tok == JT_SHARED && c_any);
         /* W3  node auth on + empty token: every mounted call, LLM routes included, is 401 */
         w[3] = (k != K_FAN && c_alive && node_up && c_any && reg_tok == JT_EMPTY && rout != O_401);
         /* W4  empty token + node auth off: LLM routes with shared_as and no allow_insecure answer 403 */
         w[4] = (LLMK(k) && c_alive && node_up && !c_any && reg_tok == JT_EMPTY && nsas && !nins && rout != O_403);
         /* W5  sessions warning iff the node's token cannot reach the sessions route (node online and reachable) */
         w[5] = (k == K_FAN && c_alive && node_up && (rwarn == c_oracle));
         /* W6  a lease that does not exist holds no token */
         w[6] = (!has && reg_tok != JT_EMPTY);
         /* W7  a share-only join token works on the LLM routes of a node that holds shared-model tokens */
         w[7] = (reg_tok == JT_SHARED && nsh && c_alive && node_up && (k == K_MODELS || (k == K_COMP && nsas)) && rout != O_ALLOW);
         /* W8  (reach) the hazard: empty token, node open, other_api allowed */
         w[8] = (k == K_OTHER && c_alive && node_up && !c_any && reg_tok == JT_EMPTY && rout == O_ALLOW);
         /* W9  (reach) stale privilege: the join config has no token, the relay still presents the old main one */
         w[9] = (k == K_OTHER && c_alive && node_up && n_join == JT_EMPTY && reg_tok == JT_MAIN && nm && rout == O_ALLOW);
         /* W10 (reach) the node's gate is off and the shared token is stale, yet other_api is reachable through a share-only join */
         w[10] = (c_nonllm && c_alive && node_up && reg_tok == JT_SHARED && !c_any && rout == O_ALLOW);
         rec = 1
       }
  :: rec ->
       atomic {
         rec = 0; rk = 0; rout = 0; rwarn = 0; k = 0;
         c_cls = 0; c_out = 0; c_tok = 0; c_any = 0; c_alive = 0; c_oracle = 0; c_nonllm = 0;
         w[1] = 0; w[2] = 0; w[3] = 0; w[4] = 0; w[5] = 0; w[6] = 0; w[7] = 0; w[8] = 0; w[9] = 0; w[10] = 0
       }
  od
}

#ifdef LIVE
/* the world never stops: without it a terminal state has no moving process and the weak-fairness null steps
   of the engine loop there without the claim moving (see the report, engine note) */
proctype idle() {
  do
  :: skip
  od
}
#endif

init {
#ifdef LIVE
  has = 1; age = 0; reg_tok = JT_MAIN; n_join = JT_MAIN; nm = 1; node_up = 1;
#endif
  atomic {
#ifndef TUNNEL
    run hb();
#endif
    run reconf(); run crash(); run restart(); run clock();
#ifndef LIVE
    run client();
#else
    run idle();
#endif
    skip
  }
}
