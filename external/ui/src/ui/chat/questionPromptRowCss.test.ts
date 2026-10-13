import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../styles.css",
);

function ruleBodies(css: string, selector: string): string[] {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`(?:^|\\n)\\s*${escaped}\\s*\\{([^}]*)\\}`, "g");
  return [...css.matchAll(re)].map((m) => m[1] ?? "");
}

// The question card is a prompt in the transcript like the permission card, and
// both rows are flush with the transcript column: on a phone the card's edges meet
// the composer's under it on both sides. The question row used to carry a
// padding-left of its own and no padding on the right, which set the card in from
// the left edge only.
test("the question row is flush with the column on both sides, like the permission row", () => {
  const css = readFileSync(cssPath, "utf8");
  for (const selector of [".message-row-question", ".message-row-permission"]) {
    const bodies = ruleBodies(css, selector);
    expect(bodies.length, selector).toBeGreaterThan(0);
    for (const body of bodies) {
      expect(body, selector).not.toMatch(
        /padding(-left|-right|-inline[a-z-]*)?\s*:/,
      );
      expect(body, selector).not.toMatch(
        /margin(-left|-right|-inline[a-z-]*)\s*:/,
      );
      const margin = body.match(/(?:^|;|\s)margin\s*:\s*([^;]+);/);
      if (margin) {
        // margin: <top> 0 <bottom> - the sides stay at zero.
        const parts = margin[1]!.trim().split(/\s+/);
        expect(parts[1] ?? parts[0], selector).toBe("0");
      }
    }
  }
});
