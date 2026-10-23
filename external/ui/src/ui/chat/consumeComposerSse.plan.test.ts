import { afterEach, expect, test, vi } from "vitest";
import {
  consumeComposerSseReader,
  type ConsumeComposerSseParams,
} from "./consumeComposerSse";
import type { TodoPlanEntry } from "./todoToolPreview";

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

async function plansOf(sse: string): Promise<TodoPlanEntry[][]> {
  vi.stubGlobal("requestAnimationFrame", () => 0);
  const seen: TodoPlanEntry[][] = [];
  const params: ConsumeComposerSseParams = {
    reader: mockReader(sse),
    dec: new TextDecoder(),
    carry: { buf: "" },
    assistantId: "a-init",
    applyStreamItems: () => {},
    setTokenUsage: () => {},
    setContextUsage: () => {},
    tokenBaselineRef: { current: { input: 0, output: 0, total: 0 } },
    reasoningDurationMsByContentRef: { current: new Map() },
    newId: (p) => p,
    applyMemoryRunToItems: (prev) => prev,
    onTodoPlan: (entries) => seen.push(entries),
  };
  await consumeComposerSseReader(params);
  return seen;
}

const planFrame = (payload: object) =>
  `event: plan\ndata: ${JSON.stringify({ sessionUpdate: "plan", ...payload })}\n\n`;

test("a plan frame reaches the caller as the checklist, a design plan's frame does not", async () => {
  const seen = await plansOf(
    planFrame({
      entries: [
        { content: "Read the failing test", status: "completed" },
        { content: "Fix the parser", status: "in_progress", priority: "high" },
      ],
    }) +
      // plan_write publishes the todos of a design plan on the same event,
      // marked as one: they are not the session's checklist.
      planFrame({
        entries: [{ content: "Design the cache", status: "pending" }],
        _meta: {
          "coddy.dev/planSlug": "cache",
          "coddy.dev/planKind": "design",
        },
      }) +
      // An archived checklist arrives as an empty list.
      planFrame({ entries: [] }) +
      `data: [DONE]\n\n`,
  );
  expect(seen).toEqual([
    [
      { content: "Read the failing test", status: "completed" },
      { content: "Fix the parser", status: "in_progress" },
    ],
    [],
  ]);
});

test("a plan frame the checklist cannot be read from is dropped", async () => {
  const seen = await plansOf(
    planFrame({ entries: [{ content: "", status: "pending" }] }) +
      planFrame({ entries: [{ content: "Fix it", status: "blocked" }] }) +
      planFrame({ entries: "Fix it" }) +
      `event: plan\ndata: {not json}\n\n` +
      `data: [DONE]\n\n`,
  );
  expect(seen).toEqual([]);
});

test("a plan frame left in the buffer when the stream ends still arrives", async () => {
  // No blank line after the last frame: the reader reads it out of what the
  // stream left behind.
  const seen = await plansOf(
    `event: plan\ndata: ${JSON.stringify({
      sessionUpdate: "plan",
      entries: [{ content: "Ship it", status: "completed" }],
    })}`,
  );
  expect(seen).toEqual([[{ content: "Ship it", status: "completed" }]]);
});
