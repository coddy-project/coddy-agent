import { afterEach, describe, expect, test, vi } from "vitest";
import {
  HookSlotHost,
  isHookName,
  parseHookRef,
  validateHookContribution,
  type HookContribution,
} from "./hooks";
import { Contributions, type Owned } from "./contributions";
import type { ExtensionAPI } from "./runtime";

function setup() {
  const store = new Contributions<HookContribution>(() => host.refresh());
  const host = new HookSlotHost(
    () => store.rows,
    (key) => store.remove(key),
  );
  return { store, host };
}

function row(
  owner: string,
  id: string,
  into: string,
  mount: HookContribution["mount"],
): Owned<HookContribution> {
  return {
    id,
    into,
    mount,
    key: `${owner}/${id}`,
    owner,
    api: { id: owner, signal: new AbortController().signal } as ExtensionAPI,
  };
}

describe("hooks in interface elements", () => {
  test("a hook reference is <owner>/<name>, and names follow the id shape", () => {
    expect(parseHookRef("demo/summary")).toEqual({
      owner: "demo",
      hook: "summary",
    });
    expect(parseHookRef("demo")).toBeNull();
    expect(parseHookRef("demo/")).toBeNull();
    expect(parseHookRef("Demo/summary")).toBeNull();
    expect(isHookName("summary")).toBe(true);
    expect(isHookName("Summary")).toBe(false);
    expect(() =>
      validateHookContribution({
        id: "x",
        into: "demo",
        mount: () => {},
      }),
    ).toThrow(/hook contribution/);
    expect(() =>
      validateHookContribution({
        id: "x",
        into: "demo/summary",
        mount: undefined as never,
      }),
    ).toThrow(/hook contribution/);
  });

  test("a slot draws its contributions in owner order and nothing when empty", () => {
    const { store, host } = setup();
    const box = document.createElement("div");
    host.attach("demo/summary", box, new AbortController().signal);
    expect(box.children).toHaveLength(0);
    const order: string[] = [];
    store.register(
      "b",
      { id: "w", signal: new AbortController().signal } as ExtensionAPI,
      {
        id: "two",
        into: "demo/summary",
        mount: (el) => {
          order.push("b");
          el.textContent = "from b";
        },
      },
      validateHookContribution,
    );
    store.register(
      "a",
      { id: "w", signal: new AbortController().signal } as ExtensionAPI,
      {
        id: "one",
        into: "demo/summary",
        mount: (el) => {
          order.push("a");
          el.textContent = "from a";
        },
      },
      validateHookContribution,
    );
    // The keyed diff keeps b mounted: only a is new.
    expect(order).toEqual(["b", "a"]);
    expect([...box.children].map((c) => c.textContent)).toEqual([
      "from a",
      "from b",
    ]);
    expect(box.children[0]!.getAttribute("data-extension-renderer")).toBe(
      "a/one",
    );
  });

  test("contributions appear and leave live, with their cleanups", () => {
    const { store, host } = setup();
    const box = document.createElement("div");
    const api = {
      id: "w",
      signal: new AbortController().signal,
    } as never as ExtensionAPI;
    host.attach("demo/summary", box, new AbortController().signal);
    const cleanup = vi.fn();
    const handle = store.register(
      "demo",
      api,
      {
        id: "one",
        into: "demo/summary",
        mount: (el) => {
          el.textContent = "hello";
          return cleanup;
        },
      },
      validateHookContribution,
    );
    expect(box.textContent).toBe("hello");
    handle.remove();
    expect(box.children).toHaveLength(0);
    expect(cleanup).toHaveBeenCalledOnce();
  });

  test("a contribution that throws is dropped, its neighbours stay", () => {
    const failed: string[] = [];
    const store = new Contributions<HookContribution>(() => host.refresh());
    const host = new HookSlotHost(
      () => store.rows,
      (key) => {
        failed.push(key);
        store.remove(key);
      },
    );
    const box = document.createElement("div");
    const api = {
      id: "w",
      signal: new AbortController().signal,
    } as never as ExtensionAPI;
    host.attach("demo/summary", box, new AbortController().signal);
    store.register(
      "a",
      api,
      {
        id: "bad",
        into: "demo/summary",
        mount: () => {
          throw new Error("boom");
        },
      },
      validateHookContribution,
    );
    store.register(
      "b",
      api,
      {
        id: "good",
        into: "demo/summary",
        mount: (el) => {
          el.textContent = "ok";
        },
      },
      validateHookContribution,
    );
    expect(failed).toEqual(["a/bad"]);
    expect(box.textContent).toBe("ok");
  });

  test("the slot handle removes the box and its mounts", () => {
    const { store, host } = setup();
    const box = document.createElement("div");
    const cleanup = vi.fn();
    store.register(
      "demo",
      {
        id: "w",
        signal: new AbortController().signal,
      } as never as ExtensionAPI,
      {
        id: "one",
        into: "demo/summary",
        mount: (el) => {
          el.textContent = "hello";
          return cleanup;
        },
      },
      validateHookContribution,
    );
    const handle = host.attach(
      "demo/summary",
      box,
      new AbortController().signal,
    );
    expect(box.textContent).toBe("hello");
    handle.remove();
    expect(box.children).toHaveLength(0);
    expect(cleanup).toHaveBeenCalledOnce();
    // A second attach after removal renders again.
    host.attach("demo/summary", box, new AbortController().signal);
    expect(box.textContent).toBe("hello");
  });

  test("the lifetime signal removes the slot", () => {
    const { host } = setup();
    const controller = new AbortController();
    const box = document.createElement("div");
    host.attach("demo/summary", box, controller.signal);
    controller.abort();
    expect(box.children).toHaveLength(0);
    expect(() => host.attach("demo/summary", box, controller.signal)).toThrow(
      /disposed/,
    );
  });
});
