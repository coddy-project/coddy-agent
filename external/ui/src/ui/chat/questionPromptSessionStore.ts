import { parseQuestionToolQuestionsFromArgs } from "./questionToolDisplay";
import type {
  CoddyQuestionPayload,
  QuestionResolvedState,
} from "./questionTypes";
import type { TranscriptItem } from "./types";

const STORAGE_PREFIX = "coddy_qp_v1:";

export type StoredQuestionPromptRecord = {
  requestId: string;
  payload: CoddyQuestionPayload;
  resolved?: QuestionResolvedState | undefined;
};

export function hasUnresolvedQuestionPrompt(
  items: readonly TranscriptItem[],
): boolean {
  return items.some(
    (item) => item.type === "question_prompt" && !item.resolved,
  );
}

function storageKey(sessionId: string): string {
  return `${STORAGE_PREFIX}${sessionId.trim()}`;
}

export function loadQuestionPromptRecords(
  sessionId: string,
): StoredQuestionPromptRecord[] {
  if (typeof window === "undefined") return [];
  const sid = sessionId.trim();
  if (!sid) return [];
  try {
    const raw = window.localStorage.getItem(storageKey(sid));
    if (!raw) return [];
    const v = JSON.parse(raw) as unknown;
    if (!Array.isArray(v)) return [];
    const out: StoredQuestionPromptRecord[] = [];
    for (const row of v) {
      if (!row || typeof row !== "object") continue;
      const o = row as Record<string, unknown>;
      const requestId = String(o.requestId ?? "").trim();
      const payload = o.payload as CoddyQuestionPayload | undefined;
      if (!requestId || !payload?.requestId) continue;
      out.push({
        requestId,
        payload,
        ...(o.resolved !== undefined
          ? { resolved: o.resolved as QuestionResolvedState }
          : {}),
      });
    }
    return out;
  } catch {
    return [];
  }
}

export function upsertQuestionPromptRecord(
  sessionId: string,
  record: StoredQuestionPromptRecord,
): void {
  if (typeof window === "undefined") return;
  const sid = sessionId.trim();
  const rid = record.requestId.trim();
  if (!sid || !rid) return;
  const list = loadQuestionPromptRecords(sid);
  const i = list.findIndex((r) => r.requestId.trim() === rid);
  const next = {
    requestId: rid,
    payload: record.payload,
    ...(record.resolved !== undefined ? { resolved: record.resolved } : {}),
  };
  if (i >= 0) {
    list[i] = {
      ...list[i],
      ...next,
      resolved:
        record.resolved !== undefined ? record.resolved : list[i]?.resolved,
    };
  } else {
    list.push(next);
  }
  try {
    window.localStorage.setItem(storageKey(sid), JSON.stringify(list));
  } catch {
    // quota or privacy mode
  }
}

export function clearQuestionPromptRecords(sessionId: string): void {
  if (typeof window === "undefined") return;
  const sid = sessionId.trim();
  if (!sid) return;
  try {
    window.localStorage.removeItem(storageKey(sid));
  } catch {
    //
  }
}

/** Rebuild tool arguments JSON so Question rows parse after reload. */
export function questionToolArgsJsonFromPayload(
  payload: CoddyQuestionPayload,
): string {
  return JSON.stringify({ questions: payload.questions });
}

/**
 * When GET tool-calls returns a truncated argsPreview, keep richer args from messages.
 */
export function pickRicherQuestionToolArgs(
  fromMessage: string | undefined,
  fromApiPreview: string | undefined,
): string | undefined {
  const a = String(fromMessage ?? "").trim();
  const b = String(fromApiPreview ?? "").trim();
  if (!a) return b || undefined;
  if (!b) return a || undefined;
  const na = parseQuestionToolQuestionsFromArgs(a).length;
  const nb = parseQuestionToolQuestionsFromArgs(b).length;
  if (nb > na) return b;
  return a;
}

export function mergeStoredQuestionPromptsIntoTranscript(
  merged: TranscriptItem[],
  sessionId: string,
): TranscriptItem[] {
  const records = loadQuestionPromptRecords(sessionId);
  if (records.length === 0) return merged;

  let out = [...merged];
  const existing = new Set(
    out
      .filter(
        (x): x is Extract<TranscriptItem, { type: "question_prompt" }> =>
          x.type === "question_prompt",
      )
      .map((x) => x.payload.requestId.trim()),
  );

  for (const rec of records) {
    const rid = rec.payload.requestId.trim();
    if (!rid || existing.has(rid)) continue;

    const tcid = rec.payload.toolCallId?.trim();
    let insertAt = -1;
    if (tcid) {
      const idx = out.findIndex(
        (x) => x.type === "tool_call" && x.toolCallId === tcid,
      );
      if (idx >= 0) insertAt = idx + 1;
    }
    if (insertAt < 0) continue;

    const row: Extract<TranscriptItem, { type: "question_prompt" }> = {
      id: `qp_${rid}`,
      type: "question_prompt",
      payload: rec.payload,
      ...(rec.resolved !== undefined ? { resolved: rec.resolved } : {}),
    };
    out.splice(insertAt, 0, row);
    existing.add(rid);
  }

  return out;
}

/** Fill missing question tool args from persisted SSE payloads so the tool row renders Q&A. */
export function patchQuestionToolArgsFromPromptRecords(
  items: TranscriptItem[],
  sessionId: string,
): TranscriptItem[] {
  const records = loadQuestionPromptRecords(sessionId);
  if (records.length === 0) return items;

  const byTc = new Map<string, StoredQuestionPromptRecord>();
  for (const r of records) {
    const id = r.payload.toolCallId?.trim();
    if (id) byTc.set(id, r);
  }
  if (byTc.size === 0) return items;

  return items.map((it) => {
    if (it.type !== "tool_call") return it;
    const name = (it.title || it.kind || "").trim().toLowerCase();
    if (name !== "question") return it;
    const rec = byTc.get(it.toolCallId);
    if (!rec) return it;
    const synthetic = questionToolArgsJsonFromPayload(rec.payload);
    const curN = parseQuestionToolQuestionsFromArgs(it.argsText).length;
    if (curN === 0) {
      return { ...it, argsText: synthetic };
    }
    return it;
  });
}
