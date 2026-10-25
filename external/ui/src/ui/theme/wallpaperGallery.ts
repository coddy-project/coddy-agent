/**
 * A few dark Unsplash photographs offered as wallpapers in Settings →
 * Appearance, served by Lorem Picsum (picsum.photos: Unsplash photos by id,
 * any size, CORS open, no key). Nothing is fetched until the gallery is
 * opened; a picked photo is downloaded once at the screen's size and kept in
 * this browser like an uploaded one (backdropPrefs.ts), with its author.
 * Shuffle swaps the whole batch for twelve others of a larger hand-picked
 * pool (shuffleGallery, wallpaperPool.ts).
 */
import {
  setWallpaper,
  type WallpaperCredit,
  type WallpaperResult,
} from "./backdropPrefs";

export interface GalleryPhoto extends WallpaperCredit {
  /** Lorem Picsum id. */
  id: string;
}

export const GALLERY: readonly GalleryPhoto[] = [
  {
    id: "120",
    author: "Guillaume",
    url: "https://unsplash.com/photos/_DA3D5P71qs",
  },
  {
    id: "149",
    author: "Guillaume",
    url: "https://unsplash.com/photos/revxuIor0nY",
  },
  {
    id: "184",
    author: "Tim de Groot",
    url: "https://unsplash.com/photos/yNGQ830uFB4",
  },
  {
    id: "249",
    author: "Anders Jildén",
    url: "https://unsplash.com/photos/nrLtvA05jk8",
  },
  {
    id: "122",
    author: "Vadim Sherbakov",
    url: "https://unsplash.com/photos/xS_RzdD5CFE",
  },
  {
    id: "83",
    author: "Julie Geiger",
    url: "https://unsplash.com/photos/dYshDcTI1Js",
  },
  {
    id: "339",
    author: "Tirza van Dijk",
    url: "https://unsplash.com/photos/aGx7X1VmFIY",
  },
  {
    id: "254",
    author: "Pavel Voinov",
    url: "https://unsplash.com/photos/adG2dX-ruJ4",
  },
  {
    id: "377",
    author: "Martin Staněk",
    url: "https://unsplash.com/photos/8WClaa1CmZ0",
  },
  {
    id: "352",
    author: "Caleb George",
    url: "https://unsplash.com/photos/URmkfvtK3Qw",
  },
  {
    id: "329",
    author: "Felipe Santana",
    url: "https://unsplash.com/photos/-e_njRV9hRE",
  },
  {
    id: "353",
    author: "Forrest Cavale",
    url: "https://unsplash.com/photos/qfmd9bu7IgA",
  },
];

const PICSUM = "https://picsum.photos/id";

export function galleryThumbUrl(photo: GalleryPhoto): string {
  return `${PICSUM}/${photo.id}/320/180`;
}

/**
 * galleryPhotoUrl asks for the photo at the screen's size in device pixels,
 * 16:9, between 1920 and 3840 wide, so it is never under the minimum size.
 */
export function galleryPhotoUrl(
  photo: GalleryPhoto,
  screenWidth: number,
  scale: number,
): string {
  const width = Math.min(3840, Math.max(1920, Math.round(screenWidth * scale)));
  const height = Math.round((width * 9) / 16);
  return `${PICSUM}/${photo.id}/${width}/${height}`;
}

export type GalleryResult = WallpaperResult | { refusal: "offline" };

/** pickGalleryPhoto downloads the photo and makes it the wallpaper. */
export async function pickGalleryPhoto(
  photo: GalleryPhoto,
): Promise<GalleryResult> {
  const width = typeof screen === "undefined" ? 1920 : screen.width;
  const scale = typeof devicePixelRatio === "number" ? devicePixelRatio : 1;
  let blob: Blob;
  try {
    const res = await fetch(galleryPhotoUrl(photo, width, scale));
    if (!res.ok) {
      return { refusal: "offline" };
    }
    blob = await res.blob();
  } catch {
    return { refusal: "offline" };
  }
  return setWallpaper(blob, { author: photo.author, url: photo.url });
}

/** The size of every batch the gallery shows. */
export const GALLERY_SIZE = 12;

// Pool photos shown since the page loaded, so a shuffle brings photos not
// seen yet until the pool runs out.
const seen = new Set<string>();

export interface ShuffleOptions {
  random?: () => number;
}

/**
 * shuffleGallery draws a new batch of GALLERY_SIZE photos from the pool
 * (wallpaperPool.ts, loaded on the first shuffle), none of `current` and none
 * shown before on this page while the pool has enough of them left; then it
 * starts over. Nothing goes to the network but the pool's chunk, from the
 * server the page came from. Returns null when that chunk does not load.
 */
export async function shuffleGallery(
  current: readonly GalleryPhoto[],
  options: ShuffleOptions = {},
): Promise<GalleryPhoto[] | null> {
  const random = options.random ?? Math.random;
  let pool: readonly (readonly [string, string, string])[];
  try {
    pool = (await import("./wallpaperPool")).WALLPAPER_POOL;
  } catch {
    return null;
  }
  for (const photo of current) seen.add(photo.id);
  let left = pool.filter(([id]) => !seen.has(id));
  if (left.length < GALLERY_SIZE) {
    seen.clear();
    for (const photo of current) seen.add(photo.id);
    left = pool.filter(([id]) => !seen.has(id));
  }
  // Fisher-Yates over what is left, then the first GALLERY_SIZE
  const order = [...left];
  for (let i = order.length - 1; i > 0; i--) {
    const j = Math.floor(random() * (i + 1));
    [order[i], order[j]] = [order[j]!, order[i]!];
  }
  const batch = order.slice(0, GALLERY_SIZE).map(([id, author, unsplash]) => ({
    id,
    author,
    url: `https://unsplash.com/photos/${unsplash}`,
  }));
  for (const photo of batch) seen.add(photo.id);
  return batch.length > 0 ? batch : null;
}
