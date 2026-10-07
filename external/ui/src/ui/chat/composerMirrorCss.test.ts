import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.#*+?^${}()|[\]\\]/g, "\\$&");
  const m = new RegExp(`\\n${escaped}\\s*\\{([^}]+)\\}`, "s").exec(css);
  expect(m, `${selector} rule`).not.toBeNull();
  return m![1]!;
}

// The mirror draws the text the masked textarea hides, so the two text boxes
// have to be the same width to the fraction of a pixel at every page zoom and
// screen density. The width the scrollbar takes is left to the browser on both:
// a stable gutter on the textarea and on the mirror's clipping box, never a
// width measured in script, which rounds to whole pixels and goes stale when
// the zoom or the density changes (npm run check:caret measures the result).
test("the mirror reserves the textarea's scrollbar gutter itself", () => {
  expect(rule("textarea#composer")).toMatch(/scrollbar-gutter:\s*stable/);
  const mirror = rule(".composer-mirror");
  expect(mirror).toMatch(/scrollbar-gutter:\s*stable/);
  expect(mirror).toMatch(/overflow:\s*hidden/);
});

// The mirror scrolls as a scroll container of its own, so a fractional offset
// snaps to the same device pixel as the textarea's; a transform on the text
// does not snap and set the lines half a pixel apart at 125%.
test("the mirror's text is not moved by a transform", () => {
  expect(rule(".composer-mirror-inner")).not.toMatch(/transform/);
});
