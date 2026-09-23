/** Viewports at most this width use stacked top nav and document scroll (see styles.css). */
export const SHELL_STACK_MAX_WIDTH_PX = 1199;

/** Pass to matchMedia for chat scroll shell behavior (align with CSS media queries). */
export const shellStackMaxWidthMediaQuery = `(max-width: ${SHELL_STACK_MAX_WIDTH_PX}px)`;

/** useSyncExternalStore subscribe for the mobile/narrow shell breakpoint. */
export function subscribeShellStack(cb: () => void): () => void {
  if (typeof window === "undefined") return () => {};
  // matchMedia can be missing or emptied in test environments (a bare
  // vi.fn() after restoreAllMocks answers undefined) - never crash on it.
  const mq = window.matchMedia?.(shellStackMaxWidthMediaQuery);
  mq?.addEventListener?.("change", cb);
  return () => mq?.removeEventListener?.("change", cb);
}

/** useSyncExternalStore snapshot (client) for the mobile/narrow shell breakpoint. */
export function snapshotShellStack(): boolean {
  return (
    typeof window !== "undefined" &&
    window.matchMedia?.(shellStackMaxWidthMediaQuery)?.matches === true
  );
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
  const mq = window.matchMedia?.(touchOnlyMediaQuery);
  mq?.addEventListener?.("change", cb);
  return () => mq?.removeEventListener?.("change", cb);
}

/** useSyncExternalStore snapshot (client) for the touch-only query. */
export function snapshotTouchOnly(): boolean {
  return (
    typeof window !== "undefined" &&
    window.matchMedia?.(touchOnlyMediaQuery)?.matches === true
  );
}

/** useSyncExternalStore snapshot (server) for the touch-only query. */
export function serverSnapshotTouchOnly(): boolean {
  return false;
}
