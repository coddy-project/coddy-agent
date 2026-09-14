import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
  "utf8",
);

/** Every `@media (max-width: 1199px)` block, joined: the narrow shell is split
 *  across several of them, and a rule may live in any one. */
function narrowShellCss(): string {
  const out: string[] = [];
  const opener = "@media (max-width: 1199px) {";
  let at = css.indexOf(opener);
  while (at !== -1) {
    let depth = 0;
    let i = at + opener.length - 1;
    const start = i + 1;
    for (; i < css.length; i++) {
      if (css[i] === "{") depth++;
      else if (css[i] === "}") {
        depth--;
        if (depth === 0) break;
      }
    }
    out.push(css.slice(start, i));
    at = css.indexOf(opener, i);
  }
  return out.join("\n");
}

function ruleBody(source: string, selector: string): string {
  const at = source.indexOf(selector);
  expect(at, `missing rule ${selector}`).toBeGreaterThan(-1);
  const open = source.indexOf("{", at);
  return source.slice(open + 1, source.indexOf("}", open));
}

/** The drawer's own inline inset, set by the head and the lead pane. */
const INSET = /14px/;

test("the drawer head and its lead pane set one inline inset", () => {
  expect(ruleBody(css, ".sessions-head {")).toMatch(/padding:\s*14px\s+14px/);
  expect(ruleBody(css, ".settings.drawer .settings-lead-pane {")).toMatch(
    /padding:\s*8px\s+14px/,
  );
});

test("the narrow shell keeps every band of the settings drawer on that inset", () => {
  const narrow = narrowShellCss();
  // The tile grid is the section picker; it was hugging the drawer edge while
  // the text above it stood 14px in.
  expect(ruleBody(narrow, ".settings-tile-grid {")).toMatch(
    /padding:\s*4px\s+14px/,
  );
  // The section detail scrolls in the same band.
  expect(ruleBody(narrow, ".settings.drawer .settings-scroll {")).toMatch(
    new RegExp(`padding-inline:\\s*${INSET.source}`),
  );
  // So does the reload / save footer.
  expect(
    ruleBody(narrow, ".settings.drawer .settings-footer-actions {"),
  ).toMatch(new RegExp(`padding-inline:\\s*${INSET.source}`));
});
