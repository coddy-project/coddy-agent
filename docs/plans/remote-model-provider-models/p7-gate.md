# p7-gate: the credentials that remain against every route, with TLS reduced to a handshake

Model `p7-gate.pml` (Promela; a decision procedure, an ordered chain as the code is, explored over every combination of its inputs in the nondeterministic choices of `init`: 5 bearers x 3 certificates x 2 cookies x 2 client-CA modes x 7 routes x 4 configuration bits = 6,720 combinations, which two rules reduce to **2,912 distinct inputs** (a bearer of a class the node does not have is a wrong token, and no certificate is presented without a client CA); every run explores 2 x 2,912 + 1 = **5,825 states**, the same accounting as p5-gate). Engine `mcd` 0.3.1. Code: `external/httpserver/auth.go` (`authGate`: the main bearer, the shared bearer on a shared route, a signed-in cookie, else 401; the gate and everything it calls read nothing of `r.TLS`: the only reader in the package is `requestIsTLS`, called by `sessionCookie` when a sign-in sets the cookie's Secure flag), `external/httpserver/tls.go` and `internal/netx/servercert.go` (`ClientCertTLS`: `RequireAndVerifyClientCert`, nothing else is read), `external/httpserver/shared_models_http.go` (`anonymousSharedRefused`), `authPolicyNow` (`gateOn` = a main token, a login or a shared token). **It supersedes `p5-gate.pml`** (and `p3-d2-mtls-identity.pml`), whose certificate class (`cert_names`, the class `mtls`, `client_auth`) was removed in phase 4b: TLS is the transport's business, and Coddy reads no identity out of a certificate ([`remote-model-provider-tls-builtin.md`](../remote-model-provider-tls-builtin.md)). The model keeps what survives of p5-gate's latches (G2 to G5, G7, G9, G11, G13, G14), drops the certificate-class ones (G1, G6, G8, G10, G12), and adds **G15** (the latch of the stage), **G16** and **G17** below.

Commands (`M=p7-gate.pml`; every run is exhaustive, `deadlock` is verified in every run, 5,825 states in every run):

```
# the code, every latch
mcd check --promela $M --no-timing -D ASSERT_PROP=gSharedOpensOther                   # verified (5,825 states), G2
mcd check --promela $M --no-timing -D ASSERT_PROP=gMainRefused                        # verified (5,825 states), G3
mcd check --promela $M --no-timing -D ASSERT_PROP=gAnonServed                         # verified (5,825 states), G4
mcd check --promela $M --no-timing -D ASSERT_PROP=gHandshake                          # verified (5,825 states), G5
mcd check --promela $M --no-timing -D ASSERT_PROP=gDupMain                            # verified (5,825 states), G7
mcd check --promela $M --no-timing -D ASSERT_PROP=gSharedRefused                      # verified (5,825 states), G9
mcd check --promela $M --no-timing -D ASSERT_PROP=gWrongServed                        # verified (5,825 states), G11
mcd check --promela $M --no-timing -D ASSERT_PROP=gOpenRefused                        # verified (5,825 states), G13
mcd check --promela $M --no-timing -D ASSERT_PROP=gCookieRefused                      # verified (5,825 states), G14
mcd check --promela $M --no-timing -D ASSERT_PROP=gCertDecides                        # verified (5,825 states), G15
mcd check --promela $M --no-timing -D ASSERT_PROP=gNoCertClass                        # verified (5,825 states), G16
mcd check --promela $M --no-timing -D ASSERT_PROP=gOracle                             # verified (5,825 states), G17
# each mutant, against every latch (the table below)
for m in CERTOPENS CERTMAIN CERTVETO SHAREDALL DUPMAIN NOHANDSHAKE UNVERIFIEDOK ANONSHARED DUPDROP COOKIENOSHARED INSECUREREFUSED WRONGOK SHAREDSTATS DROPPING DROPUSAGE MAINDROP REFUSAL403 COOKIEFIRST PUBLICGATED REQTLS GATESHARED; do
  for l in gSharedOpensOther gMainRefused gAnonServed gHandshake gDupMain gSharedRefused gWrongServed gOpenRefused gCookieRefused gCertDecides gNoCertClass gOracle; do
    mcd check --promela $M --no-timing -D $m=1 -D ASSERT_PROP=$l; done; done
# the veto is reachable only through a leaky handshake
for l in gMainRefused gHandshake gSharedRefused gCookieRefused gCertDecides gOracle; do mcd check --promela $M --no-timing -D CERTVETO=1 -D UNVERIFIEDOK=1 -D ASSERT_PROP=$l; done
# sanity: reachable when violated
mcd check --promela $M --no-timing -D ASSERT_PROP=sMain                               # violated (5,825 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sShared                             # violated (5,825 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sCookie                             # violated (5,825 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sOpen                               # violated (5,825 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=s401                                # violated (5,825 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=s403                                # violated (5,825 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sHand                               # violated (5,825 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sCertVerified                       # violated (5,825 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sCertAlone                          # violated (5,825 states)
```

Latches (G15, G16 and G17 are new; the others are p5-gate's, by the same numbers). **G15 and G16 are structural**: `decide` takes the certificate only through the mutants, so they hold on the code by the model's construction, and what ties them to the code is the reading of `authGate` above and the end-to-end tests `TestACertificateIsNotACredential`, `TestATokenOverACertificateDoesWhatTheTokenDoes` and `TestTheHandshakeAdmitsByTheChainOnly` in `external/httpserver/shared_tls_test.go`; the mutants are what show they can fail.

- **G2** `gSharedOpensOther`: a request whose only credential is a shared token opened a route that is not one of the four shared routes (the media raw route aside)
- **G3** `gMainRefused`: a request with the main token was refused by the gate on a node that has a credential (a refusal past the handshake, any route but the public ones and the media raw route)
- **G4** `gAnonServed`: a request with no credential was served on a protected route of a node that has one, or on a shared route of a node with none and no `allow_insecure`
- **G5** `gHandshake`: under a client CA a request with no certificate, or with one that does not verify, reached the gate
- **G7** `gDupMain`: a token configured as both classes was classed as the main one
- **G9** `gSharedRefused`: a configured shared token was refused on a shared route, past the handshake
- **G11** `gWrongServed`: a request with no valid credential was served on a protected route of a node that has a credential
- **G13** `gOpenRefused`: a node with no credential refused a request it offers (every route but the media raw route, and the shared ones under `allow_insecure`)
- **G14** `gCookieRefused`: a live browser session was refused on a node that has a credential, past the handshake (the CSRF check on a cookie's write aside; every route but the media raw route)
- **G15** `gCertDecides`: **structural**: past the handshake, the decision depends on the certificate or on whether a client CA was asked for (four comparisons: no CA and no certificate, against a verified certificate, a certificate that did not verify and was let through, and a request that reached the gate with no certificate under a client CA, the way the swarm reverse tunnel delivers one)
- **G16** `gNoCertClass`: **structural**: a request was served under a certificate's class (the model has none)
- **G17** `gOracle`: the whole decision, `(result, class)`, differs from a table of the code written by cases (the handshake, a transparent gate, then the ordered credentials) instead of by the chain the model decides with: it is what catches a mutant that changes an outcome no other latch names (the status of the final refusal, the order of the cookie and the bearer, a public route behind the gate)

| mutant | what it changes | latches it violates |
|---|---|---|
| `CERTOPENS` | a verified certificate opens the shared routes (phase 3's `cert_names`, as a mutant) | G4, G11, G15, G16, G17 |
| `CERTMAIN` | a verified certificate opens every route | G4, G11, G15, G16, G17 |
| `CERTVETO` | a certificate that did not verify vetoes the other credentials | none (unobservable alone: a certificate that did not verify never passes the handshake in the code, so a veto on it has no input to act on; it needs `UNVERIFIEDOK`) |
| `CERTVETO+UNVERIFIEDOK` | the same, with the leaky handshake that lets such a certificate through | G3, G5, G9, G14, G15, G17 |
| `UNVERIFIEDOK` | the listener asks for a certificate without verifying it | G5 |
| `NOHANDSHAKE` | a peer with no certificate is let in under a client CA | G5 |
| `SHAREDALL` | a shared token opens every route | G2, G17 |
| `DUPMAIN` | a token configured as both classes is the main one | G7, G17 |
| `ANONSHARED` | the shared handlers serve an anonymous caller of a node with no credential | G4, G17 |
| `DUPDROP` | a token configured as both classes is refused on the shared routes | G9, G17 |
| `COOKIENOSHARED` | a live browser session is refused on the shared routes | G14, G17 |
| `INSECUREREFUSED` | a node with no credential and `allow_insecure` refuses the shared routes | G13, G17 |
| `WRONGOK` | a wrong token is read as the main token | G11, G17 |
| `SHAREDSTATS` | the gate's route test also says the counters route | G2, G17 |
| `DROPPING` | the route test forgets the ping | G9, G17 |
| `DROPUSAGE` | the route test forgets the usage route | G9, G17 |
| `MAINDROP` | the main bearer opens nothing (a node's own token refused) | G3, G17 |
| `REFUSAL403` | the final refusal is a 403, not a 401 (the web UI's sign-in redirect and `WWW-Authenticate` need the 401) | G17 |
| `COOKIEFIRST` | the cookie is judged before the main bearer (the class of a request with both changes, and the cookie's CSRF check could refuse a legitimate bearer request) | G17 |
| `PUBLICGATED` | the public routes (the sign-in routes, the SPA shell) are behind the gate on a node that has a credential: a lockout | G17 |
| `REQTLS` | a gate that refuses a request with no certificate under a client CA, once the handshake has passed (the swarm tunnel delivers such requests: it would break the relay) | G15 |
| `GATESHARED` | a shared token does not turn the gate on (`authPolicyNow` forgot it): a node that holds only shared tokens is open | G4, G9, G11, G14, G17 |

The code satisfies every latch, and each mutant of the table is violated by the latches shown (`CERTVETO` is observable only with `UNVERIFIEDOK`). The two handshake mutants (`UNVERIFIEDOK`, `NOHANDSHAKE`) are G5's alone, since G17's first case takes them into account; `REQTLS` is G15's alone, since the oracle has no tunnel. Three mutants of the first version of this table, `REFUSAL403`, `COOKIEFIRST` and `PUBLICGATED`, passed every latch it then had: that is why G17 exists. The model also records what it does **not** claim: the CSRF check on a cookie's state-changing request, the media capability route, the SSE `access_token` query parameter, `public_docs`, CORS preflights, a cookie when the login is broken (accepted: wider), the limiter keys and the counters, the verification of the chain itself (`crypto/x509`: held by the end-to-end tests), and the exactness of "no connection" for plain HTTP sent to a TLS port, and the media raw route (`GET .../workspace/raw` with `?access_token=`), which `auth.go` decides before the enabled check and before every credential: an invalid capability is a 401 even on an open node or with a cookie, and a valid one is served with no credential, so G2, G3, G4, G11, G13, G14 and G17 hold on every route but that one. A request that arrives over the swarm reverse tunnel, or through a TLS terminator, carries no `r.TLS` and reaches the gate with no certificate under a client CA (`docs/features/shared-models.md`): G15's fourth comparison covers the gate's side of that (a gate that refused it, `REQTLS`, is caught); G5 does not claim it.
