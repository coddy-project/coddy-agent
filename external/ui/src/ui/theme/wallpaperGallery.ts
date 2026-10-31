import { resourceRequest } from "../files/api";
import { RASTER_IMAGE_TYPE } from "../files/objectUrl";
import { WALLPAPER_MAX_BYTES, type WallpaperResult } from "./backdropPrefs";

export interface GalleryPhoto {
  name: string;
}

export type GalleryResult = WallpaperResult | { refusal: "offline" };

export class GalleryReadError extends Error {
  constructor(readonly refusal: "too-large" | "not-image") {
    super(refusal);
  }
}

export function galleryPhotoUrl(photo: GalleryPhoto): string {
  return `/coddy/wallpapers/${encodeURIComponent(photo.name)}`;
}

/** Both the listing and image reads are bounded, including chunked responses. */
async function readBytes(
  path: string,
  signal: AbortSignal,
  accept: RegExp,
): Promise<{ chunks: Uint8Array<ArrayBuffer>[]; type: string }> {
  signal.throwIfAborted();
  const res = await resourceRequest(path, { signal });
  signal.throwIfAborted();
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  if (Number(res.headers.get("Content-Length")) > WALLPAPER_MAX_BYTES) {
    await res.body?.cancel();
    throw new GalleryReadError("too-large");
  }
  const type = res.headers.get("Content-Type") || "";
  if (!accept.test(type)) {
    await res.body?.cancel();
    throw new GalleryReadError("not-image");
  }
  const chunks: Uint8Array<ArrayBuffer>[] = [];
  if (res.body) {
    const reader = res.body.getReader();
    let size = 0;
    try {
      for (;;) {
        signal.throwIfAborted();
        const next = await reader.read();
        signal.throwIfAborted();
        if (next.done) break;
        size += next.value.byteLength;
        if (size > WALLPAPER_MAX_BYTES) {
          await reader.cancel();
          throw new GalleryReadError("too-large");
        }
        chunks.push(new Uint8Array(next.value));
      }
    } finally {
      reader.releaseLock();
    }
  } else {
    const bytes = await res.arrayBuffer();
    signal.throwIfAborted();
    if (bytes.byteLength > WALLPAPER_MAX_BYTES)
      throw new GalleryReadError("too-large");
    chunks.push(new Uint8Array(bytes));
  }
  return { chunks, type };
}

export async function readGallery(
  signal: AbortSignal,
): Promise<GalleryPhoto[]> {
  const { chunks } = await readBytes(
    "/coddy/wallpapers",
    signal,
    /^application\/json(?:;|$)/i,
  );
  const decoder = new TextDecoder();
  const text =
    chunks.map((chunk) => decoder.decode(chunk, { stream: true })).join("") +
    decoder.decode();
  const body: unknown = JSON.parse(text);
  if (
    !body ||
    typeof body !== "object" ||
    !("wallpapers" in body) ||
    !Array.isArray(body.wallpapers)
  ) {
    throw new Error("Invalid wallpaper listing");
  }
  const names = new Set<string>();
  return body.wallpapers.map((photo: unknown) => {
    if (
      !photo ||
      typeof photo !== "object" ||
      !("name" in photo) ||
      typeof photo.name !== "string" ||
      !photo.name ||
      /[\\/\x00]/.test(photo.name) ||
      names.has(photo.name)
    ) {
      throw new Error("Invalid wallpaper name");
    }
    names.add(photo.name);
    return { name: photo.name };
  });
}

/** Returns bytes, never applies them: the caller owns selection cancellation. */
export async function readGalleryPhoto(
  photo: GalleryPhoto,
  signal: AbortSignal,
): Promise<Blob> {
  const { chunks, type } = await readBytes(
    galleryPhotoUrl(photo),
    signal,
    RASTER_IMAGE_TYPE,
  );
  return new Blob(chunks, { type });
}
