import { t } from "../i18n/i18n";
import type { SessionChangeDetail, SessionChanges } from "./types";

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
 * The working copy is read again whenever a turn ends, so a server that is
 * restarting must degrade into a normal error result rather than an unhandled
 * rejection.
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

/** What git reports for the session's folder, stats only: the patches can be large. */
export async function fetchSessionChanges(
  sessionId: string,
): Promise<ChangesApiResult<SessionChanges>> {
  const res = await request(basePath(sessionId), {
    headers: headers(sessionId),
  });
  return res ? parseJson<SessionChanges>(res) : offline();
}

/** One file with its unified patch, fetched when a viewer shows it. */
export async function fetchSessionChangeFile(
  sessionId: string,
  path: string,
): Promise<ChangesApiResult<SessionChangeDetail>> {
  const res = await request(
    `${basePath(sessionId)}/file?path=${encodeURIComponent(path)}`,
    { headers: headers(sessionId) },
  );
  return res ? parseJson<SessionChangeDetail>(res) : offline();
}

/** Which uncommitted changes a discard puts back at HEAD. */
export type DiscardSelection = { paths: string[] } | { all: true };

/**
 * Puts files of the session's folder back at HEAD through git: a tracked file
 * gets HEAD's content back, a new one is deleted. Destructive and not undoable.
 */
export async function discardSessionChanges(
  sessionId: string,
  selection: DiscardSelection,
): Promise<ChangesApiResult<{ sessionId: string }>> {
  const res = await request(`${basePath(sessionId)}/revert`, {
    method: "POST",
    headers: { ...headers(sessionId), "Content-Type": "application/json" },
    body: JSON.stringify(selection),
  });
  return res ? parseJson<{ sessionId: string }>(res) : offline();
}
