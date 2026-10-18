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
 * the answer this tab gave, and dropped once the call has finished - unless
 * the command was being stopped then: one that outlives a cancelled turn keeps
 * its request open until it ends, and no frame says when, so the record goes
 * when a Stop learns the request is over.
 */

const STORAGE_PREFIX = "coddy_opc_v1:";

/** A session holds a handful at most; an abandoned one must not pile up. */
const MAX_RECORDS = 16;

export type StoredOperatorCommandRecord = {
  request: OperatorCommandRequest;
  answered?: OperatorCommandAction;
  /** The last frame said stopping: the command may outlive its call. */
  stopping?: boolean;
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
      const rec: StoredOperatorCommandRecord = { request };
      if (answered) rec.answered = answered;
      if (o.stopping === true) rec.stopping = true;
      out.push(rec);
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

/** Note that the command of a call is being stopped. */
export function markOperatorCommandStopping(
  sessionId: string,
  toolCallId: string,
): void {
  const sid = sessionId.trim();
  if (!sid) return;
  const list = loadOperatorCommandRecords(sid);
  const i = list.findIndex((r) => r.request.toolCallId === toolCallId);
  if (i < 0 || list[i]!.stopping) return;
  list[i] = { ...list[i]!, stopping: true };
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

/**
 * Whether the command of a call that ended may still run: the call did not
 * complete (a completed call carries the run's own end), its run did not end
 * on screen, and this tab ran it or saw it being stopped. A command that
 * outlives a cancelled turn keeps its request open, and Stop with it, until a
 * Stop learns the request is over.
 */
export function stillRuns(
  status: string,
  rec: StoredOperatorCommandRecord | undefined,
  live?: { phase?: string | undefined },
): boolean {
  if (status === "completed" || !rec) return false;
  if (live?.phase === "ended" || live?.phase === "stopped") return false;
  return (
    rec.stopping === true ||
    rec.answered === "run" ||
    rec.answered === "stop" ||
    live?.phase === "stopping" ||
    live?.phase === "running"
  );
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
    const live = liveByTc.get(it.toolCallId);
    const ended = terminal(it.status);
    if (ended && !stillRuns(it.status, rec, live)) {
      if (rec) dropOperatorCommandRecord(sessionId, it.toolCallId);
      // The run's own end, from the frames on screen, stays on the row: the
      // server's row carries none.
      if (!live) return it;
      changed = true;
      return {
        ...it,
        operatorCommand: mergeOperatorCommandState(
          live,
          it.operatorCommand ?? {},
        ),
      };
    }
    let state = it.operatorCommand;
    if (live) state = mergeOperatorCommandState(live, state ?? {});
    if (rec) {
      state = mergeOperatorCommandState(
        {
          request: rec.request,
          ...(rec.answered ? { answered: rec.answered } : {}),
          ...(rec.stopping ? { phase: "stopping" as const } : {}),
        },
        state ?? {},
      );
    }
    // A call that ended while its command runs on is being stopped, whatever
    // the last frame this tab saw.
    if (ended && state) state = { ...state, phase: "stopping" };
    if (!state || state === it.operatorCommand) return it;
    changed = true;
    return { ...it, operatorCommand: state };
  });
  return changed ? out : items;
}
