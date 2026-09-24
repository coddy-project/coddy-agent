import { expect, test } from "vitest";

import {
  TRANSCRIPT_BOTTOM_THRESHOLD_PX,
  documentScrollBottom,
  documentTranscriptMetrics,
  easeTranscriptJump,
  elementScrollBottom,
  elementTranscriptMetrics,
  isTranscriptAtBottom,
  keyboardInset,
  transcriptDistanceFromBottom,
  transcriptJumpDurationMs,
} from "./transcriptScrollPosition";

test("distance from the bottom is what is left below the viewport", () => {
  expect(
    transcriptDistanceFromBottom({
      scrollHeight: 1000,
      scrollTop: 100,
      clientHeight: 400,
    }),
  ).toBe(500);
});

test("overscroll past the end never reports a negative distance", () => {
  expect(
    transcriptDistanceFromBottom({
      scrollHeight: 1000,
      scrollTop: 640,
      clientHeight: 400,
    }),
  ).toBe(0);
});

test("the bottom is a band, not a single pixel row", () => {
  const atBottom = {
    scrollHeight: 1000,
    scrollTop: 1000 - 400 - (TRANSCRIPT_BOTTOM_THRESHOLD_PX - 1),
    clientHeight: 400,
  };
  expect(isTranscriptAtBottom(atBottom)).toBe(true);
  // One pixel further up is outside the band: the transcript stops following.
  expect(isTranscriptAtBottom({ ...atBottom, scrollTop: 519 })).toBe(false);
});

test("a transcript shorter than its viewport is already at the bottom", () => {
  expect(
    isTranscriptAtBottom({
      scrollHeight: 200,
      scrollTop: 0,
      clientHeight: 400,
    }),
  ).toBe(true);
});

test("element metrics come from the scroll viewport itself", () => {
  const el = document.createElement("div");
  Object.defineProperty(el, "scrollHeight", { value: 1000 });
  Object.defineProperty(el, "clientHeight", { value: 400 });
  el.scrollTop = 250;
  expect(elementTranscriptMetrics(el)).toEqual({
    scrollHeight: 1000,
    scrollTop: 250,
    clientHeight: 400,
  });
});

test("document metrics come from the scrolling document and the viewport height", () => {
  const view = {
    scrollY: 120,
    innerHeight: 700,
    document: { documentElement: { scrollHeight: 2000 } },
  } as unknown as Window;
  expect(documentTranscriptMetrics(view)).toEqual({
    scrollHeight: 2000,
    scrollTop: 120,
    clientHeight: 700,
  });
});

test("the scroll bottom of a viewport is the last reachable scrollTop", () => {
  const el = document.createElement("div");
  Object.defineProperty(el, "scrollHeight", { value: 1200 });
  Object.defineProperty(el, "clientHeight", { value: 400 });
  expect(elementScrollBottom(el)).toBe(800);
});

test("a viewport taller than its content has nowhere to scroll", () => {
  const el = document.createElement("div");
  Object.defineProperty(el, "scrollHeight", { value: 200 });
  Object.defineProperty(el, "clientHeight", { value: 400 });
  expect(elementScrollBottom(el)).toBe(0);
});

test("the document scroll bottom takes the taller of body and documentElement", () => {
  const view = {
    innerHeight: 800,
    document: {
      body: { scrollHeight: 2400 },
      documentElement: { scrollHeight: 2000 },
    },
  } as unknown as Window;
  expect(documentScrollBottom(view)).toBe(1600);
});

test("the jump stays brisk for a short hop and bounded for a long one", () => {
  expect(transcriptJumpDurationMs(100)).toBe(220);
  expect(transcriptJumpDurationMs(1000)).toBe(220);
  expect(transcriptJumpDurationMs(2000)).toBe(440);
  expect(transcriptJumpDurationMs(40000)).toBe(460);
});

test("the jump leaves fast and settles into the end", () => {
  expect(easeTranscriptJump(0)).toBe(0);
  expect(easeTranscriptJump(1)).toBe(1);
  // Past the halfway mark in the first third of the time, and the last tenth
  // of the travel spends the final third: fast out, soft landing.
  expect(easeTranscriptJump(1 / 3)).toBeGreaterThan(0.6);
  expect(easeTranscriptJump(2 / 3)).toBeGreaterThan(0.95);
  // Monotonic, and clamped outside the unit interval.
  expect(easeTranscriptJump(-1)).toBe(0);
  expect(easeTranscriptJump(2)).toBe(1);
});

// An on-screen keyboard that overlays the page (iOS Safari, and any browser
// that ignores interactive-widget=resizes-content) leaves innerHeight alone and
// shrinks the visual viewport. What the reader sees is the visual viewport, so
// that is what "at the bottom" and "the bottom" are measured against.
test("with an overlaying keyboard the document is measured by what is visible", () => {
  const view = {
    scrollY: 1300,
    innerHeight: 844,
    visualViewport: { height: 500, offsetTop: 0, scale: 1 },
    document: {
      body: { scrollHeight: 1800 },
      documentElement: { scrollHeight: 1800 },
    },
  } as unknown as Window;
  // The page shows 1300..1800: the end of the document is in view.
  expect(documentTranscriptMetrics(view)).toEqual({
    scrollHeight: 1800,
    scrollTop: 1300,
    clientHeight: 500,
  });
  expect(isTranscriptAtBottom(documentTranscriptMetrics(view))).toBe(true);
  // The bottom is where the end of the document meets the top of the keyboard,
  // not 1800 - 844 = 956, which is 344px back up the page.
  expect(documentScrollBottom(view)).toBe(1300);
});

test("a visual viewport panned down the page counts from where it stands", () => {
  const view = {
    scrollY: 956,
    innerHeight: 844,
    visualViewport: { height: 500, offsetTop: 344, scale: 1 },
    document: {
      body: { scrollHeight: 1800 },
      documentElement: { scrollHeight: 1800 },
    },
  } as unknown as Window;
  expect(documentTranscriptMetrics(view).scrollTop).toBe(1300);
  expect(documentScrollBottom(view)).toBe(956);
});

// The docked composer is fixed to the bottom of the layout viewport, which an
// overlaying keyboard covers: it is lifted by what the keyboard hides.
test("the keyboard inset is the part of the layout viewport the keyboard covers", () => {
  const view = (vv: Record<string, number> | undefined) =>
    ({ innerHeight: 844, visualViewport: vv }) as unknown as Window;
  expect(keyboardInset(view({ height: 500, offsetTop: 0, scale: 1 }))).toBe(344);
  // Panned so its bottom meets the layout viewport's: nothing is covered.
  expect(keyboardInset(view({ height: 500, offsetTop: 344, scale: 1 }))).toBe(0);
  // No keyboard, a keyboard that resizes the page, or no API at all.
  expect(keyboardInset(view({ height: 844, offsetTop: 0, scale: 1 }))).toBe(0);
  expect(keyboardInset(view(undefined))).toBe(0);
  // A pinch zoom shrinks the visual viewport too, and is not a keyboard.
  expect(keyboardInset(view({ height: 422, offsetTop: 100, scale: 2 }))).toBe(0);
});
