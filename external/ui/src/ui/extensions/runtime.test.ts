import { afterEach, describe, expect, test, vi } from "vitest";
import { createHash, webcrypto } from "node:crypto";
import {
  ExtensionRuntime,
  type ExtensionAPI,
  type ExtensionEntry,
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
});
