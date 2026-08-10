import { afterEach, expect, test, vi } from "vitest";
import {
  consumeComposerSseReader,
  type ConsumeComposerSseParams,
  type ConsumeComposerSseResult,
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

async function drive(
  sse: string,
): Promise<{ res: ConsumeComposerSseResult; items: TranscriptItem[] }> {
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
  return { res, items };
}

// The relay reports a failed turn as a NAMED error event, not as an unnamed data
// frame. Dropping it left the reader looping until the server closed the socket,
// which reads to the user as a stream that hangs and then completes empty.
test("a named error event ends the stream with its message", async () => {
  const { res } = await drive(
    textEvent("partial ") +
      `event: error\ndata: ${JSON.stringify({ error: { message: "model exploded" } })}\n\n` +
      textEvent("never rendered"),
  );

  expect(res.streamErrorMessage).toBe("model exploded");
  expect(res.streamErrorCode).toBeNull();
});

test("a named error event stops rendering further frames", async () => {
  const { items } = await drive(
    textEvent("kept ") +
      `event: error\ndata: ${JSON.stringify({ error: { message: "boom" } })}\n\n` +
      textEvent("dropped"),
  );

  const text = items
    .filter((it) => it.type === "assistant_message")
    .map((it) => (it as { content: string }).content)
    .join("");
  expect(text).toBe("kept ");
});

// "Nothing is running" is a state, not a failure: the caller needs the code so it
// can reconcile from the persisted transcript instead of showing an error row.
test("the no_active_stream code is reported alongside the message", async () => {
  const { res } = await drive(
    `event: error\ndata: ${JSON.stringify({
      error: { message: "no active composer stream", code: "no_active_stream" },
    })}\n\n`,
  );

  expect(res.streamErrorCode).toBe("no_active_stream");
  expect(res.streamErrorMessage).toBe("no active composer stream");
});

// A server that still emits the old bare {"message":...} payload must terminate the
// stream too, and keep its text, so a mixed-version deployment still reconciles.
test("a named error event without an error envelope still ends the stream", async () => {
  const { res } = await drive(
    `event: error\ndata: ${JSON.stringify({ message: "no active composer stream" })}\n\n`,
  );

  expect(res.streamErrorMessage).toBe("no active composer stream");
  expect(res.streamErrorCode).toBeNull();
});

test("a malformed named error event does not throw", async () => {
  const { res } = await drive(
    `event: error\ndata: {not json\n\n` + textEvent("after"),
  );

  expect(res.streamErrorMessage).toBeNull();
});

// The tail-flush block re-parses whatever is left in carry.buf, so it needs the
// same branch: an error frame that arrives without a trailing blank line is only
// seen there.
test("a named error event in the trailing partial block is honoured", async () => {
  const { res } = await drive(
    textEvent("x") +
      `event: error\ndata: ${JSON.stringify({ error: { message: "late failure" } })}`,
  );

  expect(res.streamErrorMessage).toBe("late failure");
});

// Resume needs the sequence of the last frame the client actually consumed.
test("the last relay frame id is reported", async () => {
  const { res } = await drive(
    `id: 7\n` + textEvent("a") + `id: 9\n` + textEvent("b"),
  );

  expect(res.lastEventId).toBe("9");
  expect(res.desynced).toBe(false);
});

// A relay that dropped frames says so; the caller reloads instead of rendering a hole.
test("a desync frame is reported without ending the stream", async () => {
  const { res, items } = await drive(
    `event: desync\ndata: {"object":"coddy.stream_desync","lastEventId":2,"resumedAt":9}\n\n` +
      textEvent("after the gap"),
  );

  expect(res.desynced).toBe(true);
  expect(res.streamErrorMessage).toBeNull();
  const text = items
    .filter((it) => it.type === "assistant_message")
    .map((it) => (it as { content: string }).content)
    .join("");
  expect(text).toBe("after the gap");
});
