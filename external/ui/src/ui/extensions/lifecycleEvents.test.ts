import { describe, expect, test, vi } from "vitest";
import { ExtensionEvents, type ExtensionEventMap } from "./events";

function setup() {
  const reports: string[] = [];
  const events = new ExtensionEvents((owner, message) =>
    reports.push(`${owner}: ${message}`),
  );
  const signal = new AbortController().signal;
  return { events, signal, reports };
}

describe("the lifecycle event set", () => {
  test("every named lifecycle type is accepted, an unknown one refused", () => {
    const { events, signal } = setup();
    for (const type of [
      "tabChanged",
      "stepEnded",
      "turnEnded",
      "surfaceChanged",
      "overlaysChanged",
      "focusChanged",
      "beforeUnload",
      "configReloaded",
    ] as const)
      expect(events.on("demo", signal, type, () => {}).remove).toBeTypeOf(
        "function",
      );
    expect(events.on("demo", signal, "ext.demo.ping", () => {})).toBeTruthy();
    expect(() =>
      events.on("demo", signal, "surfaceOpened" as never, () => {}),
    ).toThrow(/Invalid extension event listener/);
  });

  test("surfaceChanged delivers a frozen payload to every listener", () => {
    const { events, signal } = setup();
    const seen: ExtensionEventMap["surfaceChanged"][] = [];
    events.on("demo", signal, "surfaceChanged", (event) => {
      seen.push(event);
    });
    events.on("other", signal, "surfaceChanged", (event) => {
      seen.push(event);
    });
    const payload = { surface: "panel", id: "demo/lab", open: true } as const;
    events.emit("surfaceChanged", { ...payload });
    expect(seen).toHaveLength(2);
    for (const event of seen) {
      expect(event).toEqual({ ...payload });
      expect(Object.isFrozen(event)).toBe(true);
    }
  });

  test("overlaysChanged freezes the id list it carries", () => {
    const { events, signal } = setup();
    const seen: string[][] = [];
    events.on("demo", signal, "overlaysChanged", (event) => {
      const ids = (event as ExtensionEventMap["overlaysChanged"]).ids;
      seen.push([...ids]);
      expect(Object.isFrozen(ids)).toBe(true);
    });
    events.emit("overlaysChanged", { ids: ["demo/one", "demo/two"] });
    expect(seen).toEqual([["demo/one", "demo/two"]]);
  });

  test("configReloaded reaches listeners with an empty frozen payload", () => {
    const { events, signal } = setup();
    const listener = vi.fn();
    events.on("demo", signal, "configReloaded", listener);
    events.emit("configReloaded", {});
    expect(listener).toHaveBeenCalledTimes(1);
    expect(Object.isFrozen(listener.mock.calls[0]![0])).toBe(true);
  });

  test("a listener that throws is removed and reported once", () => {
    const { events, signal, reports } = setup();
    events.on("demo", signal, "surfaceChanged", () => {
      throw new Error("boom");
    });
    events.emit("surfaceChanged", { surface: "page", id: null, open: false });
    events.emit("surfaceChanged", { surface: "page", id: null, open: false });
    expect(reports).toEqual(["demo: Error: boom"]);
  });
});
