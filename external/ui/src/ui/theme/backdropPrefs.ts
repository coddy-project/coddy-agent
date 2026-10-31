/**
 * The page backdrop choices of Settings → Appearance, kept in this browser
 * only, like the theme:
 *
 * - smooth gradients (cookie `coddy_ui_smooth_backdrop`): the glows, the glass
 *   of the title bar, the rail and the composer painted with random dither on
 *   a canvas (canvasBackdrop.ts), for screens that show the browser's own
 *   gradients as rings;
 * - a wallpaper: a picture the person chose, or one from the server gallery
 *   (wallpaperGallery.ts), kept in IndexedDB (never sent to
 *   the server), shown under the app instead of the glows and dimmed toward
 *   the theme's canvas colour by `coddy_ui_wallpaper_dim` percent.
 *
 * followBackdrop applies them to <html> and follows every change. Smooth
 * gradients cover the wallpaper too: the picture itself has its own grain,
 * but the glass blurs it into smooth ramps that band like the glows.
 */
export const SMOOTH_BACKDROP_COOKIE = "coddy_ui_smooth_backdrop";
export const WALLPAPER_DIM_COOKIE = "coddy_ui_wallpaper_dim";
export const WALLPAPER_DIM_DEFAULT = 40;
/** Larger files are refused: IndexedDB quotas differ and the page holds it in memory. */
export const WALLPAPER_MAX_BYTES = 20 * 1024 * 1024;
/** Smaller pictures are refused: stretched over a screen they turn to mush. */
export const WALLPAPER_MIN_WIDTH = 1280;
export const WALLPAPER_MIN_HEIGHT = 720;

const MAX_AGE_SECONDS = 60 * 60 * 24 * 365;
const DB_NAME = "coddy-ui";
const DB_STORE = "backdrop";
const DB_KEY = "wallpaper";

function readCookie(name: string): string | null {
  if (typeof document === "undefined") {
    return null;
  }
  for (const part of document.cookie.split(";")) {
    const s = part.trim();
    if (s.startsWith(`${name}=`)) {
      return decodeURIComponent(s.slice(name.length + 1).trim());
    }
  }
  return null;
}

function writeCookie(name: string, value: string | null): void {
  if (typeof document === "undefined") {
    return;
  }
  if (value === null) {
    document.cookie = `${name}=; Path=/; Max-Age=0; SameSite=Lax`;
    return;
  }
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${name}=${encodeURIComponent(value)}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}

export interface BackdropState {
  smooth: boolean;
  /** Object URL of the wallpaper, or null when there is none. */
  wallpaper: string | null;
  /** 0..90, percent of the theme's canvas colour laid over the wallpaper. */
  dim: number;
}

function readDim(): number {
  const v = Number(readCookie(WALLPAPER_DIM_COOKIE));
  if (readCookie(WALLPAPER_DIM_COOKIE) === null || !Number.isFinite(v)) {
    return WALLPAPER_DIM_DEFAULT;
  }
  return Math.min(90, Math.max(0, Math.round(v)));
}

let state: BackdropState = {
  smooth: readCookie(SMOOTH_BACKDROP_COOKIE) === "on",
  wallpaper: null,
  dim: readDim(),
};
const listeners = new Set<() => void>();

function publish(next: BackdropState): void {
  state = next;
  for (const listener of [...listeners]) listener();
}

/** For useSyncExternalStore. */
export function subscribeBackdrop(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => listeners.delete(onChange);
}

export function backdropSnapshot(): BackdropState {
  return state;
}

export function setSmoothBackdrop(on: boolean): void {
  writeCookie(SMOOTH_BACKDROP_COOKIE, on ? "on" : null);
  publish({ ...state, smooth: on });
}

export function setWallpaperDim(dim: number): void {
  const v = Math.min(90, Math.max(0, Math.round(dim)));
  writeCookie(WALLPAPER_DIM_COOKIE, String(v));
  publish({ ...state, dim: v });
}

function openDb(): Promise<IDBDatabase | null> {
  if (typeof indexedDB === "undefined") {
    return Promise.resolve(null);
  }
  return new Promise((resolve) => {
    try {
      const req = indexedDB.open(DB_NAME, 1);
      req.onupgradeneeded = () => req.result.createObjectStore(DB_STORE);
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => resolve(null);
    } catch {
      resolve(null);
    }
  });
}

async function dbRequest<T>(
  mode: IDBTransactionMode,
  run: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T | null> {
  const db = await openDb();
  if (!db) {
    return null;
  }
  return new Promise((resolve) => {
    try {
      const req = run(db.transaction(DB_STORE, mode).objectStore(DB_STORE));
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => resolve(null);
    } catch {
      resolve(null);
    }
  });
}

// The kept picture itself, for the canvas to decode.
let wallpaperBlob: Blob | null = null;

function showWallpaper(blob: Blob | null): void {
  wallpaperBlob = blob;
  if (state.wallpaper) {
    URL.revokeObjectURL(state.wallpaper);
  }
  publish({
    ...state,
    wallpaper: blob ? URL.createObjectURL(blob) : null,
  });
}

export type WallpaperRefusal =
  | "not-image"
  | "too-large"
  | "too-small"
  | "unreadable";

export interface WallpaperResult {
  refusal: WallpaperRefusal | null;
  /** The picture's size, when it was read. */
  width?: number;
  height?: number;
}

/** pictureSize decodes the picture for its size; null where it cannot be decoded. */
async function pictureSize(
  file: Blob,
): Promise<{ width: number; height: number } | null | undefined> {
  if (typeof createImageBitmap !== "function") {
    return undefined; // nothing to measure with (tests): take it as it is
  }
  try {
    const bitmap = await createImageBitmap(file);
    const size = { width: bitmap.width, height: bitmap.height };
    bitmap.close();
    return size;
  } catch {
    return null;
  }
}

/**
 * setWallpaper keeps the picture in this browser and shows it, unless it is
 * not a picture, too large, or smaller than WALLPAPER_MIN_WIDTH x
 * WALLPAPER_MIN_HEIGHT. When IndexedDB is not available it is shown for the
 * life of the page only.
 */
export async function setWallpaper(
  file: Blob,
  signal?: AbortSignal,
): Promise<WallpaperResult> {
  if (!file.type.startsWith("image/")) {
    return { refusal: "not-image" };
  }
  if (file.size > WALLPAPER_MAX_BYTES) {
    return { refusal: "too-large" };
  }
  const size = await pictureSize(file);
  if (size === null) {
    return { refusal: "unreadable" };
  }
  if (
    size &&
    (size.width < WALLPAPER_MIN_WIDTH || size.height < WALLPAPER_MIN_HEIGHT)
  ) {
    return { refusal: "too-small", ...size };
  }
  if (signal?.aborted) return { refusal: null };
  await dbRequest("readwrite", (store) => store.put(file, DB_KEY));
  if (signal?.aborted) return { refusal: null };
  showWallpaper(file);
  return { refusal: null, ...size };
}

export async function clearWallpaper(): Promise<void> {
  await dbRequest("readwrite", (store) => store.delete(DB_KEY));
  showWallpaper(null);
}

/** loadWallpaper reads the kept picture once at start. */
export async function loadWallpaper(): Promise<void> {
  const blob = await dbRequest<unknown>("readonly", (store) =>
    store.get(DB_KEY),
  );
  if (blob instanceof Blob) {
    showWallpaper(blob);
  }
}

/** How long the canvas waits after a change, so a dragged dimming slider repaints once. */
const CANVAS_SETTLE_MS = 150;

/**
 * followBackdrop applies the choices to <html> now and on every change:
 * `data-wallpaper` with `--coddy-wallpaper` and `--coddy-wallpaper-dim`, and,
 * with smooth gradients on, `data-smooth-backdrop` with the canvas running
 * over the glows or over the wallpaper. Returns the teardown.
 */
export function followBackdrop(doc: Document = document): () => void {
  const root = doc.documentElement;
  let stopCanvas: (() => void) | null = null;
  let canvasKey: string | null = null;
  let settle: ReturnType<typeof setTimeout> | undefined;

  const stop = () => {
    stopCanvas?.();
    stopCanvas = null;
  };

  const startCanvas = async (key: string) => {
    let painter: typeof import("./canvasBackdrop");
    try {
      painter = await import("./canvasBackdrop");
    } catch {
      // A missing chunk leaves the CSS backdrop and ordinary glass in place.
      if (key === canvasKey) {
        stop();
        delete root.dataset.smoothBackdrop;
      }
      return;
    }
    if (key !== canvasKey) return;
    const blob = wallpaperBlob;
    const { wallpaper, dim } = state;
    let picture: ImageBitmap | null = null;
    if (wallpaper && blob && typeof createImageBitmap === "function") {
      try {
        picture = await createImageBitmap(blob);
      } catch {
        picture = null;
      }
    }
    if (key !== canvasKey) {
      picture?.close();
      return; // overtaken by a newer choice
    }
    stop();
    if (wallpaper && !picture) {
      delete root.dataset.smoothBackdrop;
      return; // the picture cannot be decoded: leave the CSS wallpaper
    }
    stopCanvas = painter.followCanvasBackdrop(
      doc,
      picture ? { picture, dim: dim / 100 } : undefined,
    );
    // Mark <html> only when the canvas runs: the panels' rules expect its images.
    if (stopCanvas) {
      root.dataset.smoothBackdrop = "true";
    } else {
      delete root.dataset.smoothBackdrop;
    }
  };

  const apply = () => {
    const { smooth, wallpaper, dim } = state;
    if (wallpaper) {
      root.dataset.wallpaper = "true";
      root.style.setProperty("--coddy-wallpaper", `url("${wallpaper}")`);
      root.style.setProperty("--coddy-wallpaper-dim", `${dim}%`);
    } else {
      delete root.dataset.wallpaper;
      root.style.removeProperty("--coddy-wallpaper");
      root.style.removeProperty("--coddy-wallpaper-dim");
    }
    const key =
      smooth && root.dataset.simple !== "on"
        ? wallpaper
          ? `${wallpaper}|${dim}`
          : "glows"
        : null;
    if (key === canvasKey) {
      return;
    }
    const first = canvasKey === null;
    canvasKey = key;
    clearTimeout(settle);
    if (key === null) {
      stop();
      delete root.dataset.smoothBackdrop;
      return;
    }
    if (first) {
      void startCanvas(key);
    } else {
      settle = setTimeout(() => void startCanvas(key), CANVAS_SETTLE_MS);
    }
  };

  apply();
  const unsubscribe = subscribeBackdrop(apply);
  // Simplified mode keeps the choices but stops the canvas and its observers.
  const mode = new MutationObserver(apply);
  mode.observe(root, { attributes: true, attributeFilter: ["data-simple"] });
  void loadWallpaper();
  return () => {
    unsubscribe();
    mode.disconnect();
    clearTimeout(settle);
    canvasKey = null;
    stop();
    delete root.dataset.smoothBackdrop;
    delete root.dataset.wallpaper;
  };
}
