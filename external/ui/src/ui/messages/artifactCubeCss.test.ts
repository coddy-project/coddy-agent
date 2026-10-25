import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../styles.scss"),
  "utf8",
);

/**
 * A declaration block on one line: Prettier breaks a long value over several,
 * indented, so the assertions read it with runs of whitespace as one space and
 * none inside the parentheses.
 */
function squash(body: string): string {
  return body
    .replace(/\s+/g, " ")
    .replace(/\(\s+/g, "(")
    .replace(/\s+\)/g, ")");
}

function block(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return squash(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`).exec(css)?.[1] ?? "");
}

test("inline artifact cards are compact cubes with low clamped file names", () => {
  expect(block(".tool-artifact-card")).toMatch(/width:\s*134px/);
  expect(block(".tool-artifact-card")).toMatch(/height:\s*140px/);
  expect(block(".tool-artifact-name")).toMatch(/align-self:\s*end/);
  expect(block(".tool-artifact-name")).toMatch(/-webkit-line-clamp:\s*2/);
  expect(css).toMatch(/\.tool-artifact-card:hover\s*\{/);
});

test("phone artifact cards retain an accessible action trigger", () => {
  expect(css).toMatch(
    /@media \(max-width: 599px\)[\s\S]*\.tool-artifact-card\s*\{[^}]*width:\s*min\(118px, calc\(50vw - 28px\)\)/,
  );
  expect(css).toMatch(
    /@media \(max-width: 599px\)[\s\S]*\.tool-artifact-card\s*\{[^}]*height:\s*132px/,
  );
  expect(css).toMatch(
    /@media \(max-width: 599px\)[\s\S]*\.inline-artifact-menu-trigger\s*\{[^}]*opacity:\s*1/,
  );
});

test("inline artifact groups are block rows with breathing room after files", () => {
  expect(block(".inline-artifacts")).toMatch(/display:\s*flex/);
  expect(block(".inline-artifacts")).toMatch(/margin:\s*12px 0 16px/);
  expect(block(".inline-artifacts")).toMatch(/max-width:\s*100%/);
});

test("image artifacts use a square extension badge and centered cover preview", () => {
  const badge = block(".inline-artifact-extension");
  expect(badge).toMatch(/display:\s*grid/);
  expect(badge).toMatch(/place-items:\s*center/);
  expect(badge).toMatch(/width:\s*var\(--artifact-badge-size\)/);
  expect(badge).toMatch(/height:\s*var\(--artifact-badge-size\)/);
  expect(block(".tool-artifact-card")).toMatch(/--artifact-badge-size:\s*28px/);
  expect(badge).toMatch(/border-radius:\s*6px/);
  expect(badge).toMatch(/pointer-events:\s*none/);
  expect(block(".inline-artifact-thumb")).toMatch(/object-fit:\s*cover/);
  expect(block(".inline-artifact-thumb")).toMatch(/object-position:\s*center/);
});

// The trigger is placed from the badge's own numbers, so it is level with the
// badge and mirrors it across the card at any size either of them takes: a
// trigger with a top and a right of its own sat 6px above the badge and 7px
// nearer its edge.
test("the actions trigger mirrors the extension badge from the badge's numbers", () => {
  const badge = block(".inline-artifact-extension");
  expect(badge).toMatch(/top:\s*var\(--artifact-badge-inset\)/);
  expect(badge).toMatch(/left:\s*var\(--artifact-badge-inset\)/);
  const trigger = block(".inline-artifact-menu-trigger");
  const offset =
    "calc\\(var\\(--artifact-badge-inset\\) \\+ \\(var\\(--artifact-badge-size\\) - var\\(--artifact-trigger-size\\)\\) / 2\\)";
  expect(trigger).toMatch(new RegExp(`top:\\s*${offset}`));
  expect(trigger).toMatch(new RegExp(`right:\\s*${offset}`));
  expect(trigger).toMatch(/width:\s*var\(--artifact-trigger-size\)/);
  expect(trigger).toMatch(/height:\s*var\(--artifact-trigger-size\)/);
  expect(trigger).toMatch(/display:\s*grid/);
  expect(trigger).toMatch(/place-items:\s*center/);
  // A phone's trigger is a larger target and stays centred on the badge.
  expect(css).toMatch(
    /@media \(max-width: 599px\)[\s\S]*\.tool-artifact-card\s*\{[^}]*--artifact-trigger-size:\s*32px/,
  );
});

test("a pointer finds the trigger on a hovered card, a touch screen sees it always", () => {
  expect(css).toMatch(
    /\.tool-artifact-card:hover \.inline-artifact-menu-trigger,[^{]*\{[^}]*opacity:\s*1/,
  );
  expect(css).toMatch(
    /@media \(hover: none\)\s*\{\s*\.inline-artifact-menu-trigger\s*\{[^}]*opacity:\s*1/,
  );
});

// The card is `overflow: hidden` (it clips a thumbnail to its corners), so a
// menu inside it was cut off and a click on the trigger drew nothing.
test("the actions menu is fixed to the window, outside the clipping card", () => {
  const menu = block(".inline-artifact-menu");
  expect(menu).toMatch(/position:\s*fixed/);
  expect(menu).not.toMatch(/top:\s*calc\(100%/);
  // A window shorter than the menu scrolls its items rather than cut them off.
  expect(menu).toMatch(/max-height:\s*calc\(100dvh - 16px\)/);
  expect(menu).toMatch(/overflow-y:\s*auto/);
});

// The row keeps 16px before the paragraph after it; cards that end the answer
// keep none, so the answer's box ends with them.
test("the last row of cards keeps no margin under it", () => {
  expect(css).toMatch(
    /\.inline-artifacts:last-child\s*\{[^}]*margin-bottom:\s*0/,
  );
});

// The whole card opens the file it was copied from; the trigger stands over
// the open button, or a click on the dots opened the file too.
test("the open button covers the card under the actions trigger", () => {
  const open = block(".tool-artifact-open");
  expect(open).toMatch(/position: absolute/);
  expect(open).toMatch(/inset: 0/);
  expect(open).toMatch(/z-index: 1/);
  expect(open).toMatch(/background: transparent/);
  expect(block(".inline-artifact-menu-trigger")).toMatch(/z-index: 2/);
  expect(block(".tool-artifact-open:focus-visible")).toMatch(/outline: 2px/);
});

// A deleted source is news, not a failure: the card steps back and says so,
// in muted text rather than the danger colour of a download that failed.
test("a card whose source was deleted is struck through and muted, not red", () => {
  expect(block(".tool-artifact-card--deleted .tool-artifact-name")).toMatch(
    /text-decoration: line-through/,
  );
  expect(
    block(".tool-artifact-card--deleted .inline-artifact-extension"),
  ).toMatch(/color: color-mix\(in srgb, var\(--text\) 52%/);
  const meta = block(".tool-artifact-meta--deleted");
  expect(meta).toMatch(/color: color-mix\(in srgb, var\(--text\) 74%/);
  expect(meta).not.toMatch(/danger/);
});
