import { describe, expect, test, vi } from "vitest";
import { FrameObserver } from "./frameObserver";

describe("frame observers", () => {
  const frame = {
    sessionId: "s1",
    transport: "post" as const,
    event: "custom_event",
    id: "4",
    data: "hello",
  };

  test("a listener sees every frame frozen, and remove stops delivery", () => {
    const observer = new FrameObserver(vi.fn());
    const seen: unknown[] = [];
    const handle = observer.register(
      "demo",
      "a",
      new AbortController().signal,
      (f) => seen.push(f),
    );
    observer.observe(frame);
    handle.remove();
    observer.observe({ ...frame, id: "5" });
    expect(seen).toHaveLength(1);
    expect(Object.isFrozen(seen[0])).toBe(true);
  });

  test("a listener that throws is removed and reported, the next still runs", () => {
    const onError = vi.fn();
    const observer = new FrameObserver(onError);
    const good = vi.fn();
    observer.register("demo", "bad", new AbortController().signal, () => {
      throw new Error("boom");
    });
    observer.register("demo", "good", new AbortController().signal, good);
    observer.observe(frame);
    observer.observe({ ...frame, id: "5" });
    expect(onError).toHaveBeenCalledOnce();
    expect(good).toHaveBeenCalledTimes(2);
  });

  test("the lifetime signal removes the listener", () => {
    const controller = new AbortController();
    const observer = new FrameObserver(vi.fn());
    const listener = vi.fn();
    observer.register("demo", "a", controller.signal, listener);
    controller.abort();
    observer.observe(frame);
    expect(listener).not.toHaveBeenCalled();
  });

  test("ids follow the contribution rules", () => {
    const observer = new FrameObserver(vi.fn());
    const signal = new AbortController().signal;
    expect(() => observer.register("demo", "Bad Id", signal, vi.fn())).toThrow(
      /id and listener/,
    );
    observer.register("demo", "a", signal, vi.fn());
    expect(() => observer.register("demo", "a", signal, vi.fn())).toThrow(
      /Duplicate/,
    );
  });
});
