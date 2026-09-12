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

Over HTTP the same action is `POST /coddy/sessions/{id}/compact` with an optional body `{"instructions": "..."}`; it answers with the summary and the message counts, `400` when compaction is disabled, `409` while a turn holds the session and for a read-only child session ([HTTP API](../reference/http-api.md)).

## Automatic compaction

The agent estimates the context it is about to send - system prompt, tool definitions, rules, skills, MCP and the conversation - and compares it with the model's `max_context_tokens`. When the estimate reaches `compaction.threshold_percent` of it (80 by default) the history is compacted before the call: once before the first model call of a turn, and again between rounds when tool results grew the context past the threshold mid-turn. A model entry without `max_context_tokens` in `config.yaml` never compacts automatically; `/compact` still works. Automatic compaction is not forced: it needs more user turns than `keep_recent_turns` in the visible window, and any failure - nothing to compact, a summariser error, a hook veto - is logged and the turn continues uncompacted.

## What is kept and what is summarised

The boundary is the `keep_recent_turns`-th most recent user message (2 by default). Everything from that message on - the user turns, the replies and the tool activity after each - stays verbatim. Everything before it is sent to the summariser as a flattened transcript, tool calls rendered as labelled lines, and is replaced in the model's view by one summary row inserted at the boundary. A previous summary is part of the older history, so a second compaction folds it into the new one and the model always sees exactly one. `keep_recent_turns: 0` summarises the whole window.

The summary is requested from `compaction.model` when set, otherwise from the session's model, with a fixed system prompt that asks, in order, for the user's goals and constraints, the decisions taken and the approaches rejected, the state of the work, the exact paths, names, commands and values that matter, and the open questions and next steps. The result-eviction projection below is applied to the history first, so a page the model had already moved past is not summarised in full.

## The summary row and the context estimate

The summary row is a user-role message flagged `compaction_summary` that starts with `The earlier conversation was compacted. Summary of the compacted part:`; the model's window begins at the latest such row, and the rows before it stay in `messages.json` for the transcript only. `PreCompact` hooks run before either trigger and may veto it, `PostCompact` hooks receive the summary ([Hooks](hooks.md#events)).

After a compaction the context estimate is recomputed and published as a `usage_update` with `used` and `size`, which is what the composer's context ring and the console footer's context percentage show; `GET /coddy/sessions/{id}/stats` returns the same breakdown by category.

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
  threshold_percent: 80    # auto-compact at this percent of models[].max_context_tokens (1..100)
  keep_recent_turns: 2     # user turns kept verbatim; 0 summarises everything
  model: ""                # models[].model for the summariser; empty = the session's model
  result_eviction:
    enable: true
    keep_recent: 2         # most recent read/grep results kept intact
    min_result_bytes: 2000 # results at or below this size are never evicted
```

| Key | Default | Meaning |
|---|---|---|
| `enable` | `true` | compaction at all: the command, the REST route and the automatic trigger |
| `threshold_percent` | `80` | the automatic trigger, as a percent of the model's `max_context_tokens` |
| `keep_recent_turns` | `2` | user turns (with the activity after each) that stay verbatim |
| `model` | `""` | a `models[].model` for the summariser when the session's model should not summarise its own history |
| `result_eviction.enable` | `true` | collapse superseded `read` and `grep` results |
| `result_eviction.keep_recent` | `2` | most recent candidates kept as the working window |
| `result_eviction.min_result_bytes` | `2000` | results at or below this size are left alone |

The field table with types and validation is in the [config.yaml reference](../reference/config.md#compaction); the keys are ordinary settings, editable in the **Context compaction** section of the web UI's Settings. The one thing a model entry needs for the automatic trigger is `max_context_tokens`: without it the threshold has nothing to compare against.

## Testing

- Executable specs in `features/`: `context_compaction.feature` (the kept turns, the summary in the next request, the smaller context over ACP; harness `internal/agent/bdd_compaction_test.go`), `context_compaction_command.feature` (`/compact` over the prompt surface and the REST endpoint; `external/httpserver/bdd_compaction_http_test.go`), `context_compaction_auto.feature` (a prompt over the threshold compacts before the reply; `external/httpserver/bdd_compaction_auto_test.go`), `context_result_eviction.feature` (marked pages and searches survive, unmarked ones collapse, the output limit; `internal/agent/bdd_result_eviction_test.go`).
- Unit tests: `internal/session/compaction_test.go` (the split index and the visible window), `internal/agent/result_eviction_test.go` (pins, staleness, placeholders), `internal/config/compaction_test.go` (defaults and validation).
