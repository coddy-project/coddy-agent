import { expect, test } from "vitest";
import { WALLPAPER_MIN_WIDTH } from "./backdropPrefs";
import { GALLERY, galleryPhotoUrl } from "./wallpaperGallery";

test("every gallery photo names its author and its Unsplash page", () => {
  expect(GALLERY.length).toBeGreaterThan(0);
  for (const photo of GALLERY) {
    expect(photo.author).not.toBe("");
    expect(photo.url).toMatch(/^https:\/\/unsplash\.com\/photos\//);
  }
});

test("a gallery photo is asked for at the screen's size, 16:9, never under the minimum", () => {
  const photo = GALLERY[0]!;
  expect(galleryPhotoUrl(photo, 1536, 1.25)).toBe(
    `https://picsum.photos/id/${photo.id}/1920/1080`,
  );
  expect(galleryPhotoUrl(photo, 2560, 1)).toBe(
    `https://picsum.photos/id/${photo.id}/2560/1440`,
  );
  expect(galleryPhotoUrl(photo, 3840, 2)).toBe(
    `https://picsum.photos/id/${photo.id}/3840/2160`,
  );
  const width = Number(galleryPhotoUrl(photo, 800, 1).split("/").at(-2));
  expect(width).toBeGreaterThanOrEqual(WALLPAPER_MIN_WIDTH);
});
