import { describe, expect, test } from "vitest";
import {
  allRowsSelected,
  formatRowTimestamp,
  formatTokenCount,
  rowTotalTokens,
  selectionWithout,
  someRowsSelected,
  toggleAllRows,
  toggleSelected,
  workspaceBasename,
  type SessionManagerRow,
} from "./sessionManagerRows";

const rows: SessionManagerRow[] = [
  { id: "sess_a" },
  { id: "sess_b" },
  { id: "sess_c" },
];

describe("selection", () => {
  test("toggling one id leaves the original set alone", () => {
    const before = new Set(["sess_a"]);
    const after = toggleSelected(before, "sess_b", true);
    expect([...after].sort()).toEqual(["sess_a", "sess_b"]);
    expect([...before]).toEqual(["sess_a"]);
    expect([...toggleSelected(after, "sess_a", false)]).toEqual(["sess_b"]);
  });

  test("the header checkbox reports all, some or none of the rendered rows", () => {
    expect(allRowsSelected(rows, new Set())).toBe(false);
    expect(someRowsSelected(rows, new Set())).toBe(false);
    expect(someRowsSelected(rows, new Set(["sess_a"]))).toBe(true);
    const all = new Set(["sess_a", "sess_b", "sess_c"]);
    expect(allRowsSelected(rows, all)).toBe(true);
    expect(someRowsSelected(rows, all)).toBe(false);
  });

  test("an empty table never reads as fully selected", () => {
    expect(allRowsSelected([], new Set())).toBe(false);
  });

  test("unticking the header touches only the rendered rows", () => {
    const selected = new Set(["sess_a", "sess_b", "sess_c", "sess_z"]);
    const after = toggleAllRows(rows, selected, false);
    expect([...after]).toEqual(["sess_z"]);
  });

  test("ticking the header adds the rendered rows to what is already ticked", () => {
    const after = toggleAllRows(rows, new Set(["sess_z"]), true);
    expect([...after].sort()).toEqual(["sess_a", "sess_b", "sess_c", "sess_z"]);
  });

  test("deleted ids leave the selection", () => {
    const after = selectionWithout(new Set(["sess_a", "sess_b"]), ["sess_a"]);
    expect([...after]).toEqual(["sess_b"]);
  });
});

describe("cell formatting", () => {
  test("a session with no model call reports zero tokens", () => {
    expect(rowTotalTokens({ id: "sess_a" })).toBe(0);
    expect(rowTotalTokens({ id: "sess_a", tokenUsage: {} })).toBe(0);
    expect(
      rowTotalTokens({ id: "sess_a", tokenUsage: { totalTokens: 42 } }),
    ).toBe(42);
  });

  test("token counts stay short", () => {
    expect(formatTokenCount(0)).toBe("0");
    expect(formatTokenCount(999)).toBe("999");
    expect(formatTokenCount(1000)).toBe("1k");
    expect(formatTokenCount(1250)).toBe("1.3k");
    expect(formatTokenCount(999_000)).toBe("999k");
    expect(formatTokenCount(2_400_000)).toBe("2.4M");
  });

  test("a missing stamp renders as unknown, never as now", () => {
    expect(formatRowTimestamp(undefined)).toBe("—");
    expect(formatRowTimestamp("")).toBe("—");
    expect(formatRowTimestamp("not a date")).toBe("—");
    expect(formatRowTimestamp("2026-09-13T10:11:12Z")).not.toBe("—");
  });

  test("the workspace cell shows the folder name", () => {
    expect(workspaceBasename("/home/u/projects/coddy")).toBe("coddy");
    expect(workspaceBasename("/home/u/projects/coddy/")).toBe("coddy");
    expect(workspaceBasename("C:\\work\\coddy")).toBe("coddy");
    expect(workspaceBasename("/")).toBe("");
    expect(workspaceBasename(undefined)).toBe("");
  });
});
