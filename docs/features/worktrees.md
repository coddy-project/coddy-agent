# Git worktrees

Coddy can give a session its own feature branch and working copy. The session's
working directory moves into the worktree, so its next `run_command`, file tool,
rule lookup and project-scoped MCP connection use that directory without a
manual `cd`.

In an agent conversation, ask Coddy to work on a feature branch. The
`worktree_open` tool takes `{"branch":"feature/login"}` and moves the session
into the worktree that holds the branch, or opens one for it: it fetches
`origin`, reads the default branch from `origin/HEAD`, and creates a new branch
from the fresh `origin/<base>` commit. A branch that exists only on `origin` is
checked out from `origin/<branch>` with tracking; a branch that already exists
locally is reused at its own tip, and a branch already checked out in a worktree
reuses that worktree. The tool refuses the default branch itself and a local
branch that tracks `origin/<base>`.

A new branch can also start where the session stands: with `"base":"head"` the
tool starts it at the commit the session's checkout is on, so the next branch is
stacked on the work in progress, even when the session works in a worktree
itself. A branch that already exists keeps its own tip whatever `base` says. In
both cases the new worktree goes under the main checkout's `.coddy/worktrees/`,
never inside the worktree it was opened from. With `{"path":"..."}` instead of a
branch, `worktree_open` enters a worktree that already exists, its folder
absolute or relative to the main checkout. It creates and fetches nothing, so it
reaches a worktree on a detached HEAD or one another tool opened; the main
checkout itself is `worktree_exit`'s.

`worktree_list` shows every worktree of the repository, the main checkout first,
and where each stands: its branch and last commit, uncommitted and unpushed
changes, commits ahead of and behind origin's default branch, whether it is
locked or its folder is gone, which one the session works in and which other
sessions work in each. It moves nothing and is offered in Plan and Ask modes
too. To look closer at one worktree without moving there, the agent runs `git`
in it with `run_command` and its `cwd`.

When the work in a worktree is done, `worktree_exit` moves the session back to
the main checkout, and its next tool call runs there. `{"action":"keep"}`, the
default, leaves the worktree and its branch for later; `worktree_open` with the
same branch goes back to it. `{"action":"remove"}` removes the worktree on the
way. It refuses a worktree with uncommitted changes unless `discard_changes` is
`true`, and, whatever `discard_changes` says, a worktree on commits that no
branch, remote branch or tag holds (a detached HEAD with work of its own), one
with initialised submodules (their own commits live in the worktree's git
directory), one with a git repository of its own among its untracked folders (a
snapshot would keep only a pointer to its commit), a locked one, one another
live session of this Coddy process or a background task of any session still
works in (a chat of another Coddy process in the same worktree is not seen), and
one Coddy did not open (outside `.coddy/worktrees/`). Discarded changes are not
lost: before the worktree goes, Coddy saves them the way `git stash` does, the
index and the files, under `refs/coddy/worktree-snapshots/` and names the ref in
the answer, and `git stash apply --index <ref>` on the branch brings them back
as they were, files added with `--force` past `.gitignore` included.
Ignored files, such as build output, go with the worktree. The branch goes with
the worktree only when git counts it as merged (`git branch -d`): a branch with
commits of its own stays, and the answer says so. A refusal leaves the session
where it was. A session whose worktree folder is gone, removed by hand or by
another chat, comes back to the main checkout with the same call, and git
forgets the worktree. Like `worktree_open`, the tool asks for approval under the
current permission mode, runs in agent mode only and is never offered to a
subagent.

The operator does the same with the `/worktree` command in the console, the web
UI and an ACP editor, without a turn of the model. `/worktree <branch>` moves the
chat into the worktree of the branch, opening one when there is none
(`--from-head` starts a new branch at the current commit); `/worktree <path>`
moves it into an existing worktree (a path starts with `/`, `./`, `../` or `~`
and is read from the chat's folder); `/worktree exit` brings the chat back to
the main checkout (`--remove` removes the worktree too, and `--force` with it
discards its uncommitted changes, kept as a snapshot); and `/worktree` alone
says where the chat works and where every worktree of the repository stands, as
`worktree_list` does. The command makes the same checks and refusals as the
tools, but as operator input it asks for no approval and works in every session
mode. The messenger bots drop it like the other built-ins, and a turn the bot
restricts for somebody who is not its admin refuses it.

Creation requires an `origin` remote and an approval under the current tool
permission mode; reusing a worktree that already exists needs neither the
network nor `origin`. A refusal or a failed fetch leaves the session in its
original directory. Other errors the tool reports: an invalid branch name,
`origin/HEAD` that resolves to nothing, the branch being checked out in the
main checkout, and a worktree path that already exists on disk. Sessions whose
cwd sits inside a git submodule resolve `repo_root` to nothing and group under
their own folder in History.

The main checkout is advanced to the fetched default commit only by a safe
fast-forward: it must already be an ancestor, and a checked-out default branch
must have a clean worktree. A dirty or diverged main checkout is left alone;
the new feature branch still starts at the fetched remote commit. The agent's
turn context names the main checkout path and the default branch. Use
`run_command` with `cwd` when a command must run in the main checkout for one
call; omitting `cwd` uses the session's worktree.

Coddy keeps the worktrees under `<main checkout>/.coddy/worktrees/`; for
example, `feature/login` becomes `feature-login`. A `.gitignore` in that parent
folder hides the worktrees from the main checkout's status. Project rules,
skills, hooks and configured MCP servers are reloaded for the new cwd, with
project MCP declarations checked through the workspace trust gate; what the main
checkout approved holds there, and the project MCP servers it runs are shared
rather than started again
([What a worktree shares with its main checkout](#what-a-worktree-shares-with-its-main-checkout)).
The session records its new cwd for later turns.

A branch that exists only on a remote other than `origin` opens the same way: by
its name (`fix/typo`, taken from `origin` when `origin` has it too, else from
the first remote by name that does), or by its remote-tracking name
(`upstream/fix/typo`) to pick the remote. Either way the local `fix/typo` is
created at the remote branch and tracks it; an existing local `fix/typo` that
already tracks it is reused, and one that tracks something else is refused
rather than silently opened. The tool's answer names the local branch.

In the web UI, select a feature branch with the **Worktree** checkbox on the
plate over the composer before the first message. The same fetch, branch checks
and worktree placement apply. The branch list fetches every remote before it
opens and lists the branches only on a remote among the local ones, marked with
a cloud, so a branch pushed from another machine can be opened without a
terminal; see
[Per-session workspace](../surfaces/web-ui.md#per-session-workspace-folder--branch--worktree). Once the chat runs, the plate shows the main
project's name and the feature branch, the branch's tooltip naming the worktree;
History groups the main checkout and its worktrees under that project. The
session still runs inside the worktree. The web workspace picks give way to that
plate once the conversation starts; the agent tool can move its own session
during a turn.

A chat that runs in a linked worktree has the way back on the plate: the arrow
right after the branch.

![The plate over the composer of a chat in a worktree: the project, the worktree's branch and the arrow back to the main checkout](../assets/git-worktree/worktree-leave-plate-dark-1280.png)

*The arrow after the branch brings the chat back to the main checkout.*

The arrow asks first whether to keep the worktree or remove it, the way Claude
Code asks when a worktree session ends, with what removing would touch in view:
the branch, uncommitted changes, commits not pushed, and whatever stops a
removal now (another chat in the worktree, a running background task, a lock,
commits on no branch, submodules, a git repository of its own inside it, a
worktree Coddy did not open). Keeping is the
default answer and sends `/worktree exit`. Removing sends `/worktree exit
--remove`, or `/worktree exit --remove --force` when there are uncommitted
changes, which the button then says it discards and the dialog says it keeps as
a snapshot. The dialog reads `GET /coddy/sessions/{id}/worktree` each time it
opens, and again when the chat moves to another folder while it is open. The plate names the main checkout as soon as that turn ends: it reads
where the chat works after every turn of the chat, so it follows `worktree_open`,
`worktree_exit` and `/worktree` without a reload.

![The dialog of the arrow: the branch, three uncommitted changes and a commit not pushed, with Cancel, Discard changes and remove, and Keep worktree](../assets/git-worktree/worktree-leave-dialog-dark-1280.png)

*Removing a worktree with uncommitted work says that it discards it and keeps a snapshot.*

![History grouped under the main project, and the plate over the composer naming the project and the worktree's branch](../assets/git-worktree/worktree-project-group-dark-1920.png)

*The feature session runs in its worktree while the plate and History name the main project.*

`GET /coddy/workspace/context` reports `repo_root` (the main checkout) and
`base_branch` (from `origin/HEAD`) alongside the current `path` and `branch`.
Session list rows carry `repoRoot` for grouping. See the
[Web UI](../surfaces/web-ui.md#per-session-workspace-folder--branch--worktree)
and [HTTP API](../reference/http-api.md) for those surfaces.

If a managed worktree is later removed with `git worktree remove`, a saved
session that pointed inside `<main>/.coddy/worktrees/` opens against the main
checkout instead. History groups it there immediately; the original recorded
worktree path is retained until you explicitly choose another workspace.

## What a worktree shares with its main checkout

A linked worktree is the same project as its main checkout, so what Coddy decided
about the project holds in every worktree of it. Only what the worktree itself adds
or changes is asked about again:

- approvals of project configuration. A project MCP declaration, a project subagent
  definition, a project hook file, a project marketplace entry and a project
  scheduler job approved in the main checkout are approved in its worktrees. A
  receipt is still bound to the digest of what was approved, so a declaration, a
  file or an entry the branch added or changed waits for its own approval. An
  approval given in a worktree holds for the project, so the branch merged with the
  same content asks nothing, and a revoke in either checkout revokes for the
  project;
- your switches of project MCP servers (`<home>/mcp-overrides.json`): a server
  switched off in the main checkout is off in its worktrees;
- project MCP servers. A project server whose declaration does not name `${CWD}`
  runs the same command in every checkout, so the main checkout and its worktrees
  share one process, and moving a chat into a worktree does not restart it. A
  server that names `${CWD}` sees the files of its own checkout and runs once per
  worktree: that is how an MCP server under development in a branch is tested;
- project scheduler jobs. The scheduler scans projects, not checkouts: a project
  job runs on cron once, from the main checkout, however many worktrees chats
  opened the scheduler in. A worktree's copy of a job is listed as not scheduled
  and can be run by hand from a chat there; a job a branch adds runs on cron once
  the branch is merged.

A folder inside a worktree answers to the same folder of the main checkout:
`<worktree>/pkg` to `<main checkout>/pkg`. In a worktree, `coddy mcp list`,
`coddy agents list` and `coddy hooks list` add a line naming the main checkout the
approvals are kept for, and the `workspace` field of `GET /coddy/mcp`,
`GET /coddy/subagents` and `GET /coddy/hooks` names it too.

A folder counts as a worktree only when git links it both ways: its `.git` file
names the repository's worktree storage, and that storage names the folder back. A
`.git` file copied or written into another folder lends that folder nothing.
Approvals a worktree recorded under its own path before Coddy shared them were not
moved, so the worktree asks once more for what only it had approved.
