import { stableMemoryCopilotItemId } from "./memoryStableId";
import type { TranscriptItem } from "./types";

export type MemoryPhaseEvt = {
  memoryRowId: string;
  phase: string;
  status: string;
  userTurnIndex?: number;
  durationMs?: number;
  persistSaved?: boolean;
  persistRelativePath?: string;
  persistTitle?: string;
  persistSavedBody?: string;
  recallReadPaths?: string[];
};

export type MemoryChunkEvt = {
  memoryRowId: string;
  phase: string;
  kind: string;
  delta: string;
};

export type MemoryTurnApi = {
  userTurnIndex: number;
  memoryRowId?: string;
  memoryMode?: string;
  memoryDurationMs?: number;
  memoryContextText?: string;
  recallSkipped?: boolean;
  recallText?: string;
  recallReasoningText?: string;
  recallDurationMs?: number;
  persistJudgeText?: string;
  persistDurationMs?: number;
  persistSaved?: boolean;
  persistRelativePath?: string;
  persistTitle?: string;
  persistSavedBody?: string;
  recallReadPaths?: string[];
};

export function memoryTranscriptFromApi(
  row: MemoryTurnApi,
): Extract<TranscriptItem, { type: "memory_copilot" }> {
  const rowId = (row.memoryRowId || "").trim() || `mem-${row.userTurnIndex}`;
  const unifiedCtx = (row.memoryContextText || "").trim();
  const rt = (row.recallText || "").trim();
  const rr = (row.recallReasoningText || "").trim();
  const paths = Array.isArray(row.recallReadPaths)
    ? row.recallReadPaths.filter(
        (x) => typeof x === "string" && x.trim() !== "",
      )
    : [];
  const hasRecallTrail = !!(
    row.recallDurationMs ||
    rt ||
    rr ||
    paths.length > 0
  );
  const pt = (row.persistJudgeText || "").trim();
  const hasPersistTrail = !!(row.persistDurationMs || pt || row.persistSaved);
  const hasUnified = !!(row.memoryDurationMs || unifiedCtx);
  const sumMs =
    typeof row.memoryDurationMs === "number" && row.memoryDurationMs > 0
      ? row.memoryDurationMs
      : (row.recallDurationMs ?? 0) + (row.persistDurationMs ?? 0);
  const legacyCombined = [row.recallText, row.persistJudgeText]
    .filter((x) => typeof x === "string" && x.trim() !== "")
    .join("\n\n");
  const memoryTextOut = unifiedCtx || legacyCombined;
  return {
    id: stableMemoryCopilotItemId(rowId, row.userTurnIndex),
    type: "memory_copilot",
    memoryRowId: rowId,
    userTurnIndex: row.userTurnIndex,
    ...(hasUnified
      ? { memoryStatus: "completed" as const, memoryText: memoryTextOut }
      : {}),
    recallStatus: hasRecallTrail ? "completed" : "idle",
    persistStatus: hasPersistTrail ? "completed" : "idle",
    recallText: row.recallText || "",
    recallReasoning: row.recallReasoningText || "",
    persistText: row.persistJudgeText || "",
    persistReasoning: "",
    ...(typeof row.recallDurationMs === "number"
      ? { recallDurationMs: row.recallDurationMs }
      : {}),
    ...(typeof row.persistDurationMs === "number"
      ? { persistDurationMs: row.persistDurationMs }
      : {}),
    ...(sumMs > 0 ? { memoryWallDurationMs: sumMs } : {}),
    ...(typeof row.persistSaved === "boolean"
      ? { persistSaved: row.persistSaved }
      : {}),
    ...(row.persistRelativePath
      ? { persistRelativePath: row.persistRelativePath }
      : {}),
    ...(row.persistTitle ? { persistTitle: row.persistTitle } : {}),
    ...(row.persistSavedBody ? { persistSavedBody: row.persistSavedBody } : {}),
    ...(paths.length > 0 ? { recallReadPaths: paths } : {}),
  };
}

export function applyMemoryPhaseToItems(
  prev: TranscriptItem[],
  p: MemoryPhaseEvt,
): TranscriptItem[] {
  const now = Date.now();
  let idx = prev.findIndex(
    (x) => x.type === "memory_copilot" && x.memoryRowId === p.memoryRowId,
  );
  const next = [...prev];
  let uidx = -1;
  for (let i = prev.length - 1; i >= 0; i--) {
    const it = prev[i];
    if (it && it.type === "user_message") {
      uidx = i;
      break;
    }
  }
  const insertAt = uidx >= 0 ? uidx + 1 : next.length;

  const baseMemory = (): Extract<
    TranscriptItem,
    { type: "memory_copilot" }
  > => ({
    id: stableMemoryCopilotItemId(
      p.memoryRowId,
      typeof p.userTurnIndex === "number" ? p.userTurnIndex : 0,
    ),
    type: "memory_copilot",
    memoryRowId: p.memoryRowId,
    userTurnIndex: typeof p.userTurnIndex === "number" ? p.userTurnIndex : 0,
    memoryStatus: "idle",
    memoryText: "",
    recallStatus: "idle",
    persistStatus: "idle",
    recallText: "",
    recallReasoning: "",
    persistText: "",
    persistReasoning: "",
  });

  if (idx < 0) {
    next.splice(insertAt, 0, baseMemory());
    idx = insertAt;
  }

  const cur = next[idx];
  if (!cur || cur.type !== "memory_copilot") {
    return prev;
  }

  let patch: Extract<TranscriptItem, { type: "memory_copilot" }> = { ...cur };
  const st = (p.status || "").trim();

  if (p.phase === "memory") {
    if (st === "started") {
      patch.memoryStatus = "in_progress";
      patch.recallStatus = "in_progress";
      patch.persistStatus = "idle";
      if (patch.memoryWallStartedAtMs == null)
        patch.memoryWallStartedAtMs = now;
    }
    if (st === "completed") {
      patch.memoryStatus = "completed";
      patch.recallStatus = "completed";
      patch.persistStatus = p.persistSaved ? "completed" : "idle";
      const rp = p.recallReadPaths;
      if (Array.isArray(rp) && rp.length > 0) {
        const cleaned = rp.map((x) => String(x).trim()).filter((x) => x !== "");
        if (cleaned.length > 0) patch.recallReadPaths = cleaned;
      }
      if (typeof p.persistSaved === "boolean") {
        patch.persistSaved = p.persistSaved;
      }
      const pr = (p.persistRelativePath || "").trim();
      if (pr) patch.persistRelativePath = pr;
      const tt = (p.persistTitle || "").trim();
      if (tt) patch.persistTitle = tt;
      const pb = (p.persistSavedBody || "").trim();
      if (pb) patch.persistSavedBody = pb;
      if (typeof patch.memoryWallStartedAtMs === "number") {
        patch.memoryWallDurationMs = Math.max(
          0,
          now - patch.memoryWallStartedAtMs,
        );
      }
    }
  }
  if (p.phase === "recall") {
    if (st === "started") {
      patch.recallStatus = "in_progress";
      if (patch.memoryWallStartedAtMs == null)
        patch.memoryWallStartedAtMs = now;
    }
    if (st === "completed") {
      patch.recallStatus = "completed";
      if (typeof p.durationMs === "number" && p.durationMs > 0)
        patch.recallDurationMs = p.durationMs;
      const rp = p.recallReadPaths;
      if (Array.isArray(rp) && rp.length > 0) {
        const cleaned = rp.map((x) => String(x).trim()).filter((x) => x !== "");
        if (cleaned.length > 0) patch.recallReadPaths = cleaned;
      }
    }
  }
  if (p.phase === "persist") {
    if (st === "started") {
      patch.persistStatus = "in_progress";
      if (patch.memoryWallStartedAtMs == null)
        patch.memoryWallStartedAtMs = now;
      const wallStart = patch.memoryWallStartedAtMs;
      const wallElapsed =
        typeof wallStart === "number" ? Math.max(0, now - wallStart) : 0;
      if (
        typeof patch.memoryWallLiveCapMs === "number" &&
        Number.isFinite(patch.memoryWallLiveCapMs)
      ) {
        patch.memoryWallLiveCapMs = Math.max(
          patch.memoryWallLiveCapMs,
          wallElapsed,
        );
      } else {
        patch.memoryWallLiveCapMs = wallElapsed;
      }
    }
    if (st === "completed") {
      patch.persistStatus = "completed";
      if (typeof p.durationMs === "number" && p.durationMs > 0)
        patch.persistDurationMs = p.durationMs;
      if (typeof p.persistSaved === "boolean") {
        patch.persistSaved = p.persistSaved;
      }
      const pr = (p.persistRelativePath || "").trim();
      if (pr) patch.persistRelativePath = pr;
      const tt = (p.persistTitle || "").trim();
      if (tt) patch.persistTitle = tt;
      const pb = (p.persistSavedBody || "").trim();
      if (pb) patch.persistSavedBody = pb;
      if (typeof patch.memoryWallStartedAtMs === "number") {
        patch.memoryWallDurationMs = Math.max(
          0,
          now - patch.memoryWallStartedAtMs,
        );
      }
    }
  }

  next[idx] = patch;
  return next;
}

export function applyMemoryChunkToItems(
  prev: TranscriptItem[],
  c: MemoryChunkEvt,
): TranscriptItem[] {
  const idx = prev.findIndex(
    (x) => x.type === "memory_copilot" && x.memoryRowId === c.memoryRowId,
  );
  if (idx < 0) return prev;
  const cur = prev[idx];
  if (!cur || cur.type !== "memory_copilot") return prev;
  const next = [...prev];
  const patch: Extract<TranscriptItem, { type: "memory_copilot" }> = { ...cur };
  const ph = (c.phase || "").trim();
  const kd = (c.kind || "").trim();
  const d = typeof c.delta === "string" ? c.delta : "";
  if (!d) return prev;
  if (ph === "memory") {
    if (kd !== "reasoning") patch.memoryText = (patch.memoryText || "") + d;
  } else if (ph === "recall") {
    if (kd !== "reasoning") patch.recallText += d;
  } else if (ph === "persist") {
    if (kd !== "reasoning") patch.persistText += d;
  } else {
    return prev;
  }
  next[idx] = patch;
  return next;
}

/** Freeze the memory wall-clock label once main-model reasoning starts while recall/persist are still SSE-busy (events can arrive after reasoning deltas). */
export function freezeMemoryWallWhenThinkingAfterRecall(
  items: TranscriptItem[],
  freezeAtMs: number,
): TranscriptItem[] {
  let userIdx = -1;
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (!it) continue;
    if (it && it.type === "user_message") {
      userIdx = i;
      break;
    }
  }
  if (userIdx < 0) return items;

  let memIdx = -1;
  let thinkingIdx = -1;
  for (let i = userIdx + 1; i < items.length; i++) {
    const it = items[i];
    if (!it) continue;
    if (it.type === "user_message") break;
    if (it.type === "memory_copilot") memIdx = i;
    if (it.type === "thinking" && it.status === "in_progress") {
      thinkingIdx = i;
      break;
    }
  }
  if (memIdx < 0 || thinkingIdx < 0) return items;

  const m = items[memIdx];
  if (!m || m.type !== "memory_copilot") return items;

  const memBusy =
    m.memoryStatus === "in_progress" ||
    m.recallStatus === "in_progress" ||
    m.persistStatus === "in_progress";
  if (!memBusy || typeof m.memoryWallLiveCapMs === "number") return items;

  const startMs = m.memoryWallStartedAtMs;
  if (typeof startMs !== "number") return items;

  const cap = Math.max(0, freezeAtMs - startMs);
  const next = [...items];
  next[memIdx] = { ...m, memoryWallLiveCapMs: cap };
  return next;
}
