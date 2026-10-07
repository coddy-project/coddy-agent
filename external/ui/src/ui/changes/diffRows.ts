import type { ParsedDiffHunk, ParsedDiffLine } from "../messages/parseDiff";

/**
 * Diff lines arranged for display, in the two shapes the edits window offers.
 *
 * Kept separate from the components so the pairing rules - which decide what
 * lines up against what in the side-by-side view - can be tested on their own.
 *
 * A `gap` marks the jump between two hunks. It renders as a plain separator:
 * the reference UI writes "N unmodified lines" there, which this deliberately
 * does not, so the eye is not asked to read a number it cannot act on.
 */
export type UnifiedRow =
  | { kind: "gap" }
  | { kind: "line"; line: ParsedDiffLine };

/** One row of the side-by-side view: old on the left, new on the right. */
export type SplitRow =
  | { kind: "gap" }
  | { kind: "pair"; left: ParsedDiffLine | null; right: ParsedDiffLine | null };

/** Every diff line in order, with a gap between hunks. */
export function toUnifiedRows(hunks: ParsedDiffHunk[]): UnifiedRow[] {
  const rows: UnifiedRow[] = [];
  for (const hunk of hunks) {
    if (hunk.lines.length === 0) {
      continue;
    }
    // Never at either end: a leading or trailing separator would read as
    // content missing from the file rather than skipped between changes.
    if (rows.length > 0) {
      rows.push({ kind: "gap" });
    }
    for (const line of hunk.lines) {
      rows.push({ kind: "line", line });
    }
  }
  return rows;
}

/**
 * The same lines paired for the side-by-side view.
 *
 * A run of removals followed by a run of additions is one edit, so the two runs
 * are zipped: the first removal faces the first addition and so on, with the
 * shorter side padded out. Context ends a run and appears on both sides, which
 * keeps a deletion and an addition separated by unchanged code from being drawn
 * as if one replaced the other.
 */
export function toSplitRows(hunks: ParsedDiffHunk[]): SplitRow[] {
  const rows: SplitRow[] = [];
  for (const hunk of hunks) {
    if (hunk.lines.length === 0) {
      continue;
    }
    if (rows.length > 0) {
      rows.push({ kind: "gap" });
    }

    let dels: ParsedDiffLine[] = [];
    let adds: ParsedDiffLine[] = [];
    const flush = () => {
      const height = Math.max(dels.length, adds.length);
      for (let i = 0; i < height; i++) {
        rows.push({
          kind: "pair",
          left: dels[i] ?? null,
          right: adds[i] ?? null,
        });
      }
      dels = [];
      adds = [];
    };

    for (const line of hunk.lines) {
      if (line.kind === "del") {
        // Additions already closed the previous run, so this starts a new one.
        if (adds.length > 0) {
          flush();
        }
        dels.push(line);
      } else if (line.kind === "add") {
        adds.push(line);
      } else {
        flush();
        rows.push({ kind: "pair", left: line, right: line });
      }
    }
    flush();
  }
  return rows;
}
