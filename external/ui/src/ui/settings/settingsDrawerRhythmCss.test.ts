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
  // and it stood 4px under the head's rule; it keeps the same 14px there.
  expect(ruleBody(narrow, ".settings-tile-grid {")).toMatch(
    /padding:\s*14px\s+14px/,
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

test("a disabled settings input reads as disabled, like a read-only one", () => {
  // The provider proxy URL is disabled while the row connects directly; with
  // the plain input look it would still read as a field to type into.
  const at = css.indexOf(".settings-input:disabled");
  expect(at, "missing .settings-input:disabled").toBeGreaterThan(-1);
  const open = css.indexOf("{", at);
  const body = css.slice(open + 1, css.indexOf("}", open));
  expect(body).toContain("background: var(--coddy-surface-muted)");
  expect(body).toContain("color: var(--muted)");
  expect(body).toContain("cursor: not-allowed");
});

// A folding fieldset (Advanced settings of a provider form): the chevron
// hangs in front of the name the way a transcript row's does, so the legend
// reaches 18px further left and the name lines up with every other legend;
// folded, the frame is gone (transparent, so nothing moves), never an empty
// box or a bare rule.
test("a folded fieldset has no frame and its chevron hangs left of the name", () => {
  expect(ruleBody(css, ".settings-fieldset--collapsible > legend {")).toMatch(
    /margin-left:\s*-24px/,
  );
  const folded = ruleBody(
    css,
    ".settings-fieldset--collapsible:not(.is-open) {",
  );
  expect(folded).toMatch(/border-color:\s*transparent/);
  expect(folded).toMatch(/background:\s*transparent/);
  expect(folded).not.toMatch(/border-width/);
});

// A fieldset's legend text starts on the line its fields' labels start on:
// the 6px of padding that opens the frame's gap is pulled back by 6px.
test("a fieldset legend lines up with the labels of its fields", () => {
  const legend = ruleBody(css, ".settings-fieldset > legend {");
  expect(legend).toMatch(/margin-left:\s*-6px/);
  expect(legend).toMatch(/padding:\s*0 6px/);
});

// A list section's lead line sits 8px over its first row: the column's 12px
// gap, less the 4px the paragraph gives back (it used to add its own 8px).
test("a list section's lead line sits close to its rows", () => {
  expect(ruleBody(css, ".settings-master > .settings-field-desc {")).toMatch(
    /margin:\s*0 0 -4px/,
  );
  expect(ruleBody(css, ".settings-master,\n.settings-detail {")).toMatch(
    /gap:\s*12px/,
  );
});

// A button standing alone in a field's column (the reasoning levels' Add)
// keeps its own width; the column is a stretching flex box.
test("a lone action button in a field column does not stretch", () => {
  expect(ruleBody(css, ".settings-row > .settings-row-action {")).toMatch(
    /align-self:\s*flex-start/,
  );
});

// A list row is its input and its trash, level with each other: a margin on
// the trash (a rule cut in half once left `margin-top: 22px` on every one)
// makes each row taller than its input and opens a gap above every entry.
test("a list row's trash carries no margin of its own", () => {
  const rules = [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)].filter(([, sel]) =>
    /\.settings-array-remove\b/.test(sel ?? ""),
  );
  expect(rules.length).toBeGreaterThan(0);
  for (const [, sel, body] of rules) {
    expect(body, sel).not.toMatch(/margin/);
  }
});
