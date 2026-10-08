# p3-d5-scope-routes: how the routes of a scoped client are decided at the relay (phase 3, dilemma D5)

Spec: `docs/plans/remote-model-provider-phase3.md` sections 3.2, 3.4, 10 (D5), 11 (H2a). Model: `p3-d5-scope-routes.pml` (this directory; the
header comment lists every switch). Engine: `mcd 0.2.0` CLI (`mcd-report/1`), 2026-10-08. The CLI has no `mc_simulate` and no `mc_lint_property`:
sanity is the reachability latches of section 3.2 and the deadlock check. A result is a statement about the model; the plan's code and the standard
library facts it cites were read, not run here (the plan says `http.ServeMux` was run for the spellings of section 1).

## 1. Intake card

- **Boundary.** One adversary request at a time against a relay decision and the node's routing function; one later step in which a release adds a
  route under `/coddy/llm/`. Outside: the principal and the node check of 3.2, the limits, the hops (D1), the body.
- **State.** `r4` (the added route exists), the ghost latches `gLeak gLegit gSpell` and the sanity latches `sLegit sR4 sAdmin sHead sSuffix`; a request
  is a class of the DECODED path and a spelling, and everything about it is a temporary cleared at the end of the step.
- **Classes.** 0 `GET /coddy/llm/models`, 1 `GET /coddy/llm/models/{alias}/usage`, 2 `POST /coddy/llm/completions` (the scope); 3 `POST /coddy/llm/embeddings`
  (the route a later release adds under `/coddy/llm/`; it exists only once the Registrar has run); 4 `GET /coddy/config`, 5 `GET /coddy/sessions` and 6
  `GET /coddy/llm-admin` (outside the scope; 6 begins with the string `/coddy/llm` and not with `/coddy/llm/`).
- **Spellings.** plain; encoded (a percent-encoded letter: decoded before the node routes it, so the node answers it as the plain path); a trailing slash
  and `;x=1` (the node routes them nowhere); a dot segment (the relay's `mountRemainder` refuses it for every rule, so it is refused here); HEAD (on a GET
  route: the node routes it to the GET handler).
- **Node.** `nodeRoutes`: the handler that answers, or none. **Relay.** `relayDecides`: the rule of the option, on the decoded path.
- **Assumptions.** A1 the relay judges the decoded remainder (the real mount does); A2 the node's routing facts of the plan, above, hold; A3 the operator of
  option (c) writes one of four things (PATV): the three routes spelled out, the atom `/coddy/llm/*`, a typo `/coddy/*`, or `/coddy/llm*`; A4 `RAW = 1` is a
  defect of any option, not an option: the rule compares the raw spelling, so every encoded request is refused (coarse by design). **Bounds.** Seven classes,
  six spellings, one added route.

Mapping (model -> code): option (a) = `sharedRoute` of 3.2, an exact match on the decoded route with the entry's method; (b) = the style of `mountedPrefixes`;
(c) = a per-entry route list the operator writes; `r4` = a route a later release adds, the thing the comment of `mountedPrefixes` warns about.

## 2. Properties

| id | latch | meaning |
|---|---|---|
| R1 | `gLeak` | no admitted request is routed by the node to a handler outside the three routes of the scope, at any step (r4 once the Registrar has added it) |
| R2 | `gLegit` | a legitimate shared-model call (one of the three routes with its own method, plain or encoded) is admitted |
| R3 | `gSpell` | the decision is by the decoded path: a plain request and its encoded spelling are decided alike |
| sanity | `sLegit sR4 sAdmin sHead sSuffix` | a legitimate call is admitted; a request reaches r4; a request reaches `/coddy/config`, `/coddy/sessions` or `/coddy/llm-admin`; a HEAD is admitted; a path with a trailing slash or `;x=1` is admitted |

Safety is checked as a state assertion, one property per run (`-D ASSERT_PROP=<latch>`); the sanity latches are "violated" by design, which says reachable.

## 3. Results

**3.1 The rules.** `REG = 1` means a later release has added `r4`; `REG = 0` is today, with no `r4` (the control: what each rule admits now). 192 runs
(six rules x RAW x REG x eight latches), all decided.

| rule | R1 `gLeak` (REG=1) | R1 today (REG=0) | R2 `gLegit` | R3 `gSpell` | R2 / R3 with RAW=1 |
|---|---|---|---|---|---|
| (a) exact table | ok | ok | ok | ok | **VIOL** / **VIOL** |
| (b) prefix `/coddy/llm/` | **VIOL** | ok | ok | ok | **VIOL** / **VIOL** |
| (c) patterns, PATV 0: the three routes spelled out | ok | ok | ok | ok | **VIOL** / **VIOL** |
| (c) PATV 1: `/coddy/llm/*` | **VIOL** | ok | ok | ok | **VIOL** / **VIOL** |
| (c) PATV 2: typo `/coddy/*` | **VIOL** | **VIOL** | ok | ok | **VIOL** / **VIOL** |
| (c) PATV 3: `/coddy/llm*` | **VIOL** | **VIOL** | ok | ok | **VIOL** / **VIOL** |

**3.2 Sanity (reachability), REG = 1, RAW = 0.**

| rule | `sLegit` | `sR4` | `sAdmin` | `sHead` | `sSuffix` |
|---|---|---|---|---|---|
| (a) exact table | reachable | not reachable | not reachable | not reachable | not reachable |
| (b) prefix `/coddy/llm/` | reachable | reachable | not reachable | reachable | reachable |
| (c) patterns, PATV 0: the three routes spelled out | reachable | not reachable | not reachable | reachable | not reachable |
| (c) PATV 1: `/coddy/llm/*` | reachable | reachable | not reachable | reachable | reachable |
| (c) PATV 2: typo `/coddy/*` | reachable | reachable | reachable | reachable | reachable |
| (c) PATV 3: `/coddy/llm*` | reachable | reachable | reachable | reachable | reachable |

(`sHead` is reachable for the prefix and for the patterns because they are method-blind and the node routes a HEAD to a GET handler; it is harmless, since the
handler is one of the three. `sAdmin` is reachable for exactly the typos.)

## 4. Reading

- **(a) the exact table** holds R1 (also after `r4` exists), R2 and R3, admits no path with a suffix and no HEAD. A new route under `/coddy/llm/` is not admitted until a
  release adds it to the table.
- **(b) the prefix** violates R1 from the release that adds `r4`: it admits `POST /coddy/llm/embeddings` the day it exists, for every client already holding a scope.
  Today (REG = 0) it leaks nothing; that is exactly the case the plan's comment on `mountedPrefixes` is about.
- **(c) the patterns** hold R1 only when the operator spelled the three routes out (PATV 0): with the atom `/coddy/llm/*` they behave as (b); a typo
  `/coddy/*` or `/coddy/llm*` opens the other routes of the node at once (`sAdmin`), today and not only after a release. The rule that is safe is the one that
  has no prefix atom, which is (a) with the entries the operator writes.
- **The raw spelling** violates R2 and R3 under every rule: an encoded legitimate call is refused, so the decision must be by the decoded path (the real mount does).

## 5. Commands (reproduce)

`M=docs/plans/remote-model-provider-models/p3-d5-scope-routes.pml`:

```
mcd check --promela $M -D OPT=1 -D ASSERT_PROP=gLeak --no-timing               # verified: the exact table
mcd check --promela $M -D OPT=2 -D ASSERT_PROP=gLeak --no-timing               # violated: the prefix, after r4
mcd check --promela $M -D OPT=2 -D REG=0 -D ASSERT_PROP=gLeak --no-timing      # verified: the prefix today
mcd check --promela $M -D OPT=3 -D PATV=1 -D ASSERT_PROP=gLeak --no-timing     # violated: the atom `/coddy/llm/*`
mcd check --promela $M -D OPT=3 -D PATV=2 -D REG=0 -D ASSERT_PROP=gLeak --no-timing   # violated: the typo, today
mcd check --promela $M -D OPT=1 -D RAW=1 -D ASSERT_PROP=gLegit --no-timing     # violated: the raw spelling
```

Switches: `OPT` 1..3, `PATV` 0..3 (with `OPT=3`), `RAW` 0/1, `REG` 0/1. The model has under a dozen states; the whole table runs in seconds.

## 6. Statements

- CONFIRMED: a closed table matched exactly on the decoded path holds R1, R2 and R3, also after a release adds a route under `/coddy/llm/`.
- CONFIRMED: a prefix on `/coddy/llm/` violates R1 from the release that adds a route under it (and not before).
- CONFIRMED: per-entry patterns hold R1 only if no entry has a prefix or wildcard atom; the typos `/coddy/*` and `/coddy/llm*` open admin routes today.
- CONFIRMED: the decision must be made on the decoded path (R2 and R3 fail on the raw one).
- NOT DECIDABLE here: routes whose method is not GET or POST, a node route registered by an extension, an `HTTP/2` or proxy path rewrite before the relay.

## 7. Decision

**Option (a): the scope is a closed class whose routes the code enumerates, an exact match on the decoded remainder with the route's method.** `GET /coddy/llm/models`,
`GET /coddy/llm/models/{alias}/usage` with `config.ValidSharedAlias(alias)`, `POST /coddy/llm/completions`; nothing else, HEAD and OPTIONS included. A new
scoped route is a code change with its own review, not something a prefix or a pattern admits by default.

## 8. Recommended spec changes (`docs/plans/remote-model-provider-phase3.md`)

- Section 10, D5: mark closed, with the verdict and this report; the expectation of the plan stands ("(b) violates R1 from step k, (c) holds only if its grammar has no
  prefix atom, (a) holds").
- Section 3.2: no change of text: `sharedRoute` is the closed table, with the decoded comparison and the spellings listed. Add to the tests of 3.5 the cases of
  this model: an added route under `/coddy/llm/` is refused for a scoped client until the table lists it, and a path with a trailing slash or `;x=1` is refused.
- Section 11, H2a: no longer waits for D5.

## 9. Not done, and what the review of the model changed

- **Cross-review.** The model was read by two reviewers (`gpt-6.1-sol` and `gemma-4-31b`; `qwen3.8-27b` was cut off at its output limit) and every finding was
  checked by a run. One real defect was found and corrected: the rejection of a path with a suffix (rules (a) and (c) with PATV 0) was a sibling branch of the
  admitting branches, so the engine could take either; it is nested in an `else` now, and a sanity latch `sSuffix` was added that shows the fix (unreachable for
  the rules that spell the routes out). All 168 earlier verdicts are unchanged. Not changed, after a check: the coarse `RAW` (now described as a control in the header),
  `r4` counted as a leak (the header says so), and the HEAD spelling being rewritten for POST routes (the exact table refuses it anyway).
- **Not asked.** A client that sends a method other than GET, POST or HEAD; more added routes; a route added under a different prefix.
