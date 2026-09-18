# Turn progress and background task controls

Design record for the branch `feat/turn-progress-and-tasks`. It covers what an operator sees
while a long turn runs - in the web UI and in the console - and how the operator reaches the
background tasks of a session from the console.

## What was asked

1. The total running time of a turn, so a long command shows progress.
2. The tokens the model generated in this turn's loop.
3. Before the first token of a turn arrives: just the clock and a "waiting for the model" phrase.
4. The count of background tasks running right now, and an opener for the tasks panel that does
   not scroll away with the transcript (top right of the chat).
5. The same line in the console.
6. In the console, a way for the operator to list and manage running background processes. The
   agent already has `background_list` / `background_output` / `background_wait` /
   `background_stop` / `background_reap`; the operator has nothing.
7. All of it has to work against a remote server (`--remote`, `coddy acp --remote`, the web UI's
   remote environment) and through a swarm relay.

## What already exists

- A live status line next to the typing dots (SPA, `chat/liveStatus.ts`) and next to the spinner
  (console, `external/cli/status.go`): verb, target and a counter of the *current step*. The
  waiting phrase escalates at 15 s and 60 s. Both derive everything client-side.
- `token_usage` after every completed LLM call: per-call input and output, plus a turn-cumulative
  `totalTokens` (input and output summed). Nothing is published while a call streams.
- The active-turn registry of `session.Manager` counts turns per session and publishes
  `turn_started` / `turn_ended`. It does not keep when the turn started, and the snapshot of
  `GET /coddy/events` stamps a turn already in flight with `time.Now()`.
- `GET /coddy/sessions/{id}/activity` reports `turnActive` and no timestamps.
- The composer relay replays a late subscriber only the frames its transcript snapshot does not
  hold (`since_rev`), so a frame written before the assistant message was persisted is not
  replayed after a reload.
- The background task pool, its REST routes and the docked Tasks panel of the SPA, opened by a
  chip at the end of the transcript.

## Decisions

### One server-side signal: `turn_progress`

The clients could each estimate tokens from the deltas they see, but they do not see tool-call
arguments (a `write` call is mostly arguments), and three clients would carry three estimators.
The agent sees everything, so it publishes one update:

```json
{"sessionUpdate":"turn_progress","startedAt":"2026-09-18T10:00:00Z","elapsedMs":45210,
 "outputTokens":433,"estimated":true}
```

- `startedAt` is when the turn was admitted (`beginTurn`), the moment the operator's prompt was
  accepted. `elapsedMs` is the same fact as a duration, so a client whose clock disagrees with
  the server's subtracts it from its own clock instead of trusting `startedAt`.
- `outputTokens` is the output of the turn's completed LLM calls as the provider reported it,
  plus an estimate (`session.EstimateTokens`, runes / 4) of what the call in flight has streamed:
  text, reasoning, and the arguments of a tool call once it completes. `estimated` is true while
  any part of the number is an estimate. A provider that reports no usage leaves the estimate in.
- It is sent when the loop starts (zero tokens - the clients render the clock alone), at most
  once a second while a call streams, and after every call with the exact number.
- The same numbers are kept on the session state, and `GET /coddy/sessions/{id}/activity`
  returns them (`turnStartedAt`, `turnElapsedMs`, `turnOutputTokens`, `turnTokensEstimated`)
  while a turn is active in this process. That is the recovery path after a reload: the relay
  does not replay frames the transcript snapshot already covers, and the SPA polls `/activity`
  every two seconds anyway.
- The events snapshot replays a turn in flight with its real start instead of `time.Now()`.

Transport: the HTTP bridge maps it to `event: turn_progress`; the OpenAI-strict view turns named
events into comments already; `internal/remote` maps the frame back to the ACP update; the swarm
mount is a byte-transparent reverse proxy; ACP over stdio passes any update through; the
Telegram sender ignores kinds it does not know.

### The line

`<turn clock> · <tokens> · <running tasks> · <phrase> [· <step clock>]`

- The turn clock leads. Tokens appear once there are any, so a turn that has not heard from the
  model reads `57s · Waiting for the model`.
- The step clock stays only on steps that run something other than the model (a tool call, the
  memory run): `2m 05s · 1.2k tokens · Running make test · 45s`. The model's own phases
  (waiting, thinking, writing) are covered by the turn clock, and a second clock equal to the
  first one reads as a glitch.
- The two operator gates keep the turn clock - it is wall time since the prompt - and still show
  no step clock, because nothing runs while the operator decides. The existing rule "a gate
  renders no counter" was about the step counter, and is reworded to say so.
- The running-task count leaves out system tasks (the memory run of every turn), like
  `Pool.RunningCount` does. In the SPA the segment is a button that opens the Tasks panel.
- A client that has not received a `turn_progress` yet (an older server, a turn held by another
  process) falls back to the creation time of the turn's user message, without tokens.

### Opening the Tasks panel

The chat header gets a control at its right edge: the running dot and `N running`, or the task
count once everything finished; nothing in a chat that never ran a task. The header is sticky,
so the opener no longer scrolls away. The chip under the last message stays, and steps aside in
exactly one case: a turn is running and tasks are running, because the status line right above
it then carries the same count and opens the same panel. With a turn running and nothing but
finished tasks the status line says nothing about tasks, so the chip keeps its place.

"Running tasks" means one thing in the status line, the header control and the chip: tasks
whose row says `running`, system tasks left out (one helper, `countRunningTasks`). Otherwise the
memory run of every turn would make the line read 1 while the chip reads 2.

### Console: `/tasks`

An overlay in the place of the editor, like `/resume`, with the tasks in one order - by start
time, newest first, as in the SPA. Enter opens a task: its status line and the tail of its
output, refreshed while it runs; `s` stops it; escape goes back. The console reaches the tasks
through two new `backend` methods: the in-process pool locally, the REST routes under
`--remote`. The footer names running tasks between turns, so the operator knows there is
something to open.

### Agent: running tasks in the turn context

The turn context block (after the history, so the prompt cache holds) gets a
`## Background tasks` section listing the session's running tasks - id, command or label,
elapsed, silence - when there are any. The model no longer needs a `background_list` call to
remember what it left running.

## Out of scope

- Tool-call argument deltas from the providers. The estimate catches the arguments when the call
  completes; the exact number follows at the end of the call.
- Progress for a turn another process holds (`TurnLockHeld` without an in-process turn).
- A console view of a foreign turn's transcript.
