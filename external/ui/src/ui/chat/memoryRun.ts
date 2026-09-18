import type { TranscriptItem } from "./types";

/**
 * The `memory_run` event of the composer stream: the memory subagent of the
 * turn started, finished or could not start. No text travels on it; the run's
 * record is the Tasks drawer (an agent task flagged `system`) and the child
 * transcript it names.
 */
export type MemoryRunEvt = {
  status: string;
  taskId?: string;
  childSessionId?: string;
  taskStatus?: string;
  durationMs?: number;
  delivered?: boolean;
  reason?: string;
};

type MemoryRunItem = Extract<TranscriptItem, { type: "memory_run" }>;

function memoryRunItemId(e: MemoryRunEvt): string {
  const task = (e.taskId || "").trim();
  return task ? `mem-run-${task}` : `mem-run-skipped-${Date.now()}`;
}

/**
 * Applies one `memory_run` event to the transcript. The run is an invisible
 * item of the current turn (nothing renders it; the live status line reads
 * it): `started` places it right after the turn's user message, `finished`
 * and `skipped` patch it, or place it when the start was never seen (a
 * reconnect mid-run).
 */
export function applyMemoryRunToItems(
  prev: TranscriptItem[],
  e: MemoryRunEvt,
): TranscriptItem[] {
  const status = (e.status || "").trim();
  if (status !== "started" && status !== "finished" && status !== "skipped") {
    return prev;
  }
  let userIdx = -1;
  for (let i = prev.length - 1; i >= 0; i--) {
    const it = prev[i];
    if (it && (it.type === "user_message" || it.type === "background_wake")) {
      userIdx = i;
      break;
    }
  }
  const taskId = (e.taskId || "").trim();
  let idx = -1;
  for (let i = userIdx + 1; i < prev.length; i++) {
    const it = prev[i];
    if (!it) continue;
    if (it.type === "memory_run" && (!taskId || !it.taskId || it.taskId === taskId)) {
      idx = i;
      break;
    }
  }
  const now = Date.now();
  const base: MemoryRunItem =
    idx >= 0 && prev[idx]?.type === "memory_run"
      ? (prev[idx] as MemoryRunItem)
      : {
          id: memoryRunItemId(e),
          type: "memory_run",
          status: "started",
          startedAtMs: now,
        };
  const patch: MemoryRunItem = {
    ...base,
    status,
    ...(taskId ? { taskId } : {}),
    ...(e.childSessionId ? { childSessionId: e.childSessionId } : {}),
    ...(e.taskStatus ? { taskStatus: e.taskStatus } : {}),
    ...(typeof e.durationMs === "number" ? { durationMs: e.durationMs } : {}),
    ...(typeof e.delivered === "boolean" ? { delivered: e.delivered } : {}),
    ...(e.reason ? { reason: e.reason } : {}),
  };
  const next = [...prev];
  if (idx >= 0) {
    next[idx] = patch;
    return next;
  }
  next.splice(userIdx >= 0 ? userIdx + 1 : next.length, 0, patch);
  return next;
}
