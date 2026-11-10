import { describe, expect, it } from "vitest";
import { buildOnboardingConfig, PROVIDER_PRESETS } from "./providerConfig";

describe("Kimi onboarding", () => {
  it("writes the generated identity into the provider row", () => {
    const preset = PROVIDER_PRESETS.find((p) => p.id === "kimi")!;
    const doc = buildOnboardingConfig({
      preset,
      apiKey: "",
      apiBase: "",
      proxy: "",
      modelId: "",
      baseDoc: {},
      catalog: [],
      managedLogin: true,
      kimiMode: "subscription",
      kimiRegion: "global",
      kimiIdentity: "0123456789abcdef0123456789abcdef",
    });
    expect(doc.providers).toEqual([
      expect.objectContaining({
        kimi_identity: "0123456789abcdef0123456789abcdef",
      }),
    ]);
  });
  it("offers Kimi and saves each mode and region", () => {
    const preset = PROVIDER_PRESETS.find((item) => item.id === "kimi");
    expect(preset).toBeDefined();
    if (!preset) return;
    for (const mode of ["subscription", "platform"] as const) {
      for (const region of ["global", "china"] as const) {
        const result = buildOnboardingConfig({
          preset,
          apiKey: mode === "platform" ? "test-key" : "",
          apiBase: "",
          proxy: "",
          modelId: "",
          baseDoc: {},
          catalog: [],
          managedLogin: false,
          kimiMode: mode,
          kimiRegion: region,
        });
        expect(result.providers).toEqual([
          expect.objectContaining({ name: "kimi", type: "kimi", mode, region }),
        ]);
      }
    }
  });
});
