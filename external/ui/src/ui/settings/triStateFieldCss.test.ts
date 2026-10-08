/**
 * Contract: the three-state control is one segmented group of native radios.
 *
 * jsdom does no layout, so the rules the look rests on are pinned at the
 * source: the radio is the invisible hit area that owns the focus, the chosen
 * position takes the accent, a focused one takes a ring, and a phone gets the
 * 44px touch target. The look itself is in DESIGN.md.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return [
    ...css.matchAll(
      new RegExp(`(?:^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, "g"),
    ),
  ]
    .map((m) => m[1])
    .join(";");
}

test("the radio fills its position, invisible, so a click anywhere in it picks it", () => {
  const body = rule(".tristate-option input");
  expect(body).toMatch(/position\s*:\s*absolute/);
  expect(body).toMatch(/opacity\s*:\s*0/);
  expect(body).toMatch(/inset\s*:\s*0/);
});

test("the chosen position carries the accent and a focused one a ring", () => {
  expect(rule(".tristate-option:has(input:checked)")).toMatch(
    /background\s*:[^;]*var\(--accent\)/,
  );
  expect(rule(".tristate-option:has(input:focus-visible)")).toMatch(
    /outline\s*:\s*2px solid/,
  );
});

test("the group wraps instead of widening the panel, and stays as wide as its options", () => {
  const body = rule(".tristate-options");
  expect(body).toMatch(/flex-wrap\s*:\s*wrap/);
  expect(body).toMatch(/max-width\s*:\s*100%/);
  expect(body).toMatch(/align-self\s*:\s*flex-start/);
});

test("a stacked shell gets a 44px touch target", () => {
  const phone = css.match(
    /@media \(max-width: 1199px\)\s*\{\s*\.tristate-option\s*\{([^}]*)\}/,
  );
  expect(phone?.[1]).toMatch(/min-height\s*:\s*44px/);
});
