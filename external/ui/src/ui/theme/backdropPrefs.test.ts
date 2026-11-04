import { afterEach, describe, expect, test, vi } from "vitest";
import { setUiSimple } from "./uiSimple";
import {
  backdropSnapshot,
  clearWallpaper,
  followBackdrop,
  setWallpaper,
  setWallpaperDim,
  WALLPAPER_DIM_COOKIE,
  WALLPAPER_DIM_DEFAULT,
} from "./backdropPrefs";

afterEach(async () => {
  setUiSimple(false);
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  await clearWallpaper();
  document.cookie = `${WALLPAPER_DIM_COOKIE}=; Path=/; Max-Age=0`;
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

test("the backdrop store holds only wallpaper choices", () => {
  expect(Object.keys(backdropSnapshot()).sort()).toEqual(["dim", "wallpaper"]);
});

describe("wallpaper", () => {
  test("a readable picture becomes the wallpaper", async () => {
    const result = await setWallpaper(pictureOf(1920, 1080));
    expect(result).toEqual({ refusal: null, width: 1920, height: 1080 });
    expect(backdropSnapshot().wallpaper).toBe("blob:picture");
    await clearWallpaper();
    expect(backdropSnapshot().wallpaper).toBeNull();
  });

  test.each([
    [1, 1],
    [600, 1600],
    [100000, 1],
    [8192, 4096],
  ])(
    "a readable %sx%s picture becomes the wallpaper without a geometry limit",
    async (width, height) => {
      expect(await setWallpaper(pictureOf(width, height))).toEqual({
        refusal: null,
        width,
        height,
      });
      expect(backdropSnapshot().wallpaper).toBe("blob:picture");
    },
  );

  test("a readable picture larger than 20 MiB becomes the wallpaper", async () => {
    pictureOf(1920, 1080);
    const file = new Blob([new Uint8Array(20 * 1024 * 1024 + 1)], {
      type: "image/png",
    });
    expect((await setWallpaper(file)).refusal).toBeNull();
    expect(backdropSnapshot().wallpaper).toBe("blob:picture");
  });

  test("a file that is not a picture is refused", async () => {
    expect(
      (await setWallpaper(new Blob(["x"], { type: "text/plain" }))).refusal,
    ).toBe("not-image");
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

  test("wallpaper state carries no external credit metadata", async () => {
    await setWallpaper(pictureOf(1920, 1080));
    expect(backdropSnapshot()).not.toHaveProperty("credit");
    await clearWallpaper();
    expect(backdropSnapshot()).not.toHaveProperty("credit");
  });
});

test("non-finite dimming uses the default instead of writing NaN", () => {
  setWallpaperDim(NaN);
  expect(backdropSnapshot().dim).toBe(WALLPAPER_DIM_DEFAULT);
  expect(document.cookie).toContain(
    `${WALLPAPER_DIM_COOKIE}=${WALLPAPER_DIM_DEFAULT}`,
  );
});

test("high-resolution pictures are accepted and the validation bitmap is closed", async () => {
  const file = pictureOf(8192, 4096);
  const close = vi.fn();
  vi.stubGlobal("createImageBitmap", async () => ({
    width: 8192,
    height: 4096,
    close,
  }));
  expect((await setWallpaper(file)).refusal).toBeNull();
  expect(close).toHaveBeenCalledOnce();
  expect(backdropSnapshot().wallpaper).toBe("blob:picture");
});

describe("image element fallback validation", () => {
  function fallback(width?: number, height?: number) {
    const file = pictureOf(1920, 1080);
    vi.stubGlobal("createImageBitmap", undefined);
    vi.stubGlobal(
      "Image",
      class {
        naturalWidth = width;
        naturalHeight = height;
        onload?: () => void;
        onerror?: () => void;
        set src(value: string) {
          if (value)
            queueMicrotask(() =>
              width === undefined ? this.onerror?.() : this.onload?.(),
            );
        }
      },
    );
    return file;
  }
  test("a valid picture is decoded without createImageBitmap", async () => {
    expect(await setWallpaper(fallback(1920, 1080))).toEqual({
      refusal: null,
      width: 1920,
      height: 1080,
    });
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:picture");
  });
  test("a small picture is accepted without createImageBitmap", async () => {
    expect(await setWallpaper(fallback(800, 600))).toEqual({
      refusal: null,
      width: 800,
      height: 600,
    });
    expect(backdropSnapshot().wallpaper).toBe("blob:picture");
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:picture");
  });
  test("a corrupt picture is refused without createImageBitmap", async () => {
    expect((await setWallpaper(fallback())).refusal).toBe("unreadable");
    expect(backdropSnapshot().wallpaper).toBeNull();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:picture");
  });
  test("a missing decoder never bypasses image validation", async () => {
    const file = pictureOf(1920, 1080);
    vi.stubGlobal("createImageBitmap", undefined);
    vi.stubGlobal("Image", undefined);
    expect((await setWallpaper(file)).refusal).toBe("unreadable");
    expect(backdropSnapshot().wallpaper).toBeNull();
  });
});
