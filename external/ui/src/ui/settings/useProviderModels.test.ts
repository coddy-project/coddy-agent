import { expect, test, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { providerModelsRequest, useProviderModels } from "./useProviderModels";

test("Kimi discovery sends the mode and region from the unsaved row", () => {
  expect(
    providerModelsRequest({
      name: "kimi",
      type: "kimi",
      mode: "platform",
      region: "china",
    }),
  ).toMatchObject({ mode: "platform", region: "china" });
  expect(
    providerModelsRequest({ name: "kimi", type: "kimi", mode: "", region: "" }),
  ).toMatchObject({ mode: "", region: "" });
});

test("Kimi discovery carries the draft identity", () => {
  expect(
    providerModelsRequest({
      name: "kimi",
      type: "kimi",
      kimi_identity: "0123456789abcdef0123456789abcdef",
    }),
  ).toMatchObject({ kimi_identity: "0123456789abcdef0123456789abcdef" });
});

test("discovery leaves Kimi settings out of other provider requests", () => {
  const request = providerModelsRequest({ name: "openai", type: "openai" });
  expect(request).not.toHaveProperty("mode");
  expect(request).not.toHaveProperty("region");
});

test("preserves known image output without catalog windows", async () => {
  const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue({
    ok: true,
    json: async () => ({
      ok: true,
      models: [
        { id: "yes", catalog: { image_output: true } },
        { id: "no", context_window: 100, catalog: { image_output: false } },
        { id: "unknown", catalog: { context_window: 200 } },
      ],
    }),
  } as Response);
  try {
    const { result } = renderHook(() => useProviderModels());
    let response: Awaited<ReturnType<typeof result.current.fetchModels>>;
    await act(async () => {
      response = await result.current.fetchModels({
        name: "demo",
        type: "openai",
      });
    });
    expect(response!.models).toEqual([
      { id: "yes", catalog: { image_output: true } },
      { id: "no", context_window: 100, catalog: { image_output: false } },
      { id: "unknown", catalog: { context_window: 200 } },
    ]);
  } finally {
    fetchMock.mockRestore();
  }
});
