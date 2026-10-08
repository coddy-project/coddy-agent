/* p3-d2-mtls-identity.pml - how a verified client certificate relates to the bearer at the relay, and whether the identity is read
   per request or cached per connection (phase 3, dilemma D2).

   Decides  dilemma D2 of docs/plans/remote-model-provider-phase3.md (section 10) and the places that depend on it: 4.1, 4.2
            (the relay listener, the mapping, the entry semantics), 11 (stage H2a).
   Code     external/swarm/server.go (authGate: the principal of a request), serve_swarm.go (server.TLSConfig: ClientCAs,
            ClientAuth), crypto/tls (a client certificate is verified at the handshake; VerifyPeerCertificate does NOT run on a
            resumed connection, but the verified chain of the original session is restored on r.TLS; the entry mapping is the
            relay's own, read from the live configuration), the relay replaces the caller's credential (rewriteFor), so the node
            never sees a certificate and the relay is the only place the binding can live.
   Class    finite, untimed, interleaving of a Revoker (the operator removes the name of entry 0 and the whole entry 1, once, at
            any moment) and two connections, each a handshake followed by any number of requests. No clock.

   World
     entries  e0 = token T0 AND certificate name N0 (an entry that binds both);  e1 = token T1 (E1MODE 0, a bearer entry) or the
              certificate name N1 (E1MODE 1, a certificate entry);  full = token F (the full class).
     certs    K0 maps to e0, K1 maps to e1, K2 is chain-valid for the CA and maps to no entry. After the revocation K0 maps to
              nothing (the name was removed) and K1 maps to nothing (the entry was removed); T0 stays a valid token of e0.
     attacker holds ANY subset of {T0, T1, F, K0, K1, K2} (chosen at the start: 64 subsets); a request presents one held
              certificate (or none) and one held bearer (or none, or a wrong one).
     connection  a handshake (the certificate chosen, a resumed flag: a TLS ticket issued before the revocation) and then requests.

   Options (the dilemma)
     OPT 1   (a) a certificate alone authenticates an entry, a token alone does too (two credentials of one entry, either suffices:
             a token of one entry with a certificate of another is admitted as the token's entry, and a wrong bearer with a mapped
             certificate is admitted as the certificate's entry, "either" is meant literally); identity read per request
     OPT 2   (b) per entry: e0 needs the token AND a certificate that maps to e0, e1 is a bearer entry or a certificate entry; the
             identity is read per request from r.TLS against the LIVE configuration                         (expected: holds)
     OPT 3   (c) a listener-wide mode, `required` for everything: a connection without a verified certificate is refused at the
             handshake; the identity is resolved once, at the handshake, and cached in the connection (a resumed connection takes
             the one of its ticket)
     E1MODE  0 e1 is a bearer entry, 1 e1 is a certificate entry (only for OPT 2 and OPT 3)
     MUT     1 a defect: a certificate nobody mapped (K2) opens the full class

   Properties (ghost latches, monotone)
     S1  gS1   with only a stolen token (no K0) no request is admitted on e0, which binds both
         gS1k  with only a stolen key (no T0) no request is admitted on e0
     S2  gS2   an open connection (not resumed) opened before the revocation is judged by the live configuration: a request is not
               admitted unless a fresh connection of the same certificate and bearer would be admitted now
     S3  gS3   a chain-valid certificate with no entry (K2), alone, reaches no scoped route
     S4  gS4   the full class is not reachable by a certificate (no request is admitted as full without F)
     S5  gS5   a resumed connection is judged like a fresh one of the same certificate and bearer
     L1  gL1   a client holding both T0 and K0 is admitted on e0 before the revocation
         gL1b  (cost) a client that holds only the token of a bearer entry is not locked out by the listener (E1MODE 0)
     sanity    sE0 sE1 sFull sStale (a request on a connection whose cached identity is out of date) sRevRefuse (a revoked
               credential is refused)
*/

#ifndef OPT
#define OPT 2
#endif
#ifndef E1MODE
#define E1MODE 0
#endif
#ifndef MUT
#define MUT 0
#endif

byte rev;                    /* 0 before the revocation, 1 after */
bit hT0, hT1, hF, hK0, hK1, hK2;   /* what the attacker holds */
bit gS1, gS1k, gS2, gS3, gS4, gS5, gL1, gL1b;
bit sE0, sE1, sFull, sStale, sRevRefuse;

byte cOpen[2];               /* 1 once the handshake succeeded                              */
byte cCert[2];               /* 0 none, 1 K0, 2 K1, 3 K2                                    */
byte cRes[2];                /* 1 resumed                                                   */
byte cCache[2];              /* the identity cached at the handshake: 0 none, 1 e0, 2 e1    */
byte cEp[2];                 /* the revocation epoch the cache was taken at                 */

/* temporaries of one request */
byte t_cert, t_b, t_cache, t_r, t_p, t_m, t_twin, t_res, t_c;

/* the entry a certificate maps to at revocation epoch t_r (0 none, 1 e0, 2 e1) */
inline mapCert() {
    t_m = 0;
    if
    :: t_cert == 1 && t_r == 0 -> t_m = 1
    :: t_cert == 2 && t_r == 0 -> t_m = 2
    :: else -> skip
    fi
}

/* the principal of a request: 0 none, 1 e0, 2 e1, 3 full. Inputs: t_cert (0 none, 1..3), t_b (0 none, 1 T0, 2 T1, 3 F, 4 wrong),
   t_r (the live revocation epoch), t_cache (the identity cached at the handshake, used by OPT 3 only) */
inline decide() {
    t_p = 0;
#if OPT == 1
    /* (a) either credential suffices, read live */
    mapCert();
    if
    :: t_b == 3 -> t_p = 3
    :: t_b == 1 -> t_p = 1
    :: t_b == 2 && t_r == 0 -> t_p = 2
    :: else -> if :: t_m != 0 -> t_p = t_m :: else -> skip fi
    fi
#elif OPT == 2
    /* (b) per entry, live */
    mapCert();
    if
    :: t_b == 3 -> t_p = 3
    :: else ->
         if :: t_b == 1 && t_m == 1 -> t_p = 1 :: else -> skip fi;
#if E1MODE == 0
         if :: t_b == 2 && t_r == 0 -> t_p = 2 :: else -> skip fi
#else
         if :: t_m == 2 -> t_p = 2 :: else -> skip fi
#endif
    fi
#else
    /* (c) listener-wide required, identity cached at the handshake */
    if
    :: t_b == 3 -> t_p = 3
    :: else ->
         if :: t_b == 1 && t_cache == 1 -> t_p = 1 :: else -> skip fi;
#if E1MODE == 0
         if :: t_b == 2 && t_r == 0 -> t_p = 2 :: else -> skip fi
#else
         if :: t_cache == 2 -> t_p = 2 :: else -> skip fi
#endif
    fi
#endif
#if MUT == 1
    /* a certificate nobody mapped opens the full class */
    if :: t_cert == 3 && t_p == 0 -> t_p = 3 :: else -> skip fi
#endif
}

proctype Revoker() {
    atomic { rev = 1 }
}

proctype Conn(byte c) {
    byte cert, res;
    /* the handshake: any held certificate (or none, where the listener allows it), resumed or not */
    atomic {
        if
        :: cert = 0
        :: hK0 == 1 -> cert = 1
        :: hK1 == 1 -> cert = 2
        :: hK2 == 1 -> cert = 3
        fi;
        if :: res = 0 :: cert != 0 -> res = 1 fi;
#if OPT == 3
        /* the listener refuses a peer without a verified certificate */
        if
        :: cert == 0 ->
             if :: hT1 == 1 && E1MODE == 0 && rev == 0 -> gL1b = 1 :: else -> skip fi;
#ifdef ASSERT_PROP
             /* the lockout is decided here, at the handshake: the latch must be checked here too, or a run in which no
                connection ever completes a handshake would never see it */
             assert(ASSERT_PROP == 0);
#endif
             goto done
        :: else -> skip
        fi;
#endif
        cOpen[c] = 1; cCert[c] = cert; cRes[c] = res;
        /* the identity of the handshake: a resumed connection takes the one of its ticket, issued before the revocation */
        t_cert = cert;
        if :: res == 1 -> t_r = 0 :: else -> t_r = rev fi;
        mapCert();
        cCache[c] = t_m; cEp[c] = t_r;
        t_cert = 0; t_r = 0; t_m = 0
    };
    /* the requests of the connection, as many as the adversary likes */
    do
    :: atomic {
         /* the bearer: none, a held one, or a wrong one */
         if
         :: t_b = 0
         :: hT0 == 1 -> t_b = 1
         :: hT1 == 1 -> t_b = 2
         :: hF == 1 -> t_b = 3
         :: t_b = 4
         fi;
         t_cert = cCert[c]; t_r = rev; t_cache = cCache[c];
         decide();
         t_res = t_p;
         /* the same certificate and bearer on a FRESH connection now: the live judgement */
         if :: cCert[c] != 0 || OPT != 3 -> t_cache = 0 :: else -> skip fi;
         t_cert = cCert[c]; t_r = rev;
         if :: OPT == 3 -> mapCert(); t_cache = t_m :: else -> skip fi;
         decide();
         t_twin = t_p;
         t_p = t_res;
         /* S1: admitted on e0 without both credentials */
         if :: t_p == 1 && hK0 == 0 -> gS1 = 1 :: else -> skip fi;
         if :: t_p == 1 && hT0 == 0 -> gS1k = 1 :: else -> skip fi;
         /* S2 and S5: an admission a fresh connection would not give now */
         if :: t_p != 0 && t_twin == 0 && cRes[c] == 0 && cEp[c] != rev -> gS2 = 1 :: else -> skip fi;
         if :: t_p != 0 && t_twin == 0 && cRes[c] == 1 -> gS5 = 1 :: else -> skip fi;
         /* S3: only an unmapped certificate (and possibly nothing else the attacker holds): never a scoped route */
         if :: (t_p == 1 || t_p == 2) && hT0 == 0 && hT1 == 0 && hK0 == 0 && hK1 == 0 -> gS3 = 1 :: else -> skip fi;
         /* S4 */
         if :: t_p == 3 && hF == 0 -> gS4 = 1 :: else -> skip fi;
         /* L1: both credentials of e0, before the revocation, on a fresh connection */
         if :: hT0 == 1 && cCert[c] == 1 && t_b == 1 && rev == 0 && cRes[c] == 0 && t_p != 1 -> gL1 = 1 :: else -> skip fi;
         /* sanity */
         if :: t_p == 1 && hT0 == 1 && cCert[c] == 1 -> sE0 = 1 :: else -> skip fi;
         if :: t_p == 2 -> sE1 = 1 :: else -> skip fi;
         if :: t_p == 3 -> sFull = 1 :: else -> skip fi;
         if :: cEp[c] != rev -> sStale = 1 :: else -> skip fi;
         if :: rev == 1 && t_p == 0 && ((cCert[c] == 1 && t_b == 1) || (cCert[c] == 2)) -> sRevRefuse = 1 :: else -> skip fi;
#ifdef ASSERT_PROP
         assert(ASSERT_PROP == 0);
#endif
         t_cert = 0; t_b = 0; t_cache = 0; t_r = 0; t_p = 0; t_m = 0; t_twin = 0; t_res = 0
       }
    :: break
    od;
done:
    skip
}

init {
    atomic {
        if :: hT0 = 0 :: hT0 = 1 fi;
        if :: hT1 = 0 :: hT1 = 1 fi;
        if :: hF = 0 :: hF = 1 fi;
        if :: hK0 = 0 :: hK0 = 1 fi;
        if :: hK1 = 0 :: hK1 = 1 fi;
        if :: hK2 = 0 :: hK2 = 1 fi;
        run Conn(0);
        run Conn(1);
        run Revoker()
    }
}
