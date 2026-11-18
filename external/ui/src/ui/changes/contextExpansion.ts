import {
  parseDiffPatch,
  type ParsedDiffHunk,
  type ParsedDiffLine,
} from "../messages/parseDiff";

export const CONTEXT_STEP = 10;
export type ContextDirection = "up" | "down" | "both";
export interface GapExpansion {
  up: number;
  down: number;
}
export interface DiffContextState {
  gaps: Record<number, GapExpansion>;
  all: boolean;
}
export interface ContextGap {
  total: number;
  remaining: number;
  canUp: boolean;
  canDown: boolean;
}
export interface ExpandedDiff {
  hunks: ParsedDiffHunk[];
  gaps: ContextGap[];
}
export const EMPTY_DIFF_CONTEXT: DiffContextState = { gaps: {}, all: false };

export function expandContextAt(
  state: DiffContextState,
  gapIndex: number,
  direction: ContextDirection,
): DiffContextState {
  const gap = state.gaps[gapIndex] ?? { up: 0, down: 0 };
  return {
    ...state,
    gaps: {
      ...state.gaps,
      [gapIndex]: {
        up: gap.up + (direction !== "down" ? CONTEXT_STEP : 0),
        down: gap.down + (direction !== "up" ? CONTEXT_STEP : 0),
      },
    },
  };
}

export function expandAllContext(state: DiffContextState): DiffContextState {
  return { ...state, all: true };
}

function fileLines(text: string): string[] {
  if (!text) return [];
  const lines = text.split("\n");
  if (lines.at(-1) === "") lines.pop();
  return lines;
}

function bounds(hunk: ParsedDiffHunk): {
  oldStart: number;
  newStart: number;
  oldEnd: number;
  newEnd: number;
} {
  const match = /^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@/.exec(
    hunk.header,
  );
  if (match) {
    const oldCount = Number(match[2] ?? 1);
    const newCount = Number(match[4] ?? 1);
    const oldStart = Number(match[1]) + (oldCount === 0 ? 1 : 0);
    const newStart = Number(match[3]) + (newCount === 0 ? 1 : 0);
    return {
      oldStart,
      newStart,
      oldEnd: oldStart + oldCount,
      newEnd: newStart + newCount,
    };
  }
  const old = hunk.lines.flatMap((line) =>
    line.oldNo === null ? [] : [line.oldNo],
  );
  const next = hunk.lines.flatMap((line) =>
    line.newNo === null ? [] : [line.newNo],
  );
  const oldStart = old[0] ?? 1;
  const newStart = next[0] ?? 1;
  return {
    oldStart,
    newStart,
    oldEnd: oldStart + old.length,
    newEnd: newStart + next.length,
  };
}

export function expandDiffContext(
  patch: string,
  before: string,
  after: string,
  state: DiffContextState,
): ExpandedDiff {
  const original = parseDiffPatch(patch).hunks;
  const hunks = original.map((hunk) => ({ ...hunk, lines: [...hunk.lines] }));
  const oldLines = fileLines(before);
  const newLines = fileLines(after);
  const ranges = original.map(bounds);
  const gaps: ContextGap[] = [];
  for (let index = 0; index <= hunks.length; index++) {
    const previous = ranges[index - 1];
    const next = ranges[index];
    const oldStart = previous?.oldEnd ?? 1;
    const newStart = previous?.newEnd ?? 1;
    const oldEnd = next?.oldStart ?? oldLines.length + 1;
    const newEnd = next?.newStart ?? newLines.length + 1;
    const total = Math.max(
      0,
      Math.min(
        oldEnd - oldStart,
        newEnd - newStart,
        oldLines.length - oldStart + 1,
        newLines.length - newStart + 1,
      ),
    );
    const expansion = state.gaps[index] ?? { up: 0, down: 0 };
    const up = next
      ? Math.min(total, state.all ? total : Math.max(0, expansion.up))
      : 0;
    const down = previous
      ? Math.min(total - up, state.all ? total : Math.max(0, expansion.down))
      : 0;
    const makeLine = (offset: number): ParsedDiffLine => ({
      kind: "ctx",
      oldNo: oldStart + offset,
      newNo: newStart + offset,
      content: oldLines[oldStart + offset - 1]!,
    });
    if (previous)
      hunks[index - 1]!.lines.push(
        ...Array.from({ length: down }, (_, i) => makeLine(i)),
      );
    if (next)
      hunks[index]!.lines.unshift(
        ...Array.from({ length: up }, (_, i) => makeLine(total - up + i)),
      );
    const remaining = total - up - down;
    gaps.push({
      total,
      remaining,
      canUp: !!next && remaining > 0,
      canDown: !!previous && remaining > 0,
    });
  }
  return { hunks, gaps };
}
