import { describe, expect, test } from "vitest";

import {
  composerAutoCeilingPx,
  composerFieldHeightPx,
  type ComposerFieldMetrics,
} from "./composerHeight";

// The docked field of a desktop: 15px type at 1.5, 9px and 10px of padding,
// a 76px floor, in an 860px window, 500px of chat above the composer block.
const docked: ComposerFieldMetrics = {
  floorPx: 76,
  contentPx: 76,
  lineHeightPx: 22.5,
  chromePx: 19,
  viewportPx: 860,
  roomAbovePx: 500,
};

describe("the field follows its text", () => {
  test("a short draft keeps the floor", () => {
    expect(composerFieldHeightPx({ ...docked, contentPx: 41.5 }, false)).toBe(
      76,
    );
  });

  test("a longer draft grows the field to show it all", () => {
    expect(composerFieldHeightPx({ ...docked, contentPx: 154 }, false)).toBe(
      154,
    );
  });

  test("past eight lines the field stops growing and scrolls", () => {
    // 8 lines of 22.5px and 19px of padding.
    expect(composerAutoCeilingPx(docked)).toBe(199);
    expect(composerFieldHeightPx({ ...docked, contentPx: 1026 }, false)).toBe(
      199,
    );
  });

  test("a short viewport (a phone with its keyboard open) caps it lower", () => {
    const phone = { ...docked, viewportPx: 420, contentPx: 1026 };
    expect(composerFieldHeightPx(phone, false)).toBe(168);
  });

  test("the field never pushes the docked block over the chat header", () => {
    // A phone held sideways: 80px between the block and the header.
    const sideways = { ...docked, roomAbovePx: 80, contentPx: 1026 };
    expect(composerFieldHeightPx(sideways, false)).toBe(156);
  });

  test("the ceiling never goes under the floor", () => {
    // A landscape phone with its keyboard open leaves 150px.
    const tiny = { ...docked, viewportPx: 150, contentPx: 1026 };
    expect(composerAutoCeilingPx(tiny)).toBe(76);
    expect(composerFieldHeightPx(tiny, false)).toBe(76);
  });
});

describe("an expanded field takes the chat under its header", () => {
  test("whatever the text, it reaches the header", () => {
    expect(composerFieldHeightPx(docked, true)).toBe(576);
    expect(composerFieldHeightPx({ ...docked, contentPx: 1026 }, true)).toBe(
      576,
    );
  });

  test("with no room above it, it stays at its floor", () => {
    const cramped = { ...docked, roomAbovePx: -40, contentPx: 154 };
    expect(composerFieldHeightPx(cramped, true)).toBe(76);
  });

  test("with no header to reach (the start screen), expanding changes nothing", () => {
    const hero = { ...docked, roomAbovePx: Infinity, contentPx: 154 };
    expect(composerFieldHeightPx(hero, true)).toBe(154);
  });
});
