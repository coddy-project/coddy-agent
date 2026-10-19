# A worktree shares what belongs to its project

Design record for stage 2 of easy git worktrees (stage 1 was pull request #561: `worktree_open`,
`worktree_list`, `worktree_exit`, `/worktree` and the leave dialog). It decides what a linked
worktree shares with the main checkout of its repository and what stays its own.

## The problem

A worktree is a second checkout of the same project, but every store that holds a decision about
project-local configuration keyed it by the workspace path:

- the trust receipts of project MCP declarations (`<home>/mcp-trust.json`), project subagent
  definitions (`subagents-trust.json`), project hook files (`hooks-trust.json`), project
  marketplace entries (`skills-trust.json`) and project scheduler jobs (`scheduler-trust.json`);
- the project switches of `<home>/mcp-overrides.json`;
- the key of a project MCP server in the shared server pool, which made it one process per
  workspace;
- the set of workspaces the scheduler daemon scans for project jobs.

So a chat that moved into a worktree was asked again for every server, definition, hook file and
job the main checkout had approved, started a second copy of every project MCP server, and a
project job could run on cron once per worktree a chat had opened the scheduler in (under
`scheduler.project_trust: allow` that took nothing more than opening the drawer).

What the operator asked for: in a worktree, nothing of the project starts a second time, and only
what the worktree itself adds or changes (a new MCP server under test, a changed definition) is
asked about and run for it.

## How others do it

Codex resolves a worktree to the root of its main repository before it looks a project up in its
trusted projects (`resolve_root_git_project_for_trust`): trust belongs to the repository, not to
the folder of one checkout. Coddy binds a receipt to the digest of what it approved (the
declaration, the file bytes, the entry), so keying the workspace part by the repository keeps "a
new or changed item asks again" for free.

## Decision

1. **One identity per project folder.** `gitws.MainCheckoutFolder(dir)` answers, for a folder of a
   linked worktree, the same folder of the main checkout (`<worktree>/pkg` answers
   `<main>/pkg`). It reads the worktree's `.git` file and the repository's
   `worktrees/<name>/{commondir,gitdir}` and never runs git, since trust decisions run on every
   turn. A worktree counts only when the repository names it back (its `gitdir` file points at
   this `.git` file): a `.git` file copied or written into another folder lends that folder
   nothing. The storage is read where it really is, its links resolved before anything (the main
   checkout, a relative back-link as git 2.48+ writes them) is derived from it, so a tree that
   reaches a repository's storage through a link of its own does not pass for one of its
   worktrees. The answer must stay inside the main checkout once links are resolved, so a folder the
   main checkout holds as a link elsewhere (committed, or shipped in an archive together with
   storage of its own) answers nothing, nor does one in a repository nested in the main checkout,
   and a repository whose config says `core.bare` has no main checkout, its storage named `.git`
   or not. Only regular files are read, each up to a small limit: a `.git` that is a link to a
   file, a FIFO or a device is refused without being read, since a blocked read would hold every
   trust decision of the process. The main checkout, a plain folder, a nested
   repository, a submodule and a worktree of a bare repository answer to themselves.
2. **`mcp.TrustWorkspace(cwd)`** is that identity in canonical form (symlinks resolved), and every
   trust store keys its receipts by it inside the store: `Approved`, `Approve`, `Revoke` and the
   listings. The loaders, the scope of a file and every path (where a job file is, where a hook
   file is relative to) keep the real workspace; only the key moves. The digest still binds, so an
   identical item is trusted in every worktree of the project and a new or changed one asks.
   Under the shared key each checkout keeps its own receipt (the `checkout` it was given in; a
   receipt without one belongs to the folder of its key), and the project approves every digest
   its checkouts approved: a worktree approving its changed declaration replaces only its own
   receipt, never the main checkout's. What a checkout deletes or renames in its own file (a
   scheduler job, a project marketplace entry) takes only its receipt; an explicit untrust takes
   the name from the whole project.
3. **Project switches** (`mcp-overrides.json`) key by the same identity: a server switched off for
   the project is off in its worktrees.
4. **The pool.** A project MCP server whose declaration does not name `${CWD}` resolves to the same
   command line in every checkout of the project (a stdio server is not started in the workspace
   folder; the workspace reaches it only through `${CWD}`), so its pool key takes the project
   identity and the main checkout and its worktrees share one process. A project server that names
   `${CWD}` keeps one process per workspace: in a worktree it must see the worktree's files, which
   is also how an MCP server under development in a branch is tested. A refresh after an approval
   reaches every session of the project, not only the sessions in the exact folder.
5. **The scheduler** scans projects, not checkouts: the registry and the scan set map every
   workspace to its identity before deduplicating, so a project job runs on cron once, from the
   main checkout, whatever worktrees chats opened. A job added in a branch is listed and can be
   run by hand from the worktree, and approved there (the receipt keys by the project and the
   digest); it runs on cron once the branch is merged with the same bytes.

## What it does not do

- Receipts written under a worktree path before this change are not migrated: a worktree asks
  once more for what only it had approved. Worktrees are short-lived, and what the main checkout
  approved is what they share.
- A second Coddy process in the same project keeps its own pool; sharing a process across Coddy
  processes is out of scope.
- No new setting: a worktree is the same project, and nothing about that is a preference.
