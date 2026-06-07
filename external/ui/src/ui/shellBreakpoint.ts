/** Viewports at most this width use stacked top nav and document scroll (see styles.css). */
export const SHELL_STACK_MAX_WIDTH_PX = 1199;

/** Pass to matchMedia for chat scroll shell behavior (align with CSS media queries). */
export const shellStackMaxWidthMediaQuery = `(max-width: ${SHELL_STACK_MAX_WIDTH_PX}px)`;

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
