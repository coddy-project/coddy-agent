import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));

function cssText(): string {
  return readFileSync(join(dir, "../../styles.css"), "utf8");
}

test("prompt enhancement control is anchored in the textarea's top-right corner", () => {
  const css = cssText();
  const block = css.match(/\.composer-enhance-btn\s*\{([^}]+)\}/s);
  expect(block).not.toBeNull();
  expect(block![1]).toMatch(/position:\s*absolute/);
  expect(block![1]).toMatch(/top:\s*\d+px/);
  expect(block![1]).toMatch(/right:\s*\d+px/);
});
