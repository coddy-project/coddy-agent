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
  expect(list).toMatch(/flex:\s*1\s+1\s+120px/);
  expect(list).toMatch(/overflow-y:\s*auto/);
});

// The roomy body a short listing used to get from the list's own floor now
// comes from the dialog, where it is bounded by the same viewport fraction as
// the cap and therefore yields on a short window instead of overflowing.
test("dialog carries the roomy-body floor, clamped by the same viewport unit", () => {
  const modal = block(".workspace-modal");
  expect(modal).toMatch(/width:\s*min\(560px,/);
  expect(modal).toMatch(/min-height:\s*min\(520px,\s*80vh\)/);
  expect(modal).toMatch(/min-height:\s*min\(520px,\s*80dvh\)/);
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
  expect(modal).toMatch(/max-height:\s*min\(80vh,\s*760px\)/);
  expect(modal).toMatch(/max-height:\s*min\(80dvh,\s*760px\)/);
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

test("short listings keep a stable list area while modal chrome stays separated", () => {
  const list = block(".workspace-modal-list");
  expect(list).toMatch(/flex:\s*1\s+1\s+120px/);
  expect(list).toMatch(/min-height:\s*0\b/);
  // The head's rule is the drawers' head rule the dialog's head is drawn with.
  expect(block(".sessions-head")).toMatch(/border-bottom:\s*1px solid/);
  expect(block(".workspace-modal-actions")).toMatch(/border-top:\s*1px solid/);
});

test("hidden and symlink folders retain clear, compact row treatments", () => {
  const css = cssText();
  expect(css).toMatch(/\.workspace-modal-row--hidden\s*\{[^}]*opacity:\s*0\.6/);
  expect(css).toMatch(/\.workspace-modal-row-target\s*\{/);
  // A link's long target is what gets cut, not its name.
  expect(block(".workspace-modal-row-name")).toMatch(/flex:\s*0\s+0\s+auto/);
  expect(block(".workspace-modal-row-target")).toMatch(/flex:\s*1\s+1\s+0\b/);
});

// The dialog stays a centred card up to the tablet tier, and a phone opens it
// where History opens: the box and the glass tokens of .sessions.drawer on the
// stacked shell. A stacked-shell rule (max-width: 1199px) used to stretch it to
// the full width and height of a tablet, an empty wall of a dialog for a folder
// with three children.
test("a phone opens the folder modal where History opens", () => {
  const css = cssText();
  const phone =
    /@media \(max-width: 599px\)\s*\{\s*\.workspace-modal\s*\{([^}]*)\}/.exec(
      css,
    );
  expect(phone, "phone rule for .workspace-modal").not.toBeNull();
  const rule = phone![1];
  expect(rule).toMatch(/position:\s*fixed/);
  expect(rule).toMatch(/top:\s*calc\(var\(--coddy-mobile-top-inset\) \+ 6px\)/);
  expect(rule).toMatch(/bottom:\s*14px/);
  expect(rule).toMatch(/left:\s*max\(10px, env\(safe-area-inset-left, 0px\)\)/);
  expect(rule).toMatch(
    /right:\s*max\(10px, env\(safe-area-inset-right, 0px\)\)/,
  );
  expect(rule).toMatch(/background:\s*var\(--coddy-glass-panel-bg\)/);
  expect(rule).toMatch(/border-radius:\s*var\(--coddy-glass-panel-radius\)/);
  expect(css).not.toMatch(
    /@media \(max-width: 1199px\)\s*\{\s*\.workspace-modal\s*\{/,
  );
});

// The head is the drawers' head (.sessions-head, as History and the Scheduler
// open with); overriding its font made the title a 16px regular line.
test("the folder modal head keeps the drawers' head font", () => {
  const head = block(".workspace-modal-head");
  expect(head).not.toMatch(/font-size|font-weight/);
});

// Inputs and buttons do not inherit the page font, so every control of the
// dialog has to ask for it, or it is drawn in the browser's control font.
test("the folder modal's controls use the app font", () => {
  for (const selector of [
    ".workspace-modal-path",
    ".workspace-modal-row",
    ".workspace-modal-new-name",
    ".workspace-modal-btn",
  ]) {
    expect(block(selector), selector).toMatch(/font:\s*inherit/);
  }
});

// New folder is a square on the scheduler's add-button colours: the colour rules
// are the scheduler's own, so the two cannot drift apart in any theme.
test("new folder shares the scheduler add button colours and is square", () => {
  const css = cssText();
  expect(css).toMatch(
    /\.scheduler-btn-primary,\s*\.workspace-modal-btn--add\s*\{[^}]*background:/,
  );
  expect(css).toMatch(
    /\[data-theme="light"\] \.scheduler-btn-primary,\s*\[data-theme="light"\] \.workspace-modal-btn--add\s*\{[^}]*background:/,
  );
  const add = block(".workspace-modal-btn--add");
  const width = /(?:^|\n)\s*width:\s*(\d+)px/.exec(add);
  const height = /(?:^|\n)\s*height:\s*(\d+)px/.exec(add);
  expect(width && height && width[1] === height[1], add).toBe(true);
});

// The footer reads New folder, Show hidden, then Cancel / Open on the right:
// the switch's auto margin does the split.
test("show hidden sits next to new folder on the left of the footer", () => {
  expect(block(".workspace-modal-show-hidden")).toMatch(/margin-right:\s*auto/);
  expect(cssText()).not.toMatch(/\.workspace-modal-btn--lead\s*\{/);
});
