import { expect, test } from "vitest";
import { nextFilesWindowScope } from "./filesWindowKey";

test("the chat's folder becoming known keeps the window", () => {
  const opened = nextFilesWindowScope(null, "sess_a", "");
  const known = nextFilesWindowScope(opened, "sess_a", "/work/a");
  expect(known.generation).toBe(opened.generation);
  expect(known.path).toBe("/work/a");
});

test("a folder that gives way to another starts the window over", () => {
  const known = nextFilesWindowScope(null, "sess_a", "/work/a");
  const moved = nextFilesWindowScope(known, "sess_a", "/work/a-worktree");
  expect(moved.generation).toBe(known.generation + 1);
});

test("the same folder, or none for a moment, keeps the scope as it is", () => {
  const known = nextFilesWindowScope(null, "sess_a", "/work/a");
  expect(nextFilesWindowScope(known, "sess_a", "/work/a")).toBe(known);
  expect(nextFilesWindowScope(known, "sess_a", "")).toBe(known);
});

test("another chat is a scope of its own", () => {
  const first = nextFilesWindowScope(null, "sess_a", "/work/a");
  const moved = nextFilesWindowScope(first, "sess_a", "/work/b");
  const other = nextFilesWindowScope(moved, "sess_b", "/work/b");
  expect(other).toEqual({
    sessionId: "sess_b",
    path: "/work/b",
    generation: 0,
  });
});
