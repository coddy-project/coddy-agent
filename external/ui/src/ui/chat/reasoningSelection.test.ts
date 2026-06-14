import { expect, test } from "vitest";
import { pickReasoningLevel } from "./reasoningSelection";

const levels = ["minimal", "low", "medium", "high"] as const;

test("no levels yields empty (model has no reasoning)", () => {
  expect(
    pickReasoningLevel({ levels: [], cookie: "high", modelDefault: "high" }),
  ).toBe("");
});

test("stored session level wins over cookie and default", () => {
  expect(
    pickReasoningLevel({
      levels,
      sessionLevel: "low",
      cookie: "high",
      modelDefault: "medium",
    }),
  ).toBe("low");
});

test("new chat prefers cookie over model default", () => {
  expect(
    pickReasoningLevel({ levels, cookie: "high", modelDefault: "medium" }),
  ).toBe("high");
});

test("falls back to model default when cookie absent/invalid", () => {
  expect(
    pickReasoningLevel({ levels, cookie: "bogus", modelDefault: "low" }),
  ).toBe("low");
});

test("falls back to medium when nothing else valid", () => {
  expect(pickReasoningLevel({ levels, cookie: null })).toBe("medium");
});

test("falls back to first level when medium not offered", () => {
  expect(
    pickReasoningLevel({ levels: ["low", "high"], cookie: null }),
  ).toBe("low");
});

test("session level invalid for model falls through to cookie", () => {
  expect(
    pickReasoningLevel({ levels, sessionLevel: "ultra", cookie: "high" }),
  ).toBe("high");
});
