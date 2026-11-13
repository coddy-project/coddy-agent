import { expect, test } from "vitest";
import { providerModelsRequest } from "./useProviderModels";

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
