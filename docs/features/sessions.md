# Sessions

A session is one conversation: the transcript, the working directory it runs in, the mode, model and permission mode it uses, its todo checklist, the files uploaded into it and the tool activity it produced. Every surface - the console, the web UI, an ACP editor, the Telegram gateway, a scheduler run, a subagent - works on the same kind of session, stored the same way, so a conversation started in one place can be continued in another. This page covers the bundle on disk, resuming, branches from an edited message, the todo checklist, child sessions and the `coddy sessions` command. Writing a transcript to a file is [Session export](session-export.md).

## The bundle on disk

Every session is a directory under the sessions root, `$CODDY_HOME/sessions/<id>/` (`~/.coddy/sessions/` by default). The root moves with `sessions.dir` in `config.yaml` or with `--sessions-dir` on `coddy serve`, `coddy acp`, the console and the `coddy sessions` verbs ([config.yaml reference](../reference/config.md#sessions)); the process creates it at start and fails to start when it cannot. Ids are `sess_` followed by 24 hex characters; child sessions of subagents start with `sub_`, scheduler runs with `sched_`. An id doubles as the folder name, so only letters, digits, `_` and `-` are accepted.

| Path | What it holds |
|---|---|
| `session.json` | id, `cwd`, `mode`, the session's model and reasoning overrides, the permission-mode override, the title (derived, or pinned by the user), `updatedAt`, the context handed over by `SessionStart` hooks, the run metadata of a scheduler or subagent bundle, and the activity counters behind the unread dot |
| `messages.json` | the transcript the model sees: `user`, `assistant` and `tool` rows, compaction summaries included; hooks receive its path as `transcript_path` |
| `assets/` | files uploaded through the composer, saved read-only, with PNG previews under `thumbnails/` |
| `todos/active.md` | the current todo checklist; `todos/archive/` keeps replaced and archived lists |
| `plans/<slug>.plan.md` | design plans written in plan mode |
| `tool_calls/<id>/` | `args.json`, `result.md` and `meta.json` of every tool call, so a full result can be fetched after the stream showed a preview |
| `stats.json` | token totals of the completed model calls |
| `branches.json` | the branch points of an edited conversation |
| `diffs/turn_<n>.json` | the workspace files each turn changed, replayed backwards when a branch is created |
| `background/<task_id>/` | the record and output log of every background task and subagent run |
| `memory_trace.json` | what the memory copilot did on each turn ([Long-term memory](memory.md)) |
| `ui_log.json`, `permission_grants.json`, `pending_permission.json` | notice rows shown in the transcript, the commands and write targets approved with "allow always", a permission prompt waiting for its answer over HTTP |

Compaction and result eviction never rewrite a bundle: both are projections built when a request goes to the model, and `messages.json` keeps every original row ([Context compaction](compaction.md)).

## What a session remembers

The mode (`agent`, `plan` or `ask`), the model, the reasoning level and the permission mode are session-scoped overrides persisted in `session.json`: the ACP `session/set_mode` and `session/set_config_option` methods, the composer's Mode, Model and Reasoning selectors (`PATCH /coddy/sessions/{id}` with `selectedModelId` or `selectedReasoning`) and the console's `/mode` and `ctrl+l` all write there, and a session-level `permission_mode` outranks `tools.permission_mode` from the configuration. The working directory is recorded too: `coddy -c`, the console picker and `coddy sessions list --cwd` filter on it. The web UI picks the folder, the git branch and an optional worktree before the first message and locks them once the transcript has messages ([Web UI](../surfaces/web-ui.md#per-session-workspace-folder--branch--worktree-chips)). The title is derived from the first prompt until you pin one, inline in the chat header or with `PATCH /coddy/sessions/{id}` and a `title`.

## One store, every surface

`coddy serve` runs one session manager over one store for the HTTP API, the web UI, the Telegram gateway and the scheduler, so a chat with the bot is an ordinary session that the History drawer lists and opens while it is happening, and a turn started on one surface is mirrored into a browser watching the same session ([coddy serve](../operate/serve.md), [Telegram gateway](../surfaces/gateway.md)). The console and `coddy acp` open the same store on their own; with `--remote` they work on the server's sessions instead of a local store. A swarm relay aggregates the session lists of its nodes ([Swarm](../operate/swarm.md)).

A session runs one turn at a time. The turn lock is a file lock on the bundle, so a second process - `coddy serve` while `coddy acp` holds the turn - is refused rather than interleaved (`409` over HTTP), and a cancel also writes a marker into the bundle that the process holding the turn picks up between its polls. Different sessions stream in parallel, each behind its own lock ([Web UI](../surfaces/web-ui.md#parallel-sessions-and-generation-cancel)).

## Resuming

| Surface | How |
|---|---|
| Console | `coddy -c` (`--continue`) reopens the most recent session recorded for the current folder and fails when there is none; `--session-id <id>` reopens that session or creates one under that id; `--resume` and `/resume` open a picker of the folder's sessions by title and last update; `-c -p "..."` continues in print mode. Quitting prints the id and the command that resumes it. Under `--remote` all of them work on the server's list, without the folder filter ([Console](../surfaces/console.md)). |
| Web UI | The **History** drawer (`#/history`) lists sessions newest first, with a search over the title and the first prompt, a spinner on a session still generating, a dot on one that finished while you were elsewhere and a question mark on one waiting for a permission; a row opens `#/s/<id>`, and that URL alone brings the session back ([Web UI](../surfaces/web-ui.md#session-list)). |
| ACP editors | `session/load` restores `session.json` and `messages.json`, replays the turns and the tool call summaries, sends a `plan` update when `todos/active.md` exists, then the command catalog; `session/list` lists the bundles with an optional `cwd` filter, and `loadSession` is advertised whenever a store is configured. `coddy acp --session-id <id>` makes the next `session/new` reopen that bundle, or create one under that name ([ACP protocol](../reference/acp-protocol.md#sessionload)). |
| HTTP | `GET /coddy/sessions` (`limit`, `cursor`, `q`, `include_scheduler`, `include_subagents`, `include_activity`), `GET /coddy/sessions/{id}/messages`, and `X-Coddy-Session-ID` on `POST /v1/responses` to continue a session ([HTTP API](../reference/http-api.md)). |

![History drawer](../assets/screenshot-fullhd-history.png)
*The History drawer over the start screen.*

Listings sort by `updatedAt`, newest first; the stamp moves when something is persisted - a turn, a pinned title - and not when a bundle is merely loaded to serve a read. Reopening a session runs the `SessionStart` hooks again with `source: resume` ([Hooks](hooks.md#events)).

## Branches from an edited message

Editing a message you already sent does not overwrite the answer it produced. The pencil on a user bubble in the web UI loads that message back into the composer; sending it calls `POST /coddy/sessions/{id}/branches` with the 0-based index of the user message, and the server creates a new session holding every message before that point, reverses the workspace changes recorded by the turns after it so the files match the state the branch starts from, and sends the edited text to the new session. Both threads stay readable, and both are ordinary bundles that every surface can open.

The bookkeeping lives in `branches.json`. The source records a branch point at that message index with its threads in order - the source itself first, each fork after it, each with a preview of its message - and the new session records its origin: the parent, the index and its own position. Forking a branch at the same point where it diverged from its parent adds a sibling to the parent's branch point; forking it anywhere else opens a branch point of its own. Under the branch point the transcript shows a `‹ 2/2 ›` navigator whose arrows switch between the threads. Opening a session by id follows the most recently updated thread at every branch point, so a link to the root lands where you last worked; a thread picked in the navigator opens as picked. Deleting a thread retracts it from the parent's file, and a branch point left with a single thread disappears, so the navigator never points at a bundle that is gone.

![Branch navigator](../assets/screenshot-fullhd-branches.png)
*The `‹ 2/2 ›` navigator under an edited user message.*

`GET /coddy/sessions/{id}/branches` returns the branch points a session sees, its own and the sibling view inherited from its parent ([Web UI](../surfaces/web-ui.md#message-editing-and-conversation-branches)). Child sessions of subagents cannot be forked.

## The todo checklist

The `coddy_todo_*` tools (`plan_read`, `plan_replace`, `plan_archive`, `item_add`, `item_update`, `item_remove`, `item_move`, with the statuses `pending`, `in_progress`, `completed`, `failed` and `cancelled`) keep the session's checklist. Every change is mirrored to `todos/active.md` and published to the client as a `plan` update, and while the list is non-empty the system prompt carries a `### Current todo checklist` block that is refreshed before every model call of the turn, so an item ticked mid-turn is visible at once. Ask mode offers no todo tools.

Two rules govern replacement. `coddy_todo_plan_replace` is refused while any item is not `completed` - finish the rows or archive first - and when every row is completed the previous `active.md` moves to `todos/archive/todo-<nanos>.md` before the new list is written. `coddy_todo_plan_archive` marks the open rows completed, writes `todos/archive/plan_<unix_seconds>.md` and clears the active list. Over HTTP the same list is `GET` and `PUT /coddy/sessions/{id}/plan`, and `POST /coddy/sessions/{id}/plan/archive`.

Design plans written in plan mode are separate files, `plans/<slug>.plan.md`, and never fill the checklist by themselves ([ACP protocol](../reference/acp-protocol.md#design-plans-plan-mode)).

## Child sessions and run bundles

A subagent run is a child session with a `sub_` id: a real bundle whose `session.json` carries `subagentRun`, `parentSessionId`, `subagentName`, `subagentTaskId` and `subagentDepth`. Children stay out of every default listing - History, `GET /coddy/sessions`, `coddy sessions list`, `coddy -c`, ACP `session/list` - and are read-only transcripts: a prompt, a fork or a permission answer against one is refused with the parent named ([Subagents](subagents.md#child-sessions)). Scheduler runs are bundles too, `sched_` ids with `schedulerRun` set, hidden from the composer list and pruned per job by `scheduler.retain_sessions` ([Scheduler](../operate/scheduler.md)).

## Deleting a session

The trash icon on a History row, after one confirmation, and `DELETE /coddy/sessions/{id}` remove the session tree: the session plus every child it spawned, their background tasks stopped first, the bundles removed deepest first. A deleted branch is retracted from the `branches.json` of the session it forked from. A running turn is cancelled and awaited before anything is removed. There is no delete verb on the command line; deleting the directory by hand is equivalent for a session no process holds.

## The sessions CLI

```bash
coddy sessions list [--sessions-dir <path>] [--cwd <filter>]
coddy sessions export <session-id> [--format md|html|json|jsonl] [--out <path>] [--no-tools] [--no-thinking] [--sessions-dir <path>]
```

`list` prints one tab-separated row per stored session - `SESSION_ID`, `UPDATED_AT`, `CWD`, `TITLE` - newest first, then `(total N)`; scheduler runs and child sessions are left out, and `--cwd` keeps only the sessions saved with that absolute directory. `export` writes a stored session's transcript to a file and accepts a unique id prefix in place of the full id; the formats, the path rules and the trimming options are in [Session export](session-export.md).

## Testing

- Executable specs in `features/`: `acp_session_integration.feature` (a reopened bundle replays its transcript after the `session/new` response; harness `internal/session/bdd_acp_session_test.go`), `session_branch_delete.feature` (a deleted branch is retracted from its parent; `external/httpserver/bdd_branch_delete_test.go`), `session_export.feature` and `session_export_cli.feature` ([Session export](session-export.md)).
- Unit tests: `internal/session/filesystem_test.go` (the bundle layout and listing), `internal/session/branches_test.go` (fork slicing and `branches.json` bookkeeping), `external/cli/continue_test.go` (`-c` resolution), and in the SPA `external/ui/src/ui/chat/BranchNavigator.test.tsx`, `branchInject.test.ts` and `resolveLatestLeaf.test.ts`.
