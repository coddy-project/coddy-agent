import { expect, test } from "vitest";
import { stylesSource } from "../../stylesSource";

const css = stylesSource();

function ruleBody(selector: string): string {
  const idx = css.indexOf(selector);
  expect(idx, `${selector} is missing from styles.scss`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

// The pictures belong to the row above them, so they sit close under it at the
// label's inset; below them the transcript's own gap between rows is the whole
// space, the same as after a row without pictures (DESIGN.md, Tool timeline).
test("the pictures of a read hang under its row with no space of their own below", () => {
  expect(ruleBody(".tool-images {")).toMatch(/margin:\s*6px 0 0 14px;/);
});

test("a picture card has the size and the corners of a sent attachment", () => {
  const card = ruleBody(".tool-image-card {");
  expect(card).toMatch(/width:\s*128px/);
  expect(card).toMatch(/height:\s*96px/);
  expect(card).toMatch(/border-radius:\s*12px/);
  expect(ruleBody(".tool-image-thumb {")).toMatch(/object-fit:\s*cover/);
});
