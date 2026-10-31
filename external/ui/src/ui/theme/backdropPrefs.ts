/**
 * Browser-local wallpaper choices for Settings → Appearance. A picture from
 * this computer or the server gallery is kept in IndexedDB, never uploaded,
 * and dimmed toward the theme's canvas colour by `coddy_ui_wallpaper_dim`.
 * followBackdrop applies the picture and dimming to <html> through CSS.
 */
export const WALLPAPER_DIM_COOKIE = "coddy_ui_wallpaper_dim";
export const WALLPAPER_DIM_DEFAULT = 40;

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
  /** Object URL of the wallpaper, or null when there is none. */
  wallpaper: string | null;
  /** 0..90, percent of the theme's canvas colour laid over the wallpaper. */
  dim: number;
  /** Last storage failure, including a failed startup read. */
  storageError?: Error;
}

function readDim(): number {
  const v = Number(readCookie(WALLPAPER_DIM_COOKIE));
  if (readCookie(WALLPAPER_DIM_COOKIE) === null || !Number.isFinite(v)) {
    return WALLPAPER_DIM_DEFAULT;
  }
  return Math.min(90, Math.max(0, Math.round(v)));
}

let state: BackdropState = {
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

export function setWallpaperDim(dim: number): void {
  const v = Number.isFinite(dim)
    ? Math.min(90, Math.max(0, Math.round(dim)))
    : WALLPAPER_DIM_DEFAULT;
  writeCookie(WALLPAPER_DIM_COOKIE, String(v));
  publish({ ...state, dim: v });
}

function openDb(): Promise<IDBDatabase | null> {
  if (typeof indexedDB === "undefined") {
    return Promise.resolve(null);
  }
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 1);
    let failed = false;
    req.onupgradeneeded = () => {
      if (failed) req.transaction?.abort();
      else req.result.createObjectStore(DB_STORE);
    };
    req.onsuccess = () => {
      // A blocked open cannot be cancelled; close it if it eventually succeeds.
      if (failed) req.result.close();
      else resolve(req.result);
    };
    req.onblocked = () => {
      failed = true;
      reject(new Error("Wallpaper storage is blocked by another page"));
    };
    req.onerror = () => {
      failed = true;
      reject(req.error ?? new Error("Could not open wallpaper storage"));
    };
  });
}

async function dbRequest<T>(
  mode: IDBTransactionMode,
  run: (store: IDBObjectStore) => IDBRequest<T>,
  signal?: AbortSignal,
): Promise<T | null> {
  const db = await openDb();
  if (!db) return null;
  let cleanup: (() => void) | undefined;
  try {
    signal?.throwIfAborted();
    return await new Promise<T>((resolve, reject) => {
      const tx = db.transaction(DB_STORE, mode);
      let failure: DOMException | Error | null = null;
      const abort = () => {
        try {
          tx.abort();
        } catch (error) {
          reject(error);
        }
      };
      tx.onabort = () => {
        reject(
          failure ?? tx.error ?? new Error("Wallpaper transaction aborted"),
        );
      };
      tx.onerror = () => {
        failure = tx.error ?? new Error("Wallpaper transaction failed");
      };
      const req = run(tx.objectStore(DB_STORE));
      req.onerror = () => {
        failure = req.error ?? new Error("Wallpaper storage request failed");
      };
      // Request success is not durable: the transaction can still abort.
      tx.oncomplete = () => {
        if (failure) reject(failure);
        else resolve(req.result);
      };
      signal?.addEventListener("abort", abort, { once: true });
      cleanup = () => signal?.removeEventListener("abort", abort);
      if (signal?.aborted) abort();
    });
  } finally {
    cleanup?.();
    db.close();
  }
}

function showWallpaper(blob: Blob | null): void {
  const wallpaper = blob ? URL.createObjectURL(blob) : null;
  if (state.wallpaper) URL.revokeObjectURL(state.wallpaper);
  publish({ wallpaper, dim: state.dim });
}

let generation = 0;
let activeMutation: AbortController | null = null;
let writes: Promise<WallpaperResult> = Promise.resolve({ refusal: null });

function beginMutation(signal?: AbortSignal) {
  const ownGeneration = ++generation;
  activeMutation?.abort();
  const controller = new AbortController();
  activeMutation = controller;
  const abort = () => controller.abort();
  signal?.addEventListener("abort", abort, { once: true });
  if (signal?.aborted) abort();
  return {
    signal: controller.signal,
    current: () => generation === ownGeneration && !controller.signal.aborted,
    finish: () => {
      signal?.removeEventListener("abort", abort);
      if (activeMutation === controller) activeMutation = null;
    },
  };
}

function storageFailure(error: unknown): WallpaperResult {
  publish({
    ...state,
    storageError: error instanceof Error ? error : new Error(String(error)),
  });
  return { refusal: "storage" };
}

function persistWallpaper(
  blob: Blob | null,
  mutation: ReturnType<typeof beginMutation>,
) {
  writes = writes.then(async () => {
    if (!mutation.current()) return { refusal: null };
    try {
      if (blob) {
        await dbRequest(
          "readwrite",
          (store) => store.put(blob, DB_KEY),
          mutation.signal,
        );
      } else {
        await dbRequest(
          "readwrite",
          (store) => store.delete(DB_KEY),
          mutation.signal,
        );
      }
      if (mutation.current()) showWallpaper(blob);
      return { refusal: null };
    } catch (error) {
      // Cancellation is intentional; a real failure remains visible and retryable.
      return mutation.current() ? storageFailure(error) : { refusal: null };
    }
  });
  return writes;
}

export type WallpaperRefusal = "not-image" | "unreadable" | "storage";

export interface WallpaperResult {
  refusal: WallpaperRefusal | null;
  /** The picture's size, when it was read. */
  width?: number;
  height?: number;
}

/** Decode using the browser, including engines without createImageBitmap. */
async function pictureSize(
  file: Blob,
  signal: AbortSignal,
): Promise<{ width: number; height: number } | null> {
  if (typeof createImageBitmap === "function") {
    try {
      const bitmap = await createImageBitmap(file);
      try {
        return { width: bitmap.width, height: bitmap.height };
      } finally {
        bitmap.close();
      }
    } catch {
      return null;
    }
  }
  if (typeof Image !== "function" || signal.aborted) return null;
  const url = URL.createObjectURL(file);
  try {
    return await new Promise((resolve) => {
      const image = new Image();
      const finish = (size: { width: number; height: number } | null) => {
        image.onload = image.onerror = null;
        signal.removeEventListener("abort", abort);
        image.src = "";
        resolve(size);
      };
      const abort = () => finish(null);
      image.onload = () =>
        finish({ width: image.naturalWidth, height: image.naturalHeight });
      image.onerror = () => finish(null);
      signal.addEventListener("abort", abort, { once: true });
      image.src = url;
      if (signal.aborted) abort();
    });
  } finally {
    URL.revokeObjectURL(url);
  }
}

async function validatePicture(
  file: Blob,
  signal: AbortSignal,
): Promise<WallpaperResult> {
  if (!file.type.startsWith("image/")) return { refusal: "not-image" };
  const size = await pictureSize(file, signal);
  if (!size) return { refusal: "unreadable" };
  return { refusal: null, ...size };
}

/** Validate before persisting; absent IndexedDB keeps a page-lifetime picture only. */
export async function setWallpaper(
  file: Blob,
  signal?: AbortSignal,
): Promise<WallpaperResult> {
  const mutation = beginMutation(signal);
  try {
    if (!mutation.current()) return { refusal: null };
    const validated = await validatePicture(file, mutation.signal);
    if (!mutation.current()) return { refusal: null };
    if (validated.refusal) return validated;
    const persisted = await persistWallpaper(file, mutation);
    return persisted.refusal ? persisted : validated;
  } finally {
    mutation.finish();
  }
}

export async function clearWallpaper(): Promise<WallpaperResult> {
  const mutation = beginMutation();
  try {
    return await persistWallpaper(null, mutation);
  } finally {
    mutation.finish();
  }
}

/** A startup read never replaces a choice made since it began. */
export async function loadWallpaper(): Promise<void> {
  // Only bootstrap may restore storage; a user choice has priority from its start.
  if (generation !== 0) return;
  const ownGeneration = generation;
  try {
    const blob = await dbRequest<unknown>("readonly", (store) =>
      store.get(DB_KEY),
    );
    if (generation !== ownGeneration || blob == null) return;
    if (!(blob instanceof Blob)) throw new Error("Invalid saved wallpaper");
    const result = await validatePicture(blob, new AbortController().signal);
    if (generation !== ownGeneration) return;
    if (result.refusal)
      throw new Error(`Invalid saved wallpaper: ${result.refusal}`);
    showWallpaper(blob);
  } catch (error) {
    if (generation === ownGeneration) storageFailure(error);
  }
}

/** Applies the wallpaper and dimming to <html> now and on every change. */
export function followBackdrop(doc: Document = document): () => void {
  const root = doc.documentElement;
  const apply = () => {
    const { wallpaper, dim } = state;
    if (wallpaper) {
      root.dataset.wallpaper = "true";
      root.style.setProperty("--coddy-wallpaper", `url("${wallpaper}")`);
      root.style.setProperty("--coddy-wallpaper-dim", `${dim}%`);
    } else {
      delete root.dataset.wallpaper;
      root.style.removeProperty("--coddy-wallpaper");
      root.style.removeProperty("--coddy-wallpaper-dim");
    }
  };

  apply();
  const unsubscribe = subscribeBackdrop(apply);
  void loadWallpaper();
  return () => {
    unsubscribe();
    delete root.dataset.wallpaper;
    root.style.removeProperty("--coddy-wallpaper");
    root.style.removeProperty("--coddy-wallpaper-dim");
  };
}
