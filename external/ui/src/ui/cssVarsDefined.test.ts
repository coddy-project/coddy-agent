import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";

/**
 * A var() with no fallback that names a custom property nobody defines makes
 * the whole declaration invalid at computed-value time: `font: 12px/1.6
 * var(--font-mono)` left the Files window's line numbers in the interface's
 * font and the code in the browser's, so the two drifted apart line by line.
 * Every such var() names a property a stylesheet declares or the code sets.
 */

const src = join(__dirname, "..");

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) walk(path, out);
    else out.push(path);
  }
  return out;
}

test("every var() without a fallback names a custom property that is defined", () => {
  const files = walk(src);
  const css = files.filter((f) => f.endsWith(".css"));
  const code = files.filter((f) => /\.(tsx?|mjs)$/.test(f) && !f.endsWith(".test.ts") && !f.endsWith(".test.tsx"));
  const defined = new Set<string>();
  for (const f of css) {
    for (const m of readFileSync(f, "utf8").matchAll(/(--[\w-]+)\s*:/g)) defined.add(m[1]!);
  }
  // Properties the code sets on an element (style.setProperty, style objects).
  for (const f of code) {
    for (const m of readFileSync(f, "utf8").matchAll(/["'`](--[\w-]+)["'`]/g)) defined.add(m[1]!);
  }
  const missing: string[] = [];
  for (const f of css) {
    const lines = readFileSync(f, "utf8").split("\n");
    lines.forEach((line, i) => {
      for (const m of line.matchAll(/var\(\s*(--[\w-]+)\s*\)/g)) {
        if (!defined.has(m[1]!)) missing.push(`${f.slice(src.length + 1)}:${i + 1} ${m[1]}`);
      }
    });
  }
  expect(missing).toEqual([]);
});
