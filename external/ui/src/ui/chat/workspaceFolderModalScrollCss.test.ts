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

function block(selector: string): string {
  const css = cssText();
  const re = new RegExp(
    selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\s*\\{[^}]+\\}",
    "m",
  );
  const found = re.exec(css);
  expect(found, `missing CSS block for ${selector}`).not.toBeNull();
  return found![0];
}

// Issue #159 (Safari 26.6): the "Open folder" dialog is a column flex box capped
// by max-height and clipped by overflow: hidden. A positive min-height on the
// folder list stops that list from shrinking, so on a short window the head,
// path row and action buttons are laid out past the cap and clipped away with
// no scrollport to reach them. min-height: 0 is what makes a flex child a real
// scrollport; the roomy default body moves onto the dialog (test below).
test("folder list may shrink so the dialog never clips its own chrome", () => {
  const list = block(".workspace-modal-list");
  expect(list).toMatch(/min-height:\s*0\b/);
  expect(list).not.toMatch(/min-height:\s*[1-9]/);
  expect(list).toMatch(/flex:\s*1\s+1\s+auto/);
  expect(list).toMatch(/overflow-y:\s*auto/);
});

// The roomy body a short listing used to get from the list's own floor now
// comes from the dialog, where it is bounded by the same viewport fraction as
// the cap and therefore yields on a short window instead of overflowing.
test("dialog carries the roomy-body floor, clamped by the same viewport unit", () => {
  const modal = block(".workspace-modal");
  expect(modal).toMatch(/min-height:\s*min\(262px,\s*60vh\)/);
  expect(modal).toMatch(/min-height:\s*min\(262px,\s*60dvh\)/);
});

// Only the list may give up height; the head, the path row and the actions row
// carry the controls the user has to reach, so they never shrink.
test("dialog chrome rows are flex: none so only the list yields", () => {
  for (const selector of [
    ".workspace-modal-head",
    ".workspace-modal-path",
    ".workspace-modal-actions",
  ]) {
    expect(block(selector), selector).toMatch(/flex:\s*none/);
  }
});

// Safari excludes the dynamic browser chrome from vh but not from dvh, so a
// vh-only cap can push the dialog's footer under the toolbar. Keep the vh line
// as the fallback for engines without dvh.
test("dialog height cap is expressed in dvh with a vh fallback", () => {
  const modal = block(".workspace-modal");
  expect(modal).toMatch(/max-height:\s*min\(60vh,\s*520px\)/);
  expect(modal).toMatch(/max-height:\s*min\(60dvh,\s*520px\)/);
});

// A wheel gesture that reaches the end of the list used to chain into the page
// behind the modal, which reads as "the dialog does not scroll, the page does".
test("folder list contains its own overscroll", () => {
  expect(block(".workspace-modal-list")).toMatch(
    /overscroll-behavior:\s*contain/,
  );
});

// The scroll affordance is styled the way the settings lists are, so the thumb
// is legible against the dark panel wherever the platform draws one. Overlay
// scrollbars (macOS) still reserve no space; that is why the checks above, not
// this one, are what keep the dialog usable.
test("folder list styles its scrollbar like the other scrollable lists", () => {
  const css = cssText();
  expect(css).toMatch(
    /\.workspace-modal-list\s*\{[^}]*scrollbar-width:\s*thin/,
  );
  expect(css).toMatch(/\.workspace-modal-list::-webkit-scrollbar\s*\{/);
  expect(css).toMatch(/\.workspace-modal-list::-webkit-scrollbar-thumb\s*\{/);
});
