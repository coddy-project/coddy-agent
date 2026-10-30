import { expect, test, vi } from "vitest";
import { IncomingStream, type IncomingFrame } from "./incomingStream";

const frame: IncomingFrame = {
  sessionId: "sess_demo",
  transport: "relay",
  event: "tool_call_update",
  id: "12",
  ageMs: 40,
  data: '{"text":"hello"}',
};

test("incoming processors transform and enrich data in stable extension order", () => {
  const stream = new IncomingStream(vi.fn());
  const signal = new AbortController().signal;
  stream.register("z", "append", signal, (data, context) => {
    const { data: _data, ...metadata } = frame;
    expect(context).toEqual(metadata);
    expect(Object.isFrozen(context)).toBe(true);
    const value = JSON.parse(data);
    return JSON.stringify({ ...value, text: value.text + "!" });
  });
  stream.register("a", "upper", signal, (data) => {
    const value = JSON.parse(data);
    return JSON.stringify({
      ...value,
      text: value.text.toUpperCase(),
      note: "added",
    });
  });
  expect(JSON.parse(stream.process(frame))).toEqual({
    text: "HELLO!",
    note: "added",
  });
  expect(frame.data).toBe('{"text":"hello"}');
});

test("removed and aborted registrations stop immediately and ids can be reused", () => {
  const stream = new IncomingStream(vi.fn());
  const ctl = new AbortController();
  const transform = vi.fn((data: string) => data);
  const handle = stream.register("demo", "one", ctl.signal, transform);
  expect(() => stream.register("demo", "one", ctl.signal, transform)).toThrow();
  handle.remove();
  stream.register("demo", "one", ctl.signal, transform);
  ctl.abort();
  stream.process(frame);
  expect(transform).not.toHaveBeenCalled();
  expect(() =>
    stream.register("demo", "late", ctl.signal, transform),
  ).toThrow();
});

test.each([
  () => {
    throw new Error("broken");
  },
  () => "invalid JSON",
  () => "[DONE]",
  () => 42,
  () => Promise.reject(new Error("async is unsupported")),
])(
  "a broken processor is removed and the next sees the previous valid data",
  async (bad) => {
    const error = vi.fn();
    const next = vi.fn();
    const stream = new IncomingStream(error);
    const signal = new AbortController().signal;
    stream.register("a", "bad", signal, bad as never);
    stream.register("b", "observer", signal, next);
    expect(stream.process(frame)).toBe(frame.data);
    expect(stream.process(frame)).toBe(frame.data);
    expect(error).toHaveBeenCalledOnce();
    expect(next.mock.calls.map((c) => c[0])).toEqual([frame.data, frame.data]);
    await Promise.resolve();
  },
);

test("empty keepalives, termination and desync frames bypass processors", () => {
  const stream = new IncomingStream(vi.fn());
  const transform = vi.fn();
  stream.register("demo", "observe", new AbortController().signal, transform);
  expect(stream.process({ ...frame, data: "[DONE]" })).toBe("[DONE]");
  expect(stream.process({ ...frame, event: "desync" })).toBe(frame.data);
  expect(stream.process({ ...frame, event: "", data: "" })).toBe("");
  expect(transform).not.toHaveBeenCalled();
});
