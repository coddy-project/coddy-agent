import { describe, expect, test } from "vitest";
import { parseDiffPatch } from "../messages/parseDiff";
import { toSplitRows, toUnifiedRows } from "./diffRows";

function hunksOf(patch: string) {
  return parseDiffPatch(patch, "f.txt").hunks;
}

const ONE_HUNK = [
  "--- a/f.txt",
  "+++ b/f.txt",
  "@@ -1,3 +1,3 @@",
  " keep",
  "-old",
  "+new",
  " tail",
].join("\n");

describe("toUnifiedRows", () => {
  test("keeps every diff line in order and adds no filler text", () => {
    const rows = toUnifiedRows(hunksOf(ONE_HUNK));
    expect(rows.map((r) => (r.kind === "line" ? r.line.kind : "gap"))).toEqual([
      "ctx",
      "del",
      "add",
      "ctx",
    ]);
  });

  test("separates hunks with a gap row rather than a counted line", () => {
    const patch = [
      "--- a/f.txt",
      "+++ b/f.txt",
      "@@ -1,1 +1,1 @@",
      "-a",
      "+b",
      "@@ -40,1 +40,1 @@",
      "-c",
      "+d",
    ].join("\n");
    const rows = toUnifiedRows(hunksOf(patch));
    const gaps = rows.filter((r) => r.kind === "gap");
    expect(gaps).toHaveLength(1);
    // The gap sits between the hunks, never at either end.
    expect(rows[0]!.kind).toBe("line");
    expect(rows[rows.length - 1]!.kind).toBe("line");
    expect(rows[2]!.kind).toBe("gap");
  });
});

describe("toSplitRows", () => {
  test("pairs a replaced line side by side", () => {
    const rows = toSplitRows(hunksOf(ONE_HUNK));
    expect(rows).toHaveLength(3);
    const replaced = rows[1]!;
    if (replaced.kind !== "pair") throw new Error("want a pair row");
    expect(replaced.left?.content).toBe("old");
    expect(replaced.right?.content).toBe("new");
  });

  test("shows a context line on both sides", () => {
    const rows = toSplitRows(hunksOf(ONE_HUNK));
    const first = rows[0]!;
    if (first.kind !== "pair") throw new Error("want a pair row");
    expect(first.left?.content).toBe("keep");
    expect(first.right?.content).toBe("keep");
  });

  test("pads the shorter side when a run is uneven", () => {
    const patch = [
      "--- a/f.txt",
      "+++ b/f.txt",
      "@@ -1,3 +1,1 @@",
      "-one",
      "-two",
      "-three",
      "+only",
    ].join("\n");
    const rows = toSplitRows(hunksOf(patch));
    expect(rows).toHaveLength(3);
    const pairs = rows.map((r) => (r.kind === "pair" ? r : null));
    expect(pairs[0]?.left?.content).toBe("one");
    expect(pairs[0]?.right?.content).toBe("only");
    // Nothing on the new side to line these up against.
    expect(pairs[1]?.right).toBeNull();
    expect(pairs[2]?.right).toBeNull();
    expect(pairs[2]?.left?.content).toBe("three");
  });

  test("puts a pure addition on the new side only", () => {
    const patch = [
      "--- a/f.txt",
      "+++ b/f.txt",
      "@@ -0,0 +1,2 @@",
      "+alpha",
      "+beta",
    ].join("\n");
    const rows = toSplitRows(hunksOf(patch));
    expect(rows).toHaveLength(2);
    for (const row of rows) {
      if (row.kind !== "pair") throw new Error("want pair rows");
      expect(row.left).toBeNull();
      expect(row.right).not.toBeNull();
    }
  });

  test("puts a pure deletion on the old side only", () => {
    const patch = [
      "--- a/f.txt",
      "+++ b/f.txt",
      "@@ -1,2 +0,0 @@",
      "-alpha",
      "-beta",
    ].join("\n");
    const rows = toSplitRows(hunksOf(patch));
    expect(rows).toHaveLength(2);
    for (const row of rows) {
      if (row.kind !== "pair") throw new Error("want pair rows");
      expect(row.right).toBeNull();
      expect(row.left).not.toBeNull();
    }
  });

  test("does not let a run leak across a context line", () => {
    const patch = [
      "--- a/f.txt",
      "+++ b/f.txt",
      "@@ -1,4 +1,4 @@",
      "-old",
      " middle",
      "+new",
    ].join("\n");
    const rows = toSplitRows(hunksOf(patch));
    // The deletion and the addition are separated by context, so they must not
    // be paired onto one row.
    expect(rows).toHaveLength(3);
    const first = rows[0]!;
    const last = rows[2]!;
    if (first.kind !== "pair" || last.kind !== "pair") throw new Error("want pairs");
    expect(first.right).toBeNull();
    expect(last.left).toBeNull();
  });
});
