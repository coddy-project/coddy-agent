import { expect, test } from "vitest";
import {
  applyMentionRow,
  mentionInsertForPath,
  mentionRowFromRecent,
  mergeMentionRows,
  recentKindOf,
} from "./mentionRows";

test("a path with a space is inserted quoted, a folder keeps the quote open", () => {
  expect(mentionInsertForPath("src/app.go")).toBe("@src/app.go");
  expect(mentionInsertForPath("my notes.md")).toBe('@"my notes.md"');
  expect(mentionInsertForPath("my dir/")).toBe('@"my dir/');
});

test("a remembered folder reopens the picker, a file does not", () => {
  expect(mentionRowFromRecent({ path_rel: "src/ui", kind: "dir" })).toEqual({
    kind: "directory",
    insert: "@src/ui/",
    label: "src/ui/",
    detail: "src/",
    continue: true,
  });
  expect(mentionRowFromRecent({ path_rel: "README.md", kind: "file" })).toEqual({
    kind: "file",
    insert: "@README.md",
    label: "README.md",
    detail: "",
  });
});

test("recent rows lead and the server rows do not repeat them", () => {
  const recent = [{ kind: "file", insert: "@a.go", label: "a.go" }];
  const server = [
    { kind: "scheme", insert: "@session:", label: "session:", continue: true },
    { kind: "file", insert: "@a.go", label: "a.go" },
  ];
  expect(mergeMentionRows(recent, server).map((r) => r.insert)).toEqual([
    "@a.go",
    "@session:",
  ]);
});

test("only files and folders are remembered", () => {
  expect(recentKindOf({ kind: "file", insert: "@a", label: "a" })).toBe("file");
  expect(recentKindOf({ kind: "directory", insert: "@a/", label: "a/" })).toBe("dir");
  expect(recentKindOf({ kind: "session", insert: "@session:x", label: "x" })).toBeNull();
});

test("a quoted folder is closed ahead of the caret, and the next pick takes that quote over", () => {
  const folder = {
    kind: "directory",
    insert: '@"my folder/',
    label: "my folder/",
    continue: true,
  };
  const step = applyMentionRow("see @my", 4, 7, folder);
  expect(step).toEqual({ text: 'see @"my folder/"', caret: 16 });
  const file = { kind: "file", insert: '@"my folder/a b.md"', label: "my folder/a b.md" };
  expect(applyMentionRow(step.text, 4, step.caret, file)).toEqual({
    text: 'see @"my folder/a b.md" ',
    caret: 24,
  });
});

test("an unquoted row ends with a space unless it is a step", () => {
  const file = { kind: "file", insert: "@a.go", label: "a.go" };
  expect(applyMentionRow("see @a tail", 4, 6, file)).toEqual({
    text: "see @a.go  tail",
    caret: 10,
  });
  const dir = { kind: "directory", insert: "@src/", label: "src/", continue: true };
  expect(applyMentionRow("see @sr", 4, 7, dir)).toEqual({ text: "see @src/", caret: 9 });
});
