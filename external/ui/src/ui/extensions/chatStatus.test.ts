import { describe, expect, test } from "vitest";
import { snapshotChatStatus } from "./chatBridge";
import type { TranscriptItem } from "../chat/types";
import type { TurnProgress } from "../chat/turnProgress";

const prompt: TranscriptItem = {
  id: "u1",
  type: "user_message",
  content: "Write a poem",
  createdAtUtc: "2026-01-01T00:00:00Z",
};
const progress: TurnProgress = {
  startedAtMs: 1000,
  outputTokens: 42,
  estimated: false,
  tokensPerSecond: 12.5,
  phase: "preparing",
};
const streamingProgress: TurnProgress = {
  startedAtMs: 1000,
  outputTokens: 42,
  estimated: false,
  tokensPerSecond: 12.5,
};

function status(
  turnActive: boolean,
  items: TranscriptItem[],
  current: TurnProgress | null = progress,
) {
  return snapshotChatStatus({
    sessionId: "s1",
    turnActive,
    items,
    progress: current,
    tokenUsage: { inputTokens: 10, outputTokens: 42, totalTokens: 52 },
    permissionPending: false,
    questionPending: false,
  });
}

describe("the parsed chat status", () => {
  test("an idle chat exposes no running progress", () => {
    expect(status(false, [prompt])).toMatchObject({
      activity: "idle",
      progress: null,
    });
  });

  test("preparing, writing and the generation rate come from host state", () => {
    expect(status(true, [prompt])).toMatchObject({
      activity: "preparing",
      progress: {
        startedAtMs: 1000,
        outputTokens: 42,
        estimated: false,
        tokensPerSecond: 12.5,
      },
    });
    const answer: TranscriptItem = {
      id: "a1",
      type: "assistant_message",
      content: "A poem",
      streaming: true,
    };
    expect(status(true, [prompt, answer], streamingProgress)).toMatchObject({
      activity: "writing",
    });
  });

  test("an unresolved operator gate is a parsed activity too", () => {
    const gate = {
      id: "p1",
      type: "permission_prompt",
      payload: {},
    } as unknown as TranscriptItem;
    expect(status(true, [prompt, gate], streamingProgress)).toMatchObject({
      activity: "permission",
    });
  });
});
