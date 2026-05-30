import { expect, test } from "vitest";
import {
  permissionPromptDetail,
  permissionPromptTitle,
} from "./permissionPromptDisplay";
import type { CoddyPermissionPayload } from "./permissionTypes";

const basePayload = (): CoddyPermissionPayload => ({
  sessionId: "sess_x",
  toolCall: {
    toolCallId: "call_1",
    title: "Run: run_command",
    kind: "run_command",
    content: [
      {
        type: "content",
        content: {
          type: "text",
          text: 'Arguments: {"command":"ls -la"}',
        },
      },
    ],
  },
  options: [
    { optionId: "allow", name: "Allow", kind: "allow_once" },
    { optionId: "reject", name: "Reject", kind: "reject_once" },
  ],
});

test("permissionPromptTitle humanizes run_command", () => {
  expect(permissionPromptTitle(basePayload())).toBe("Run Command");
});

test("permissionPromptDetail extracts command from Arguments JSON", () => {
  expect(permissionPromptDetail(basePayload())).toBe("ls -la");
});

test("permissionPromptDetail handles Execute prefix", () => {
  const p = basePayload();
  p.toolCall.content = [
    {
      type: "content",
      content: { type: "text", text: "Execute: echo hi" },
    },
  ];
  expect(permissionPromptDetail(p)).toBe("echo hi");
});
