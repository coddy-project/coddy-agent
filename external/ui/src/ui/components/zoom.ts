/**
 * One zoom for every surface that zooms: the picture viewer, the canvas and the
 * swarm map. The wheel zooms at the pointer, continuously, and the same turn of
 * it doubles or halves whatever is under it; the buttons step by ZOOM_STEP.
 * DESIGN.md, Zoom.
 */

// A wheel reports its delta in pixels, lines or pages (`deltaMode` 0, 1, 2),
// and a trackpad sends many small ones where a mouse sends a few large: one
// reading in pixels is what the zoom is driven by.
export const WHEEL_LINE_PX = 16;
export const WHEEL_PAGE_PX = 400;

/** The pixels of wheel that double what is under the pointer, up or down. */
export const WHEEL_DOUBLING_PX = 500;

/** What a press of + or - multiplies the scale by. */
export const ZOOM_STEP = 1.25;

/** A wheel's vertical delta in pixels, whatever the device counts in. */
export function wheelPixels(deltaY: number, deltaMode: number): number {
  if (!Number.isFinite(deltaY)) {
    return 0;
  }
  if (deltaMode === 1) {
    return deltaY * WHEEL_LINE_PX;
  }
  if (deltaMode === 2) {
    return deltaY * WHEEL_PAGE_PX;
  }
  return deltaY;
}

/** The factor a wheel event zooms by: above 1 for a turn up, below for down. */
export function wheelFactor(deltaY: number, deltaMode: number): number {
  return Math.pow(2, -wheelPixels(deltaY, deltaMode) / WHEEL_DOUBLING_PX);
}
