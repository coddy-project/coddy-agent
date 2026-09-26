import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

// The look of a save and of the first open (issue #359, DESIGN.md, Settings
// drawer: Saving and Loading). jsdom computes no styles, so the contract is
// held on the stylesheet itself.

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
  "utf8",
);

/** The body of the first rule whose selector list starts with selector. */
function ruleBody(selector: string): string {
  const at = css.indexOf(`\n${selector}`);
  expect(at, `missing rule ${selector}`).toBeGreaterThan(-1);
  const open = css.indexOf("{", at);
  return css.slice(open + 1, css.indexOf("}", open));
}

/** The keyframes block of name, braces and all. */
function keyframes(name: string): string {
  const at = css.indexOf(`@keyframes ${name}`);
  expect(at, `missing @keyframes ${name}`).toBeGreaterThan(-1);
  let depth = 0;
  for (let i = css.indexOf("{", at); i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}" && --depth === 0) return css.slice(at, i + 1);
  }
  throw new Error(`unterminated @keyframes ${name}`);
}

// The green is the message, so it is the state of the button, not a frame of
// the pop: under prefers-reduced-motion the animation goes and the green stays.
// It used to live only inside the keyframes, and reduced motion showed nothing.
test("a saved Save button is green as a state, and the pop only moves it", () => {
  const saved = ruleBody(".settings-btn-primary.is-saved");
  expect(saved).toMatch(/background:\s*rgba\(34, 197, 94/);
  expect(saved).toMatch(/border-color:\s*rgba\(34, 197, 94/);
  const pop = keyframes("settings-save-pop");
  expect(pop).not.toMatch(/background|border-color|box-shadow/);
  const reduced = reducedMotionRules().find((r) =>
    r.selectors.includes(".settings-btn-primary.is-saved"),
  );
  expect(reduced?.body.trim()).toBe("animation: none;");
});

/** Every rule inside a prefers-reduced-motion block: its selectors and body. */
function reducedMotionRules(): { selectors: string[]; body: string }[] {
  const out: { selectors: string[]; body: string }[] = [];
  const opener = "@media (prefers-reduced-motion: reduce) {";
  for (let at = css.indexOf(opener); at !== -1; at = css.indexOf(opener, at + 1)) {
    let i = at + opener.length;
    for (;;) {
      const open = css.indexOf("{", i);
      const close = css.indexOf("}", i);
      if (open === -1 || close < open) break;
      const end = css.indexOf("}", open);
      out.push({
        selectors: css
          .slice(i, open)
          .split(",")
          .map((s) => s.trim()),
        body: css.slice(open + 1, end),
      });
      i = end + 1;
    }
  }
  return out;
}

// The success line of the status band is gone for good; only failures speak
// there.
test("the status band has no success line", () => {
  expect(css).not.toMatch(/\.settings-ok\b/);
});

// Grey from the theme's own text colour reads on every theme, dark and light.
test("the skeleton is tinted from the theme's text colour", () => {
  const bar = ruleBody(".settings-skeleton-bar");
  expect(bar).toMatch(/var\(--text\)/);
  expect(bar).not.toMatch(/rgba\(255, 255, 255/);
});
