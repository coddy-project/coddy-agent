import { t } from "../i18n/i18n";
import type { ChangeScope, SessionChangeDetail, SessionChanges } from "./types";

export type ChangesApiResult<T> =
  | { ok: true; data: T }
  | { ok: false; status: number; message: string };

async function readErrorMessage(res: Response): Promise<string> {
  try {
    const j = (await res.json()) as { error?: { message?: string } };
    const m = j?.error?.message;
    if (typeof m === "string" && m.trim()) {
      return m.trim();
    }
  } catch {
    /* ignore */
  }
  return `HTTP ${res.status}`;
}

async function parseJson<T>(res: Response): Promise<ChangesApiResult<T>> {
  if (!res.ok) {
    return {
      ok: false,
      status: res.status,
      message: await readErrorMessage(res),
    };
  }
  try {
    return { ok: true, data: (await res.json()) as T };
  } catch {
    return {
      ok: false,
      status: res.status,
      message: t("changes.invalidResponse"),
    };
  }
}

/**
 * The card refetches whenever a turn ends, so a server that is restarting must
 * degrade into a normal error result rather than an unhandled rejection.
 */
async function request(
  path: string,
  init: RequestInit,
): Promise<Response | null> {
  try {
    return await fetch(path, init);
  } catch {
    return null;
  }
}

// Built per call rather than once at module load, so a locale switch is picked
// up without a reload.
function offline<T>(): ChangesApiResult<T> {
  return { ok: false, status: 0, message: t("changes.offline") };
}

function basePath(sessionId: string): string {
  return `/coddy/sessions/${encodeURIComponent(sessionId)}/changes`;
}

function headers(sessionId: string): Record<string, string> {
  return { "X-Coddy-Session-ID": sessionId };
}

/** `?scope=` is omitted for the session default so older backends still answer. */
function scopeQuery(scope: ChangeScope | undefined, separator: string): string {
  return scope && scope !== "session" ? `${separator}scope=${scope}` : "";
}

/** Stats only: the card never needs the patches, which can be large. */
export async function fetchSessionChanges(
  sessionId: string,
  scope?: ChangeScope,
): Promise<ChangesApiResult<SessionChanges>> {
  const res = await request(basePath(sessionId) + scopeQuery(scope, "?"), {
    headers: headers(sessionId),
  });
  return res ? parseJson<SessionChanges>(res) : offline();
}

/** One file with its unified patch, fetched when the viewer selects a row. */
export async function fetchSessionChangeFile(
  sessionId: string,
  path: string,
  scope?: ChangeScope,
): Promise<ChangesApiResult<SessionChangeDetail>> {
  const res = await request(
    `${basePath(sessionId)}/file?path=${encodeURIComponent(path)}` +
      scopeQuery(scope, "&"),
    { headers: headers(sessionId) },
  );
  return res ? parseJson<SessionChangeDetail>(res) : offline();
}

/** Reverses every turn diff of the session. Destructive and not undoable. */
export async function revertSessionChanges(
  sessionId: string,
): Promise<ChangesApiResult<{ note: string }>> {
  const res = await request(`${basePath(sessionId)}/revert`, {
    method: "POST",
    headers: headers(sessionId),
  });
  return res ? parseJson<{ note: string }>(res) : offline();
}
