/**
 * Contract: the layout grid (DESIGN.md, "Layout grid") is the only source of
 * viewport widths. Every width query in styles.css and every matchMedia in the
 * code uses an edge of a tier - phone, tablet, desktop, wide - or one of the
 * component thresholds the grid lists by name. A new width is added to the grid
 * first: a threshold nobody wrote down is how the phone rules ended up split
 * between 480px and 520px while the shell broke at 1199px.
 */
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";

import {
  PHONE_MAX_WIDTH_PX,
  SHELL_STACK_MAX_WIDTH_PX,
  WIDE_RAIL_MIN_WIDTH_PX,
} from "./shellBreakpoint";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const design = readFileSync(join(dir, "../../../../DESIGN.md"), "utf8");

/** The "Layout grid" section of DESIGN.md, up to the next heading of its level. */
function gridSection(): string {
  const start = design.indexOf("### Layout grid");
  expect(start, "DESIGN.md has a Layout grid section").toBeGreaterThanOrEqual(0);
  const next = design.indexOf("\n### ", start + 1);
  return design.slice(start, next < 0 ? undefined : next);
}

/** The edges of the four tiers, as a min-width and as the max-width just under it. */
const TIER_EDGES = new Set([
  PHONE_MAX_WIDTH_PX,
  PHONE_MAX_WIDTH_PX + 1,
  SHELL_STACK_MAX_WIDTH_PX,
  SHELL_STACK_MAX_WIDTH_PX + 1,
  WIDE_RAIL_MIN_WIDTH_PX - 1,
  WIDE_RAIL_MIN_WIDTH_PX,
]);

/** Component thresholds the grid lists: each one is a row of its table. */
const COMPONENT_THRESHOLDS = new Map<number, string>([
  [640, "plan document card head"],
  [700, "swarm screen padding"],
  [761, "desktop rail pill"],
  [1279, "documentation outline"],
]);

/** Every px width named in a @media prelude of the stylesheet. */
function mediaWidths(): Array<{ prelude: string; px: number }> {
  const out: Array<{ prelude: string; px: number }> = [];
  for (const m of css.matchAll(/@media([^{]+)\{/g)) {
    const prelude = (m[1] ?? "").trim();
    for (const w of prelude.matchAll(/(?:min|max)-width:\s*(\d+)px/g)) {
      out.push({ prelude, px: Number(w[1]) });
    }
  }
  return out;
}

function sourceFiles(root: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(root)) {
    const path = join(root, name);
    if (statSync(path).isDirectory()) out.push(...sourceFiles(path));
    else if (/\.(ts|tsx)$/.test(name) && !/\.test\.(ts|tsx)$/.test(name)) out.push(path);
  }
  return out;
}

describe("the layout grid", () => {
  test("every width query of the stylesheet is a tier edge or a listed component threshold", () => {
    const strays = mediaWidths().filter(
      (w) => !TIER_EDGES.has(w.px) && !COMPONENT_THRESHOLDS.has(w.px),
    );
    expect(strays).toEqual([]);
  });

  test("the phone rules live under one query, not a second threshold of their own", () => {
    const widths = new Set(mediaWidths().map((w) => w.px));
    // The phone block used to sit at 520px and the turn line's caption at 480px.
    expect(widths.has(520)).toBe(false);
    expect(widths.has(480)).toBe(false);
    expect(widths.has(PHONE_MAX_WIDTH_PX)).toBe(true);
  });

  test("DESIGN.md names every tier edge and every component threshold the stylesheet uses", () => {
    const grid = gridSection();
    for (const px of new Set(mediaWidths().map((w) => w.px))) {
      expect(grid, `${px}px is in the Layout grid section`).toContain(`${px}px`);
    }
    for (const name of COMPONENT_THRESHOLDS.values()) {
      expect(grid.toLowerCase(), `${name} is a row of the grid`).toContain(name);
    }
  });

  test("the code asks for a width only through shellBreakpoint.ts", () => {
    const root = join(dir, "..");
    const offenders = sourceFiles(root)
      .filter((path) => !path.endsWith("shellBreakpoint.ts"))
      .filter((path) => /matchMedia\(\s*["'`]\([^)]*width:\s*\d+px/.test(readFileSync(path, "utf8")))
      .map((path) => relative(root, path));
    expect(offenders).toEqual([]);
  });
});
