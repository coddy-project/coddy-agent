import {
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  act,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { useProviderModels } from "./useProviderModels";
import { ProviderModelList } from "./ProviderModelList";
import {
  buildOnboardingConfig,
  PROVIDER_PRESETS,
} from "../onboarding/providerConfig";

const row = {
  name: "kimi",
  type: "kimi",
  mode: "subscription",
  region: "global",
  kimi_identity: "account-a",
};
const entries = [
  {
    id: "vision",
    context_window: 120000,
    kimi_protocol: "kimi",
    kimi_image_input: true,
    kimi_thinking_type: "enabled",
    kimi_reasoning_levels: ["low", "high"],
    kimi_default_reasoning: "high",
  },
  { id: "text", kimi_protocol: "kimi", kimi_image_input: false },
  { id: "unknown", kimi_protocol: "kimi" },
];
const preset = PROVIDER_PRESETS.find((p) => p.id === "kimi")!;
const input = {
  preset,
  apiKey: "",
  apiBase: "",
  proxy: "",
  modelId: "vision",
  baseDoc: {},
  managedLogin: true,
  kimiMode: "subscription" as const,
  kimiRegion: "global" as const,
  kimiIdentity: "account-a",
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function stub() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({ ok: true, models: entries }),
    })),
  );
}

async function listing() {
  const hook = renderHook(() => useProviderModels());
  let result: Awaited<ReturnType<typeof hook.result.current.fetchModels>>;
  await act(async () => {
    result = await hook.result.current.fetchModels(row);
  });
  return result!.models;
}

test("normalization preserves Kimi capabilities and source including identity", async () => {
  stub();
  const models = await listing();
  expect(models[0]).toMatchObject({
    ...entries[0],
    kimi_source: {
      name: "kimi",
      mode: "subscription",
      region: "global",
      identity: "account-a",
    },
  });
  expect(models[1]).toHaveProperty("kimi_image_input", false);
  expect(models[2]).not.toHaveProperty("kimi_image_input");
});

test("onboarding seeds the current listing and leaves an existing model untouched", async () => {
  stub();
  const catalog = await listing();
  const doc = buildOnboardingConfig({ ...input, catalog });
  expect((doc.models as object[])[0]).toMatchObject({
    model: "kimi/vision",
    multimodal: true,
    max_context_tokens: 120000,
    reasoning_levels: ["low", "high"],
    reasoning_default: "high",
    kimi_metadata: {
      model: "kimi/vision",
      protocol: "kimi",
      mode: "subscription",
      region: "global",
    },
  });
  const old = {
    model: "kimi/vision",
    multimodal: false,
    max_tokens: 123,
    temperature: 0.8,
  };
  expect(
    buildOnboardingConfig({ ...input, catalog, baseDoc: { models: [old] } })
      .models,
  ).toEqual([old]);
  expect(
    (
      buildOnboardingConfig({ ...input, modelId: "text", catalog })
        .models as Record<string, unknown>[]
    )[0]!.multimodal,
  ).toBe(false);
});

test("onboarding rejects a catalog from another identity, mode, region or name", async () => {
  stub();
  const catalog = await listing();
  for (const changed of [
    { kimiIdentity: "account-b" },
    { kimiMode: "platform" as const },
    { kimiRegion: "china" as const },
  ]) {
    const model = (
      buildOnboardingConfig({ ...input, ...changed, catalog }).models as Record<
        string,
        unknown
      >[]
    )[0]!;
    expect(model.kimi_metadata).toBeUndefined();
    expect(model.reasoning_levels).toBeUndefined();
    expect(model.multimodal).not.toBe(true);
    expect(model.max_context_tokens).toBeUndefined();
  }
  const wrongName = catalog.map((m) => ({
    ...m,
    kimi_source: { ...m.kimi_source!, name: "other" },
  }));
  expect(
    (
      buildOnboardingConfig({ ...input, catalog: wrongName }).models as Record<
        string,
        unknown
      >[]
    )[0]!.kimi_metadata,
  ).toBeUndefined();
});

test("settings forwards the validated complete listing and rejects stale identity", async () => {
  stub();
  const add = vi.fn();
  const view = render(
    <ProviderModelList
      provider={row}
      existingModels={[]}
      onAddModel={add}
      onRemoveModel={() => {}}
    />,
  );
  await screen.findByTestId("provider-model-vision");
  fireEvent.click(screen.getByTestId("provider-model-toggle-vision"));
  expect(add.mock.calls[0]![3]).toBe(true);
  expect(add.mock.calls[0]![5]).toMatchObject({
    ...entries[0],
    kimi_source: { identity: "account-a" },
  });
  view.rerender(
    <ProviderModelList
      provider={{ ...row, kimi_identity: "account-b" }}
      existingModels={[]}
      onAddModel={add}
      onRemoveModel={() => {}}
    />,
  );
  fireEvent.click(screen.getByTestId("provider-model-toggle-vision"));
  expect(add.mock.calls[1]![1]).toBeUndefined();
  expect(add.mock.calls[1]![5]).toBeUndefined();
});
