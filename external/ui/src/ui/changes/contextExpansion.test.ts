import { describe, expect, it } from "vitest";
import {
  CONTEXT_STEP,
  EMPTY_DIFF_CONTEXT,
  expandAllContext,
  expandContextAt,
  expandDiffContext,
} from "./contextExpansion";

const beforeLines = Array.from({ length: 60 }, (_, i) => `line ${i + 1}`);
const afterLines = [
  ...beforeLines.slice(0, 16),
  "inserted",
  ...beforeLines.slice(16),
];
const before = `${beforeLines.join("\n")}\n`;
const after = `${afterLines.join("\n")}\n`;
const patch = `@@ -16,1 +16,2 @@ first
 line 16
+inserted
@@ -42,1 +43,1 @@ second
-line 42
+changed`;

const render = (state = EMPTY_DIFF_CONTEXT) =>
  expandDiffContext(patch, before, after, state);
const context = (oldNo: number, newNo = oldNo) => ({
  kind: "ctx",
  oldNo,
  newNo,
  content: `line ${oldNo}`,
});

describe("diff context expansion", () => {
  it("increments each direction immutably in steps", () => {
    expect(CONTEXT_STEP).toBe(10);
    const up = expandContextAt(EMPTY_DIFF_CONTEXT, 1, "up");
    const both = expandContextAt(up, 1, "both");
    expect(EMPTY_DIFF_CONTEXT).toEqual({ gaps: {}, all: false });
    expect(both).toEqual({ gaps: { 1: { up: 20, down: 10 } }, all: false });
    expect(expandAllContext(both)).toEqual({ ...both, all: true });
  });

  it.each(["up", "down", "both"] as const)(
    "expands ten lines %s",
    (direction) => {
      const result = render(expandContextAt(EMPTY_DIFF_CONTEXT, 1, direction));
      const up = direction !== "down";
      const down = direction !== "up";
      expect(result.gaps[1]).toEqual({
        total: 25,
        remaining: 25 - (up ? 10 : 0) - (down ? 10 : 0),
        canUp: true,
        canDown: true,
      });
      expect(result.hunks[0]!.lines.slice(2)).toEqual(
        down
          ? Array.from({ length: 10 }, (_, i) => context(17 + i, 18 + i))
          : [],
      );
      expect(result.hunks[1]!.lines.slice(0, up ? 10 : 0)).toEqual(
        up ? Array.from({ length: 10 }, (_, i) => context(32 + i, 33 + i)) : [],
      );
      expect(result.hunks.map((h) => h.header)).toEqual([
        "@@ -16,1 +16,2 @@ first",
        "@@ -42,1 +43,1 @@ second",
      ]);
    },
  );

  it("clamps repeated clicks and overlapping ends without duplicate rows", () => {
    let state = expandContextAt(EMPTY_DIFF_CONTEXT, 1, "both");
    state = expandContextAt(state, 1, "both");
    const result = render(state);
    expect(result.gaps[1]).toEqual({
      total: 25,
      remaining: 0,
      canUp: false,
      canDown: false,
    });
    const lines = [
      ...result.hunks[0]!.lines.slice(2),
      ...result.hunks[1]!.lines.slice(0, -2),
    ];
    expect(lines).toEqual(
      Array.from({ length: 25 }, (_, i) => context(17 + i, 18 + i)),
    );
  });

  it("expands leading and trailing gaps at their adjacent hunks", () => {
    let state = expandContextAt(EMPTY_DIFF_CONTEXT, 0, "up");
    state = expandContextAt(state, 2, "down");
    const result = render(state);
    expect(result.gaps.map((g) => g.total)).toEqual([15, 25, 18]);
    expect(result.gaps[0]).toMatchObject({ canUp: true, canDown: false });
    expect(result.gaps[2]).toMatchObject({ canUp: false, canDown: true });
    expect(result.hunks[0]!.lines.slice(0, 10)).toEqual(
      Array.from({ length: 10 }, (_, i) => context(6 + i)),
    );
    expect(result.hunks[1]!.lines.slice(-10)).toEqual(
      Array.from({ length: 10 }, (_, i) => context(43 + i, 44 + i)),
    );
  });

  it("expands every gap including file ends", () => {
    const result = render(expandAllContext(EMPTY_DIFF_CONTEXT));
    expect(result.gaps.map((g) => g.remaining)).toEqual([0, 0, 0]);
    const lines = result.hunks.flatMap((h) => h.lines);
    expect(lines.filter((line) => line.kind === "ctx")).toHaveLength(59);
    expect(lines[0]).toEqual(context(1));
    expect(lines.at(-1)).toEqual(context(60, 61));
  });

  it("handles empty input", () => {
    expect(expandDiffContext("", "", "", EMPTY_DIFF_CONTEXT)).toEqual({
      hunks: [],
      gaps: [{ total: 0, remaining: 0, canUp: false, canDown: false }],
    });
  });
});
