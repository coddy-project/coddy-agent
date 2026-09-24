import { expect, test } from "vitest";
import {
  type ArchiveMove,
  cursorAfterRemovals,
  overlayArchiveMoves,
  pruneArchiveBookkeeping,
  restoreRow,
  rowPlace,
} from "./archiveMoves";
import type { SessionRow } from "./types";

const rows = (...ids: string[]): SessionRow[] => ids.map((id) => ({ id, title: id }));
const ids = (list: SessionRow[]) => list.map((row) => row.id);

test("a listing issued before the archive settled is shown with the move applied", () => {
  const moves = new Map<string, ArchiveMove>([
    ["b", { archived: true, removesLoaded: true, settledAtSeq: null }],
    ["c", { archived: true, removesLoaded: true, settledAtSeq: 4 }],
  ]);
  // In flight: gone from the working list whatever the listing says.
  expect(ids(overlayArchiveMoves(rows("a", "b"), moves, 9, "exclude"))).toEqual(["a"]);
  // Settled at 4: listing 4 was issued before it, listing 5 after it and is
  // the server's own answer.
  expect(ids(overlayArchiveMoves(rows("a", "c"), moves, 4, "exclude"))).toEqual(["a"]);
  expect(ids(overlayArchiveMoves(rows("a", "c"), moves, 5, "exclude"))).toEqual(["a", "c"]);
  // Under "all" the row stays and takes the flag this tab wrote.
  const all = overlayArchiveMoves(rows("a", "b"), moves, 9, "all");
  expect(ids(all)).toEqual(["a", "b"]);
  expect(all[1]?.archived).toBe(true);
  // The archive view keeps what was archived and loses what was taken out.
  const back = new Map<string, ArchiveMove>([["a", { archived: false, removesLoaded: true, settledAtSeq: null }]]);
  expect(ids(overlayArchiveMoves(rows("a", "b"), back, 1, "only"))).toEqual(["b"]);
});

test("the next page starts as many rows earlier as the archive took out", () => {
  expect(cursorAfterRemovals("30", 1)).toBe("29");
  expect(cursorAfterRemovals("30", 3)).toBe("27");
  expect(cursorAfterRemovals("1", 5)).toBe("0");
  expect(cursorAfterRemovals("30", 0)).toBe("30");
  // No next page, or a cursor that is not an offset: nothing to move.
  expect(cursorAfterRemovals(null, 2)).toBeNull();
  expect(cursorAfterRemovals("opaque-token", 2)).toBe("opaque-token");
});

test("a row is put back before the row that followed it, or at its index", () => {
  const list = rows("a", "b", "c", "d");
  const place = rowPlace(list, "b");
  expect(place).toEqual({ row: list[1], index: 1, nextId: "c" });
  expect(rowPlace(list, "zz")).toBeNull();
  // A page loaded in between puts rows before it; the neighbour still anchors it.
  expect(ids(restoreRow(rows("x", "a", "c", "d"), place!))).toEqual(["x", "a", "b", "c", "d"]);
  // The neighbour is gone too: the old index, within the list.
  expect(ids(restoreRow(rows("a", "d"), place!))).toEqual(["a", "b", "d"]);
  expect(ids(restoreRow(rows(), place!))).toEqual(["b"]);
  // Still listed (the "all" view): only its old flag comes back, and what
  // changed on the row meanwhile (a new title, new tags) stays.
  const flagged = restoreRow(
    [{ id: "b", title: "renamed", tags: ["x"], archived: true }],
    { ...place!, row: { id: "b", title: "b", archived: false } },
  );
  expect(flagged).toEqual([{ id: "b", title: "renamed", tags: ["x"], archived: false }]);
});

test("bookkeeping older than every listing still out is dropped", () => {
  const moves = new Map<string, ArchiveMove>([
    ["a", { archived: true, removesLoaded: true, settledAtSeq: 2 }],
    ["b", { archived: true, removesLoaded: true, settledAtSeq: 5 }],
    ["c", { archived: true, removesLoaded: true, settledAtSeq: null }],
  ]);
  expect(pruneArchiveBookkeeping(moves, [2, 5, 7], 5)).toEqual([5, 7]);
  expect([...moves.keys()]).toEqual(["b", "c"]);
});
