import { expect, test } from "vitest";
import { formatStepDuration } from "./formatStepDuration";

// Milliseconds only while they are the readable unit: "32749ms" asks the reader to
// divide by a thousand. Past three seconds a step is counted in seconds, then in
// minutes and seconds, and a long one in hours and minutes.
test.each([
  [0, "0ms"],
  [12, "12ms"],
  [846, "846ms"],
  [2999, "2999ms"],
  [2999.6, "3s"],
  [3000, "3s"],
  [32749, "32s"],
  [59999, "59s"],
  [60000, "1m 00s"],
  [102000, "1m 42s"],
  [3599999, "59m 59s"],
  [3600000, "1h 00m"],
  [5_025_000, "1h 23m"],
])("%d ms reads as %s", (ms, want) => {
  expect(formatStepDuration(ms)).toBe(want);
});

test("a duration that is not one renders nothing", () => {
  expect(formatStepDuration(Number.NaN)).toBe("");
  expect(formatStepDuration(-1)).toBe("");
  expect(formatStepDuration(Number.POSITIVE_INFINITY)).toBe("");
});
