# Plan: goal chains (issue #512)

Status: implemented on branch `feat/goal-chain` as v2 of this plan, after one
cross-review round of v1 (coddy codex/gpt-6.1-sol, cursor auto, coddy
devin/swe-2; the qwen reviewer did not answer). Changes from v1 are marked
`[rev]`.

## 1. Where the issue stands

Issue #512 asks for two things.

1. **Permission mode across restarts.** Already on `main`: commit `ab9ca469`
   (merged with pull request #502) writes `sessionPermissionMode` into
   `session.json` for every ordinary session and restores it in
   `loadSessionFromDisk` (`internal/session/manager.go`), with the scenario
   "A session keeps its permission mode across a restart of the server" in
   `features/session_settings.feature`. Nothing is left to do for this half
   beyond checking that a session a chain opens carries the mode too.
2. **Goal chains.** Not implemented. `/goal` knows `--model` and
   `--reasoning` only (`internal/session/goal_command.go`); a met goal ends in
   `goalRun.finish(GoalComplete, ...)` (`internal/session/supervisor.go`, two
   call sites: the ordinary check and the check of a stuck turn) and nothing
   follows it.

Overlap with other issues: #254 (closed) introduced `/goal` and the
supervisor; #362 (closed) made a session keep its model, reasoning and
permission mode; #443 (closed) is the question tool under bypass. No open issue
covers chains. #496 (server restarting itself) and #437 (session CRUD) touch
neighbouring code but not this behaviour.

## 2. Scope: the smallest change that delivers a chain

In scope:

- `/goal --then-file <path> <objective>`: when the goal becomes `complete`,
  the file's text becomes the goal of a new session in the same workspace,
  and the file is consumed so it never runs twice.
- Inheritance: workspace, model, reasoning level, permission mode, the goal's
  checker (`--model` / `--reasoning`), mode `agent`.
- The worker of a chained goal is told where the next task goes, so the chain
  does not depend on every objective restating the handoff.
- Links: the finished goal records `continuedIn`, the new one
  `continuedFrom`, both visible through the goal snapshot (every surface,
  `GET /coddy/sessions/{id}/goal`, `event: session_goal`), plus a notice row
  in both transcripts.
- Stops: only a `complete` goal whose met verdict the verifier confirmed (or
  with verification off) advances `[rev]`; a bound of 10 follow-up sessions per
  chain; a missing, empty, overlong or non-regular file ends the chain with a
  notice.
- The new session's first turn runs under `coddy serve` (set from the web UI,
  a messenger bot, a console or an editor attached with `--remote`). Elsewhere
  `--then-file` is refused when the goal is set: the local console shows one
  session, and a local `coddy acp` runs turns only for sessions its editor
  opened `[rev]`.
- A messenger chat gets the `continued in <id>` notice and nothing more: the
  new session is not bound to the chat. Telegram's `/resume <id>` follows it;
  Pachca has no session switch `[rev]`.
- Unattended chains need the permission mode `bypass`: the new session's
  prompts go to `defaultSender`, which refuses whatever the mode does not
  allow, and under `accept_edits` a shell command still asks `[rev]`.
- Sessions started by `/goal` get the objective as their derived title instead
  of the first line of the kickoff text (`[Goal set] Start working ...`): a
  chained session is created server-side and nothing else names it.

Out of scope (named in the pull request and the issue comment):

- `--then-prompt-file` (a template read again for every link). The worker
  writes the next `--then-file` when work remains, which is also what ends the
  chain cleanly; a template that never changes needs a separate stop rule.
- `supervisor.chain_check: marker` (rule-based mode without a checking model).
- A minimum interval between links: every link already costs a turn, a check
  and a verification.
- `[rev2]` The bound was first a constant, as a backstop like
  `maxWakesPerSession` (`internal/agent/background_notify.go`). After the
  implementation the operator asked for the key the issue proposed:
  `supervisor.max_chain_sessions` (default 10, `0` turns goal chains off and
  refuses `--then-file`), with its schema entry, the Settings field in
  **Goal checks**, both dictionaries and the recaptured Settings screenshot.
- Running a chain in a local console or a local `coddy acp`: the console shows
  one session and has no way to run another one's turn in the background.
- A clickable link to the other session in the web UI, and the links in the
  `GET /coddy/sessions` rows.

## 3. Design

### 3.1 Command and goal state (`internal/session`)

- `GoalCommand.ThenFile` parsed by `ParseGoalCommand` from `--then-file <path>`
  and `--then-file=<path>` (no short spelling; the value is one word, so a path
  with spaces is not supported). Like `--model`, a missing value is an error.
  With `clear`, `pause` or `resume` it is an error: "--then-file goes with a
  new goal". `GoalUsage` and the `goal` hint in `commands.go` name it.
- `GoalState` gains:
  - `ThenFile string json:"thenFile,omitempty"` - the path as typed;
  - `ChainIndex int json:"chainIndex,omitempty"` - the goal's place in its
    chain: 0 for the goal the operator set, N in the N-th session the chain
    opened;
  - `ContinuedFrom string json:"continuedFrom,omitempty"` - the session whose
    met goal opened this one;
  - `ContinuedIn string json:"continuedIn,omitempty"` - the session this
    goal's chain continued in.
  They persist with the goal (`session.json` `goal`), survive `/goal resume`,
  and are dropped by `/goal <objective>` (a new goal) and `/goal clear`.
- `acp.SessionGoal` gains the same four fields; `GoalSnapshot` fills them;
  `GoalStatusText` adds `Then: <path>` and `Continued from` / `Continued in`
  lines for the text surfaces.
- `applyGoalCommand` (`GoalCommandSet`): with `ThenFile` set and no chain
  starter installed (3.3), the command is refused: "--then-file needs coddy
  serve: ...".

### 3.2 The chain step (`internal/session/goal_chain.go`, new)

`goalRun.complete(goal, result)` replaces the two
`r.finish(GoalComplete, "", r.completeNotice(goal, result))` call sites: it
finishes the goal as today and then, for a persisted goal with `ThenFile`,
calls `m.continueGoalChain(st, goal)`. The implicit goal of
`supervisor.enable` never has a `ThenFile`.

`[rev]` `finish` returns whether it applied: the operator may pause, clear or
replace the goal through REST while the check runs, outside the turn lock.
`complete` chains only when `finish` applied, re-reads the goal (same id as the
run's, status `complete`) instead of the snapshot taken before the check, and
only when `LastCheck.Verified` is true or there was no verification to run
(`supervisor.verify: false`, or no verifier installed). A met verdict the
verifier could not confirm stops the chain with
`Goal chain stopped: the goal was not verified (<reason>)`, file untouched.

`continueGoalChain`, synchronous up to the first turn:

1. `goal.ChainIndex >= supervisor.max_chain_sessions` (10 by default; `0`
   turns chains off) `[rev2]`: notice
   `Goal chain stopped: it opened 10 sessions (supervisor.max_chain_sessions)`,
   nothing else.
2. No starter: notice `Goal chain stopped: this process cannot start a
   session on its own` (a goal set under `coddy serve`, reopened in a local
   console).
3. Resolve the path: absolute as is, a leading `~/` against the home folder,
   otherwise against the session's workspace (`st.GetCWD()`). Read it:
   missing - notice `Goal chain ended: <path> does not exist`; empty after
   trimming - `Goal chain ended: <path> is empty` (left in place); any other
   read error or an objective over 4000 characters (`NewGoal`) - notice
   `Goal chain stopped: <reason>`, file left in place. `[rev]` The file is
   opened, the open handle must be a regular file (a FIFO would block the
   turn lock forever), and at most `MaxGoalObjectiveRunes*4+1` bytes are read
   through `io.LimitReader`.
4. Consume: rename `<dir>/<stem><ext>` to
   `<dir>/<stem>.started-<UTC 20060102T150405Z><ext>`. A failed rename stops
   the chain with its error.
5. Create the session: `m.newSession(context.Background(),
   acp.SessionNewParams{CWD: st.GetCWD()}, "", "")`; on the new state set
   the selected model, the selected reasoning and the permission mode of the
   finished session (`SetSelectedModelID`, `SetSelectedReasoning`,
   `SetPermissionMode`; never `rememberPermissionMode`, which records an
   operator's choice), and the goal: `NewGoal(text)` with the finished goal's
   `Model`, `Reasoning`, `ThenFile`, `ChainIndex+1` and `ContinuedFrom`.
   A failure renames the file back and stops the chain with a notice.
6. Link: `ContinuedIn` on the finished goal (`UpdateGoal`, guarded by the goal
   id), notices `Goal chain: continued in session <id>` in the finished session
   and `Goal chain: continued from session <id> (link n of 10)` in the new one
   (`AppendUILogNotice`; the goal update carries the same notice, so a
   messenger chat posts it through `GoalEndNote`).
7. Start: build the kickoff prompt (`goalKickoffText`) and the kickoff marker,
   and call the starter on its own goroutine with
   `context.Background()`, `acp.SessionPromptParams{SessionID: new, Prompt:
   kickoff}` and `&PromptRunOpts{SkipUsagePublish: true, goalKickoff:
   marker}`. `[rev]` The starter's error is logged; a notice
   (`Goal chain: the first turn did not start: <err>; /goal resume starts it`)
   is written to the new session only for an admission failure (busy after the
   retries, a draining pool), never for `ErrSessionGone` /
   `ErrSessionDeleting` (a notice would recreate a deleted bundle) and never
   for the error of a turn that did run.

`runSupervisedTurn` takes `opts.goalKickoff` (unexported, like
`subagentTurn`) as the marker of the first turn when the session's goal is
active, so the chained session's first message is a goal kickoff row exactly
like one `/goal` starts. `[rev]` It takes it once and clears it:
`HandleSessionPromptWithSender` hands the same `opts` to the runs that answer
queued messages (`manager.go`, the drain loop), and a message a watcher typed
during the first turn must not be stored as a kickoff. A turn with
`goalKickoff` is, like a wake, not an operator turn (no settings commands, no
armed settings) and its prompt gets no mention resolution. A goal no longer
active when the turn is admitted drops the kickoff without running it.

### 3.3 Who runs the first turn

`session.GoalChainStarter func(ctx, acp.SessionPromptParams, *PromptRunOpts)
error`, installed with `Manager.SetGoalChainStarter`.

`coddy serve` installs it in `internal/serve/runtime.go` next to
`SetGoalVerifier`. `[rev]` `Runtime.runGoalChainTurn`, in the order of
`external/httpserver/background_http.go`: refuse while the task pool drains
(shutdown); `AcquireComposerTurnLock` (a relay registered before the lock
could evict the relay of a turn just admitted); `MirrorTurn(id,
&defaultSender{...})` so a browser follows it live; then
`HandleSessionPromptWithSender` with `SkipTurnLock`. `ErrSessionTurnBusy` is
retried with a short backoff for a bounded time, while the new session has no
user turn yet and its goal is still the chain's (same id, active); once a
person's own prompt ran there, it worked on the goal under the supervisor and
the kickoff is dropped.
Permission prompts get `defaultSender`'s answer: allowed when the session's
permission mode allows the call (the inherited `bypass`), refused otherwise,
as for every turn nobody is watching. The docs say so.

The local console and `coddy acp` install nothing.

### 3.4 What the worker is told

`goalKickoffText`, `goalResumeText`, `goalContinuationText` and
`goalRecoveryText` `[rev]` add one block for a goal with `ThenFile` (a wrap-up
turn ends the goal `limited`, so it never chains):

> This goal is one link of a chain. When the supervisor finds it met, a new
> session starts with the text of `<path>` (relative to the workspace) as its
> goal, and the file is renamed so it runs once. If work remains after this
> goal, write the next task there before you finish: self-contained, as the
> objective of a session that has none of this conversation. If nothing
> remains, do not create the file.

These are user-role messages, so the system prompt and its cache are not
touched.

### 3.5 Title

`titleFromMessages` (`internal/session/filesystem.go`) takes
`msg.GoalTurn.Objective` for a user message that carries a kickoff marker,
instead of the kickoff text. Every session started by `/goal` benefits; a
chained session is the one that needs it, since no client names it.

### 3.6 Web UI

`goalSetPrompt` (`external/ui/src/ui/chat/goal.ts`), which the goal menu's
Edit sends, carries `--then-file <path>` when the goal has one, so editing an
objective keeps the chain. `[rev]` `parseSessionGoal` reads the four fields
(it rebuilds the goal field by field), and `GoalPopover.tsx` passes
`goal.thenFile` to `goalSetPrompt`. No visual change.

## 4. Tests

Red first, each run alone before the code.

- `features/goal_chain.feature` (godog in `internal/session`, real manager and
  turn lock, scripted runner and judge, starter that runs
  `HandleSessionPromptWithSender`):
  - "A met goal hands the follow-up file to a new session": the new session is
    in the same workspace with the file's text as its active goal, the same
    follow-up path and `chainIndex` 1; its first message is a goal kickoff; the
    file is renamed `next-task.started-*.md`; the finished goal says
    `continuedIn`, the new one `continuedFrom`, and both transcripts carry the
    notice;
  - "The chain ends when no follow-up file is left": the second session's goal
    is met without a new file, no third session opens, the notice names the
    missing file.
- Unit tests (`internal/session/goal_chain_test.go`):
  - parsing: both spellings, a missing value, `--then-file` with
    `pause` / `clear` / `resume`;
  - `/goal --then-file` refused without a starter;
  - inheritance: workspace, model, reasoning, permission mode, checker, mode;
  - no advance on `needs_user`, a paused goal (check failure), `limited`, and
    in plan mode (the goal is never checked there);
  - `[rev]` no advance when the verifier failed; advance with verification
    off; no advance when the goal was cleared while the check ran;
  - `[rev]` `/goal resume` keeps `ThenFile` and a resumed chained goal chains;
  - `[rev]` a message queued during the chained kickoff is stored without a
    goal marker;
  - `[rev]` a FIFO and an oversized file stop the chain, left in place;
  - the bound: `ChainIndex` 10 stops with the notice, the file untouched;
  - an empty file, an overlong file: no session, file untouched;
  - session creation fails (the workspace removed, an absolute follow-up file
    elsewhere): the file is renamed back;
  - a starter error leaves the notice in the new session;
  - store-backed: the chained session's `session.json` carries
    `sessionPermissionMode`, and a new manager over the same store restores it;
  - `GoalStatusText` and `GoalSnapshot` carry the new fields; the title of a
    `/goal` session is its objective.
- `internal/serve`: the runtime installs the starter; a turn through it takes
  the real lock, is mirrored, and is retried while a prompt holds the lock
  `[rev]`.
- vitest: a server-shaped goal payload through `parseSessionGoal` and the
  popover's Edit yields `--then-file` `[rev]`.

Then the narrow packages (`internal/session`, `internal/serve`,
`external/httpserver -tags http`, `external/ui` vitest for `goal.ts`),
`make lint`, `make docs-check`; the full matrix is CI's.

## 5. Documentation

- `docs/features/session-supervisor.md` and its translation: a "Goal chains"
  section (the option, what is inherited, how the file is consumed, the stops,
  the bound, unattended permission prompts, `coddy serve` only), the commands
  table, the surfaces list.
- `docs/reference/slash-commands.md` (+ru): `/goal --then-file`.
- `docs/reference/http-api.md` (+ru) and the goal description in
  `external/httpserver/openapi.go`: the four fields.
- `docs/reference/acp-protocol.md` (+ru) if it lists the `session_goal` fields.
- `internal/skills/bundled/configure-coddy/SKILL.md` names `/goal` usage; add
  `--then-file`.
- `make docs`, `make docs-stamp` for the Russian pages, `make site-docs-check`.

## 6. Risks

- The follow-up file is read whoever wrote it: a checkout that already holds
  the named file starts that text as the next goal, under the inherited
  permission mode. The operator names the path; the docs say the file is read
  when the goal is met and must be one the operator controls.
- The new session runs with nobody answering prompts: under `ask` or
  `accept_edits` the gated calls are refused and the goal ends blocked.
  Documented; unattended chains use `bypass`. Routing the first turn through
  the HTTP server's wake path, where a prompt waits for a browser, is left for
  later `[rev]`.
- The first turn starts on a goroutine with a background context; a shutdown in
  between leaves the new session with an active goal and no turn. The notice
  says `/goal resume` starts it.
- The file is consumed before the session is created, so a crash in between
  runs the task at most once; its text stays in `<stem>.started-<ts><ext>`
  `[rev]`.
- `PATCH /coddy/sessions/{id}/goal` with an objective and `/goal <objective>`
  replace the goal and drop the follow-up file and the chain position, as
  every other field of the old goal; the docs say so `[rev]`.
