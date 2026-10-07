# Session goal and supervisor

A session goal keeps the agent working until a second model confirms the result. `/goal <objective>` sets the goal and starts work on it at once. After every turn the **supervisor** checks what the turn actually did against the objective. When work is left, it starts another turn that names what is missing. It stops when the check is met, when the work needs you, or when a budget runs out. A **watchdog** watches the same turns for stalls and loops.

The supervisor is a second model rather than the worker grading itself. It judges the evidence the harness recorded, such as tool calls, their results and the files changed, and treats the worker's final message as a claim. Before a goal is closed, a read-only **verifier** subagent opens the workspace and confirms every requirement.

## Commands

| Command | What it does |
|---|---|
| `/goal [-m\|--model <id>] [-r\|--reasoning <level>] <objective>` | Sets the goal, replacing any earlier one, and starts a turn on it right away. The objective is limited to 4000 characters; put longer specifications in a file and name it. `--model` and `--reasoning` choose the model and the reasoning level that check this goal, like `/compact --model`: a configured `models[].model` (or a part of one that matches exactly one model) and a level that model offers (`default` for its own). `-m` and `-r` are the short spellings. |
| `/goal` | Shows the goal: its status and why, continuations used, the last check's verdict, and what is still open. The web UI and the console open their goal menu instead. |
| `/goal pause` | Stops the automatic continuations and keeps the goal. A turn working on it finishes the step it is in. |
| `/goal resume [-m\|--model <id>] [-r\|--reasoning <level>]` | Makes a paused, blocked or limited goal active again with a fresh continuation and token budget, and starts a turn. The goal keeps the checker it was set with unless the options name another. |
| `/goal clear` | Removes the goal. `stop`, `off`, `cancel`, `reset` and `none` work as well. |

The commands work in the web composer, the console, ACP editors, `POST /v1/responses`, Telegram and Pachca. In a messenger, only the bot's admins can set, pause, resume or clear a goal; any permitted user can show it. A goal is worked on in agent mode. It is saved with the session, so it survives a restart, and a compaction keeps the progress toward it in the summary.

## How a goal is worked on

1. **Kickoff.** `/goal <objective>` stores the goal and starts a turn whose message tells the worker to start now, restates the objective, and gives the rules: work from evidence, prove claims with tool results, do not weaken checks to pass them, and say so plainly when the work needs you.
2. **Check.** When the turn ends, the supervisor gets the objective, the requirement list from its previous check, and an **evidence digest**. The digest lists every tool call since the goal was set with its arguments and the head and tail of its result, plus the files the turns changed and red flags such as edited tests, test data or CI files and added skip or suppression markers. The worker's final message comes last, labelled as claims. The supervisor answers with a requirement checklist and a verdict:
   - `met`: every requirement is proven by a tool result shown;
   - `not_met`: work is left, with the next steps;
   - `needs_user`: the worker asked a question only you can answer, lacks access, or found a conflict between the request and the tests;
   - `impossible`: the objective cannot be met in this workspace.
3. **Verify.** A `met` verdict goes to the verifier, a system subagent on the read-only tools (`read`, `glob`, `grep`, `print_tree`) that runs on the supervisor's model. It reads the files behind every requirement and answers in the same format. If it finds a gap, the goal goes on with the verifier's list. Its run appears in the session's Tasks panel as **Goal verification**. If the verifier itself fails to answer, the check's verdict stands and the reason says it was not verified. `supervisor.verify: false` skips this step.
4. **Continue.** For `not_met`, the next turn opens with the verdict's reason, what is still open, the checklist, the whole objective again, and the budget used so far. Restating the objective keeps a long run from drifting toward an easier task.
5. **Stop.** The goal becomes `complete` when the check (and the verifier) find it met, and `blocked` with the question when the work needs you. It becomes `limited` when `max_continuations` or `token_budget` is used up. Before that last stop the worker gets one wrap-up turn: no new work, only a summary of what is done, what is open and the next step.

A message you send while the supervisor works goes first; the goal is checked again after its turn. A `/goal` command sent while a turn runs waits for the turn's end, whatever the queue mode, and runs before any continuation. Pausing, clearing or replacing the goal while the supervisor is checking it takes effect at once: the check's verdict is dropped and no continuation starts. A turn that leaves a background command or subagent running with `notify_on_finish` defers its check: the wake that reports the work starts the next supervised turn. Work that wakes nobody - a system task such as the memory run, a task started with `notify_on_finish: false`, a process without a waker - is not waited for. Stop ends the run without changing the goal's status. Your next message is checked against the goal again, or use `/goal pause`.

Turns the supervisor starts are not messages you typed. Every surface shows each one as a one-line row, live and after a reload: **Goal set: ...**, **Goal continuation 2 of 10: tests still fail**, **Goal recovery 1 of 2: ...**, **Goal resumed: ...**. Telegram and Pachca post the same line as a message of its own, and the answer of the previous turn is sent before it.

## Statuses

| Status | Meaning | What moves it |
|---|---|---|
| `active` | Every turn end is checked; unfinished work is continued. | `/goal <objective>`, `/goal resume` |
| `paused` | Kept, nothing is checked or continued. The reason says who paused it: you, a usage limit, a turn that failed, a check that failed twice. | `/goal pause`, the menu, the system |
| `blocked` | The work needs you, or two turns in a row ended without a single tool call, or recovery from a stall or loop ran out. The reason holds the question. | the supervisor |
| `complete` | Checked as met, and confirmed by the verifier when it could answer; a goal the verifier could not confirm says "not verified" and why in its notice and its last check. | the supervisor |
| `limited` | The continuation or token budget is used up. | the supervisor |

## Watchdog

While a goal turn runs, the watchdog sees every update the turn sends.

- **Stall.** A turn that sends nothing for `stall_seconds` is cut and gets a recovery turn. The timer counts any update as activity: text, reasoning and tool calls. It waits while a permission or question prompt is open, while a tool runs (a tool has its own timeout), and while background work runs. A model row with `stream: false` sends nothing while it answers, so for it the timer waits at least the row's `timeout_ms` (plus 30 s), and is off when the row sets none.
- **Loop.** Each finished tool call is a fingerprint of its name, its canonical arguments and a hash of its result. When the last operations form a cycle of one, two or three that came round `loop_repeat` times, the turn is cut and a recovery turn asks the worker to summarize what it tried and change approach. A cycle of two or three needs at least two full rounds. Assistant text in between does not hide a loop. The same call with a different result is progress: a test run that fails differently, or a file read after it changed.
- **Stuck and failed turns.** A turn the watchdog cut, the agent's own loop guard stopped, or the agent refused to go on with is checked before anything else, because a worker that loops is often one that needs you: a `needs_user` or `impossible` verdict blocks the goal with the question, anything left gets a recovery turn that names it and asks the worker to change approach or ask you. A passing provider error, such as a 5xx, a dropped stream or a stream that went silent, gets a recovery turn without a check. A usage limit pauses the goal. Any other error pauses it with the error as the reason.

Recoveries count against `max_nudges` per run. When they run out, the goal is blocked with the last reason.

## Configuration

A goal needs no configuration: without a `supervisor` section, or with an empty one, the session's own model checks the work with every bound at its default.

```yaml
supervisor: {}
```

Every key, with its default:

```yaml
supervisor:
  enable: false              # check ordinary turns against the latest request too
  model: ""                  # the checking model; empty uses the session's model
  verify: true               # confirm a met verdict against the workspace
  max_continuations: 10      # automatic turns per goal, then a wrap-up turn
  token_budget: 0            # uncached input + output tokens per goal; 0 = no cap
  stall_seconds: 300         # 0 turns the stall timer off
  max_nudges: 2              # recovery turns after a stall, a loop or a failed turn
  loop_repeat: 3             # 0 turns loop detection off
```

The checking model is chosen per goal first: `/goal --model <id> --reasoning <level>` wins over `supervisor.model`, which wins over the session's model. `model` names a configured `models[].model`. Left empty, the session's own model checks its own work. Research on model judges finds that a model rates its own output higher, so a model of another family is the better choice even when it is smaller. The check runs without tools, through the same retries and proxy as the agent's own calls. An answer without valid JSON is asked for once more, and a check that fails twice pauses the goal. **Settings → Session goal** in the web UI edits the same keys.

`enable: true` puts ordinary turns under the same checks, with the latest request as the objective. Nothing is stored for them: a `blocked` or limited run ends with a notice. A goal is checked whatever `enable` says.

## The goal on every surface

- **Web UI**: a goal chip in the composer toolbar shows the status. It opens the goal menu with the objective, the status and its reason, the last check with the verifier's mark, the checklist, the model and the level that check it, the numbers, and **Pause**, **Resume**, **Edit** (keeps the checker) and **Clear**. A bare `/goal` opens the same menu. Typing `/goal --` completes `--model` with the configured models and `--reasoning` with the levels of the model `--model` names; the short `-m` and `-r` open the same lists.
- **Console**: the footer shows the goal's status and continuations; a bare `/goal` opens the goal modal with the same details (the checker on a **Checked by** line) and actions. Under `--remote` both follow the server.
- **HTTP**: `GET`, `PATCH` (`{"status":"paused"}` or `{"objective":"..."}`) and `DELETE /coddy/sessions/{id}/goal`, the `session_goal` and `goal_turn` events of a turn's stream, `event: session_goal` on `GET /coddy/events`, and `goal` in `GET /coddy/sessions/{id}/messages`; see [HTTP API](../reference/http-api.md).
- **ACP editors**: the `session_goal` and `goal_turn` session updates; an editor that renders neither still gets the command's answer as text.

## Compared with other agents

| | Coddy | Claude Code `/goal` | Codex `/goal` |
|---|---|---|---|
| Who decides the goal is met | a second model on an evidence digest, then a read-only verifier on the workspace | a small model on the transcript, no tools | the worker itself, through `update_goal`, after a completion audit |
| Continuation | separate goal turns, each restating the objective and what is left | inside the same turn | automatic turns while the status is active |
| Needs-you outcome | `needs_user` and `impossible` block the goal with the question | `impossible` clears it | `blocked` after three turns on the same blocker |
| Budgets | continuations, tokens, recovery turns; a wrap-up turn at the end | none, beyond a cap on turns without tool calls | tokens, with a wrap-up message |
| Stall and loop watchdog | yes | no | no-progress breakers only |
