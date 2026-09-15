import { describe, expect, it } from "vitest";
import { pinDropIndex, reorderPins } from "./reorderPins";

describe("reorderPins", () => {
  const ids = ["a", "b", "c", "d"];

  it("moves a row down", () => {
    expect(reorderPins(ids, 0, 2)).toEqual(["b", "c", "a", "d"]);
  });

  it("moves a row up", () => {
    expect(reorderPins(ids, 3, 1)).toEqual(["a", "d", "b", "c"]);
  });

  it("leaves the list alone when nothing moved", () => {
    expect(reorderPins(ids, 2, 2)).toEqual(ids);
  });

  it("refuses an index that is not in the list", () => {
    expect(reorderPins(ids, -1, 2)).toEqual(ids);
    expect(reorderPins(ids, 1, 9)).toEqual(ids);
  });

  it("never mutates what it was given", () => {
    const original = [...ids];
    reorderPins(ids, 0, 3);
    expect(ids).toEqual(original);
  });
});

describe("pinDropIndex", () => {
  const rects = [
    { top: 0, height: 40 },
    { top: 40, height: 40 },
    { top: 80, height: 40 },
  ];

  it("reads the top half of a row as that row's slot", () => {
    expect(pinDropIndex(rects, 5)).toBe(0);
    expect(pinDropIndex(rects, 45)).toBe(1);
  });

  it("reads the bottom half as the slot after it", () => {
    expect(pinDropIndex(rects, 35)).toBe(1);
    expect(pinDropIndex(rects, 75)).toBe(2);
  });

  it("clamps past the end", () => {
    expect(pinDropIndex(rects, 500)).toBe(2);
  });

  it("answers for an empty list without reaching into it", () => {
    expect(pinDropIndex([], 10)).toBe(0);
  });
});
