import type { SessionArchiveFilter } from "./sessionQuery";
import type { SessionRow } from "./types";

/**
 * Archiving from History moves the row at once and sends the PATCH behind it
 * (App.tsx, archiveSession). Until the server has the new flag, and until every
 * listing read before that has come back, a listing still carries the row as it
 * was; these helpers keep such a listing from putting the row back, and keep
 * the offset the next page starts at in step with the rows the archive took
 * out of the server's listing.
 */

/** An archive change this tab made and the listings it still has to outlive. */
export type ArchiveMove = {
  archived: boolean;
  /**
   * The row was part of what History had loaded and leaves the listing the
   * filter asks for: once the server has the flag, every row after it in that
   * listing is one place earlier.
   */
  removesLoaded: boolean;
  /**
   * How many listings had been issued when the PATCH settled; null while it is
   * in flight. A listing issued after that reads the server's own answer.
   */
  settledAtSeq: number | null;
};

/** Whether a row with this flag belongs to the listing the filter asks for. */
export function rowVisibleUnder(
  filter: SessionArchiveFilter,
  archived: boolean,
): boolean {
  return filter === "all" || (filter === "only") === archived;
}

/**
 * The rows of a listing issued as number `seq`, with the moves it may predate
 * applied: the row takes the flag this tab wrote, and leaves when the filter no
 * longer lists it.
 */
export function overlayArchiveMoves(
  rows: SessionRow[],
  moves: ReadonlyMap<string, ArchiveMove>,
  seq: number,
  filter: SessionArchiveFilter,
): SessionRow[] {
  if (moves.size === 0) return rows;
  const out: SessionRow[] = [];
  for (const row of rows) {
    const move = moves.get(row.id);
    if (!move || (move.settledAtSeq !== null && seq > move.settledAtSeq)) {
      out.push(row);
      continue;
    }
    if (!rowVisibleUnder(filter, move.archived)) continue;
    out.push(!!row.archived === move.archived ? row : { ...row, archived: move.archived });
  }
  return out;
}

/**
 * The listing's cursor is an offset into the server's rows. `removed` rows of
 * the part already loaded have left that listing since the cursor was given,
 * so the next page starts that many rows earlier, or the rows that moved up
 * into the gap would never be fetched. A cursor that is not an offset is kept.
 */
export function cursorAfterRemovals(
  cursor: string | null,
  removed: number,
): string | null {
  if (cursor === null || removed <= 0 || !/^\d+$/.test(cursor)) return cursor;
  return String(Math.max(0, Number(cursor) - removed));
}

/** Where a row stood before it was taken out, to put it back there. */
export type RowPlace = { row: SessionRow; index: number; nextId: string | null };

export function rowPlace(rows: SessionRow[], id: string): RowPlace | null {
  const index = rows.findIndex((row) => row.id === id);
  if (index < 0) return null;
  return {
    row: rows[index] as SessionRow,
    index,
    nextId: rows[index + 1]?.id ?? null,
  };
}

/**
 * Puts a row back where it stood: before the row that followed it, or at its
 * old index when that row has gone too. A row already in the list only takes
 * its old flag back; whatever else changed on it meanwhile stays.
 */
export function restoreRow(rows: SessionRow[], place: RowPlace): SessionRow[] {
  if (rows.some((row) => row.id === place.row.id)) {
    const archived = !!place.row.archived;
    return rows.map((row) =>
      row.id === place.row.id && !!row.archived !== archived
        ? { ...row, archived }
        : row,
    );
  }
  const before =
    place.nextId !== null ? rows.findIndex((row) => row.id === place.nextId) : -1;
  const at = before >= 0 ? before : Math.min(place.index, rows.length);
  return [...rows.slice(0, at), place.row, ...rows.slice(at)];
}

/**
 * Drops what no listing can be affected by any more: a move or a removal that
 * settled before the oldest listing still out (or, with none out, before the
 * next one to be issued) predates every listing that will ever come back.
 * Returns the removals that are kept; `moves` is pruned in place.
 */
export function pruneArchiveBookkeeping(
  moves: Map<string, ArchiveMove>,
  removals: readonly number[],
  oldestOpenSeq: number,
): number[] {
  for (const [id, move] of moves) {
    if (move.settledAtSeq !== null && move.settledAtSeq < oldestOpenSeq) {
      moves.delete(id);
    }
  }
  return removals.filter((at) => at >= oldestOpenSeq);
}
