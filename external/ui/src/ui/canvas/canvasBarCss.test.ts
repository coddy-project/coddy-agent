import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * The canvas's fields take the focus as the app's other fields do: their own
 * border in the accent, never the browser's blue ring around them.
 */

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

function squash(body: string): string {
  return body
    .replace(/\s+/g, " ")
    .replace(/\(\s+/g, "(")
    .replace(/\s+\)/g, ")");
}

function ruleBody(selector: string): string {
  const idx = css.indexOf(selector);
  expect(idx, `${selector} is missing from styles.css`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  return squash(css.slice(open + 1, css.indexOf("}", open)));
}

test("a canvas field draws its own focus, not the browser's ring", () => {
  const body = ruleBody(".canvas-bar-field:focus");
  expect(body).toContain("outline: none");
  expect(body).toMatch(/border-color: color-mix\(in srgb, var\(--accent\)/);
  for (const field of [
    ".canvas-popover-note:focus",
    ".canvas-file-input:focus",
  ]) {
    expect(css).toContain(field);
  }
});

test("the bar has no Stop of its own beside Send", () => {
  expect(css).not.toMatch(/\.canvas-bar-stop\b/);
});
