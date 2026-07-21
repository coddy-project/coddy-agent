/**
 * Contract: the `/` skills picker row renders the description inline after the
 * command name (one flowing block), not in a separate right-hand column.
 *
 * Key invariants:
 * - The row button is a single block (not a 2-column grid), so name + desc
 *   share one inline flow.
 * - `.slash-row-line` clamps that flow to 2 lines with an ellipsis, so a long
 *   description wraps under the name and anything that does not fit ends in "…".
 * - Composer renders the name and description inside a single `.slash-row-line`.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8");
const composer = readFileSync(join(dir, "Composer.tsx"), "utf8");

test("slash row button is a single block, not a 2-column grid", () => {
  const block = css.match(/\.slash-row-btn\s*\{[^}]*\}/s)?.[0] ?? "";
  expect(block).toMatch(/display:\s*block/);
  expect(block).not.toMatch(/grid-template-columns/);
});

test("slash row line clamps name+description to 2 lines with an ellipsis", () => {
  const block = css.match(/\.slash-row-line\s*\{[^}]*\}/s)?.[0] ?? "";
  expect(block).toMatch(/-webkit-line-clamp:\s*2/);
  expect(block).toMatch(/overflow:\s*hidden/);
});

test("composer wraps the name and description in one inline slash-row-line", () => {
  expect(composer).toMatch(/className="slash-row-line"/);
  // Name and description are siblings inside the same line block.
  expect(composer).toMatch(
    /slash-row-line[\s\S]{0,200}slash-row-name[\s\S]{0,200}slash-row-desc/,
  );
});
