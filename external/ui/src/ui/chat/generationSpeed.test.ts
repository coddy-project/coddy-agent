import { expect, test } from "vitest";
import {
  formatGenerationSpeed,
  generationSummaryFromFrame,
} from "./generationSpeed";

test.each([
  [0.125, "0.13"],
  [1.999, "2.00"],
  [2, "2.0"],
  [9.8, "9.8"],
  [10, "10"],
  [10.6, "11"],
])(
  "formats %s tokens per second on a fixed decimal point",
  (value, expected) => {
    expect(formatGenerationSpeed(value)).toBe(expected);
  },
);

test("only completed measured turns produce a summary", () => {
  const reading = { outputTokens: 500, tokensPerSecond: 9.8 };
  expect(generationSummaryFromFrame(reading)).toBeNull();
  expect(generationSummaryFromFrame({ ...reading, completed: true })).toEqual(
    reading,
  );
  expect(
    generationSummaryFromFrame({
      ...reading,
      completed: true,
      tokensPerSecond: null,
    }),
  ).toBeNull();
});
