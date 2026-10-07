import { afterEach, expect, test, vi } from "vitest";
import {
  consumeComposerSseReader,
  type ConsumeComposerSseParams,
} from "./consumeComposerSse";
import type { SessionGoalUpdate } from "./goal";
import type { TranscriptItem } from "./types";
import { userMsgIndices } from "../messages/userMsgIndices";

afterEach(() => vi.unstubAllGlobals());

function mockReader(text: string): ReadableStreamDefaultReader<Uint8Array> {
  const chunks = [new TextEncoder().encode(text)];
  let i = 0;
  return {
    read: async () =>
      i < chunks.length
        ? { done: false, value: chunks[i++]! }
        : { done: true, value: undefined },
    cancel: async () => {},
    releaseLock: () => {},
    closed: Promise.resolve(undefined),
  } as unknown as ReadableStreamDefaultReader<Uint8Array>;
}

function textEvent(content: string): string {
  return `data: ${JSON.stringify({ choices: [{ delta: { content } }] })}\n\n`;
}

function frame(name: string, data: unknown): string {
  return `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`;
}

async function drive(
  initial: TranscriptItem[],
  sse: string,
  extra: Partial<ConsumeComposerSseParams> = {},
): Promise<TranscriptItem[]> {
  vi.stubGlobal("requestAnimationFrame", () => 0);
  const items: TranscriptItem[] = [...initial];
  let idc = 0;
  const params: ConsumeComposerSseParams = {
    reader: mockReader(sse),
    dec: new TextDecoder(),
    carry: { buf: "" },
    assistantId: "a-init",
    applyStreamItems: (fn) => {
      const next = fn(items.slice());
      items.length = 0;
      items.push(...next);
    },
    setTokenUsage: () => {},
    setContextUsage: () => {},
    tokenBaselineRef: { current: { input: 0, output: 0, total: 0 } },
    reasoningDurationMsByContentRef: { current: new Map() },
    newId: (p) => `${p}-${idc++}`,
    applyMemoryRunToItems: (prev) => prev,
    ...extra,
  };
  const res = await consumeComposerSseReader(params);
  res.flushToolQueue();
  return items;
}

const OBJECTIVE = "Make the tests pass";

test("a /goal stream: the kickoff row replaces the prompt, each continuation opens a turn of its own", async () => {
  const sent: TranscriptItem = {
    id: "u-sent",
    type: "user_message",
    content: `/goal ${OBJECTIVE}`,
    createdAtUtc: "2026-10-07T10:00:00Z",
  };
  const goals: SessionGoalUpdate[] = [];
  const items = await drive(
    [{ id: "u0", type: "user_message", content: "hello" }, sent],
    frame("session_goal", {
      sessionUpdate: "session_goal",
      sessionId: "sess_1",
      goal: { objective: OBJECTIVE, status: "active" },
      version: 3,
      notice: `Goal set: ${OBJECTIVE}`,
    }) +
      frame("goal_turn", {
        sessionUpdate: "goal_turn",
        kind: "kickoff",
        limit: 10,
        objective: OBJECTIVE,
      }) +
      frame("tool_call", {
        toolCallId: "c1",
        title: "run_command",
        status: "pending",
      }) +
      textEvent("Two tests still fail.") +
      frame("goal_turn", {
        sessionUpdate: "goal_turn",
        kind: "continue",
        index: 1,
        limit: 10,
        objective: OBJECTIVE,
        reason: "two tests still fail",
        remaining: ["fix the parser"],
      }) +
      textEvent("All green now.") +
      `data: [DONE]\n\n`,
    { onSessionGoal: (u) => goals.push(u) },
  );
  expect(items.map((it) => it.type)).toEqual([
    "user_message",
    "goal_turn",
    "tool_call",
    "assistant_message",
    "goal_turn",
    "assistant_message",
  ]);
  // No user bubble names the /goal prompt: the kickoff row stands in it.
  expect(
    items.filter((it) => it.type === "user_message").map((it) => it.id),
  ).toEqual(["u0"]);
  const kickoff = items[1] as Extract<TranscriptItem, { type: "goal_turn" }>;
  expect(kickoff.turn.kind).toBe("kickoff");
  expect(kickoff.createdAtUtc).toBe("2026-10-07T10:00:00Z");
  const cont = items[4] as Extract<TranscriptItem, { type: "goal_turn" }>;
  expect(cont.turn).toMatchObject({ kind: "continue", index: 1 });
  // Text after a goal row opens a new answer below it.
  expect(items[3]!.id).not.toBe(items[5]!.id);
  // Neither goal row is an editable prompt, and both count as turns.
  const idx = userMsgIndices(items);
  expect([...idx.keys()]).toEqual(["u0"]);
  expect(goals).toHaveLength(1);
  expect(goals[0]!.version).toBe(3);
  expect(goals[0]!.goal?.objective).toBe(OBJECTIVE);
});

test("a goal frame that is not one adds nothing", async () => {
  const goals: SessionGoalUpdate[] = [];
  const items = await drive(
    [],
    frame("goal_turn", { sessionUpdate: "goal_turn", objective: "x" }) +
      `event: session_goal\ndata: not json\n\n` +
      `data: [DONE]\n\n`,
    { onSessionGoal: (u) => goals.push(u) },
  );
  expect(items).toEqual([]);
  expect(goals).toEqual([]);
});
