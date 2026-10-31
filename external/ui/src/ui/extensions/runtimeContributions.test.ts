import { afterEach, describe, expect, test, vi } from "vitest";
import { createHash, webcrypto } from "node:crypto";
import {
  ExtensionRuntime,
  type ExtensionAPI,
  type ExtensionEntry,
} from "./runtime";
import { bootstrapEngineTier } from "./compat";

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
  permissions: ["ui.page"],
  ui: { entry: "main.js" },
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document
    .querySelectorAll(
      "style[data-ext],style[data-ext-compat],style[data-ext-theme-override]",
    )
    .forEach((el) => el.remove());
  delete document.documentElement.dataset.coddyEngine;
  delete document.documentElement.dataset.coddyChromium;
});

function setup() {
  vi.stubGlobal("crypto", webcrypto);
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(source)),
  );
  URL.createObjectURL = vi.fn(() => "blob:demo");
  URL.revokeObjectURL = vi.fn();
}

async function activateOne(
  register: (api: ExtensionAPI) => void,
  opened: { page?: unknown; panel?: unknown } = {},
) {
  let api!: ExtensionAPI;
  const runtime = new ExtensionRuntime(
    (page) => {
      opened.page = page;
    },
    async () => ({
      activate(value) {
        api = value;
        register(value);
      },
    }),
    () => {},
    (panel) => {
      opened.panel = panel;
    },
  );
  await runtime.reconcile([entry]);
  return { api, runtime };
}

describe("the widened extension API", () => {
  test("a panel registers, opens through the host and leaves on dispose", async () => {
    setup();
    const opened: { panel?: unknown } = {};
    const { api, runtime } = await activateOne((api) => {
      api.ui.registerPanel({
        id: "lab",
        title: "Lab",
        placement: "slide-over",
        mount: () => {},
      });
      api.ui.openPanel("lab");
    }, opened);
    expect(opened.panel).toMatchObject({
      key: "demo/lab",
      placement: "slide-over",
    });
    expect(runtime.snapshot().panels).toHaveLength(1);
    runtime.stop();
    expect(runtime.snapshot().panels).toHaveLength(0);
    expect(() =>
      api.ui.registerPanel({
        id: "other",
        title: "Other",
        placement: "full",
        mount: () => {},
      }),
    ).toThrow(/disposed/);
  });

  test("frame listeners observe what the stream publishes", async () => {
    setup();
    const seen: unknown[] = [];
    const { runtime } = await activateOne((api) => {
      api.stream.onFrame({
        id: "watch",
        listener: (frame) => seen.push(frame),
      });
    });
    runtime.frames.observe({
      sessionId: "s1",
      transport: "post",
      event: "custom_event",
      id: "1",
      data: "x",
    });
    expect(seen).toHaveLength(1);
    runtime.stop();
    runtime.frames.observe({
      sessionId: "s1",
      transport: "post",
      event: "custom_event",
      id: "2",
      data: "y",
    });
    expect(seen).toHaveLength(1);
  });

  test("message renderers, permission previews and session filters clean up", async () => {
    setup();
    const { runtime } = await activateOne((api) => {
      api.ui.registerMessageRenderer({
        id: "rows",
        match: "ext_frame:usage",
        mount: () => {},
      });
      api.ui.registerPermissionPreview({
        id: "preview",
        tool: "demo__status",
        mount: () => {},
      });
      api.ui.registerSessionFilter({
        id: "filter",
        title: "Active only",
        filter: (row) => !row.archived,
      });
    });
    const snap = runtime.snapshot();
    expect(snap.messageRenderers).toHaveLength(1);
    expect(snap.permissionPreviews).toHaveLength(1);
    expect(snap.sessionFilters).toHaveLength(1);
    runtime.stop();
    const after = runtime.snapshot();
    expect(after.messageRenderers).toHaveLength(0);
    expect(after.permissionPreviews).toHaveLength(0);
    expect(after.sessionFilters).toHaveLength(0);
  });

  test("a field may name its own section, and unknown sections stay refused", async () => {
    setup();
    const { runtime } = await activateOne((api) => {
      api.settings.registerSection({ id: "lab", title: "Lab" });
      api.settings.registerField({
        id: "hue",
        section: "lab",
        title: "Hue",
        type: "number",
        default: 120,
      });
      api.settings.registerField({
        id: "mode",
        section: "agent",
        title: "Mode",
        type: "string",
      });
    });
    expect(runtime.snapshot().sections.map((s) => s.id)).toEqual(["lab"]);
    expect(runtime.snapshot().fields.map((f) => f.id)).toEqual(["hue", "mode"]);
    const errors: string[] = [];
    let api!: ExtensionAPI;
    const runtime2 = new ExtensionRuntime(vi.fn(), async () => ({
      activate(value) {
        api = value;
      },
    }));
    await runtime2.reconcile([{ ...entry, id: "demo2" }]);
    expect(() =>
      api.settings.registerField({
        id: "x",
        section: "nowhere",
        title: "X",
        type: "string",
      }),
    ).toThrow(/Unknown settings section/);
  });

  test("compat styles layer on a legacy engine only", async () => {
    setup();
    const { api, runtime } = await activateOne(() => {});
    api.ui.addCompatStyles(".x { color: red }");
    expect(document.querySelector("style[data-ext-compat]")).toBeNull();
    runtime.stop();

    vi.stubGlobal("navigator", {
      userAgent: "Mozilla/5.0 Chrome/98.0.4758.102",
    });
    bootstrapEngineTier();
    expect(document.documentElement.dataset.coddyEngine).toBe("legacy");
    const second = await activateOne(() => {});
    second.api.ui.addCompatStyles(".x { color: red }");
    const style = document.querySelector("style[data-ext-compat='demo']");
    expect(style?.textContent).toBe(".x { color: red }");
    second.runtime.stop();
    expect(document.querySelector("style[data-ext-compat]")).toBeNull();
  });

  test("a menu action opens its panel", async () => {
    setup();
    const opened: { panel?: unknown } = {};
    const { runtime } = await activateOne((api) => {
      api.ui.registerPanel({
        id: "lab",
        title: "Lab",
        placement: "drawer",
        mount: () => {},
      });
      api.ui.registerMenuItem({
        id: "open-lab",
        title: "Open Lab",
        onSelect: () => api.ui.openPanel("lab"),
      });
    }, opened);
    const item = runtime.snapshot().menu[0]!;
    expect(item.title).toBe("Open Lab");
    item.onSelect();
    expect(opened.panel).toMatchObject({
      key: "demo/lab",
      placement: "drawer",
    });
  });

  test("fireEvent publishes a host event and delivers it locally", async () => {
    setup();
    let api!: ExtensionAPI;
    const runtime = new ExtensionRuntime(vi.fn(), async () => ({
      activate(value) {
        api = value;
      },
    }));
    await runtime.reconcile([entry]);
    const seen: unknown[] = [];
    api.events.on("ext.demo.ping", (event) => {
      seen.push(event);
    });
    await api.host.fireEvent({ type: "ping", data: { n: 1 } });
    expect(fetch).toHaveBeenCalledWith(
      "/coddy/extensions/demo/events",
      expect.objectContaining({ method: "POST" }),
    );
    expect(seen).toMatchObject([
      { extension: "demo", type: "ping", data: { n: 1 } },
    ]);
    expect(Object.isFrozen(seen[0])).toBe(true);
    await expect(api.host.fireEvent({ type: "Bad Type" })).rejects.toThrow(
      /event type/,
    );
    const cyclic: { self?: unknown } = {};
    cyclic.self = cyclic;
    await expect(
      api.host.fireEvent({ type: "ping", data: cyclic }),
    ).rejects.toThrow(/JSON value/);
  });

  test("request resolves with the answer and null when nobody answers", async () => {
    setup();
    let api!: ExtensionAPI;
    const runtime = new ExtensionRuntime(vi.fn(), async () => ({
      activate(value) {
        api = value;
      },
    }));
    await runtime.reconcile([entry]);
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({ answered: true, data: { opened: true } }),
            {
              headers: { "Content-Type": "application/json" },
            },
          ),
      ),
    );
    await expect(
      api.host.request({
        type: "open-file",
        data: { path: "a.ts" },
        timeoutMs: 5000,
      }),
    ).resolves.toEqual({ opened: true });
    expect(fetch).toHaveBeenCalledWith(
      "/coddy/extensions/demo/request",
      expect.objectContaining({ method: "POST" }),
    );
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ answered: false }), {
            headers: { "Content-Type": "application/json" },
          }),
      ),
    );
    await expect(api.host.request({ type: "ping" })).resolves.toBeNull();
    await expect(api.host.request({ type: "Bad Type" })).rejects.toThrow(
      /event type/,
    );
    await expect(
      api.host.request({ type: "ping", timeoutMs: 0 }),
    ).rejects.toThrow(/timeoutMs/);
  });

  test("anchors and the host channel answer through the API", async () => {
    setup();
    const { api, runtime } = await activateOne(() => {});
    const box = document.createElement("div");
    runtime.anchors.attach("composer.actions", box);
    expect(api.ui.anchor("composer.actions")).toBe(box);
    expect(api.ui.anchor("nope")).toBeNull();
    const seen: (HTMLElement | null)[] = [];
    api.ui.onAnchor("composer.actions", (el) => seen.push(el));
    expect(seen).toEqual([box]);
    await expect(api.host.capabilities()).resolves.toEqual(["ui.page"]);
  });
});
