import { describe, expect, test } from "vitest";
import type { TranscriptItem } from "./types";
import { insertNewThinkingBeforeStreamingAssistant } from "./transcriptThinkingPlacement";

describe("insertNewThinkingBeforeStreamingAssistant", () => {
  test("appends when no assistant row for this stream id yet", () => {
    const prev: TranscriptItem[] = [
      { id: "u1", type: "user_message", content: "hi" },
    ];
    const row: Extract<TranscriptItem, { type: "thinking" }> = {
      id: "r1",
      type: "thinking",
      status: "in_progress",
      content: "",
      startedAtMs: 0,
    };
    const got = insertNewThinkingBeforeStreamingAssistant(prev, "a1", row);
    expect(got.map((x) => x.id).join(",")).toBe("u1,r1");
  });

  test("inserts before streaming assistant so later reasoning stays above reply text", () => {
    const assistantId = "a-stream";
    const prev: TranscriptItem[] = [
      { id: "u1", type: "user_message", content: "hi" },
      {
        id: "r-done",
        type: "thinking",
        status: "completed",
        content: "first",
        durationMs: 10,
      },
      {
        id: assistantId,
        type: "assistant_message",
        content: "partial answer",
        streaming: true,
      },
    ];
    const row: Extract<TranscriptItem, { type: "thinking" }> = {
      id: "r2",
      type: "thinking",
      status: "in_progress",
      content: "",
      startedAtMs: 0,
    };
    const got = insertNewThinkingBeforeStreamingAssistant(
      prev,
      assistantId,
      row,
    );
    expect(got.map((x) => x.id).join(",")).toBe("u1,r-done,r2,a-stream");
  });
});
