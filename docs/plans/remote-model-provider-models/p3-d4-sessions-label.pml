/* p3-d4-sessions-label.pml - what the node label `coddy.token_class: shared_models` does to the relay's aggregated sessions list:
   skip a labelled node without asking, or still ask and swallow only a 401 (phase 3, dilemma D4).

   Decides  dilemma D4 of docs/plans/remote-model-provider-phase3.md (section 10) and the places that depend on it: 7 (the label),
            11 (stage H3).
   Code     external/swarm/sessions.go (handleSessions, fanOut, askNode: every online node is asked for its sessions with the
            node's token; any non-200 answer is a warning `<node>: <status>`; an unreachable node warns with the transport error),
            internal/swarm/labels.go (DerivedLabels: the label follows the token the node actually sends; SharedModelsOnly: the
            exact key and value, agents only), external/swarm/registry.go (labels are the node's own claim and are replaced at every
            renewal).
   Class    finite, untimed: one aggregated call over three nodes, each described by what it answers and whether it carries the label.

   World
     answer   what a node's sessions route answers to the relay: 0 rows (a node whose token the relay holds is a main one, or a node
              that is open), 1 a 401 (a node that joined with a shared-model token, a node with authentication and an empty token, a
              stale or rotated token), 2 a server error, 3 the node is unreachable (a transport error)
     label    1 when the relay treats the node as labelled, i.e. `SharedModelsOnly`: an AGENT whose registration carries the exact key
              and the exact value (a relay, a padded value or a case variant is "not labelled"), 0 otherwise. A CURRENT node sets it exactly when its token is a
              shared-model token (so a label with answer 0 is a mislabel: a hand-written value, a label on an older node, or a token
              that changed between two renewals), an older node carries none (label 0 with answer 1 is a node the label was meant to
              silence and could not).
     offline  a node whose lease expired is not asked in either mode: the relay warns `offline since ...` before the fan-out and
              whatever the label says, so it is outside the comparison of the two modes and is not modelled.
     relay    MODE 1 (a): a labelled node is not asked, it produces no row and no warning
              MODE 2 (b): every node is asked; a labelled node's 401 is swallowed (no warning), everything else is as for any node

   Properties (ghost latches, monotone)
     V1 gV1   every node whose sessions a full client could read without the label (answer 0) is listed
     V2 gV2   every genuine warning of a node without the label still appears: answers 1, 2 and 3 of an unlabelled node warn
     V3 gV3   a labelled node whose token opens only the shared-model routes (label 1, answer 1) never warns
     V4 gV4   (cost) the relay makes no request to a labelled node whose answer is a 401 (the node's log stays quiet)
     V5 gV5   the label silences the refusal and nothing else: a labelled node that answers a server error, or is unreachable, warns
     sanity   sListed sWarned sSilenced sAsked  (V2 and V3 hold in both modes, because both keep every warning of an unlabelled node and
              neither warns for a labelled 401; sWarned and sSilenced show that the cases they guard are reachable, so they are
              regression guards for a change of the relay's code)
*/

#ifndef MODE
#define MODE 2
#endif

byte ans[3];       /* 0 rows, 1 401, 2 server error, 3 unreachable */
byte lab[3];       /* 1 labelled */
byte listed[3];    /* 1 the node's rows are in the list    */
byte warned[3];    /* 1 a warning names the node            */
byte asked[3];     /* 1 the relay made a request            */
bit gV1, gV2, gV3, gV4, gV5;
bit sListed, sWarned, sSilenced, sAsked;

inline relayAsks(i) {
    if
    :: MODE == 1 && lab[i] == 1 ->
         /* (a) skipped: no request, no row, no warning */
         skip
    :: else ->
         asked[i] = 1;
         if
         :: ans[i] == 0 -> listed[i] = 1
         :: ans[i] == 1 ->
              if
              :: MODE == 2 && lab[i] == 1 -> skip     /* (b) the refusal is swallowed for a labelled node */
              :: else -> warned[i] = 1
              fi
         :: else -> warned[i] = 1                     /* a server error and a transport error warn as for every node */
         fi
    fi
}

inline checks(i) {
    if :: ans[i] == 0 && listed[i] == 0 -> gV1 = 1 :: else -> skip fi;
    if :: lab[i] == 0 && ans[i] != 0 && warned[i] == 0 -> gV2 = 1 :: else -> skip fi;
    if :: lab[i] == 1 && ans[i] == 1 && warned[i] == 1 -> gV3 = 1 :: else -> skip fi;
    if :: lab[i] == 1 && ans[i] == 1 && asked[i] == 1 -> gV4 = 1 :: else -> skip fi;
    if :: lab[i] == 1 && (ans[i] == 2 || ans[i] == 3) && warned[i] == 0 -> gV5 = 1 :: else -> skip fi;
    if :: listed[i] == 1 -> sListed = 1 :: else -> skip fi;
    if :: warned[i] == 1 -> sWarned = 1 :: else -> skip fi;
    if :: lab[i] == 1 && ans[i] == 1 && warned[i] == 0 -> sSilenced = 1 :: else -> skip fi;
    if :: asked[i] == 1 -> sAsked = 1 :: else -> skip fi
}

init {
    atomic {
        /* every combination of three nodes */
        if :: ans[0] = 0 :: ans[0] = 1 :: ans[0] = 2 :: ans[0] = 3 fi;
        if :: ans[1] = 0 :: ans[1] = 1 :: ans[1] = 2 :: ans[1] = 3 fi;
        if :: ans[2] = 0 :: ans[2] = 1 :: ans[2] = 2 :: ans[2] = 3 fi;
        if :: lab[0] = 0 :: lab[0] = 1 fi;
        if :: lab[1] = 0 :: lab[1] = 1 fi;
        if :: lab[2] = 0 :: lab[2] = 1 fi;
        relayAsks(0); relayAsks(1); relayAsks(2);
        checks(0); checks(1); checks(2);
#ifdef ASSERT_PROP
        assert(ASSERT_PROP == 0)
#endif
    }
}
