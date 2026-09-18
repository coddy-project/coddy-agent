import { afterEach, expect, test, vi } from "vitest";
import {
  consumeComposerSseReader,
  minMeasurableThinkingMs,
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
    applyMemoryRunToItems: (prev) => prev,
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
    applyMemoryRunToItems: (prev) => prev,
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

test("completed todo calls keep the plan snapshot sent with their status update", async () => {
  const todoPlan = [
    { content: "Inspect existing cards", status: "completed" },
    { content: "Render the preview", status: "in_progress" },
  ];
  const sse =
    `event: tool_call\ndata: ${JSON.stringify({ toolCallId: "todo-1", title: "coddy_todo_item_update", kind: "todo", status: "pending" })}\n\n` +
    `event: tool_call_update\ndata: ${JSON.stringify({ toolCallId: "todo-1", status: "completed", content: [{ content: { text: "updated item 1" } }], _meta: { coddy: { todoPlan } } })}\n\n` +
    `data: [DONE]\n\n`;

  const items = await drive(sse);
  const call = items.find(
    (item): item is Extract<TranscriptItem, { type: "tool_call" }> =>
      item.type === "tool_call" && item.toolCallId === "todo-1",
  );

  expect(call?.todoPlan).toEqual(todoPlan);
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

// A model that calls several tools in one answer puts whitespace between the calls
// (the hermes parser behind vLLM passes "\n\n" through as content). Each run used
// to open an assistant segment of its own: an empty, zero-height row that still
// took the column's gap, so the live transcript spread its tool rows apart until a
// reload rebuilt it from the persisted messages, where the whitespace is one tail.
test("whitespace between tool calls opens no empty assistant segment", async () => {
  const tool = (id: string) =>
    `event: tool_call\ndata: ${JSON.stringify({ toolCallId: id, title: "webfetch", kind: "fetch", status: "pending" })}\n\n`;
  const sse =
    textEvent("Checking the shops.") +
    tool("t1") +
    textEvent("\n\n") +
    tool("t2") +
    textEvent("\n\n") +
    tool("t3") +
    textEvent("Found it.") +
    `data: [DONE]\n\n`;

  const items = await drive(sse);
  const shape = items.map((it) =>
    it.type === "assistant_message"
      ? `text:${it.content.trim()}`
      : it.type === "tool_call"
        ? `tool:${it.toolCallId}`
        : it.type,
  );
  expect(shape).toEqual([
    "text:Checking the shops.",
    "tool:t1",
    "tool:t2",
    "tool:t3",
    "text:Found it.",
  ]);
});

test("whitespace inside a paragraph still reaches the segment it belongs to", async () => {
  const items = await drive(
    textEvent("one") + textEvent("\n\n") + textEvent("two") + `data: [DONE]\n\n`,
  );
  expect(
    items
      .filter((it) => it.type === "assistant_message")
      .map((it) => (it.type === "assistant_message" ? it.content : "")),
  ).toEqual(["one\n\ntwo"]);
});

// A tab reloaded mid-turn is replayed the step still streaming in one burst. Dated on
// arrival, the reasoning restarted its clock at the reload and a tool call whose start
// and end came in the same burst read 0ms; each frame now carries its age.
test("replayed frames are dated when they happened, not when they arrived", async () => {
  const now = Date.now();
  vi.spyOn(Date, "now").mockReturnValue(now);
  const aged = (age: number, frame: string) => frame.replace(/^/, `age: ${age}\n`);
  const sse =
    aged(30000, `data: ${JSON.stringify({ choices: [{ delta: { reasoning_content: "Weighing." } }] })}\n\n`) +
    aged(20000, `data: ${JSON.stringify({ choices: [{ delta: { reasoning_content: " More." } }] })}\n\n`) +
    aged(12000, `event: tool_call\ndata: ${JSON.stringify({ toolCallId: "tc1", title: "webfetch", status: "pending" })}\n\n`) +
    aged(9000, `event: tool_call_update\ndata: ${JSON.stringify({ toolCallId: "tc1", status: "in_progress", content: [{ content: { text: '{"url":"https://coddy.dev/"}' } }] })}\n\n`) +
    aged(4000, `event: tool_call_update\ndata: ${JSON.stringify({ toolCallId: "tc1", status: "completed", content: [{ content: { text: "page" } }] })}\n\n`) +
    `data: [DONE]\n\n`;

  const items = await drive(sse);
  vi.restoreAllMocks();
  const thinking = items.find((it) => it.type === "thinking");
  const call = items.find((it) => it.type === "tool_call");
  expect(thinking?.type === "thinking" && thinking.startedAtMs).toBe(now - 30000);
  expect(thinking?.type === "thinking" && thinking.durationMs).toBe(18000);
  expect(call?.type === "tool_call" && call.durationMs).toBe(5000);
});

// Tool rows wait for an animation frame so a burst of updates costs one render.
// Reasoning is applied at once, so a reasoning block that followed queued tool rows
// used to land above them, and a tab that gets no animation frames - hidden, or a
// window the browser treats as occluded - never landed the tool rows at all.
test("a reasoning block that follows queued tool rows lands below them", async () => {
  const sse =
    `event: tool_call\ndata: ${JSON.stringify({ toolCallId: "t1", title: "webfetch", status: "pending" })}\n\n` +
    `data: ${JSON.stringify({ choices: [{ delta: { reasoning_content: "Next." } }] })}\n\n` +
    `data: [DONE]\n\n`;
  const items = await drive(sse);
  expect(items.map((it) => it.type)).toEqual(["tool_call", "thinking"]);
});

test("queued tool rows land even when no animation frame ever comes", async () => {
  vi.useFakeTimers();
  try {
    vi.stubGlobal("requestAnimationFrame", () => 1);
    const items: TranscriptItem[] = [];
    const params: ConsumeComposerSseParams = {
      reader: mockReader(
        `event: tool_call\ndata: ${JSON.stringify({ toolCallId: "t1", title: "webfetch", status: "pending" })}\n\n`,
      ),
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
      newId: (p) => p,
      applyMemoryRunToItems: (prev) => prev,
    };
    await consumeComposerSseReader(params);
    expect(items).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(300);
    expect(items.map((it) => it.type)).toEqual(["tool_call"]);
  } finally {
    vi.useRealTimers();
  }
});

// A model configured with stream: false delivers reasoning and answer in the same
// flush, so the client-side clock measures the gap between two frames, not how long
// the model thought. The row must report nothing rather than a fabricated duration.
test("thinking row from a non-streamed response carries no duration", async () => {
  const sse =
    `data: ${JSON.stringify({ choices: [{ delta: { reasoning_content: "Deliberating." } }] })}\n\n` +
    textEvent("Answer after thinking.") +
    `data: [DONE]\n\n`;

  const items = await drive(sse);
  const thinking = items.find((it) => it.type === "thinking");

  expect(thinking).toBeDefined();
  expect(thinking && "status" in thinking ? thinking.status : "").toBe(
    "completed",
  );
  expect(
    thinking && "durationMs" in thinking ? thinking.durationMs : undefined,
  ).toBeUndefined();
});

// A genuinely streamed turn still reports how long the thinking took.
test("thinking row from a streamed response keeps its measured duration", async () => {
  vi.useFakeTimers();
  try {
    const sse =
      `data: ${JSON.stringify({ choices: [{ delta: { reasoning_content: "Deliberating." } }] })}\n\n` +
      textEvent("Answer after thinking.") +
      `data: [DONE]\n\n`;

    let now = 1_000_000;
    vi.spyOn(Date, "now").mockImplementation(() => {
      const v = now;
      now += 400; // every reading advances, so the gap clears the floor
      return v;
    });

    const items = await drive(sse);
    const thinking = items.find((it) => it.type === "thinking");
    const dur =
      thinking && "durationMs" in thinking ? thinking.durationMs : undefined;

    expect(typeof dur).toBe("number");
    expect(dur as number).toBeGreaterThanOrEqual(minMeasurableThinkingMs);
  } finally {
    vi.restoreAllMocks();
    vi.useRealTimers();
  }
});

// A gate prompt is answered outside the rAF-batched tool queue, so a question or
// permission event landing in the same frame as the tool row that raised it used
// to render above that row: the card appeared, and the "asking a question" row
// followed it. Both prompt events must drain the queue before the card is added.
async function driveWithPrompts(sse: string): Promise<string[]> {
  vi.stubGlobal("requestAnimationFrame", () => 0);
  const items: TranscriptItem[] = [];
  let idc = 0;
  const applyStreamItems = (fn: (prev: TranscriptItem[]) => TranscriptItem[]) => {
    const next = fn(items.slice());
    items.length = 0;
    items.push(...next);
  };
  const params: ConsumeComposerSseParams = {
    reader: mockReader(sse),
    dec: new TextDecoder(),
    carry: { buf: "" },
    assistantId: "a-init",
    applyStreamItems,
    setTokenUsage: () => {},
    setContextUsage: () => {},
    tokenBaselineRef: { current: { input: 0, output: 0, total: 0 } },
    reasoningDurationMsByContentRef: { current: new Map() },
    newId: (p) => `${p}-${idc++}`,
    applyMemoryRunToItems: (prev) => prev,
    // Mirrors App.tsx: both handlers apply their row straight away, outside the queue.
    onQuestion: (raw) =>
      applyStreamItems((prev) => [
        ...prev,
        {
          id: `qp_${String(raw.requestId)}`,
          type: "question_prompt",
          payload: {
            sessionId: "s1",
            requestId: String(raw.requestId),
            toolCallId: String(raw.toolCallId || ""),
            questions: [{ question: "Which one?", options: [{ label: "A" }] }],
          },
        },
      ]),
    onPermission: (raw) =>
      applyStreamItems((prev) => [
        ...prev,
        {
          id: `pp_${String(raw.toolCallId)}`,
          type: "permission_prompt",
          payload: {
            sessionId: "s1",
            toolCall: { toolCallId: String(raw.toolCallId), title: "run_command" },
            options: [],
          },
        } as TranscriptItem,
      ]),
  };
  const res = await consumeComposerSseReader(params);
  res.flushToolQueue();
  return items.map((it) =>
    it.type === "tool_call" ? `tool:${it.toolCallId}` : it.type,
  );
}

test("a question card renders below the tool row that raised it", async () => {
  const sse =
    `event: tool_call\ndata: ${JSON.stringify({ toolCallId: "tc-q", title: "question", kind: "other", status: "pending" })}\n\n` +
    `event: question\ndata: ${JSON.stringify({ sessionId: "s1", requestId: "q_1", toolCallId: "tc-q", questions: [] })}\n\n` +
    `data: [DONE]\n\n`;

  expect(await driveWithPrompts(sse)).toEqual(["tool:tc-q", "question_prompt"]);
});

test("a permission card renders below the tool row that raised it", async () => {
  const sse =
    `event: tool_call\ndata: ${JSON.stringify({ toolCallId: "tc-p", title: "run_command", kind: "execute", status: "pending" })}\n\n` +
    `event: permission\ndata: ${JSON.stringify({ sessionId: "s1", toolCallId: "tc-p" })}\n\n` +
    `data: [DONE]\n\n`;

  expect(await driveWithPrompts(sse)).toEqual([
    "tool:tc-p",
    "permission_prompt",
  ]);
});

test("turn_progress reaches the caller on this machine's clock, replayed frames aged", async () => {
  vi.stubGlobal("requestAnimationFrame", () => 0);
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-09-18T10:00:45Z"));
  const now = Date.now();
  const seen: Array<{ startedAtMs: number; outputTokens: number; estimated: boolean }> = [];
  const frame = (payload: object, age?: number) =>
    `event: turn_progress\n${age === undefined ? "" : `age: ${age}\n`}data: ${JSON.stringify(payload)}\n\n`;
  const params: ConsumeComposerSseParams = {
    reader: mockReader(
      frame({ sessionUpdate: "turn_progress", startedAt: "2026-09-18T12:00:00Z", elapsedMs: 40_000, outputTokens: 0, estimated: false }, 5_000) +
        frame({ sessionUpdate: "turn_progress", startedAt: "2026-09-18T12:00:00Z", elapsedMs: 45_000, outputTokens: 433, estimated: true }) +
        `data: [DONE]\n\n`,
    ),
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
    onTurnProgress: (p) => seen.push(p),
  };
  try {
    await consumeComposerSseReader(params);
  } finally {
    vi.useRealTimers();
  }
  expect(seen).toEqual([
    { startedAtMs: now - 45_000, outputTokens: 0, estimated: false },
    { startedAtMs: now - 45_000, outputTokens: 433, estimated: true },
  ]);
});
