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

// The capped preview viewport only works if the `--scroll` modifier can beat the
// `overflow: hidden` shorthand on the base rule. Same specificity, so source order
// decides: the modifier must come after the base, and nothing later may re-declare
// the shorthand on the viewport itself.
test("preview viewport modifiers win over the base overflow shorthand", () => {
  const css = cssText();

  // Anchor to column 0 so nested/media-query copies of the selector are skipped.
  const baseRule = css.match(/^\.permission-preview-viewport \{[^}]*\}/ms);
  const scrollRule = css.match(
    /^\.permission-preview-viewport--scroll \{[^}]*\}/ms,
  );
  const staticRule = css.match(
    /^\.permission-preview-viewport--static \{[^}]*\}/ms,
  );

  expect(baseRule).toBeTruthy();
  expect(scrollRule).toBeTruthy();
  expect(staticRule).toBeTruthy();

  expect(css.indexOf(scrollRule![0])).toBeGreaterThan(
    css.indexOf(baseRule![0]),
  );
  expect(css.indexOf(staticRule![0])).toBeGreaterThan(
    css.indexOf(baseRule![0]),
  );

  expect(baseRule![0]).toMatch(/overflow:\s*hidden/);
  expect(baseRule![0]).toMatch(/max-height:\s*190px/);
  expect(scrollRule![0]).toMatch(/overflow-y:\s*auto/);
  expect(staticRule![0]).toMatch(/max-height:\s*none/);
  expect(staticRule![0]).toMatch(/overflow:\s*visible/);
});
