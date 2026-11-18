export const VIEWED_FILES_STORAGE_KEY = "coddy.changes.viewed";

type ViewedFiles = Record<string, Record<string, string>>;

function readViewedFiles(): ViewedFiles {
  try {
    const parsed: unknown = JSON.parse(
      window.localStorage.getItem(VIEWED_FILES_STORAGE_KEY) ?? "{}",
    );
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return {};
    }
    return parsed as ViewedFiles;
  } catch {
    return {};
  }
}

export function isFileViewed(
  workspace: string,
  path: string,
  digest: string,
): boolean {
  if (!workspace || !path || !digest) {
    return false;
  }
  const workspaces = readViewedFiles();
  return workspaces[workspace]?.[path] === digest;
}

export function setFileViewed(
  workspace: string,
  path: string,
  digest: string,
  viewed: boolean,
): void {
  if (!workspace || !path || !digest) {
    return;
  }
  const workspaces = readViewedFiles();
  const files = { ...workspaces[workspace] };
  if (viewed) {
    files[path] = digest;
  } else {
    delete files[path];
  }
  const next = { ...workspaces, [workspace]: files };
  try {
    window.localStorage.setItem(VIEWED_FILES_STORAGE_KEY, JSON.stringify(next));
  } catch {
    // A disabled or full store only loses this presentation preference.
  }
}
