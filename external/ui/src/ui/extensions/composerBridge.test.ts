import { describe, expect, test, vi } from "vitest";
import { ComposerBridge } from "./composerBridge";

function surface() {
  let text = "";
  const calls: string[] = [];
  return {
    calls,
    surface: {
      getText: () => text,
      setText: (next: string) => {
        text = next;
        calls.push("set:" + next);
      },
      insertText: (chunk: string) => {
        text += chunk;
        calls.push("insert:" + chunk);
      },
      attachFiles: (files: readonly File[]) =>
        calls.push("files:" + files.length),
      getMode: () => "agent",
      setMode: (mode: string) => calls.push("mode:" + mode),
      getModel: () => "m1",
      setModel: (id: string) => calls.push("model:" + id),
      getReasoning: () => "low",
      setReasoning: (level: string) => calls.push("reasoning:" + level),
      getPermissionMode: () => "ask",
      textarea: null,
    } as never,
  };
}

describe("the composer bridge", () => {
  test("the draft moves both ways and listeners hear writes", () => {
    const bridge = new ComposerBridge();
    const { surface: s, calls } = surface();
    bridge.report(s);
    const heard: string[] = [];
    const handle = bridge.onDraftChange(new AbortController().signal, (v) =>
      heard.push(v),
    );
    bridge.setText("hello");
    bridge.insertText("!");
    expect(bridge.text()).toBe("hello!");
    expect(heard).toEqual(["hello", "hello!"]);
    expect(calls).toEqual(["set:hello", "insert:!"]);
    handle.remove();
    bridge.setText("gone");
    expect(heard).toEqual(["hello", "hello!"]);
  });

  test("the picks travel through the bridge", () => {
    const bridge = new ComposerBridge();
    const { surface: s, calls } = surface();
    bridge.report(s);
    bridge.setMode("plan");
    bridge.setModel("m2");
    bridge.setReasoning("high");
    bridge.attachFiles([new File(["x"], "x.txt")]);
    expect([bridge.mode(), bridge.model(), bridge.reasoning()]).toEqual([
      "agent",
      "m1",
      "low",
    ]);
    expect(bridge.permissionMode()).toBe("ask");
    expect(calls).toEqual([
      "mode:plan",
      "model:m2",
      "reasoning:high",
      "files:1",
    ]);
  });

  test("a paste or drop listener that claims the event takes it", () => {
    const bridge = new ComposerBridge();
    const seen: string[] = [];
    const event = { preventDefault: vi.fn() };
    bridge.onPaste(new AbortController().signal, () => {
      seen.push("first");
    });
    bridge.onPaste(new AbortController().signal, () => {
      seen.push("second");
      return true;
    });
    bridge.onPaste(new AbortController().signal, () => {
      seen.push("third");
    });
    expect(bridge.claimPaste(event)).toBe(true);
    expect(seen).toEqual(["first", "second"]);
    // A listener that throws loses its registration, not the claim.
    const boom = new AbortController();
    bridge.onDrop(boom.signal, () => {
      throw new Error("boom");
    });
    const good = vi.fn(() => true);
    bridge.onDrop(boom.signal, good);
    expect(bridge.claimDrop(event)).toBe(true);
    expect(good).toHaveBeenCalledOnce();
    expect(bridge.claimDrop(event)).toBe(true);
  });

  test("the lifetime signal removes the listeners", () => {
    const bridge = new ComposerBridge();
    const controller = new AbortController();
    const listener = vi.fn(() => true);
    bridge.onPaste(controller.signal, listener);
    controller.abort();
    expect(bridge.claimPaste({ preventDefault: vi.fn() })).toBe(false);
    expect(listener).not.toHaveBeenCalled();
  });
});
