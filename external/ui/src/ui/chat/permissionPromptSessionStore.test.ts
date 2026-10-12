import { afterEach, expect, test } from "vitest";

import {
  dropSettledPermissionPrompts,
  loadPermissionPromptRecords,
  mergePermissionPromptsIntoTranscript,
  permissionPendingSessionIdsFromStorage,
  upsertPermissionPromptRecord,
} from "./permissionPromptSessionStore";
import type { CoddyPermissionPayload } from "./permissionTypes";
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

const payload = (toolCallId: string): CoddyPermissionPayload => ({
  sessionId: SID,
  toolCall: { toolCallId, title: "Run: run_command", kind: "shell" },
  options: [
    { optionId: "allow", name: "Allow", kind: "allow_once" },
    { optionId: "reject", name: "Reject", kind: "reject_once" },
  ],
});

// A turn stopped while its prompt was open records the call as refused; the
// prompt was settled without the reader and must not wait for an answer.
test("an unanswered prompt whose call has a result is dropped with its record", () => {
  upsertPermissionPromptRecord(SID, {
    toolCallId: "call_stopped",
    payload: payload("call_stopped"),
  });
  upsertPermissionPromptRecord(SID, {
    toolCallId: "call_open",
    payload: payload("call_open"),
  });
  const items: TranscriptItem[] = [
    {
      type: "tool_call",
      id: "tc_1",
      toolCallId: "call_stopped",
      status: "in_progress",
      resultText: "permission denied by user",
    },
    {
      type: "permission_prompt",
      id: "pp_1",
      payload: payload("call_stopped"),
    },
    {
      type: "tool_call",
      id: "tc_2",
      toolCallId: "call_open",
      status: "pending",
    },
    { type: "permission_prompt", id: "pp_2", payload: payload("call_open") },
  ];
  const out = dropSettledPermissionPrompts(items, SID);
  expect(out.map((x) => x.id)).toEqual(["tc_1", "tc_2", "pp_2"]);
  expect(loadPermissionPromptRecords(SID).map((r) => r.toolCallId)).toEqual([
    "call_open",
  ]);
});

test("an answered prompt stays, and nothing changes without a settled call", () => {
  const items: TranscriptItem[] = [
    {
      type: "tool_call",
      id: "tc_1",
      toolCallId: "call_done",
      status: "completed",
      resultText: "ok",
    },
    {
      type: "permission_prompt",
      id: "pp_1",
      payload: payload("call_done"),
      resolved: { optionId: "allow", summaryLine: "Allowed" },
    },
  ];
  expect(dropSettledPermissionPrompts(items, SID)).toBe(items);
  const open: TranscriptItem[] = [
    {
      type: "tool_call",
      id: "tc_2",
      toolCallId: "call_open",
      status: "pending",
    },
    { type: "permission_prompt", id: "pp_2", payload: payload("call_open") },
  ];
  expect(dropSettledPermissionPrompts(open, SID)).toBe(open);
});

test("a stored prompt of a settled call no longer marks the session after a reload", () => {
  upsertPermissionPromptRecord(SID, {
    toolCallId: "call_stopped",
    payload: payload("call_stopped"),
  });
  expect(permissionPendingSessionIdsFromStorage().has(SID)).toBe(true);
  const out = mergePermissionPromptsIntoTranscript(
    [
      {
        type: "tool_call",
        id: "tc_1",
        toolCallId: "call_stopped",
        status: "in_progress",
        resultText: "permission denied by user",
      },
    ],
    SID,
    {
      requirePermissionForCommands: true,
      requirePermissionForWrites: false,
      permissionMasterKey: false,
      commandAllowlist: [],
    },
  );
  expect(out.some((x) => x.type === "permission_prompt")).toBe(false);
  expect(permissionPendingSessionIdsFromStorage().has(SID)).toBe(false);
});
