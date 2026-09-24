import type { TranscriptItem } from "./types";

/**
 * The render window of a transcript: which of the rows the client holds are
 * in the DOM (issue #338). A long conversation used to mount every row - a
 * 3306-message session was 94 thousand elements and a 32-second task on a
 * slow phone - so only a bounded slice is rendered, grown a chunk per frame
 * toward the edge the reader approaches and trimmed on the far side.
 */

/** Rows a transcript opens with: about a phone screen and a half. */
export const INITIAL_ROWS = 8;

/** Rows added per frame when the reader nears an edge of the window: few
 *  enough that a frame holding markdown answers stays short on a slow phone. */
export const CHUNK_ROWS = 4;

/** The window is trimmed back to this many rows... */
export const MAX_ROWS = 120;

/** ...once it holds this many more, so trims come in batches, not per row. */
export const TRIM_SLACK_ROWS = 2 * CHUNK_ROWS;

/**
 * The window, anchored by row ids so it does not move when older pages are put
 * in front of the list or a reload replaces the rows. `start` and `end` are
 * the indices it last resolved to: the fallback when an anchor row has left
 * the list (a reload that renamed it), so the reader stays where they were
 * instead of being thrown to the newest message.
 */
export type RenderWindow = {
  /** First rendered row; null: the last `INITIAL_ROWS` rows. */
  startId: string | null;
  /** Last rendered row; null: attached to the tail, new rows join it. */
  endId: string | null;
  start: number;
  end: number;
};

/** The window a conversation opens with, before its rows are known. */
export const OPENING_RENDER_WINDOW: RenderWindow = {
  startId: null,
  endId: null,
  start: 0,
  end: 0,
};

/** The window attached to the tail over its last `rows` rows. */
export function tailRenderWindow(
  items: readonly TranscriptItem[],
  rows = INITIAL_ROWS,
): RenderWindow {
  const end = items.length;
  const start = Math.max(0, end - rows);
  return { startId: items[start]?.id ?? null, endId: null, start, end };
}

/** Index of every row by id, for resolving a window against a new list. */
export function rowIndexById(
  items: readonly TranscriptItem[],
): Map<string, number> {
  const m = new Map<string, number>();
  items.forEach((it, i) => m.set(it.id, i));
  return m;
}

/** The slice `[start, end)` of `items` a window names. */
export function resolveRenderWindow(
  items: readonly TranscriptItem[],
  index: ReadonlyMap<string, number>,
  w: RenderWindow,
): { start: number; end: number } {
  const len = items.length;
  const clamp = (n: number) => Math.min(Math.max(0, n), len);
  let end = len;
  if (w.endId !== null) {
    const i = index.get(w.endId);
    end = i !== undefined ? i + 1 : clamp(w.end);
  }
  let start: number;
  if (w.startId === null) {
    start = Math.max(0, end - INITIAL_ROWS);
  } else {
    const i = index.get(w.startId);
    if (i !== undefined) {
      start = i;
    } else if (w.endId === null) {
      // Attached to the tail: keep as many of the newest rows as before.
      start = Math.max(0, end - Math.max(INITIAL_ROWS, w.end - w.start));
    } else {
      start = clamp(w.start);
    }
  }
  if (start > end) start = Math.max(0, end - INITIAL_ROWS);
  return { start, end };
}

function at(
  items: readonly TranscriptItem[],
  start: number,
  end: number,
  attached: boolean,
): RenderWindow {
  return {
    startId: items[start]?.id ?? null,
    endId: attached || end >= items.length ? null : (items[end - 1]?.id ?? null),
    start,
    end: attached ? items.length : end,
  };
}

/** Renders `n` more rows above the window. */
export function growRenderWindowUp(
  items: readonly TranscriptItem[],
  range: { start: number; end: number },
  attached: boolean,
  n = CHUNK_ROWS,
): RenderWindow {
  return at(items, Math.max(0, range.start - n), range.end, attached);
}

/** Renders `n` more rows below the window; reaching the end attaches it. */
export function growRenderWindowDown(
  items: readonly TranscriptItem[],
  range: { start: number; end: number },
  n = CHUNK_ROWS,
): RenderWindow {
  const end = Math.min(items.length, range.end + n);
  return at(items, range.start, end, end >= items.length);
}

/** Drops the rows above `start` from the window. */
export function trimRenderWindowTop(
  items: readonly TranscriptItem[],
  range: { start: number; end: number },
  attached: boolean,
  start: number,
): RenderWindow {
  return at(items, Math.min(start, range.end), range.end, attached);
}

/** Drops the rows from `end` on; the window is no longer attached. */
export function trimRenderWindowBottom(
  items: readonly TranscriptItem[],
  range: { start: number; end: number },
  end: number,
): RenderWindow {
  return at(items, range.start, Math.max(end, range.start), false);
}

/** A row on screen, as the trim measures it. */
export type MeasuredRow = { id: string; top: number; bottom: number };

/**
 * Where the window may start once the rows far above the visible area are
 * dropped: after the last row that ends more than `margin` above `viewTop`,
 * keeping at least `MAX_ROWS` rows and never dropping a row that holds focus.
 * `rows` are the rendered rows top to bottom. Returns `range.start` when
 * nothing can go.
 */
export function trimTopTo(
  rows: readonly MeasuredRow[],
  index: ReadonlyMap<string, number>,
  range: { start: number; end: number },
  viewTop: number,
  margin: number,
  focusedId: string | null,
): number {
  const limit = range.end - MAX_ROWS;
  let start = range.start;
  for (const row of rows) {
    if (row.bottom >= viewTop - margin || row.id === focusedId) break;
    const i = index.get(row.id);
    if (i === undefined || i + 1 > limit) break;
    start = i + 1;
  }
  return start;
}

/**
 * Where the window may end once the rows far below the visible area are
 * dropped: before the first row that starts more than `margin` below
 * `viewBottom` counted from the bottom, keeping at least `MAX_ROWS` rows.
 * Returns `range.end` when nothing can go.
 */
export function trimBottomTo(
  rows: readonly MeasuredRow[],
  index: ReadonlyMap<string, number>,
  range: { start: number; end: number },
  viewBottom: number,
  margin: number,
  focusedId: string | null,
): number {
  const limit = range.start + MAX_ROWS;
  let end = range.end;
  for (let k = rows.length - 1; k >= 0; k--) {
    const row = rows[k]!;
    if (row.top <= viewBottom + margin || row.id === focusedId) break;
    const i = index.get(row.id);
    if (i === undefined || i < limit) break;
    end = i;
  }
  return end;
}
