/**
 * Contract: the label of a tool row and the text that trails it - the target the
 * call names, the failure marker, the duration - sit on one baseline.
 *
 * They are three different type sizes in two different families (a 14px system
 * label, a 12px monospace target, a 12px system duration). Centring their boxes
 * lines up the boxes and leaves the baselines a fraction apart, which reads as
 * the trailing text floating above the label. Aligning the boxes on their
 * baselines is the only thing that puts the text itself on one line.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`${escaped}\\s*\\{[^}]*\\}`, "s").exec(css)?.[0] ?? "";
}

test("the row's label, target and duration align on their baselines", () => {
  const head = rule(".thinking-head");
  expect(head).toMatch(/align-items:\s*baseline/);
  expect(head).not.toMatch(/align-items:\s*center/);
});

test("the target shares the line box of the label beside it", () => {
  // A trailing box with a line-height of its own makes the row's height depend on
  // which of the two happens to be taller.
  expect(rule(".tool-summary-target")).toMatch(/line-height:\s*20px/);
});

test("the fold chevron is centred on the label's line, not on the whole row", () => {
  // The chevron is positioned inside .thinking-left. Centred on that box (top: 50%)
  // it followed the box's height, and a tool row's box is taller than its label's
  // line: the 12px target and duration sit a pixel or two lower on the baseline, so
  // the chevron sank below the label while the thinking row, whose box is exactly
  // one line, kept it level. The chevron is centred on the first line instead: half
  // the label's line box, whatever else the row carries or wraps onto a second line.
  const chevron = rule(".thinking-chevron");
  expect(chevron).not.toMatch(/top:\s*50%/);
  const label = rule(".thinking-label");
  const line = Number(/line-height:\s*(\d+(?:\.\d+)?)px/.exec(label)?.[1]);
  const top = Number(/top:\s*(\d+(?:\.\d+)?)px/.exec(chevron)?.[1]);
  expect(line).toBeGreaterThan(0);
  expect(top).toBe(line / 2);
  expect(chevron).toMatch(/translateY\(-50%\)/);
});
