/**
 * The page backdrop choices of Settings → Appearance, kept in this browser
 * only, like the theme: smooth gradients (cookie `coddy_ui_smooth_backdrop`),
 * the glows and the glass of the title bar, the rail and the composer painted
 * with random dither on a canvas (canvasBackdrop.ts), for screens that show
 * the browser's own gradients as rings.
 *
 * followBackdrop applies the choice to <html> and follows every change.
 */
import { followCanvasBackdrop } from "./canvasBackdrop";

export const SMOOTH_BACKDROP_COOKIE = "coddy_ui_smooth_backdrop";

const MAX_AGE_SECONDS = 60 * 60 * 24 * 365;

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
}

let state: BackdropState = {
  smooth: readCookie(SMOOTH_BACKDROP_COOKIE) === "on",
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

/**
 * followBackdrop applies the choice to <html> now and on every change: with
 * smooth gradients on, `data-smooth-backdrop` and the canvas running. Returns
 * the teardown.
 */
export function followBackdrop(doc: Document = document): () => void {
  const root = doc.documentElement;
  let stopCanvas: (() => void) | null = null;

  const apply = () => {
    if (state.smooth && !stopCanvas) {
      // Mark <html> only when the canvas runs: the panels' rules expect its images.
      stopCanvas = followCanvasBackdrop(doc);
      if (stopCanvas) {
        root.dataset.smoothBackdrop = "true";
      }
    } else if (!state.smooth && stopCanvas) {
      stopCanvas();
      stopCanvas = null;
      delete root.dataset.smoothBackdrop;
    }
  };

  apply();
  const unsubscribe = subscribeBackdrop(apply);
  return () => {
    unsubscribe();
    stopCanvas?.();
    stopCanvas = null;
    delete root.dataset.smoothBackdrop;
  };
}
