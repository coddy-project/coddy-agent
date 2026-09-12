# Tools

The built-in tools the model can call, their arguments, permissions and the modes that expose them. Names, argument names and permission flags are taken from the constructors under `internal/tools` and the optional packages; `internal/tools.NewRegistryFor` builds the registry from them, and `internal/agent.ToolSetForMode` in `internal/agent/toolsets.go` decides which definitions each mode sends to the model. Agent mode is unrestricted; plan and ask carry fixed allowlists, and ask re-checks its list at execution time so a call replayed from history cannot cross it. The reasoning behind the modes is in [Operating modes](../features/modes.md#the-three-modes); how to add a tool is in [Custom tools](../contributing/custom-tools.md).

## Permissions

The Permission column uses the classes the gate in `internal/agent/react.go` applies under `tools.permission_mode` (`ask` by default, `accept_edits`, `bypass`; see [Security and trust](../operate/security.md#permission-modes-and-prompts)):

- `none`: never prompts;
- `write`: the file-write class - prompts under `ask` unless the path was granted in this session, auto-approved under `accept_edits` and `bypass`;
- `command`: `run_command` - prompts unless the command is covered by `tools.command_allowlist` or a session grant (including the program-wide grants a prompt can store, see [Background tasks](../features/background-tasks.md#permissions)); `bypass` skips the prompt;
- `config`: `config_commit` and `config_rollback` - prompts under `ask` and `accept_edits`, since a commit can start MCP processes and change the permission policy itself; only `bypass` skips it;
- `always`: tools that set `RequiresPermission` outside those classes - the permission mode does not change them, and only a PreToolUse hook answering `allow` skips the prompt.

## Files

| Tool | Purpose | Arguments (short) | Permission | Modes |
|---|---|---|---|---|
| `read` | Read a file as text with an optional 1-based line range, or list a directory | `path`, `offset`, `limit`, `recursive`, `show_hidden`, `keep` | none | agent, plan, ask |
| `keep_result` | Mark a page already read or a grep result as useful so result eviction keeps it ([Context compaction](../features/compaction.md#result-eviction)) | `path`, `offset`, `limit`, `pattern` | none | agent, plan, ask |
| `glob` | Find files by glob pattern, newest first, at most 100 paths | `pattern`, `path` | none | agent, plan, ask |
| `grep` | Regular-expression search over file contents | `pattern`, `path`, `glob`, `case_sensitive`, `max_results`, `keep` | none | agent, plan, ask |
| `print_tree` | Print a directory tree like `tree` | `path`, `depth` | none | agent, plan, ask |
| `edit` | Replace an exact text range; line endings are preserved | `path`, `oldString`, `newString`, `replaceAll` | write | agent |
| `write` | Create or overwrite a file; parent directories are created | `path`, `content` | write | agent |
| `apply_patch` | Apply a unified diff or a Codex V4A patch | `path`, `patch` | write | agent |
| `mkdir` | Create a directory; `parents` works like `mkdir -p` | `path`, `parents` | write | agent |
| `rmdir` | Remove an empty directory | `path` | write | agent |
| `touch` | Create an empty file or refresh its modification time | `path`, `create_parents` | write | agent |
| `rm` | Remove a file, or a whole tree with `recursive` | `path`, `recursive` | write | agent |
| `mv` | Move or rename; destination parents are created | `src`, `dst` | write | agent |

## Shell and background tasks

The `background_*` tools are registered only while `tools.background` is enabled (the default). The guide is [Background tasks](../features/background-tasks.md#model-facing-surface).

| Tool | Purpose | Arguments (short) | Permission | Modes |
|---|---|---|---|---|
| `run_command` | Run a shell command in the workspace through the host shell; `background: true` returns a task id at once, and a foreground command that outlives its timeout is handed to the pool instead of being killed | `command`, `permission_rationale`, `timeout_seconds`, `background`, `notify_on_finish`, `expected_seconds` | command | agent, plan |
| `background_list` | List the session's background tasks with status, elapsed time and estimate | none | none | agent, plan |
| `background_output` | Return the captured stdout and stderr of a task | `task_id`, `tail_lines` | none | agent, plan |
| `background_wait` | Wait for a task to finish, bounded by a default and a maximum | `task_id`, `timeout_seconds` | none | agent, plan |
| `background_stop` | Terminate a task and everything it spawned | `task_id` | none | agent, plan |
| `background_reap` | Find and kill processes of this session that outlived the run which started them | none | always | agent |
| `ssh_run_command` | Run a command on a remote host over SSH, using the agent socket and then key files ([Configuration](../getting-started/configuration.md#ssh-remote-execution)) | `host`, `command`, `port`, `timeout_seconds`, `permission_rationale` | always | agent |

## Web

| Tool | Purpose | Arguments (short) | Permission | Modes |
|---|---|---|---|---|
| `websearch` | Search DuckDuckGo, Google and Bing at once and merge the results | `query`, `page`, `max_results` | none | agent, plan, ask |
| `webfetch` | Download a public page and return its main text as Markdown; private networks and localhost are refused | `url`, `timeout_seconds`, `max_chars` | none | agent, plan, ask |

## Interaction, skills and subagents

| Tool | Purpose | Arguments (short) | Permission | Modes |
|---|---|---|---|---|
| `question` | Ask the user one or more multiple-choice questions and wait for the answers | `questions[]` of `header`, `question`, `options[]` (`label`, `description`), `multiple`, `custom` | none (the turn waits for the user) | agent, plan, ask |
| `load_skill` | Load the full instructions of a catalogued skill by name; registered only while `skills.auto_discovery` is on ([Skills](../features/skills.md)) | `name` | none | agent, plan, ask |
| `spawn_agent` | Delegate a self-contained task to a subagent, waiting for its report or running it as a background task ([Subagents](../features/subagents.md#the-spawn_agent-tool)) | `agent`, `prompt`, `description`, `background`, `expected_seconds`, `timeout_seconds`, `notify_on_finish` | none itself; the child's own calls prompt through the parent | agent, plan (the child of a plan-mode parent stays in plan mode); hidden in ask, at `subagents.max_depth` and where no runtime exists, such as a scheduled run |

## Todo checklist

The session's plan document as a checklist, persisted in the bundle and shown as the ACP `plan` update ([Sessions](../features/sessions.md#the-todo-checklist)). Agent mode only.

| Tool | Purpose | Arguments (short) | Permission | Modes |
|---|---|---|---|---|
| `coddy_todo_plan_read` | Read the checklist without changing it | none | none | agent |
| `coddy_todo_plan_replace` | Replace the whole checklist from Markdown | `markdown` | none | agent |
| `coddy_todo_plan_archive` | Finalise the checklist: incomplete items are marked completed and the plan is archived | none | none | agent |
| `coddy_todo_item_add` | Add one item, at the end or after an index | `content`, `status`, `after_index` | none | agent |
| `coddy_todo_item_remove` | Remove one item by zero-based index | `index` | none | agent |
| `coddy_todo_item_update` | Change the content or the status of one item | `index`, `content`, `status` | none | agent |
| `coddy_todo_item_move` | Move one row | `from_index`, `to_index` | none | agent |

## Design plans

Plan documents live at `plans/<slug>.plan.md` inside the session bundle ([Operating modes](../features/modes.md#plan-mode-and-the-plan-document), [ACP protocol](../reference/acp-protocol.md#design-plans-plan-mode)).

| Tool | Purpose | Arguments (short) | Permission | Modes |
|---|---|---|---|---|
| `plan_write` | Write or replace a plan document | `slug`, `content` | none | agent, plan |
| `plan_list` | List the plan documents of the session | none | none | agent, plan |
| `plan_read` | Read one plan document | `slug` | none | agent, plan |
| `plan_exit` | Switch the session from plan mode to agent mode | none | none | agent (not in the plan allowlist) |

## Self-configuration

Staged edits to the live `config.yaml` ([config.yaml reference](../reference/config.md#agent-self-configuration)). The editing family is offered only when the surface wired a configuration reloader; without one, `config_get` is the only member left.

| Tool | Purpose | Arguments (short) | Permission | Modes |
|---|---|---|---|---|
| `config_get` | Read a redacted value by dotted uci-style path; `.` is the whole file | `path` | none | agent, plan |
| `config_changes` | List the staged commands a commit would apply | none | none | agent, plan |
| `config_set` | Stage uci-like commands against the active file: `set <path>=<value>`, `add_list <path>=<value>`, `del_list <path>=<value>`, `delete <path>` | `commands[]` | none | agent |
| `config_revert` | Discard staged commands, all of them or those under a path | `path` | none | agent |
| `config_commit` | Validate the batch, snapshot the previous file and apply; hot-reloads the running process | none | config | agent |
| `config_rollback` | Restore the snapshot written by the last commit | none | config | agent |

## Scheduler

Compiled in with the `scheduler` tag and registered only while the scheduler is enabled (`internal/tools/scheduler_hook.go`, `external/scheduler/tools/register.go`). `job_id` is the file basename under `scheduler.dir`; the job fields are those of the frontmatter ([Scheduler](../operate/scheduler.md#tools-when-scheduler-is-enabled)). Agent mode only.

| Tool | Purpose | Arguments (short) | Permission | Modes |
|---|---|---|---|---|
| `coddy_scheduler_jobs_list` | List every job file | `include_body` | none | agent |
| `coddy_scheduler_job_get` | Load one job | `job_id` | none | agent |
| `coddy_scheduler_job_runs` | List the persisted runs of a job with their session ids | `job_id`, `limit` | none | agent |
| `coddy_scheduler_job_create` | Create a job file | `job_id`, `description`, `schedule`, `paused`, `cwd`, `model`, `mode`, `body` | always | agent |
| `coddy_scheduler_job_replace` | Replace every field of a job | `job_id`, `description`, `schedule`, `paused`, `cwd`, `model`, `mode`, `body` | always | agent |
| `coddy_scheduler_job_patch` | Change only the given fields, optionally renaming the job | `job_id`, `new_job_id`, `description`, `schedule`, `paused`, `cwd`, `model`, `mode`, `body` | always | agent |
| `coddy_scheduler_job_pause`, `coddy_scheduler_job_resume` | Set or clear `paused` | `job_id` | always | agent |
| `coddy_scheduler_job_delete` | Delete a job and its `.state` and `.lock` sidecars when idle | `job_id` | always | agent |
| `coddy_scheduler_job_run` | Trigger one asynchronous run now | `job_id` | always | agent |
| `coddy_scheduler_job_cancel` | Cancel the active run of a job | `job_id` | always | agent |

## Memory copilot

With the `memory` tag, the long-term memory copilot runs its own small model loop before and after a turn and calls these tools itself; they are not in the registry and the main model never sees them (`external/memory/copilot.go`, `external/memory/tools`). The recall phase gets the first three, the persist phase all six. Paths are `scope:relative/path.md` ([Long-term memory](../features/memory.md#the-tools)).

| Tool | Purpose | Arguments (short) | Permission | Modes |
|---|---|---|---|---|
| `coddy_memory_search` | Rank the notes under the chosen roots against a query | `query`, `scope` | none | copilot only |
| `coddy_memory_list` | List directories and notes one level under a path | `path` | none | copilot only |
| `coddy_memory_read` | Read one note | `path` | none | copilot only |
| `coddy_memory_mkdir` | Create nested folders under a scope | `path` | none | copilot only |
| `coddy_memory_save` | Write or overwrite a note | `title`, `body`, `scope`, `relative_path` | none | copilot only |
| `coddy_memory_delete` | Delete a note or a folder with everything under it, never a root | `path` | none | copilot only |

## MCP tools

Every enabled tool of every connected MCP server joins the same function-calling list under the name `server__tool`, with the server's own input schema and a description prefixed with `[server]` (`internal/mcp/client.go`; [MCP servers](../features/mcp.md#tool-namespacing)). Because `__` is the separator, a server name may not contain it. The definitions are appended in agent and plan mode and never in ask mode, where a call to such a name is refused at execution time. A call whose name contains `__` bypasses the registry: the agent routes it to the owning server and applies the `default` output limit to the result. There is no built-in permission prompt for MCP calls; whether a server may start is the workspace trust decision, and the per-server and per-tool disable switches bound what a running server may do ([MCP servers](../features/mcp.md#permission-model)).

## What runs before a tool call

Operator hooks see every call, MCP tools included, before the permission gate and whatever the permission mode ([Hooks](../features/hooks.md#what-a-hook-answers)). A `PreToolUse` hook answering `permissionDecision: deny` stops the call; it is never executed and the model reads `blocked by hook: <reason>` as the tool result. `allow` skips the prompt a tool would otherwise raise, `ask` forces one even under `accept_edits` or `bypass`, and `updatedInput` replaces the argument object before the call runs. Several matching hooks all run, and the most restrictive decision wins.

A subagent's tool set is narrowed from the parent's ([Subagents](../features/subagents.md#how-capabilities-narrow)): a definition's `tools` and `disallowed_tools` only remove names, and `question`, the five config editing tools and `plan_exit` are excluded from every child, so a child can neither ask the user nor rewrite the configuration nor leave plan mode. Results are capped by `tools.output_limits` before they reach the model, for built-in and MCP tools alike.
