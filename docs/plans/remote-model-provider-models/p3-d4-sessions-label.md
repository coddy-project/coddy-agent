# p3-d4-sessions-label: what the node label does to the relay's aggregated sessions list (phase 3, dilemma D4)

Spec: `docs/plans/remote-model-provider-phase3.md` sections 7, 10 (D4), 11 (H3). Model: `p3-d4-sessions-label.pml` (this directory; the header comment lists every
switch). Engine: `mcd 0.2.0` CLI (`mcd-report/1`), 2026-10-08. A result is a statement about the model; the code of the plan was read, not run.

## 1. Intake card

- **Boundary.** One aggregated sessions call of a relay over three online nodes, each described by what its sessions route answers and whether the relay treats it as
  labelled. Outside: offline nodes (the relay warns `offline since ...` before the fan-out and whatever the label says), chained relays, the order of the answers.
- **State.** Per node `ans` (0 rows, 1 a 401, 2 a server error, 3 unreachable), `lab` (the relay's predicate `SharedModelsOnly`: an agent with the exact key and value),
  `listed`, `warned`, `asked`; the ghost latches `gV1 gV2 gV3 gV4 gV5` and the sanity latches `sListed sWarned sSilenced sAsked`. The first step enumerates all
  4^3 x 2^3 combinations.
- **Modes.** MODE 1 (a): a labelled node is not asked, it produces no row and no warning. MODE 2 (b): every node is asked; a labelled node's 401 is swallowed, every
  other outcome is as for any node (the implementation of `sessions.go`).
- **Assumptions.** A1 a current node sets the label exactly when its token is a shared-model token, so a label with the answer `rows` is a mislabel (a hand-written
  value, an older node, a token changed between two renewals) and an unlabelled 401 is a node the label was meant to silence and could not (an older node, an empty or
  stale token); A2 a server error and a transport error warn as for every node.

## 2. Properties

| id | latch | meaning |
|---|---|---|
| V1 | `gV1` | every node whose sessions a full client could read without the label (answer rows) is listed |
| V2 | `gV2` | every genuine warning of a node without the label still appears |
| V3 | `gV3` | a labelled node whose token opens only the shared-model routes (label, 401) never warns |
| V4 | `gV4` | (cost) the relay makes no request to a labelled node that would answer a 401 |
| V5 | `gV5` | the label silences the refusal and nothing else: a labelled node that answers a server error, or is unreachable, warns |

## 3. Results

Each property is one run per mode over all 2744 combinations, safety as a state assertion.

| mode | V1 | V2 | V3 | V4 | V5 |
|---|---|---|---|---|---|
| (a) skip a labelled node | **VIOL** | ok | ok | ok | **VIOL** |
| (b) ask, swallow only a 401 | ok | ok | ok | **VIOL** | ok |

Sanity: `sListed`, `sWarned`, `sSilenced`, `sAsked` are reachable in both modes. V2 and V3 hold in both modes, so they separate nothing: they are regression guards.

- **(a) skip** violates V1: a node that carries the label and holds a main token (a mislabel) vanishes from the list without a trace. It also violates V5, which the
  plan did not name: a labelled node that fails with a server error or is unreachable warns nothing, because the relay never asks it, so a real failure is hidden by
  a label whose only job is to silence a refusal.
- **(b) ask and swallow only a 401** holds V1, V2, V3 and V5 and pays V4: one request per labelled node per call, and a 401 in its log.

## 4. Counterexamples, classified

- **(a), V1.** Node: answer rows, label. The relay does not ask it; its sessions are missing from the list although a full client could read them.
- **(a), V5.** Node: answer server error (or unreachable), label. The relay does not ask it; no warning names the node.
- **(b), V4.** Node: answer 401, label. The relay asks it and swallows the answer: no warning, one request.

## 5. Commands (reproduce)

`M=docs/plans/remote-model-provider-models/p3-d4-sessions-label.pml`:

```
mcd check --promela $M -D MODE=1 -D ASSERT_PROP=gV1 --no-timing    # violated: skip, a mislabelled node vanishes
mcd check --promela $M -D MODE=1 -D ASSERT_PROP=gV5 --no-timing    # violated: skip, a failing labelled node is silent
mcd check --promela $M -D MODE=2 -D ASSERT_PROP=gV1 --no-timing    # verified
mcd check --promela $M -D MODE=2 -D ASSERT_PROP=gV4 --no-timing    # violated: the cost
```

## 6. Statements

- CONFIRMED: asking the node and swallowing only a 401 holds V1, V2, V3 and V5.
- CONFIRMED: skipping a labelled node violates V1 for a mislabelled node.
- CONFIRMED (new): skipping violates V5 too: a labelled node that fails is not reported.
- CONFIRMED: the cost of (b) is one request per labelled node per call (V4).
- NOT DECIDABLE here: a node that loses its label between two calls, a chained relay's own sessions warnings (a relay is never labelled), the order of the answers.

## 7. Decision

**Option (b): the relay asks every online node and swallows only the 401 of a labelled agent.** This is how `external/swarm/sessions.go` already behaves (H3). The plan's
rule stands: the label is the node's own claim, used for this one purpose, and `coddy -t` warns about a hand-written label whose token is not a shared-model token.

## 8. Recommended spec changes (`docs/plans/remote-model-provider-phase3.md`)

- Section 10, D4: mark closed, with the verdict and this report. The text of section 7 says "leaves a node ... out of `fanOut`: no request"; the implementation and the
  decision are the other way round (the node is asked and only its 401 is swallowed): rewrite that bullet and the test line of section 7 accordingly.
- Section 11, H3: no longer waits for D4.

## 9. Not done, and what the review of the model changed

- **Cross-review.** The model was read by three reviewers, all answered; every finding was checked by a run. No verdict changed. The header now says what `lab` means
  (the relay's predicate: an agent with the exact key and value), that an offline node is outside both modes, and that V2 and V3 hold in both modes by design.
- **Open.** How a chained relay reports the warnings of its own sessions call: irrelevant while only agents can carry the label.
