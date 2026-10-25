/**
 * A few dark Unsplash photographs offered as wallpapers in Settings →
 * Appearance, served by Lorem Picsum (picsum.photos: Unsplash photos by id,
 * any size, CORS open, no key). Nothing is fetched until the gallery is
 * opened; a picked photo is downloaded once at the screen's size and kept in
 * this browser like an uploaded one (backdropPrefs.ts), with its author.
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
