import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * How the views of a chat mark what is on show: a pressed toggle and the
 * active row of a tree brighten - text, border and ground from the text
 * colour - the way the Tasks control always marked its open panel.
 * The accent is the mark of work in flight, not of a choice, so none of them
 * takes it, in any theme.
 */

const css = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");

/**
 * A declaration block on one line: Prettier breaks a long value over several,
 * indented, so the assertions read it with runs of whitespace as one space and
 * none inside the parentheses.
 */
function squash(body: string): string {
  return body
    .replace(/\s+/g, " ")
    .replace(/\(\s+/g, "(")
    .replace(/\s+\)/g, ")");
}

/** The body of the last rule whose selector list ends with `selector {`. */
function ruleBody(selector: string): string {
  const idx = css.lastIndexOf(`${selector} {`);
  expect(idx, `${selector} is missing from styles.css`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  return squash(css.slice(open + 1, css.indexOf("}", open)));
}

test.each([".files-icon-btn.is-active", ".files-tree-row.is-active"])(
  "%s brightens from the text colour, not the accent",
  (selector) => {
    const body = ruleBody(selector);
    expect(body).not.toContain("var(--accent)");
    expect(body).toMatch(/background: color-mix\(in srgb, var\(--text\)/);
  },
);

test("a pressed toggle and the active row of the tree carry a light outline too", () => {
  // The edits window is headed with the Files window's controls, not toggles of its own.
  expect(css).not.toContain(".dv-icon-btn.is-active");
  expect(ruleBody(".files-icon-btn.is-active")).toMatch(
    /border-color: color-mix\(in srgb, var\(--text\)/,
  );
  expect(ruleBody(".files-tree-row.is-active")).toMatch(
    /box-shadow: inset 0 0 0 1px color-mix\(in srgb, var\(--text\)/,
  );
});

test("git's count on the plate is framed as a button, and its frame brightens on hover", () => {
  const rest = ruleBody(".workspace-bar-edits");
  expect(rest).toMatch(/border: 1px solid color-mix\(in srgb, var\(--text\)/);
  expect(rest).not.toContain("var(--accent)");
  const hover = ruleBody(".workspace-bar-edits:focus-visible");
  expect(hover).toMatch(/border-color: color-mix\(in srgb, var\(--text\)/);
  expect(hover).toMatch(/background: color-mix\(in srgb, var\(--text\)/);
});

test("the plate's icons sit a pixel above the middle of the lowercase letters, in the plate's own colour", () => {
  const icon = ruleBody(".workspace-bar-icon");
  expect(icon).toContain("vertical-align: middle");
  expect(icon).toMatch(/position: relative;[^}]*top: -1px/);
  // The worktree mark is the branch glyph in the plate's colour, not an accent.
  expect(css).not.toContain(".workspace-bar-icon--worktree");
});

// The words of the plate start where the field's text starts: the folder's
// icon stands on the placeholder's left edge, on the plate of a running chat,
// on the picks of the start screen, and on a phone or a touch screen.
test("the plate's first item lines up with the placeholder", () => {
  const ta = /\ntextarea#composer\s*\{([^}]+)\}/s.exec(css)![1]!;
  const fieldLeft = parseFloat(
    /padding:\s*[\d.]+px\s+[\d.]+px\s+[\d.]+px\s+([\d.]+)px/.exec(ta)![1]!,
  );
  const bar = /\n\.workspace-bar\s*\{([^}]+)\}/s.exec(css)![1]!;
  const barLeft = parseFloat(
    /padding:\s*[\d.]+px\s+[\d.]+px\s+[\d.]+px\s+([\d.]+)px/.exec(bar)![1]!,
  );
  expect(barLeft).toBe(fieldLeft);
  const left = (block: string, sel: string) =>
    parseFloat(
      new RegExp(
        `\\n\\s*${sel.replace(/\./g, "\\.")}\\s*\\{[^}]*?padding-left:\\s*([\\d.]+)px`,
        "s",
      ).exec(block)![1]!,
    );
  const padX = (block: string, sel: string) =>
    parseFloat(
      new RegExp(
        `\\n\\s*${sel.replace(/\./g, "\\.")}[^{]*\\{[^}]*?padding:\\s*[\\d.]+px\\s+([\\d.]+)px`,
        "s",
      ).exec(block)![1]!,
    );
  const top = css.slice(
    0,
    css.indexOf(
      "@media (max-width: 599px), (any-hover: none) and (any-pointer: coarse) {\n  .workspace-bar {",
    ),
  );
  expect(
    left(top, ".workspace-bar--pick") + padX(top, ".workspace-bar-pick,"),
  ).toBe(fieldLeft);
  const touchStart = css.indexOf(
    "@media (max-width: 599px), (any-hover: none) and (any-pointer: coarse) {\n  .workspace-bar {",
  );
  const touch = css.slice(touchStart, css.indexOf("\n}\n", touchStart));
  expect(
    left(touch, ".workspace-bar--pick") + padX(touch, ".workspace-bar-pick"),
  ).toBe(fieldLeft);
});
