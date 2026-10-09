/* p7-gate.pml - the node's gate after phase 4b: the credentials that remain, against every route, with TLS reduced to a handshake. It
   supersedes p5-gate.pml (and p3-d2-mtls-identity.pml), whose certificate class (`cert_names`, the class `mtls`, `client_auth`) was
   removed: TLS is the transport's business, and Coddy reads no identity out of a certificate (docs/plans/remote-model-provider-tls-builtin.md).

   Code     external/httpserver/auth.go (authGate: not gateOn or not a protected route passes through; the main bearer; the shared bearer
            on a shared route; a signed-in cookie; else 401; the gate and everything it calls read nothing of r.TLS: the only reader in the package is requestIsTLS, called by sessionCookie for the Secure flag of the sign-in cookie), tls.go (listenerTLS: a client CA makes the
            handshake REQUIRE a verified certificate, none is asked for without one), internal/netx/servercert.go (ClientCertTLS:
            RequireAndVerifyClientCert, nothing else is read), shared_models_http.go (the handlers: anonymousSharedRefused is 403),
            authPolicyNow (gateOn = a main token, a login or a shared token; a token that is both main and shared is read as shared).
   Class    finite, no time: a decision procedure explored over every combination of its inputs (the nondeterministic choices of init).
            The decision is an ordered chain, first match wins, as the code is (each step runs only while the result is still a refusal).

   World (one request)
     bearer  0 none, 1 the main token, 2 a shared-model token, 3 a token that is configured as both (read as shared), 4 a wrong token
     cert    0 none, 1 a certificate that verifies against the listener's client CA, 2 a certificate that does not (another authority,
             expired at the handshake, no clientAuth usage)
     cookie  0 none, 1 a live browser session: the sign-in form's, or a Telegram admin's Mini App session
     mode    0 no client CA (the listener asks for nothing: a certificate is not even presented), 2 a client CA (the handshake requires one)
     route   0 listing, 1 usage, 2 completions, 3 ping (the four shared routes), 4 counters (stats), 5 another protected route, 6 a public route
     config  which credentials the node has: HMAIN, HSHARED, HLOGIN and ALLOWINSECURE

   Result  0 served, 1 refused 401, 2 refused 403, 3 no connection (a handshake failure)
   Class that opened the request  0 none (public or open node), 1 main, 2 shared, 4 cookie   (3 was the certificate: it no longer exists)

   Truth and reading. shared_route is the truth (the four shared routes); gshared is what the gate's route test (isSharedLLMPattern) says,
   and a mutant moves it. credNode is the truth of the node's configuration; gateOn is what authPolicyNow computes. The properties judge by
   the truth, never by the reading the decision uses.

   The latch of the stage. G15 gCertDecides: past the handshake, the decision (result and class) for one request is the same whatever
   certificate it came with, and whether a client CA was asked for or not. The model decides the request four times over the same bearer,
   cookie, route and configuration - (A) on a listener with no client CA and no certificate, (B) on one with a client CA and a verified
   certificate, (C) on one with a client CA and a certificate that does not verify, and (D) on one with a client CA and a request that
   arrived with no handshake at all (the swarm reverse tunnel or a TLS terminator delivers such requests) - and compares what reached the
   gate with A: no input makes a certificate change an authorization decision. A mutant that gives a certificate any weight (CERTOPENS,
   CERTMAIN, CERTVETO together with UNVERIFIEDOK, REQTLS) must violate it.
   G17 gOracle is the independent check: the whole decision written as a table of cases.

   Options (the code is the default of each; a mutant changes one rule)
     CERTOPENS 0 (1: a verified certificate opens the shared routes, as phase 3's `cert_names` did, a mutant)
     CERTMAIN  0 (1: a verified certificate opens every route, a mutant)
     CERTVETO  0 (1: a certificate that does not verify, once the handshake let it through, refuses the request: a mutant)
     MAINDROP 0 the main bearer opens (1: it opens nothing)
     REFUSAL403 0 the final refusal is a 401 (1: a 403)
     COOKIEFIRST 0 the cookie is judged after the main bearer (1: before it)
     PUBLICGATED 0 a public route is open on a node that has a credential (1: it is behind the gate)
     REQTLS 0 a request with no certificate under a client CA, past the handshake, is judged like any other (1: it is refused)
     GATESHARED 0 a shared token turns the gate on (1: it does not: authPolicyNow forgot it)
     SHAREDALL 0 a shared token opens only the shared routes (1: every route)
     DUPMAIN 0 a token configured as both classes is the shared one (1: the main one)
     NOHANDSHAKE 0 a peer with no verified certificate under a client CA is refused (1: let in)
     UNVERIFIEDOK 0 a certificate that does not verify fails the handshake (1: the listener asks for a certificate without verifying it)
     ANONSHARED 0 the shared handlers refuse an anonymous caller of a node with no credential and no allow_insecure (1: they serve it)
     DUPDROP 0 a token configured as both classes is accepted on the shared routes (1: it is not)
     COOKIENOSHARED 0 a live browser session opens every protected route (1: not the shared ones)
     INSECUREREFUSED 0 a node with no credential and allow_insecure serves the shared routes (1: it refuses them)
     WRONGOK 0 a wrong token is refused (1: it is read as the main token)
     SHAREDSTATS 0 the gate's route test says the four shared routes (1: it also says the counters route)
     DROPPING 0 (1: the route test forgets the ping)
     DROPUSAGE 0 (1: the route test forgets the usage route)

   Properties (ghost latches, one asserted per run with -D ASSERT_PROP=<latch>, expected to stay 0)
     G2 gSharedOpensOther a request whose only credential is a shared token opened a route that is not shared
     G3 gMainRefused     a request with the main token (and no more) was refused by the gate on a node that has a credential
     G4 gAnonServed      a request with no credential at all was served on a protected route of a node that has a credential, or on a shared
                         route of a node with none and no allow_insecure
     G5 gHandshake       under a client CA a request with no certificate, and a certificate that does not verify, reached the gate
     G7 gDupMain         a token configured as both classes was classed as the main one
     G9 gSharedRefused   a configured shared token was refused on a shared route, once the handshake has passed
     G11 gWrongServed    a request with no valid credential (no token or a wrong one, no cookie) was served on a protected route of a node that
                         has a credential
     G13 gOpenRefused    a node with no credential refused a request it offers: any route, and the shared ones when allow_insecure is set
     G14 gCookieRefused  a live browser session was refused on a node that has a credential, once the handshake has passed (the CSRF check on
                         a cookie's write aside)
     G15 gCertDecides    the decision depends on the certificate or on whether a client CA was asked for (above)
     G16 gNoCertClass    a request was served under a class that is a certificate's (the model has no such class: a mutant gives it class 3)
     G17 gOracle         the whole decision, (result, class), differs from a table of the code written by cases (the handshake, a transparent
                         gate, then the ordered credentials); the two handshake mutants are G5's, the oracle's first case takes them into account
     G2, G3, G4, G11, G13, G14 and G17 hold on every route but the media raw route (see the abstractions)
     sanity              sMain, sShared, sCookie, sOpen (an open node served), s401, s403, sHand, sCertVerified (a verified certificate reached the gate
                         and was served on the strength of another credential), sCertAlone (a verified certificate alone was refused with 401)

   Abstractions (what the model is not): the gate's CSRF check on a cookie's state-changing request (a refusal that only narrows); the media
     capability route (GET .../workspace/raw with access_token), which the gate decides before the enabled check and before the bearer, so G2, G3, G4,
     G11, G13, G14 and G17 hold on every route but that one (an invalid capability is a 401 even on an open node or with a cookie, a valid one is served with no credential); the SSE access_token query parameter; public_docs; CORS preflights, which are answered before the gate; a cookie
     when the login is broken (the model accepts it: wider); the limiter keys and the counters (their separation is by construction); the verification
     of the chain itself (crypto/x509: held by the end-to-end tests); the tunnel: a request that arrives over the reverse tunnel carries no r.TLS, so under
     a client CA it reaches the gate with no certificate (docs/features/shared-models.md), which G5 does not claim.
*/

#ifndef CERTOPENS
#define CERTOPENS 0
#endif
#ifndef CERTMAIN
#define CERTMAIN 0
#endif
#ifndef CERTVETO
#define CERTVETO 0
#endif
#ifndef GATESHARED
#define GATESHARED 0
#endif
#ifndef MAINDROP
#define MAINDROP 0
#endif
#ifndef REFUSAL403
#define REFUSAL403 0
#endif
#ifndef COOKIEFIRST
#define COOKIEFIRST 0
#endif
#ifndef PUBLICGATED
#define PUBLICGATED 0
#endif
#ifndef REQTLS
#define REQTLS 0
#endif
#ifndef SHAREDALL
#define SHAREDALL 0
#endif
#ifndef DUPMAIN
#define DUPMAIN 0
#endif
#ifndef NOHANDSHAKE
#define NOHANDSHAKE 0
#endif
#ifndef UNVERIFIEDOK
#define UNVERIFIEDOK 0
#endif
#ifndef ANONSHARED
#define ANONSHARED 0
#endif
#ifndef DUPDROP
#define DUPDROP 0
#endif
#ifndef COOKIENOSHARED
#define COOKIENOSHARED 0
#endif
#ifndef INSECUREREFUSED
#define INSECUREREFUSED 0
#endif
#ifndef WRONGOK
#define WRONGOK 0
#endif
#ifndef SHAREDSTATS
#define SHAREDSTATS 0
#endif
#ifndef DROPPING
#define DROPPING 0
#endif
#ifndef DROPUSAGE
#define DROPUSAGE 0
#endif

byte bearer, cert, cookie, mode, route;
bit hmain, hshared, hlogin, allowinsecure;
byte result, cls;
byte resultA, clsA, resultB, clsB, resultC, clsC, resultD, clsD;   /* the comparison decisions of G15 */
byte exr, exc;                                     /* the oracle of G17 */
bit gateOn, shared_route, gshared;
bit credNode;                        /* the node has a credential of some class: the truth the properties judge by */

bit gSharedOpensOther, gMainRefused, gAnonServed, gHandshake, gDupMain, gSharedRefused, gWrongServed, gOpenRefused, gCookieRefused;
bit gCertDecides, gNoCertClass, gOracle;
bit sMain, sShared, sCookie, sOpen, s401, s403, sHand, sCertVerified, sCertAlone;

inline latch() {
#ifdef ASSERT_PROP
    assert(ASSERT_PROP == 0);
#endif
    skip
}

/* decide(m, c, t): the request as it is, on a listener of mode m, with the certificate c in the handshake, and t = 1 for a request that
   arrived with no handshake (the reverse tunnel); result and cls are left in the globals. The code is the same function of everything
   but the TLS layer, which is the point of G15. */
inline decide(m, c, t) {
    result = 1; cls = 0;
    gateOn = (hmain || hlogin || hshared);
#if GATESHARED == 1
    gateOn = (hmain || hlogin);
#endif
    shared_route = (route <= 3);      /* the truth: the four shared routes */
    gshared = shared_route;           /* what the gate's isSharedLLMPattern says (a mutant moves it) */
#if SHAREDSTATS == 1
    gshared = (route <= 4);
#endif
#if DROPPING == 1
    gshared = (route <= 2);
#endif
#if DROPUSAGE == 1
    gshared = (route == 0 || route == 2 || route == 3);
#endif
    /* the TLS layer first: the handshake. With a client CA a peer with no verified certificate gets no connection; without one no
       certificate is asked for. */
    if
    :: !t && m == 2 && c == 2 && UNVERIFIEDOK == 0 -> result = 3
    :: !t && m == 2 && c == 0 && NOHANDSHAKE == 0 -> result = 3
    :: else -> skip
    fi;
    if
    :: result == 3 -> skip
    :: else ->
         if
         :: !gateOn || (route == 6 && PUBLICGATED == 0) ->
              /* the gate is transparent; the shared handlers then refuse an anonymous caller of a node with no credential and no allow_insecure */
              if
              :: ANONSHARED == 0 && shared_route && !gateOn && !allowinsecure -> result = 2
              :: INSECUREREFUSED == 1 && shared_route && !gateOn -> result = 2
              :: else -> result = 0
              fi
         :: else ->
              /* ordered, first match wins: a step runs only while result is still 1; a token that is both classes is read as shared */
              if :: COOKIEFIRST == 1 && result == 1 && cookie == 1 -> result = 0; cls = 4 :: else -> skip fi;
              if :: MAINDROP == 0 && result == 1 && bearer == 1 -> result = 0; cls = 1 :: else -> skip fi;
              if :: result == 1 && bearer == 3 && DUPMAIN == 1 -> result = 0; cls = 1 :: else -> skip fi;
              if :: result == 1 && bearer == 4 && WRONGOK == 1 -> result = 0; cls = 1 :: else -> skip fi;
              if :: result == 1 && gshared && (bearer == 2 || (bearer == 3 && DUPDROP == 0)) && hshared -> result = 0; cls = 2 :: else -> skip fi;
              if :: result == 1 && cookie == 1 && !(COOKIENOSHARED == 1 && gshared) -> result = 0; cls = 4 :: else -> skip fi;
              /* mutants: a certificate given weight */
              if
              :: result == 1 && CERTOPENS == 1 && c == 1 && gshared -> result = 0; cls = 3
              :: result == 1 && CERTMAIN == 1 && c == 1 -> result = 0; cls = 3
              :: result == 1 && SHAREDALL == 1 && (bearer == 2 || bearer == 3) && hshared -> result = 0; cls = 2
              :: else -> skip
              fi;
              /* a mutant: a node that has a credential refuses a request with no certificate under a client CA, once the handshake let it through (the tunnel) */
              if :: REQTLS == 1 && m == 2 && c == 0 -> result = 1; cls = 0 :: else -> skip fi;
              /* a mutant: a certificate that does not verify, let through by a leaky handshake, vetoes the other credentials */
              if
              :: CERTVETO == 1 && c == 2 -> result = 1; cls = 0
              :: else -> skip
              fi
         fi
    fi;
    /* a mutant: the final refusal is a 403, not a 401 */
    if :: REFUSAL403 == 1 && result == 1 -> result = 2 :: else -> skip fi
}

init {
    atomic {
        /* every combination of the inputs */
        if :: bearer = 0 :: bearer = 1 :: bearer = 2 :: bearer = 3 :: bearer = 4 fi;
        if :: cert = 0 :: cert = 1 :: cert = 2 fi;
        if :: cookie = 0 :: cookie = 1 fi;
        if :: mode = 0 :: mode = 2 fi;
        if :: route = 0 :: route = 1 :: route = 2 :: route = 3 :: route = 4 :: route = 5 :: route = 6 fi;
        if :: hmain = 0 :: hmain = 1 fi;
        if :: hshared = 0 :: hshared = 1 fi;
        if :: hlogin = 0 :: hlogin = 1 fi;
        if :: allowinsecure = 0 :: allowinsecure = 1 fi;
        /* a configuration is consistent with the bearer: a token that is configured is a token the request can carry; a request whose
           bearer is main/shared/both needs that class configured, or it is a wrong token (4) */
        if
        :: bearer == 1 && !hmain -> bearer = 4
        :: bearer == 2 && !hshared -> bearer = 4
        :: bearer == 3 && !(hmain && hshared) -> bearer = 4
        :: else -> skip
        fi;
        /* without a client CA no certificate is presented at all */
        if :: mode == 0 && cert != 0 -> cert = 0 :: else -> skip fi;
        credNode = (hmain || hlogin || hshared);

        /* the comparison decisions, over the same everything but the TLS layer: a listener with no client CA and a peer with no
           certificate (A), a listener with a client CA and a peer whose certificate verifies (B) or does not (C), and a listener with a client
           CA and a request that arrived with no handshake (D, the tunnel) */
        decide(0, 0, 0); resultA = result; clsA = cls;
        decide(2, 1, 0); resultB = result; clsB = cls;
        decide(2, 2, 0); resultC = result; clsC = cls;
        decide(2, 0, 1); resultD = result; clsD = cls;
        decide(mode, cert, 0);

        /* ---- the properties ---- */
        if :: result == 3 -> sHand = 1 :: else -> skip fi;
        if :: result == 0 && cls == 1 -> sMain = 1 :: else -> skip fi;
        if :: result == 0 && cls == 2 -> sShared = 1 :: else -> skip fi;
        if :: result == 0 && cls == 4 -> sCookie = 1 :: else -> skip fi;
        if :: result == 0 && cls == 0 && !credNode -> sOpen = 1 :: else -> skip fi;
        if :: result == 1 -> s401 = 1 :: else -> skip fi;
        if :: result == 2 -> s403 = 1 :: else -> skip fi;
        if :: cert == 1 && mode == 2 && result == 0 && cls != 0 -> sCertVerified = 1 :: else -> skip fi;
        if :: cert == 1 && mode == 2 && bearer != 1 && bearer != 2 && bearer != 3 && cookie == 0 && credNode && route != 6 && result == 1 -> sCertAlone = 1 :: else -> skip fi;

        /* G2 */
        if :: result == 0 && cls == 2 && !shared_route -> gSharedOpensOther = 1 :: else -> skip fi;
        /* G3: the main token alone, on a node that has a credential */
        if
        :: bearer == 1 && credNode && result != 0 && result != 3 && route != 6 -> gMainRefused = 1
        :: else -> skip
        fi;
        /* G4 */
        if
        :: bearer == 0 && cookie == 0 && result == 0 && credNode && route != 6 -> gAnonServed = 1
        :: bearer == 0 && cookie == 0 && result == 0 && !credNode && shared_route && !allowinsecure -> gAnonServed = 1
        :: else -> skip
        fi;
        /* G5 */
        if :: mode == 2 && cert != 1 && result != 3 -> gHandshake = 1 :: else -> skip fi;
        /* G7: a token configured as both classes opened a non-shared route */
        if :: bearer == 3 && result == 0 && cls == 1 -> gDupMain = 1 :: else -> skip fi;
        /* G9: a configured shared token is served on every shared route (past the handshake) */
        if :: (bearer == 2 || bearer == 3) && hshared && shared_route && result != 0 && result != 3 -> gSharedRefused = 1 :: else -> skip fi;
        /* G11: no valid credential, no service: bearer none or wrong, no cookie */
        if :: (bearer == 0 || bearer == 4) && cookie == 0 && result == 0 && credNode && route != 6 -> gWrongServed = 1 :: else -> skip fi;
        /* G13: a node with no credential serves what it offers: every route, and the shared ones only on allow_insecure */
        if :: !credNode && result != 0 && result != 3 && (!shared_route || allowinsecure) -> gOpenRefused = 1 :: else -> skip fi;
        /* G14: a live browser session is served on every route of a node that has a credential (past the handshake) */
        if :: cookie == 1 && credNode && result != 0 && result != 3 -> gCookieRefused = 1 :: else -> skip fi;
        /* G15: past the handshake the certificate changes nothing: whatever reaches the gate under a client CA (a verified certificate,
           or one that did not verify and was let through) is decided exactly as the same request on a listener with no client CA and
           no certificate. A request the handshake refused (result 3) is not decided, so it is not compared. */
        if
        :: resultB != 3 && (resultB != resultA || clsB != clsA) -> gCertDecides = 1
        :: resultC != 3 && (resultC != resultA || clsC != clsA) -> gCertDecides = 1
        :: resultD != 3 && (resultD != resultA || clsD != clsA) -> gCertDecides = 1
        :: else -> skip
        fi;
        /* G17: the decision as a table of the code, written by cases (not by the chain the model decides with): the handshake first, then a
           transparent gate, then the ordered credentials. Nested else, never overlapping guards: an overlap would make a Promela if
           nondeterministic. The two handshake mutants are G5's, since the oracle's first case takes them into account. */
        exr = 1; exc = 0;
        if
        :: mode == 2 && ((cert == 2 && UNVERIFIEDOK == 0) || (cert == 0 && NOHANDSHAKE == 0)) -> exr = 3
        :: else ->
             if
             :: !credNode || route == 6 ->
                  if :: shared_route && !credNode && !allowinsecure -> exr = 2 :: else -> exr = 0 fi
             :: else ->
                  if
                  :: bearer == 1 -> exr = 0; exc = 1
                  :: else ->
                       if
                       :: (bearer == 2 || bearer == 3) && hshared && shared_route -> exr = 0; exc = 2
                       :: else -> if :: cookie == 1 -> exr = 0; exc = 4 :: else -> skip fi
                       fi
                  fi
             fi
        fi;
        if :: result != exr || cls != exc -> gOracle = 1 :: else -> skip fi;
        /* G16: a class that is a certificate's */
        if :: cls == 3 -> gNoCertClass = 1 :: else -> skip fi;
        latch()
    }
}
