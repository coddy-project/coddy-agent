import { expect, test } from "vitest";
import { totpPeriodLeft } from "./totpPeriod";

const SECOND = 1000;
const PERIOD = 30 * SECOND;

test("the start of a period is a full period left", () => {
  expect(totpPeriodLeft(0)).toEqual({ seconds: 30, fraction: 1 });
  expect(totpPeriodLeft(PERIOD)).toEqual({ seconds: 30, fraction: 1 });
  expect(totpPeriodLeft(4 * PERIOD)).toEqual({ seconds: 30, fraction: 1 });
});

test("the bar and the label follow the wall clock, never drift", () => {
  expect(totpPeriodLeft(15 * SECOND)).toEqual({ seconds: 15, fraction: 0.5 });
  const last = totpPeriodLeft(PERIOD - 1);
  expect(last.seconds).toBe(1);
  expect(last.fraction).toBeCloseTo(1 / PERIOD, 6);
});

test("a boundary reads as a new full period, never as zero", () => {
  for (const at of [PERIOD - 1, PERIOD, PERIOD + 1]) {
    const left = totpPeriodLeft(at);
    expect(left.seconds).toBeGreaterThanOrEqual(1);
    expect(left.seconds).toBeLessThanOrEqual(30);
    expect(left.fraction).toBeGreaterThan(0);
    expect(left.fraction).toBeLessThanOrEqual(1);
  }
  expect(totpPeriodLeft(PERIOD + 1).seconds).toBe(30);
});

test("the label is the ceiling of the fill's exact position", () => {
  for (const at of [1, 7 * SECOND + 999, 29 * SECOND, PERIOD - 1]) {
    const left = totpPeriodLeft(at);
    expect(Math.ceil(left.fraction * 30)).toBe(left.seconds);
  }
});

test("the period length is an argument", () => {
  expect(totpPeriodLeft(0, 60)).toEqual({ seconds: 60, fraction: 1 });
  expect(totpPeriodLeft(30 * SECOND, 60)).toEqual({
    seconds: 30,
    fraction: 0.5,
  });
});
