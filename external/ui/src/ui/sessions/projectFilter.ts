import type { SessionRow } from "./types";

/**
 * Which projects a listing shows. The default is "project": the listing opens
 * on the folder the operator is working in, and the filter menu takes it back
 * to every project. A row belongs to the project when its folder or its
 * repository root is the project folder, under it, or above it - a session in
 * a linked worktree is a session of the repository's project.
 */
export type SessionProjectFilter = "project" | "all";

export const SESSION_PROJECT_FILTERS: readonly SessionProjectFilter[] = [
  "project",
  "all",
];

export const DEFAULT_PROJECT_FILTER: SessionProjectFilter = "project";

/** Type guard, so a cookie from an older build cannot set a state that is gone. */
export function isSessionProjectFilter(v: string): v is SessionProjectFilter {
  return (SESSION_PROJECT_FILTERS as readonly string[]).includes(v);
}

function normalize(path: string): string {
  return path.replace(/\\/g, "/").replace(/\/+$/, "").toLowerCase();
}

export function isSameProject(
  row: Pick<SessionRow, "cwd" | "repoRoot">,
  projectPath: string,
): boolean {
  const root = normalize(projectPath);
  if (!root) return true;
  const cwd = row.cwd ? normalize(row.cwd) : "";
  const repo = row.repoRoot ? normalize(row.repoRoot) : "";
  const within = (path: string) =>
    path === root || path.startsWith(root + "/") || root.startsWith(path + "/");
  return within(cwd) || within(repo);
}

/** The rows one project filter keeps. */
export function applyProjectFilter(
  rows: SessionRow[],
  filter: SessionProjectFilter,
  projectPath: string | undefined,
): SessionRow[] {
  if (filter === "all" || !projectPath) return rows;
  return rows.filter((row) => isSameProject(row, projectPath));
}
