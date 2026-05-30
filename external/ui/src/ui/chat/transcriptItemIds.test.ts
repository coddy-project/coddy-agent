import { expect, test } from "vitest";
import { preserveTranscriptItemIds, stableToolCallItemId } from "./transcriptItemIds";
import type { TranscriptItem } from "./types";

test("stableToolCallItemId", () => {
  expect(stableToolCallItemId("abc")).toBe("tc_abc");
});

test("preserveTranscriptItemIds reuses id for matching tool_call", () => {
  const prev: TranscriptItem[] = [
    {
      id: "tc_keep",
      type: "tool_call",
      toolCallId: "call_1",
      status: "completed",
      resultText: "ok",
    },
  ];
  const merged: TranscriptItem[] = [
    {
      id: "tc_new",
      type: "tool_call",
      toolCallId: "call_1",
      status: "completed",
      resultText: "ok",
    },
  ];
  const out = preserveTranscriptItemIds(merged, prev);
  expect(out[0]?.id).toBe("tc_keep");
});
