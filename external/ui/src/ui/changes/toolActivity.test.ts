import { expect, test } from "vitest";
import { finishedToolCalls } from "./toolActivity";
import type { TranscriptItem } from "../chat/types";

function call(id: string, status: string): TranscriptItem {
  return { id, type: "tool_call", toolCallId: id, status } as unknown as TranscriptItem;
}

test("only finished tool calls count, whatever way they ended", () => {
  const items: TranscriptItem[] = [
    call("a", "pending"),
    call("b", "in_progress"),
    call("c", "completed"),
    call("d", "failed"),
    call("e", "cancelled"),
    { id: "m", type: "assistant_message", content: "done" } as unknown as TranscriptItem,
  ];
  expect(finishedToolCalls(items)).toBe(3);
  expect(finishedToolCalls([])).toBe(0);
});
