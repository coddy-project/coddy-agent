import { expect, test } from "vitest";

import {
  TRANSCRIPT_BOTTOM_THRESHOLD_PX,
  documentScrollBottom,
  documentTranscriptMetrics,
  easeTranscriptJump,
  elementScrollBottom,
  elementTranscriptMetrics,
  isTranscriptAtBottom,
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
