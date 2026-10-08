# p3-d2-mtls-identity: how a verified client certificate relates to the bearer at the relay (phase 3, dilemma D2)

Spec: `docs/plans/remote-model-provider-phase3.md` sections 4.1, 4.2, 10 (D2), 11 (H2a). Model: `p3-d2-mtls-identity.pml` (this directory; the header
comment lists every switch). Engine: `mcd 0.2.0` CLI (`mcd-report/1`), 2026-10-08. The CLI has no `mc_simulate` and no `mc_lint_property`: sanity is the
reachability latches of section 3.2 and the deadlock check. A result is a statement about the model; the standard-library facts it relies on (below) are
quoted from the plan, not re-run.

## 1. Intake card

- **Boundary.** One relay listener, two entries and the full class, an attacker holding any subset of their credentials, an operator who revokes once (the name
  of entry 0 and the whole of entry 1), two connections each a handshake and any number of requests. Outside: the node (it never sees a certificate: the relay
  replaces the credential), the scope check (D1, D5), the limits.
- **State.** `rev` (0 before the revocation, 1 after), the attacker's holdings `hT0 hT1 hF hK0 hK1 hK2` (chosen at the start, so one run covers all 64
  subsets), per connection its certificate, a resumed flag, the cached identity and the revocation epoch the cache was taken at; the ghost latches
  `gS1 gS1k gS2 gS3 gS4 gS5 gL1 gL1b` and the sanity latches `sE0 sE1 sFull sStale sRevRefuse`.
- **Atomic step.** A handshake (the certificate chosen, resumed or not); one request (a bearer chosen, the principal decided, the fresh twin decided, the
  latches updated); the revocation.
- **Class.** Finite, untimed, interleaving. A request of an open connection may come before or after the revocation, which is the S2 question.
- **Assumptions.** A1 a certificate is verified at the handshake against the CA; its names map to an entry through the relay's own mapping, read from the
  LIVE configuration at each request in options (a) and (b); A2 on a resumed connection the verified chain of the original session is restored (Go does not
  re-run `VerifyPeerCertificate`), so a per-request reader still sees the certificate and a handshake-time cache holds the identity of the ticket;
  A3 the bearer is checked live against the configuration in every option; A4 a certificate never opens the full class, except in the mutant MUT 1.
  **Bounds.** Two entries, three certificates (K0 maps to entry 0, K1 to entry 1, K2 to nothing), one revocation, two connections; E1MODE 0 (a bearer entry)
  and 1 (a certificate entry).

Mapping (model -> code): `decide` = `authGate`'s principal of 4.2; `mapCert` = the relay's `cert_names` match on the live configuration; OPT 3's cache = a
value stored in the connection by `VerifyPeerCertificate`; the fresh twin = a new connection of the same certificate and bearer now.

## 2. Properties

| id | latch | meaning |
|---|---|---|
| S1 | `gS1`, `gS1k` | with only a stolen token (no K0), or only a stolen key (no T0), no request is admitted on e0, which binds both |
| S2 | `gS2` | an open connection opened before the revocation is judged by the live configuration: a request is not admitted unless a fresh connection of the same certificate and bearer would be admitted now |
| S3 | `gS3` | a chain-valid certificate with no entry (K2), alone, reaches no scoped route |
| S4 | `gS4` | the full class is not reachable by a certificate |
| S5 | `gS5` | a resumed connection is judged like a fresh one of the same certificate and bearer |
| L1 | `gL1` | a client holding both T0 and K0 is admitted on e0 before the revocation |
| cost | `gL1b` | a client that holds only the token of a bearer entry is not locked out by the listener |

Safety is checked as a state assertion, one property per run (`-D ASSERT_PROP=<latch>`).

## 3. Results

**3.1 The options.** 12 configurations (three options x E1MODE x the defect MUT), 13 latches each, 156 runs, all decided.

| option | E1MODE | `gS1` | `gS1k` | `gS2` | `gS3` | `gS4` | `gS5` | `gL1` | `gL1b` | `gS4` with the defect (MUT=1) |
|---|---|---|---|---|---|---|---|---|---|---|
| (a) a credential of its own | 0 | **VIOL** | **VIOL** | ok | ok | ok | ok | ok | ok | **VIOL** |
| (a) a credential of its own | 1 | **VIOL** | **VIOL** | ok | ok | ok | ok | ok | ok | **VIOL** |
| (b) per entry, read per request | 0 | ok | ok | ok | ok | ok | ok | ok | ok | **VIOL** |
| (b) per entry, read per request | 1 | ok | ok | ok | ok | ok | ok | ok | ok | **VIOL** |
| (c) listener-wide, cached at the handshake | 0 | ok | ok | **VIOL** | ok | ok | **VIOL** | ok | **VIOL** | **VIOL** |
| (c) listener-wide, cached at the handshake | 1 | ok | ok | **VIOL** | ok | ok | **VIOL** | ok | ok | **VIOL** |

- **(a) a credential of its own** violates S1: a stolen token alone, and a stolen key alone, are as good as both, so an entry cannot require both.
- **(b) per entry, read per request** holds every property, for a bearer entry and for a certificate entry. A revocation takes effect at the next request of an open
  connection, and a resumed connection is read as a fresh one.
- **(c) listener-wide, cached at the handshake** violates S2 (a name or an entry removed mid-connection still admits on the open connection) and S5 (a resumed
  connection takes the identity of its ticket). It also locks out the holder of a bearer-only entry (`gL1b`): `required` for everything means no certificate, no
  connection, for browsers and for joining nodes too.
- **S4 is not vacuous:** the defect "a certificate nobody mapped opens the full class" is caught by `gS4` in every option.

**3.2 Sanity** (option (b), E1MODE 0): `sE0` reachable, `sE1` reachable, `sFull` reachable, `sStale` reachable, `sRevRefuse` reachable.

## 4. Counterexamples, classified

The engine's counterexamples are long low-level traces; the classification derives from the design of the model and the verdict of the latch.

- **(a), S1.** The attacker holds T0 only: the token alone authenticates e0. Or K0 only: the certificate alone does.
- **(c), S2.** A connection opens with K0 before the revocation, the cache holds e0; the operator removes the name; the next request on the open connection,
  with T0, is admitted by the cache although a new connection of the same certificate would not be.
- **(c), S5.** A resumed connection takes the cache of its ticket, issued before the revocation; the same certificate on a fresh connection is refused.
- **(c), cost.** A holder of T1 alone has no certificate: the listener refuses the handshake, so the bearer is never read.

## 5. Commands (reproduce)

`M=docs/plans/remote-model-provider-models/p3-d2-mtls-identity.pml`:

```
mcd check --promela $M -D OPT=2 -D ASSERT_PROP=gS2 --no-timing                     # verified: (b)
mcd check --promela $M -D OPT=1 -D ASSERT_PROP=gS1 --no-timing                     # violated: (a), a stolen token
mcd check --promela $M -D OPT=3 -D ASSERT_PROP=gS2 --no-timing                     # violated: (c), a revoked name on an open connection
mcd check --promela $M -D OPT=3 -D ASSERT_PROP=gS5 --no-timing                     # violated: (c), a resumed connection
mcd check --promela $M -D OPT=2 -D MUT=1 -D ASSERT_PROP=gS4 --no-timing            # violated: the defect is caught
```

Switches: `OPT` 1..3, `E1MODE` 0/1, `MUT` 0/1. A run covers all 64 subsets of the attacker's holdings and takes seconds to a minute (about 500000 states); the
whole table takes about five minutes on 12 parallel workers.

## 6. Statements

- CONFIRMED: option (b), per entry with the identity read per request from `r.TLS` against the live configuration, holds S1 to S5 and L1.
- CONFIRMED: option (a) cannot require both credentials of an entry (S1): a stolen key is as good as a stolen token.
- CONFIRMED: option (c) keeps a revoked name or entry alive on an open connection (S2) and on a resumed one (S5), and locks out bearer-only holders.
- CONFIRMED: a certificate that maps to nothing reaches no scoped route (S3) in every option.
- NOT DECIDABLE here: more than two entries, several names per entry, a certificate past `NotAfter` mid-connection (4.2 reads that per request; the model has no
  certificate expiry), a CA rotation, HTTP/2 stream multiplexing on one connection (identical to one connection for these properties).

## 7. Decision

**Option (b): per entry, a token only, a certificate only, or both required and bound to the same entry, with the identity read per request from `r.TLS` against the
live configuration.** The listener mode stays `optional` or `required` by the operator as 4.2 says, but the identity is never cached in the connection: the mapping
and the entry's credentials are read at each request. A certificate never opens the full class.

## 8. Recommended spec changes (`docs/plans/remote-model-provider-phase3.md`)

- Section 10, D2: mark closed, with the verdict and this report; the expectation of the plan stands.
- Section 4.2: no change of text (it already says the match runs per request on the live configuration, and that an entry with both needs both). Add to the
  tests of 4.5 the cases of this model: a name removed from the configuration refuses the next request on an open connection; a resumed connection is judged
  by its request; a token alone and a key alone are each refused on an entry that binds both.
- Section 11, H2a: no longer waits for D2.

## 9. Not done, and what the review of the model changed

- **Cross-review.** The model was read by three reviewers in two parts (all three answered part 1; `gpt-6.1-sol` and `gemma-4-31b` part 2, `qwen3.8-27b` was cut off at
  its output limit) and every finding was checked by a run. One correction: the lockout at the handshake (`gL1b`) is decided before any request, so the assertion is
  now checked there too; no verdict changed in 156 runs. The header now says that option (a) admits "either credential" literally. Rejected after a check: caching the
  bearer in option (c) (only the certificate identity is cached), mapping the certificate at the ticket's epoch in (a) and (b) (the mapping is the relay's own, read live),
  and the dead `cOpen[]` state (it affects no property).
- **Open for the code.** Whether the real `authGate` ignores a wrong bearer when a valid certificate-only entry is presented: the model assumes yes for a
  certificate entry; to be settled when H2a is written.
