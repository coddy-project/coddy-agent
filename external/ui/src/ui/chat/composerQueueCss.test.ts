/**
 * Contract: a queued message reads as a frosted panel over the transcript, and its
 * cancel control is the app's one close control - a framed square in the top right
 * corner that lightens under the pointer - sized so a one-line message keeps its
 * proportions.
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

// The row was a 6% wash of the text colour: over a dark transcript that is almost
// transparent, and the words scrolling under the queue read through it.
test("a queued message is cut from the frosted glass panel", () => {
  const item = rule(".composer-queue-item");
  expect(item).toMatch(/background:\s*var\(--coddy-glass-panel-bg\)/);
  expect(item).toMatch(/backdrop-filter:\s*var\(--coddy-glass-panel-backdrop\)/);
  expect(item).toMatch(/-webkit-backdrop-filter:\s*var\(--coddy-glass-panel-backdrop\)/);
});

test("the cancel control sits in the top right corner", () => {
  expect(rule(".composer-queue-item")).toMatch(/align-items:\s*flex-start/);
  expect(rule(".composer-queue-remove")).toMatch(/align-self:\s*flex-start/);
});

test("the close control is a framed square that lightens under the pointer", () => {
  const close = rule(".sessions-close");
  expect(close).toMatch(/border:\s*1px solid/);
  expect(close).not.toMatch(/border-radius:\s*50%/);
  const hover = rule(".sessions-close:hover,\n.sessions-close:focus-visible");
  expect(hover).toMatch(/background:/);
  // The queue's control is the same element class at a size a one-line row can hold.
  const compact = rule(".sessions-close.composer-queue-remove");
  expect(compact).toMatch(/width:\s*24px/);
  expect(compact).toMatch(/height:\s*24px/);
});
