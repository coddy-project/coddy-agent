# Model check: SSE stream state machine and its client (plan 4.2, 4.3)

Files: `m3-stream.pml` (main model, every variant behind `-D`), `m3-stream-timers.pml` (discrete-clock cross-check of the stall guards; the full timing model is `m5-guards`).
Engine: `mcd` CLI (it has no `simulate` and no `lint`: properties were classified by hand with `properties-ltl-ctl.md`).

## 1. Summary

- SPEC client (the semantics of the 4.3 table): all 19 properties `verified`, evidence `exhaustive`, on four complementary slices and three scale checks (section 6). No SPEC run is inconclusive.
- Every mutation is caught by exactly the properties it should break (BUG_A, B, C, D, E), so the properties are not vacuous.
- **Three defects of the plan text**, each with a counterexample on the literal text (8.2): (1) the first alternative of the typed-errors paragraph (`*streamStalledError` / `*streamTruncatedError` / `*streamServerError{code, emitted}` built from `cause` and `emitted`) contradicts the kind table and "nobody repeats the call after output"; (2) "two guards" are three (the agent's first-token timer cuts a live, heartbeating remote; same finding as `m5-guards` P1c); (3) the partial of an `upstream` failure after the first chunk is lost once the recovery budget is spent, which the plan calls a regression avoided.
- The "16 requests" bound is exact for the first alternative (reached by a plain `error{upstream}`); with the correct client a deterministic upstream failure costs 4, and 16 per call (48 per turn at the defaults) stays reachable only by repeated transport failures.
- Smaller ambiguities with replacement text: 200 non-SSE on the completions route, the busy deadline scope, `emitted` / `cause` / `status` semantics, finished tool calls in the partial.

## 2. Scope and assumptions (intake card)

- **Boundary.** Inside: the remote's frame emitter (one handler per connection), the client's SSE reader, the typed-error layer with the real predicates of `resilient.go` and `upstream_error.go`, the outer wrapper (`callWithRetry`), the agent's execute / recover / persist decisions. Environment: the network (may cut anywhere), relay and proxy HTTP answers, the remote's upstream provider (nondeterministic success, transient failure, permanent failure). Excluded: slot limit, authentication, the busy wait's clock (a counter), first-token retry, `wait_for_limit_reset`, tool results.
- **State.** Connection id, `net` buffer (one frame), server counters (chunks sent, tool-call state per id 0..3, remote retries left, heartbeats, junk), client accumulators (text, reasoning, tool states), its own `emitted`, the error descriptor (Go type, code, kind, cause, emitted), retry / busy / recovery counters, per-step ghost counters, the execution counters of the two tool ids.
- **Atomic step.** One statement (interleaving). `atomic` only in the server's `emit`: the guard `nfull(net)` and the send must not be separated (first version deadlocked: a committed send into a full buffer).
- **Semantics.** Interleaving; frames on one FIFO channel of capacity 1; request by rendezvous. A connection the client closed drops later server writes; a frame already buffered keeps the old id and is ignored (ids alternate 1, 2: at most one stale frame can exist).
- **Fairness.** None needed: every run ends (`<> fin` verified without fairness). Timers model: clocks bounded by 14.
- **Bounds** (scaled; product defaults in brackets): R_LOCAL 0..3 [3], R_REMOTE 1..3 [3], REC_MAX 0..2 [2], BUSY_MAX 0..1, HB_MAX 0..1, J_MAX 0..1, CH_MAX 0..3 chunks per connection, tool ids {0,1}, text counted in chunks. No single scale holds every dimension (section 5).
- **Relation to the implementation.** Hand-written model of the spec and of the Go predicates (the `p_*` inlines transliterate `isRetryableLLMError`, `httpStatusFromError`, `IsTransientProviderError`, `IsStreamTruncated`, `IsStreamStalled` for the error types the client can build). Nothing is claimed about a `coddy.go` that does not exist yet; its conformance to the TABLE typing is the missing argument.

## 3. Model class

Finite, untimed, interleaving. The stall-guard question needs time: `m3-stream-timers.pml` is a **discrete-clock abstraction** (a tick counter bounded by 14, one atomic world step per tick, the guard looking first inside a tick so that the order is adversarial). It is not a timed automaton; its constants are ticks (H=2, FT=4, IDLE=6 keep the order 15 s < 90 s < 300 s).

## 4. Properties

| ID | Text | Class / logic | Slices |
|---|---|---|---|
| P1a | a tool-call id runs at most once per step: `[] (exec0<=1 && exec1<=1)` | safety, LTL | all |
| P1b | it runs exactly once iff a `final` carrying it was delivered (`v_exec_iff`), never otherwise (`v_exec_nofin`) | safety, `[]!v` | all |
| P2 | no retry after output (`v_noret`); the output of two attempts is never both visible (`v_dupvis`) | safety | all |
| P3 | `Stream` returns once (`v_once`), always terminates (`<> fin`), no deadlock | safety, liveness | all |
| P4 | exactly one terminal outcome is consumed per attempt (`v_term`) | safety | all |
| P5 | frames after the terminal never change the result or what the user saw (`v_junk`) | safety | all (junk only with J_MAX=1: C) |
| P6 | after a failure with output the partial holds every text and reasoning chunk and exactly the finished tool calls (`v_part`) | safety | all |
| P7 | no retry the table forbids (`v_forb`); what the table retries is retried (`v_compl`) | safety | all |
| P8 | requests per step <= (R_LOCAL+1)(BUSY_MAX+1) [or (R_LOCAL+1)+BUSY_MAX]; upstream per step <= (R_LOCAL+1)(R_REMOTE+1); per turn x (REC_MAX+1) (`v_bound`) | safety | S, S0, scales, totals, WORST |
| P9 | the result is the `final`, not final+chunks (`v_final`); what the user saw is what is returned (`v_saw`) | safety | all |
| P10 | a transient failure is recovered while budget remains; a limit, a refusal, busy never (`v_rec`) | safety | all |
| P11 | a cut, stalled or truncated stream keeps its visible text (`v_cut`) | safety | all |
| P12 | the status-ordered classification as a decision table over 13 statuses x 10 bodies x 2 `emitted` (260 triples) plus 14 named rows (`v_table`) | exhaustive table | init of every run |
| S* | sanity reaches (6.3) | reachability | SANITY build |

Each `v_*` is a flag set by `check(v, cond)` and checked by its own LTL `[] !v_x` (the CLI has no `invariant` / `reach` kind). The engine warns "atom v_x is never true" on every SPEC run: that is the expected shape of a violation flag; the BUG runs show each flag can fire.

## 5. Method, slices, generalisation

DFS, no fairness, unreduced (`--por` refuses `run` and rendezvous). Every run also sets `BUSY_MAX`, `HB_MAX`, `J_MAX` (S, S0, scales, totals, OPT1W, HTTPPASS: BUSY_MAX=1 HB_MAX=0 J_MAX=0; C: 0/1/1; R: 0/1/0; the BUG and OPT1 slices: 0/0/0, BUG_D J_MAX=1); R_REMOTE=1 and BUSY_SCOPE=1 unless listed.

- **S** retry structure: R_LOCAL=2 REC_MAX=1 BUSY_MAX=1 CH_MAX=1 HB_MAX=0 J_MAX=0 BOUNDS. **S0** = S with BUSY_SCOPE=0.
- **C** stream content: R_LOCAL=1 REC_MAX=0 BUSY_MAX=0 CH_MAX=3 HB_MAX=1 J_MAX=1 (all chunk shapes, heartbeats, junk, one retry).
- **R** recovery: R_LOCAL=0 REC_MAX=1 BUSY_MAX=0 CH_MAX=3 HB_MAX=1 J_MAX=0.
- **scale A / B / totals**: CH_MAX=0 with (R_LOCAL, R_REMOTE) = (3,1) and (2,2); REC_MAX=1 with TOTALS.
- **WORST=1 / 2**: the server only fails upstream and cuts (1) or reports `error{upstream}` (2); a few thousand states, so it runs at the **real defaults** R_LOCAL=3, R_REMOTE=3, REC_MAX=0 and 2.

Why the slices generalise: stream-content properties (P1, P5, P6, P9, P11) are functions of one connection's frames; a further attempt repeats them and the retry decision depends on the error descriptor, not on history (each attempt clears its state, so the history is only counters). Retry and bound properties (P2, P7, P8, P10) depend on counters only, covered at three scales and at the defaults by WORST. Residual risk: a long chunk shape combined with two or more retries is argued, not exhausted (slice C has R_LOCAL=1).

Reproduce (the 16 `v_*` flags are `v_table v_once v_dupvis v_bound v_term v_junk v_part v_noret v_forb v_compl v_rec v_cut v_exec_nofin v_exec_iff v_final v_saw`):
```
mcd check --promela m3-stream.pml -D R_LOCAL=2 -D REC_MAX=1 -D BUSY_MAX=1 -D J_MAX=0 -D HB_MAX=0 -D CH_MAX=1 -D BOUNDS --no-timing \
  --ltl '[]!v_table' --ltl '[]!v_once' --ltl '[]!v_dupvis' --ltl '[]!v_bound' --ltl '[]!v_term' --ltl '[]!v_junk' --ltl '[]!v_part' \
  --ltl '[]!v_noret' --ltl '[]!v_forb' --ltl '[]!v_compl' --ltl '[]!v_rec' --ltl '[]!v_cut' --ltl '[]!v_exec_nofin' --ltl '[]!v_exec_iff' \
  --ltl '[]!v_final' --ltl '[]!v_saw' --ltl '<>fin' --ltl 'at-most-once' --budget-states 40000000 --budget-mem-mb 20000
```
Other runs change only the `-D` list: `BUG_A`..`BUG_E`, `OPT1`, `OPT1W`, `HTTPPASS`, `FIX_PERSIST`, `SANITY` (with `--ltl '[]!seen_x'`), `WORST=1|2`, `TOTALS`, `BOUND_UP=n`. Timers: `mcd check --promela m3-stream-timers.pml [-D FIX_A|FIX_AB|IDLE=n]` with the formulas in its header.

## 6. Results

### 6.1 SPEC client (each row: deadlock, the 16 flags, `<> fin`, at-most-once: 19 properties)

| Slice | Constants | States | Complete | Time | Statuses |
|---|---|---|---|---|---|
| S | R_LOCAL=2 REC_MAX=1 BUSY_MAX=1 CH_MAX=1 BOUNDS | 10,182,060 | True | 326.96s | all 19 verified |
| S0 | as S, BUSY_SCOPE=0 | 4,194,361 | True | 127.16s | all 19 verified |
| C | R_LOCAL=1 REC_MAX=0 CH_MAX=3 HB=1 J=1 | 10,837,743 | True | 306.50s | all 19 verified |
| R | R_LOCAL=0 REC_MAX=1 CH_MAX=3 HB=1 | 3,497,035 | True | 96.11s | all 19 verified |
| scale A | R_LOCAL=3 R_REMOTE=1 CH_MAX=0 BOUNDS | 1,173,474 | True | 31.30s | all 19 verified |
| scale B | R_LOCAL=2 R_REMOTE=2 CH_MAX=0 BOUNDS | 955,682 | True | 24.35s | all 19 verified |
| totals | R_LOCAL=2 REC_MAX=1 CH_MAX=0 BOUNDS TOTALS | 7,976,180 | True | 172.73s | all 19 verified |

### 6.2 Variants (anti-vacuity). The other properties of each run stay `verified`.

| Variant | Slice | States | Violated properties |
|---|---|---|---|
| BUG_A | R_LOCAL=0 REC_MAX=1 CH_MAX=3 | 2,367,633 | v_exec_nofin, v_exec_iff, at-most-once |
| BUG_B | R_LOCAL=1 REC_MAX=0 CH_MAX=2 | 1,738,394 | v_dupvis, v_noret, v_forb, v_cut, v_saw |
| BUG_C | R_LOCAL=0 REC_MAX=1 CH_MAX=3 | 1,638,575 | v_part, v_rec, v_cut |
| BUG_D | R_LOCAL=0 REC_MAX=0 J_MAX=1 CH_MAX=2 | 608,428 | v_junk |
| BUG_E | R_LOCAL=0 REC_MAX=0 CH_MAX=3 | 759,443 | v_final, v_saw |
| OPT1 | R_LOCAL=1 REC_MAX=0 CH_MAX=2 | 1,742,730 | v_dupvis, v_noret, v_forb, v_cut, v_saw |
| OPT1W | R_LOCAL=2 REC_MAX=1 BUSY_MAX=1 CH_MAX=0 BOUNDS TOTALS | 15,876,862 | v_forb |
| HTTPPASS | R_LOCAL=2 REC_MAX=1 BUSY_MAX=1 CH_MAX=0 BOUNDS TOTALS | 14,605,427 | v_forb |

**Bounds, SPEC client** (a `hit_*` reach shows the bound is tight): requests per step <= (R_LOCAL+1)(BUSY_MAX+1) = 6 (S) or (R_LOCAL+1)+BUSY_MAX = 4 (S0), both reached; upstream per step <= (R_LOCAL+1)(R_REMOTE+1) = 6, 8, 9 at (2,1), (3,1), (2,2), reached; per turn <= (REC_MAX+1) x that, reached (WORST). **At the defaults (WORST=1, `R_LOCAL=3 R_REMOTE=3`): 16 per call reached and never exceeded; with REC_MAX=2, 48 per turn reached and never exceeded.** WORST=2 with `BOUND_UP=4`: SPEC verified (a deterministic upstream failure costs R_REMOTE+1 = 4 per call). OPT1 under the same WORST=2: `BOUND_UP=15` violated, `16` verified: the plan's 16, exactly.

### 6.3 Sanity reaches (SANITY build; `[] !flag` violated = reachable)

Reachable: final delivered, error after output, EOF before output retried, relay `502` retried, busy waited, recovery re-issue, heartbeat, stale frame dropped, junk sent after the terminal, tool call executed, partial built, unfinished tool call dropped from the partial, `hit_req`, `hit_up`, `hit_tot` (runs hit0, hit1, hitA, hitB, sanR, sanHJ, WORST). A flag is unreachable only in a slice that switches its feature off (checked case by case).
Information flags: `lost_vis_cut` unreachable (P11); `lost_vis_status` **reachable** with SPEC typing and unreachable with `FIX_PERSIST`; `lost_vis_other` reachable with and without it (parity with the local path).

## 7. Decoded counterexamples (trace decoded from the CLI report)

| Run | Violated | Decoded run | Class |
|---|---|---|---|
| BUG_A | `v_exec_nofin`, `v_exec_iff`, at-most-once | chunk `tool_call(0)` arrives; the stream ends with `error{rate, emitted}`; the chunk already ran the tool | mutation (meant to be caught) |
| BUG_B | `v_noret`, `v_forb`, `v_dupvis`, `v_saw`, `v_cut` | 2 text chunks, then `error{upstream, cause: stall, emitted: 1}`; the typed error lost `emitted`; the call is retried; the text shows twice | client defect |
| **OPT1** | the same five | the same run with the literal text: `*streamStalledError` has no `emitted` field and `isRetryableLLMError` returns true for it | **spec defect** |
| OPT1 | `v_forb`, `v_bound` (BOUND_UP=15) | `error{upstream, status}` without output typed `*streamServerError{502}` is retried by status; busy after the budget typed `429` is retried by the outer wrapper (restarts the wait) | **spec defect** |
| OPT1W | `v_forb` only | the stall wrapped in `streamTransportError{emitted}` repairs P2, but a stall / truncation / 5xx without output is still retried although the remote already retried | **spec defect** |
| HTTPPASS | `v_forb` | a plain `500` or `429` (no coddy kind) handed on as a status error is retried; the plan says non-retryable | requirement the plan does not state |
| BUG_C | `v_part`, `v_rec`, `v_cut` | text chunks, then `error{upstream, stall}` typed as an unregistered error: partial dropped, not transient, no recovery | client defect |
| BUG_D | `v_junk` | `error{busy}` terminal, then a junk text chunk: `saw_text` changes after the terminal | client defect |
| BUG_E | `v_final`, `v_saw` | `final{text: 2}` after 2 text chunks: the result has 4 | client defect |

## 8. Per statement of the plan

### 8.1 CONFIRMED (verified, exhaustive, on the slices of section 5)

- 4.2 "Exactly one terminal event; anything after it is discarded; EOF without one is a truncated stream and a transport failure": P4, P5, P2, P7 (EOF before output is retried, after output it is not).
- 4.2 `final` is returned as the result and never appended to what the chunks built; no tool call runs twice; tool calls run **only** from the final: P1, P9.
- 4.2 "nobody repeats the call after output" and 5a "a stream ending without a terminal event is a transport failure retried only while nothing was emitted": P2, P7.
- 4.3 error table row by row, **as semantics** (TABLE typing): `busy` waits within its budget and then fails without outer retry; `rate`, `quota`, `upstream`, `invalid`, `auth` are never retried locally; transport failures are retried only before output.
- 4.3 status-ordered classification (P12, 260 triples and 14 named rows): plain-text `401` -> auth; relay `502` before output -> transient, retried; relay `404` hop error -> invalid; `200` HTML -> clear error; other -> non-retryable; a coddy kind wins over any status; HTML `401` / `503` follow the same rules; **no (status, emitted=true) pair is retried**; the function is total.
- 4.3 "on a failure after a chunk `Stream` returns the partial `*Response` ... drops tool calls that were not finished": P6 (all text and reasoning chunks; exactly the tool calls whose `tool_call` chunk arrived).
- 4.3 "`IsTransientProviderError` starts the recovery that re-issues a step" and "`IsStreamTruncated` / `IsStreamStalled` keep the partial after a cut stream": P10, P11.
- 4.2 the liveness guard: with H < IDLE the client byte guard never cuts a live remote; with IDLE <= H it does (timers model; needs a margin of one tick, see `m5-guards` P1).

### 8.2 CONTRADICTED

1. **4.3 typed-errors paragraph, first alternative** (OPT1, OPT1W, HTTPPASS; section 7). Four independent defects: (a) `*streamStalledError` carries no `emitted`; only `*streamTransportError{cause, emitted: true}` around it stops the retry after output (as `openai_stream.go` builds it); (b) with `emitted: false`, stall, truncation and `*streamServerError{502 / 429}` are retried by the wrapper although the table says `upstream` and `rate` "no, the remote already retried": 16 upstream requests per call; (c) a spent busy budget typed as a `429` status error is retried by the outer wrapper and restarts the wait that 4.1b forbids restarting; (d) a plain `429` / `500` passed through is retried although "everything else is non-retryable". System defect (spec). Only the second alternative, a registered error type, is sound.
2. **4.2 "Stall and liveness are two guards"** (timers model, BASE): the agent's first-token timer (`llm_first_token_timeout_ms` 90 s, armed when the LOCAL row streams, stopped only by text / reasoning / tool chunks) cuts a live remote that only heartbeats (`ft_on_hb` reachable): a long remote backoff, a `stream: false` row, a slow first token. The 5a row "a row with `stream: false` that generates for six minutes is not cut" holds for the plan's two guards and fails for the third. FIX_A alone (a heartbeat re-arms the timer) is wrong: a hung upstream that keeps heartbeating is waited for forever (`clk == 14 && res == R_WAIT` reachable); FIX_AB (FIX_A plus a first-token guard of the remote for streaming rows, a blocking row bounded by its request timeout) verifies. Same defect and a fuller treatment in `m5-guards` (P1c, section 9).
3. **4.3 "a stall or a `5xx` after the first chunk would lose the partial answer ... a regression against a local model"**, for `error{upstream, cause: status}` after output: with the typed `*streamServerError{code, emitted}` the spec lists, and with a registered `coddyAPIError`, the agent keeps the text only for recovery; once `maxProviderRecoveries` is spent `react.go` (line 1243) persists the partial only for `IsStreamTruncated`, `IsStreamStalled` or cancel, so the visible text is lost (`lost_vis_status`). The local `streamServerError` path has the same gap: parity, not a regression, so the sentence overclaims. `FIX_PERSIST` removes it; `rate` / `quota` / `invalid` / `auth` after output stay lost, as locally.

### 8.3 NOT DECIDABLE here

- Whether `coddy.go` implements the TABLE typing (conformance); wall-clock values (15 s, 90 s, 300 s): orderings only.
- Interplay with the first-token retry (`maxFirstTokenRetries`) and `wait_for_limit_reset`: not modelled.
- 16 MiB frames, the 60 s write deadline, `Expect: 100-continue`: outside this model.

### 8.4 Decisions

No `[decide at modeling]` item is assigned to this model. Data for m2 (4.1b): the busy budget scope changes the request bound from (R_LOCAL+1)(BUSY_MAX+1) (per inner attempt, S) to (R_LOCAL+1)+BUSY_MAX (per call, S0); both verified and reached; at the defaults with a 30 s wait that is up to 4 waits against 1 (R6).

## 9. Recommended spec changes (replacement text)

**R1. 4.3, replace the paragraph "The agent relies on the **types** of `llm` errors ... drops tool calls that were not finished" with:**

> The agent relies on the **types** of `llm` errors: `IsStreamTruncated` and `IsStreamStalled` keep the partial answer after a cut stream, `IsTransientProviderError` starts the recovery that re-issues a step, and the resilient wrapper decides retries with `isRetryableLLMError` and `httpStatusFromError`. Those predicates must not be fed `*streamStalledError`, `*streamTruncatedError`, `*streamServerError{code, emitted}` or a status error built from the remote's answer: the wrapper retries a stall or a truncation while `emitted` is false (the remote has already retried them), retries a `429` / `502` by its status, retries a bare `*streamStalledError` even after output (it has no `emitted` field; only `*streamTransportError{emitted: true}` around it refuses) and retries a plain `429` / `500`. The client builds **one** error type, `coddyAPIError{kind, cause, emitted, status}`, registered in all five predicates (`IsStreamTruncated`: cause `truncated`, or kind `upstream` after output; `IsStreamStalled`: cause `stall`; `IsTransientProviderError`: kind `upstream`; `UpstreamStatus`; `UpstreamRetryAfter`) and treated by `isRetryableLLMError` / `httpStatusFromError` as status zero and **never retryable**, for every kind and for every HTTP status except `408`, `502`, `503`, `504` before output. The only failures retried locally are those statuses (a relay answer of that kind, a refused or cut connection) and a stream that ends without a terminal event, both only while no chunk was emitted; they are built as the existing transport errors (`*streamTruncatedError{emitted}`). The client's `emitted` is its own count of chunks passed to `onChunk` (heartbeat comments are not chunks), never the frame's field. On a failure after a chunk `Stream` returns the partial `*Response` next to the error: all text and reasoning, and the tool calls whose `tool_call` chunk arrived (the agent records them as interrupted; a tool call runs only from a `final`); calls that were only named or streamed are dropped.

**R2. 4.3, replace "Without this both ends retry and one turn costs up to 16 requests" with:**

> Without this both ends retry and one `Stream` call costs the remote up to (`llm_retry_max`+1) x (remote retries+1) = 16 upstream requests for a plain `error{upstream}`. With it a deterministic upstream failure costs remote retries+1 = 4 per call; 16 remains the worst case only for repeated transport failures (the connection cut after the remote retried). The agent's provider recovery repeats the call up to twice (3 calls, 48 requests in the worst case, the same multiplier as for a local model).

**R3. 4.2 "Stall and liveness are two guards": adopt the text of `m5-guards` section 9 ("Three timers, three jobs").** Two data points of this model for it: the heartbeat must also stop (re-arm) the first-token timer or the timer must be off for type `coddy`, and then the remote must carry its own first-token guard for a streaming row, otherwise a hung upstream that keeps heartbeating hangs the call (FIX_A against FIX_AB). Add to the 5a Client row: "the agent's first-token timer does not cut a live remote that only heartbeats".

**R4. 4.2, error frame: after the field list add** "`cause` is meaningful for kind `upstream` only (`status` for the other kinds). `status` is informational: the client decides by `kind` and `cause`. `emitted` counts chunk frames; heartbeat comments are not chunks."

**R5. 4.3, classification: after the sentence on the `GET` of the listing add** "The same holds for the completions route: a `200` whose `Content-Type` is not `text/event-stream` is the 'remote does not offer shared models' error and is not retried (the byte guard only wraps event streams). A status is meaningful only before output: after the `200` of a stream an error frame follows its `kind`, never its `status`."

**R6. 4.1b, after "One monotonic deadline covers the requests, the sleeps and the jitter":** "The deadline belongs to one `Stream` call of the outer wrapper and travels in the context (as `RetryAllowance` does), so a transport failure after a successful wait does not start a new wait; otherwise a call can wait (`llm_retry_max`+1) x `busy_wait_ms`."

## 10. Limitations and next actions

- Slices, not one scale; the content x retry combination beyond R_LOCAL=1 is argued. Text in chunks, ids {0,1}, one buffered frame, no size limits, no reasoning signatures.
- The Go predicates are transliterated for the error types the client can build; a new type needs new rows. `ET_QUOTA` is not tied to `limitResetToWaitFor`.
- The timers model is a discrete abstraction: orderings and reachability only.
- Next: implement the registered error type and test it table-driven over (kind, cause, emitted) x the five predicates (the `p_*` inlines list the expected outputs); fold R6 into m2's busy model; keep R3 aligned with `m5-guards`.

## 11. Artifacts

`m3-stream.pml`, `m3-stream-timers.pml`, this report (all under `docs/plans/remote-model-provider-models/`). Run logs and JSON reports are in the session scratchpad.
