import { afterEach, expect, test, vi } from "vitest";
import { createHash, webcrypto } from "node:crypto";
import { readFileSync } from "node:fs";
import { ExtensionRuntime, type ExtensionEntry } from "./runtime";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as first from "../../../../../examples/extensions/first-extension/main.js";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as hello from "../../../../../examples/extensions/hello/main.js";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as localApi from "../../../../../examples/extensions/local-api/main.js";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as streamTools from "../../../../../examples/extensions/stream-tools/main.js";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as workflowTools from "../../../../../examples/extensions/workflow-tools/main.js";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as themeLab from "../../../../../examples/extensions/theme-lab/main.js";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as oceanTheme from "../../../../../examples/extensions/ocean-theme/main.js";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as okCoddy from "../../../../../examples/extensions/ok-coddy/main.js";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as okCoddyAddon from "../../../../../examples/extensions/ok-coddy-addon/main.js";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as neuraldeepOnboarding from "../../../../../examples/extensions/neuraldeep-onboarding/main.js";
// @ts-expect-error - JavaScript example, no generated declaration module
import * as neuraldeepStability from "../../../../../examples/extensions/neuraldeep-stability/main.js";

vi.mock("../env/remoteEnv", () => ({
  requireEnvironmentReload: vi.fn(),
  onEnvironmentSwitch: vi.fn(),
}));

const modules = {
  "first-extension": first,
  hello,
  "local-api": localApi,
  "stream-tools": streamTools,
  "workflow-tools": workflowTools,
  "theme-lab": themeLab,
  "ocean-theme": oceanTheme,
  "ok-coddy": okCoddy,
  "ok-coddy-addon": okCoddyAddon,
  "neuraldeep-onboarding": neuraldeepOnboarding,
  "neuraldeep-stability": neuraldeepStability,
};
const runtimes: ExtensionRuntime[] = [];

function manifest(id: string) {
  return JSON.parse(
    readFileSync(`../../examples/extensions/${id}/manifest.json`, "utf8"),
  ) as Record<string, unknown>;
}

function entry(id: string): ExtensionEntry {
  const source = readFileSync(
    `../../examples/extensions/${id}/main.js`,
    "utf8",
  );
  return {
    ...manifest(id),
    sha256: `package-${id}`,
    entry_sha256: createHash("sha256").update(source).digest("hex"),
    enabled: true,
  } as ExtensionEntry;
}

function transport(id: string, writes?: unknown[]) {
  return vi.fn(async (input: string, init?: RequestInit) => {
    const url = String(input);
    if (url.includes("/providers/upsert")) {
      writes?.push(JSON.parse(String(init?.body)));
      return new Response(null, { status: 204 });
    }
    if (url.includes("/coddy/info"))
      return Response.json({ version: "1.4.23" });
    if (url.includes("/coddy/config")) {
      return Response.json({
        providers: [
          {
            name: "neuraldeep",
            type: "neuraldeep",
            api_base: "https://hub.example/v1",
          },
        ],
      });
    }
    if (url.includes("main.js")) {
      return new Response(
        readFileSync(`../../examples/extensions/${id}/main.js`, "utf8"),
      );
    }
    if (url.includes("style.css")) {
      return new Response(
        readFileSync(`../../examples/extensions/${id}/style.css`, "utf8"),
      );
    }
    return new Response("");
  });
}

async function activate(
  id: string,
  runtime?: ExtensionRuntime,
  writes?: unknown[],
) {
  vi.stubGlobal("crypto", webcrypto);
  vi.stubGlobal("fetch", transport(id, writes));
  URL.createObjectURL = vi.fn(() => `blob:${id}`);
  URL.revokeObjectURL = vi.fn();
  const owner =
    runtime ??
    new ExtensionRuntime(
      vi.fn(),
      async () => modules[id as keyof typeof modules],
    );
  await owner.reconcile([entry(id)]);
  await vi.waitFor(() => {
    if (id === "neuraldeep-onboarding") {
      expect(owner.snapshot().menu.length).toBe(1);
    }
  });
  expect(owner.snapshot().errors[id], id).toBeUndefined();
  runtimes.push(owner);
  return owner;
}

afterEach(() => {
  for (const runtime of runtimes.splice(0)) runtime.stop();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

test("every shipped manifest and example activates unchanged", async () => {
  for (const id of Object.keys(modules)) await activate(id);
});

test("the addon uses targeted operations, not a whole-screen reorder", async () => {
  const runtime = await activate("ok-coddy-addon");
  const rows = runtime.composerHost.descriptors("tabs", [
    { id: "mode", key: "host/mode", placement: "tabs" },
    { id: "model", key: "host/model", placement: "tabs" },
    { id: "future", key: "host/future", placement: "tabs" },
    { id: "reasoning", key: "host/reasoning", placement: "tabs" },
  ]);
  expect(rows.map((row) => row.key)).toEqual([
    "host/mode",
    "host/model",
    "ok-coddy-addon/disco-quick",
    "host/future",
    "host/reasoning",
  ]);
  const settings = runtime.settingsRowAlters.rows[0]!.run(
    [
      { id: "language", label: "Language" },
      { id: "future", label: "Future" },
    ] as never,
    { section: "appearance" },
  );
  expect(settings.map((row) => row.id)).toEqual(["language", "future"]);
  expect(settings[0]!.label).toBe("Interface language");
});

test("the addon invokes the panel owner's trick without touching its DOM", async () => {
  let root: HTMLElement | null = null;
  const runtime = new ExtensionRuntime(
    vi.fn(),
    async (url) => modules[url.replace("blob:", "") as keyof typeof modules],
    vi.fn(),
    (panel) => {
      root = document.createElement("div");
      panel.mount(root, panel.api);
    },
  );
  runtimes.push(runtime);
  vi.stubGlobal("crypto", webcrypto);
  const primary = transport("ok-coddy");
  const addon = transport("ok-coddy-addon");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) =>
      String(input).includes("ok-coddy-addon")
        ? addon(input, init)
        : primary(input, init),
    ),
  );
  const addonSize = readFileSync(
    "../../examples/extensions/ok-coddy-addon/main.js",
    "utf8",
  ).length;
  URL.createObjectURL = vi.fn(
    (blob: Blob) =>
      `blob:${blob.size === addonSize ? "ok-coddy-addon" : "ok-coddy"}`,
  );
  URL.revokeObjectURL = vi.fn();
  await runtime.reconcile([entry("ok-coddy"), entry("ok-coddy-addon")]);
  expect(runtime.snapshot().errors).toEqual({});
  runtime
    .snapshot()
    .menu.find((row) => row.owner === "ok-coddy")!
    .onSelect();
  runtime.hookSlots.invoke("ok-coddy/tricks", { name: "disco" });
  expect(root!.querySelector(".ok-duck")!.getAttribute("data-trick")).toBe(
    "disco",
  );
  root!.remove();
});

test("onboarding writes provider and model once, by name", async () => {
  let root: HTMLElement | null = null;
  const runtime = new ExtensionRuntime(
    vi.fn(),
    async () => neuraldeepOnboarding,
    vi.fn(),
    (panel) => {
      root = document.createElement("div");
      panel.mount(root, panel.api);
    },
  );
  runtimes.push(runtime);
  const writes: unknown[] = [];
  await activate("neuraldeep-onboarding", runtime, writes);
  runtime.snapshot().menu[0]!.onSelect();
  const input = root!.querySelector("input")! as HTMLInputElement;
  input.value = "sk-test";
  const create = [...root!.querySelectorAll("button")].find(
    (button) => button.textContent === "Create provider",
  )!;
  create.click();
  await vi.waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    provider: { name: "neuraldeep", type: "neuraldeep", api_key: "sk-test" },
    models: [{ model: "neuraldeep/qwen3.6-35b-a3b" }],
  });
  expect(JSON.stringify(writes[0])).not.toMatch(/providers\.\d/);
  root!.remove();
});

test("the stream example transforms semantic deltas without parsing raw SSE", async () => {
  const runtime = await activate("stream-tools");
  const transformed = runtime.incoming.process({
    sessionId: "s1",
    transport: "post",
    event: "response.output_text.delta",
    id: "1",
    data: '{"delta":"hello"}',
  });
  expect(JSON.parse(transformed)).toEqual({ delta: "HELLO" });
});
