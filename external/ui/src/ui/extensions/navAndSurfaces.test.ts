import { afterEach, describe, expect, test, vi } from "vitest";
import { extensionNav } from "./nav";
import { ChatBridge } from "./chatBridge";
import { validateModal, validateOverlay, validateShell } from "./surfaces";
import {
  ExtensionRuntime,
  type ExtensionAPI,
  type ExtensionEntry,
} from "./runtime";
import { createHash, webcrypto } from "node:crypto";

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
  ui: { entry: "main.js" },
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.location.hash = "";
});

describe("navigation", () => {
  test("the address reports the route and screens open like the rail", () => {
    const nav = extensionNav();
    window.location.hash = "#/s/sess_1";
    expect(nav.currentRoute()).toEqual({
      branch: "session",
      sessionId: "sess_1",
    });
    nav.openScreen("history");
    expect(nav.currentRoute().branch).toBe("history");
    nav.openSession("sess_2");
    expect(nav.currentRoute()).toEqual({
      branch: "session",
      sessionId: "sess_2",
    });
    nav.openScreen("home");
    expect(nav.currentRoute().sessionId).toBeNull();
    expect(() => nav.openScreen("nowhere" as never)).toThrow(/Unknown screen/);
    expect(() => nav.openSession("bad id!")).toThrow(/Invalid session id/);
  });
});

describe("the chat bridge", () => {
  test("listeners hear real changes only", () => {
    const bridge = new ChatBridge();
    const heard: unknown[] = [];
    const handle = bridge.onStatusChange(
      new AbortController().signal,
      (status) => heard.push(status),
    );
    const status = {
      sessionId: "s1",
      turnActive: true,
      tokenUsage: { inputTokens: 1, outputTokens: 2, totalTokens: 3 },
      permissionPending: false,
      questionPending: false,
    };
    bridge.report(status);
    bridge.report({ ...status });
    expect(heard).toHaveLength(1);
    expect(bridge.current()).toMatchObject({
      sessionId: "s1",
      turnActive: true,
    });
    handle.remove();
    bridge.report({ ...status, turnActive: false });
    expect(heard).toHaveLength(1);
  });
});

describe("surfaces", () => {
  test("a modal, an overlay and the shell need their ids and mounts", () => {
    expect(() =>
      validateModal({ id: "x", title: "", mount: () => {} }),
    ).toThrow(/modal/);
    expect(() =>
      validateOverlay({
        id: "x",
        mount: () => {},
        position: { top: "x" } as never,
      }),
    ).toThrow(/overlay/);
    expect(() => validateShell({ id: "x", mount: undefined as never })).toThrow(
      /shell/,
    );
  });

  async function activateOne(register: (api: ExtensionAPI) => void) {
    vi.stubGlobal("crypto", webcrypto);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(source)),
    );
    URL.createObjectURL = vi.fn(() => "blob:demo");
    URL.revokeObjectURL = vi.fn();
    let api!: ExtensionAPI;
    const runtime = new ExtensionRuntime(vi.fn(), async () => ({
      activate(value) {
        api = value;
        register(value);
      },
    }));
    await runtime.reconcile([entry]);
    return { api, runtime };
  }

  test("a modal and overlays open, close and leave on dispose", async () => {
    const { api, runtime } = await activateOne(() => {});
    const modal = api.ui.openModal({ id: "m", title: "M", mount: () => {} });
    api.ui.openOverlay({
      id: "o",
      mount: () => {},
      position: { top: 8, right: 8 },
    });
    expect(runtime.snapshot().modals.map((m) => m.key)).toEqual(["demo/m"]);
    expect(runtime.snapshot().overlays.map((o) => o.key)).toEqual(["demo/o"]);
    // One modal at a time: a second replaces the first.
    api.ui.openModal({ id: "m2", title: "M2", mount: () => {} });
    expect(runtime.snapshot().modals.map((m) => m.key)).toEqual(["demo/m2"]);
    modal.close();
    expect(runtime.snapshot().modals.map((m) => m.key)).toEqual(["demo/m2"]);
    runtime.closeSurface("overlays", "demo/o");
    expect(runtime.snapshot().overlays).toEqual([]);
    runtime.stop();
    expect(runtime.snapshot().modals).toEqual([]);
  });

  test("the shell takeover holds the window until it is released", async () => {
    const { api, runtime } = await activateOne(() => {});
    const shell = api.ui.takeOverShell({ id: "shell", mount: () => {} });
    expect(runtime.snapshot().shell?.key).toBe("demo/shell");
    shell.release();
    expect(runtime.snapshot().shell).toBeNull();
    // A second takeover replaces the first; dispose releases it.
    api.ui.takeOverShell({ id: "shell", mount: () => {} });
    runtime.stop();
    expect(runtime.snapshot().shell).toBeNull();
  });
});
