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
  /** Interpreter run_command goes through on the server host; absent on older servers. */
  shell?: string;
  repo_root?: string;
  base_branch?: string;
  branch?: string;
  branches?: string[];
  worktrees?: WorkspaceWorktree[];
};

export type WorkspaceFolderRow = {
  name: string;
  path: string;
  hidden?: boolean;
  symlink?: boolean;
  target?: string;
};

export type WorkspaceFolderListing = {
  path: string;
  parent: string;
  folders: WorkspaceFolderRow[];
  // The drive level (":drives:") lists the machine volumes instead of a real
  // folder: it is a place to navigate through, not a workspace to open.
  drives?: boolean;
};

export function pathBasename(p: string): string {
  const trimmed = (p || "").replace(/[/\\]+$/, "");
  const idx = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return idx >= 0 ? trimmed.slice(idx + 1) : trimmed;
}

// pathParent is separator-agnostic: the server may run on Windows, where the
// paths it hands back are backslash-separated. A path with no parent (a drive
// root, a bare UNC share, the posix root) is returned unchanged rather than
// collapsed to "/", which would send the picker to a different volume.
export function pathParent(p: string): string {
  const raw = (p || "").trim();
  const trimmed = raw.replace(/[/\\]+$/, "");
  const idx = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  if (idx < 0) {
    return raw;
  }
  if (idx === 0) {
    return "/";
  }
  const head = trimmed.slice(0, idx);
  if (/^[A-Za-z]:$/.test(head)) {
    // "H:" alone is the drive's current directory, not its root.
    return head + "\\";
  }
  if (/^\\\\[^\\]*$/.test(head)) {
    // "\\\\server" is a host, not a folder: the share is already the top.
    return raw;
  }
  return head;
}

// cleanPathInput normalizes a hand-typed or pasted path: Windows Explorer's
// "Copy as path" wraps the value in double quotes.
export function cleanPathInput(raw: string): string {
  const trimmed = (raw || "").trim();
  if (trimmed.length >= 2 && trimmed.startsWith('"') && trimmed.endsWith('"')) {
    return trimmed.slice(1, -1).trim();
  }
  return trimmed;
}

export function folderChipLabel(ctx: WorkspaceContext | null): string {
  if (!ctx) {
    return "workspace";
  }
  const name =
    ctx.is_worktree && ctx.repo_root
      ? pathBasename(ctx.repo_root)
      : (ctx.name || "").trim() || pathBasename(ctx.path);
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

/** How many characters of a branch name the bar shows before it cuts the middle. */
export const BRANCH_CHARS = 24;

/**
 * Cuts the middle out of a long name, keeping both ends: a branch is told
 * apart by its prefix (feat/, fix/) and by its last words alike.
 */
export function middleTruncate(text: string, max: number): string {
  if (text.length <= max) {
    return text;
  }
  if (max < 5) {
    // Too little room for both ends: the start alone, cut.
    return text.slice(0, Math.max(1, max));
  }
  const keep = max - 1;
  // A little more of the end than of the start: the last words tell
  // branches of one prefix apart.
  const head = Math.max(1, Math.ceil(keep / 2) - 2);
  const tail = keep - head;
  return text.slice(0, head) + "…" + text.slice(text.length - tail);
}
