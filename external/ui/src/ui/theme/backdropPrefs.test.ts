import { afterEach, describe, expect, test, vi } from "vitest";
import {
  backdropSnapshot,
  followBackdrop,
  setSmoothBackdrop,
  SMOOTH_BACKDROP_COOKIE,
} from "./backdropPrefs";

afterEach(() => {
  vi.restoreAllMocks();
  setSmoothBackdrop(false);
  document.cookie = `${SMOOTH_BACKDROP_COOKIE}=; Path=/; Max-Age=0`;
});

describe("smooth gradients", () => {
  test("the switch is remembered in a cookie", () => {
    setSmoothBackdrop(true);
    expect(document.cookie).toContain(`${SMOOTH_BACKDROP_COOKIE}=on`);
    expect(backdropSnapshot().smooth).toBe(true);
    setSmoothBackdrop(false);
    expect(document.cookie).not.toContain(SMOOTH_BACKDROP_COOKIE);
    expect(backdropSnapshot().smooth).toBe(false);
  });

  test("<html> is marked only while the canvas runs", () => {
    // A browser without a 2D context (jsdom has none): the canvas cannot
    // start, so the panels keep their own glass and nothing is marked.
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    const stop = followBackdrop(document);
    setSmoothBackdrop(true);
    expect(document.querySelector("canvas.canvas-backdrop")).toBeNull();
    expect(document.documentElement.dataset.smoothBackdrop).toBeUndefined();
    stop();
  });
});
