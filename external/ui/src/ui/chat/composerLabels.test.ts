import { expect, test } from "vitest";
import {
  clamp01,
  displayLlmId,
  displayModeLabel,
  fmtBytes,
  fmtInt,
} from "./composerLabels";

const t = (key: string, params?: Record<string, string | number>) =>
  `${key}:${params ? Object.values(params).join(",") : ""}`;

test("fmtBytes picks the B/KB/MB bucket", () => {
  expect(fmtBytes(512, t)).toBe("composer.bytesB:512");
  expect(fmtBytes(2048, t)).toBe("composer.bytesKB:2.0");
  expect(fmtBytes(3 * 1024 * 1024, t)).toBe("composer.bytesMB:3.0");
});

test("clamp01 clamps and rejects non-finite values", () => {
  expect(clamp01(0.5)).toBe(0.5);
  expect(clamp01(-1)).toBe(0);
  expect(clamp01(2)).toBe(1);
  expect(clamp01(NaN)).toBe(0);
});

test("fmtInt truncates and floors at zero", () => {
  expect(fmtInt(12.9)).toBe("12");
  expect(fmtInt(-3)).toBe("0");
  expect(fmtInt(undefined)).toBe("0");
});

test("displayLlmId keeps the segment after the last slash", () => {
  expect(displayLlmId("openai/gpt-5")).toBe("gpt-5");
  expect(displayLlmId("plain-model")).toBe("plain-model");
  expect(displayLlmId("", "Model")).toBe("Model");
});

test("displayModeLabel localizes agent/plan and shortens other ids", () => {
  expect(displayModeLabel("agent", t)).toBe("composer.modeAgent:");
  expect(displayModeLabel("plan", t)).toBe("composer.modePlan:");
  expect(displayModeLabel("ask", t)).toBe("composer.modeAsk:");
  expect(displayModeLabel("acme/custom", t)).toBe("custom");
  expect(displayModeLabel("", t)).toBe("composer.modeAgent:");
});
