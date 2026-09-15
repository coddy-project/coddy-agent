# Context compaction

A model reads a bounded context window, and a working session outgrows it: every exchange, every tool result and every file page stays in the transcript. Compaction keeps long sessions inside the window without losing the record. Older history is folded into a generated summary that the model reads in place of the original rows, the most recent turns stay verbatim, and a second, cheaper mechanism collapses the file pages and search results the model has moved past. Both are projections built when a request goes to the model: the transcript on disk keeps every original message and every full result.

## The /compact command

```text
/compact [instructions]
```

The built-in `/compact` runs on every prompt surface - the console, ACP editors, the web UI composer and `POST /v1/responses` - the way `/export` and `/plugin` do: it is recognised before the text becomes a message, without a turn of the main model. It is listed in the command catalog (`GET /coddy/commands`, the ACP `available_commands_update`) only while `compaction.enable` is true. Anything after the command is handed to the summariser as additional instructions, so `/compact focus on the file paths and the failing test` steers what the summary keeps. Ask mode does not restrict it: the command is an operator action, outside the read-only boundary.

A manual compaction is forced. It folds whatever exists: when the configured number of kept turns leaves nothing to summarise, it retries with fewer kept turns, down to none, so even a short conversation compacts. The command text is persisted as a user row so the transcript shows it, and the reply is one line:

```text
Context compacted: 14 message(s) summarized, 6 kept verbatim.
Nothing to compact: there is no earlier conversation to summarize yet.
Compaction is disabled in the configuration (compaction.enable: false).
```

Over HTTP the same action is `POST /coddy/sessions/{id}/compact` with an optional body `{"instructions": "..."}`; it answers with the summary and the message counts, `400` when compaction is disabled, `409` while a turn holds the session, while it is being deleted and for a read-only child session ([HTTP API](../reference/http-api.md)). It runs as a turn of the session, so `GET /coddy/events` announces its start and end and a browser tab viewing the session reloads what changed.

## Automatic compaction

The agent estimates the context it is about to send - system prompt, tool definitions, rules, skills, MCP and the conversation - and compares it with the context window of the session's model. When the estimate reaches `compaction.threshold_percent` of the window (80 by default) the history is compacted before the call: once before the first model call of a turn, a turn resumed after a permission answer included, and again between rounds when tool results grew the context past the threshold mid-turn. Any failure - a summariser error, a hook veto - is logged and the turn continues uncompacted.

### The context window

Every reader resolves the window the same way - the trigger, the `usage_update` behind the console's context percentage, and the `max_context_tokens` of `GET /v1/models` that the web UI draws its context ring against - so what the ring shows is what the trigger measures:

1. the model entry's `max_context_tokens`;
2. the window the provider's model listing reports for the model: `limit.context` (the NeuralDeep hub), `context_length` (OpenRouter), `max_model_len` (vLLM), `max_context_length` (LM Studio) or `context_window`. The listing is read for `neuraldeep` providers and for `openai` providers with an explicit `api_base`, never for api.openai.com, Anthropic or Codex, whose listings carry no window. It is read when a turn starts or the model list is served, with a turn waiting at most three seconds for a listing that has never answered, and it is trusted for an hour; a failed read is retried after five minutes;
3. 128000.

Set `max_context_tokens` when the provider reports no window, or a larger one than the deployment actually serves (a local server started with a smaller context).

### Few long turns

Automatic compaction keeps `keep_recent_turns` user turns verbatim when the window holds more than that. When it does not - a session of a few long agent turns, where the context outgrows the window without many prompts - it keeps fewer, down to the prompt being answered, which it never folds. With only that prompt in the window there is nothing to compact: the turn logs it once and continues.

## The model can ask for it

The threshold is a guess made before a call; the model is the one that knows what it just read. A build log it pasted, a file it no longer needs, a search that returned far more than expected - the model sees the context fill and can fold the history itself with the `compact_context` tool, instead of waiting for the trigger or for the operator to type `/compact`:

```json
{"instructions": "keep the file paths and the failing test"}
```

`instructions` is optional and steers the summary exactly as the text after `/compact` does. The call behaves like a manual compaction - it folds whatever exists - and the tool answers with the same line the command does, so the model reads back what it did and continues on the shortened history: the loop rebuilds the request from the folded transcript before its next call. The tool is offered in agent and plan mode, and hidden entirely when `compaction.enable` is false. Ask mode does not get it, because every tool offered there is read-only; the operator's `/compact` and the automatic trigger still work in that mode.

## A history larger than one summarization request

Before any of it, the head is deduplicated: a line the conversation already carried verbatim is dropped, keeping the first copy where it stands, and each entry says how many repeats went. A session fills its window by repeating itself - the same build log pasted after every attempt, the same file read a dozen times - and the summariser learns nothing from the second copy while paying for it in full (issue #273). Short lines are left alone: a closing brace and a bare number are structure, and dropping them would mangle the code the summary has to read.

Folding used to be one call: the whole head of the conversation in a single request to the summariser. That holds while the session is near the window it is measured against, and stops holding exactly when compaction matters most. A session that ran far past its window - a model that kept reading large files, an automatic trigger that never fired because the window was unknown - arrives at `/compact` with a history several times the summariser's own window, and the provider refuses the request:

```text
compaction LLM call: 400 Bad Request: this model's maximum context length is exceeded
```

That left the session stuck: too large to send, and the only thing that could shrink it was the call that would not go out.

A summariser that refuses is not the end of it either. `compaction.fallback_models` lists the models tried, in order, when the one before them fails, and the session's own model is the last resort whether or not it is listed - so a `compaction.model` pointing at a deployment that is down, overloaded or gone no longer leaves a full session with no way out (issue #247).

Such a history is now folded in passes. Each pass carries the summary of everything folded so far plus the next run of transcript, both sized against the summariser's own context window (`compaction.model`'s, when it names another model), and answers with one summary covering both. The last pass's answer is what goes into the transcript, so a compaction that took seven calls leaves the session looking exactly like one that took a single call. A pass the provider still refuses is retried with fewer messages, and a single entry too large even on its own is sent with its middle elided, head and tail kept - the fold makes progress rather than stopping on the one message it exists to fold away. `POST /coddy/sessions/{id}/compact` reports the passes as `steps`.

## What the session shows while it runs

Every compaction, whichever way it started, draws the row a tool call draws: announced as `compact_context` when it begins, updated with the pass it is on while a multi-pass fold runs (`compacting context: pass 3 of 7`), and closed with what it folded. Before this the session simply went quiet for as long as the summariser took, which a fold of several calls over a large history makes hard to sit through.

![The compact_context row while a compaction runs](../assets/compaction/compact-row-running-dark-1280.png)
*`/compact` sent from the composer: the row says what is running, above the answer of the compaction before it. Captured from the running UI.*

The row a compaction draws for itself is live: it reaches whoever is watching the session - the composer that sent `/compact`, a second browser tab, a console on `--remote` - and what stays in the transcript afterwards is the compaction summary row. A compaction the model asked for is different, because there the row already exists: the `compact_context` call reuses it, so the passes appear under the call that ordered them, and it stays in the transcript like any other tool call.

## What is kept and what is summarised

The boundary is the `keep_recent_turns`-th most recent user message (2 by default). Everything from that message on - the user turns, the replies and the tool activity after each - stays verbatim. Everything before it is sent to the summariser as a flattened transcript, tool calls rendered as labelled lines, and is replaced in the model's view by one summary row inserted at the boundary. A previous summary is part of the older history, so a second compaction folds it into the new one and the model always sees exactly one. `keep_recent_turns: 0` summarises the whole window.

The summary is requested from `compaction.model` when set, otherwise from the session's model, with a fixed system prompt that asks, in order, for the user's goals and constraints, the decisions taken and the approaches rejected, the state of the work, the exact paths, names, commands and values that matter, and the open questions and next steps. The result-eviction projection below is applied to the history first, so a page the model had already moved past is not summarised in full.

## The summary row and the context estimate

The summary row is a user-role message flagged `compaction_summary` that starts with `The earlier conversation was compacted. Summary of the compacted part:`; the model's window begins at the latest such row, and the rows before it stay in `messages.json` for the transcript only. `PreCompact` hooks run before either trigger and may veto it, `PostCompact` hooks receive the summary ([Hooks](hooks.md#events)).

After a compaction the context estimate is recomputed and published as a `usage_update` with `used` and `size`, which is what the composer's context ring and the console footer's context percentage show; `GET /coddy/sessions/{id}/stats` returns the same breakdown by category. Every client of a shared session reads the smaller number: the tab that sent the turn from its own stream, another tab watching the turn from `GET /coddy/sessions/{id}/composer-stream` (the same frames), and a tab that only views the session from the stats it reloads when `turn_ended` arrives. A compaction folds the turns before the kept tail, so the numbers fall when the bulk of the context sits in older turns; a large last message stays verbatim until newer turns push it past `keep_recent_turns`.

The web UI also updates the session's context window from `usage_update.size` on either stream. If a provider listing arrives after `/v1/models` returned the 128000 fallback, the ring adopts the reported window without a reload. Stats refreshes preserve that live window; selecting a different model or saving the configuration uses the model listing until a fresh usage update arrives. A window received for one session never changes another session's ring.

![Context using the fallback window](../assets/compaction/context-window-before-dark-1280.png)
*Before the provider answers: the same 120000 tokens fill 93.8% of the fallback window. Captured from the running UI with a stand-in provider.*

![Context using the late provider window](../assets/compaction/context-window-after-dark-1280.png)
*After the stream reports a 262144-token window: 45.8%, without reloading. Narrow layout: [before](../assets/compaction/context-window-before-dark-390.png) and [after](../assets/compaction/context-window-after-dark-390.png).*

## What the transcript shows

In the web UI the summary is a foldout row labelled **context compacted**, styled like the thinking disclosure, whose body renders the summary - what is now in the model's context. The row sits at the boundary, above the turns kept verbatim, not at the end of the chat; after a forced compaction of a short session it is the last row. The `/compact` command and its one-line reply are ordinary user and assistant rows. `GET /coddy/sessions/{id}/messages` returns every original row, the summary carrying `compaction_summary: true`, and an export writes it as a `compaction_summary` entry ([Session export](session-export.md)). An ACP client, and with it the console, sees the row on replay as a user message beginning with the preamble above.

## Result eviction

Paging a large file or running a wide search would otherwise pin every result in the context for the rest of the session. Result eviction collapses `read` and `grep` results the model has moved past to short placeholders when the request is built, so the tool call and its result stay paired for the provider while the bulk is gone:

```text
[evicted: internal/agent/react.go lines 1-200, not marked as useful; re-read if this range is needed again]
[evicted: grep "maybeAutoCompact" in internal/agent, not marked as useful; re-run the search if needed]
[evicted: internal/agent/react.go was modified after this read; re-read for current contents]
```

A result survives when it is inside the working window - the `keep_recent` most recent candidates (2 by default, so a read and a grep can be compared) - or when the model marked it useful: `keep: true` on the `read` or `grep` call itself, or a later `keep_result` call naming the page (`path`, optionally `offset` and `limit`) or the search (`pattern`, optionally `path`). `keep_result` reads nothing; the call recorded in the history is the pin. A successful write to a file (`write`, `edit`, `apply_patch`, `mv`, `rm`, `touch`, `mkdir`, `rmdir`; not a denied or failed call) makes every earlier read of that file and every grep whose search root or hits touched it stale, pinned or not: they collapse with the "was modified" placeholder so the model re-reads current contents. Results at or below `min_result_bytes` (2000) are never candidates. The tool descriptions tell the model all of this, and `keep_result` is offered in every mode, ask mode included.

Eviction is the second projection over the same history: the persisted transcript keeps every result in full, and `GET /coddy/sessions/{id}/tool-calls/{toolCallId}` serves it. It complements `tools.output_limits`, the per-tool line caps that bound a result when it is produced ([config.yaml reference](../reference/config.md#toolsoutput_limits)).

## Configuration

```yaml
compaction:
  enable: true             # master switch: the command and the automatic trigger
  threshold_percent: 80    # auto-compact at this percent of the model's context window (1..100)
  keep_recent_turns: 2     # user turns kept verbatim; 0 summarises everything
  model: ""                # models[].model for the summariser; empty = the session's model
  result_eviction:
    enable: true
    keep_recent: 2         # most recent read/grep results kept intact
    min_result_bytes: 2000 # results at or below this size are never evicted
    start_percent: 50      # evict only once the context reaches this percent of the window
```

| Key | Default | Meaning |
|---|---|---|
| `enable` | `true` | compaction at all: the command, the REST route and the automatic trigger |
| `threshold_percent` | `80` | the automatic trigger, as a percent of the model's [context window](#the-context-window) |
| `keep_recent_turns` | `2` | user turns (with the activity after each) that stay verbatim |
| `model` | `""` | a `models[].model` for the summariser when the session's model should not summarise its own history |
| `result_eviction.enable` | `true` | collapse superseded `read` and `grep` results |
| `result_eviction.keep_recent` | `2` | most recent candidates kept as the working window |
| `result_eviction.min_result_bytes` | `2000` | results at or below this size are left alone |
| `result_eviction.start_percent` | `50` | how full the context must be before eviction starts; `0` evicts from the first result |

`start_percent` exists because eviction and the provider's prompt cache pull in
opposite directions. A placeholder is written **into the middle of the replayed
history**, and a provider caches a request by its prefix, so one collapsed result
throws away the cached copy of every message behind it. With a sliding working
window that happens on nearly every step, and a long conversation is then
reprocessed at full price to save a few thousand tokens the window was not short
of yet. Below the mark the history therefore goes out exactly as the provider
already has it; above it, the room matters more than the cache. A model entry
without `max_context_tokens` cannot be measured and evicts from the first result,
as it always did. See *The turn context block* in
[react-agent.md](../contributing/react-agent.md) for the other half of the same
story - what Coddy stopped putting in the system prompt for the same reason.

The field table with types and validation is in the [config.yaml reference](../reference/config.md#compaction); the keys are ordinary settings, editable in the web UI's Settings on the **Context compaction** tab, which follows **ReAct agent** because it is the same loop deciding what to send the model (`#/settings/compaction`). The window the threshold is a percent of belongs to the model entry: its `max_context_tokens`, else what its provider reports, else 128000 ([The context window](#the-context-window)).

## Testing

- Executable specs in `features/`: `context_compaction.feature` (the kept turns, the summary in the next request, the smaller context over ACP, a history folded in passes with the row the client watches, and the model folding its own history through `compact_context`; harness `internal/agent/bdd_compaction_test.go`), `context_compaction_command.feature` (`/compact` over the prompt surface and the REST endpoint; `external/httpserver/bdd_compaction_http_test.go`), `context_compaction_auto.feature` (a prompt over the threshold compacts before the reply, and a model without `max_context_tokens` compacts at the window its provider reports, the one `GET /v1/models` shows; `external/httpserver/bdd_compaction_auto_test.go`), `context_result_eviction.feature` (marked pages and searches survive, unmarked ones collapse, the output limit; `internal/agent/bdd_result_eviction_test.go`).
- Unit tests: `internal/session/compaction_test.go` (the split index and the visible window), `internal/session/context_window_test.go` (the resolution order, which providers are asked, the listing cache and its bounded wait), `internal/llm/model_list_test.go` (the window fields of a listing), `internal/agent/react_test.go` (the threshold, the fewer kept turns, the resumed turn), `internal/agent/compact_fold_test.go` (the pass budget and its floor, where a pass is cut, an entry elided because it does not fit alone, the retry on a refused pass, and the prompt being answered surviving `keep_recent_turns: 0`), `internal/agent/result_eviction_test.go` (pins, staleness, placeholders), `internal/config/compaction_test.go` (defaults and validation).
- Live, against a real provider:
  - `examples/httpserver/http_e2e_compact_auto.py` boots its own `coddy serve` with a model that has no `max_context_tokens`, checks that the web UI's window, the stream's `usage_update` and the trigger agree, and that the session compacts without `/compact`;
  - `examples/httpserver/http_e2e_compact_clients.py` plays three clients of one session - the sender, a second tab on the composer stream, an idle viewer on the stats - and checks that all of them read the context usage fall after `/compact`, after `POST .../compact` (announced on `GET /coddy/events`) and after an automatic compaction;
  - `examples/cli/cli_e2e_compact.py` drives the console in a pty and checks that the footer's context percentage falls after `/compact` and stays down on the next turn.
