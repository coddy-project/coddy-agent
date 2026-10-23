import { expect, test } from "vitest";
import { blockEdgePaddingTop, composerReserveFor } from "./composerReserve";

// The newest row of a transcript parked at its end stands two row gaps above
// the docked block's first visible edge (its card, or the usage banner over
// it), the live line included: one row gap read as pressed against the
// composer's card.
test("the newest row stands two row gaps above the docked block", () => {
  // Desktop: the block is 166px with 18px of empty padding over its card,
  // the scroll content ends where the block does, rows stand 10px apart.
  expect(
    composerReserveFor({
      blockHeight: 166,
      blockPaddingTop: 18,
      rowGap: 10,
      contentEndAboveBlockEnd: 0,
    }),
  ).toBe(168);
  // Phone: 14px of padding over the card, a 12px row gap, and the document's
  // content ends 6px above the fixed block's bottom (the shell's 18px of
  // padding under the transcript against the block's 12px).
  expect(
    composerReserveFor({
      blockHeight: 158,
      blockPaddingTop: 14,
      rowGap: 12,
      contentEndAboveBlockEnd: 6,
    }),
  ).toBe(162);
});

test("the reserve never drops under its floor", () => {
  expect(
    composerReserveFor({
      blockHeight: 40,
      blockPaddingTop: 0,
      rowGap: 10,
      contentEndAboveBlockEnd: 0,
    }),
  ).toBe(140);
});

// The block's empty padding counts only above its card. A usage banner rises
// over the composer inside the block, before the card's wrapper: the banner is
// then the first visible edge, and the wrapper's padding lies under it.
test("the empty padding over the block counts only while the card is its top", () => {
  const host = document.createElement("div");
  const wrap = document.createElement("div");
  wrap.className = "composer-wrap";
  wrap.style.paddingTop = "18px";
  host.append(wrap);
  document.body.append(host);
  try {
    expect(blockEdgePaddingTop(host)).toBe(18);
    const banner = document.createElement("div");
    banner.className = "usage-banner";
    host.prepend(banner);
    expect(blockEdgePaddingTop(host)).toBe(0);
  } finally {
    host.remove();
  }
});
