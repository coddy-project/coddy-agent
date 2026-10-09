import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const m = new RegExp(`^${escaped}\\s*\\{([^}]+)\\}`, "m").exec(css);
  expect(m, selector).not.toBeNull();
  return m![1]!;
}

// A line's number and its text are one line box: one monospaced font, one
// size, one line height, set on the block and taken by both. The code element
// inherits it rather than the browser's own monospace, whose size and line
// height differ, so the numbers never drift from the text they number.
test("a line's number and its text share one font and one line height", () => {
  const block = rule(".files-code");
  expect(block).toMatch(/font-family:\s*ui-monospace,[^;]*monospace;/);
  expect(block).toMatch(/font-size:\s*12px/);
  expect(block).toMatch(/line-height:\s*1\.6/);
  expect(rule(".files-code code")).toMatch(/font:\s*inherit/);
  expect(rule(".files-line-no")).not.toMatch(/font/);
});

// For now a file is only read: the line an address asks for is scrolled to,
// not painted.
test("no line of an open file is painted as the one asked for", () => {
  expect(css).not.toMatch(/\.files-code\s*>\s*\.is-active/);
});

// The token colours of the theme reach every view that colours code: the
// chat's code blocks, an open file and a diff. They were once scoped to the
// chat alone, and the Files and edits windows drew every token in the text
// colour although the spans carried their classes.
test.each([
  "comment",
  "keyword",
  "string",
  "number",
  "title",
  "attribute",
  "type",
  "meta",
  "deletion",
])(
  "the %s colour of the theme reaches the Files window and the diffs",
  (role) => {
    const rules = [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)].filter((m) =>
      m[2]!.includes(`color: var(--syntax-${role})`),
    );
    expect(rules.length).toBeGreaterThan(0);
    for (const scope of [".md-code", ".files-code", ".dv-code"]) {
      expect(
        rules.some((m) => m[1]!.includes(scope)),
        `${scope} takes --syntax-${role}`,
      ).toBe(true);
    }
  },
);
