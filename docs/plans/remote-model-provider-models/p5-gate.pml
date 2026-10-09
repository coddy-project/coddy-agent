/* p5-gate.pml - the node's gate: every credential a caller can bring against every route, with the two client-certificate modes, as the
   code decides it (phase 1 token classes, phase 3 H7: httpserver.tls and httpserver.shared_models.cert_names).

   Code     external/httpserver/auth.go (authGate: not gateOn or not a protected route passes through; the main bearer; the shared
            bearer on a shared route; a verified certificate name from cert_names on a shared route; a signed-in cookie; else 401),
            tls.go (sharedCertName: names read per request from the verified chain, the leaf's validity rechecked), internal/netx
            (ClientCertTLS: required refuses a peer with no verified certificate at the handshake, optional lets it in, a certificate
            that does not verify fails the handshake in both), shared_models_http.go (the handlers: anonymousSharedRefused is 403),
            authPolicyNow (gateOn = a main token, a login, a shared token or a certificate name; a token that is both main and shared
            is read as shared), sharedClassOf (shared_audit.go) and sharedCallerKey (shared_models_http.go), which are two more copies
            of the order of the credentials and which the gate's model cannot see (the gate serves in either order: the order is only
            visible in the class that is counted and in the limiter key).
   Class    finite, no time: a decision procedure explored over every combination of its inputs (the nondeterministic choices of init).
            The decision is an ordered chain, first match wins, as the code is (each step runs only while the result is still a refusal).

   World (one request)
     bearer  0 none, 1 the main token, 2 a shared-model token, 3 a token that is configured as both (read as shared), 4 a wrong token
     cert    0 none, 1 verified, name listed in cert_names, 2 verified, name not listed (or the list is empty), 3 verified and then expired on
             an open connection, 4 verified, no DNS or URI name (an e-mail or an IP address only), 5 not verifying (another authority, expired at the
             handshake, no clientAuth)
     cookie  0 none, 1 a live browser session: the sign-in form's, or a Telegram admin's Mini App session (which does not depend on the login)
     mode    0 no client CA (the listener asks for nothing), 1 optional, 2 required
     route   0 listing, 1 usage, 2 completions, 3 ping (the four shared routes), 4 counters (stats), 5 another protected route, 6 a public route
     config  which credentials the node has: HMAIN, HSHARED, HLOGIN, HCERT (names listed) and ALLOWINSECURE

   Result  0 served, 1 refused 401, 2 refused 403, 3 no connection (a handshake failure)
   Class that opened the request  0 none (public or open node), 1 main, 2 shared, 3 certificate, 4 cookie

   Truth and reading. shared_route is the truth (the four shared routes); gshared is what the gate's route test (isSharedLLMPattern) says,
   and a mutant moves it. credNode is the truth of the node's configuration; gateOn is what authPolicyNow computes, and a mutant moves it.
   The properties judge by the truth, never by the reading the decision uses.

   Options (the code is the default of each; a mutant changes one rule)
     CERTMAIN 0 a certificate opens only the shared routes (1: a listed certificate opens every route, a mutant)
     SHAREDALL 0 a shared token opens only the shared routes (1: every route)
     DUPMAIN 0 a token configured as both classes is the shared one (1: the main one)
     NOLISTCHECK 0 a verified certificate needs a listed name (1: any verified certificate passes)
     EXPIREDOK 0 a certificate past its dates on an open connection has no name (1: it keeps its name)
     NAMELESSOK 0 a certificate with no DNS or URI name has no name (1: it passes as listed)
     UNVERIFIEDOK 0 a certificate that does not verify fails the handshake (1: a compound regression: the listener asks for a certificate without
             verifying it (RequestClientCert) and the identity is read from PeerCertificates, so under optional it passes and is read as listed;
             PeerCertificates alone could not admit it, VerifyClientCertIfGiven fails the handshake first)
     NOHANDSHAKE 0 a peer with no verified certificate under required is refused (1: let in)
     GATEIGNORESCERT 0 a node whose only credential is cert_names counts as having one (1: it does not, so it is open: authPolicyNow forgot it)
     ANONSHARED 0 the shared handlers refuse an anonymous caller of a node with no credential and no allow_insecure (1: they serve it)
     CERTFIRST 0 the main bearer is judged before a listed certificate (1: a listed certificate on a shared route comes first)
     BADCERTFIRST 0 a verified certificate that gives no identity changes nothing for the other credentials (1: it refuses the request)
     DUPDROP 0 a token configured as both classes is accepted on the shared routes (1: it is not)
     COOKIENOSHARED 0 a live browser session opens every protected route (1: not the shared ones)
     INSECUREREFUSED 0 a node with no credential and allow_insecure serves the shared routes (1: it refuses them)
     WRONGOK 0 a wrong token is refused (1: it is read as the main token)
     NOCERTONPING 0 the probe's ping takes a listed certificate like the other shared routes (1: it does not)
     SHAREDSTATS 0 the gate's route test says the four shared routes (1: it also says the counters route)
     DROPPING 0 (1: the route test forgets the ping)
     DROPUSAGE 0 (1: the route test forgets the usage route)

   Properties (ghost latches, one asserted per run with -D ASSERT_PROP=<latch>, expected to stay 0)
     G1 gCertOpensOther  a request whose only credential is a certificate opened a route that is not one of the four shared routes
     G2 gSharedOpensOther a request whose only credential is a shared token opened a route that is not shared
     G3 gMainRefused     a request with the main token (and no more) was refused by the gate on a node that has a credential
     G4 gAnonServed      a request with no credential at all was served on a protected route of a node that has a credential, or on a shared
                         route of a node with none and no allow_insecure
     G5 gHandshake       under required a request with no certificate, and under either mode a certificate that does not verify, reached the gate
                         (on the direct listener: see below)
     G6 gNoIdentity      a certificate that is unlisted, expired on the open connection, nameless or not verifying was given the certificate class
     G7 gDupMain         a token configured as both classes was classed as the main one (on any route)
     G8 gClassOrder      a request with a main token AND a listed certificate was classed as the certificate (a certificate never lowers a credential)
     G9 gSharedRefused   a configured shared token was refused on a shared route, once the handshake has passed (a peer that fails it never reaches
                         the gate: G5)
     G10 gCertRefused    a listed verified certificate was refused on a shared route, once the handshake has passed
     G11 gWrongServed    a request with no valid credential (no token or a wrong one, no cookie, no listed certificate on a shared route) was
                         served on a protected route of a node that has a credential (on every route but the media raw route)
     G12 gClsExact       a shared bearer (or a token of both classes) with a listed certificate on a shared route was classed as anything but
                         shared (the code's order)
     G13 gOpenRefused    a node with no credential refused a request it offers: any route, and the shared ones when allow_insecure is set
     G14 gCookieRefused  a live browser session was refused on a node that has a credential, once the handshake has passed
     sanity              sMain, sShared, sCert, sCookie, sOpen (an open node served), s401, s403, sHand,
                         sCertOverShared (must NOT be reachable: the chain is ordered; an unordered decision reaches it)

   Abstractions (what the model is not): the gate's CSRF check on a cookie's state-changing request (a refusal that only narrows); the media
     capability route (GET .../workspace/raw with access_token), which the gate decides before the enabled check and before the bearer, so
     G3, G4 and G11 hold on every route but that one; the SSE access_token query parameter; public_docs; CORS preflights, which are answered before
     the gate; a cookie when the login is broken (the model accepts it: wider); the limiter keys and the counters (their separation is by
     construction: the key of a bearer, of a certificate name and of a cookie are different strings) and the copies of the order of the
     credentials in sharedClassOf and sharedCallerKey; the verification of the chain itself (crypto/x509: held by the end-to-end tests of
     docs/operate/certificates.md); the tunnel: a request that arrives over the reverse tunnel carries no r.TLS, so under required it reaches
     the gate with no certificate (docs/features/shared-models.md), which G5 does not claim.
*/

#ifndef CERTMAIN
#define CERTMAIN 0
#endif
#ifndef SHAREDALL
#define SHAREDALL 0
#endif
#ifndef DUPMAIN
#define DUPMAIN 0
#endif
#ifndef NOLISTCHECK
#define NOLISTCHECK 0
#endif
#ifndef EXPIREDOK
#define EXPIREDOK 0
#endif
#ifndef NOHANDSHAKE
#define NOHANDSHAKE 0
#endif
#ifndef GATEIGNORESCERT
#define GATEIGNORESCERT 0
#endif
#ifndef ANONSHARED
#define ANONSHARED 0
#endif
#ifndef CERTFIRST
#define CERTFIRST 0
#endif
#ifndef BADCERTFIRST
#define BADCERTFIRST 0
#endif

#ifndef INSECUREREFUSED
#define INSECUREREFUSED 0
#endif
#ifndef COOKIENOSHARED
#define COOKIENOSHARED 0
#endif
#ifndef DUPDROP
#define DUPDROP 0
#endif

#ifndef WRONGOK
#define WRONGOK 0
#endif
#ifndef UNVERIFIEDOK
#define UNVERIFIEDOK 0
#endif
#ifndef NAMELESSOK
#define NAMELESSOK 0
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
#ifndef NOCERTONPING
#define NOCERTONPING 0
#endif

byte bearer, cert, cookie, mode, route;
bit hmain, hshared, hlogin, hcert, allowinsecure;
byte result, cls;
bit gateOn, shared_route, gshared, shared_tok, cert_name;
bit gSharedRefused, gCertRefused, gWrongServed, gClsExact, sCertOverShared;
bit credNode;                  /* the node has a credential of some class: the truth the properties judge by, never a mutant's reading */

bit gCertOpensOther, gSharedOpensOther, gMainRefused, gAnonServed, gHandshake, gNoIdentity, gDupMain, gClassOrder, gOpenRefused, gCookieRefused;
bit sMain, sShared, sCert, sCookie, sOpen, s401, s403, sHand;

inline latch() {
#ifdef ASSERT_PROP
    assert(ASSERT_PROP == 0);
#endif
    skip
}

inline decide() {
    result = 1; cls = 0;
#if GATEIGNORESCERT == 1
    gateOn = (hmain || hlogin || hshared);
#else
    gateOn = (hmain || hlogin || hshared || hcert);
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
    /* the TLS layer first: the handshake */
    if
    :: cert == 5 && !(UNVERIFIEDOK == 1 && mode == 1) -> result = 3
    :: mode == 2 && cert == 0 && NOHANDSHAKE == 0 -> result = 3
    :: else -> skip
    fi;
    if
    :: result == 3 -> skip
    :: else ->
         /* the names the verified certificate maps to: only a listed name counts; an expired leaf has none */
         cert_name = 0;
#if NOLISTCHECK == 1
         if :: (cert == 1 || cert == 2) -> cert_name = 1 :: else -> skip fi;
#else
         if :: cert == 1 && hcert -> cert_name = 1 :: else -> skip fi;
#endif
#if EXPIREDOK == 1
         if :: cert == 3 && hcert -> cert_name = 1 :: else -> skip fi;
#endif
#if NAMELESSOK == 1
         if :: cert == 4 && hcert -> cert_name = 1 :: else -> skip fi;
#endif
#if UNVERIFIEDOK == 1
         if :: cert == 5 && hcert -> cert_name = 1 :: else -> skip fi;
#endif
#if NOCERTONPING == 1
         if :: route == 3 -> cert_name = 0 :: else -> skip fi;
#endif
         if
         :: !gateOn || route == 6 ->
              /* the gate is transparent; the shared handlers then refuse an anonymous caller of a node with no credential and no allow_insecure */
              if
              :: ANONSHARED == 0 && shared_route && !gateOn && !allowinsecure -> result = 2
              :: INSECUREREFUSED == 1 && shared_route && !gateOn -> result = 2
              :: else -> result = 0
              fi
         :: else ->
              /* ordered, first match wins: a step runs only while result is still 1; a token that is both classes is read as shared */
              if :: CERTFIRST == 1 && gshared && cert_name -> result = 0; cls = 3 :: else -> skip fi;
              if :: result == 1 && bearer == 1 -> result = 0; cls = 1 :: else -> skip fi;
              if :: result == 1 && bearer == 3 && DUPMAIN == 1 -> result = 0; cls = 1 :: else -> skip fi;
              if :: result == 1 && bearer == 4 && WRONGOK == 1 -> result = 0; cls = 1 :: else -> skip fi;
              if :: result == 1 && gshared && (bearer == 2 || (bearer == 3 && DUPDROP == 0)) && hshared -> result = 0; cls = 2 :: else -> skip fi;
              if :: result == 1 && gshared && cert_name -> result = 0; cls = 3 :: else -> skip fi;
              if :: result == 1 && cookie == 1 && !(COOKIENOSHARED == 1 && gshared) -> result = 0; cls = 4 :: else -> skip fi;
              /* mutants that widen a class */
              if
              :: result == 1 && CERTMAIN == 1 && cert_name -> result = 0; cls = 3
              :: result == 1 && SHAREDALL == 1 && (bearer == 2 || bearer == 3) && hshared -> result = 0; cls = 2
              :: else -> skip
              fi;
              /* a mutant that lets a certificate with no identity veto the other credentials */
              if
              :: BADCERTFIRST == 1 && (cert == 2 || cert == 3 || cert == 4) -> result = 1; cls = 0
              :: else -> skip
              fi
         fi
    fi
}

init {
    atomic {
        /* every combination of the inputs */
        if :: bearer = 0 :: bearer = 1 :: bearer = 2 :: bearer = 3 :: bearer = 4 fi;
        if :: cert = 0 :: cert = 1 :: cert = 2 :: cert = 3 :: cert = 4 :: cert = 5 fi;
        if :: cookie = 0 :: cookie = 1 fi;
        if :: mode = 0 :: mode = 1 :: mode = 2 fi;
        if :: route = 0 :: route = 1 :: route = 2 :: route = 3 :: route = 4 :: route = 5 :: route = 6 fi;
        if :: hmain = 0 :: hmain = 1 fi;
        if :: hshared = 0 :: hshared = 1 fi;
        if :: hlogin = 0 :: hlogin = 1 fi;
        if :: hcert = 0 :: hcert = 1 fi;
        if :: allowinsecure = 0 :: allowinsecure = 1 fi;
        /* a configuration is consistent with the bearer: a token that is configured is a token the request can carry; a request whose
           bearer is main/shared/both needs that class configured, or it is a wrong token (4) */
        if
        :: bearer == 1 && !hmain -> bearer = 4
        :: bearer == 2 && !hshared -> bearer = 4
        :: bearer == 3 && !(hmain && hshared) -> bearer = 4
        :: else -> skip
        fi;
        /* a client certificate needs a client CA (a mode other than 0) to be verified at all; a name "listed" in an empty list is not listed */
        if :: cert == 1 && !hcert -> cert = 2 :: else -> skip fi;
        if :: mode == 0 && cert != 0 -> cert = 0 :: else -> skip fi;
        credNode = (hmain || hlogin || hshared || hcert);
        decide();

        /* ---- the properties ---- */
        if
        :: result == 3 -> sHand = 1
        :: else -> skip
        fi;
        if :: result == 0 && cls == 1 -> sMain = 1 :: else -> skip fi;
        if :: result == 0 && cls == 2 -> sShared = 1 :: else -> skip fi;
        if :: result == 0 && cls == 3 -> sCert = 1 :: else -> skip fi;
        if :: result == 0 && cls == 4 -> sCookie = 1 :: else -> skip fi;
        if :: result == 0 && cls == 0 && !credNode -> sOpen = 1 :: else -> skip fi;
        if :: result == 1 -> s401 = 1 :: else -> skip fi;
        if :: result == 2 -> s403 = 1 :: else -> skip fi;

        /* G1: the only credential is a certificate (no usable bearer, no cookie) */
        if
        :: result == 0 && cls == 3 && !shared_route -> gCertOpensOther = 1
        :: else -> skip
        fi;
        /* G2 */
        if
        :: result == 0 && cls == 2 && !shared_route -> gSharedOpensOther = 1
        :: else -> skip
        fi;
        /* G3: the main token alone, on a node that has a credential */
        if
        :: bearer == 1 && cert != 5 && credNode && !(mode == 2 && cert == 0) && result != 0 && route != 6 -> gMainRefused = 1
        :: else -> skip
        fi;
        /* G4 */
        if
        :: bearer == 0 && cert == 0 && cookie == 0 && result == 0 && credNode && route != 6 -> gAnonServed = 1
        :: bearer == 0 && cert == 0 && cookie == 0 && result == 0 && !credNode && shared_route && !allowinsecure -> gAnonServed = 1
        :: else -> skip
        fi;
        /* G5 */
        if :: ((mode == 2 && cert == 0) || cert == 5) && result != 3 -> gHandshake = 1 :: else -> skip fi;
        /* G6 */
        if :: (cert == 2 || cert == 3 || cert == 4 || cert == 5) && cls == 3 && result == 0 -> gNoIdentity = 1 :: else -> skip fi;
        /* G7: a token configured as both classes opened a non-shared route */
        if :: bearer == 3 && result == 0 && cls == 1 -> gDupMain = 1 :: else -> skip fi;
        /* G8: the main token and a listed certificate together: the class is the main one */
        if :: bearer == 1 && cert == 1 && hcert && result == 0 && cls == 3 -> gClassOrder = 1 :: else -> skip fi;
        /* G9: a configured shared token is served on every shared route (past the handshake) */
        if :: (bearer == 2 || bearer == 3) && hshared && shared_route && result != 0 && result != 3 -> gSharedRefused = 1 :: else -> skip fi;
        /* G10: a listed verified certificate is served on every shared route */
        if :: cert == 1 && hcert && shared_route && result != 0 && result != 3 -> gCertRefused = 1 :: else -> skip fi;
        /* G11: no valid credential, no service: bearer none or wrong, no cookie, no listed certificate on a shared route */
        if :: (bearer == 0 || bearer == 4) && cookie == 0 && !(cert == 1 && hcert && shared_route) && result == 0 && credNode && route != 6 -> gWrongServed = 1 :: else -> skip fi;
        /* class exactness: a shared bearer with a listed certificate on a shared route is class shared (the code's order) */
        if :: (bearer == 2 || bearer == 3) && cert == 1 && hcert && shared_route && result == 0 && cls != 2 -> gClsExact = 1 :: else -> skip fi;
        if :: bearer == 2 && cert == 1 && hcert && shared_route && result == 0 && cls == 3 -> sCertOverShared = 1 :: else -> skip fi;
        /* G13: a node with no credential serves what it offers: every route, and the shared ones only on allow_insecure */
        if :: !credNode && result != 0 && result != 3 && (!shared_route || allowinsecure) -> gOpenRefused = 1 :: else -> skip fi;
        /* G14: a live browser session is served on every route of a node that has a credential (past the handshake) */
        if :: cookie == 1 && credNode && result != 0 && result != 3 -> gCookieRefused = 1 :: else -> skip fi;
        latch()
    }
}
