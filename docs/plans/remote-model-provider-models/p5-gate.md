# p5-gate: every credential against every route

Model `p5-gate.pml` (Promela; a decision procedure, an ordered chain as the code is, explored over every combination of its inputs in the nondeterministic choices of `init`: 5 bearers x 6 certificates x 2 cookies x 3 client-CA modes x 7 routes x 5 configuration bits, filtered to the consistent ones by three rewrites: a bearer of a class the node does not have is a wrong token, a name listed in an empty list is not listed, and a certificate needs a client CA; this leaves 17,472 distinct inputs, and the 34,945 states of every run are 2 x 17,472 + 1, which an independent count confirms). Engine `mcd` 0.3.1. Code: `external/httpserver/auth.go` (`authGate`, `authPolicyNow`, `anonymousSharedRefused`), `tls.go` (`sharedCertName`), `internal/netx` (`ClientCertTLS`, `CertificateNames`). It exists because stage H7 added a credential class (a verified client certificate named in `cert_names`) next to the main token, the shared token and the cookie.

Commands (`M=p5-gate.pml`; every run is exhaustive; `deadlock` is verified in every default run):

```
# the default configuration: the code, every latch
mcd check --promela $M --no-timing -D ASSERT_PROP=gCertOpensOther                                   # verified (34,945 states), G1
mcd check --promela $M --no-timing -D ASSERT_PROP=gSharedOpensOther                                 # verified (34,945 states), G2
mcd check --promela $M --no-timing -D ASSERT_PROP=gMainRefused                                      # verified (34,945 states), G3
mcd check --promela $M --no-timing -D ASSERT_PROP=gAnonServed                                       # verified (34,945 states), G4
mcd check --promela $M --no-timing -D ASSERT_PROP=gHandshake                                        # verified (34,945 states), G5
mcd check --promela $M --no-timing -D ASSERT_PROP=gNoIdentity                                       # verified (34,945 states), G6
mcd check --promela $M --no-timing -D ASSERT_PROP=gDupMain                                          # verified (34,945 states), G7
mcd check --promela $M --no-timing -D ASSERT_PROP=gClassOrder                                       # verified (34,945 states), G8
mcd check --promela $M --no-timing -D ASSERT_PROP=gSharedRefused                                    # verified (34,945 states), G9
mcd check --promela $M --no-timing -D ASSERT_PROP=gCertRefused                                      # verified (34,945 states), G10
mcd check --promela $M --no-timing -D ASSERT_PROP=gWrongServed                                      # verified (34,945 states), G11
mcd check --promela $M --no-timing -D ASSERT_PROP=gClsExact                                         # verified (34,945 states), G12
mcd check --promela $M --no-timing -D ASSERT_PROP=gOpenRefused                                      # verified (34,945 states), G13
mcd check --promela $M --no-timing -D ASSERT_PROP=gCookieRefused                                    # verified (34,945 states), G14
# each mutant, against the latch it is written for (every other latch: see the table below)
mcd check --promela $M --no-timing -D ASSERT_PROP=gCertOpensOther -D CERTMAIN=1                     # violated (34,945 states), G1
mcd check --promela $M --no-timing -D ASSERT_PROP=gSharedOpensOther -D SHAREDALL=1                  # violated (34,945 states), G2
mcd check --promela $M --no-timing -D ASSERT_PROP=gDupMain -D DUPMAIN=1                             # violated (34,945 states), G7
mcd check --promela $M --no-timing -D ASSERT_PROP=gNoIdentity -D NOLISTCHECK=1                      # violated (34,945 states), G6
mcd check --promela $M --no-timing -D ASSERT_PROP=gNoIdentity -D EXPIREDOK=1                        # violated (34,945 states), G6
mcd check --promela $M --no-timing -D ASSERT_PROP=gNoIdentity -D NAMELESSOK=1                       # violated (34,945 states), G6
mcd check --promela $M --no-timing -D ASSERT_PROP=gNoIdentity -D UNVERIFIEDOK=1                     # violated (34,945 states), G6
mcd check --promela $M --no-timing -D ASSERT_PROP=gHandshake -D NOHANDSHAKE=1                       # violated (34,945 states), G5
mcd check --promela $M --no-timing -D ASSERT_PROP=gAnonServed -D GATEIGNORESCERT=1                  # violated (34,945 states), G4
mcd check --promela $M --no-timing -D ASSERT_PROP=gAnonServed -D ANONSHARED=1                       # violated (34,945 states), G4
mcd check --promela $M --no-timing -D ASSERT_PROP=gClassOrder -D CERTFIRST=1                        # violated (34,945 states), G8
mcd check --promela $M --no-timing -D ASSERT_PROP=gMainRefused -D BADCERTFIRST=1                    # violated (34,945 states), G3
mcd check --promela $M --no-timing -D ASSERT_PROP=gWrongServed -D WRONGOK=1                         # violated (34,945 states), G11
mcd check --promela $M --no-timing -D ASSERT_PROP=gCertRefused -D NOCERTONPING=1                    # violated (34,945 states), G10
mcd check --promela $M --no-timing -D ASSERT_PROP=gCertOpensOther -D SHAREDSTATS=1                  # violated (34,945 states), G1
mcd check --promela $M --no-timing -D ASSERT_PROP=gSharedRefused -D DROPPING=1                      # violated (34,945 states), G9
mcd check --promela $M --no-timing -D ASSERT_PROP=gSharedRefused -D DROPUSAGE=1                     # violated (34,945 states), G9
mcd check --promela $M --no-timing -D ASSERT_PROP=gSharedRefused -D DUPDROP=1                       # violated (34,945 states), G9
mcd check --promela $M --no-timing -D ASSERT_PROP=gCookieRefused -D COOKIENOSHARED=1                # violated (34,945 states), G14
mcd check --promela $M --no-timing -D ASSERT_PROP=gOpenRefused -D INSECUREREFUSED=1                 # violated (34,945 states), G13
# sanity: each outcome is reachable (violated); the last must NOT be: the decision is ordered
mcd check --promela $M --no-timing -D ASSERT_PROP=sMain                                             # violated (34,945 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sShared                                           # violated (34,945 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sCert                                             # violated (34,945 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sCookie                                           # violated (34,945 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sOpen                                             # violated (34,945 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=s401                                              # violated (34,945 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=s403                                              # violated (34,945 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sHand                                             # violated (34,945 states)
mcd check --promela $M --no-timing -D ASSERT_PROP=sCertOverShared                                   # verified (34,945 states)
```

## Verdicts

In the model of the code:

- **G1** a certificate alone opens only the four shared routes - holds (34,945 states).
- **G2** a shared token alone opens only the shared routes - holds (34,945 states).
- **G3** the main token is never refused by the gate on a node that has a credential (on every route but the media raw route, which the gate decides first) - holds (34,945 states).
- **G4** no credential, no service (a node with none and no allow_insecure refuses the shared routes), on every route but the media raw route - holds (34,945 states).
- **G5** under required, a peer with no certificate never reaches the gate, and under either mode a certificate that does not verify never does, **on the direct listener** (a request over the reverse tunnel has no `r.TLS`) - holds (34,945 states).
- **G6** an unlisted, expired, nameless or non-verifying certificate is given no class - holds (34,945 states).
- **G7** a token configured as both classes is never classed as the main one, on any route - holds (34,945 states).
- **G8** a certificate never lowers a main token's class - holds (34,945 states).
- **G9** a configured shared token is served on every shared route, once the handshake has passed (a peer that fails it never reaches the gate: G5) - holds (34,945 states).
- **G10** a listed verified certificate is served on every shared route, once the handshake has passed - holds (34,945 states).
- **G11** a request with no valid credential (no token or a wrong one, no cookie, no listed certificate on a shared route) is not served on a protected route of a node that has a credential, on every route but the media raw route - holds (34,945 states).
- **G12** a shared bearer (or a token of both classes) with a listed certificate on a shared route is classed shared (the code's order) - holds (34,945 states).
- **G13** a node with no credential serves what it offers: every route, and the shared ones when `allow_insecure` is set - holds (34,945 states).
- **G14** a live browser session is served on every route of a node that has a credential, once the handshake has passed - holds (34,945 states).

Every mutant is caught by the latch it was written for, and several also by others (the cross matrix of all 20 mutants against all 14 latches is in the last column; a mutant that is caught by no latch would be a hole):

| mutant | what it changes | designed latch | caught by |
|---|---|---|---|
| `CERTMAIN` | a listed certificate opens every route | G1 | G1, G11 |
| `SHAREDALL` | a shared token opens every route | G2 | G2 |
| `DUPMAIN` | a token of both classes is read as the main one | G7 | G7, G12 |
| `NOLISTCHECK` | any verified certificate passes | G6 | G6, G11 |
| `EXPIREDOK` | a leaf that expired on an open connection keeps its name | G6 | G6, G11 |
| `NAMELESSOK` | a certificate with no DNS or URI name passes as listed | G6 | G6, G11 |
| `UNVERIFIEDOK` | a compound regression: the listener asks for a certificate without verifying it and the identity is read from `PeerCertificates`, so under optional a certificate that does not verify passes and is read as listed (`PeerCertificates` alone could not admit it: `VerifyClientCertIfGiven` fails the handshake first) | G6 | G5, G6, G11 |
| `NOHANDSHAKE` | required lets a peer with no certificate in | G5 | G5 |
| `GATEIGNORESCERT` | a node whose only credential is `cert_names` counts as open | G4 | G4, G10, G11, G14 |
| `ANONSHARED` | the shared handlers serve an anonymous caller of a node with no credential | G4 | G4 |
| `CERTFIRST` | a listed certificate is judged before the main bearer | G8 | G8, G12 |
| `BADCERTFIRST` | a certificate that gives no identity vetoes the other credentials | G3 | G3, G9, G14 |
| `WRONGOK` | a wrong token is read as the main token | G11 | G11 |
| `NOCERTONPING` | the probe's ping does not take a listed certificate | G10 | G10 |
| `SHAREDSTATS` | the route test also says the counters route (a new route added to the shared set) | G1 | G1, G2, G11 |
| `DROPPING` | the route test forgets the ping | G9 | G9, G10, G12 |
| `DROPUSAGE` | the route test forgets the usage route | G9 | G9, G10, G12 |
| `DUPDROP` | a token configured as both classes is not accepted on the shared routes | G9 | G9, G12 |
| `COOKIENOSHARED` | a live browser session does not open the shared routes | G14 | G14 |
| `INSECUREREFUSED` | a node with no credential and `allow_insecure` refuses the shared routes | G13 | G13 |

**What the latches cannot see.** G8 is invisible at the gate: both orders of a main token and a listed certificate serve the request, and the class matters only in `sharedClassOf` (`shared_audit.go`) and `sharedCallerKey` (`shared_models_http.go`), which are two more copies of the order that the model has no variable for; they agree with the gate today. The shared-route set has four members by truth; a regression of `isSharedLLMPattern` that widens it (`SHAREDSTATS`) or narrows it (`DROPPING`, `DROPUSAGE`) is caught, but only because the properties read the truth, not the gate's route test.

## Not modelled

The cookie's CSRF check on a state-changing request (a refusal that only narrows); the media capability route (`GET .../workspace/raw` with `access_token`), which the gate decides before the `enabled` check and before the bearer, so a valid main token with an invalid capability is a 401 and a valid capability is served with no other credential: G3 and G4 hold on every route but that one; the SSE `access_token` query parameter; `public_docs`; CORS preflights, answered before the gate with a 204 and no data; a cookie when the login is broken (the model accepts it: wider); the limiter keys and counters (their separation is by construction: the key of a bearer, of a certificate name and of a cookie are different strings); the verification of the chain itself, which is `crypto/x509` and is held by the end-to-end tests of `docs/operate/certificates.md` (`examples/tls`, `internal/netx` scenarios); **the tunnel**: a request that arrives over the reverse tunnel is served on an `activityConn`, not a `*tls.Conn`, so it has no `r.TLS` and under `client_auth: required` it reaches the gate with no certificate (`docs/features/shared-models.md` says so; the relay authenticates the tunnel instead): G5 is a claim about the direct listener.

## Found while modelling

Nothing in the code. The model needed two corrections that the review found: the properties read the same route classification the decision reads, so a regression of the route test moved the property with it (the same flaw as `gateOn`, fixed first by `credNode` and then, in the second round, by `shared_route` and `gshared`); and the decision was not an ordered function (its inner guards were not exclusive, so `result` was right and the class an over-approximation). Both are fixed; the model now has latches for the other direction (a configured credential is served: G9, G10), for a wrong token (G11) and for the class order (G12).

## Review

**Round 1** (two reviewers answered, a third gave an empty reply twice; every claim re-run, the whole first matrix reproduced). Verdict: approve with changes; no property turned violated for the code and no code defect was found. Changed: the route classification is a truth and a reading (`gshared`) with three mutants that move it (the properties were blind to a regression of `isSharedLLMPattern`); the property set was one-sided: G9 to G11, and `WRONGOK` and `NOCERTONPING`; the decision is an ordered chain (G12, `sCertOverShared`), which also makes the state counts one number; G6 covers the non-verifying certificate and `UNVERIFIEDOK`, `NAMELESSOK` are added; the cookie is no longer tied to a login (a Telegram Mini App session is a cookie without one) and a name listed in an empty list is normalised; the verdicts say "in the model of the code", G3 and G4 carry the media-route qualifier and G5 the direct-listener one, and the copies of the order in `sharedClassOf` and `sharedCallerKey`, the CORS preflight and the tunnel are in Not modelled. Rejected with evidence: that the verdicts are unsound because of the model's nondeterminism (only the class was over-approximated, and the result is deterministic), and that no defect exists because the first reviewer said so (three mutants were undetected).

**Open for the operator.** Is `client_auth: required` meant to leave tunnel traffic unconstrained by a client certificate? The documentation says the relay authenticates the tunnel instead; this is read as intended.

**Round 2** (the brief was 38 KB and stalled two of the three reviewers; one answer came back whole, one only by recovery from its session and with no finding, and the orchestrator re-ran the whole first matrix and compared the decision with the code step by step, so the review rests on one reviewer and that verification). Verdict: approve with changes; the model is the same decision function as the code in the same order, no verdict changed and no code defect was found. Changed: G7 required a non-shared route, so a token of both classes classed as the main one on a shared route did not trip it (it holds on any route now, and G12 covers the token of both classes); G11 had the media-route qualifier only on G3 and G4; G9 and G10 now say they hold once the handshake has passed; G5 covers a certificate that does not verify; the description of `UNVERIFIEDOK` was wrong (a compound regression, see the table); two directions no latch asserted are now G13 (an open node refuses nothing it offers) and G14 (a live session is served), with the mutants `INSECUREREFUSED`, `COOKIENOSHARED` and `DUPDROP`; the preface names the third rewrite of the inputs and the count; a stale comment in the code (`isSharedLLMPattern` listed three of its four routes) is corrected. Rejected with evidence: that an overlapping-token regression is uncovered (`DUPDROP` is violated by G9), that the model is not the whole decision function (compared step by step with `authGate`, `authPolicyNow`, `anonymousSharedRefused`, `sharedCertName`, `CertificateNames` and `ClientCertTLS`), and that the engine output was missing (every run reproduced).
