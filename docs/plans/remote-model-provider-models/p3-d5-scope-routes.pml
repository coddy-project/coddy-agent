/* p3-d5-scope-routes.pml - how a scoped client's routes are decided at the relay: a closed class with an exact route table,
   a prefix on /coddy/llm/, or per-entry route patterns the operator writes (phase 3, dilemma D5).

   Decides  dilemma D5 of docs/plans/remote-model-provider-phase3.md (section 10) and the places that depend on it:
            3.2 (the gate and the mount: `sharedRoute`, the closed table), 3.4, 11 (stage H2a).
   Code     external/swarm/mount.go (mountAllows, mountedPrefixes: "the node gains routes with every release", mountRemainder
            refuses `.` and `..` segments and an encoded slash, the decoded remainder is what the node routes), the node's
            http.ServeMux (external/httpserver/shared_models_http.go: GET /coddy/llm/models, GET /coddy/llm/models/{alias}/usage,
            POST /coddy/llm/completions; a HEAD request matches a GET pattern; `%63ompletions` reaches the completions route;
            a trailing slash and `;x=1` match no route).
   Class    finite, untimed, one adversary request at a time against a relay decision and a node routing function; one
            Registrar step adds a route r4 under /coddy/llm/ (a later release). No clock.

   World
     classes  the DECODED path of a request:  0 GET /coddy/llm/models      1 GET /coddy/llm/models/{alias}/usage
              2 POST /coddy/llm/completions   (the three routes of the scope)
              3 POST /coddy/llm/embeddings    (r4: a route a later release adds under /coddy/llm/; it exists on the node
                                               only once the Registrar has run)
              4 GET /coddy/config   5 GET /coddy/sessions   6 GET /coddy/llm-admin   (handlers outside the scope; 6 starts
                                               with the string `/coddy/llm` and not with `/coddy/llm/`)
     spelling 0 plain   1 encoded (a percent-encoded letter: decodes to the plain path, and the node routes it so)
              2 trailing slash (`.../completions/`: the node routes it nowhere)   3 `;x=1` suffix (the node routes it nowhere)
              4 a dot segment (`..`): the relay's mountRemainder refuses it for every class and every rule, so it is refused here
              5 HEAD (on a class whose route is a GET: the node routes it to the GET handler)
     node     route(class, spelling) = the handler that answers, or none (404 or 405)
     relay    decide(class, spelling) = admitted or refused, by the rule of the option

   Options (the dilemma)
     OPT 1   (a) a closed class: an EXACT table on the decoded remainder: the three routes with their methods, nothing else
     OPT 2   (b) a prefix: every decoded path that starts with `/coddy/llm/`, any method
     OPT 3   (c) per-entry route patterns the operator writes; PATV chooses what the operator wrote:
               PATV 0 the three routes spelled out                      PATV 1 the atom `/coddy/llm/*`
               PATV 2 a typo `/coddy/*`                                 PATV 3 `/coddy/llm*` (the slash left out)
     REG     0 no later release adds the route r4 (the control of the question: what does each rule admit TODAY)
     RAW     1 the rule compares the RAW spelling of the request instead of the decoded one (a defect of any option). Coarse by
             design: every encoded request is refused, which assumes the encoded letter lies inside the text the rule compares
             (a prefix or a wildcard rule would still match some encoded spellings); it is a control, not an option

   Properties (ghost latches, monotone)
     R1 gLeak    no admitted request is routed by the node to a handler outside the three routes of the scope, at any step
                 (r4 included once the Registrar has added it)
     R2 gLegit   a legitimate shared-model call (one of the three routes with its own method, plain or encoded) is admitted
     R3 gSpell   the decision is by the decoded path: a plain request and its encoded spelling are decided alike
     sanity      sSuffix (a path with a trailing slash or `;x=1` is admitted: reachable under a prefix or a wildcard, unreachable under
                 a rule that spells the routes out)  sLegit (a legitimate call is admitted)  sR4 (r4 exists and a request reaches it)  sAdmin (a request reaches
                 /coddy/config, /coddy/sessions or /coddy/llm-admin)  sHead (a HEAD request is admitted)
*/

#ifndef OPT
#define OPT 1
#endif
#ifndef PATV
#define PATV 0
#endif
#ifndef RAW
#define RAW 0
#endif
#ifndef REG
#define REG 1                               /* 0: the Registrar never runs, no route r4 is ever added */
#endif

byte r4;                                   /* 1 once a later release added the route r4 to the node */
bit gLeak, gLegit, gSpell;
bit sLegit, sR4, sAdmin, sHead, sSuffix;

byte t_cls, t_sp, t_meth, t_adm, t_adm0, t_adm1, t_h, t_legit, t_plain;   /* one request */

/* the HTTP method of a request: GET for the routes that are GET, POST for the others, HEAD for the spelling 5 */
inline methodOf() {
    if
    :: t_sp == 5 -> t_meth = 2           /* HEAD */
    :: t_cls == 2 || t_cls == 3 -> t_meth = 1   /* POST */
    :: else -> t_meth = 0                /* GET */
    fi
}

/* the handler the NODE routes the request to: 0 none, 1 models, 2 usage, 3 completions, 4 r4, 5 config, 6 sessions, 7 llm-admin */
inline nodeRoutes() {
    t_h = 0;
    if
    :: t_sp == 2 || t_sp == 3 -> t_h = 0                     /* a trailing slash and `;x=1` match no route */
    :: t_sp == 4 -> t_h = 0                                   /* never reaches the node: the relay refused it */
    :: else ->
         if
         :: t_cls == 0 && (t_meth == 0 || t_meth == 2) -> t_h = 1
         :: t_cls == 1 && (t_meth == 0 || t_meth == 2) -> t_h = 2
         :: t_cls == 2 && t_meth == 1 -> t_h = 3
         :: t_cls == 3 && t_meth == 1 && r4 == 1 -> t_h = 4
         :: t_cls == 4 && (t_meth == 0 || t_meth == 2) -> t_h = 5
         :: t_cls == 5 && (t_meth == 0 || t_meth == 2) -> t_h = 6
         :: t_cls == 6 && (t_meth == 0 || t_meth == 2) -> t_h = 7
         :: else -> t_h = 0
         fi
    fi
}

/* the decision of the relay for (class, spelling) by the option's rule, into t_adm */
inline relayDecides() {
    t_adm = 0;
    if
    :: t_sp == 4 -> t_adm = 0                                 /* mountRemainder refuses `.` and `..` for every rule */
    :: else ->
#if RAW == 1
         /* the raw spelling: an encoded request does not equal the literal text of a rule */
         if :: t_sp == 1 -> t_adm = 0 :: else -> skip fi;
         if
         :: t_sp == 1 -> skip
         :: else ->
#endif
#if OPT == 1
         /* (a) the exact table: the decoded path equals an entry and the method is the entry's */
         if
         :: t_sp == 2 || t_sp == 3 -> t_adm = 0      /* `.../completions/` and `...;x=1` equal no entry */
         :: else ->
              if
              :: t_cls == 0 && t_meth == 0 -> t_adm = 1
              :: t_cls == 1 && t_meth == 0 -> t_adm = 1
              :: t_cls == 2 && t_meth == 1 -> t_adm = 1
              :: else -> t_adm = 0
              fi
         fi
#elif OPT == 2
         /* (b) a prefix: the decoded path starts with /coddy/llm/ (any suffix, any method) */
         if
         :: t_cls <= 3 -> t_adm = 1
         :: else -> t_adm = 0
         fi
#else
         /* (c) the patterns the operator wrote, matched on the decoded path, any method */
#if PATV == 0
         if
         :: t_sp == 2 || t_sp == 3 -> t_adm = 0      /* the three routes spelled out equal no suffixed path */
         :: else -> if :: t_cls <= 2 -> t_adm = 1 :: else -> t_adm = 0 fi
         fi
#elif PATV == 1
         if :: t_cls <= 3 -> t_adm = 1 :: else -> t_adm = 0 fi
#elif PATV == 2
         t_adm = 1
#else
         if :: t_cls <= 3 || t_cls == 6 -> t_adm = 1 :: else -> t_adm = 0 fi
#endif
#endif
#if RAW == 1
         fi
#endif
    fi
}

proctype Client() {
    do
    :: atomic {
         if :: t_cls = 0 :: t_cls = 1 :: t_cls = 2 :: t_cls = 3 :: t_cls = 4 :: t_cls = 5 :: t_cls = 6 fi;
         if :: t_sp = 0 :: t_sp = 1 :: t_sp = 2 :: t_sp = 3 :: t_sp = 4 :: t_sp = 5 fi;
         /* a HEAD request exists only for a route that is a GET */
         if :: t_sp == 5 && (t_cls == 2 || t_cls == 3) -> t_sp = 0 :: else -> skip fi;
         methodOf();
         relayDecides();
         nodeRoutes();
         t_legit = 0;
         if :: t_cls <= 2 && (t_sp == 0 || t_sp == 1) -> t_legit = 1 :: else -> skip fi;
         /* R1: admitted and routed outside the three */
         if :: t_adm == 1 && t_h >= 4 -> gLeak = 1 :: else -> skip fi;
         /* R2 */
         if :: t_legit == 1 && t_adm == 0 -> gLegit = 1 :: else -> skip fi;
         /* R3: an encoded request and its plain twin are decided alike */
         if
         :: t_sp == 1 ->
              t_adm1 = t_adm;
              t_sp = 0; relayDecides(); t_adm0 = t_adm; t_sp = 1;
              if :: t_adm0 != t_adm1 -> gSpell = 1 :: else -> skip fi;
              t_adm = t_adm1
         :: else -> skip
         fi;
         /* sanity */
         if :: t_legit == 1 && t_adm == 1 -> sLegit = 1 :: else -> skip fi;
         if :: t_adm == 1 && t_h == 4 -> sR4 = 1 :: else -> skip fi;
         if :: t_adm == 1 && t_h >= 5 -> sAdmin = 1 :: else -> skip fi;
         if :: t_adm == 1 && t_sp == 5 -> sHead = 1 :: else -> skip fi;
         /* a path with a trailing slash or `;x=1` is admitted: never by a rule that spells the routes out (OPT 1, PATV 0) */
         if :: t_adm == 1 && (t_sp == 2 || t_sp == 3) -> sSuffix = 1 :: else -> skip fi;
#ifdef ASSERT_PROP
         assert(ASSERT_PROP == 0);
#endif
         t_cls = 0; t_sp = 0; t_meth = 0; t_adm = 0; t_adm0 = 0; t_adm1 = 0; t_h = 0; t_legit = 0
       }
    od
}

proctype Registrar() {
    atomic { r4 = 1 }
}

init {
    atomic {
        run Client();
#if REG == 1
        run Registrar()
#endif
    }
}
