import { expect, test } from "vitest";

import {
  CHUNK_ROWS,
  growRenderWindowDown,
  growRenderWindowUp,
  INITIAL_ROWS,
  MAX_ROWS,
  OPENING_RENDER_WINDOW,
  promptWaitsInLastTurn,
  resolveRenderWindow,
  rowIndexById,
  tailRenderWindow,
  trimBottomTo,
  trimRenderWindowBottom,
  trimRenderWindowTop,
  trimTopTo,
  type MeasuredRow,
} from "./transcriptRenderWindow";
import type { TranscriptItem } from "./types";

const rows = (n: number, from = 0): TranscriptItem[] =>
  Array.from({ length: n }, (_, i) => ({
    id: `r${from + i}`,
    type: "assistant_message" as const,
    content: `row ${from + i}`,
  }));

const resolve = (items: TranscriptItem[], w: Parameters<typeof resolveRenderWindow>[2]) =>
  resolveRenderWindow(items, rowIndexById(items), w);

test("a conversation opens on its last rows, attached to the tail", () => {
  const items = rows(300);
  expect(resolve(items, OPENING_RENDER_WINDOW)).toEqual({
    start: 300 - INITIAL_ROWS,
    end: 300,
  });
  // A short one is shown whole.
  expect(resolve(rows(5), OPENING_RENDER_WINDOW)).toEqual({ start: 0, end: 5 });
});

test("new rows join a window attached to the tail", () => {
  const items = rows(100);
  const w = tailRenderWindow(items);
  const more = [...items, ...rows(3, 100)];
  expect(resolve(more, w)).toEqual({ start: 100 - INITIAL_ROWS, end: 103 });
});

test("an older page put in front does not move the window", () => {
  const items = rows(100, 50);
  const w = tailRenderWindow(items);
  const withOlder = [...rows(50), ...items];
  expect(resolve(withOlder, w)).toEqual({ start: 150 - INITIAL_ROWS, end: 150 });
});

test("the window grows toward the reader and attaches on reaching the end", () => {
  const items = rows(200);
  const range = { start: 150, end: 170 };
  const up = growRenderWindowUp(items, range, false);
  expect(resolve(items, up)).toEqual({ start: 150 - CHUNK_ROWS, end: 170 });
  const down = growRenderWindowDown(items, range);
  expect(resolve(items, down)).toEqual({ start: 150, end: 170 + CHUNK_ROWS });
  const toEnd = growRenderWindowDown(items, {
    start: 150,
    end: 200 - CHUNK_ROWS + 1,
  });
  expect(toEnd.endId).toBeNull();
  expect(resolve(items, toEnd)).toEqual({ start: 150, end: 200 });
});

test("a trim keeps the rows around the reader and never fewer than the bound", () => {
  const items = rows(400);
  const index = rowIndexById(items);
  const range = { start: 100, end: 400 };
  // Rows 100..399, 50px each, the view from 10000 to 11000 (rows 300..319).
  const measured: MeasuredRow[] = items.slice(100).map((it, i) => ({
    id: it.id,
    top: i * 50,
    bottom: i * 50 + 50,
  }));
  // Rows ending more than a screen above the view go: up to row 278, which
  // ends at 9000px; the bound alone would have allowed 280.
  const top = trimTopTo(measured, index, range, 10000, 1000, null);
  expect(top).toBe(279);
  // With the view further down, the bound is what stops the trim.
  expect(trimTopTo(measured, index, range, 14000, 1000, null)).toBe(
    400 - MAX_ROWS,
  );
  // Nothing above is far enough out of view.
  expect(trimTopTo(measured, index, range, 1000, 1000, null)).toBe(100);
  // A focused row stays, and so does everything after it.
  expect(trimTopTo(measured, index, range, 10000, 1000, "r150")).toBe(150);
  const bottom = trimBottomTo(measured, index, range, 1000, 1000, null);
  expect(bottom).toBe(100 + MAX_ROWS);
  expect(resolve(items, trimRenderWindowTop(items, range, true, top))).toEqual({
    start: 279,
    end: 400,
  });
  const cut = trimRenderWindowBottom(items, range, bottom);
  expect(cut.endId).toBe("r219");
  expect(resolve(items, cut)).toEqual({ start: 100, end: 220 });
});

test("a row that left the list leaves the window where it was", () => {
  const items = rows(200);
  const w = trimRenderWindowBottom(items, { start: 50, end: 200 }, 170);
  // A reload renamed every row: the indices it last had stand.
  const renamed = rows(200, 1000);
  expect(resolve(renamed, w)).toEqual({ start: 50, end: 170 });
  // Attached to the tail, it keeps as many of the newest rows.
  const attached = growRenderWindowUp(items, { start: 150, end: 200 }, true);
  expect(resolve(renamed, attached)).toEqual({ start: 150 - CHUNK_ROWS, end: 200 });
});

test("only a prompt of the turn in flight holds the bottom of the window", () => {
  const prompt = (id: string, resolved?: boolean): TranscriptItem =>
    ({
      id,
      type: "permission_prompt",
      payload: {
        sessionId: "s",
        toolCall: { toolCallId: id, title: "run_command" },
        options: [],
      },
      ...(resolved ? { resolved: { outcome: "selected", optionId: "allow" } } : {}),
    }) as unknown as TranscriptItem;
  const user = (id: string): TranscriptItem => ({ id, type: "user_message", content: id });
  // An old turn cut off before its result left a prompt nobody will answer.
  const stale = [user("u1"), prompt("p1"), user("u2"), ...rows(3)];
  expect(promptWaitsInLastTurn(stale)).toBe(false);
  expect(promptWaitsInLastTurn([...stale, prompt("p2")])).toBe(true);
  expect(promptWaitsInLastTurn([...stale, prompt("p2", true)])).toBe(false);
});
