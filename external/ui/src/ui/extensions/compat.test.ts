import { describe, expect, test } from "vitest";
import { detectEngine, LEGACY_CHROMIUM_MAX } from "./compat";

describe("engine tier", () => {
  test.each([
    ["Mozilla/5.0 Chrome/98.0.4758.102", "legacy", 98],
    ["Mozilla/5.0 Chrome/102.0.5005.63", "legacy", 102],
    ["Mozilla/5.0 Chrome/110.0.0.0", "legacy", 110],
    ["Mozilla/5.0 Chrome/111.0.5563.65", "modern", 111],
    ["Mozilla/5.0 Chrome/131.0.0.0 Safari/537.36", "modern", 131],
  ] as const)("detects %s as %s", (ua, tier, major) => {
    const info = detectEngine(ua);
    expect(info.tier).toBe(tier);
    expect(info.chromiumMajor).toBe(major);
  });

  test("a non-Chromium engine stays modern and says no version", () => {
    const firefox = detectEngine("Mozilla/5.0 Firefox/128.0");
    expect(firefox.tier).toBe("modern");
    expect(firefox.chromiumMajor).toBeNull();
    expect(detectEngine("").tier).toBe("modern");
  });

  test("the legacy boundary is documented in the constant", () => {
    expect(LEGACY_CHROMIUM_MAX).toBe(110);
    expect(detectEngine(`Chrome/${LEGACY_CHROMIUM_MAX}.0`).tier).toBe("legacy");
    expect(detectEngine(`Chrome/${LEGACY_CHROMIUM_MAX + 1}.0`).tier).toBe(
      "modern",
    );
  });
});
