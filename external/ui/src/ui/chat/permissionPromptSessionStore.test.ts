import { afterEach, expect, test } from "vitest";

import {
  loadPermissionPromptRecords,
  mergePermissionPromptsIntoTranscript,
  upsertPermissionPromptRecord,
} from "./permissionPromptSessionStore";
import type { TranscriptItem } from "./types";

const SID = "sess_perm_store_test";

afterEach(() => {
  window.localStorage.removeItem(`coddy_pp_v1:${SID}`);
});

test("mergePermissionPromptsIntoTranscript restores from localStorage", () => {
  upsertPermissionPromptRecord(SID, {
    toolCallId: "call_store",
    payload: {
      sessionId: SID,
      toolCall: {
        toolCallId: "call_store",
        title: "Run: run_command",
        kind: "shell",
        content: [
          {
            type: "content",
            content: { type: "text", text: 'Arguments: {"command":"ls -la"}' },
          },
        ],
      },
      options: [
        { optionId: "allow", name: "Allow", kind: "allow_once" },
        { optionId: "reject", name: "Reject", kind: "reject_once" },
      ],
    },
  });
  const merged: TranscriptItem[] = [
    {
      type: "tool_call",
      id: "tc_1",
      toolCallId: "call_store",
      status: "pending",
      title: "run_command",
    },
  ];
  const out = mergePermissionPromptsIntoTranscript(merged, SID, {
    requirePermissionForCommands: true,
    requirePermissionForWrites: false,
    permissionMasterKey: false,
    commandAllowlist: [],
  });
  expect(out.some((x) => x.type === "permission_prompt")).toBe(true);
  expect(loadPermissionPromptRecords(SID)).toHaveLength(1);
});
