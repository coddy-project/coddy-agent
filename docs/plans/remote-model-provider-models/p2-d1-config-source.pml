/* p2-d1-config-source.pml - how a `*config.Config` reaches the listing cache of the session manager.

   Decides  dilemma D1 of docs/plans/remote-model-provider-phase2.md (section 7) and the places that depend
            on it: 3.3 (the resolver), 3.5 (reaching the cache), the S1 stage notes of section 8. Builds on
            m6-capabilities (the capability mechanism D, properties P1, P2, CK, L1, L2): here the listing
            cache is given, and only the PATH from a reader's config to it is varied.
   Code     internal/session/manager.go (NewManager, storeConfig, ReloadConfigForSession),
            internal/session/context_window.go (the cache is keyed by contextWindowKey(row) = type|name|
            api_base|proxy and read as ProviderModelEntry(cfg, providerName, apiModel): the ROW IS FOUND IN
            THE CONFIG THE READER PASSES), external/httpserver/skills_mgmt.go:546-556 (the server stores a
            loaded config BEFORE the manager does), internal/agent/react.go:425 and resume_permission.go:399
            (the agent loads its own config after the reload tool and keeps it as a.cfg),
            internal/agent/coddy_provider.go (revision read per call through State), internal/session/state.go
            (EffectiveReasoning(cfg) is read per LLM call).
   Class    finite, untimed, interleaving. NO CLOCK: the TTL of the cache and the fetch latency are abstracted
            by a Refresher that may complete a fetch for any key at any moment (a superset of every TTL
            schedule); "bounded number of steps" is read as: bound by the loader's own steps, or never.
            Nothing here is a timed automaton.

   World (every domain is tiny on purpose):
     keys     NK=4 = provider row (2) x api_base generation (2): the cache key of a config's row. A config's
              `ckey` is the key of the row that serves the alias in THAT config. Repointing the alias to
              another row flips the row bit, editing api_base flips the generation bit.
     remote   tr[k] in {1,2}: the capability CLASS the remote at key k serves now (a class offers exactly one
              level, so any change invalidates a level; this is the narrowest model of "levels differ").
     cache    cv[k] in {0,1,2}: the record of the listing cache for key k (0 = never answered). A fetch copies
              tr[k] into cv[k]. The revision is the class itself (equal class = same revision): sufficient,
              because only equality with the remote's current class is ever asked.
     configs  slot c: ckey[c], csrc[c] (R1 only: the source in the config's own binding cell, 0 = unbound),
              cbind[c] (R1 only: 0 nothing, 1 half-installed (BIND2 mutant), 2 installed), cview[c] (a value
              frozen into the config: R4 overlay, or the FROZEN-PER-CONFIG read variant).
     holders  mgr (the manager's pointer), srv (the HTTP server's own pointer), agt (the agent's private
              pointer, NONE outside a turn), hmgr/hsrv/hagt (the handle that travels with a pointer: R3).
     source   lsrc (R5: the lineage cell on config.Paths, one atomic cell shared by every config loaded
              from a lineage), gsrc (R2: the process-global source). 0 = nobody attached, 1 = the production
              manager, 2 = a second manager started by a test (NMGR=2).

   Processes
     Remote     the remote operator flips tr[k] (capability change), at most NOPR times
     Refresher  completes a fetch for any key whose cache differs from the remote (R4 then republishes)
     Local      the local operator edits the config file: repoint the alias to the other row, edit api_base,
                or an edit that leaves the row alone, through the HTTP save (path P) or the skills path (path S:
                the server stores the loaded config BEFORE the manager does, as skills_mgmt.go does today)
     Store      the manager side of every edit, one job at a time (storeConfig): the loader built the config
                unbound, [S: srv = c], bind, [BIND2: second store], mgr = c, observer: srv = c
     Turn       one agent turn: NewAgent(mgr.Cfg()), validate a level at admission, up to NCALL model calls
                (each reads the level and the revision through the agent's config, the remote validates against
                its CURRENT row), optional reload tool between calls (path A: the manager reloads through Store,
                then the AGENT loads its own config: react.go:425), then the turn ends or parks forever
     Rdr        readers: GET /v1/models (reads srv) and a settings change (reads mgr), pointer first, resolve
                in a second step
     Test       NMGR=2: a second NewManager is handed the same config object (test binaries do this)
     Saver      R4 only: a settings save serves mgr.Cfg() and writes its keys back to config.yaml

   Variants (-D NAME=v)
     MECH   1 binding on the config (R1; defaults REBIND=0, SRVFIRST=1, BIND2=0 = 7-D1's reading, the agent never re-binds; 3.5's ShareListing is REBIND=1)
            2 process-global source (R2)   3 explicit handle travelling with the pointer (R3)
            4 overlay config republished with the listing in the keys (R4)   5 source rides on config.Paths: one
              atomic cell per lineage, attached by NewManager (R5)   6 = 5 plus a backstop in storeConfig that
              binds a config no loader made (HAND=1 builds such configs: a test's hand-built ReplaceConfig)
     REBIND  R1: the agent's private reload shares the old config's binding (ShareListing)
     SRVFIRST  the skills path stores the loaded config in the server before the manager binds it (today's code)
     BIND2   R1 mutant: bind is two stores; between them the source is installed but the row map is the old one
     R3NIL   R3 mutant: the agent's reload forgets to carry the handle
     SRCCFG  1: the source finds the row in the config the reader passes (the interface of plan 3.3);
             0 mutant: it finds the row in the manager's CURRENT config
     READ    0 live: level and revision come from ONE read per call (consistent snapshot)
             1 live split: level and revision are two separate reads (a refresh can land between them)
             2 frozen per turn: both come from the admission snapshot, nothing unfreezes it
             3 frozen per config: the value is frozen into the config when it is created
             4 frozen per turn, unfrozen by a refusal: the snapshot is the provider's, built once per turn
               (react.go:336) and replaced by the refreshed record when the remote refuses (coddy.go setView):
               what the code does today
     NMGR    2 starts a second manager once
     WB WC WS  latch groups a run tracks (binding, reads, sanity); WB=WC=WS=0 plus R4 tracks only W1/W2
     NEDIT NOPR NCALL NRD  bounds (edits of the config, remote changes, model calls per turn, extra readers)

   Latches (ghosts) read by the LTL properties; every one is monotone:
     cross   a read answered from another key than the reader's config names (the cross-row answer)
     xmgr    a read answered from another manager's cache
     halfseen a reader met a half-installed binding
     unbA unbS unbM  a decision read of the agent / the server / the manager's own settings went through a
             config with no source
     blind   such a read happened although the cache already held the row's record
     stalerd a bound read returned something other than the cache's record at that instant (frozen read)
     lost    a call was accepted, the level validated at admission is offered by the remote now, and the call
             did not carry it (the level was silently dropped)
     rfstale rflevel  a request was refused at the revision gate / for its level
     cK0 cK1 cK2     consec refusals in a row reached 1 / 2 / 3 (CK = K holds iff cK<K> is unreachable)
     fnonop  the config file holds a key the operator never wrote (R4: P2 of m6)
     stfromref  a listing refresh called storeConfig (R4: cancels in-flight usage fetches)
*/

#ifndef MECH
#define MECH 1
#endif
#ifndef REBIND
#define REBIND 0
#endif
#ifndef SRVFIRST
#define SRVFIRST 1
#endif
#ifndef BIND2
#define BIND2 0
#endif
#ifndef R3NIL
#define R3NIL 0
#endif
#ifndef SRCCFG
#define SRCCFG 1
#endif
#ifndef READ
#define READ 0
#endif
#ifndef NMGR
#define NMGR 1
#endif
#ifndef HAND
#define HAND 0
#endif
#ifndef NEDIT
#define NEDIT 2
#endif
#ifndef NOPR
#define NOPR 2
#endif
#ifndef NCALL
#define NCALL 2
#endif
#ifndef NRD
#define NRD 2
#endif

/* Latch groups: a run tracks only the latches of the groups it asks for, so the state count does not carry
   the product of every ghost (they are monotone bits, but each reachable combination is a state). */
#ifndef WB
#define WB 1
#endif
#ifndef WC
#define WC 1
#endif
#ifndef WS
#define WS 1
#endif
#if WB
#define SB(x) x = 1
#else
#define SB(x) skip
#endif
#if WC
#define SC(x) x = 1
#else
#define SC(x) skip
#endif
#if WS
#define SS(x) x = 1
#else
#define SS(x) skip
#endif
#define NK 2
#define NC 8
#define NONE 8
#define KSWITCH(k) (1 - (k))

#if MECH == 1
#define SRCOF(p,h) (csrc[p])
#elif MECH == 2
#define SRCOF(p,h) (gsrc)
#elif MECH == 3
#define SRCOF(p,h) (h)
#elif MECH == 5 || MECH == 6
#define SRCOF(p,h) (clin[p] * lsrc + (1 - clin[p]) * csrc[p])
#else
#define SRCOF(p,h) (1)
#endif

#if MECH == 1 && BIND2
#define HALF(p) (cbind[p] == 1)
#else
#define HALF(p) (0 == 1)
#endif

byte tr[NK];
byte cv[NK];
byte ckey[NC + 1];
byte csrc[NC + 1];
byte cbind[NC + 1];
byte cview[NC + 1];
byte clin[NC + 1];            /* loaded from the lineage of the attached manager (R5, R6) */
byte ncfg;
byte mgr; byte srv; byte agt;
byte hmgr; byte hsrv; byte hagt;
byte lsrc; byte gsrc;
bit m2;
byte nrem; byte nedit; byte nrd;
byte consec;
byte nc;                      /* scratch: slot a loader just built */

/* the manager side of one edit (Store) */
bit jbusy; byte jst; byte jcfg; byte jpath;   /* jpath 0 save (P), 1 skills (S), 2 agent reload (A) */
bit adone;

/* the turn */
byte tL; byte tkey; byte tv1; byte ncalls; byte rdp; bit areq; bit agtpriv;

/* scratch of a read */
byte rs; byte rk; byte rv; byte rm; byte jk; byte jt;

/* latches */
bit cross; bit xmgr; bit halfseen; bit unbA; bit unbS; bit unbM; bit blind; bit stalerd; bit lost;
bit rfstale; bit rflevel; bit cK0; bit cK1; bit cK2; bit fnonop; bit stfromref;
bit agtUnb; bit srvUnb;
/* sanity latches */
bit sA; bit sS; bit sM; bit swapped; bit knownread; bit privread; bit ovpend; bit saved;

/* scratch is cleared at the end of every step: a value left behind would only multiply the states */
inline clr() { rs = 0; rk = 0; rv = 0; rm = 0; jk = 0; jt = 0; nc = 0 }

inline aupd() {
  if :: (agt != NONE && SRCOF(agt, hagt) == 0) -> agtUnb = 1 :: else -> agtUnb = 0 fi;
  if :: (SRCOF(srv, hsrv) == 0) -> srvUnb = 1 :: else -> srvUnb = 0 fi
}

/* a loader builds a configuration: a fresh object, unbound under R1, bound by construction elsewhere */
inline newcfg(k) {
  nc = ncfg; ncfg++;
  ckey[nc] = k; csrc[nc] = 0; cbind[nc] = 0; clin[nc] = 1;
#if MECH == 4 || READ == 3
  cview[nc] = cv[k]
#endif
}

/* the reader's resolver: source of the config, then the row, then the cache record */
inline resolve(p, h) {
  rs = SRCOF(p, h);
  if
  :: rs == 0 -> rk = 0; rv = 0; rm = 0
  :: else ->
       rm = rs;
#if MECH == 4
       rk = ckey[p]; rv = cview[p]
#else
       if
       :: HALF(p) -> rk = ckey[mgr]; SB(halfseen)
       :: !HALF(p) && SRCCFG -> rk = ckey[p]
       :: !HALF(p) && !SRCCFG -> rk = ckey[mgr]
       fi;
#if READ == 3
       rv = cview[p]
#else
       rv = cv[rk]
#endif
#endif
  fi
}

inline ghostchk(p, role) {
  if
  :: rs == 0 ->
       if :: role == 1 -> SB(unbA) :: role == 2 -> SB(unbS) :: role == 3 -> SB(unbM) fi;
       if :: cv[ckey[p]] != 0 -> SB(blind) :: else -> skip fi
  :: else ->
       if :: rk != ckey[p] -> SB(cross) :: else -> skip fi;
       if :: rm != 1 -> SB(xmgr) :: else -> skip fi;
       if :: rv != cv[rk] -> SC(stalerd) :: else -> skip fi
  fi;
  if :: rv != 0 -> SS(knownread) :: else -> skip fi
}

/* the remote validates one call against its CURRENT row: the gate first (revision), then the level */
inline judge(l, r) {
  jk = ckey[agt]; jt = tr[jk];
  if
  :: (r != 0 && r != jt) ->
       SC(rfstale); cv[jk] = jt;
#if READ == 4
       tv1 = jt;                /* the refusal refreshes the provider's own view (coddy.go setView) */
#endif
       if :: (WC && consec < 3) -> consec++ :: else -> skip fi
  :: (r == 0 || r == jt) && (l != 0 && l != jt) ->
       SC(rflevel); cv[jk] = jt;
#if READ == 4
       tv1 = jt;
#endif
       if :: (WC && consec < 3) -> consec++ :: else -> skip fi
  :: (r == 0 || r == jt) && (l == 0 || l == jt) ->
       if :: (tL != 0 && tL == jt && l != tL && ckey[agt] == tkey) -> SC(lost) :: else -> skip fi;
       consec = 0
  fi;
  if :: consec >= 1 -> SC(cK0) :: else -> skip fi;
  if :: consec >= 2 -> SC(cK1) :: else -> skip fi;
  if :: consec >= 3 -> SC(cK2) :: else -> skip fi;
  if :: agtpriv -> SS(privread) :: else -> skip fi
}

/* a request for an edit of the config: the loader builds it (unbound under R1) and Store takes over */
#define JOBG nedit < NEDIT && !jbusy
#define JOBDO(P, K) nedit++; jbusy = 1; jpath = P; newcfg(K); jcfg = nc; nc = 0; jst = 1; consec = 0

proctype Remote() {
  do
  :: d_step { nrem < NOPR -> tr[0] = 3 - tr[0]; nrem++; consec = 0 }
  :: d_step { nrem < NOPR -> tr[1] = 3 - tr[1]; nrem++; consec = 0 }
  :: nrem >= NOPR -> break
  od
}

proctype Refresher() {
end:
  do
  :: d_step { cv[0] != tr[0] -> cv[0] = tr[0]; if :: (MECH == 4 && ckey[mgr] == 0) -> ovpend = 1 :: else -> skip fi }
  :: d_step { cv[1] != tr[1] -> cv[1] = tr[1]; if :: (MECH == 4 && ckey[mgr] == 1) -> ovpend = 1 :: else -> skip fi }
#if MECH == 4
  /* R4: the manager republishes an overlay: a new config with the listing in the keys, through storeConfig */
  :: d_step { ovpend && ncfg < NC && !jbusy ->
       newcfg(ckey[mgr]); mgr = nc; srv = nc; nc = 0; hmgr = 1; hsrv = 1;
       stfromref = 1; ovpend = 0; SS(swapped); clr(); aupd() }
#endif
  od
}

proctype Local() {
  do
  :: d_step { JOBG -> JOBDO(0, ckey[mgr]) }
  :: d_step { JOBG -> JOBDO(0, KSWITCH(ckey[mgr])) }
#if HAND
  :: d_step { JOBG -> JOBDO(0, ckey[mgr]); clin[jcfg] = 0 }
  :: d_step { JOBG -> JOBDO(1, ckey[mgr]); clin[jcfg] = 0 }
#endif
#if SRVFIRST
  :: d_step { JOBG -> JOBDO(1, ckey[mgr]) }
  :: d_step { JOBG -> JOBDO(1, KSWITCH(ckey[mgr])) }
#endif
  :: nedit >= NEDIT -> break
  od
}

/* storeConfig and what is around it, one edit at a time */
proctype Store() {
end:
  do
  :: d_step { jst == 1 && jpath == 1 && SRVFIRST -> srv = jcfg; hsrv = 1; jst = 2; aupd() }
  :: d_step { jst == 1 && !(jpath == 1 && SRVFIRST) -> jst = 2 }
  :: d_step { jst == 2 ->
#if MECH == 1
       csrc[jcfg] = 1;
       if :: BIND2 -> cbind[jcfg] = 1; jst = 3 :: else -> cbind[jcfg] = 2; jst = 4 fi;
#elif MECH == 6
       if :: !clin[jcfg] -> csrc[jcfg] = 1 :: else -> skip fi;
       jst = 4;
#else
       jst = 4;
#endif
       aupd() }
  :: d_step { jst == 3 -> cbind[jcfg] = 2; jst = 4 }
  :: d_step { jst == 4 -> mgr = jcfg; hmgr = 1; SS(swapped); jst = 5; aupd() }
  :: d_step { jst == 5 -> srv = jcfg; hsrv = 1; jst = 0; jbusy = 0; if :: jpath == 2 -> adone = 1 :: else -> skip fi; aupd() }
  od
}

proctype Turn() {
  byte w1;                      /* the level view of a split read (READ=1), private to the turn */
  d_step { agt = mgr; hagt = hmgr; aupd() }
  d_step { resolve(agt, hagt); ghostchk(agt, 1);
#if WC
    tL = rv; tkey = ckey[agt];
#endif
#if READ == 2 || READ == 4
    tv1 = rv;
#endif
    SS(sA); clr() }
  do
  :: d_step { ncalls < NCALL && rdp == 0 && !areq && nedit < NEDIT && !jbusy -> JOBDO(2, ckey[mgr]); areq = 1 }
  :: d_step { ncalls < NCALL && rdp == 0 && !areq && nedit < NEDIT && !jbusy -> JOBDO(2, KSWITCH(ckey[mgr])); areq = 1 }
  /* the agent loads its own configuration from the file the manager just reloaded (react.go:425) */
  :: d_step { areq && adone ->
       adone = 0; areq = 0;
       newcfg(ckey[mgr]);
#if MECH == 1 && REBIND
       csrc[nc] = csrc[agt]; cbind[nc] = cbind[agt];
#endif
#if MECH == 3 && R3NIL
       hagt = 0;
#endif
       agt = nc; agtpriv = 1; nc = 0; aupd() }
#if READ == 0 || READ == 3
  :: d_step { ncalls < NCALL && rdp == 0 && !areq -> resolve(agt, hagt); ghostchk(agt, 1); judge(rv, rv); ncalls++; clr() }
#elif READ == 1
  :: d_step { ncalls < NCALL && rdp == 0 && !areq -> resolve(agt, hagt); ghostchk(agt, 1); w1 = rv; rdp = 1; rs = 0; rk = 0; rv = 0; rm = 0 }
  :: d_step { rdp == 1 -> resolve(agt, hagt); ghostchk(agt, 1); judge(w1, rv); rdp = 0; ncalls++; w1 = 0; clr() }
#else
  /* READ 2 and 4: the provider was built at admission and keeps its snapshot (agent/react.go:336, :799) */
  :: d_step { ncalls < NCALL && rdp == 0 && !areq -> judge(tv1, tv1); ncalls++; clr() }
#endif
  :: ncalls >= NCALL && rdp == 0 && !areq -> break
  od;
  if
  :: d_step { agt = NONE; tL = 0; tkey = 0; tv1 = 0; agtpriv = 0; aupd() }
  :: goto endpark
  fi;
  goto done;
endpark: (1 == 0);
done:
  skip
}

proctype Rdr() {
  byte pp; byte ph;             /* the pointer a reader loaded and the handle that came with it (private) */
  do
  :: d_step { nrd < NRD -> pp = srv; ph = hsrv; nrd++ };
     d_step { resolve(pp, ph); ghostchk(pp, 2); SS(sS); clr(); pp = 0; ph = 0 }
  :: d_step { nrd < NRD -> pp = mgr; ph = hmgr; nrd++ };
     d_step { resolve(pp, ph); ghostchk(pp, 3); SS(sM); clr(); pp = 0; ph = 0 }
  :: nrd >= NRD -> break
  od
}

#if NMGR == 2
/* a test builds a second manager from the config object the first one already has */
proctype Test() {
  d_step {
#if MECH == 1
    csrc[0] = 2;
#elif MECH == 2
    gsrc = 2;
#elif MECH == 5 || MECH == 6
    lsrc = 2;
#endif
    m2 = 1; aupd() }
}
#endif

#if MECH == 4
/* a settings save serves mgr.Cfg() and writes it back: the keys of the overlay land in config.yaml */
proctype Saver() {
  d_step { !saved -> if :: cview[mgr] != 0 -> fnonop = 1 :: else -> skip fi; saved = 1 }
}
#endif

init {
  d_step {
    tr[0] = 1; tr[1] = 2;
    ckey[0] = 0; ncfg = 1;
    mgr = 0; srv = 0; agt = NONE; hmgr = 1; hsrv = 1; hagt = 0;
    /* NewManager(cfg): the manager attaches to the config it was handed, before anything reads it */
    csrc[0] = 1; cbind[0] = 2; clin[0] = 1; lsrc = 1; gsrc = 1;
    aupd()
  };
  run Remote(); run Refresher(); run Local(); run Store(); run Turn(); run Rdr()
#if NMGR == 2
  ; run Test()
#endif
#if MECH == 4
  ; run Saver()
#endif
}
