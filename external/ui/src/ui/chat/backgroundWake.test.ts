import { expect, test } from "vitest";
import { opensTurn, parseBackgroundWakeTasks } from "./backgroundWake";
import type { TranscriptItem } from "./types";

test("the stream's camelCase and the transcript's snake_case read the same", () => {
  const stream = parseBackgroundWakeTasks({
    tasks: [{ id: "bg_3", kind: "command", label: "make test", status: "failed", exitCode: 2, durationMs: 90000 }],
  });
  const stored = parseBackgroundWakeTasks({
    tasks: [{ id: "bg_3", kind: "command", label: "make test", status: "failed", exit_code: 2, duration_ms: 90000 }],
  });
  expect(stream).toEqual(stored);
  expect(stream).toEqual([
    { id: "bg_3", kind: "command", label: "make test", status: "failed", exitCode: 2, durationMs: 90000 },
  ]);
});

test("a task nobody can name is dropped, a malformed wake reads as none", () => {
  expect(parseBackgroundWakeTasks({ tasks: [{ status: "failed" }, { id: " bg_1 ", status: "succeeded" }] })).toEqual([
    { id: "bg_1", status: "succeeded" },
  ]);
  expect(parseBackgroundWakeTasks(undefined)).toEqual([]);
  expect(parseBackgroundWakeTasks({ tasks: "bg_1" })).toEqual([]);
});

test("a wake opens a turn the way a typed message does", () => {
  const wake: TranscriptItem = { id: "w", type: "background_wake", tasks: [{ id: "bg_1", status: "failed" }] };
  const user: TranscriptItem = { id: "u", type: "user_message", content: "hi" };
  const answer: TranscriptItem = { id: "a", type: "assistant_message", content: "hello" };
  expect(opensTurn(wake)).toBe(true);
  expect(opensTurn(user)).toBe(true);
  expect(opensTurn(answer)).toBe(false);
  expect(opensTurn(undefined)).toBe(false);
});
