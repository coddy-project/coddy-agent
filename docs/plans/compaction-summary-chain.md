# Plan: a chain of summaries instead of a summary of summaries (#361)

Status: implemented on `feat/compaction-block-chain`. Issue #361 reported that
every compaction retold the previous summary, so the details of the earliest
turns wore away and the prompt cache was lost from the first message on at
every fold. The current reference is `docs/features/compaction.md`
(*Summaries form a chain*); this file keeps the design as it was decided and
the numbers measured against `main`.

## 1. Decisions

- **Storage stays flat and insert-only.** A compaction inserts its row at its
  boundary, as before, and the row covers the messages between the previous
  summary row and itself. Nothing is ever removed or rewritten, which
  `subagent.runMessages` relies on, the UI and `rewind` keep working on the
  same transcript, and no index has to be stored (an insert shifts every index
  after it, so a stored range would go stale).
- **One flag, no counter.** `llm.Message.CompactionBlock` marks a row that
  continues the chain. A row without it starts one: every row written before
  this change, and the row a merge writes. A merge row is inserted right after
  the last row it retells, so `session.LLMWindow` (the last row without the
  flag, every block after it, then the tail) drops the retold rows by itself
  and the merge needs no count of what it replaced. An older binary reading a
  new session takes the last row as its window and loses the earlier blocks
  from the prompt, not the session.
- **The summarizer reads the chain but folds only the tail.** The earlier rows
  go in an `<earlier-summaries>` section ahead of the transcript, as context it
  must not repeat; their size comes off the budget of each pass, and they take
  at most half of it, the oldest left out of the request first: the chain is
  sized against the session's window, and a smaller `compaction.model` or a
  switch to a smaller model would otherwise leave passes with no room for the
  transcript. A request with no earlier rows is the one `main` sends.
- **In-turn folds join the chain.** The first fold of a turn writes the row
  that begins with the prompt (#495); a second fold of the same turn adds a
  block without it, so a request carries the prompt once. The fold's step
  budget leaves room for the chain, which is in every request after it.
- **Merge with hysteresis.** The chain may take a third of the session model's
  window; past that, the earliest rows are merged until it is down to half of
  that share, the merged row counted as long as the longest row it retells
  (the summarizer is asked for no more). The row just written is never merged.
  The rows of the turn in progress go last, when the earlier ones are not
  enough: one long turn folded again and again would otherwise grow a chain
  that only ends the turn, and the merged row then carries the turn's prompt in
  front, as the turn's first row did. Both numbers are constants: #495 removed
  the keys a fold had rather than adding new ones, and nothing measured here
  asks for a knob.

## 2. Method

`examples/httpserver/http_e2e_compact_chain_recall.py` boots `coddy serve`
(`-tags http`) against `openai/gpt-5.4-mini` with automatic compaction off and
`keep_recent_turns: 1`, and drives one session through 8 rounds: a turn that
states 3 project facts with random values (`<word>-<4 digits>`, seed 361, the
same facts in every run), a turn of about 250 words of unrelated design prose,
and `POST /coddy/sessions/{id}/compact`. The facts of round 1 go through 8
compactions, those of round 8 through one. A last turn asks for all 24 values
from memory, one line each or `unknown`, and the harness counts exact values.
Cache figures are the `llm call usage` debug lines of the server log for the
turns after the first compaction (the quiz left out). Three arms:

- `main` - a binary built from `c9e5360f`;
- `chain` - this branch, `max_context_tokens: 400000`, so no merge happens;
- `chain-merge` - this branch, `max_context_tokens: 8000`, so the chain is
  merged from the fourth or fifth compaction on (12-13 summary rows at the end
  instead of 8).

Runs went up to 15 at a time in parallel on separate ports.

## 3. Results

Facts recalled out of 24, per run, sorted:

| Arm | Runs | Recalled | Mean | All 24 |
| --- | --- | --- | --- | --- |
| `main` | 10 | 3, 3, 3, 3, 9, 15, 15, 18, 24, 24 | 11.7 | 2 of 10 |
| `chain` | 10 | 21, 24, 24, 24, 24, 24, 24, 24, 24, 24 | 23.7 | 9 of 10 |
| `chain-merge` | 7 | 3, 24, 24, 24, 24, 24, 24 | 21.0 | 6 of 7 |

Share of the facts of each round recalled (round 1 went through 8 compactions):

| Arm | R1 | R2 | R3 | R4 | R5 | R6 | R7 | R8 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `main` | 20% | 20% | 30% | 50% | 50% | 60% | 60% | 100% |
| `chain` | 90% | 100% | 100% | 100% | 100% | 100% | 100% | 100% |
| `chain-merge` | 86% | 86% | 86% | 86% | 86% | 86% | 86% | 100% |

On `main` the model answers `unknown` for what it lost (21 of 24 in the
worst runs): the facts were retold away, not misread, and the earliest go
first. One `chain` run lost the 3 facts of round 1. One `chain-merge` run
recalled only round 8, the pattern of the worst `main` runs; it came from the
first batch, before the harness kept the answer, so its cause is not known.

Cache. The share of input tokens served from the provider's cache after the
first compaction varied between runs of the same arm by more than between the
arms (71-95% in all three), so the totals do not separate them. The call right
after each compaction does: in two runs started together, the uncached part of
that call grew on `main` with the summary it had just rewritten (1136, 1546,
1757, 1851, 1891, 2040, 2264, 2332 tokens) and stayed at the new block and the
tail on the chain (1072, 967, 1349, 1762, 1159, 1093, 1025, 1684), 10111
tokens against 14817 over the eight compactions, while the cached prefix of
the chain grew from 14976 to 17024 tokens. The price is a longer window: the
last request of those runs was 18708 tokens on the chain and 16796 on `main`,
which is what the merge bounds.

## 4. Not measured

- Another model or provider. The harness takes `RECALL_MODEL` and
  `RECALL_API_BASE` for any OpenAI-compatible one.
- Facts stated in prose rather than as a list, and a summarizer other than the
  session's model.
- The merge under an automatic trigger and in-turn folds on a live model; the
  unit tests and `TestPromptCacheInTurnFoldRewritesTheHistoryOnlyAtTheFold`
  cover them with a stub.
