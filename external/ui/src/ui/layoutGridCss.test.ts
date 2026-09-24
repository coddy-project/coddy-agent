/**
 * Contract: the layout grid (DESIGN.md, "Layout grid") is the only source of
 * viewport widths. Every width query in styles.css and every matchMedia in the
 * code uses an edge of a tier - phone, tablet, desktop, wide - or one of the
 * component thresholds the grid's table lists, in the direction it lists it. A
 * new width is added to that table first: a threshold nobody wrote down is how
 * the phone rules ended up split between 480px and 520px while the shell broke
 * at 1199px.
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

type Query = { kind: "min" | "max"; px: number };
const key = (q: Query) => `${q.kind}-width: ${q.px}px`;

/** The "Layout grid" section of DESIGN.md, up to the next heading of its level. */
function gridSection(): string {
  const start = design.indexOf("### Layout grid");
  expect(start, "DESIGN.md has a Layout grid section").toBeGreaterThanOrEqual(0);
  const next = design.indexOf("\n### ", start + 1);
  return design.slice(start, next < 0 ? undefined : next);
}

/** The rows of the grid's component table: `| **`min-width: 640px`** | name | why |`. */
function componentThresholds(): Map<string, string> {
  const out = new Map<string, string>();
  for (const m of gridSection().matchAll(
    /^\|\s*\*\*`(min|max)-width:\s*(\d+)px`\*\*\s*\|\s*([^|]+?)\s*\|/gm,
  )) {
    out.set(key({ kind: m[1] as "min" | "max", px: Number(m[2]) }), m[3]!);
  }
  return out;
}

/** The edges of the four tiers: where each ends, and where the next one starts. */
const TIER_EDGES = new Set(
  [
    { kind: "max", px: PHONE_MAX_WIDTH_PX },
    { kind: "min", px: PHONE_MAX_WIDTH_PX + 1 },
    { kind: "max", px: SHELL_STACK_MAX_WIDTH_PX },
    { kind: "min", px: SHELL_STACK_MAX_WIDTH_PX + 1 },
    { kind: "max", px: WIDE_RAIL_MIN_WIDTH_PX - 1 },
    { kind: "min", px: WIDE_RAIL_MIN_WIDTH_PX },
  ].map((q) => key(q as Query)),
);

/**
 * Every width a query names: `min-width: Npx` / `max-width: Npx`, and anything
 * else that mentions a width - range syntax, another unit - as a stray to fix.
 */
function widthsIn(query: string): { queries: Query[]; strays: string[] } {
  const queries: Query[] = [];
  const strays: string[] = [];
  const rest = query.replace(/\((min|max)-width:\s*(\d+)px\)/g, (_, kind: string, px: string) => {
    queries.push({ kind: kind as "min" | "max", px: Number(px) });
    return "";
  });
  if (/width/.test(rest)) strays.push(query.trim());
  return { queries, strays };
}

function mediaPreludes(): string[] {
  return [...css.matchAll(/@media([^{]+)\{/g)].map((m) => (m[1] ?? "").trim());
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
    const allowed = new Set([...TIER_EDGES, ...componentThresholds().keys()]);
    const strays: string[] = [];
    for (const prelude of mediaPreludes()) {
      const { queries, strays: other } = widthsIn(prelude);
      strays.push(...other);
      for (const q of queries) if (!allowed.has(key(q))) strays.push(`${prelude} (${key(q)})`);
    }
    expect(strays).toEqual([]);
  });

  test("the phone rules live under one query, not a second threshold of their own", () => {
    const used = new Set(mediaPreludes().flatMap((p) => widthsIn(p).queries.map(key)));
    // The phone block used to sit at 520px and the turn line's caption at 480px.
    expect(used.has("max-width: 520px")).toBe(false);
    expect(used.has("max-width: 480px")).toBe(false);
    expect(used.has(`max-width: ${PHONE_MAX_WIDTH_PX}px`)).toBe(true);
  });

  test("DESIGN.md names every tier edge the stylesheet uses, and every threshold it lists is in use", () => {
    const grid = gridSection();
    const used = new Set(mediaPreludes().flatMap((p) => widthsIn(p).queries.map(key)));
    for (const edge of TIER_EDGES) {
      if (used.has(edge)) expect(grid, `${edge} is in the Layout grid section`).toContain(edge);
    }
    const thresholds = componentThresholds();
    expect(thresholds.size, "the grid lists its component thresholds").toBeGreaterThan(0);
    for (const [threshold, name] of thresholds) {
      expect(used.has(threshold), `${threshold} (${name}) is still used`).toBe(true);
    }
  });

  test("the code asks for a width only through shellBreakpoint.ts", () => {
    const root = join(dir, "..");
    const offenders = sourceFiles(root)
      .filter((path) => !path.endsWith("shellBreakpoint.ts"))
      .filter((path) =>
        /matchMedia\(\s*["'`][^"'`]*width[^"'`]*\d+(px|em|rem)/.test(readFileSync(path, "utf8")),
      )
      .map((path) => relative(root, path));
    expect(offenders).toEqual([]);
  });
});
