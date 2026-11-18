import { expect, test } from "vitest";
import { stylesPartial } from "../../stylesSource";

/** The desktop block of styles/shell/_stacked.scss, braces matched. */
function desktopBlock(): string {
  const css = stylesPartial("shell/stacked")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\s+/g, " ");
  const at = css.indexOf("@media (min-width: 1200px) {");
  expect(at, "the desktop block of styles/shell/_stacked.scss").toBeGreaterThan(
    -1,
  );
  let depth = 0;
  for (let i = css.indexOf("{", at); i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}" && --depth === 0) return css.slice(at, i + 1);
  }
  throw new Error("the desktop block is not closed");
}

/** One rule's body out of a block, the selector matched whole. */
function rule(block: string, selector: string): string {
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const found = new RegExp(`(?:^|[{};] )${esc} \\{([^}]*)\\}`).exec(block);
  expect(found, `${selector} in the desktop block`).not.toBeNull();
  return found?.[1] ?? "";
}

// The chat column is the window's height on a desktop, so the transcript's
// scroller - and its scrollbar - runs from the top kerb to the bottom one and a
// row scrolls right up to both edges. The column used to keep 14px clear above
// and below, which stopped the bar short of the window at both ends.
test("the desktop chat column fills the window's height", () => {
  expect(rule(desktopBlock(), ".main")).toMatch(/padding: 0;/);
});

// What the column stopped keeping clear its contents keep: the sticky head pads
// itself by those 14px, so the chat header stands exactly where it did and only
// the text passing behind it reaches the top edge.
test("the chat header keeps its distance from the top kerb", () => {
  expect(rule(desktopBlock(), ".chat-scroll-sticky-head")).toMatch(
    /padding-top: 14px;/,
  );
});

// The docked block pads itself by the same 14px under the composer's card, as
// the stacked shell already does. That padding is also what ChatScreen reads
// for `contentEndAboveBlockEnd`, so the transcript parked at its end keeps its
// two row gaps over the card rather than hiding a row behind it.
test("the composer keeps its distance from the bottom kerb", () => {
  expect(rule(desktopBlock(), ".chat-bottom")).toMatch(/padding-bottom: 14px;/);
});

// The start screen is not a scroller and takes no part in this: it keeps the
// room it always had, over and under the hero.
test("the start screen keeps the room around the hero", () => {
  expect(rule(desktopBlock(), ".main.is-empty")).toMatch(
    /padding: 24px 0 14px;/,
  );
});
