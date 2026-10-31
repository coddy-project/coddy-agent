import { afterEach, describe, expect, test, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import * as canvasBackdrop from "./canvasBackdrop";
import { setUiSimple } from "./uiSimple";
import {
  backdropSnapshot,
  clearWallpaper,
  followBackdrop,
  setSmoothBackdrop,
  setWallpaper,
  setWallpaperDim,
  SMOOTH_BACKDROP_COOKIE,
  WALLPAPER_DIM_COOKIE,
  WALLPAPER_MAX_BYTES,
} from "./backdropPrefs";

afterEach(async () => {
  setUiSimple(false);
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  setSmoothBackdrop(false);
  await clearWallpaper();
  for (const name of [SMOOTH_BACKDROP_COOKIE, WALLPAPER_DIM_COOKIE]) {
    document.cookie = `${name}=; Path=/; Max-Age=0`;
  }
});

/** A picture of the given size as createImageBitmap would read it. */
function pictureOf(width: number, height: number) {
  vi.stubGlobal("createImageBitmap", async () => ({
    width,
    height,
    close() {},
  }));
  // jsdom has no object URLs
  Object.assign(URL, {
    createObjectURL: vi.fn(() => "blob:picture"),
    revokeObjectURL: vi.fn(),
  });
  return new Blob(["x"], { type: "image/png" });
}

describe("smooth gradients", () => {
  test("simplified mode suspends the painter and restores the saved choice when off", async () => {
    const stopCanvas = vi.fn();
    const startCanvas = vi
      .spyOn(canvasBackdrop, "followCanvasBackdrop")
      .mockReturnValue(stopCanvas);
    setSmoothBackdrop(true);
    const stop = followBackdrop(document);
    try {
      await waitFor(() => expect(startCanvas).toHaveBeenCalledTimes(1));
      setUiSimple(true);
      await Promise.resolve();
      expect(stopCanvas).toHaveBeenCalledTimes(1);
      expect(document.documentElement.dataset.smoothBackdrop).toBeUndefined();
      expect(backdropSnapshot().smooth).toBe(true);
      expect(document.cookie).toContain(`${SMOOTH_BACKDROP_COOKIE}=on`);
      setUiSimple(false);
      await Promise.resolve();
      await waitFor(() => expect(startCanvas).toHaveBeenCalledTimes(2));
      expect(document.documentElement.dataset.smoothBackdrop).toBe("true");
    } finally {
      stop();
    }
  });

  test("a page loaded in simplified mode does not start the saved painter", () => {
    const startCanvas = vi
      .spyOn(canvasBackdrop, "followCanvasBackdrop")
      .mockReturnValue(vi.fn());
    setUiSimple(true);
    setSmoothBackdrop(true);
    const stop = followBackdrop(document);
    try {
      expect(startCanvas).not.toHaveBeenCalled();
      expect(backdropSnapshot().smooth).toBe(true);
    } finally {
      stop();
    }
  });

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

describe("wallpaper", () => {
  test("a picture of at least 1280x720 becomes the wallpaper", async () => {
    const result = await setWallpaper(pictureOf(1920, 1080));
    expect(result).toEqual({ refusal: null, width: 1920, height: 1080 });
    expect(backdropSnapshot().wallpaper).toBe("blob:picture");
    await clearWallpaper();
    expect(backdropSnapshot().wallpaper).toBeNull();
  });

  test("a picture under the minimum size is refused with both sizes", async () => {
    expect(await setWallpaper(pictureOf(800, 600))).toEqual({
      refusal: "too-small",
      width: 800,
      height: 600,
    });
    expect(backdropSnapshot().wallpaper).toBeNull();
  });

  test("a file that is not a picture or is too large is refused", async () => {
    expect(
      (await setWallpaper(new Blob(["x"], { type: "text/plain" }))).refusal,
    ).toBe("not-image");
    const big = { type: "image/png", size: WALLPAPER_MAX_BYTES + 1 } as Blob;
    expect((await setWallpaper(big)).refusal).toBe("too-large");
    expect(backdropSnapshot().wallpaper).toBeNull();
  });

  test("dimming is kept in a cookie and bounded to 0..90", () => {
    setWallpaperDim(55);
    expect(backdropSnapshot().dim).toBe(55);
    expect(document.cookie).toContain(`${WALLPAPER_DIM_COOKIE}=55`);
    setWallpaperDim(200);
    expect(backdropSnapshot().dim).toBe(90);
    setWallpaperDim(-5);
    expect(backdropSnapshot().dim).toBe(0);
  });

  test("<html> carries the wallpaper and its dimming while one is set", async () => {
    const stop = followBackdrop(document);
    await setWallpaper(pictureOf(1920, 1080));
    setWallpaperDim(30);
    const root = document.documentElement;
    expect(root.dataset.wallpaper).toBe("true");
    expect(root.style.getPropertyValue("--coddy-wallpaper")).toBe(
      'url("blob:picture")',
    );
    expect(root.style.getPropertyValue("--coddy-wallpaper-dim")).toBe("30%");
    await clearWallpaper();
    expect(root.dataset.wallpaper).toBeUndefined();
    stop();
  });

  test("a gallery photo keeps its author with it, and Remove forgets both", async () => {
    const credit = {
      author: "Tim de Groot",
      url: "https://unsplash.com/photos/x",
    };
    await setWallpaper(pictureOf(1920, 1080), credit);
    expect(backdropSnapshot().credit).toEqual(credit);
    await clearWallpaper();
    expect(backdropSnapshot().credit).toBeNull();
  });
});
