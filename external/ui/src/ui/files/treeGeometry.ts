/**
 * Where a row of a file tree (the Files window's and the edits window's) puts
 * its glyph and its name, so the column reads as one edge from the filter
 * down: the glyph of a top-level row stands where the filter's magnifier does
 * and its name starts where the filter's text does (`.files-filter-icon`,
 * `.files-filter input`, held together by `filesWindowCss.test.ts`). Nested
 * rows step in by `TREE_INDENT` a level.
 */

/** A row's left padding at the top level, from the tree's own padding. */
export const TREE_ROW_INSET = 10;

/** How far a level steps in. */
export const TREE_INDENT = 14;

/** The glyph's box (`.files-tree-glyph`) and the gap after it (the row's `gap`). */
export const TREE_GLYPH = 16;
export const TREE_GAP = 7;

/** A row's left padding at a depth: where its glyph starts. */
export function rowInset(depth: number): number {
  return TREE_ROW_INSET + depth * TREE_INDENT;
}

/** Where a row's name starts at a depth: what a row with no glyph is padded by. */
export function nameInset(depth: number): number {
  return rowInset(depth) + TREE_GLYPH + TREE_GAP;
}
