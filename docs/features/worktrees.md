# Git worktrees

Coddy can give a session its own feature branch and working copy. The session's
working directory moves into the worktree, so its next `run_command`, file tool,
rule lookup and project-scoped MCP connection use that directory without a
manual `cd`.

In an agent conversation, ask Coddy to work on a feature branch. The
`worktree_create` tool takes `{"branch":"feature/login"}`. It fetches `origin`,
reads the default branch from `origin/HEAD`, and creates a new branch from the
fresh `origin/<base>` commit. A branch that exists only on `origin` is checked
out from `origin/<branch>` with tracking; a branch that already exists locally
is reused at its own tip, and a branch already checked out in a worktree reuses
that worktree. The tool refuses the default branch itself and a local branch
that tracks `origin/<base>`.

A new branch can also start where the session stands: with `"base":"head"` the
tool starts it at the commit the session's checkout is on, so the next branch is
stacked on the work in progress, even when the session works in a worktree
itself. A branch that already exists keeps its own tip whatever `base` says. In
both cases the new worktree goes under the main checkout's `.coddy/worktrees/`,
never inside the worktree it was opened from.

When the work in a worktree is done, `worktree_exit` moves the session back to
the main checkout, and its next tool call runs there. `{"action":"keep"}`, the
default, leaves the worktree and its branch for later; `worktree_create` with the
same branch goes back to it. `{"action":"remove"}` removes the worktree on the
way. It refuses a worktree with uncommitted changes or initialised submodules
unless `discard_changes` is `true`, and, whatever `discard_changes` says, a
worktree on commits no branch holds (a detached HEAD with work of its own), a
locked one, one another live session or a background task of any session still
works in, and one Coddy did not open (outside `.coddy/worktrees/`). The branch
goes with the worktree only when git counts it as merged (`git branch -d`): a
branch with commits of its own stays, and the answer says so. A refusal leaves
the session where it was. A session whose worktree folder is gone, removed by
hand or by another chat, comes back to the main checkout with the same call. Like `worktree_create`, the tool asks for approval under
the current permission mode, runs in agent mode only and is never offered to a
subagent.

The operator does the same with the `/worktree` command in the console, the web
UI and an ACP editor, without a turn of the model: `/worktree <branch>` opens a
worktree for the branch and moves the chat there (`--from-head` starts a new
branch at the current commit), `/worktree exit` brings the chat back to the main
checkout (`--remove` removes the worktree too, and `--force` with it drops its
uncommitted changes), and `/worktree` alone says where the chat works and lists
the worktrees of the repository. The command makes the same checks and refusals
as the tools, but as operator input it asks for no approval and works in every
session mode. The messenger bots drop it like the other built-ins, and a turn the
bot restricts for somebody who is not its admin refuses it.

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
project MCP declarations checked through the workspace trust gate. The session
records its new cwd for later turns.

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
right after the branch sends `/worktree exit`, and the plate names the main
checkout as soon as that turn ends. The plate reads where the chat works after
every turn of the chat, so it follows `worktree_create`, `worktree_exit` and
`/worktree` without a reload.

![The plate over the composer of a chat in a worktree: the project, the worktree's branch and the arrow back to the main checkout](../assets/git-worktree/worktree-leave-plate-dark-1280.png)

*The arrow after the branch brings the chat back to the main checkout.*

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
