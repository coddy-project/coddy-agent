import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import * as sass from "sass";

// The stylesheet is SCSS: the source-reading *Css.test.ts suites grep the text,
// so nothing else in this suite would catch a stylesheet Sass no longer parses.
// This is the one place that compiles it the way `vite build` does.
const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(join(here, "styles.scss"), "utf8");

describe("styles.scss compiles through Sass", () => {
  const compiled = sass.compileString(source, { syntax: "scss" });

  it("produces the stylesheet the browser receives", () => {
    expect(compiled.css).toContain(":root");
    expect(compiled.css).toMatch(/--radius:\s*14px/);
  });

  it("keeps the quoted monospace stack when the SCSS variable expands", () => {
    expect(compiled.css).toMatch(
      /--coddy-md-inline-code-font:[^;]*"Liberation Mono"/,
    );
    expect(compiled.css).toMatch(
      /font-family:[^;]*"Liberation Mono", "Courier New", monospace/,
    );
  });

  it("expands mix-text() back into the color-mix() ladder", () => {
    expect(source).toContain("@function mix-text(");
    expect(compiled.css).not.toMatch(/:\s*mix-text\(/);
    expect(compiled.css).toMatch(
      /color-mix\(in srgb, var\(--text\) \d+%, var\(--coddy-blend-base\)\)/,
    );
  });
});
