import { expect, test } from "vitest";
import {
  atMenuDraftAtCaret,
  extractAtFileAttachments,
  listAtPathSpans,
} from "./draftAt";

test("atMenuDraft detects path with spaces before caret", () => {
  const s = "x @readme here.md";
  const caret = s.length;
  const d = atMenuDraftAtCaret(s, caret);
  expect(d).toEqual({
    open: true,
    lineStart: 0,
    atIdx: 2,
    caret,
    prefix: "readme here.md",
  });
});

test("atMenuDraft closes after dotted path once user types prose", () => {
  const s = "@http_todo_report.md asdf asdf zxcv";
  const caret = s.length;
  expect(atMenuDraftAtCaret(s, caret).open).toBe(false);
});

test("atMenuDraft stays open trailing space after file pick", () => {
  const s = "@http_todo_report.md ";
  const caret = s.length;
  expect(atMenuDraftAtCaret(s, caret)).toMatchObject({
    open: true,
    prefix: "http_todo_report.md ",
  });
});

test("extractAtFileAttachments skips folders and dedupes", () => {
  const s = "see @a/b.txt and @a/ and @a/b.txt";
  expect(extractAtFileAttachments(s)).toEqual([{ path: "a/b.txt" }]);
});

test("atMenuDraft disabled inside fenced code", () => {
  const s = "```\n@\n```";
  const caret = s.indexOf("@") + 1;
  expect(atMenuDraftAtCaret(s, caret).open).toBe(false);
});

// Parity strings below are shared with internal/session/at_line_range_test.go.

test("listAtPathSpans absorbs a line-range suffix", () => {
  const s = "see @Dockerfile:21-31 ok";
  const spans = listAtPathSpans(s);
  expect(spans).toHaveLength(1);
  expect(spans[0]!.path).toBe("Dockerfile");
  expect(spans[0]!.lines).toEqual({ start: 21, end: 31 });
  expect(s.slice(spans[0]!.start, spans[0]!.end)).toBe("@Dockerfile:21-31");
});

test("extractAtFileAttachments carries line ranges", () => {
  expect(extractAtFileAttachments("@a/b.go:5-5")).toEqual([
    { path: "a/b.go", startLine: 5, endLine: 5 },
  ]);
});

test("single number after colon is not a range", () => {
  expect(extractAtFileAttachments("open @x.go:21 now")).toEqual([
    { path: "x.go" },
  ]);
});

test("range with trailing garbage is not a range", () => {
  expect(extractAtFileAttachments("see @file.go:21-31x here")).toEqual([
    { path: "file.go" },
  ]);
});

test("invalid ranges are dropped", () => {
  expect(extractAtFileAttachments("@f.go:31-21 x")).toEqual([{ path: "f.go" }]);
  expect(extractAtFileAttachments("@f.go:0-5 x")).toEqual([{ path: "f.go" }]);
  expect(extractAtFileAttachments("@f.go:1234567890-1234567891 x")).toEqual([
    { path: "f.go" },
  ]);
});

test("range parses at CRLF boundary, before punctuation and at end of text", () => {
  expect(extractAtFileAttachments("take @f.go:2-4\r\nplease")).toEqual([
    { path: "f.go", startLine: 2, endLine: 4 },
  ]);
  expect(extractAtFileAttachments("check @f.go:2-4, then run")).toEqual([
    { path: "f.go", startLine: 2, endLine: 4 },
  ]);
  expect(extractAtFileAttachments("@f.go:2-4")).toEqual([
    { path: "f.go", startLine: 2, endLine: 4 },
  ]);
});

// A padded token had its trailing space trimmed, so the suffix is prose.
test("a range after a space does not attach to the path", () => {
  expect(extractAtFileAttachments("look @notes.md :2-4 here")).toEqual([{ path: "notes.md" }]);
});

test("attachments dedupe by path and range", () => {
  expect(extractAtFileAttachments("@f.go:1-2 @f.go:1-2 @f.go:3-4 @f.go")).toEqual([
    { path: "f.go", startLine: 1, endLine: 2 },
    { path: "f.go", startLine: 3, endLine: 4 },
    { path: "f.go" },
  ]);
});
