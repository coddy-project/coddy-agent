import { expect, test } from "vitest";
import {
  applyMemoryChunkToItems,
  applyMemoryPhaseToItems,
  freezeMemoryWallWhenThinkingAfterRecall,
  memoryTranscriptFromApi,
} from "./memoryTranscript";
import type { TranscriptItem } from "./types";

test("memoryTranscriptFromApi maps unified context to completed memory", () => {
  const it = memoryTranscriptFromApi({
    userTurnIndex: 2,
    memoryRowId: "row-1",
    memoryDurationMs: 1200,
    memoryContextText: "recalled context",
  });
  expect(it.type).toBe("memory_copilot");
  expect(it.memoryStatus).toBe("completed");
  expect(it.memoryText).toBe("recalled context");
  expect(it.memoryWallDurationMs).toBe(1200);
});

test("memoryTranscriptFromApi falls back to legacy recall+persist text and sums durations", () => {
  const it = memoryTranscriptFromApi({
    userTurnIndex: 0,
    recallText: "recall",
    persistJudgeText: "judge",
    recallDurationMs: 100,
    persistDurationMs: 50,
  });
  expect(it.memoryRowId).toBe("mem-0");
  expect(it.recallStatus).toBe("completed");
  expect(it.persistStatus).toBe("completed");
  expect(it.memoryWallDurationMs).toBe(150);
  expect(it.memoryStatus).toBeUndefined();
});

test("applyMemoryPhaseToItems inserts a row after the last user message on started", () => {
  const prev: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" } as TranscriptItem,
  ];
  const next = applyMemoryPhaseToItems(prev, {
    memoryRowId: "row-1",
    phase: "memory",
    status: "started",
    userTurnIndex: 0,
  });
  expect(next).toHaveLength(2);
  const mem = next[1];
  if (!mem || mem.type !== "memory_copilot") throw new Error("expected memory");
  expect(mem.memoryStatus).toBe("in_progress");
  expect(mem.recallStatus).toBe("in_progress");
  expect(typeof mem.memoryWallStartedAtMs).toBe("number");
});

test("applyMemoryPhaseToItems completes the row and records persist metadata", () => {
  const started = applyMemoryPhaseToItems([], {
    memoryRowId: "row-1",
    phase: "memory",
    status: "started",
  });
  const done = applyMemoryPhaseToItems(started, {
    memoryRowId: "row-1",
    phase: "memory",
    status: "completed",
    persistSaved: true,
    persistRelativePath: "notes/x.md",
    recallReadPaths: [" a.md ", ""],
  });
  const mem = done[0];
  if (!mem || mem.type !== "memory_copilot") throw new Error("expected memory");
  expect(mem.memoryStatus).toBe("completed");
  expect(mem.persistStatus).toBe("completed");
  expect(mem.persistRelativePath).toBe("notes/x.md");
  expect(mem.recallReadPaths).toEqual(["a.md"]);
  expect(mem.memoryWallDurationMs).toBeGreaterThanOrEqual(0);
});

test("applyMemoryChunkToItems appends non-reasoning deltas per phase and ignores unknown rows", () => {
  const base = applyMemoryPhaseToItems([], {
    memoryRowId: "row-1",
    phase: "recall",
    status: "started",
  });
  const withChunk = applyMemoryChunkToItems(base, {
    memoryRowId: "row-1",
    phase: "recall",
    kind: "text",
    delta: "abc",
  });
  const mem = withChunk[0];
  if (!mem || mem.type !== "memory_copilot") throw new Error("expected memory");
  expect(mem.recallText).toBe("abc");

  const reasoningIgnored = applyMemoryChunkToItems(withChunk, {
    memoryRowId: "row-1",
    phase: "recall",
    kind: "reasoning",
    delta: "zzz",
  });
  expect(reasoningIgnored).toStrictEqual(withChunk);

  const unknown = applyMemoryChunkToItems(withChunk, {
    memoryRowId: "nope",
    phase: "recall",
    kind: "text",
    delta: "x",
  });
  expect(unknown).toBe(withChunk);
});

test("freezeMemoryWallWhenThinkingAfterRecall caps the wall clock while memory is busy", () => {
  const startMs = 1_000;
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" } as TranscriptItem,
    {
      id: "m1",
      type: "memory_copilot",
      memoryRowId: "row-1",
      userTurnIndex: 0,
      memoryStatus: "in_progress",
      memoryText: "",
      recallStatus: "in_progress",
      persistStatus: "idle",
      recallText: "",
      recallReasoning: "",
      persistText: "",
      persistReasoning: "",
      memoryWallStartedAtMs: startMs,
    } as TranscriptItem,
    {
      id: "t1",
      type: "thinking",
      status: "in_progress",
      content: "",
    } as TranscriptItem,
  ];
  const next = freezeMemoryWallWhenThinkingAfterRecall(items, 4_500);
  const mem = next[1];
  if (!mem || mem.type !== "memory_copilot") throw new Error("expected memory");
  expect(mem.memoryWallLiveCapMs).toBe(3_500);
});

test("freezeMemoryWallWhenThinkingAfterRecall is a no-op without an in-progress thinking row", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" } as TranscriptItem,
  ];
  expect(freezeMemoryWallWhenThinkingAfterRecall(items, 5_000)).toBe(items);
});
