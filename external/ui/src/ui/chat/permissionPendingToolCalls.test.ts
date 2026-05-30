import { expect, test } from "vitest";
import { permissionPendingToolCallIds } from "./permissionPendingToolCalls";
import type { TranscriptItem } from "./types";

const permissionRow = (
  toolCallId: string,
  resolved?: boolean,
): TranscriptItem => ({
  type: "permission_prompt",
  id: "pp_1",
  payload: {
    sessionId: "sess_x",
    toolCall: {
      toolCallId,
      title: "Run: run_command",
      kind: "run_command",
      content: [],
    },
    options: [],
  },
  ...(resolved ? { resolved: { optionId: "allow", summaryLine: "Allow" } } : {}),
});

test("collects unresolved permission tool call ids", () => {
  const ids = permissionPendingToolCallIds([
    { type: "tool_call", id: "tc_row", toolCallId: "call_a", status: "in_progress" },
    permissionRow("call_a"),
    permissionRow("call_b", true),
  ]);
  expect(ids.has("call_a")).toBe(true);
  expect(ids.has("call_b")).toBe(false);
});
