import { describe, expect, it } from "vitest";
import { overrideTexts } from "./SettingsSection";

const schema = { type: "string", title: "Field" };

describe("Kimi provider fields", () => {
  it("shows mode and region only for Kimi and keeps credentials searchable", () => {
    expect(overrideTexts("providers", "mode", { type: "openai" }, schema)).toBe(
      false,
    );
    expect(
      overrideTexts("providers", "region", { type: "openai" }, schema),
    ).toBe(false);
    expect(
      overrideTexts("providers", "api_base", { type: "kimi" }, schema),
    ).toBe(false);
    for (const path of ["api_key", "api_key_command"]) {
      expect(
        overrideTexts("providers", path, { type: "kimi" }, schema),
      ).toBeNull();
    }
    expect(
      overrideTexts(
        "providers",
        "api_base",
        { type: "kimi", mode: "platform" },
        schema,
      ),
    ).toBe(false);
    expect(
      overrideTexts(
        "providers",
        "api_key",
        { type: "kimi", mode: "platform" },
        schema,
      ),
    ).toBeNull();
  });
});
