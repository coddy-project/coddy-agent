import { expect, test } from "vitest";

import {
  buildGutterRows,
  measureLineVisualRows,
} from "./markdownLineGutter";

test("buildGutterRows numbers only first visual row per logical line", () => {
  const rows = buildGutterRows([1, 3, 1]);
  expect(rows).toHaveLength(5);
  expect(rows.filter((r) => r.showNumber).map((r) => r.logicalLine)).toEqual([
    0, 1, 2,
  ]);
  expect(rows[2].showNumber).toBe(false);
  expect(rows[3].showNumber).toBe(false);
});

test("measureLineVisualRows ceil rounds partial lines up", () => {
  const probe = document.createElement("div");
  Object.defineProperty(probe, "offsetHeight", { value: 40, configurable: true });
  expect(measureLineVisualRows("wrapped", 200, 17.4, probe)).toBe(3);
  Object.defineProperty(probe, "offsetHeight", { value: 17, configurable: true });
  expect(measureLineVisualRows("one", 200, 17.4, probe)).toBe(1);
});
