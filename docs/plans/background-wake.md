# Background wake on every surface

Design record for issue #305 and the branch `feat/background-wake-surfaces`. It covers which
process turns a finished `notify_on_finish` task into a turn, how that turn is recorded, how
every surface tells it apart from a message somebody typed, and who answers a permission prompt
raised inside it.

## What was asked

1. The console (bare `coddy`) and `coddy acp` wake the agent; where nothing can wake it, the tool
   result says so instead of promising a turn.
2. The first message of a woken turn reads as a wake-up, not as a user message, on every surface:
   the web UI live and after a reload, the console local and over `--remote`, ACP, Telegram. It
   names every task with its outcome.
3. A Telegram chat receives the answer of a turn woken in its session, and a `coddy serve` without
   the HTTP server still wakes.
4. The Tasks panel and `/tasks` show which running tasks will wake the agent.
5. A permission prompt raised in a woken turn reaches a surface that can answer it.

## What already exists

- `agent.BackgroundWaker` batches finished tasks per session, retries a session whose turn is
  still running (`ErrSessionTurnBusy`, #176) and runs one turn per batch through a `RunTurnFunc`.
  The turn starts from `WakeInstruction`, a user-role message.
- Only `external/httpserver` attaches a waker (`Server.attachBackgroundWaker`, from the
  constructor). The woken turn runs through the composer relay with a non-interactive sender,
  which denies a gated tool unless the server runs in bypass.
- `env.WakeableSession` is true for every top-level session, so `run_command` and `spawn_agent`
  promise a wake on every surface.
- The Telegram bot keeps a persisted map chat key -> session id (`sessionstore`), and `KeyFor`
  finds the chat of a session. `serve.Runtime` already offers a detached subagent's permission
  prompt to every surface that is up and takes the first answer.
- The relay stamps each frame with the message history revision, so a frame sent before the
  message it describes is persisted is not replayed to a client whose snapshot holds the message.
- `fix/background-wake-console-acp` (`0ab78d79`) had the console and ACP waker and `Pool.CanWake`,
  363 commits behind `main`. It is not merged; the parts that still fit are carried forward here.

## Decisions

### Who owns the waker

The waker belongs to the process, not to one surface, and exactly one is subscribed to the pool
(`bgtask.WakeWatcherKey`, a keyed subscription):

| Process | Waker | Runs the woken turn |
|---|---|---|
| bare `coddy` (local console) | `App.attachBackgroundWaker` | on the UI goroutine, the console's own sender |
| `coddy acp` | `acpWakeRunner` | through the manager, the ACP server as sender |
| `coddy serve` | `serve.Runtime` | the surface that owns the session, see below |
| `coddy -p`, `coddy --remote`, `coddy acp --remote` | none | nothing local to wake; a remote server wakes on its own |

`Pool.CanWake` reports whether that subscription exists. `run_command` and `spawn_agent` promise a
wake only when it does; otherwise the result says that nothing will wake the model and names the
tools that follow the task. The task record keeps `notify_on_finish` as the model asked for it
only where a wake can happen; a child or a scheduled run still never registers one.

`httpserver.New` no longer subscribes. `coddy serve` hands the HTTP server to the runtime as a
wake surface, and a test that wants the HTTP path alone attaches it with
`Server.AttachBackgroundWaker`.

### Which surface runs a woken turn under `coddy serve`

`serve.Runtime.RunBackgroundWake` asks the registered surfaces in two ranks:

1. A surface that owns the conversation: the Telegram bot, when a chat is bound to the session.
   The turn runs exactly like a chat message - the chat sender, mirrored into the HTTP relay - so
   the answer lands in the chat and a browser watching follows it. Permission semantics are the
   chat's own: the chat's agent is allowed what it asks, a subagent is asked about in the chat.
2. A surface that can host any session: the HTTP server. The turn runs through the composer relay
   as before, but its sender now asks permission interactively: the `permission` frame goes to the
   relay, the pending record is persisted, and the answer comes through
   `POST /coddy/sessions/{id}/permission` from the web UI or from a console following the turn.

With neither up, the runtime runs the turn itself through the manager's default sender, which
refuses a gated tool unless the operator chose bypass: that is the one case where no surface can
answer. Questions stay non-interactive in a woken turn, as before.

### The record of a wake

The woken turn's first message stays a user-role message: the model reads the instruction as it
did, and the provider's cached prefix is untouched. It gains a structured marker,
`llm.Message.BackgroundWake` (`background_wake` in `messages.json` and in
`GET /coddy/sessions/{id}/messages`): the tasks with id, kind, label, agent, status, exit code,
duration and error.

The waker passes the batch to the surface as `agent.Wake`; the surface hands the record to the
manager in `PromptRunOpts.BackgroundWake`; the manager holds it on the state for the length of the
turn (like the surface system prompt) and publishes a `woken` turn event; the agent takes it once
when it builds the user message, sends the `background_wake` session update first and persists
the marked message after it - the order every other message frame follows, so a client that
reloads between the two is not shown the wake twice.

### How each surface shows it

The first cut drew a note where the user's message would stand on every surface: a bell, *Woken by
a finished background task* and one line per task. Seen on the web UI it was noise between two
answers of the same work, so the surfaces that list the tasks next to the conversation show
nothing at all, and the task list carries the mark instead (next section).

- **Web UI.** `background_wake` is a named event on the turn stream and on the relay; the messages
  endpoint carries the field after a reload. Both become a `background_wake` transcript item that
  renders as nothing: the answer follows the previous turn as the work carrying on. It is not a
  user bubble - it cannot be edited or retried - and it still counts as a turn, so UI notices, the
  live status line, edit indices and branches stay attached to the right turn.
- **Console.** The same update adds nothing to the transcript; the woken turn's answer starts a
  block of its own. A wake for a session the console is not showing is held (the waker retries as
  for a busy turn) and a one-time dim line says where it will continue.
- **Console over `--remote` and `coddy acp --remote`.** `GET /coddy/events` announces
  `background_wake` (the `woken` turn event). The remote client follows that turn through
  `GET /coddy/sessions/{id}/composer-stream`, translating the frames as it does for its own turns,
  so the answer and a permission prompt reach it. A client that attaches later - it opens the
  session while the woken turn runs, or reconnects - learns of the turn from
  `GET .../activity` (`backgroundWake`) and from the events snapshot, which replays
  `background_wake` after `turn_started`. Both date the wake at the turn's start, which names the
  turn: a new turn is read after the transcript the client loaded (`since_rev`), the same turn
  again after the last frame applied (`Last-Event-ID`), so nothing is shown twice.
- **ACP.** The `background_wake` session update, plus an `agent_message_chunk` with a one-line
  note for editors that ignore Coddy's own updates: an editor has no task list beside the thread.
  `session/load` replays a wake the same way.
- **Telegram.** The chat receives the note as a message of its own before the answer, for the same
  reason.

### The notify flag on a task

`notify_on_finish` is already on the task row. A running task that will wake the agent shows a
bell after its title on the card and in `/tasks`, with the hover text saying so; `background_list`
and the turn context say it to the model too, so it does not wait on a task it will be woken for.

Once the woken turn begins, the agent marks its tasks in the pool (`Pool.MarkWokeAgent`,
`woke_agent` on the row, persisted in the task's record), before the `background_wake` update goes
out, so a surface that reads its task list on that update finds the mark. A finished task keeps
the bell only with the mark: `notify_on_finish` alone would also mark a task whose wake never came
(the process went away, the session stayed busy past the give-up window, the wake cap). The mark is
set without notifying the pool's watchers - the waker would read a finished task arriving again as
a second outcome.
