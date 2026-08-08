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

// Regression: the tool-call foldout body (command output, args, "read" result)
// hardcoded the dark-theme pale grey `rgba(186, 192, 204, …)` with no light
// override, so on the Light theme that text was near-invisible on the pale
// surface. It must use the themed `--coddy-surface-code-fg` token instead.
test("tool-call body text uses the themed code foreground token", () => {
  const css = cssText();
  const rule = css.match(/\.coddy-tool-call-body \.tool-block\s*\{[^}]*\}/s);
  expect(rule).toBeTruthy();
  expect(rule![0]).toMatch(/color:\s*var\(--coddy-surface-code-fg\)/);
  expect(rule![0]).not.toMatch(/color:\s*rgba\(186/);
});

// Regression: the standalone raw tool result reused the same hardcoded dark grey
// text and a dark translucent background with no light override.
test("raw tool result uses themed tokens, not hardcoded dark values", () => {
  const css = cssText();
  const rule = css.match(/\.tool-block\.tool-result-raw\s*\{[^}]*\}/s);
  expect(rule).toBeTruthy();
  expect(rule![0]).toMatch(/color:\s*var\(--coddy-surface-code-fg\)/);
  expect(rule![0]).not.toMatch(/rgba\(186/);
});

test("diff block leaves vertical overflow to the clip and scroll viewport modes", () => {
  const css = cssText();
  const diffRule = css.match(/\.diff-block\s*\{[^}]*\}/s);
  const scrollRule = css.match(/\.tool-result-viewport--scroll\s*\{[^}]*\}/s);

  expect(diffRule).toBeTruthy();
  expect(diffRule![0]).toMatch(/overflow-x:\s*hidden/);
  expect(diffRule![0]).not.toMatch(/overflow:\s*hidden/);
  expect(scrollRule).toBeTruthy();
  expect(scrollRule![0]).toMatch(/overflow-y:\s*auto/);
});
