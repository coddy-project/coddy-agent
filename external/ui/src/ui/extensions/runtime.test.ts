import { afterEach, describe, expect, test, vi } from "vitest";
import { createHash, webcrypto } from "node:crypto";
import {
  ExtensionRuntime,
  type ExtensionAPI,
  type ExtensionEntry,
  type ExtensionPage,
  type MenuItem,
} from "./runtime";

vi.mock("../env/remoteEnv", () => ({
  requireEnvironmentReload: vi.fn(),
  onEnvironmentSwitch: vi.fn(),
}));
const source = "export function activate(coddy) {}";
const entry: ExtensionEntry = {
  api: 1,
  id: "demo",
  name: "Demo",
  version: "1",
  sha256: "package",
  entry_sha256: createHash("sha256").update(source).digest("hex"),
  enabled: true,
  ui: { entry: "main.js", styles: ["main.css"] },
};
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.querySelectorAll("style[data-ext]").forEach((el) => el.remove());
});
function setup() {
  vi.stubGlobal("crypto", webcrypto);
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async (url: string) =>
        new Response(url.includes("main.css") ? ".demo {}" : source),
    ),
  );
  URL.createObjectURL = vi.fn(() => "blob:demo");
  URL.revokeObjectURL = vi.fn();
}
describe("extension lifetime", () => {
  test("disabled packages never fetch or execute code", async () => {
    setup();
    const load = vi.fn();
    const runtime = new ExtensionRuntime(vi.fn(), load);
    await runtime.reconcile([{ ...entry, enabled: false }]);
    expect(fetch).not.toHaveBeenCalled();
    expect(load).not.toHaveBeenCalled();
  });
  test("every extension gets the theme API of the active theme", async () => {
    setup();
    let api!: ExtensionAPI;
    const runtime = new ExtensionRuntime(vi.fn(), async () => ({
      activate(value) {
        api = value;
      },
    }));
    await runtime.reconcile([entry]);
    expect(api.theme.get().colors).toHaveProperty("accent");
    const seen: unknown[] = [];
    api.theme.onChange((theme) => {
      seen.push(theme);
    });
    runtime.themes.changed();
    expect(seen).toHaveLength(1);
    runtime.stop();
    expect(() => api.theme.get()).toThrow("Extension disposed");
    expect(() => api.theme.onChange(() => {})).toThrow("Extension disposed");
  });
  test("register, update, open and remove controls; disabling disposes and reloads", async () => {
    setup();
    const open = vi.fn();
    const reload = vi.fn();
    const dispose = vi.fn();
    let api!: ExtensionAPI;
    const runtime = new ExtensionRuntime(
      open,
      async () => ({
        activate(value) {
          api = value;
          const page = api.ui.registerPage({
            id: "page",
            title: "Before",
            mount() {},
          });
          page.update({ title: "After" });
          api.ui.registerMenuItem({
            id: "action",
            title: "Action",
            onSelect() {},
          });
          return dispose;
        },
      }),
      reload,
    );
    await runtime.reconcile([entry]);
    expect(runtime.snapshot().pages[0]?.title).toBe("After");
    api.ui.openPage("page");
    expect(open).toHaveBeenCalledOnce();
    expect(document.querySelector("style[data-ext=demo]")).not.toBeNull();
    await runtime.reconcile([{ ...entry, enabled: false }]);
    expect(dispose).toHaveBeenCalledOnce();
    expect(api.signal.aborted).toBe(true);
    expect(runtime.snapshot().pages).toEqual([]);
    expect(runtime.snapshot().menu).toEqual([]);
    expect(document.querySelector("style[data-ext=demo]")).toBeNull();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:demo");
    expect(reload).toHaveBeenCalledOnce();
    await expect(api.assets.text("page.html")).rejects.toThrow();
  });
  test("a digest mismatch runs no code and is not retried on every list refresh", async () => {
    setup();
    const load = vi.fn();
    const runtime = new ExtensionRuntime(vi.fn(), load);
    await runtime.reconcile([{ ...entry, entry_sha256: "wrong" }]);
    await runtime.reconcile([entry]);
    expect(load).not.toHaveBeenCalled();
    expect(runtime.snapshot().errors.demo).toContain("digest mismatch");
  });
  test("a late module cannot register after the environment is disposed", async () => {
    setup();
    let resolve!: (mod: { activate: (api: ExtensionAPI) => void }) => void;
    const load = vi.fn(
      () =>
        new Promise<{ activate: (api: ExtensionAPI) => void }>((r) => {
          resolve = r;
        }),
    );
    const runtime = new ExtensionRuntime(vi.fn(), load);
    const work = runtime.reconcile([entry]);
    await vi.waitFor(() => expect(load).toHaveBeenCalledOnce());
    runtime.stop();
    const activate = vi.fn();
    resolve({ activate });
    await work;
    expect(activate).not.toHaveBeenCalled();
    expect(runtime.snapshot().pages).toEqual([]);
    expect(document.querySelector("style[data-ext=demo]")).toBeNull();
  });
  test("a broken extension leaves the other extension's controls intact", async () => {
    setup();
    const runtime = new ExtensionRuntime(vi.fn(), async () => ({
      activate(api) {
        api.ui.registerMenuItem({ id: "menu", title: api.id, onSelect() {} });
        if (api.id === "demo") throw new Error("broken");
      },
    }));
    await runtime.reconcile([entry, { ...entry, id: "other" }]);
    expect(runtime.snapshot().menu.map((row) => row.owner)).toEqual(["other"]);
    expect(runtime.snapshot().errors.demo).toContain("broken");
    runtime.stop();
  });
  test("a module that never settles releases reconcile when its lifetime ends", async () => {
    setup();
    const load = vi.fn(() => new Promise<never>(() => {}));
    const runtime = new ExtensionRuntime(vi.fn(), load);
    const work = runtime.reconcile([entry]);
    await vi.waitFor(() => expect(load).toHaveBeenCalledOnce());
    runtime.stop();
    await work;
    expect(load).toHaveBeenCalledOnce();
  });
  test("an activation timeout releases reconcile and is not retried", async () => {
    setup();
    // Only the activation timer is faked: the fetch and digest chain before
    // the module import still needs the real event loop.
    vi.useFakeTimers({
      toFake: ["setTimeout", "clearTimeout", "Date"],
    });
    const load = vi.fn(() => new Promise<never>(() => {}));
    const runtime = new ExtensionRuntime(vi.fn(), load);
    const work = runtime.reconcile([entry]);
    // The chain before the module import runs on the real event loop, and a
    // loaded machine can spend more than a fixed number of turns on the digest:
    // wait for the import on the wall clock, which the faked activation timer
    // (setTimeout, Date) does not touch.
    const deadline = performance.now() + 10_000;
    while (load.mock.calls.length === 0 && performance.now() < deadline) {
      await new Promise((done) => setImmediate(done));
    }
    expect(load).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(10_000);
    await work;
    expect(runtime.snapshot().errors.demo).toBe("Activation timed out");
    await runtime.reconcile([entry]);
    expect(load).toHaveBeenCalledOnce();
    vi.useRealTimers();
  });
  test("an explicit refresh retries a failure that never ran code", async () => {
    setup();
    let attempts = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        attempts += 1;
        if (attempts === 1) return new Response("", { status: 500 });
        return new Response(url.includes("main.css") ? ".demo {}" : source);
      }),
    );
    const load = vi.fn(async () => ({ activate() {} }));
    const runtime = new ExtensionRuntime(vi.fn(), load);
    await runtime.reconcile([entry]);
    expect(load).not.toHaveBeenCalled();
    expect(runtime.snapshot().errors.demo).toContain("HTTP 500");
    // The list poll keeps leaving the failed digest alone.
    await runtime.reconcile([entry]);
    expect(load).not.toHaveBeenCalled();
    await runtime.reconcile([entry], true);
    expect(load).toHaveBeenCalledOnce();
    expect(runtime.snapshot().errors.demo).toBeUndefined();
    runtime.stop();
  });
  test("an explicit refresh never re-evaluates a module that already ran", async () => {
    setup();
    const load = vi.fn(async () => ({
      activate() {
        throw new Error("broken");
      },
    }));
    const runtime = new ExtensionRuntime(vi.fn(), load);
    await runtime.reconcile([entry]);
    expect(runtime.snapshot().errors.demo).toContain("broken");
    await runtime.reconcile([entry], true);
    expect(load).toHaveBeenCalledOnce();
    expect(runtime.snapshot().errors.demo).toContain("broken");
    runtime.stop();
  });
  test("control updates are validated like registrations", async () => {
    setup();
    let api!: ExtensionAPI;
    let page!: ReturnType<ExtensionAPI["ui"]["registerPage"]>;
    let menu!: ReturnType<ExtensionAPI["ui"]["registerMenuItem"]>;
    const runtime = new ExtensionRuntime(vi.fn(), async () => ({
      activate(value: ExtensionAPI) {
        api = value;
        page = api.ui.registerPage({
          id: "page",
          title: "Page",
          mount() {},
        });
        menu = api.ui.registerMenuItem({
          id: "action",
          title: "Action",
          onSelect() {},
        });
      },
    }));
    await runtime.reconcile([entry]);
    expect(() =>
      api.ui.registerPage({
        id: "bad",
        title: "Bad",
        mount: 1 as unknown as () => void,
      }),
    ).toThrow("mount");
    expect(() => page.update({ title: " " })).toThrow("title");
    expect(() =>
      page.update({ mount: 1 as unknown as ExtensionPage["mount"] }),
    ).toThrow("mount");
    expect(() =>
      menu.update({ onSelect: 1 as unknown as MenuItem["onSelect"] }),
    ).toThrow("onSelect");
    page.update({ title: "Renamed" });
    menu.update({ title: "Renamed" });
    expect(runtime.snapshot().pages[0]?.title).toBe("Renamed");
    expect(runtime.snapshot().menu[0]?.title).toBe("Renamed");
    runtime.stop();
  });
});
