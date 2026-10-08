import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { useProviderModels } from "./useProviderModels";

afterEach(() => {
  vi.unstubAllGlobals();
});

function answer(models: unknown[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({ ok: true, models }),
    })),
  );
}

async function listed(models: unknown[]) {
  answer(models);
  const { result } = renderHook(() => useProviderModels());
  await act(async () => {
    await result.current.fetchModels({ name: "lab", type: "coddy" });
  });
  return result.current.models;
}

test("a coddy listing's capabilities of a model reach the list", async () => {
  const models = await listed([
    {
      id: "terra",
      context_window: 200000,
      revision: "r1",
      multimodal: true,
      reasoning_levels: ["low", "high"],
      reasoning_default: "low",
      allow_reasoning_off: true,
    },
  ]);
  expect(models).toEqual([
    {
      id: "terra",
      context_window: 200000,
      multimodal: true,
      reasoning_levels: ["low", "high"],
      reasoning_default: "low",
      allow_reasoning_off: true,
    },
  ]);
});

test("what the listing does not say stays absent, and a false is not carried", async () => {
  const models = await listed([
    { id: "plain" },
    {
      id: "falsy",
      multimodal: false,
      allow_reasoning_off: false,
      reasoning_levels: [],
      reasoning_default: "",
    },
    { id: "odd", reasoning_levels: ["low", 3, "", null, "high"] },
  ]);
  expect(models).toEqual([
    { id: "plain" },
    { id: "falsy" },
    { id: "odd", reasoning_levels: ["low", "high"] },
  ]);
});
