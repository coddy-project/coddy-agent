/**
 * The simplified mode: for a machine the glass and the motion cost too much on,
 * solid colours and nothing that moves, in every theme. It is a cookie and an
 * attribute on <html> that styles/_simple.scss reads, client-side only like the
 * theme, and the inline script of index.html applies it before the first paint.
 */
export const CODDY_UI_SIMPLE_COOKIE = "coddy_ui_simple";

const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

export function readUiSimpleCookie(): boolean {
  if (typeof document === "undefined") {
    return false;
  }
  return document.cookie
    .split(";")
    .some((part) => part.trim() === `${CODDY_UI_SIMPLE_COOKIE}=1`);
}

function writeUiSimpleCookie(on: boolean): void {
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  const value = on ? `1; Max-Age=${MAX_AGE_SECONDS}` : "; Max-Age=0";
  document.cookie = `${CODDY_UI_SIMPLE_COOKIE}=${value}; Path=/; SameSite=Lax${secure}`;
}

function applyUiSimple(on: boolean): void {
  if (on) {
    document.documentElement.dataset.simple = "on";
  } else {
    delete document.documentElement.dataset.simple;
  }
}

export function readAppliedUiSimple(): boolean {
  return (
    typeof document !== "undefined" &&
    document.documentElement.dataset.simple === "on"
  );
}

export function bootstrapUiSimpleFromCookie(): void {
  if (typeof document !== "undefined") {
    applyUiSimple(readUiSimpleCookie());
  }
}

export function setUiSimple(on: boolean): void {
  writeUiSimpleCookie(on);
  applyUiSimple(on);
}

/** Follows the mode on <html>, for useSyncExternalStore. */
export function subscribeUiSimple(onChange: () => void): () => void {
  if (typeof MutationObserver === "undefined") {
    return () => {};
  }
  const watch = new MutationObserver(onChange);
  watch.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-simple"],
  });
  return () => watch.disconnect();
}

/** Whether what a script animates should go at once instead. */
export function motionIsReduced(): boolean {
  return (
    readAppliedUiSimple() ||
    (typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches)
  );
}
