/**
 * Contract: a session whose turn is running carries a pulsing dot, the same
 * shape as the unread dot a finished background turn leaves behind and a third
 * darker, so the two read as one family - "working" and "done, not read yet" -
 * without being mistaken for each other at a glance.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`(^|\\n)${escaped}\\s*\\{[^}]*\\}`, "s").exec(css)?.[0] ?? "";
}

test("the activity dot is the unread dot, a third darker", () => {
  const unread = rule(".session-unread-dot");
  const dot = rule(".session-activity-dot");
  expect(dot).not.toBe("");
  for (const prop of ["width", "height", "border-radius"]) {
    const re = new RegExp(`${prop}:\\s*([^;]+);`);
    expect(re.exec(dot)?.[1]).toBe(re.exec(unread)?.[1]);
  }
  expect(dot).toMatch(
    /background:\s*color-mix\(in srgb,\s*rgba\(167,\s*139,\s*250,\s*0\.95\)\s*67%,\s*#000\)/,
  );
});

test("the activity dot pulses, and holds still for reduced motion", () => {
  expect(rule(".session-activity-dot")).toMatch(
    /animation:\s*session-activity-pulse\s/,
  );
  expect(css).toMatch(/@keyframes session-activity-pulse\s*\{/);
  expect(css).toMatch(
    /@media \(prefers-reduced-motion: reduce\)\s*\{\s*\.session-activity-dot\s*\{\s*animation:\s*none;/,
  );
});

test("the spinner is gone", () => {
  expect(css).not.toMatch(/\.session-activity-spinner\b/);
});
