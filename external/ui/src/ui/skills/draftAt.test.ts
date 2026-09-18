import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";
import {
  atMenuDraftAtCaret,
  extractAtFileAttachments,
  listAtPathSpans,
  parseMentions,
  type MentionToken,
} from "./draftAt";

// --- grammar ---
// The cases live in internal/mention/testdata/grammar_cases.json, which the Go
// grammar's test (internal/mention/mention_test.go) reads too: both twins are
// held to the same literals. A token is its span (the text a surface
// highlights) and its readings, longest first, encoded the way the Go test
// encodes them.

type GrammarCase = {
  in: string;
  tokens: { span: string; readings: string[] }[];
};

// A path from this file's own location: Vite rewrites the literal
// `new URL("...", import.meta.url)` form into a dev-server asset URL.
const grammarCases = JSON.parse(
  readFileSync(
    join(
      dirname(fileURLToPath(import.meta.url)),
      "../../../../../internal/mention/testdata/grammar_cases.json",
    ),
    "utf8",
  ),
) as GrammarCase[];

function readingsOf(tok: MentionToken): string[] {
  if (tok.kind === "url") {
    return [`url:${tok.url}`];
  }
  if (tok.kind === "scheme") {
    return [`${tok.scheme}:${tok.ref}`];
  }
  return tok.readings.map((r) =>
    r.lines ? `${r.path}#${r.lines.start}-${r.lines.end}` : r.path,
  );
}

describe("parseMentions matches the Go grammar", () => {
  test("the shared cases are all there", () => {
    expect(grammarCases.length).toBeGreaterThanOrEqual(30);
  });

  test.each(grammarCases.map((tc) => [tc.in, tc] as const))("%j", (_in, tc) => {
    const got = parseMentions(tc.in).map((tok) => ({
      span: tc.in.slice(tok.start, tok.end),
      readings: readingsOf(tok),
    }));
    expect(got).toEqual(tc.tokens);
  });
});

test("parseMentions reports kinds, schemes and quoting", () => {
  const toks = parseMentions(
    'see @session:sess_1 and @"a b.md":2-3 and @https://x.dev/a',
  );
  expect(toks).toEqual([
    {
      start: 4,
      end: 19,
      kind: "scheme",
      scheme: "session",
      ref: "sess_1",
      readings: [],
    },
    {
      start: 24,
      end: 37,
      kind: "path",
      quoted: true,
      readings: [{ path: "a b.md", lines: { start: 2, end: 3 } }],
    },
    {
      start: 42,
      end: 58,
      kind: "url",
      url: "https://x.dev/a",
      readings: [],
    },
  ]);
});

test("a scheme without a reference falls back to a path", () => {
  // Go's parseAt tries the path run when parseScheme finds no reference.
  expect(listAtPathSpans("@session: hi")).toEqual([
    { start: 0, end: 8, path: "session" },
  ]);
});

test("an inline code span opened before the @ keeps it prose", () => {
  expect(parseMentions("`code @x.go` and @y.go").map(readingsOf)).toEqual([
    ["y.go"],
  ]);
});

// --- picker draft ---

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

test("an @ after a slash stays inside the filter, as the grammar reads it", () => {
  // A scoped package: "@types" is part of the path, not a second mention.
  expect(atMenuDraftAtCaret("see @node_modules/@ty", 21)).toMatchObject({
    open: true,
    atIdx: 4,
    prefix: "node_modules/@ty",
  });
  expect(atMenuDraftAtCaret("see @mail@ho", 12).open).toBe(false);
});

test("atMenuDraft disabled inside fenced code", () => {
  const s = "```\n@\n```";
  const caret = s.indexOf("@") + 1;
  expect(atMenuDraftAtCaret(s, caret).open).toBe(false);
});

function draftAtEnd(s: string) {
  return atMenuDraftAtCaret(s, s.length);
}

test("atMenuDraft keeps a meta scheme open", () => {
  expect(draftAtEnd("@session:")).toEqual({
    open: true,
    lineStart: 0,
    atIdx: 0,
    caret: 9,
    prefix: "session:",
  });
  expect(draftAtEnd("ask @rule:dep")).toMatchObject({
    open: true,
    atIdx: 4,
    prefix: "rule:dep",
  });
  expect(draftAtEnd("@agent:")).toMatchObject({ open: true, prefix: "agent:" });
});

test("atMenuDraft closes a meta reference at a space or a second colon", () => {
  expect(draftAtEnd("@session:sess_1 please").open).toBe(false);
  expect(draftAtEnd("@session:a:b").open).toBe(false);
  // Schemes are lower case, like Go's ParseScheme.
  expect(draftAtEnd("@Session:").open).toBe(false);
});

test("atMenuDraft closes at a colon that is not a scheme's", () => {
  // The line-range picker takes over there.
  expect(draftAtEnd("@foo:").open).toBe(false);
  expect(draftAtEnd("@f.go:12").open).toBe(false);
});

test("atMenuDraft keeps an open quote open, spaces and all", () => {
  expect(draftAtEnd('see @"my no')).toMatchObject({
    open: true,
    atIdx: 4,
    prefix: '"my no',
  });
  // Prose after a dotted name does not close a quote that is still open.
  expect(draftAtEnd('@"my notes.md and more')).toMatchObject({
    open: true,
    prefix: '"my notes.md and more',
  });
  expect(draftAtEnd('@"')).toMatchObject({ open: true, prefix: '"' });
});

test("atMenuDraft closes once the quote closes", () => {
  expect(draftAtEnd('@"my notes.md" x').open).toBe(false);
  expect(draftAtEnd('@"my notes.md"').open).toBe(false);
});

test("atMenuDraft accepts ~ and + in a path", () => {
  expect(draftAtEnd("@~/no")).toMatchObject({ open: true, prefix: "~/no" });
  expect(draftAtEnd("@lib/c++/x")).toMatchObject({
    open: true,
    prefix: "lib/c++/x",
  });
});

test("atMenuDraft opens after an opening bracket or quote", () => {
  expect(draftAtEnd("(@src")).toEqual({
    open: true,
    lineStart: 0,
    atIdx: 1,
    caret: 5,
    prefix: "src",
  });
  for (const opener of ["[", "{", '"', "'"]) {
    expect(draftAtEnd(`${opener}@src`)).toMatchObject({
      open: true,
      atIdx: 1,
      prefix: "src",
    });
  }
  expect(draftAtEnd("mail user@exa").open).toBe(false);
});

test("atMenuDraft stays shut inside an inline code span", () => {
  expect(draftAtEnd("`@x").open).toBe(false);
  expect(draftAtEnd("`code @x").open).toBe(false);
  // A closed span before the @ is not code any more.
  expect(draftAtEnd("`code` @x")).toMatchObject({ open: true, prefix: "x" });
});

test("atMenuDraft stays shut on a blockquote or a fence line", () => {
  expect(draftAtEnd("> @x").open).toBe(false);
  // The closing fence toggles the block shut, but holds no reference itself.
  expect(draftAtEnd("```\ncode\n``` @x").open).toBe(false);
});

test("atMenuDraft on a later line anchors at that line", () => {
  const s = "first line\n@src";
  expect(draftAtEnd(s)).toEqual({
    open: true,
    lineStart: 11,
    atIdx: 11,
    caret: s.length,
    prefix: "src",
  });
  // A caret at the very start never reads the next line.
  expect(atMenuDraftAtCaret("\n@x", 0).open).toBe(false);
});

// --- spans and attachments ---

test("listAtPathSpans absorbs a line-range suffix", () => {
  const s = "see @Dockerfile:21-31 ok";
  const spans = listAtPathSpans(s);
  expect(spans).toHaveLength(1);
  expect(spans[0]!.path).toBe("Dockerfile");
  expect(spans[0]!.lines).toEqual({ start: 21, end: 31 });
  expect(s.slice(spans[0]!.start, spans[0]!.end)).toBe("@Dockerfile:21-31");
});

test("listAtPathSpans reads the #L forms of a range", () => {
  expect(listAtPathSpans("see @f.go#L10-20 now")).toEqual([
    { start: 4, end: 16, path: "f.go", lines: { start: 10, end: 20 } },
  ]);
});

// Changed: a folder mention used to be skipped (it only navigated the picker).
// It now attaches a listing of the folder, so the composer chips it too.
test("listAtPathSpans keeps a folder mention", () => {
  expect(listAtPathSpans("list @src/ now")).toEqual([
    { start: 5, end: 10, path: "src/" },
  ]);
});

// Changed: a path with ".." used to be rejected. The server resolves it now
// (outside the workspace it is shown as an absolute path), so it is a mention.
test("listAtPathSpans keeps a path that climbs out with ..", () => {
  expect(listAtPathSpans("see @../sibling/x.go")).toEqual([
    { start: 4, end: 20, path: "../sibling/x.go" },
  ]);
});

test("listAtPathSpans chips meta references, quoted paths and pages", () => {
  const s = 'ask @session:sess_1, read @"a b.md" and @https://x.dev/a.';
  expect(
    listAtPathSpans(s).map((sp) => ({
      span: s.slice(sp.start, sp.end),
      path: sp.path,
    })),
  ).toEqual([
    { span: "@session:sess_1", path: "session:sess_1" },
    { span: '@"a b.md"', path: "a b.md" },
    { span: "@https://x.dev/a", path: "https://x.dev/a" },
  ]);
});

test("extractAtFileAttachments skips folders and dedupes", () => {
  const s = "see @a/b.txt and @a/ and @a/b.txt";
  expect(extractAtFileAttachments(s)).toEqual([{ path: "a/b.txt" }]);
});

test("extractAtFileAttachments skips meta references and pages", () => {
  expect(
    extractAtFileAttachments(
      'ask @session:sess_1 about @https://x.dev/a and @"my notes.md"',
    ),
  ).toEqual([{ path: "my notes.md" }]);
});

// Range parity strings below are cases of internal/mention/testdata/grammar_cases.json.

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
