import { describe, expect, it } from "vitest";
import { applyMemoryRunToItems } from "./memoryRun";
import type { TranscriptItem } from "./types";

const user = (id = "u1"): TranscriptItem => ({
  id,
  type: "user_message",
  content: "what did we decide?",
});

describe("applyMemoryRunToItems", () => {
  it("places the run after the turn's user message and patches it when it settles", () => {
    const started = applyMemoryRunToItems([user()], {
      status: "started",
      taskId: "bg_3",
      childSessionId: "sess_child",
    });
    expect(started).toHaveLength(2);
    expect(started[1]).toMatchObject({
      type: "memory_run",
      status: "started",
      taskId: "bg_3",
      childSessionId: "sess_child",
    });
    const finished = applyMemoryRunToItems(started, {
      status: "finished",
      taskId: "bg_3",
      taskStatus: "succeeded",
      durationMs: 3210,
      delivered: true,
    });
    expect(finished).toHaveLength(2);
    expect(finished[1]).toMatchObject({
      type: "memory_run",
      status: "finished",
      taskStatus: "succeeded",
      durationMs: 3210,
      delivered: true,
    });
  });

  it("belongs to the current turn only", () => {
    const items = applyMemoryRunToItems([user("u1")], {
      status: "started",
      taskId: "bg_1",
    });
    const next = applyMemoryRunToItems([...items, user("u2")], {
      status: "started",
      taskId: "bg_2",
    });
    expect(next.map((it) => it.type)).toEqual([
      "user_message",
      "memory_run",
      "user_message",
      "memory_run",
    ]);
  });

  it("records a skip and ignores unknown statuses", () => {
    const skipped = applyMemoryRunToItems([user()], {
      status: "skipped",
      reason: "memory runs in flight for this session: 2 of 2",
    });
    expect(skipped[1]).toMatchObject({ type: "memory_run", status: "skipped" });
    expect(applyMemoryRunToItems([user()], { status: "weird" })).toHaveLength(1);
  });
});
