import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8");

/** Whitespace squashed, so a declaration Prettier broke over lines still matches. */
const squash = (s: string) => s.replace(/\s+/g, " ");

function ruleBody(selector: string, from = 0): string {
  const idx = css.indexOf(selector, from);
  expect(idx, `${selector} is missing from styles.css`).toBeGreaterThan(-1);
  const open = css.indexOf("{", idx);
  const close = css.indexOf("}", open);
  return squash(css.slice(open + 1, close));
}

// Issue #342: one step from the window's edge to the top bar, from the bar to
// the title and around the expand control of an expanded composer. The step
// is one variable, 14px on a desktop and 10px on the stacked shell.
test("the top rhythm is one variable, the stacked shell's a step of 10px", () => {
  expect(squash(css)).toMatch(/:root \{[^}]*--coddy-top-rhythm: 14px;/);
  const stacked = css.indexOf("--coddy-mobile-bar-h:");
  expect(
    squash(css.slice(css.lastIndexOf(":root {", stacked), stacked)),
  ).toContain("--coddy-top-rhythm: 10px;");
});

test("the pill and the title of the stacked shell stand one step apart", () => {
  expect(squash(css)).toContain(
    "--coddy-mobile-title-sticky-top: calc( var(--coddy-mobile-top-inset) - 4px + var(--coddy-top-rhythm) );",
  );
  expect(squash(css)).toContain(
    "padding: calc(var(--coddy-top-rhythm) + env(safe-area-inset-top, 0px)) 10px 0;",
  );
});

// The control is the scroll-to-bottom circle's twin over the composer's right
// edge, lifted above the jump while one shows: not a control in the field,
// where a chevron next to the jump read as another scroll arrow.
test("the expand control is a circle over the composer, lifted above the jump", () => {
  const body = ruleBody(".chat-expand-composer {");
  expect(body).toMatch(/position: absolute/);
  expect(body).toMatch(/bottom: 100%/);
  expect(body).toMatch(/right: 0/);
  expect(body).toMatch(/width: 34px/);
  expect(body).toMatch(/height: 34px/);
  expect(body).toMatch(/border-radius: 50%/);
  expect(ruleBody(".chat-expand-composer.is-lifted {")).toMatch(
    /transform: translateY\(-44px\)/,
  );
  expect(ruleBody(".chat-expand-composer.is-expanded {")).toMatch(
    /margin-bottom: var\(--coddy-top-rhythm\)/,
  );
  expect(ruleBody(".composer-wrap.composer-wrap--expanded {")).toMatch(
    /padding-top: 0/,
  );
  expect(css).not.toContain(".composer-expand-btn");
});

test("the composer's pickers dim nothing behind them", () => {
  expect(ruleBody(".slash-sheet-backdrop--clear {")).toMatch(
    /background: transparent/,
  );
  // Expanded, the sheet keeps inside the field it opens in.
  expect(ruleBody(".slash-menu--sheet.slash-menu--above-composer {")).toContain(
    "var(--slash-sheet-room, 100dvh)",
  );
});
