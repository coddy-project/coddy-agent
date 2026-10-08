# p3-d3-rate-windows: where the rate limits live, what a refusal answers, when a token is spent (phase 3, dilemma D3)

Spec: `docs/plans/remote-model-provider-phase3.md` sections 5.3, 10 (D3), 11 (H2b, H4, H5). Model: `p3-d3-rate-windows.pml` (this
directory; the header comment lists every switch). Engine: `mcd 0.2.0` CLI (`mcd-report/1`), 2026-10-08. The CLI has no `mc_simulate` and
no `mc_lint_property`: sanity is the reachability latches of section 3.3 and the deadlock check. A result is a statement about the model
and its bounds; the code of the plan was read, not run.

## 1. Intake card

- **Boundary.** One credential at the node, up to two relay clients (one of them with two calls at once), the node's slots and window,
  the relay's slots and window per client, and the calling Coddy's wait loop. Outside: the body read, the stream itself, the probe, the
  upstream provider's own 429, the relay's lease and routing.
- **State.** `now` (a tick), the node's tokens and slots, per client the relay's tokens and slots, per caller `st` (start, asleep, in service,
  done), `wake`, `endAt`, `cstart`, `calls`; counters `admN`, `admC[]`, `netR[]` (the relay's tokens taken, refunds taken off); the ghost
  latches `gA1 gA1p gA7 gA3 gA4 gTerm gTimeout gStuck` and the sanity latches `sAdm sNodeBusy sRelayBusy sWin sRefund`.
- **Atomic step.** One attempt of a caller (relay layer, node layer, outcome); one tick of the clock.
- **Time.** A discrete clock as a variable: a caller acts only when ready (its wake tick has come), and the clock advances only when no
  caller has an enabled action at the current tick, so the interleaving cannot delay a caller past its own timer. Not a timed automaton.
- **Assumptions.** A1 the node sees the relay as one credential (slots and bucket shared by every client of the relay); A2 the
  client waits out kind `busy` only, sleeps max(Retry-After, 1), clamps its last sleep to what the budget has left and tries once more
  (`internal/llm/coddy.go`, `admit`), any other kind ends the call; A3 a window refusal's Retry-After is the ticks to the next token, a slot
  refusal's is 1; A4 the limiter is a token bucket of B tokens gaining one every P ticks (PR at the relay), which a GCRA is equivalent to for the
  properties asked (cumulative counts and placements; the phase of a refill is not modelled); A5 the relay's token is taken after its slot and
  refunded when the node refuses a forwarded attempt (the plan's design, REFUND). **Bounds.** Up to three callers on two clients, two calls each,
  budget W = 8, service D = 2 or 4, B = 1 or 2, P = 3, PR = 3 or 6, NM 1 or 2, RM 1 or 2, horizon T = 32 (1536 configurations, 13 latches each,
  19968 runs, all decided; the largest has about 12000 states).

Mapping (model -> code): `relayLayer` = `handleMount` of a scoped client (a slot in `limits.go`, then `Take`); `nodeLayer` = `llmCompletionsPost`
(`acquire`, then `takeSharedWindow`); `outcome` refusal branch = the deferred release, the relay's `Refund` on a node busy; `sleepOrGiveUp` =
`admit` of `coddy.go` (one budget from the first attempt); KIND 1 = the answer `kind: rate`, which `classifyCoddyAnswer` treats as terminal;
`PLACE` 1, 2, 3 = a limit at the node, at the relay, at both.

## 2. Properties

| id | latch | meaning |
|---|---|---|
| A1 | `gA1` | admitted calls per layer never exceed B + now/P at the node (when it has a layer) and B + now/PR for a client's net tokens at the relay |
| A1p | `gA1p` | provider-wide: the calls the node admits in all never exceed B + now/P (only the node protects the provider from every client together) |
| A7 | `gA7` | a client is held to its own contract: its admitted calls never exceed B + now/PR (only the relay knows the client) |
| A2 | `gTimeout`, NCALL = 1 | a lone waiting caller is admitted within P + 1 ticks of a token existing: no call ends on a spent budget (W >= P + 2) |
| A3 | `gA3` | a refused request never reduces the tokens of a layer, the relay's refund counted |
| A4 | `gA4` | with both layers and 2 * RM <= NM, a client held to its relay slots never causes a node slot refusal |
| A5 | `gTerm`, `gStuck` | a client is never ended by a terminal error for a window refusal; every caller is done at the horizon |
| A6 | `gTimeout`, NCALL >= 2 | starvation under contention is reachable (documented, not promised away) |

Safety is checked as a state assertion, one property per run (`-D ASSERT_PROP=<latch>`).

## 3. Results

**3.1 Every alternative against the design.** Per design, the number of the 96 configurations (NCALL, NM, RM, B, D, PR) in which the property
is violated; `ok` is "violated in none of the 96".

| placement | spend | refusal | refund | A1 | A1p | A7 | A3 | A4 | A5 `gTerm` | A5 `gStuck` | `gTimeout` (A2 / A6) |
|---|---|---|---|---|---|---|---|---|---|---|---|
| node only | (x) attempt | `busy` + code | yes | ok | ok | **VIOL** 24/96 | **VIOL** 28/96 | ok | ok | ok | reachable in 40/96 |
| node only | (x) attempt | `rate` | yes | ok | ok | **VIOL** 14/96 | **VIOL** 16/96 | ok | **VIOL** 72/96 | ok | never |
| node only | (y) admission | `busy` + code | yes | ok | ok | **VIOL** 28/96 | ok | ok | ok | ok | reachable in 32/96 |
| node only | (y) admission | `rate` | yes | ok | ok | **VIOL** 16/96 | ok | ok | **VIOL** 52/96 | ok | reachable in 8/96 |
| relay only | (x) attempt | `busy` + code | yes | ok | **VIOL** 60/96 | ok | **VIOL** 10/96 | ok | ok | ok | reachable in 18/96 |
| relay only | (x) attempt | `rate` | yes | ok | **VIOL** 62/96 | ok | **VIOL** 8/96 | ok | **VIOL** 56/96 | ok | never |
| relay only | (y) admission | `busy` + code | yes | ok | **VIOL** 64/96 | ok | ok | ok | ok | ok | reachable in 12/96 |
| relay only | (y) admission | `rate` | yes | ok | **VIOL** 64/96 | ok | ok | ok | **VIOL** 48/96 | ok | never |
| both | (x) attempt | `busy` + code | no | ok | ok | ok | **VIOL** 64/96 | ok | ok | ok | reachable in 48/96 |
| both | (x) attempt | `busy` + code | yes | ok | ok | ok | **VIOL** 32/96 | ok | ok | ok | reachable in 40/96 |
| both | (x) attempt | `rate` | no | ok | ok | ok | **VIOL** 64/96 | ok | **VIOL** 76/96 | ok | never |
| both | (x) attempt | `rate` | yes | ok | ok | ok | **VIOL** 20/96 | ok | **VIOL** 76/96 | ok | never |
| both | (y) admission | `busy` + code | no | ok | ok | ok | **VIOL** 64/96 | ok | ok | ok | reachable in 46/96 |
| both (design) | (y) admission | `busy` + code | yes | ok | ok | ok | ok | ok | ok | ok | reachable in 32/96 |
| both | (y) admission | `rate` | no | ok | ok | ok | **VIOL** 64/96 | ok | **VIOL** 76/96 | ok | never |
| both | (y) admission | `rate` | yes | ok | ok | ok | ok | ok | **VIOL** 62/96 | ok | reachable in 6/96 |

Reading it row by row: the design (both layers, a token after the slot, `busy` with a code, the relay's refund) violates no safety property in any
of its 96 configurations; every other row violates at least one, and each alternative the plan names fails the property the plan names:

- **Node only** violates A7: nothing holds a client to its own contract (the node sees one credential).
- **Relay only** violates A1p: nothing bounds what the relay's clients admit together on the provider's side.
- **A token at the attempt (x)** violates A3: a call refused for a slot has already spent its token (and, polling every second, drains the bucket).
- **A relay that does not refund** violates A3: a node busy on a forwarded attempt costs the client a token.
- **A window refusal as `rate`** violates A5: the client ends with a terminal error instead of waiting.

A2: no lone caller timed out in any of the 512 configurations with one caller.

**3.2 Starvation.** `gTimeout` is reachable for the design in 32 of its 64 configurations with two or more callers (A6): a caller can lose
every race inside its budget. This is documented as a limit, not promised away.

**3.3 Sanity.** For the design: `sAdm` reachable in 96 of 96 configurations, `sWin` in 62, `sRefund` in 64, `sNodeBusy` in 38 and `sRelayBusy` in
16. `sNodeBusy` needs NM = 1 (with both layers and 2 * RM <= NM the relay's slots cap what is forwarded): reachable in 32 of the 32 configurations
with two or more callers and NM = 1. A4 is not vacuous: in the 8 configurations of the design where it applies (three callers, 2 * RM <= NM),
`sRelayBusy` is reachable (a call is refused at the relay's own slot) and `gA4` never fires.

**3.4 Option (iii), `quota`.** It was not run on its own: for the client `quota` and `rate` are the same (neither is kind `busy`, so the client
ends the call), so (iii) fails A5 exactly as (ii) does.

## 4. Counterexamples, classified

The engine's counterexamples are long low-level traces; the classification derives from the design of the model and the verdict of the latch.

- **(x), A3.** The attempt takes a token at a layer, then the slot is found busy, the call is refused and the token is not given back: after the refusal
  the layer holds fewer tokens than before.
- **No refund, A3.** The relay takes its token after its slot and forwards; the node answers busy (a full slot or a spent window); the relay's slot is
  released and its token is not: the client's bucket drains while it waits out the node's busy.
- **Relay only, A1p.** Each client is within its own bucket and the node has none, so the clients together admit more than B + now/P calls.
- **Node only, A7.** One client of the relay takes the node's tokens at the node's rate, above a contract the relay was meant to enforce for it.
- **`rate`, A5.** The first window refusal ends the call with a terminal error (`gTerm`).

## 5. Commands (reproduce)

`M=docs/plans/remote-model-provider-models/p3-d3-rate-windows.pml`. One safety property of one configuration:

```
mcd check --promela $M -D PLACE=3 -D SPEND=1 -D KIND=0 -D REFUND=1 -D NCALL=3 -D NM=2 -D RM=1 -D ASSERT_PROP=gA3 --no-timing   # verified
mcd check --promela $M -D PLACE=3 -D SPEND=0 -D KIND=0 -D REFUND=1 -D NCALL=3 -D NM=1 -D B=2 -D D=4 -D ASSERT_PROP=gA3 --no-timing   # violated: (x)
mcd check --promela $M -D PLACE=3 -D SPEND=1 -D KIND=0 -D REFUND=0 -D NCALL=3 -D NM=1 -D ASSERT_PROP=gA3 --no-timing   # violated: no refund
mcd check --promela $M -D PLACE=2 -D NCALL=3 -D NM=2 -D ASSERT_PROP=gA1p --no-timing   # violated: relay only
mcd check --promela $M -D PLACE=1 -D NCALL=3 -D PR=6 -D ASSERT_PROP=gA7 --no-timing   # violated: node only
mcd check --promela $M -D KIND=1 -D NCALL=2 -D ASSERT_PROP=gTerm --no-timing   # violated: rate
```

Switches: `PLACE` 1..3, `SPEND` 0/1, `KIND` 0/1, `REFUND` 0/1, `NCALL` 1..3, `NM`, `RM`, `B`, `D`, `P`, `PR`, `W`, `NC`, `T`. The whole matrix
(1536 configurations x 13 latches) runs in about a minute and a half on 12 parallel workers.

## 6. Statements

- CONFIRMED: both layers, a token after the slot, `busy` with a code, and the relay's refund hold A1, A1p, A3, A4, A5 and A7 in every configuration asked.
- CONFIRMED: a lone caller is always admitted within the budget; starvation under contention is reachable and stays documented.
- CONFIRMED: each of node only, relay only, a token at the attempt, a relay without a refund and a window answered as `rate` fails the property the plan names.
- CONFIRMED: the relay's refund is what makes A3 hold with both layers: without it the design fails A3 in 64 of 96 configurations.
- NOT DECIDABLE here: the exact phase of a GCRA (the model's bucket refills on the clock's boundaries), more than three callers or two clients, a node
  window refused on a call whose body is read by the proxy (the model has no body), a relay that dies between the take and the refund.

## 7. Decision

**Both layers, a token taken after the slot, a window refusal answered as `busy` with a code, and a relay that refunds its token when the node refuses a
forwarded attempt.** This is the plan's design (5.3) as written. Placement (c) is needed for two different reasons the model separates: only the node
bounds what the relay's clients admit on the provider (A1p), and only the relay knows a client and holds it to its contract (A7). The refund is part of the
decision, not a refinement: without it the design fails A3.

## 8. Recommended spec changes (`docs/plans/remote-model-provider-phase3.md`)

- Section 10, D3: mark closed, with the verdict and this report. The expectations of the plan stand.
- Section 5.3: no change of text; the relay's refund paragraph is the verdict's REFUND condition. Add to the tests of 5.6 a table over (placement, spend, refund)
  as in 3.1 of the report, to hold the implementation to the same matrix.
- Section 11: H2b and H4 no longer wait for D3.

## 9. Not done, and what the review of the model changed

- **Cross-review.** The model was read by three reviewers in three parts (all three answered parts 1 and 2; `gpt-6.1-sol` and `gemma-4-31b` part 3; `qwen3.8-27b`
  was cut off at its output limit on part 3) and every finding was checked by a run. Two corrections: the last sleep of a call is now cut to the budget's remainder
  and followed by one more attempt, as the real client does (it was a give-up, slightly pessimistic; no verdict changed in 1536 x 13 runs); the header now says what
  the sanity latch `sNodeBusy` needs (NM = 1) and that the bucket is a token bucket, not a GCRA. The findings rejected after a check: the initialisations of `ntok`
  and `rtok` (they are in the parts the reviewer did not see), and the three variants of "A1 is two invariants" (they are the header's A1, A1p and A7).
- **Not asked.** More callers; per-call body size; the relay's lease; the difference between a window and a slot as the first thing a call meets in a retry
  storm beyond one budget.
