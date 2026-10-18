import {
  isOperatorCommandTool,
  mergeOperatorCommandState,
  parseOperatorCommandRequest,
  type OperatorCommandAction,
  type OperatorCommandRequest,
} from "./operatorCommand";
import type { TranscriptItem } from "./types";

/**
 * A waiting operator command survives a reload of the page: the server keeps
 * waiting for the answer, and the event that announced the request is not
 * sent again once the turn's stream is gone. The request is kept in
 * sessionStorage (this tab only, like the composer's draft) per session, with
 * the answer this tab gave, and dropped once the call has finished.
 */

const STORAGE_PREFIX = "coddy_opc_v1:";

/** A session holds a handful at most; an abandoned one must not pile up. */
const MAX_RECORDS = 16;

export type StoredOperatorCommandRecord = {
  request: OperatorCommandRequest;
  answered?: OperatorCommandAction;
};

function storageKey(sessionId: string): string {
  return `${STORAGE_PREFIX}${sessionId.trim()}`;
}

function storage(): Storage | null {
  try {
    return typeof window === "undefined" ? null : window.sessionStorage;
  } catch {
    return null;
  }
}

export function loadOperatorCommandRecords(
  sessionId: string,
): StoredOperatorCommandRecord[] {
  const sid = sessionId.trim();
  const st = storage();
  if (!sid || !st) return [];
  try {
    const raw = st.getItem(storageKey(sid));
    if (!raw) return [];
    const v = JSON.parse(raw) as unknown;
    if (!Array.isArray(v)) return [];
    const out: StoredOperatorCommandRecord[] = [];
    for (const row of v) {
      if (!row || typeof row !== "object") continue;
      const o = row as Record<string, unknown>;
      const request = parseOperatorCommandRequest(o.request);
      if (!request) continue;
      const answered =
        o.answered === "run" ||
        o.answered === "decline" ||
        o.answered === "stop"
          ? o.answered
          : undefined;
      out.push(answered ? { request, answered } : { request });
    }
    return out;
  } catch {
    return [];
  }
}

function saveRecords(sessionId: string, list: StoredOperatorCommandRecord[]) {
  const st = storage();
  if (!st) return;
  try {
    if (list.length === 0) st.removeItem(storageKey(sessionId));
    else
      st.setItem(
        storageKey(sessionId),
        JSON.stringify(list.slice(-MAX_RECORDS)),
      );
  } catch {
    // quota or privacy mode
  }
}

/** Keep a request the server announced, keyed by its session. */
export function upsertOperatorCommandRequest(
  request: OperatorCommandRequest,
): void {
  const sid = request.sessionId.trim();
  if (!sid) return;
  const list = loadOperatorCommandRecords(sid);
  const i = list.findIndex((r) => r.request.requestId === request.requestId);
  if (i >= 0) list[i] = { ...list[i], request };
  else list.push({ request });
  saveRecords(sid, list);
}

/** Note the answer this tab gave, so a reload does not offer Run again. */
export function markOperatorCommandAnswered(
  sessionId: string,
  requestId: string,
  answered: OperatorCommandAction,
): void {
  const sid = sessionId.trim();
  if (!sid) return;
  const list = loadOperatorCommandRecords(sid);
  const i = list.findIndex((r) => r.request.requestId === requestId);
  if (i < 0) return;
  list[i] = { ...list[i]!, answered };
  saveRecords(sid, list);
}

/** Forget the request of a call that has finished. */
export function dropOperatorCommandRecord(
  sessionId: string,
  toolCallId: string,
): void {
  const sid = sessionId.trim();
  if (!sid) return;
  const list = loadOperatorCommandRecords(sid);
  const next = list.filter((r) => r.request.toolCallId !== toolCallId);
  if (next.length !== list.length) saveRecords(sid, next);
}

export function clearOperatorCommandRecords(sessionId: string): void {
  const sid = sessionId.trim();
  const st = storage();
  if (!sid || !st) return;
  try {
    st.removeItem(storageKey(sid));
  } catch {
    //
  }
}

function terminal(status: string): boolean {
  return (
    status === "completed" || status === "failed" || status === "cancelled"
  );
}

/**
 * Put the kept requests back on their tool rows after a transcript read, and
 * carry the live state of a run the read does not know about (the server's
 * rows carry no operator state) from the rows on screen. A finished call drops
 * its record: its card is read from the call's arguments and result.
 */
export function mergeStoredOperatorCommandsIntoTranscript(
  items: TranscriptItem[],
  sessionId: string,
  local?: readonly TranscriptItem[],
): TranscriptItem[] {
  const records = loadOperatorCommandRecords(sessionId);
  const liveByTc = new Map<
    string,
    NonNullable<
      Extract<TranscriptItem, { type: "tool_call" }>["operatorCommand"]
    >
  >();
  for (const it of local ?? []) {
    if (it.type === "tool_call" && it.operatorCommand) {
      liveByTc.set(it.toolCallId, it.operatorCommand);
    }
  }
  if (records.length === 0 && liveByTc.size === 0) return items;
  const byTc = new Map(records.map((r) => [r.request.toolCallId, r]));
  let changed = false;
  const out = items.map((it) => {
    if (it.type !== "tool_call") return it;
    if (!isOperatorCommandTool(it.title, it.kind) && !byTc.has(it.toolCallId))
      return it;
    const rec = byTc.get(it.toolCallId);
    if (terminal(it.status)) {
      if (rec) dropOperatorCommandRecord(sessionId, it.toolCallId);
      return it;
    }
    let state = it.operatorCommand;
    const live = liveByTc.get(it.toolCallId);
    if (live) state = mergeOperatorCommandState(live, state ?? {});
    if (rec) {
      state = mergeOperatorCommandState(
        {
          request: rec.request,
          ...(rec.answered ? { answered: rec.answered } : {}),
        },
        state ?? {},
      );
    }
    if (!state || state === it.operatorCommand) return it;
    changed = true;
    return { ...it, operatorCommand: state };
  });
  return changed ? out : items;
}
