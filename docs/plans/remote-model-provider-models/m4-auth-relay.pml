/*
 * m4-auth-relay.pml  -  MODEL 1 of 2: the authentication policy with the LLM-only token class
 *
 * Spec: docs/plans/remote-model-provider.md  4.1 (alias), 4.2 first paragraph (routes always registered,
 * refusal by the LIVE policy), 4.5 (LLM-only token class, fail-closed, 403 rule), 5a rows Config/Auth.
 * Code read: external/httpserver/auth.go (authPolicyNow, authGate, isProtectedPattern, acceptBearer),
 * serve_http.go (authOn = tokenOn || loginOn, loopback warning), internal/config/http.go (EffectiveAuthTokens).
 *
 * WHAT IS MODELLED  (one process, `server`; one atomic step = one HTTP request decided against ONE snapshot
 * of the running config, or one hot reload of the config)
 *   config (installed by a hot reload, nondeterministically):
 *     g_m    main bearer token in config            g_lg  web login configured
 *     g_sh   >= 1 shared-model token                g_sas some models[] row has shared_as
 *     g_ins  httpserver.allow_insecure              g_lb  listener is loopback (must not matter)
 *     g_dup  the running config has one token in two classes (must be unreachable)
 *     xm     an OUT-OF-BAND main token (--auth-token / CODDY_HTTP_TOKEN, SetExtraAuthTokens); fixed per
 *            process, chosen at start only under -D XTRA (default: none)
 *   request: caller credential c in {none, main, shared, other, cookie}, route r in
 *     {llm_models, llm_usage, llm_completions, other_api, v1_chat, v1_responses_agent, public_static},
 *     model kind mk in {alias, selector}.
 *   decision (A_ALLOW | A_401 | A_403 | A_404) is computed three times from the same state:
 *     NEW   the policy of 4.5 (class-aware gate, handler 403 rule on the three LLM routes)
 *     OLD   today's authGate with the new routes merely registered: enabled = main || login, shared tokens
 *           unknown, no handler check (the broken comparison)
 *     OLD+H OLD plus the handler's 403 rule keyed on the OLD `enabled`
 *   Properties are 0/1 flags vN[k] / vO[k] / vH[k] set in the step that decides the request; LTL `[] !v[k]`.
 *
 * ABSTRACTIONS (all declared): tokens are classes, not strings; the cookie session is a credential that is
 *   valid iff login is configured and counts as main class (gate: hasCookieSession); the same-origin check
 *   for cookie writes, the ?access_token= media capability and publicDocs are out of scope; the body of the
 *   response (200/404 from the handler) is collapsed into ALLOW/A_404; a hot reload is atomic (the installed
 *   config is the one whole snapshot a request sees); the request step is followed by a forced `clear` step
 *   (rec guard) so nothing can interleave inside the record window; in the single-read model gate and handler
 *   see the same snapshot. Under -D TWO_READ the request is split in gate step and handler step and a reload
 *   may fall between them (the plan says the handler is refused "by the live policy": two reads).
 *
 * SWITCHES (-D):  MAIN_FIRST (dup class resolved to main; default: shared = least privilege)
 *   XTRA (out-of-band main token possible)  CHECK_EXTRA (the reload path also compares with it)
 *   MUT_NODUP (no duplicate check at load)  MUT_LOOPBACK (403 rule skipped on loopback listener)
 *   MUT_STARTUP (403 rule keyed on the credential state at start-up)  MUT_NOCLASS (shared token admitted
 *   on every route)  TWO_READ (gate and handler read the config separately)
 *   SMALL (with TWO_READ: no loopback flag, credentials none/main/shared only)   GHOST (ghost bits for the "first request after a
 *   hot reload that removed the last credential" property P13 and the reachability of a rejected duplicate)
 *
 * RUN (CLI; CTL is used for the safety properties because the engine's LTL lasso extraction panics on a violated
 *   `[] !v` here; see the report): one process per property, e.g.
 *   mcd check --promela m4-auth-relay.pml [-D ...] --ctl 'AG !vN[k]' --no-timing     (k = 1..12; vO / vH for OLD, OLD+H)
 *   mcd check --promela m4-auth-relay.pml --ctl 'AG !g_dup' --no-timing
 *   mcd check --promela m4-auth-relay.pml -D TWO_READ -D SMALL --ctl 'AG !vN[14]' --no-timing   (also vN[15])
 * PROPERTY IDS (flag = violation):  1 shared token never on a non-LLM route   2 any shared token => anonymous 401
 *   3 share-only node   4 403 rule   5 main/login reach everything   6 selector never served   7 shared token reaches
 *   the shared routes   8 static shell never refused   9 allow_insecure irrelevant once a credential exists
 *   10 listener address irrelevant   11 no-credential/no-shared-row node unchanged   12 no regression on non-LLM routes
 *   without shared tokens   13 (GHOST) P4 on the first request after a reload   14/15 (TWO_READ) PT1 / PT2
 */


#define R_MODELS 0
#define R_USAGE  1
#define R_COMP   2
#define R_OTHER  3
#define R_V1CHAT 4
#define R_V1RESP 5
#define R_STATIC 6
#define LLM(r) ((r) <= 2)

#define C_NONE   0
#define C_MAIN   1
#define C_SHARED 2
#define C_OTHER  3
#define C_COOKIE 4

#define M_ALIAS 0
#define M_SEL   1

#define A_ALLOW 0
#define A_401   1
#define A_403   2
#define A_404   3
#define G_PASS  7

#define CL_NONE   0
#define CL_MAIN   1
#define CL_SHARED 2

#ifdef MAIN_FIRST
#define DUPCLS CL_MAIN
#else
#define DUPCLS CL_SHARED
#endif

#ifdef MUT_NOCLASS
#define SHROUTE(r) 1
#else
#define SHROUTE(r) LLM(r)
#endif

#ifdef MUT_STARTUP
#define BOOTSET boot_any = (cm || xm || cl || cs)
#define HANY boot_any
#else
#define BOOTSET skip
#define HANY (g_m || xm || g_lg || g_sh)
#endif

#ifdef MUT_LOOPBACK
#define LBOK(lb) (!(lb))
#else
#define LBOK(lb) 1
#endif

#ifdef GHOST
#define GINSTALL prev_any = (g_m || xm || g_lg || g_sh); fresh = 1
#define GREQ rq_fresh = fresh; fresh = 0
#define GREJ rej_dup = 1
#define GNOREJ rej_dup = 0
#else
#define GINSTALL skip
#define GREQ skip
#define GREJ skip
#define GNOREJ skip
#endif

#ifdef MUT_NODUP
#define NODUP 1
#else
#define NODUP 0
#endif

#ifdef CHECK_EXTRA
#define CHECKX 1
#else
#define CHECKX 0
#endif

/* ---- running config ---- */
bit g_m, g_lg, g_sh, g_sas, g_ins, g_lb, g_dup;
bit xm;
bit booted, boot_any, rej_dup;
bit prev_any, fresh, rq_fresh;   /* ghosts: the config before the last reload had a credential; no request since the last reload */

/* ---- record of the last request ---- */
bit rec;
byte rq_r, rq_c, rq_mk;
byte dn, do1, do2, dn_ins, dn_lb;
bit vN[16]; bit vO[16]; bit vH[16];

/* ---- scratch ---- */
bit d_on, d_any;
byte d_cls, d_gres, d_hres;
bit ins_v, lb_v;
bit mv, cv, sv, anyc, llm_, nonllm, sel, okr, prot, anon, so;

/* TWO_READ in-flight state */
byte phase;          /* 0 idle, 1 gate passed under policy A, 2 evaluated */
byte gA, hA, decA, fin1, fin2, decB;

/* ------------------------------------------------------------------ NEW policy (4.5) */
inline cls_new(C) {
  d_cls = CL_NONE;
  if
  :: (g_dup && ((C == C_MAIN && (g_m || xm)) || (C == C_SHARED && g_sh))) -> d_cls = DUPCLS
  :: (!g_dup && C == C_MAIN && (g_m || xm)) -> d_cls = CL_MAIN
  :: (C == C_COOKIE && g_lg) -> d_cls = CL_MAIN
  :: (!g_dup && C == C_SHARED && g_sh) -> d_cls = CL_SHARED
  :: else -> skip
  fi
}

inline gate_new(R, C) {
  d_any = (g_m || xm || g_lg || g_sh);
  cls_new(C);
  if
  :: (R == R_STATIC) -> d_gres = G_PASS
  :: (R != R_STATIC && !d_any) -> d_gres = G_PASS
  :: (R != R_STATIC && d_any && d_cls == CL_MAIN) -> d_gres = G_PASS
  :: (R != R_STATIC && d_any && d_cls == CL_SHARED && SHROUTE(R)) -> d_gres = G_PASS
  :: else -> d_gres = A_401
  fi
}

inline handler_new(R, MK, INS, LB) {
  if
  :: (LLM(R) && g_sas && !INS && !HANY && LBOK(LB)) -> d_hres = A_403
  :: else ->
       if
       :: ((R == R_COMP || R == R_USAGE) && (MK == M_SEL || !g_sas)) -> d_hres = A_404
       :: else -> d_hres = A_ALLOW
       fi
  fi
}

inline decide_new(R, C, MK, INS, LB, OUT) {
  gate_new(R, C);
  if
  :: (d_gres == A_401) -> OUT = A_401
  :: else -> handler_new(R, MK, INS, LB); OUT = d_hres
  fi
}

/* ------------------------------------------------------------------ OLD logic (today's gate) */
inline decide_old(R, C, MK, OUT) {
  d_on = (g_m || xm || g_lg);
  if
  :: (R == R_STATIC || !d_on || (C == C_MAIN && (g_m || xm)) || (C == C_COOKIE && g_lg) || (C == C_SHARED && g_dup)) ->
       if
       :: ((R == R_COMP || R == R_USAGE) && (MK == M_SEL || !g_sas)) -> OUT = A_404
       :: else -> OUT = A_ALLOW
       fi
  :: else -> OUT = A_401
  fi
}

inline decide_oldh(R, C, MK, OUT) {
  d_on = (g_m || xm || g_lg);
  if
  :: (R == R_STATIC || !d_on || (C == C_MAIN && (g_m || xm)) || (C == C_COOKIE && g_lg) || (C == C_SHARED && g_dup)) ->
       if
       :: (LLM(R) && g_sas && !g_ins && !d_on) -> OUT = A_403
       :: (!(LLM(R) && g_sas && !g_ins && !d_on) && (R == R_COMP || R == R_USAGE) && (MK == M_SEL || !g_sas)) -> OUT = A_404
       :: (!(LLM(R) && g_sas && !g_ins && !d_on) && !((R == R_COMP || R == R_USAGE) && (MK == M_SEL || !g_sas))) -> OUT = A_ALLOW
       fi
  :: else -> OUT = A_401
  fi
}

/* ------------------------------------------------------------------ properties (flag = violation) */
inline derive() {
  mv = (rq_c == C_MAIN && (g_m || xm));
  cv = (rq_c == C_COOKIE && g_lg);
  sv = (rq_c == C_SHARED && g_sh);
  anyc = (g_m || xm || g_lg || g_sh);
  llm_ = LLM(rq_r);
  prot = (rq_r != R_STATIC);
  nonllm = (rq_r != R_STATIC && !LLM(rq_r));
  sel = ((rq_r == R_COMP || rq_r == R_USAGE) && rq_mk == M_SEL);
  okr = (rq_r == R_MODELS || (rq_mk == M_ALIAS && g_sas));
  anon = (!mv && !cv && !sv);
  so = (g_sh && !(g_m || xm) && !g_lg)
}

inline checkp(DEC, V) {
  /* P1  a (valid) shared token is never allowed on a non-LLM route */
  V[1] = (sv && nonllm && DEC == A_ALLOW);
  /* P2  any shared token turns the gate on: anonymous / invalid credential gets 401 on every protected route */
  V[2] = (g_sh && prot && anon && DEC != A_401);
  /* P3  share-only node (no main, no login): all non-LLM routes 401 for everybody; LLM routes only for the shared token */
  V[3] = (so && ((nonllm && DEC != A_401) || (llm_ && sv && (DEC == A_401 || DEC == A_403)) || (llm_ && !sv && DEC != A_401)));
  /* P4  shared_as + no credential + not allow_insecure: the three LLM routes answer 403 */
  V[4] = (g_sas && !anyc && !g_ins && llm_ && DEC != A_403);
  /* P5  the main token (and a login session) reaches everything */
  V[5] = ((mv || cv) && (DEC == A_401 || DEC == A_403));
  /* P6  the selector as `model` is never served on the alias routes */
  V[6] = (sel && DEC == A_ALLOW);
  /* P7  a valid shared token reaches the shared routes (functional) */
  V[7] = (sv && llm_ && okr && DEC != A_ALLOW);
  /* P8  public static shell is never refused */
  V[8] = (rq_r == R_STATIC && DEC != A_ALLOW);
  /* P11 a node with no credential and no shared row behaves as before (never 401/403) */
  V[11] = (!anyc && !g_sas && (DEC == A_401 || DEC == A_403));
  /* P13 P4 restated for the first request after a hot reload that removed the last credential */
  V[13] = (rq_fresh && prev_any && !anyc && g_sas && !g_ins && llm_ && DEC != A_403)
}

inline checknew() {
  /* P9  allow_insecure cannot open a node that has a credential */
  ins_v = !g_ins;
  decide_new(rq_r, rq_c, rq_mk, ins_v, g_lb, dn_ins);
  vN[9] = (anyc && dn != dn_ins);
  /* P10 the listener address is irrelevant */
  lb_v = !g_lb;
  decide_new(rq_r, rq_c, rq_mk, g_ins, lb_v, dn_lb);
  vN[10] = (dn != dn_lb);
  /* P12 no regression: without shared tokens the non-LLM routes behave exactly as today */
  vN[12] = (!g_sh && nonllm && dn != do1)
}

inline wipe() {
  d_on = 0; d_any = 0; d_cls = 0; d_gres = 0; d_hres = 0; ins_v = 0; lb_v = 0;
  mv = 0; cv = 0; sv = 0; anyc = 0; llm_ = 0; nonllm = 0; sel = 0; okr = 0; prot = 0; anon = 0; so = 0
}

/* ------------------------------------------------------------------ the server */
proctype server() {
  bit cm, cl, cs, csas, cins, clb, cdc, cdx;
  byte i;
  do
  /* hot reload: pick a candidate config; a duplicate-class candidate is refused and the running one stays */
  :: (!rec) ->
       atomic {
         if :: cm = 0 :: cm = 1 fi;
         if :: cl = 0 :: cl = 1 fi;
         if :: cs = 0 :: cs = 1 fi;
         if :: csas = 0 :: csas = 1 fi;
         if :: cins = 0 :: cins = 1 fi;
#ifdef SMALL
         clb = 0;
#else
         if :: clb = 0 :: clb = 1 fi;
#endif
         cdc = 0; cdx = 0;
         if :: (cm && cs) -> if :: cdc = 0 :: cdc = 1 fi :: else -> skip fi;
         if :: (xm && cs) -> if :: cdx = 0 :: cdx = 1 fi :: else -> skip fi;
         if
         :: (cdc && !NODUP) -> GREJ
         :: (!cdc && cdx && CHECKX && !NODUP) -> GREJ
         :: else ->
              GINSTALL;
              g_m = cm; g_lg = cl; g_sh = cs; g_sas = csas; g_ins = cins; g_lb = clb;
              g_dup = (cdc || cdx); GNOREJ;
              if
              :: (!booted) -> booted = 1; BOOTSET
              :: else -> skip
              fi
         fi;
         cm = 0; cl = 0; cs = 0; csas = 0; cins = 0; clb = 0; cdc = 0; cdx = 0
       }
#ifndef TWO_READ
  /* single-read request: gate and handler see one snapshot */
  :: (booted && !rec) ->
       atomic {
         if :: rq_r = 0 :: rq_r = 1 :: rq_r = 2 :: rq_r = 3 :: rq_r = 4 :: rq_r = 5 :: rq_r = 6 fi;
         if :: rq_c = 0 :: rq_c = 1 :: rq_c = 2 :: rq_c = 3 :: rq_c = 4 fi;
         if :: rq_mk = 0 :: rq_mk = 1 fi;
         GREQ;
         decide_new(rq_r, rq_c, rq_mk, g_ins, g_lb, dn);
         decide_old(rq_r, rq_c, rq_mk, do1);
         decide_oldh(rq_r, rq_c, rq_mk, do2);
         derive();
         checkp(dn, vN);
         checkp(do1, vO);
         checkp(do2, vH);
         checknew();
         rec = 1
       }
  :: rec ->
       atomic {
         rec = 0; wipe(); rq_fresh = 0;
         rq_r = 0; rq_c = 0; rq_mk = 0; dn = 0; do1 = 0; do2 = 0; dn_ins = 0; dn_lb = 0;
         i = 0;
         do :: (i < 16) -> vN[i] = 0; vO[i] = 0; vH[i] = 0; i++ :: else -> break od; i = 0
       }
#else
  /* two-read request: gate under config A, handler under config B (H1) or under A (H2) */
  :: (booted && !rec && phase != 1) ->
       atomic {
         if :: rq_r = 0 :: rq_r = 1 :: rq_r = 2 :: rq_r = 3 :: rq_r = 4 :: rq_r = 5 :: rq_r = 6 fi;
#ifdef SMALL
         if :: rq_c = 0 :: rq_c = 1 :: rq_c = 2 fi;
#else
         if :: rq_c = 0 :: rq_c = 1 :: rq_c = 2 :: rq_c = 3 :: rq_c = 4 fi;
#endif
         if :: rq_mk = 0 :: rq_mk = 1 fi;
         decide_new(rq_r, rq_c, rq_mk, g_ins, g_lb, decA);
         gate_new(rq_r, rq_c); gA = d_gres;
         handler_new(rq_r, rq_mk, g_ins, g_lb); hA = d_hres;
         phase = 1
       }
  :: (phase == 1 && !rec) ->
       atomic {
         decide_new(rq_r, rq_c, rq_mk, g_ins, g_lb, decB);
         handler_new(rq_r, rq_mk, g_ins, g_lb);
         if :: (gA == A_401) -> fin1 = A_401; fin2 = A_401 :: else -> fin1 = d_hres; fin2 = hA fi;
         dn = fin1; do1 = fin2;
         derive();
         /* PT1: H1 (handler re-reads the live policy): the answer must be the answer of A or of B */
         vN[14] = (fin1 == A_ALLOW && decA != A_ALLOW && decB != A_ALLOW);
         /* PT2: H2 (handler uses the gate's snapshot): the answer is the answer of A */
         vN[15] = (fin2 != decA);
         /* P1 on the final answer of H1 */
         vN[1] = (sv && nonllm && fin1 == A_ALLOW);
         rec = 1; phase = 2
       }
  :: rec ->
       atomic {
         rec = 0; phase = 0; wipe();
         rq_r = 0; rq_c = 0; rq_mk = 0; dn = 0; do1 = 0; decA = 0; decB = 0; fin1 = 0; fin2 = 0; gA = 0; hA = 0;
         i = 0;
         do :: (i < 16) -> vN[i] = 0; i++ :: else -> break od; i = 0
       }
#endif
  od
}

init {
#ifdef XTRA
  if :: xm = 0 :: xm = 1 fi;
#endif
  run server()
}
