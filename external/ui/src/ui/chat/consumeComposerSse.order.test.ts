import { afterEach, expect, test, vi } from "vitest";
import {
  consumeComposerSseReader,
  type ConsumeComposerSseParams,
} from "./consumeComposerSse";
import type { TranscriptItem } from "./types";

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

async function drive(sse: string): Promise<TranscriptItem[]> {
  // rAF-batched tool flushes are drained synchronously by the text handler; make
  // requestAnimationFrame a no-op so the test does not depend on frame timing.
  vi.stubGlobal("requestAnimationFrame", () => 0);
  const items: TranscriptItem[] = [];
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
    applyMemoryPhaseToItems: (prev) => prev,
    applyMemoryChunkToItems: (prev) => prev,
  };
  const res = await consumeComposerSseReader(params);
  res.flushToolQueue();
  return items;
}

test("usage_update replaces the displayed current context after compaction", async () => {
  vi.stubGlobal("requestAnimationFrame", () => 0);
  const updates: Array<{ used: number; size: number }> = [];
  const params: ConsumeComposerSseParams = {
    reader: mockReader(
      `event: usage_update\ndata: ${JSON.stringify({ sessionUpdate: "usage_update", used: 42000, size: 128000 })}\n\n` +
        `data: [DONE]\n\n`,
    ),
    dec: new TextDecoder(),
    carry: { buf: "" },
    assistantId: "a-init",
    applyStreamItems: () => {},
    setTokenUsage: () => {},
    setContextUsage: (u) => updates.push(u),
    tokenBaselineRef: { current: { input: 0, output: 0, total: 0 } },
    reasoningDurationMsByContentRef: { current: new Map() },
    newId: (p) => p,
    applyMemoryPhaseToItems: (prev) => prev,
    applyMemoryChunkToItems: (prev) => prev,
  };

  await consumeComposerSseReader(params);

  expect(updates).toEqual([{ used: 42000, size: 128000 }]);
});

// Regression for the streaming-order bug: while a turn streams, text emitted
// AFTER a tool call must render BELOW that tool (interleaved in arrival order),
// not collapse into a single bubble pinned above all tools.
test("streaming interleaves text and tool calls in arrival order", async () => {
  const sse =
    textEvent("Reading files. ") +
    `event: tool_call\ndata: ${JSON.stringify({ toolCallId: "tc1", title: "read", kind: "read", status: "pending" })}\n\n` +
    `event: tool_call_update\ndata: ${JSON.stringify({ toolCallId: "tc1", status: "completed", content: [{ content: { text: "ok" } }] })}\n\n` +
    textEvent("All good.") +
    `data: [DONE]\n\n`;

  const items = await drive(sse);

  const shape = items.map((it) =>
    it.type === "assistant_message"
      ? `text:${it.content}`
      : it.type === "tool_call"
        ? `tool:${it.toolCallId}`
        : it.type,
  );
  expect(shape).toEqual(["text:Reading files. ", "tool:tc1", "text:All good."]);
});

// Two tool calls with text before, between, and after must all interleave.
test("streaming interleaves across multiple tool calls", async () => {
  const tool = (id: string) =>
    `event: tool_call\ndata: ${JSON.stringify({ toolCallId: id, title: "run", kind: "run", status: "pending" })}\n\n`;
  const sse =
    textEvent("first ") +
    tool("t1") +
    textEvent("second ") +
    tool("t2") +
    textEvent("third") +
    `data: [DONE]\n\n`;

  const items = await drive(sse);
  const shape = items.map((it) =>
    it.type === "assistant_message"
      ? `text:${it.content}`
      : it.type === "tool_call"
        ? `tool:${it.toolCallId}`
        : it.type,
  );
  expect(shape).toEqual([
    "text:first ",
    "tool:t1",
    "text:second ",
    "tool:t2",
    "text:third",
  ]);
});
