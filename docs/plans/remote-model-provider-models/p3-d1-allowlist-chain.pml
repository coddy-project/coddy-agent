/* p3-d1-allowlist-chain.pml - what a per-client NODE ALLOWLIST means when relays chain (phase 3, dilemma D1).

   Decides  dilemma D1 of docs/plans/remote-model-provider-phase3.md (section 10) and the places that depend
            on it: 3.2 (the mount: `entry.admits(hops)`), 3.4 (edge cases), 11 (stage H2a notes).
   Code     external/swarm/mount.go (handleMount: the escaped remainder, the hop bound swarmMaxHops = 4, the
            decoded walk of isSharedCompletions, rewriteFor: the node's token REPLACES the credential),
            external/swarm/server.go (authGate: class-blind today), external/swarm/registry.go (Node(name) is an
            exact, case-sensitive map lookup), internal/swarm/joinset.go:72-74 (a child relay that sets no
            swarm.join[].token presents its FULL swarm.auth_token to its parent), internal/swarm/names.go (node
            names: [A-Za-z0-9_-]{1,64}), internal/config/swarm.go (SwarmMaxHops = 4).
   Class    finite, untimed, interleaving. NO CLOCK is needed: every property is about WHICH requests a rule
            admits and WHAT they reach, not about when. Lease expiry and node registration are abstracted by a
            Registrar that may add one node at any moment (a superset of every timing). Nothing here is a timed
            automaton.

   World (every domain is tiny on purpose)
     relays   R1 (the relay that holds the scoped client, the ONLY relay that sees the client's class), R2, R3.
     names    N1=1 child=2 M1=3 M2=4 N1X=5 (N1X only so that "n1" is a string prefix of "n1x").
     nodes    a node table per relay, slot = relay*8 + name, kind 1 = leaf (an agent), 2 = relay:
                R1: n1 (leaf 0)   child -> R2   m1 (leaf 1)
                R2: n1 (leaf 2)   m2 (leaf 3)   n1x (leaf 4)   child -> R3
                R3: m1 (leaf 5)   child -> R2        <- a RING R2 <-> R3, which a hop bound of 4 still lets walk
              so a name repeats on different relays (n1 on R1/R2, m1 on R1/R3, child on every relay) and a leaf
              is reachable by more than one chain (leaf 3 by [child,m2] and by [child,child,child,m2]).
              DYN (Registrar): at any moment ONE free slot may be taken by a new leaf: R1.m2 (leaf 6, the name
              M2 is already a leaf name behind child) or R2.m1 (leaf 7).
     chain    a request names k in 1..4 hops (a hop is a node name), then a route class. 4 hops is the bound
              swarmMaxHops of mount.go; an entry of more than SwarmMaxHops segments is a config error.
     route    rt 0 = LLM: the three shared-model routes (GET /coddy/llm/models, GET .../{alias}/usage, POST
              .../completions) merged into ONE class, which is lossless for D1: the closed table of 3.2 admits all
              three or none, independently of the hops (D5 decides the table itself). rt 1 = ANY OTHER route a mount
              carries today (/v1/, /coddy/sessions..., /swarm/nodes of the child relay, nested mounts). rt 2 =
              control plane (/swarm/register, /swarm/tunnel: refused by mountAllows for EVERY class).
     spelling a hop is judged on the DECODED remainder (3.2); the model computes the decision for the plain AND the
              encoded spelling of the same request and flags a difference (gSpell). Fact from the code: the relay
              that receives an encoded hop (an encoded node name or an encoded "swarm/nodes" prefix) refuses it in
              mountRemainder (it demands the literal prefix), so a spelling can only be the ROUTE's and only the
              parent's decoded walk matters.
     principal PR=0 scoped client only, PR=1 full-access client only, PR=2 both (chosen per request).
     forward  the first relay's gate decides; the mount forwards with the NEXT relay's FULL token (joinset.go:72), so
              from hop 2 on a request is judged by the legacy rule (route != control plane): the scope holds only if
              relay R1 judged the WHOLE chain. The model resolves the chain through the tables: privilege = the
              set of (leaf, LLM route) pairs reached (rL), plus gRoute for any non-LLM route that reaches a node.

   Intent. The operator's intent is a set I of leaf instances (bit i of INTENT = leaf i, i = 0..5; INTENT=64 means
     "every subset, chosen at start", 64 intents). The allowlist is the TIGHTEST WRITING of I in the grammar of the
     option, so the question is whether the grammar can say I and nothing else:
       OPT=1 (a) first hop only     the set of FIRST names of the paths of I; the child's forwarding is trusted
       OPT=2 (b) exact hop paths    the set of the paths of I; a bare `a` is the single-hop path [a]; `*` = any
                                    single-hop path (STAR=1: the allowlist is `*` and I = every leaf directly below R1)
       OPT=3 (c) flat set of names  every name along every path of I; a chain is admitted iff EVERY hop name is in it
       OPT=4 (d) no chains          like (b) restricted to one hop (a scoped client never crosses a relay)
     Mutants of (b) (MUT, OPT=2): 1 skip2  a chain is judged by its FIRST hop only and the route table is not
                                   applied to it (what a nested mount prefix gives today: the child sees a full token)
                                  2 tail   an entry is a path PREFIX (a longer chain with the entry as prefix passes)
                                  3 lastprefix  the last segment is compared as a STRING PREFIX ("n1" admits "n1x")
                                  4 raw    the allowlist is compared on the raw spelling (an encoded request fails)
                                  5 fullscoped  the allowlist is also applied to the full-access token

   Properties (ghost latches, monotone)
     P1 gRoute     no admitted scoped request carries a non-LLM route to a node, at ANY hop
        gRouteGate (stricter, gate level) no admitted scoped request carries a non-LLM route at all
     P2 gOut       no admitted scoped request reaches a leaf outside I with an LLM route
     P3 gColl      P2 restricted to leaves that share a NAME with a leaf of I (a name collision never widens)
     P4 gTrav      every relay a request crosses is on the path of some leaf of I (the scope holds at every hop)
     P5 gDup       a leaf of I is reached only by its written chain (no scope check is skipped by naming the same
                   node through another chain; the ring makes this reachable)
     P6 gExact     the decision on the LLM route is EXACTLY "chain in the written set", for both spellings: nothing else
                   is admitted (harmless or not) and nothing written is refused
     P7 gSpell     the decision does not depend on the spelling
     P8 gFull      the full-access class is judged by the legacy rule (route != control plane) and by nothing else
     L1 cov        AG EF cov: from every state, every leaf of I (that exists) can still be reached with an LLM route
     sanity        sLLM sDeep sDyn sFull gDead (reachable), plus the model's own deadlock and assert
*/

#ifndef OPT
#define OPT 2
#endif
#ifndef MUT
#define MUT 0
#endif
#ifndef INTENT
#define INTENT 64
#endif
#ifndef STAR
#define STAR 0
#endif
#ifndef DYN
#define DYN 1
#endif
#ifndef PR
#define PR 0
#endif

#define N1 1
#define CHILD 2
#define M1 3
#define M2 4
#define N1X 5

byte kind[32];      /* slot relay*8+name: 0 absent, 1 leaf, 2 relay                                  */
byte tgt[32];       /* leaf id, or the relay a relay-node leads to                                  */
byte pl[8];         /* canonical path length of leaf i (from R1)                                    */
byte pp[24];        /* canonical path of leaf i: pp[i*3+j]                                          */
byte ln[8];         /* the name of leaf i (the last segment of its path)                             */
bit inI[8];         /* the intent, over leaf ids; 6 and 7 are the dynamic leaves                     */
bit ent[8];         /* (b): the written entries = the paths of I                                     */
bit nm1[8];         /* (a): the written first names                                                   */
bit nm3[8];         /* (c): the written flat set of names                                             */
bit starE;          /* the entry `*`                                                                 */
bit licA, licB;     /* relay slots 10 (R1.child) and 18 (R2.child) lie on a path of I                */
bit rL[8];          /* privilege: leaf i reached with an LLM route by a scoped request               */
bit cov;
bit gRoute, gRouteGate, gOut, gColl, gTrav, gDup, gExact, gSpell, gFull, gDead;
bit sLLM, sDeep, sDyn, sFull;

byte h[4];          /* the chain being judged (names), cleared at the end of every step              */
/* temporaries, all cleared at the end of a step so that only the stable part is in the state */
byte t_k, t_j, t_i, t_n, t_rt, t_cur, t_s, t_fk, t_fl, t_idx, t_pr;
bit t_dead, t_trA, t_trB, t_trC, t_sa, t_m, t_a0, t_a1, t_adm, t_leg, t_inW, t_same, t_pres;
bit t_full;

inline setupTopology() {
    kind[1*8+N1] = 1;    tgt[1*8+N1] = 0;
    kind[1*8+CHILD] = 2; tgt[1*8+CHILD] = 2;
    kind[1*8+M1] = 1;    tgt[1*8+M1] = 1;
    kind[2*8+N1] = 1;    tgt[2*8+N1] = 2;
    kind[2*8+M2] = 1;    tgt[2*8+M2] = 3;
    kind[2*8+N1X] = 1;   tgt[2*8+N1X] = 4;
    kind[2*8+CHILD] = 2; tgt[2*8+CHILD] = 3;
    kind[3*8+M1] = 1;    tgt[3*8+M1] = 5;
    kind[3*8+CHILD] = 2; tgt[3*8+CHILD] = 2;
    /* canonical (shortest) chain of every leaf, measured from R1 */
    pl[0] = 1; pp[0*3+0] = N1;                                   ln[0] = N1;
    pl[1] = 1; pp[1*3+0] = M1;                                   ln[1] = M1;
    pl[2] = 2; pp[2*3+0] = CHILD; pp[2*3+1] = N1;                ln[2] = N1;
    pl[3] = 2; pp[3*3+0] = CHILD; pp[3*3+1] = M2;                ln[3] = M2;
    pl[4] = 2; pp[4*3+0] = CHILD; pp[4*3+1] = N1X;               ln[4] = N1X;
    pl[5] = 3; pp[5*3+0] = CHILD; pp[5*3+1] = CHILD; pp[5*3+2] = M1; ln[5] = M1;
    pl[6] = 1; pp[6*3+0] = M2;                                   ln[6] = M2;      /* R1.m2, dynamic */
    pl[7] = 2; pp[7*3+0] = CHILD; pp[7*3+1] = M1;                ln[7] = M1;      /* R2.m1, dynamic */
}

/* derive the written allowlist of the option from the intent */
inline deriveAllowlist() {
    t_i = 0;
    do
    :: t_i < 8 ->
         if
         :: inI[t_i] == 1 && t_i <= 5 ->
              ent[t_i] = 1;
              nm1[pp[t_i*3 + 0]] = 1;
              nm3[pp[t_i*3 + 0]] = 1;
              if :: pl[t_i] >= 2 -> nm3[pp[t_i*3 + 1]] = 1 :: else -> skip fi;
              if :: pl[t_i] >= 3 -> nm3[pp[t_i*3 + 2]] = 1 :: else -> skip fi
         :: else -> skip
         fi;
         t_i++
    :: else -> break
    od;
    licA = 0; licB = 0;
    if :: inI[2] == 1 || inI[3] == 1 || inI[4] == 1 || inI[5] == 1 -> licA = 1 :: else -> skip fi;
    if :: inI[5] == 1 -> licB = 1 :: else -> skip fi;
#if STAR
    /* the allowlist is the single entry `*`; I is every leaf directly below R1 */
    t_i = 0;
    do :: t_i < 8 -> ent[t_i] = 0; t_i++ :: else -> break od;
    starE = 1;
    t_i = 1;
    do :: t_i <= 5 -> nm1[t_i] = 1; nm3[t_i] = 1; t_i++ :: else -> break od;
    licA = 0; licB = 0;
#endif
    t_i = 0
}

/* L1's atom: every leaf of I that exists is reached */
inline recompute() {
    cov = 1;
    t_i = 0;
    do
    :: t_i < 8 ->
         t_pres = 1;
         if :: t_i == 6 && kind[1*8+M2] == 0 -> t_pres = 0 :: else -> skip fi;
         if :: t_i == 7 && kind[2*8+M1] == 0 -> t_pres = 0 :: else -> skip fi;
         if :: inI[t_i] == 1 && rL[t_i] == 0 && t_pres == 1 -> cov = 0 :: else -> skip fi;
         t_i++
    :: else -> break
    od;
    t_i = 0; t_pres = 0
}

inline chooseChain() {
    if :: t_k = 1 :: t_k = 2 :: t_k = 3 :: t_k = 4 fi;
    t_j = 0;
    do
    :: t_j < t_k ->
         if :: h[t_j] = 1 :: h[t_j] = 2 :: h[t_j] = 3 :: h[t_j] = 4 :: h[t_j] = 5 fi;
         t_j++
    :: else -> break
    od;
    t_j = 0
}

/* t_sa: the allowlist of the option admits the chain h[0..t_k-1] (the route is judged separately) */
inline scopedAdmit() {
    t_sa = 0;
#if OPT == 1
    t_n = h[0];
    t_sa = nm1[t_n];
#elif OPT == 3
    t_sa = 1;
    t_j = 0;
    do
    :: t_j < t_k ->
         t_n = h[t_j];
         if :: nm3[t_n] == 0 -> t_sa = 0 :: else -> skip fi;
         t_j++
    :: else -> break
    od;
    t_j = 0;
#else
    /* OPT 2 (exact paths) and OPT 4 (exact paths, one hop only) */
    /* `*` is the one-hop entry in both: OPT 4 differs from OPT 2 only in the entries it takes, below */
    if :: t_k == 1 && starE == 1 -> t_sa = 1 :: else -> skip fi;
    t_i = 0;
    do
    :: t_i < 8 ->
         if
         :: ent[t_i] == 1 && (OPT != 4 || pl[t_i] == 1) ->
              /* does entry t_i match the chain ? */
              t_m = 1;
              t_j = 0;
              do
              :: t_j < pl[t_i] ->
                   t_idx = t_i*3 + t_j;
                   if
                   :: pp[t_idx] == h[t_j] -> skip
                   :: else ->
                        #if MUT == 3
                        if
                        :: t_j == pl[t_i] - 1 && t_k == pl[t_i] && pp[t_idx] == N1 && h[t_j] == N1X -> skip
                        :: else -> t_m = 0
                        fi
                        #else
                        t_m = 0
                        #endif
                   fi;
                   t_j++
              :: else -> break
              od;
              #if MUT == 2
              if :: t_k < pl[t_i] -> t_m = 0 :: else -> skip fi;
              #else
              if :: t_k != pl[t_i] -> t_m = 0 :: else -> skip fi;
              #endif
              if :: t_m == 1 -> t_sa = 1 :: else -> skip fi
         :: else -> skip
         fi;
         t_i++
    :: else -> break
    od;
    t_i = 0; t_j = 0; t_m = 0;
    #if MUT == 1
    /* skip2: a chain is judged by its first hop only (some entry starts with that name) */
    if
    :: t_k >= 2 ->
         t_n = h[0];
         t_sa = nm1[t_n]
    :: else -> skip
    fi;
    #endif
#endif
    t_n = 0
}

/* the ideal membership of the chain in the written set (the contract of the option (b); under OPT 4 the
   written set holds the one-hop entries only, since a scoped client never crosses a relay there) */
inline writtenSet() {
    t_inW = 0;
    if :: t_k == 1 && starE == 1 -> t_inW = 1 :: else -> skip fi;
    t_i = 0;
    do
    :: t_i < 8 ->
         if
         :: ent[t_i] == 1 && pl[t_i] == t_k && (OPT != 4 || pl[t_i] == 1) ->
              t_m = 1;
              t_j = 0;
              do
              :: t_j < t_k ->
                   t_idx = t_i*3 + t_j;
                   if :: pp[t_idx] != h[t_j] -> t_m = 0 :: else -> skip fi;
                   t_j++
              :: else -> break
              od;
              if :: t_m == 1 -> t_inW = 1 :: else -> skip fi
         :: else -> skip
         fi;
         t_i++
    :: else -> break
    od;
    t_i = 0; t_j = 0; t_m = 0; t_idx = 0
}

/* the walk of the chain through the node tables: where does the request end ? */
inline resolve() {
    t_cur = 1; t_j = 0; t_dead = 0; t_fk = 0; t_fl = 0; t_trA = 0; t_trB = 0; t_trC = 0;
    do
    :: t_j < t_k && t_dead == 0 ->
         t_s = t_cur*8 + h[t_j];
         if
         :: kind[t_s] == 0 -> t_dead = 1
         :: kind[t_s] == 1 ->
              if
              :: t_j == t_k - 1 -> t_fk = 1; t_fl = tgt[t_s]
              :: else -> t_dead = 1
              fi
         :: kind[t_s] == 2 ->
              if
              :: t_s == 10 -> t_trA = 1
              :: t_s == 18 -> t_trB = 1
              :: t_s == 26 -> t_trC = 1
              :: else -> skip
              fi;
              if
              :: t_j == t_k - 1 -> t_fk = 2
              :: else -> t_cur = tgt[t_s]
              fi
         fi;
         t_j++
    :: else -> break
    od;
    t_j = 0; t_s = 0; t_cur = 0
}

inline ghostsScoped() {
    if :: t_rt != 0 -> gRouteGate = 1 :: else -> skip fi;
    if :: t_rt != 0 && t_dead == 0 -> gRoute = 1 :: else -> skip fi;
    if
    :: (t_trA == 1 && licA == 0) || (t_trB == 1 && licB == 0) || t_trC == 1 -> gTrav = 1
    :: else -> skip
    fi;
    if :: t_dead == 1 -> gDead = 1 :: else -> skip fi;
    if :: t_inW == 0 -> gExact = 1 :: else -> skip fi;
    if
    :: t_rt == 0 && t_fk == 1 ->
         if
         :: inI[t_fl] == 0 ->
              gOut = 1;
              t_i = 0;
              do
              :: t_i < 8 ->
                   if :: inI[t_i] == 1 && ln[t_i] == ln[t_fl] -> gColl = 1 :: else -> skip fi;
                   t_i++
              :: else -> break
              od;
              t_i = 0
         :: else ->
              /* a leaf of I: is the chain the one that was written ? */
              t_same = 1;
              if :: t_k != pl[t_fl] -> t_same = 0 :: else -> skip fi;
              t_j = 0;
              do
              :: t_j < t_k && t_j < 3 ->
                   t_idx = t_fl*3 + t_j;
                   if :: pp[t_idx] != h[t_j] -> t_same = 0 :: else -> skip fi;
                   t_j++
              :: else -> break
              od;
              if :: t_same == 0 -> gDup = 1 :: else -> skip fi;
              t_j = 0; t_idx = 0; t_same = 0
         fi;
         rL[t_fl] = 1;
         sLLM = 1;
         if :: pl[t_fl] >= 2 -> sDeep = 1 :: else -> skip fi
    :: else -> skip
    fi
}

inline clearStep() {
    h[0] = 0; h[1] = 0; h[2] = 0; h[3] = 0;
    t_k = 0; t_j = 0; t_i = 0; t_n = 0; t_rt = 0; t_cur = 0; t_s = 0; t_fk = 0; t_fl = 0; t_idx = 0; t_pr = 0;
    t_dead = 0; t_trA = 0; t_trB = 0; t_trC = 0; t_sa = 0; t_m = 0; t_a0 = 0; t_a1 = 0; t_adm = 0; t_leg = 0;
    t_inW = 0; t_same = 0; t_pres = 0; t_full = 0
}

proctype Client() {
    do
    :: atomic {
         /* the principal of this request */
#if PR == 0
         t_full = 0;
#elif PR == 1
         t_full = 1;
#else
         if :: t_full = 0 :: t_full = 1 fi;
#endif
         chooseChain();
         if
         :: t_full == 0 -> if :: t_rt = 0 :: t_rt = 1 fi
         :: else -> if :: t_rt = 0 :: t_rt = 1 :: t_rt = 2 fi
         fi;
         scopedAdmit();
         writtenSet();
         /* the decision of a SCOPED request: the allowlist, then the closed route table, for the plain and
            for the encoded spelling of the same request */
         t_a0 = 0;
         if
         :: t_sa == 1 && t_rt == 0 -> t_a0 = 1
#if MUT == 1
         :: t_sa == 1 && t_k >= 2 -> t_a0 = 1
#endif
         :: else -> skip
         fi;
         t_a1 = t_a0;
#if MUT == 4
         t_a1 = 0;
#endif
         /* the decision of a SCOPED request must not depend on the spelling, and on the LLM route it must be
            exactly "the chain is in the written set": a refusal of a written chain (under-admission) is as
            wrong as an admission of an unwritten one (over-admission, set in ghostsScoped) */
         if :: t_full == 0 && t_a0 != t_a1 -> gSpell = 1 :: else -> skip fi;
         if :: t_full == 0 && t_rt == 0 && (t_a0 != t_inW || t_a1 != t_inW) -> gExact = 1 :: else -> skip fi;
         resolve();
         if
         :: t_full == 0 ->
              t_adm = 0;
              if :: t_a0 == 1 || t_a1 == 1 -> t_adm = 1 :: else -> skip fi;
              if :: t_adm == 1 -> ghostsScoped() :: else -> skip fi
         :: else ->
              /* the full-access class: the legacy rule (mountAllows) = every route but the control plane */
              if :: t_rt != 2 -> t_leg = 1 :: else -> t_leg = 0 fi;
              t_adm = t_leg;
#if MUT == 5
              t_adm = t_a0;
#endif
              if :: t_adm != t_leg -> gFull = 1 :: else -> skip fi;
              if :: t_adm == 1 -> sFull = 1 :: else -> skip fi
         fi;
         recompute();
#ifdef ASSERT_PROP
         /* one property per run, as a state assertion: -D ASSERT_PROP=gOut stops the search at the first state
            where the latch is set (a counterexample at once) and is exhaustive when it never is; a SANITY latch
            (sLLM, sDeep, sDyn, sFull, gDead) is "violated" by design, which says it is reachable */
         assert(ASSERT_PROP == 0);
#endif
         clearStep()
       }
    od
}

proctype Registrar() {
    atomic {
        if
        :: kind[1*8+M2] = 1; tgt[1*8+M2] = 6
        :: kind[2*8+M1] = 1; tgt[2*8+M1] = 7
        fi;
        sDyn = 1;
        recompute()
    }
}

init {
    atomic { setupTopology() };
#if INTENT >= 64
    if :: inI[0] = 0 :: inI[0] = 1 fi;
    if :: inI[1] = 0 :: inI[1] = 1 fi;
    if :: inI[2] = 0 :: inI[2] = 1 fi;
    if :: inI[3] = 0 :: inI[3] = 1 fi;
    if :: inI[4] = 0 :: inI[4] = 1 fi;
    if :: inI[5] = 0 :: inI[5] = 1 fi;
#else
    inI[0] = (INTENT / 1) % 2;
    inI[1] = (INTENT / 2) % 2;
    inI[2] = (INTENT / 4) % 2;
    inI[3] = (INTENT / 8) % 2;
    inI[4] = (INTENT / 16) % 2;
    inI[5] = (INTENT / 32) % 2;
#endif
#if STAR
    inI[0] = 1; inI[1] = 1; inI[2] = 0; inI[3] = 0; inI[4] = 0; inI[5] = 0; inI[6] = 1;
#endif
    atomic { deriveAllowlist(); recompute() };
    run Client();
#if DYN
    run Registrar();
#endif
}
