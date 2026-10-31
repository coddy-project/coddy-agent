import { getEnv } from "../env/remoteEnv";
import { resourceRequest } from "../files/api";
import { RASTER_IMAGE_TYPE } from "../files/objectUrl";
import type { WallpaperResult } from "./backdropPrefs";

export interface GalleryPhoto {
  name: string;
}

export type GalleryResult =
  | WallpaperResult
  | { refusal: "offline" | "upload" | "delete" };

export class GalleryReadError extends Error {
  constructor(readonly refusal: "not-image") {
    super(refusal);
  }
}

function parsePhoto(value: unknown): GalleryPhoto {
  if (
    !value ||
    typeof value !== "object" ||
    !("name" in value) ||
    typeof value.name !== "string" ||
    !value.name ||
    value.name === "." ||
    value.name === ".." ||
    /[\\/\x00]/.test(value.name)
  )
    throw new Error("Invalid wallpaper name");
  return { name: value.name };
}

/** Source identity excludes bearer tokens, URL credentials, queries and fragments. */
export function galleryEnvironmentScope(): string {
  const env = getEnv();
  const url = new URL(
    env.mode === "remote" ? env.baseUrl : window.location.origin,
  );
  return url.origin + url.pathname.replace(/\/+$/, "");
}

export function galleryPhotoUrl(photo: GalleryPhoto): string {
  return `/coddy/wallpapers/${encodeURIComponent(parsePhoto(photo).name)}`;
}

/** Listing and image reads keep authentication, type checks and cancellation. */
async function readBytes(
  path: string,
  signal: AbortSignal,
  accept: RegExp,
): Promise<{ chunks: Uint8Array<ArrayBuffer>[]; type: string }> {
  signal.throwIfAborted();
  const res = await resourceRequest(path, { signal });
  signal.throwIfAborted();
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const type = res.headers.get("Content-Type") || "";
  if (!accept.test(type)) {
    await res.body?.cancel();
    throw new GalleryReadError("not-image");
  }
  const chunks: Uint8Array<ArrayBuffer>[] = [];
  if (res.body) {
    const reader = res.body.getReader();
    try {
      for (;;) {
        signal.throwIfAborted();
        const next = await reader.read();
        signal.throwIfAborted();
        if (next.done) break;
        chunks.push(new Uint8Array(next.value));
      }
    } finally {
      reader.releaseLock();
    }
  } else {
    const bytes = await res.arrayBuffer();
    signal.throwIfAborted();
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
  return body.wallpapers
    .map((value: unknown) => {
      const photo = parsePhoto(value);
      if (names.has(photo.name)) throw new Error("Duplicate wallpaper name");
      names.add(photo.name);
      return photo;
    })
    .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
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

export async function uploadGalleryPhoto(
  file: File,
  signal: AbortSignal,
): Promise<{ wallpaper: GalleryPhoto; created: boolean }> {
  signal.throwIfAborted();
  const res = await resourceRequest(galleryPhotoUrl({ name: file.name }), {
    method: "POST",
    body: file,
    headers: { "Content-Type": file.type || "application/octet-stream" },
    signal,
  });
  signal.throwIfAborted();
  if (res.status === 415) throw new GalleryReadError("not-image");
  if (res.status !== 200 && res.status !== 201)
    throw new Error(`HTTP ${res.status}`);
  const body: unknown = await res.json();
  signal.throwIfAborted();
  if (
    !body ||
    typeof body !== "object" ||
    !("wallpaper" in body) ||
    !("created" in body) ||
    typeof body.created !== "boolean"
  )
    throw new Error("Invalid wallpaper upload response");
  return { wallpaper: parsePhoto(body.wallpaper), created: body.created };
}

export async function deleteGalleryPhoto(
  photo: GalleryPhoto,
  signal: AbortSignal,
): Promise<void> {
  signal.throwIfAborted();
  const res = await resourceRequest(galleryPhotoUrl(photo), {
    method: "DELETE",
    signal,
  });
  signal.throwIfAborted();
  if (res.status !== 204) throw new Error(`HTTP ${res.status}`);
}
