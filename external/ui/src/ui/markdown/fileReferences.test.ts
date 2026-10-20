import { describe, expect, test } from "vitest";

import {
  localLinkReference,
  parseFileReference,
  splitProsePaths,
} from "./fileReferences";

describe("a path written as inline code", () => {
  test.each([
    [
      "internal/session/artifacts.go",
      "internal/session/artifacts.go",
      undefined,
    ],
    ["internal/session/artifacts.go:42", "internal/session/artifacts.go", 42],
    ["internal/session/artifacts.go:42:7", "internal/session/artifacts.go", 42],
    ["src/a.ts:10-20", "src/a.ts", 10],
    ["docs/a.md#L12", "docs/a.md", 12],
    ["docs/a.md#L12-L20", "docs/a.md", 12],
    ["README.md", "README.md", undefined],
    ["Makefile", "Makefile", undefined],
    [".gitignore", ".gitignore", undefined],
    ["./cmd/coddy/main.go", "./cmd/coddy/main.go", undefined],
    ["/work/repo/x.go", "/work/repo/x.go", undefined],
    ["C:\\work\\repo\\x.go", "C:\\work\\repo\\x.go", undefined],
    ["docs/ru/руководство.md", "docs/ru/руководство.md", undefined],
  ])("%s names a file", (text, path, line) => {
    expect(parseFileReference(text)).toEqual({ path, line });
  });

  test.each([
    "",
    ".",
    "..",
    "/",
    "true",
    "npm",
    "go test ./...",
    "a b.go",
    "*.go",
    "${HOME}/x.go",
    "~/x.go",
    "https://example.com/a.go",
    "mailto:a@example.com",
    "coddy:features/mcp",
    "<x.go>",
    "x.go|y.go",
    "foo()",
  ])("%j is not a path", (text) => {
    expect(parseFileReference(text)).toBeNull();
  });
});

describe("a link target", () => {
  test("a relative or absolute target names a file", () => {
    expect(localLinkReference("internal/foo.go")).toEqual({
      path: "internal/foo.go",
      line: undefined,
    });
    expect(localLinkReference("../docs/a.md#L3")).toEqual({
      path: "../docs/a.md",
      line: 3,
    });
    expect(localLinkReference("/work/repo/x.go:9")).toEqual({
      path: "/work/repo/x.go",
      line: 9,
    });
    // Spaces come percent-encoded in a Markdown target.
    expect(localLinkReference("docs/my%20notes.md")).toEqual({
      path: "docs/my notes.md",
      line: undefined,
    });
    expect(localLinkReference("file:///work/repo/x.go")).toEqual({
      path: "/work/repo/x.go",
      line: undefined,
    });
  });

  test("an address of the web or of the app is not a file", () => {
    for (const href of [
      "https://example.com/a.go",
      "//example.com/a.go",
      "#section",
      "mailto:a@example.com",
      "coddy:features/mcp",
      "coddy-skill:review",
      "",
    ])
      expect(localLinkReference(href)).toBeNull();
  });
});

describe("paths in prose", () => {
  const paths = (text: string) =>
    splitProsePaths(text)
      .filter((part) => part.type === "path")
      .map((part) => (part.type === "path" ? [part.value, part.line] : null));

  test("a path between words is cut out with the punctuation left to the text", () => {
    expect(
      splitProsePaths("See internal/session/artifacts.go, then (docs/a.md)."),
    ).toEqual([
      { type: "text", value: "See " },
      {
        type: "path",
        value: "internal/session/artifacts.go",
        path: "internal/session/artifacts.go",
        line: undefined,
      },
      { type: "text", value: ", then (" },
      {
        type: "path",
        value: "docs/a.md",
        path: "docs/a.md",
        line: undefined,
      },
      { type: "text", value: ")." },
    ]);
  });

  test("a line suffix stays with its path", () => {
    expect(paths("in internal/foo.go:12, then")).toEqual([
      ["internal/foo.go:12", 12],
    ]);
    expect(paths("at docs/a.md#L4.")).toEqual([["docs/a.md#L4", 4]]);
  });

  test("a mention's @ is left to the text", () => {
    expect(splitProsePaths("@internal/foo.go")).toEqual([
      { type: "text", value: "@" },
      {
        type: "path",
        value: "internal/foo.go",
        path: "internal/foo.go",
        line: undefined,
      },
    ]);
  });

  test("prose takes only a path with a folder and an extension", () => {
    expect(paths("./run.sh and ../up/a.ts")).toEqual([
      ["./run.sh", undefined],
      ["../up/a.ts", undefined],
    ]);
    for (const text of [
      "and/or",
      "README.md alone",
      "/etc/hosts",
      "1/2.5 of it",
      "km/h.",
      "https://example.com/a.go",
      "~/notes/a.md",
      "TCP/IP",
    ])
      expect(paths(text)).toEqual([]);
  });

  test("text with no path is one text part", () => {
    expect(splitProsePaths("nothing here")).toEqual([
      { type: "text", value: "nothing here" },
    ]);
  });
});
