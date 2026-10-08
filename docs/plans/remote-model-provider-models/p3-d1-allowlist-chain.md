# p3-d1-allowlist-chain: what a client's node allowlist means when relays chain (phase 3, dilemma D1)

Spec: `docs/plans/remote-model-provider-phase3.md` sections 3.2, 3.4, 10 (D1), 11 (H2a). Model: `p3-d1-allowlist-chain.pml` (this
directory; the header comment lists every switch). Engine: `mcd 0.2.0` CLI (`mcd-report/1`), run on 2026-10-08 on a 32-core
host. The CLI has no `mc_simulate` and no `mc_lint_property`: sanity is the reachability properties of section 3.4 and the deadlock
check, properties were classified by hand. A result is a statement about the model and its bounds; the plan's code was read, not run.

## 1. Intake card

- **Boundary.** One scoped client of relay R1, the only relay that sees the client's class; a chain of up to four hops over a fixed
  three-relay world (R1, R2, R3, with a ring R2 <-> R3); one allowlist written in the grammar of the option. Outside: the node's own
  gate, limits, counters, mTLS, leases (a Registrar adds one node at any moment, which is a superset of every timing).
- **State.** The node tables (`kind`, `tgt`), the operator's intent `inI` over leaf ids, the written allowlist (`ent`, `nm1`, `nm3`,
  `starE`), the latches `rL[]` and `cov`, and the ghost latches `gRoute gRouteGate gOut gColl gTrav gDup gExact gSpell gFull gDead`
  and `sLLM sDeep sDyn sFull`. Everything per request lives in temporaries cleared at the end of the step.
- **Atomic step.** One request: the adversary chooses the principal, the chain (1 to 4 hop names), the route class and the spelling;
  the gate and the mount decide; the mount forwards with the next relay's full token; the model resolves the walk.
- **Class.** Finite, untimed, interleaving of a Client (every request) and a Registrar (one node added once). No clock is needed:
  every property is about which requests a rule admits and what they reach, not when.
- **Assumptions.** A1 a hop name is judged on the decoded remainder (the relay that gets an encoded hop refuses it in
  `mountRemainder`, so only the route's spelling can differ); A2 the closed route table of D5 admits the three shared-model routes
  or none, so they are one class `rt 0`; A3 from hop 2 on a request is judged by the legacy rule (every route but the control
  plane) because the child presents its full token (`joinset.go:72-74`); A4 node names repeat across relays (`n1` on R1 and R2,
  `m1` on R1 and R3, `child` on every relay) and a name is not an identity; A5 the operator's intent `I` is a set of leaves, and
  the allowlist is the tightest writing of `I` in the grammar of the option. **Bounds.** Six leaves (and two dynamic ones), chains of
  at most 4 hops, ten names; the verdicts of section 3 are over six intents of 64 (`1 4 5 20 34 40`, bit i = leaf i), chosen for one
  leaf, a name collision (`n1`), a prefix (`n1` / `n1x`), a second collision (`m1`) and a leaf behind two relays.

Mapping (model -> code): `h[]` = the hops of `/swarm/nodes/a/swarm/nodes/b/...` walked by `isSharedCompletions` (`mount.go:101-109`);
`scopedAdmit` = `entry.admits(hops)` of 3.2; `rt 0` = `sharedRoute` of 3.2; `kind/tgt` = the registry (`registry.go`, exact
case-sensitive `Node(name)`); the forward with the next relay's full token = `rewriteFor` and `joinset.go:72`; the options (a) to (d) are
the four meanings of `nodes` in D1; `MUT` 1 to 5 are defects of option (b).

## 2. Properties

| id | latch | meaning |
|---|---|---|
| P1 | `gRoute` | no admitted scoped request carries a non-LLM route to a node, at any hop |
| P1g | `gRouteGate` | the same at the gate: no admitted scoped request carries a non-LLM route at all |
| P2 | `gOut` | no admitted scoped request reaches, with an LLM route, a leaf outside the operator's intent |
| P3 | `gColl` | P2 restricted to leaves that share a name with a leaf of the intent: a name collision never widens reach |
| P4 | `gTrav` | every relay a request crosses is on the path of some leaf of the intent |
| P5 | `gDup` | a leaf of the intent is reached only by its written chain (no scope check is skipped by naming it another way; the ring makes this reachable) |
| P6 | `gExact` | the decision on the LLM route is exactly "the chain is in the written set", for both spellings: nothing else is admitted and nothing written is refused |
| P7 | `gSpell` | the decision of a scoped request does not depend on the spelling |
| P8 | `gFull` | the full-access class is judged by the legacy rule and by nothing else |
| L1 | `cov` | `AG EF cov`: from every state every leaf of the intent (that exists) can still be reached with an LLM route |
| sanity | `sLLM sDeep sDyn sFull gDead` | reachability: an LLM route is admitted; a leaf two or more hops deep; a dynamically registered leaf; a full-access request; a chain to nothing |

Safety is checked as a state assertion, one property per run: `-D ASSERT_PROP=<latch>` adds `assert(<latch> == 0)` after every request
(the first violating state is a counterexample, a clean run is exhaustive). L1 is CTL `AG EF cov` over the complete graph. The first
attempt, all properties as LTL in one run per configuration, was inconclusive (the time budget ran out at 50 minutes) and panicked
in the engine's lasso search on violated safety (`cycle.go:998`, the known defect of `m4-auth-relay.md` E1); it is not used.

## 3. Results

`ok` = `verified`, exhaustive; `VIOL` = `violated` with a counterexample. Intents in the order `1 4 5 20 34 40`. 367 runs, 288 `ok`,
79 `VIOL`, none inconclusive. `gExact` and `gSpell` were run again after the three corrections of section 9 (every other property
was not touched by them).

**3.1 The four options.**

| property | (a) opt1 | (b) opt2 | (c) opt3 | (d) opt4 |
|---|---|---|---|---|
| `gRoute` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` |
| `gRouteGate` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` |
| `gOut` | `  ok VIOL VIOL VIOL VIOL VIOL` | `  ok   ok   ok   ok   ok   ok` | `  ok VIOL   ok VIOL VIOL VIOL` | `  ok   ok   ok   ok   ok   ok` |
| `gColl` | `  ok   ok   ok   ok VIOL VIOL` | `  ok   ok   ok   ok   ok   ok` | `  ok VIOL   ok VIOL VIOL VIOL` | `  ok   ok   ok   ok   ok   ok` |
| `gTrav` | `  ok VIOL VIOL VIOL VIOL VIOL` | `  ok   ok   ok   ok   ok   ok` | `  ok VIOL VIOL VIOL VIOL VIOL` | `  ok   ok   ok   ok   ok   ok` |
| `gDup` | `  ok VIOL VIOL VIOL   ok VIOL` | `  ok   ok   ok   ok   ok   ok` | `  ok VIOL VIOL VIOL   ok VIOL` | `  ok   ok   ok   ok   ok   ok` |
| `gExact` | `VIOL VIOL VIOL VIOL VIOL VIOL` | `  ok   ok   ok   ok   ok   ok` | `VIOL VIOL VIOL VIOL VIOL VIOL` | `  ok   ok   ok   ok   ok   ok` |
| `gSpell` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` |
| `gFull` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` |
| `cov` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` | `  ok   ok   ok   ok   ok   ok` | `  ok VIOL VIOL VIOL VIOL VIOL` |

**3.2 Mutants of option (b)** (intents `5` and `20`).

| mutant | what it changes | property that catches it | intents |
|---|---|---|---|
| 1 | a chain of 2+ hops is judged by its first hop only and the route table is not applied (what a nested mount prefix gives today) | `gRoute`, `gRouteGate`, `gOut`, `gTrav`, `gDup`, `gExact` | `gRoute`: 5+20; `gRouteGate`: 5+20; `gOut`: 5+20; `gTrav`: 5+20; `gDup`: 5+20; `gExact`: 5+20 |
| 2 | an entry is a path prefix: a longer chain that starts with an entry passes | `gExact` | `gExact`: 5+20 |
| 3 | the last segment is compared as a string prefix (`n1` admits `n1x`) | `gOut`, `gExact` | `gOut`: 5; `gExact`: 5 |
| 4 | the allowlist is compared on the raw spelling (an encoded request fails) | `gExact`, `gSpell` | `gExact`: 5+20; `gSpell`: 5+20 |
| 5 | the allowlist is also applied to the full-access token | `gFull` | `gFull`: 5+20 |

Every mutant is caught by the property it was written for, and no mutant is caught by nothing. Mutant 2 is caught by `gExact` only:
in this topology a leaf has no children, so a longer chain that starts with a written entry resolves to nothing (`gDead`, below) and
reaches no unwritten leaf; it is an over-admission that does no harm here and is exactly what `gExact` is for.

**3.3 Other configurations.** Both principals at once (`PR=2`, intents `1` and `5`): every property `ok`. The star entry (`*`, the
allowlist is any single hop below R1): `gRoute`:ok `gRouteGate`:ok `gOut`:ok `gColl`:ok `gTrav`:VIOL `gDup`:ok `gExact`:ok `gSpell`:ok `gFull`:ok.

**3.4 Sanity (non-vacuity).** With option (b), intents `5` and `34`: `sLLM`, `sDeep` and `sDyn` are `VIOL` (reachable), as they
must be. `sFull` is `ok` there only because the principal is scoped; with `PR=1` it is `VIOL` (reachable). `gDead` is `ok` for (b)
(an exact path never walks to nothing) and `VIOL` with option (a) and with mutant 2, as the explanation in 3.2 says.

## 4. Counterexamples, classified

The engine's counterexamples are about 1400 low-level steps each; they were not decoded line by line. The classification below is
derived from the topology of the model (header, `setupTopology`) and confirmed by the verdict of the property on that intent.

- **(a), P2 and P4 and P6, intent `20`** (leaves `R2.n1` and `R2.n1x`, written as `child/n1` and `child/n1x`). Option (a) writes the first
  names, `{child}`; the allowlist then admits every chain that starts with `child`, `child/m2` included, which reaches `R2.m2`, a node the
  operator never meant. This is the case the plan names: a client allowlisted for a child relay reaches every node behind it.
- **(a) and (c), P3, intents `34` and `40`.** The dynamic registrar adds `R2.m1`; the allowlist `{m1, child}` admits `child/m1`, a leaf that
  shares a name with `R1.m1` and `R3.m1` of the intent and is not in it. A node name is not an identity.
- **(c), P2, intent `4`.** The flat set of names `{child, n1}` admits the single hop `n1`, which is `R1.n1`, not the intended `R2.n1`.
- **(d), L1.** A scoped client that may not cross a relay cannot reach any leaf behind one; every intent with such a leaf violates
  `AG EF cov`. Only intent `1` (a leaf directly below R1) holds. Option (d) is safe and says "no".
- **`*`, P4.** `*` admits the one-hop chain `[child]` with an LLM route, which ends at relay R2 itself. The latch is set because that
  relay is not on the path of a leaf of the intent. A relay serves no shared-model route (it answers 404), so nothing is reached: an
  artefact of the abstraction, recorded as the accepted residual of `*` ("any node directly below this relay", the relay nodes included).

## 5. Commands (reproduce)

`M=docs/plans/remote-model-provider-models/p3-d1-allowlist-chain.pml`. One safety property of one configuration:

```
mcd check --promela $M -D OPT=2 -D INTENT=5 -D ASSERT_PROP=gOut --no-timing --budget-ms 3600000 --budget-states 50000000
mcd check --promela $M -D OPT=1 -D INTENT=20 -D ASSERT_PROP=gOut --no-timing     # violated: option (a) admits child/m2
mcd check --promela $M -D OPT=2 -D MUT=4 -D INTENT=1 -D ASSERT_PROP=gExact --no-timing   # violated: the encoded spelling is refused
mcd check --promela $M -D OPT=4 -D INTENT=20 --ctl 'AG EF cov' --no-timing --budget-ms 3600000   # violated: option (d)
mcd check --promela $M -D OPT=2 -D STAR=1 -D INTENT=0 -D ASSERT_PROP=gTrav --no-timing  # violated: the artefact of 4
```

Switches: `OPT` 1..4 (the option), `MUT` 1..5 (mutants of (b)), `INTENT` 0..63 (or 64 for every subset, too costly), `STAR=1`,
`PR` 0/1/2, `DYN` 0/1 (the dynamic registrar, default 1). Every row of 3.1 and 3.2 is one such line; a run of the whole matrix is about an hour
on 10 parallel workers.

## 6. Statements

- CONFIRMED: option (b), exact hop paths, holds P1 to P8 and L1 on every intent asked, with and without the dynamic registrar's node.
- CONFIRMED: option (a), the first hop only, violates P2 (a client allowlisted for a child reaches the nodes the operator never meant).
- CONFIRMED: options (a) and (c) violate P3 under a name collision; a name is not an identity.
- CONFIRMED: option (c) violates P2 and P3 as well, and P3 on more intents than (a) (four of six against two): a flat set loses the place of a name, so a name that is written elsewhere is admitted here.
- CONFIRMED: option (d) is safe and cannot express a node behind a relay.
- CONFIRMED: every mutant of (b) is detected by one of the properties.
- NOT DECIDABLE here: intents of three or more leaves, intents of 64 subsets, a fourth relay, names of other lengths. The argument that the
  rule compares whole hop paths and never counts leaves is not proved.

## 7. Decision

**Option (b): the allowlist is a set of exact hop paths, judged by the first relay on the whole path.** A bare `a` admits only the
single-hop path `[a]`; `a/b` admits only that two-hop path through a chained relay; `*` admits any single-hop path below this relay. The
first relay judges every hop (the walk of `isSharedCompletions`), because a child forwards with its own full token and nothing past the first
relay sees the borrower's class. Option (d) is the same rule restricted to one hop and is not chosen: it cannot name a node behind a relay.
Options (a) and (c) are refused: they widen reach exactly where a name is reused.

## 8. Recommended spec changes (`docs/plans/remote-model-provider-phase3.md`)

- Section 3.2, the `nodes` key: state that an entry is an **exact hop path**; add "an entry of `n` segments admits the chain of those `n`
  names and nothing else; `*` admits any single-hop path; a name that appears at another place in the topology is not admitted by being
  an entry elsewhere".
- Section 3.4: add the edge case "`*` admits a child relay node itself; a relay serves no shared-model route, so the request ends in a 404 and
  reaches nothing (model `p3-d1-allowlist-chain`, 3.3)".
- Section 10, D1: mark closed, with this report as the verdict; the expectation of the plan ("(b) holds") stands.
- Section 11, H2a: no change of owner or order; `entry.admits(hops)` is the exact-path comparison above.

## 9. Not done, and what the review of the model changed

- **Cross-review of the model.** The `.pml` was read by three reviewers in three parts (`gpt-6.1-sol` all three parts, `qwen3.8-27b` two,
  `gemma-4-31b` none: no output within 90 s twice) and every finding was checked by a run. Three low-severity findings were corrected and the
  affected properties run again: `gSpell` was computed for full-access requests (guarded now); `gExact` caught over-admission only (it is
  two-sided now); the `#if OPT == 4` branches of `scopedAdmit` were identical (collapsed). The written set under option (d) now holds the one-hop
  entries only. Part 3 had one answering reviewer, so its review is advisory.
- **Not asked.** Intents of three or more leaves; a larger name space; `DYN=0` rows; timing (the model has none by design); the second
  dynamic registration. L1 for the mutants (they are about safety).
- **The decision is about the model.** The tests of H2a are the conformance check: a table over method, route and spelling against the
  exhaustive expected set, and a chain whose child presents its full token upstream that a scoped client cannot drive outside the three routes
  (`phase3.md` 3.5).
