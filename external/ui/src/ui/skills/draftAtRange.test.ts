import { expect, test } from "vitest";
import {
  atRangeDraftAtCaret,
  highlightedRange,
  replaceAtRangeSuffix,
} from "./draftAtRange";

function draftAtEnd(s: string) {
  return atRangeDraftAtCaret(s, s.length);
}

test("colon after a path opens the range draft with nothing selected", () => {
  const d = draftAtEnd("fix @Dockerfile:");
  expect(d.open).toBe(true);
  if (!d.open) return;
  expect(d.path).toBe("Dockerfile");
  expect(d.start).toBeNull();
  expect(d.end).toBeNull();
  expect(d.suffixStart).toBe("fix @Dockerfile".length);
  expect(d.suffixEnd).toBe("fix @Dockerfile:".length);
});

test("a partially typed range reports the start only", () => {
  const a = draftAtEnd("@f.go:21");
  expect(a.open && a.start).toBe(21);
  expect(a.open && a.end).toBeNull();
  const b = draftAtEnd("@f.go:21-");
  expect(b.open && b.start).toBe(21);
  expect(b.open && b.end).toBeNull();
});

test("a complete range reports both ends", () => {
  const d = draftAtEnd("see @docs/ui.md:10-20");
  expect(d.open).toBe(true);
  if (!d.open) return;
  expect(d.path).toBe("docs/ui.md");
  expect(d.start).toBe(10);
  expect(d.end).toBe(20);
});

// A backwards range is not a selection yet - the user is still typing.
test("an inverted range keeps only the start", () => {
  const d = draftAtEnd("@f.go:31-2");
  expect(d.open && d.start).toBe(31);
  expect(d.open && d.end).toBeNull();
});

test("the draft closes once the token ends", () => {
  expect(draftAtEnd("@f.go:1-2 ").open).toBe(false);
  expect(draftAtEnd("@f.go:1-2 and more").open).toBe(false);
  expect(draftAtEnd("@f.go:1:2").open).toBe(false);
  expect(draftAtEnd("@f.go:x").open).toBe(false);
});

test("without a colon the file picker keeps the draft", () => {
  expect(draftAtEnd("@Dockerfile").open).toBe(false);
});

test("the nearest mention wins", () => {
  const d = draftAtEnd("@a.md:1-2 and @b.md:3-");
  expect(d.open && d.path).toBe("b.md");
});

test("an @ glued to a word is not a mention", () => {
  expect(draftAtEnd("mail@host.com:1-2").open).toBe(false);
});

test("paths with spaces and folder or traversal tokens", () => {
  expect(
    draftAtEnd("open @readme copy.md:2-4").open &&
      draftAtEnd("open @readme copy.md:2-4"),
  ).toMatchObject({
    path: "readme copy.md",
  });
  expect(draftAtEnd("@pkg/:1-2").open).toBe(false);
  expect(draftAtEnd("@../secret.txt:1-2").open).toBe(false);
});

test("fenced code and blockquote lines are excluded", () => {
  expect(draftAtEnd("```\n@f.go:1-2").open).toBe(false);
  expect(draftAtEnd("> @f.go:1-2").open).toBe(false);
});

test("the draft follows the caret, not the end of the text", () => {
  const s = "@f.go:12 tail";
  const d = atRangeDraftAtCaret(s, "@f.go:12".length);
  expect(d.open && d.start).toBe(12);
});

test("replaceAtRangeSuffix rewrites the typed suffix", () => {
  const s = "fix @f.go:2";
  const d = draftAtEnd(s);
  const next = replaceAtRangeSuffix(s, d, 4, 9);
  expect(next.text).toBe("fix @f.go:4-9");
  expect(next.caret).toBe(next.text.length);
});

test("replaceAtRangeSuffix normalizes a backwards selection and keeps the tail", () => {
  const s = "fix @f.go: rest";
  const d = atRangeDraftAtCaret(s, "fix @f.go:".length);
  const next = replaceAtRangeSuffix(s, d, 9, 4);
  expect(next.text).toBe("fix @f.go:4-9 rest");
});

test("highlightedRange collapses a half-typed range to one line", () => {
  expect(highlightedRange(draftAtEnd("@f.go:7"))).toEqual({ start: 7, end: 7 });
  expect(highlightedRange(draftAtEnd("@f.go:7-9"))).toEqual({
    start: 7,
    end: 9,
  });
  expect(highlightedRange(draftAtEnd("@f.go:"))).toBeNull();
});
