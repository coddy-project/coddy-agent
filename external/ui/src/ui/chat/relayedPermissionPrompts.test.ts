import { describe, expect, it } from "vitest";
import {
  isRelayedPermissionTitle,
  retireRelayedPermissionPrompts,
} from "./relayedPermissionPrompts";
import type { TranscriptItem } from "./types";

function prompt(
  id: string,
  title: string,
  resolved?: { optionId: string; summaryLine: string },
): TranscriptItem {
  return {
    id,
    type: "permission_prompt",
    payload: {
      sessionId: "sess_parent",
      toolCall: { toolCallId: `call_${id}`, title },
      options: [{ optionId: "allow", name: "Allow", kind: "allow_once" }],
    },
    ...(resolved ? { resolved } : {}),
  } as TranscriptItem;
}

describe("relayed permission prompts", () => {
  it("recognises the relay's title prefix", () => {
    expect(isRelayedPermissionTitle("[subagent writer] Run: echo")).toBe(true);
    expect(isRelayedPermissionTitle("  [subagent writer] Run: echo")).toBe(true);
    expect(isRelayedPermissionTitle("Run: echo")).toBe(false);
    expect(isRelayedPermissionTitle(undefined)).toBe(false);
  });

  it("retires only the unresolved prompts relayed for a subagent", () => {
    const own = prompt("own", "Run: run_command");
    const relayed = prompt("relayed", "[subagent writer] Run: run_command");
    const answered = prompt("answered", "[subagent writer] Run: ls", {
      optionId: "allow",
      summaryLine: "Allowed",
    });
    const text: TranscriptItem = {
      id: "a1",
      type: "assistant_message",
      content: "done",
    } as TranscriptItem;
    const out = retireRelayedPermissionPrompts([own, relayed, answered, text]);
    expect(out.map((it) => it.id)).toEqual(["own", "answered", "a1"]);
  });

  it("returns the same array when nothing is relayed", () => {
    const items = [prompt("own", "Run: run_command")];
    expect(retireRelayedPermissionPrompts(items)).toBe(items);
  });
});
