import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../styles.css",
);

function cssText(): string {
  return readFileSync(cssPath, "utf8");
}

// The row leads with its switch, not an icon: no rule sizes or dims a leading
// <svg> of the row any more.
test("an installed skill row has no leading icon rule", () => {
  expect(cssText()).not.toMatch(
    /\.skills-list-item(?:\.is-disabled)?\s*>\s*svg/,
  );
});

// Contract: the install-results menu floats (absolute) over the installed list
// so it never reflows the rows beneath it, and its anchor is positioned.
test("install results dropdown floats over the list, anchored to the control", () => {
  const css = cssText();
  expect(css).toMatch(/\.skills-install\s*\{[^}]*position:\s*relative/s);
  expect(css).toMatch(
    /\.skills-install-results\s*\{[^}]*position:\s*absolute/s,
  );
  // Sits above the following static list rows.
  expect(css).toMatch(/\.skills-install-results\s*\{[^}]*z-index:\s*\d+/s);
});

// The installed skills stack with no rule between them, like every list of
// the settings drawer.
test("installed skill rows carry no separator line", () => {
  const body = /\.skills-list-item\s*\{([^}]*)\}/.exec(cssText())?.[1] ?? "";
  expect(body).not.toBe("");
  expect(body).not.toMatch(/border/);
});
