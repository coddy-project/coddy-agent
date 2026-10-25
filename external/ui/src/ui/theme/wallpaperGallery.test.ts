import { describe, expect, test } from "vitest";
import { WALLPAPER_MIN_WIDTH } from "./backdropPrefs";
import {
  GALLERY,
  GALLERY_SIZE,
  galleryPhotoUrl,
  shuffleGallery,
  type GalleryPhoto,
} from "./wallpaperGallery";
import { WALLPAPER_POOL } from "./wallpaperPool";

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

describe("shuffleGallery", () => {
  const ids = (photos: readonly GalleryPhoto[]) => photos.map((p) => p.id);

  test("the pool's photos name their author and Unsplash page, the first batch among them", () => {
    const pool = new Set(WALLPAPER_POOL.map(([id]) => id));
    expect(pool.size).toBe(WALLPAPER_POOL.length);
    expect(pool.size).toBeGreaterThanOrEqual(GALLERY_SIZE * 5);
    for (const [id, author, unsplash] of WALLPAPER_POOL) {
      expect(id).toMatch(/^\d+$/);
      expect(author).not.toBe("");
      expect(unsplash).toMatch(/^[\w-]+$/);
    }
    for (const photo of GALLERY) expect(pool.has(photo.id)).toBe(true);
  });

  test("a new batch is twelve pool photos not shown yet, until the pool runs out", async () => {
    let shown = new Set(ids(GALLERY));
    let current: readonly GalleryPhoto[] = GALLERY;
    const rounds = Math.floor(
      (WALLPAPER_POOL.length - GALLERY_SIZE) / GALLERY_SIZE,
    );
    for (let round = 0; round < rounds; round++) {
      const batch = await shuffleGallery(current);
      expect(batch).toHaveLength(GALLERY_SIZE);
      for (const photo of batch!) {
        expect(shown.has(photo.id)).toBe(false);
        expect(photo.url).toMatch(/^https:\/\/unsplash\.com\/photos\/[\w-]+$/);
      }
      shown = new Set([...shown, ...ids(batch!)]);
      current = batch!;
    }
    // then it starts over, never repeating the batch on screen
    const again = await shuffleGallery(current);
    expect(again).toHaveLength(GALLERY_SIZE);
    for (const photo of again!) expect(ids(current)).not.toContain(photo.id);
  });
});
