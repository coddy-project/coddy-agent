// Recently used workspace folders for the composer folder picker (MRU).

export type WorkspaceRecent = { path: string; name: string };

export const WORKSPACE_RECENTS_KEY = "coddy_workspace_recents_v1";

const WORKSPACE_RECENTS_CAP = 8;

export function readWorkspaceRecents(): WorkspaceRecent[] {
  try {
    const raw = localStorage.getItem(WORKSPACE_RECENTS_KEY);
    if (!raw) {
      return [];
    }
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) {
      return [];
    }
    return parsed.filter(
      (r): r is WorkspaceRecent =>
        !!r &&
        typeof (r as WorkspaceRecent).path === "string" &&
        typeof (r as WorkspaceRecent).name === "string",
    );
  } catch {
    return [];
  }
}

export function pushWorkspaceRecent(entry: WorkspaceRecent): WorkspaceRecent[] {
  const next = [
    entry,
    ...readWorkspaceRecents().filter((r) => r.path !== entry.path),
  ].slice(0, WORKSPACE_RECENTS_CAP);
  try {
    localStorage.setItem(WORKSPACE_RECENTS_KEY, JSON.stringify(next));
  } catch {
    // ignore quota errors: recents are a convenience
  }
  return next;
}
