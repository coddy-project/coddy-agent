// Workspace context helpers for the composer chips (folder / branch / worktree).
// Shapes mirror GET /coddy/workspace/context and /coddy/workspace/folders.

export type WorkspaceWorktree = {
  path: string;
  branch: string;
  main: boolean;
};

export type WorkspaceContext = {
  path: string;
  name: string;
  is_git_repo: boolean;
  is_worktree: boolean;
  repo_root?: string;
  branch?: string;
  branches?: string[];
  worktrees?: WorkspaceWorktree[];
};

export type WorkspaceFolderRow = { name: string; path: string };

export type WorkspaceFolderListing = {
  path: string;
  parent: string;
  folders: WorkspaceFolderRow[];
};

export function pathBasename(p: string): string {
  const trimmed = (p || "").replace(/[/\\]+$/, "");
  const idx = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return idx >= 0 ? trimmed.slice(idx + 1) : trimmed;
}

export function pathParent(p: string): string {
  const trimmed = (p || "").replace(/[/\\]+$/, "");
  const idx = trimmed.lastIndexOf("/");
  if (idx <= 0) {
    return "/";
  }
  return trimmed.slice(0, idx);
}

export function folderChipLabel(ctx: WorkspaceContext | null): string {
  if (!ctx) {
    return "workspace";
  }
  const name = (ctx.name || "").trim() || pathBasename(ctx.path);
  return name || "workspace";
}

export function branchChipVisible(ctx: WorkspaceContext | null): boolean {
  return Boolean(ctx?.is_git_repo);
}

// sortedBranches lists the current branch first, the rest alphabetically.
export function sortedBranches(ctx: WorkspaceContext): string[] {
  const branches = [...(ctx.branches || [])];
  branches.sort((a, b) => a.localeCompare(b));
  const current = (ctx.branch || "").trim();
  if (!current) {
    return branches;
  }
  return [current, ...branches.filter((b) => b !== current)];
}

// worktreeForBranch returns the linked (non-main) worktree holding branch.
export function worktreeForBranch(
  ctx: WorkspaceContext,
  branch: string,
): WorkspaceWorktree | null {
  for (const wt of ctx.worktrees || []) {
    if (!wt.main && wt.branch === branch) {
      return wt;
    }
  }
  return null;
}

// isWorktreeBadgeActive: the chip lights up when the session already lives in
// a worktree, or when the user opted future branch switches into worktrees.
export function isWorktreeBadgeActive(
  ctx: WorkspaceContext | null,
  worktreePref: boolean,
): boolean {
  if (!ctx || !ctx.is_git_repo) {
    return false;
  }
  return ctx.is_worktree || worktreePref;
}
