/**
 * Contract: the banner that offers to take a sent edit back is one line - the
 * title and the note side by side - with Undo and the cross centred on that
 * line. A two-line body left Undo hanging between the lines and the cross,
 * which keeps the queue's top alignment, above them.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`(^|\\n)${escaped}\\s*\\{[^}]*\\}`, "s").exec(css)?.[0] ?? "";
}

test("the undo banner puts the title and the note on one line", () => {
  const body = rule(".composer-edit-banner--done .composer-edit-banner-body");
  expect(body).toMatch(/flex-direction:\s*row/);
  expect(body).toMatch(/align-items:\s*baseline/);
  expect(body).toMatch(/padding-top:\s*0/);
});

test("Undo and the cross are centred on that line", () => {
  expect(rule(".composer-edit-banner--done")).toMatch(/align-items:\s*center/);
  expect(rule(".composer-edit-banner--done > *")).toMatch(/align-self:\s*center/);
});
