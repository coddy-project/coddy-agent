import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));

function cssText(): string {
  return readFileSync(join(dir, "../../styles.css"), "utf8");
}

test("prompt enhancement control is compact in the composer context row", () => {
  const css = cssText();
  const block = css.match(/\.composer-enhance-btn\s*\{([^}]+)\}/s);
  expect(block).not.toBeNull();
  expect(block![1]).toMatch(/flex:\s*0 0 24px/);
  expect(block![1]).toMatch(/margin-left:\s*auto/);
  expect(block![1]).toMatch(/width:\s*24px/);
  expect(block![1]).toMatch(/height:\s*24px/);
  expect(block![1]).not.toMatch(/position:\s*absolute/);
});

test("prompt enhancement control is pinned to the mobile context row's top-right", () => {
  const css = cssText();
  expect(css).toMatch(
    /@media\s*\(max-width:\s*520px\)\s*\{\s*\.composer-context-row\s*\{[^}]*position:\s*relative[^}]*padding-right:\s*44px[^}]*\}\s*\.composer-enhance-btn\s*\{[^}]*position:\s*absolute[^}]*top:\s*10px[^}]*right:\s*12px/s,
  );
});
