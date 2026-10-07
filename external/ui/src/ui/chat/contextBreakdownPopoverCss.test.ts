import { expect, test } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const css = readFileSync(
  resolve(import.meta.dirname, "../../styles.css"),
  "utf8",
);

test("context breakdown uses the shared drawer head divider and a two-sided usage row", () => {
  expect(css).toMatch(/\.context-breakdown-head\s*\{[^}]*padding:/s);
  expect(css).toMatch(/\.sessions-head\s*\{[^}]*border-bottom:/s);
  expect(css).toMatch(
    /\.context-breakdown-usage-row\s*\{[^}]*display:\s*flex;[^}]*justify-content:\s*space-between;/s,
  );
  expect(css).toMatch(
    /\.context-breakdown-used\s*\{[^}]*font-variant-numeric:/s,
  );
});
