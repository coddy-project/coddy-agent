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

test("patch previews expose a stable, readable line-number gutter", () => {
  const css = cssText();
  const gutter = css.match(
    /\.permission-preview-diff\s+\.diff-gutter\s*\{[^}]*\}/s,
  );
  const numbers = css.match(
    /\.permission-preview-diff\s+\.diff-no\s*\{[^}]*\}/s,
  );

  expect(gutter?.[0]).toMatch(/background:/);
  expect(numbers?.[0]).toMatch(/min-width:\s*4ch/);
  expect(numbers?.[0]).toMatch(/font-variant-numeric:\s*tabular-nums/);
});

test("code preview titles align with their content", () => {
  const css = cssText();
  const bar = css.match(/\.permission-preview-bar\s*\{[^}]*\}/s);
  const code = css.match(/\.permission-preview-code\s*\{[^}]*\}/s);

  expect(bar?.[0]).toMatch(/padding:\s*6px\s+8px\s+6px\s+12px/);
  expect(code?.[0]).toMatch(/padding:\s*10px\s+12px/);
});
