/*
 * The layout grid (DESIGN.md, "Layout grid"): four tiers by viewport width.
 *
 *   phone    ..599   the stacked shell, narrowed by the phone block of styles.css
 *   tablet   600..1199  the stacked shell: top bar, document scroll, docked composer
 *   desktop  1200..1919 the rail on the left, one inner scrollport
 *   wide     1920..     the desktop, and the rail may widen to icons plus labels
 *
 * Every width query in styles.css and in the code uses these edges;
 * layoutGridCss.test.ts fails on any other one that DESIGN.md does not list.
 */

/**
 * Viewports at most this width are phones: the compact window class, 360 to
 * 430 CSS px in portrait. The phone block at the end of styles.css applies here.
 */
export const PHONE_MAX_WIDTH_PX = 599;

/** Pass to matchMedia or @media for the phone tier. */
export const phoneMaxWidthMediaQuery = `(max-width: ${PHONE_MAX_WIDTH_PX}px)`;

/** Viewports at most this width use stacked top nav and document scroll (see styles.css). */
export const SHELL_STACK_MAX_WIDTH_PX = 1199;

/** Pass to matchMedia for chat scroll shell behavior (align with CSS media queries). */
export const shellStackMaxWidthMediaQuery = `(max-width: ${SHELL_STACK_MAX_WIDTH_PX}px)`;

/** From this width the rail may widen to icons plus labels (the wide tier). */
export const WIDE_RAIL_MIN_WIDTH_PX = 1920;

/** Pass to matchMedia for the wide tier. */
export const wideRailMinWidthMediaQuery = `(min-width: ${WIDE_RAIL_MIN_WIDTH_PX}px)`;

/** useSyncExternalStore subscribe for the mobile/narrow shell breakpoint. */
export function subscribeShellStack(cb: () => void): () => void {
  if (typeof window === "undefined") return () => {};
  const mq = window.matchMedia(shellStackMaxWidthMediaQuery);
  mq.addEventListener("change", cb);
  return () => mq.removeEventListener("change", cb);
}

/** useSyncExternalStore snapshot (client) for the mobile/narrow shell breakpoint. */
export function snapshotShellStack(): boolean {
  return typeof window !== "undefined" && window.matchMedia(shellStackMaxWidthMediaQuery).matches;
}

/** useSyncExternalStore snapshot (server) for the mobile/narrow shell breakpoint. */
export function serverSnapshotShellStack(): boolean {
  return false;
}

/**
 * No input can hover and at least one pointer is coarse: a phone or a tablet
 * without a trackpad. Decides what Enter does in the composer; the layout
 * follows the width breakpoint above instead.
 */
export const touchOnlyMediaQuery = "(any-hover: none) and (any-pointer: coarse)";

/** useSyncExternalStore subscribe for the touch-only query. */
export function subscribeTouchOnly(cb: () => void): () => void {
  if (typeof window === "undefined") return () => {};
  const mq = window.matchMedia(touchOnlyMediaQuery);
  mq.addEventListener("change", cb);
  return () => mq.removeEventListener("change", cb);
}

/** useSyncExternalStore snapshot (client) for the touch-only query. */
export function snapshotTouchOnly(): boolean {
  return typeof window !== "undefined" && window.matchMedia(touchOnlyMediaQuery).matches;
}

/** useSyncExternalStore snapshot (server) for the touch-only query. */
export function serverSnapshotTouchOnly(): boolean {
  return false;
}
