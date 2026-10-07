# m6-capabilities: how the capabilities of a `coddy` row arrive

Model `m6-capabilities.pml` (same directory), checked with `mcd 0.2.0` (built-in engine). Spec under
test: `docs/plans/remote-model-provider.md` 4.2, 4.3 (the **[decide at modeling]** bullet), 6 (signatures
and alias reassignment), 6a, and the scenarios of `features/remote_model_provider.feature` about the
context window, the reasoning levels, multimodal and "the requested reasoning level reaches the provider".

## 1. Intake card

| Field | Value |
|---|---|
| Boundary | one remote alias row (listing + validation + provider) and one local view of it (override keys, listing cache, user, request). Excluded: busy/slots (m2), auth, relay, the wire frames, prompt cache |
| State | remote: five capability bits (`lvA`, `lvB`, `off` levels; `win` window class; `mm` multimodal), row revision `rrev`, model epoch `rmodel`; local: override `ovs/ovv`, `cache`, `vrev`, `age`, fetch state, history signatures `hsig`; counters `consec`, `ssv`, `since` |
| Atomic step | a `d_step` without nondeterminism: one operator change, one fetch completion, one tick, one send built and one request delivered and validated. A choice (which change, which selection) is the choice of the transition |
| Assumptions | operator changes at any time, at most `NOP` times, flip one bit or reassign the alias (new bits, new model epoch, signatures of the old model stale); a user acts once per tick (send, peek, or idle when `ACTIVE=0`); a send filters its selection through the CURRENT view; the remote validates against its CURRENT row; a fetch samples the remote when it completes; the local override wins key by key |
| Fairness | none needed: the discrete clock is urgent (a tick waits for the user, a request in flight and an overdue fetch), so every infinite run has ticks. Liveness reported with `none` and `weak`, equal |
| Budget | at most 12M states per run; every reported verdict below is `complete` unless it says otherwise |

## 2. Model class

Finite, **untimed with a discrete-clock abstraction** (declared in the model header, not a timed
automaton): `TTL=3` ticks, fetch latency `FL<=1` tick, one tick = one user action (one request interval).
Counters saturate (`CAP`, `CAPC`) above every bound asked. `ND` keeps a prefix of the five bits; ND=2 is
`{lvA, win}`, ND=3 adds `mm`, ND=5 is everything. A fetch copies all bits in one snapshot, so timing results
do not depend on `ND`; refusal kinds do (LEVEL: levels and `off`; OVERFLOW: window class; IMAGE: `mm`).
Refusals: STALE (1, `expected_revision`, before generation), LEVEL (2) and IMAGE (3) (before generation),
OVERFLOW (4) and SIG (5) (the provider refuses: a **failed generation**).

Variants as `-D` switches (one file):

| Id | Mechanism | Flags |
|---|---|---|
| A | picker copies the values once into the row as ordinary keys (= overrides); nothing refreshes | `PICKCOPY=1` |
| B | `ModelEntry` and the manager's cache carry them, refreshed by TTL on the next reader, stale values serve meanwhile | `TTLREF=1` |
| C | copy once (cache layer), forced refresh after a capability refusal; the reader awaits it (`AWAIT=1`); C0 = no await | `ONREFUSE=1` |
| BC | B + C | `TTLREF=1 ONREFUSE=1` |
| D | BC + listing `revision`, request `expected_revision`, the remote refuses a mismatch before generation; D0 = no await; Dp = D without TTL | `XREV=1` |
| E | no gate: every response carries the revision and the client refreshes on a difference | `STAMP=1` |
| `.sN` | signatures in the history (`SIGS=1`): s0 nothing; s1 the client sends only signatures tagged with the revision of its view; s2 the remote wraps its signature with the model epoch and drops a foreign one | `SIGFIX=N` |

## 3. Properties

| Id | Text | Class / logic | How |
|---|---|---|---|
| P1 | a request built from a view equal to the published listing, with no operator change since, is never refused for LEVEL, IMAGE or OVERFLOW | safety, `assert` | model `assert`, every run |
| CK | refusals the user sees in a row after one operator change are `<= K` (`consec` resets on a change and on a success) | safety, `assert` | `-D CK=K`, K searched until the verdict flips |
| SK | sends built from a stale view after one operator change are `<= K` (detects a stale view that is never refused, e.g. widening) | safety, `assert` | `-D SK=K` |
| SB | staleness (`!EQ`) lasts at most SB ticks after the last change, user active | safety, `assert` | `-D SB=n` |
| L1 | `([]<>send) -> <>[] eqb`: if the user keeps sending, the view eventually equals the listing for good | liveness, LTL | `--ltl`, fairness none and weak |
| L2 | `([]<>send) -> [](refused -> <>ok)`: a refusal is eventually followed by a success | liveness, LTL | same |
| P2 | the local override is never overwritten by a refresh | safety, `assert` | `-D OVK=1 -D OVR=1` |
| H | alias reassignment hazard: a request with a signature of an older model is refused (`sigref`); a failed generation with a stale view (`gfs`) | reachability | LTL `[] !(atom)`, `violated` = reachable |
| deadlock | no invalid end state | safety | model's own, every run |

The CLI has no `reach`, `simulate` or lint: a reach is the LTL `[] !(p)` (status `violated` with a run =
reachable) and the classification of each property is by hand (`properties-ltl-ctl.md`). Every sanity reach of
section 6 is in the same style.

## 4. Commands (reproduce)

`M=docs/plans/remote-model-provider-models/m6-capabilities.pml`, `mcd check --promela $M FLAGS --no-timing`.
FLAGS per row of the tables; bases: bounds `-D ND=2 -D LIVE=0 -D GHOST=0 -D CAPC=8` (+ `-D CAP=9 -D CAPC=1` for SB),
liveness `-D ND=2 -D GHOST=0 -D CAP=1 -D CAPC=0 --ltl '<L1>' --ltl '<L2>' [--fairness weak]`, signatures `-D ND=1 -D SIGS=1`,
scale `-D ND=3 -D NOP=2` and `-D ND=5 -D NOP=2`. Example (D, the tight bound): `mcd check --promela $M -D ND=2
-D LIVE=0 -D GHOST=0 -D CAPC=8 -D TTLREF=1 -D ONREFUSE=1 -D XREV=1 -D CK=1 --no-timing` -> `assert` `verified`
`exhaustive`, 337181 states; with `-D CK=0` -> `violated`.

## 5. Results

Defaults: TTL 3, FL 1, NOP 3, ND 2 (CK repeated at ND 3 NOP 2: the same bounds for B, C, BC, D, D0, Dp, E), user acts every tick. A bound K is
reported only where **K verified and K-1 violated** (tight), both `exhaustive`, `complete`. "unb" = violated at
every K up to the saturation horizon 7 (CAPC 8): unbounded within the model. P1 and deadlock: `verified`
`exhaustive` in all nine variants (ND=3 NOP=2: 160k-540k states), and P1 also at ND=5 NOP=2 for B and D
(4.1M and 3.9M states).

| Variant | CK refusals in a row | SK sends from a stale view | SB staleness (ticks) | L1 view converges | L2 refusal then success |
|---|---|---|---|---|---|
| A | unb | unb | unb | `violated` | `violated` |
| B | **5** (= TTL+FL+1) | 5 | 4 (= TTL+FL) | `verified` | `verified` |
| C (await) | **1** | unb | unb | `violated` | `verified` |
| C0 (no await) | 2 | unb | unb | not run | not run |
| BC | 1 | 5 | 4 | `verified` | `verified` |
| **D** | **1** | **1** | 4 | `verified` | `verified` |
| D0 (no await) | 2 | 2 | 4 | not run | not run |
| Dp (no TTL) | 1 | 1 | unb (user only peeks) | `verified` | `verified` |
| E (stamp) | 1 | 1 | 4 | `verified` | `verified` |

Liveness gives the same verdicts under `--fairness none` and `weak`: the urgent clock, not fairness, forces
progress. `B`'s bound scales as `TTL+FL+1` (TTL 2/4, FL 2: 4, 6, 6, checked). With an idle user (`ACTIVE=0`)
B's staleness is unbounded (`SB=4` violated: the TTL refresh is lazy, a reader must ask), D's `SK<=1` still
`verified` (the first send detects it).

Failed generations and wasted refusals (reachability, ND=3 NOP=2, no signatures): in **A, B, C, E** a LEVEL,
an IMAGE and an OVERFLOW refusal and a failed generation with a stale view (`gfs`) and with an equal view
(`gff`, an operator change in flight) are all reachable. In **D** none of them is reachable (`k2`, `k3`, `k4`,
`gfs`, `gff` all `verified` = unreachable, exhaustive): the revision gate answers first. The price: `fval`
(a request that would have succeeded is refused by the gate) is reachable in D, and not in B, C, E.

Signatures and alias reassignment (ND=1, `SIGS=1`), CK:

| | s0 (nothing) | s1 (client tag) | s2 (remote envelope) |
|---|---|---|---|
| A | unb | n/a (no revision) | unb (capabilities) |
| B | unb | n/a | 5 |
| C | unb | n/a | 1 |
| D / Dp | **unb** | 1 | 1 |
| E | unb | 1 | 1 |

H (hazard reachable): `sigref` is **reachable** in B.s0, D.s0, E.s0, E.s1 (an in-flight reassignment); never in
D.s1, D.s2, E.s2. In D.s0 `gfs` is **unreachable** (every stale-view case is caught before generation, as the
spec wants), yet `sigref` stays reachable with a FRESH view and is persistent: the gate is necessary, not
sufficient. `fdrop` (s1 drops a still valid signature after a capability-only change) is reachable in D.s1.

Override (ND=2, NOP=2, `OVR=1`): P2 `verified` `exhaustive` for B (2.86M states) and D (2.09M). With an override
that disagrees with the remote the user is refused without bound whatever the mechanism (`CK=5` violated for B
and `CK=1` for D): no refresh can fix an operator's own key.

Anti-vacuity (mutations, ND=2/3, NOP=2): `BUGLIST` (the listing publishes `lvA` inverted) makes P1 `violated` in
B (437k states) and D (270k); unmutated B `verified` (333k states; D's P1 is `verified` at ND=3 NOP=2). `BUGOVR` (a refresh writes through the override keys)
makes P2 `violated` in B+OVR (3.78M) and BC+OVR (2.43M). The first CK bounds are tight (K-1 violated).

## 6. Decoded counterexamples

* **A, L2 (`ltl1` of the single-formula run), loop 20 steps**: the operator flips `lvA` twice and `win` once and
  stops; the loop is the user acting once per tick on a view nothing ever refreshes. (The engine's trace shows only
  the first statement of each `d_step`, so the refusal itself is not visible in it; it is established by CK, which
  is `violated` at every K up to the horizon.) Cause: **system defect of mechanism A** (values copied once are keys,
  a key is never refreshed).
* **C, L1, loop 40 steps (`ltl1`, fairness none and weak)**: after three flips of `lvA` the user keeps sending and
  the view differs from the listing forever. The loop contains no refusal (inferred: a refusal would force a refresh
  and equality), so the sends do not use the stale capability and nothing tells the client. Cause: **system defect of mechanism C** (refresh on refusal sees
  only narrowing that the user exercises; widening and unexercised narrowing are invisible). Not a fairness or
  model artefact: the premise `[]<> send` holds on the loop.
* **D.s0, CK**: after a reassignment the history holds a signature of the old model; the view is refreshed
  (revision equal) and every following send is refused by the provider (`consec` saturates). Cause: **spec
  defect** of 6 ("pointing an alias at another model breaks the signatures in histories already started" has no
  mitigation, and a `revision` does not give one).
* **B, CK=4**: the remote narrows a level; the user keeps selecting it from the stale view for TTL+FL+1 ticks.
  Cause: system behaviour of a TTL-only cache; with the real TTL (`contextWindowTTL = 1 h`) the bound is the
  number of requests sent in that hour.
* **engine defect (not a model defect)**: `mcd 0.2.0` panics in `explore/cycle.go` `stateOf` ("index out of
  range") when it renders a trace through a multi-statement `atomic` sequence, also for a violated safety
  formula passed with `--ltl`. The first version of the model used `atomic` (kept as a scratch copy, not a
  deliverable). Worked around by `d_step` without nondeterminism; the construct inside `atomic` that triggers it
  was not isolated.

## 7. Spec statements

| Statement | Verdict |
|---|---|
| 4.3 "the capabilities of a row must come from the remote listing ... a key set in the local YAML still wins" | CONFIRMED as a requirement on the mechanism: P2 holds for B and D (exhaustive); the write-through mutation violates it. A refresh may never write the key (a copy made by the picker is indistinguishable from an operator's key, so A with a refresh is the mutation) |
| 4.3 option (A), picker copies once | CONTRADICTED for convergence (L1, L2 violated, CK/SK/SB unbounded). Not a defect for a row the operator pins on purpose |
| 4.3 option (B), cache carries them | CONFIRMED to converge (L1, L2) and bounded; refusals up to TTL+FL+1 requests; CONTRADICTED as a sufficient design for the first request after a change |
| 4.2 `reasoning_effort` omitted means the row's default; `off` only with `allow_reasoning_off` | CONFIRMED on the model for the explicit level (an offered-by-the-view level the row does not list is refused, LEVEL, before generation; no selection is never refused). Code fact, not modeled: the session falls back to `DefaultReasoningLevelFor`, so the default travels as an explicit level and a withdrawn default is a stale-view refusal like any other |
| 4.2 listing without a `revision` | CONTRADICTED as sufficient: without it C never sees widening or unexercised narrowing (SK unbounded) and OVERFLOW is a failed generation (reachable in A, B, C, E) |
| 6 signatures and alias reassignment | CONTRADICTED as written (no mitigation): unbounded refusals with a fresh view (D.s0) |
| Request is atomic at the remote (validate, then generate) | NOT DECIDABLE here (assumed: one atomic step) |
| User retry after refresh rebuilds the request from the new view (proposed in 8) | NOT DECIDABLE here: not modeled. The bounds above are refusals on the wire without it |
| `revision` does not leak the upstream model id | NOT DECIDABLE here (needs a keyed derivation; a security argument, not a protocol property) |
| The numbers in ticks translate to the real clock | NOT DECIDABLE here (discrete abstraction; a tick is one request interval, TTL 1 h = the requests of one hour) |

## 8. DECISION (the **[decide at modeling]** of 4.3 and 6a)

**Recommendation: D, with the signature envelope (D.s2), `AWAIT`, and the TTL kept for the picker.** In words:
B's storage (the listing cache carries the whole record, never a copy into the row) plus a stale signal plus a
`revision` in the listing that the client echoes as `expected_revision`, plus the remote-side signature envelope.

Evidence: (1) A is the only variant that never converges and is refused without bound (L1, L2 violated); the
override semantics it relies on is the same as the mutation that violates P2. (2) B converges but the user sees up
to TTL+FL+1 refusals per change and a failed generation is reachable. (3) C bounds refusals to 1 but never learns
widening or unexercised narrowing (L1 violated, SK unbounded), and without `AWAIT` the bound is 2. (4) D is the only
variant with all of: CK 1, SK 1, L1, L2, and **no failed generation caused by a stale view** (`gfs`, `gff`, `k2..k4`
unreachable); E equals D on the bounds but keeps failed generations reachable (a doomed OVERFLOW reaches the
provider). (5) The `revision` field is **required**: it is the only thing that detects a stale view on the first
request (SK<=1) and the only pre-generation check for a window that the remote cannot validate itself. (6) The gate
alone does not close alias reassignment (D.s0 unbounded); the envelope does (D.s2, `sigref` unreachable) under every
mechanism, so it is independent of the choice. The client-tag variant D.s1 also works but drops valid signatures after a
capability-only change (`fdrop` reachable) and needs a revision.

Cost in code surface, from the files read (facts, no estimate of effort):

| | A | B | C | D |
|---|---|---|---|---|
| `llm.ModelEntry` (`ID`, `Name`, `ContextWindow` today) | must carry the 3 values (the picker reads them from the providers-models route) | carries them | carries them | carries them + `revision` |
| Cache `session/context_window.go` (`windows map[string]int`, TTL 1 h, 5 min after a failure, `ForgetContextWindows`, stale values serve, await only for a never-answered listing) | untouched | record instead of `int`; `coddy` added to `providerListsContextWindows` | as B + a stale mark whose fetch is awaited (today a stale entry is never awaited) | as C + `expected_revision` from the entry |
| Reasoning readers: the four `*config.Config` methods (`ReasoningLevelsFor`, `ReasoningChoicesFor`, `ReasoningOffOffered`, `DefaultReasoningLevelFor`), 25 call sites in 14 files | untouched | one resolver on `*Config`, installed by the manager on every `*Config` it serves (a reload builds a new one: `config.LoadWithPaths`), or a signature change at 25 sites | as B | as B |
| Multimodal readers: `entry.Multimodal` at `agent/tool_images.go:28`, `httpserver/server_metadata.go:192` (4 callers), `httpserver/server.go:423` | untouched | 3 reads become one method | as B | as B |
| Keys: `Multimodal bool`, `AllowReasoningOff bool` cannot say "unset" (`MaxContextTokens` 0, `ReasoningLevels *[]string`, `ReasoningDefault ""` can) | none | `*bool` (types, `jsondto`, `ui_schema`, schema, SPA toggles) or "local true OR remote true" (a local `false` cannot narrow) | as B | as B |
| SPA / picker | copies 3 values (today only the window, into `max_context_tokens`: `SettingsSection.tsx:88`) | does not copy for `coddy` | does not copy | does not copy |
| Wire / remote | none | none | refusal `invalid` with a capability code | `revision` field, `options.expected_revision`, gate, `code: stale_revision`, envelope for signatures |

A cheaper-looking A is not cheaper: it needs `ModelEntry` and the picker to change anyway, and it cannot be
refreshed without rewriting the keys.

## 9. Recommended spec changes (exact replacement text)

**4.2, first bullet** (replace):

> - `GET /coddy/llm/models` answers `{protocol, data[]}`, shared rows only. A row carries the alias as `id`,
>   `revision`, `max_context_tokens`, `multimodal`, `reasoning_levels`, `reasoning_default` and
>   `allow_reasoning_off`. `revision` is an opaque string that changes whenever anything a client can observe about
>   the row changes: any of those fields, or the model the alias points at. It is derived with a keyed hash so that
>   the upstream model id cannot be recovered from it, and it is the same before and after a restart of the remote
>   while nothing changed. There is no `stream` field: the wire is always a stream.

**4.2, after the sentence on `reasoning_effort`** (add):

> `options.expected_revision` is the `revision` of the row in the listing the client last read; absent means no
> check. When it differs from the row's current revision the call is answered `400` `kind: invalid`,
> `code: stale_revision`, with the current `revision`, before the provider is built: no upstream request is made.
> The remote's own validation of `reasoning_effort` (`off` without `allow_reasoning_off`, a level the row does not
> list) runs after that check and stays as the second line of defence.

**4.2, DTO paragraph** (add): *Reasoning signatures.* `ReasoningSignature` leaves the remote inside an envelope
that carries a tag derived (like `revision`) only from the model the alias points at, not from its capabilities. On the
way in the remote opens the envelope; a signature whose tag is not the current model's, or that has no envelope, is
dropped from the message before the provider is built (the Anthropic provider already omits a thinking block
without a signature), and it is never an error. The remote model receives a valid signature unchanged. (Scenario "Reasoning and its signature
survive the hop" must say: the local coddy receives a non-empty signature, and the remote model receives
"sig-abc" unchanged when it is sent back; a new scenario: after the alias is reassigned the old signature does not
reach the provider.)

**4.3, the "[decide at modeling]" bullet** (replace the bullet from "Local `models[]` rows" to the end of that bullet):

> - Local `models[]` rows are `<provider>/<alias>`. An alias says nothing about the model, so the capabilities of a
>   row (context window, multimodal, reasoning levels, default level, `off`) come from the remote listing, never from
>   the id heuristics of `config/reasoning.go`. A key set in the local YAML still wins, key by key, and a refresh
>   never writes a key: the listing lives in the manager's cache, not in the row. Decided by model m6:
>   - `llm.ModelEntry` carries `revision`, `multimodal`, `reasoning_levels`, `reasoning_default` and
>     `allow_reasoning_off` next to `ContextWindow`; the cache of `session/context_window.go` keeps the whole record and
>     `coddy` joins `providerListsContextWindows`. The reasoning methods of `*config.Config` consult one resolver, and
>     every read of `models[].multimodal` goes through one method. `multimodal` and `allow_reasoning_off` become
>     tri-state so that an absent key asks the listing and a written `false` still means no. The Settings picker adds a
>     `coddy` row without copying any of the three values (a window copied into `max_context_tokens` would pin the row).
>   - Refresh: a fetch starts when a reader finds the entry older than its TTL (this only keeps the picker fresh),
>     and at once when a request is answered `stale_revision` or another capability `invalid`. Stale values keep
>     serving while it runs, but a reader that follows such an answer waits for it, for at most `ContextWindowWait`.
>   - First request: the client sends `options.expected_revision` from its cache entry. After `stale_revision` it
>     refreshes, rebuilds the request from the new view (a selected level the row no longer lists falls back as
>     `EffectiveReasoning` does, attachments for a row that stopped being multimodal are dropped as for any
>     non-multimodal model) and sends it once more; a second `stale_revision` ends the call. *[not modeled]*
>   - The default level travels as an explicit `reasoning_effort` (the session falls back to it), so a withdrawn
>     default is a stale view like any other and is covered by the same check.
>   - A local key that disagrees with the listing is refused by the remote for as long as it stays: no refresh can
>     fix it. `coddy -t --dry-run` warns for every local key that differs from the listing.

**5, phases** (replace items 1 and 2's list entries): *1. ... the context window from the listing (as for every
provider), `revision`, `expected_revision`, `stale_revision` and the signature envelope on the wire and the client's
refresh on `stale_revision` (the wire shape is strict (`protocol`) and an envelope cannot be introduced under stored
histories later), a `coddy` row declares `multimodal` and `reasoning_levels` in its keys.* *2. Metadata and limits:
reasoning levels, default and `off`, and multimodal from the listing (the resolver, the three multimodal reads, the
tri-state keys, the `--dry-run` disagreement warning), the usage projection...* Multimodal goes to **phase 2**: its
scenario changes from `@phase-tbd` to `@phase2`; the untagged scenario "An image in the history reaches a vision
model as an image" stays in phase 1 with the key set in the row. New scenarios: a stale revision refused before the
provider (phase 1); levels narrowed on the remote are refused once and the next request succeeds (phase 2); an
override key is not rewritten by a refresh (phase 2); a reassigned alias drops the old signature (phase 1).

**6, bullet "Signatures and alias reassignment"** (replace): *A reasoning signature is valid only for the model that
issued it. The signature envelope of 4.2 makes the remote drop signatures of another model; a `revision` alone does
not (model m6: with a fresh view and an old signature every call is refused by the provider).*

**6a** (replace the second bullet): *How the capabilities of a row arrive (4.3): decided, listing cache plus
`revision`/`expected_revision` (model m6, `docs/plans/remote-model-provider-models/m6-capabilities.md`).*

## 10. Limitations

Results are about the model: one alias, one local view, one user acting once per tick, at most 3 operator
changes, five capability bits (ND=2 for timing, ND=3 for the three refusal kinds, ND=5 only for P1), window as a
two-valued class. The bound "5" for B is in requests, not minutes. A cache shared by several sessions of one process
(the manager has one) is one view here; several processes are independent views, each with these bounds. The
retry after refresh, the auto-detection of overflow by token counts, the `busy` slot interplay and the leak
argument for `revision` are outside the model. Transferring any verdict to the code needs the conformance
argument that the implementation matches the transitions above.
