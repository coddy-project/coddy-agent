import { envStorageSuffix } from "../env/remoteEnv";

const STORAGE_KEY = "coddy_sessions_collapsed_groups_v1";
const MAX_GROUPS = 500;

function storageKey(): string {
  return `${STORAGE_KEY}::${envStorageSuffix()}`;
}

export function readCollapsedSessionGroups(): ReadonlySet<string> {
  if (typeof localStorage === "undefined") {
    return new Set();
  }
  try {
    const raw = localStorage.getItem(storageKey());
    if (!raw) {
      return new Set();
    }
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      return new Set();
    }
    return new Set(
      parsed
        .filter((key): key is string => typeof key === "string" && key !== "")
        .slice(-MAX_GROUPS),
    );
  } catch {
    return new Set();
  }
}

export function writeCollapsedSessionGroups(groups: ReadonlySet<string>): void {
  if (typeof localStorage === "undefined") {
    return;
  }
  try {
    localStorage.setItem(
      storageKey(),
      JSON.stringify([...groups].filter(Boolean).slice(-MAX_GROUPS)),
    );
  } catch {
    // The drawer remains usable when browser storage is unavailable.
  }
}

export const COLLAPSED_SESSION_GROUPS_STORAGE_KEY = STORAGE_KEY;
