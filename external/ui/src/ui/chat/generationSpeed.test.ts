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

test("only completed turns that wrote something produce a summary", () => {
  const reading = { outputTokens: 500, tokensPerSecond: 9.8 };
  expect(generationSummaryFromFrame(reading)).toBeNull();
  expect(generationSummaryFromFrame({ ...reading, completed: true })).toEqual(
    reading,
  );
  expect(
    generationSummaryFromFrame({ completed: true, outputTokens: 0 }),
  ).toBeNull();
});

// A turn with no generation window to measure (an agent behind an acp row)
// still has a clock and a count: its line stands, without the speed. Dropping
// the frame left such a turn with no line at all under its answer.
test("a completed turn nobody could measure keeps its clock and its count", () => {
  expect(
    generationSummaryFromFrame({
      completed: true,
      outputTokens: 890,
      tokensPerSecond: null,
      elapsedMs: 111_000,
    }),
  ).toEqual({ outputTokens: 890, elapsedMs: 111_000 });
});

test("a completed frame carries how long the turn ran", () => {
  expect(
    generationSummaryFromFrame({
      completed: true,
      outputTokens: 500,
      tokensPerSecond: 9.8,
      elapsedMs: 81_000,
    }),
  ).toEqual({ outputTokens: 500, tokensPerSecond: 9.8, elapsedMs: 81_000 });
  expect(
    generationSummaryFromFrame({
      completed: true,
      outputTokens: 500,
      tokensPerSecond: 9.8,
    }),
  ).toEqual({ outputTokens: 500, tokensPerSecond: 9.8 });
});
