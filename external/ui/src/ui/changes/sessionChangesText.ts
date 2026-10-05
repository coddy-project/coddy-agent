import type { ChangeStatus } from "./types";

/** i18n key for a file's status badge. */
export function statusKey(status: ChangeStatus): string {
  return "changes.status." + status;
}

/**
 * The last path segment, used as the row label with the folder shown beside it.
 * Both separators are handled: the server reports workspace-relative paths, and
 * on Windows those carry backslashes.
 */
export function baseName(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  const cut = normalized.lastIndexOf("/");
  return cut === -1 ? normalized : normalized.slice(cut + 1);
}

/** The directory part of a path, or "" when the file sits at the workspace root. */
export function dirName(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  const cut = normalized.lastIndexOf("/");
  return cut === -1 ? "" : normalized.slice(0, cut);
}
