import { expect, test } from "vitest";
import { segmentComposerMirrorSpans } from "./composerMirrorSegments";

test("mirror chip only around caret slash token rest is plain text", () => {
  const s = "asddf /foo /ba";
  const caret = s.length;
  const segs = segmentComposerMirrorSpans(s, caret, null, null);
  expect(segs).toEqual([
    { type: "text", value: "asddf /foo " },
    { type: "slash", literal: "/ba", name: "ba" },
  ]);
});

test("mirror chip only middle token when caret inside it", () => {
  const s = "a /bcd e";
  const slashPos = s.indexOf("/");
  const caret = slashPos + "/bc".length;
  const segs = segmentComposerMirrorSpans(s, caret, null, null);
  expect(segs).toEqual([
    { type: "text", value: "a " },
    { type: "slash", literal: "/bc", name: "bc" },
    { type: "text", value: "d e" },
  ]);
});

test("ignored slash tokens elsewhere are plain without caret", () => {
  expect(segmentComposerMirrorSpans("x /zzz y", 2, null, null)).toEqual([
    { type: "text", value: "x /zzz y" },
  ]);
});

test("at mirror chip wraps active @ token before slash", () => {
  const s = "hello @notes.txt";
  const caret = s.length;
  const segs = segmentComposerMirrorSpans(s, caret, null, null);
  expect(segs).toEqual([
    { type: "text", value: "hello " },
    { type: "at", literal: "@notes.txt", pathRel: "notes.txt" },
  ]);
});

test("completed @ mention stays chipped after space and non-path text", () => {
  const s = "@http_todo_report.md что в файле?";
  const caret = s.length;
  const segs = segmentComposerMirrorSpans(s, caret, null, null);
  expect(segs).toEqual([
    { type: "at", literal: "@http_todo_report.md", pathRel: "http_todo_report.md" },
    { type: "text", value: " что в файле?" },
  ]);
});

test("completed @ mention chips file only when prose follows ASCII path", () => {
  const s = "@http_todo_report.md asdf asdf zxcv";
  const caret = s.length;
  const segs = segmentComposerMirrorSpans(s, caret, null, null);
  expect(segs).toEqual([
    { type: "at", literal: "@http_todo_report.md", pathRel: "http_todo_report.md" },
    { type: "text", value: " asdf asdf zxcv" },
  ]);
});

test("known skill chips completed /name when caret is elsewhere", () => {
  const known = new Set(["generate-rules"]);
  const s = "/generate-rules some follow-up text";
  const caret = s.length;
  const segs = segmentComposerMirrorSpans(s, caret, null, null, known);
  expect(segs[0]).toEqual({
    type: "slash",
    literal: "/generate-rules",
    name: "generate-rules",
  });
  expect(segs[1]).toEqual({ type: "text", value: " some follow-up text" });
});

test("unknown /name does not chip when not in known set", () => {
  const known = new Set(["generate-rules"]);
  const s = "/unknown-skill";
  const caret = 0;
  const segs = segmentComposerMirrorSpans(s, caret, null, null, known);
  expect(segs).toEqual([{ type: "text", value: "/unknown-skill" }]);
});

test("known skill chip shows even after selection when caret is after space", () => {
  const known = new Set(["generate-rules"]);
  const s = "/generate-rules ";
  const caret = s.length;
  const segs = segmentComposerMirrorSpans(s, caret, null, null, known);
  expect(segs[0]).toEqual({
    type: "slash",
    literal: "/generate-rules",
    name: "generate-rules",
  });
});

test("a line-range mention renders as one chip with the full literal", () => {
  const s = "fix @Dockerfile:21-31 please";
  const segs = segmentComposerMirrorSpans(s, 0, null, null);
  expect(segs).toEqual([
    { type: "text", value: "fix " },
    { type: "at", literal: "@Dockerfile:21-31", pathRel: "Dockerfile" },
    { type: "text", value: " please" },
  ]);
});
