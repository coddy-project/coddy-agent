import { expect, test } from "vitest";

import {
  buildPermissionPayloadFromToolCall,
  restorePermissionPromptsForPendingTools,
} from "./restorePermissionPrompts";
import type { ToolsPermissionPolicy } from "./toolsPermissionPolicy";
import type { TranscriptItem } from "./types";

const policy: ToolsPermissionPolicy = {
  requirePermissionForCommands: true,
  requirePermissionForWrites: false,
  permissionMasterKey: false,
  commandAllowlist: [],
};

test("restorePermissionPromptsForPendingTools inserts after pending run_command", () => {
  const items: TranscriptItem[] = [
    {
      type: "tool_call",
      id: "tc_1",
      toolCallId: "call_a",
      title: "run_command",
      kind: "shell",
      status: "pending",
      argsText: '{"command":"ls -la"}',
    },
  ];
  const out = restorePermissionPromptsForPendingTools(items, "sess_x", policy);
  expect(out).toHaveLength(2);
  expect(out[1]?.type).toBe("permission_prompt");
  if (out[1]?.type === "permission_prompt") {
    expect(out[1].payload.toolCall.toolCallId).toBe("call_a");
    expect(out[1].payload.options).toHaveLength(3);
  }
});

test("buildPermissionPayloadFromToolCall uses Arguments body", () => {
  const p = buildPermissionPayloadFromToolCall("sess_x", {
    toolCallId: "call_a",
    title: "Run: run_command",
    kind: "shell",
    argsText: '{"command":"ls -la"}',
  });
  expect(p.toolCall.content?.[0]?.content?.text).toContain("Arguments:");
});
