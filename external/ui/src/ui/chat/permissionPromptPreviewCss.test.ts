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

test("expanded previews stay bounded and scroll internally", () => {
  const css = cssText();
  const scrollingViewport = css.match(
    /\.permission-preview-viewport--scroll\s*\{[^}]*\}/s,
  );
  const staticViewport = css.match(
    /\.permission-preview-viewport--static\s*\{[^}]*\}/s,
  );

  expect(scrollingViewport?.[0]).toMatch(/overflow-y:\s*auto/);
  expect(scrollingViewport?.[0]).not.toMatch(/max-height:\s*none/);
  expect(staticViewport?.[0]).toMatch(/max-height:\s*none/);
  expect(staticViewport?.[0]).toMatch(/overflow:\s*visible/);
});

test("overflow toggles are left-aligned and taller on mobile", () => {
  const css = cssText();
  const toggle = css.match(/\.tool-overflow-toggle\s*\{[^}]*\}/s);
  const resultFooter = css.match(
    /\.coddy-tool-call-body\s*>\s*\.tool-call-result-card\s*\+\s*\.tool-result-toggle-row\s*\{[^}]*\}/s,
  );
  const mobile = css.match(
    /@media\s*\(max-width:\s*520px\)\s*\{[\s\S]*?\.tool-overflow-toggle\s*\{[^}]*\}/,
  );

  expect(toggle?.[0]).toMatch(/align-self:\s*flex-start/);
  expect(toggle?.[0]).toMatch(/margin:\s*-1px\s+0\s+0\s+8px/);
  expect(resultFooter?.[0]).toMatch(/margin-top:\s*-8px/);
  expect(mobile?.[0]).toMatch(/min-height:\s*36px/);
});
