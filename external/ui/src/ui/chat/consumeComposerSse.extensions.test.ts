import { afterEach, expect, test, vi } from "vitest";
import { createHash, webcrypto } from "node:crypto";
import { ExtensionRuntime, type ExtensionAPI } from "../extensions/runtime";
import {
  consumeComposerSseReader,
  type ComposerStreamCursor,
} from "./consumeComposerSse";
import type { TranscriptItem } from "./types";

vi.mock("../env/remoteEnv", () => ({
  requireEnvironmentReload: vi.fn(),
  onEnvironmentSwitch: vi.fn(),
}));
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

test("initial extension loading has a bounded wait before the reader continues", async () => {
  vi.useFakeTimers();
  const reader = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.close();
    },
  }).getReader();
  const read = vi.spyOn(reader, "read");
  const result = consumeComposerSseReader({
    reader,
    dec: new TextDecoder(),
    carry: { buf: "" },
    assistantId: "answer",
    incoming: {
      sessionId: "sess_demo",
      transport: "relay",
      process: (frame) => frame.data,
      ready: new Promise<void>(() => {}),
    },
    applyStreamItems() {},
    setTokenUsage() {},
    setContextUsage() {},
    tokenBaselineRef: { current: { input: 0, output: 0, total: 0 } },
    reasoningDurationMsByContentRef: { current: new Map() },
    newId: (prefix) => prefix,
    applyMemoryRunToItems: (prev) => prev,
  });
  expect(read).not.toHaveBeenCalled();
  await vi.advanceTimersByTimeAsync(11000);
  await result;
  expect(read).toHaveBeenCalledOnce();
});

test.each(["post", "relay"] as const)(
  "enabled extensions transform incoming text and tool data on %s",
  async (transport) => {
    vi.stubGlobal("crypto", webcrypto);
    vi.stubGlobal("requestAnimationFrame", () => 0);
    const source = "export function activate() {}";
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(source)),
    );
    vi.stubGlobal(
      "URL",
      class extends URL {
        static createObjectURL = vi.fn(() => "blob:stream");
        static revokeObjectURL = vi.fn();
      },
    );
    let api!: ExtensionAPI;
    const seen: string[] = [];
    const runtime = new ExtensionRuntime(
      vi.fn(),
      async () => ({
        activate(value) {
          api = value;
          api.stream.registerIncoming({
            id: "enrich",
            transform(data, context) {
              expect(context.sessionId).toBe("sess_demo");
              expect(context.transport).toBe(transport);
              seen.push(context.id);
              const payload = JSON.parse(data);
              if (!context.event)
                payload.choices[0].delta.content =
                  payload.choices[0].delta.content.toUpperCase();
              if (context.event === "tool_call_update")
                payload.content[0].content.text += " (enriched)";
              return JSON.stringify(payload);
            },
          });
        },
      }),
      vi.fn(),
    );
    const entry = {
      api: 1,
      id: "demo",
      name: "Demo",
      version: "1",
      sha256: "package",
      entry_sha256: createHash("sha256").update(source).digest("hex"),
      enabled: true,
      ui: { entry: "main.js" },
    };
    const ready = runtime.reconcile([entry]);
    const chunks = [
      ": keepalive\n\n",
      'id: 1\ndata: {"choices":[{"delta":{"content":"hello"}}]}\n\n',
      'id: 2\nevent: tool_call_update\ndata: {"toolCallId":"tool","status":"completed","content":[{"content":{"text":"result"}}]}',
    ];
    let items: TranscriptItem[] = [];
    const cursor: ComposerStreamCursor = {
      frames: 0,
      assistantId: "answer",
      segmentDirty: false,
      segmentHasText: false,
    };
    const observed = vi.fn();
    const result = await consumeComposerSseReader({
      reader: new ReadableStream<Uint8Array>({
        start(controller) {
          for (const chunk of chunks)
            controller.enqueue(new TextEncoder().encode(chunk));
          controller.close();
        },
      }).getReader(),
      dec: new TextDecoder(),
      carry: { buf: "" },
      assistantId: "answer",
      cursor,
      incoming: {
        sessionId: "sess_demo",
        transport,
        process: runtime.incoming.process,
        observe: observed,
        ready,
      },
      applyStreamItems: (fn) => {
        items = fn(items);
      },
      setTokenUsage() {},
      setContextUsage() {},
      tokenBaselineRef: { current: { input: 0, output: 0, total: 0 } },
      reasoningDurationMsByContentRef: { current: new Map() },
      newId: (prefix) => prefix,
      applyMemoryRunToItems: (prev) => prev,
    });
    result.flushToolQueue();
    expect(items).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          type: "assistant_message",
          content: "HELLO",
        }),
        expect.objectContaining({
          type: "tool_call",
          resultText: "result (enriched)",
        }),
      ]),
    );
    expect(seen).toEqual(["1", "2"]);
    const payloads = observed.mock.calls
      .map(([frame]) => frame)
      .filter((frame) => frame.data);
    expect(payloads).toHaveLength(2);
    expect(payloads[1]).toMatchObject({
      sequence: "2",
      data: '{"toolCallId":"tool","status":"completed","content":[{"content":{"text":"result"}}]}',
    });
    expect(result.lastEventId).toBe("2");
    expect(cursor.frames).toBe(2);
    expect(runtime.snapshot().errors).toEqual({});
    await runtime.reconcile([{ ...entry, enabled: false }]);
    const frame = {
      sessionId: "sess_demo",
      transport,
      event: "",
      id: "3",
      data: '{"text":"original"}',
    };
    expect(runtime.incoming.process(frame)).toBe(frame.data);
    expect(() =>
      api.stream.registerIncoming({ id: "late", transform() {} }),
    ).toThrow("disposed");
  },
);
