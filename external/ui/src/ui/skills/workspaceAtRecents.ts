/** Used in localStorage before the shell assigns a **`sessionId`**. */
export const WORKSPACE_AT_RECENTS_NO_SESSION_KEY = "__no_session__";

const STORAGE_PREFIX = "coddy-ws-at-recents:";
const MAX_ENTRIES = 15;

export type WorkspaceAtRecentStored = {
  path_rel: string;
  kind: "file" | "dir";
};

function keyFor(workspaceKey: string): string {
  return `${STORAGE_PREFIX}${workspaceKey}`;
}

function isEntry(x: unknown): x is WorkspaceAtRecentStored {
  if (!x || typeof x !== "object") {
    return false;
  }
  const o = x as Record<string, unknown>;
  return (
    typeof o.path_rel === "string" &&
    o.path_rel.length > 0 &&
    !o.path_rel.includes("..") &&
    (o.kind === "file" || o.kind === "dir")
  );
}

/**
 * MRU list for **`@`** picker (files and folder rows). Separate list per workspace key
 * (normally **`sessionId`**).
 */
export function readWorkspaceAtRecents(workspaceKey: string): WorkspaceAtRecentStored[] {
  if (typeof localStorage === "undefined") {
    return [];
  }
  const wk = workspaceKey.trim();
  if (!wk) {
    return [];
  }
  try {
    const raw = localStorage.getItem(keyFor(wk));
    if (!raw) {
      return [];
    }
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) {
      return [];
    }
    const out: WorkspaceAtRecentStored[] = [];
    for (const el of parsed) {
      if (!isEntry(el)) {
        continue;
      }
      const path_rel =
        el.kind === "dir"
          ? el.path_rel.endsWith("/")
            ? el.path_rel
            : `${el.path_rel}/`
          : el.path_rel.replace(/\/$/, "");
      out.push({ path_rel, kind: el.kind });
    }
    return out;
  } catch {
    return [];
  }
}

function normalizeStored(row: {
  path_rel: string;
  kind: string;
}): WorkspaceAtRecentStored | null {
  let path_rel = row.path_rel.trim();
  if (!path_rel || path_rel.includes("..")) {
    return null;
  }
  const kind = row.kind === "dir" ? "dir" : "file";
  if (kind === "dir") {
    path_rel = path_rel.endsWith("/") ? path_rel : `${path_rel}/`;
  } else {
    path_rel = path_rel.replace(/\/$/, "");
  }
  return { path_rel, kind };
}

/** Remember a **`@`** path the user inserted or sent as an attachment for this workspace key. */
export function recordWorkspaceAtRecent(
  workspaceKey: string,
  row: { path_rel: string; kind: string },
): void {
  if (typeof localStorage === "undefined") {
    return;
  }
  const wk = workspaceKey.trim();
  if (!wk) {
    return;
  }
  const ent = normalizeStored(row);
  if (!ent) {
    return;
  }
  const cur = readWorkspaceAtRecents(wk);
  const tail = cur.filter((x) => x.path_rel !== ent.path_rel);
  const next = [ent, ...tail].slice(0, MAX_ENTRIES);
  try {
    localStorage.setItem(keyFor(wk), JSON.stringify(next));
  } catch {
    /* quota or private mode */
  }
}

/**
 * Move recents when the client or server assigns a new session id (first send, or header refresh).
 */
export function migrateWorkspaceAtRecents(fromKey: string, toKey: string): void {
  if (typeof localStorage === "undefined") {
    return;
  }
  const from = fromKey.trim();
  const to = toKey.trim();
  if (!from || !to || from === to) {
    return;
  }
  const a = readWorkspaceAtRecents(from);
  if (a.length === 0) {
    return;
  }
  const b = readWorkspaceAtRecents(to);
  const seen = new Set<string>();
  const merged: WorkspaceAtRecentStored[] = [];
  for (const x of [...a, ...b]) {
    if (seen.has(x.path_rel)) {
      continue;
    }
    seen.add(x.path_rel);
    merged.push(x);
  }
  const sliced = merged.slice(0, MAX_ENTRIES);
  try {
    localStorage.setItem(keyFor(to), JSON.stringify(sliced));
    localStorage.removeItem(keyFor(from));
  } catch {
    /* ignore */
  }
}

/** Shape expected by **`Composer`** workspace rows. */
export function pickerRowFromRecent(e: WorkspaceAtRecentStored): {
  name: string;
  path_rel: string;
  kind: string;
} {
  const path_rel =
    e.kind === "dir"
      ? e.path_rel.endsWith("/")
        ? e.path_rel
        : `${e.path_rel}/`
      : e.path_rel.replace(/\/$/, "");
  const stem = path_rel.replace(/\/+$/, "");
  const base = stem.split("/").filter(Boolean).pop() || stem;
  const name = e.kind === "dir" ? `${base}/` : base;
  return { path_rel, kind: e.kind, name };
}
