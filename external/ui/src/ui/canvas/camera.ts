/**
 * The camera of the canvas board: where the board's origin sits on the screen
 * and how much it is scaled. A frame at board point (x, y) of size (w, h)
 * is drawn at (camX + x*scale, camY + y*scale), w*scale wide. Pure functions,
 * so the arithmetic is tested without a layout.
 */

export interface Camera {
  x: number;
  y: number;
  scale: number;
}

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export const MIN_SCALE = 0.05;
export const MAX_SCALE = 2;

/** Room kept around what Fit shows, in screen pixels. */
export const FIT_PADDING = 32;

/** Room above a frame for its title bar, in screen pixels. */
export const TITLE_ROOM = 34;

function clampScale(s: number): number {
  if (!Number.isFinite(s) || s <= 0) return 1;
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, s));
}

/** The smallest rectangle that holds every rectangle. */
export function boundsOf(rects: Rect[]): Rect | null {
  if (rects.length === 0) return null;
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const r of rects) {
    minX = Math.min(minX, r.x);
    minY = Math.min(minY, r.y);
    maxX = Math.max(maxX, r.x + r.width);
    maxY = Math.max(maxY, r.y + r.height);
  }
  return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
}

/** The camera that shows the whole rectangle in the viewport, centred, never
 *  past 100%: a lone phone frame is shown at its size, not blown up. */
export function fitCamera(
  bounds: Rect,
  viewport: { width: number; height: number },
): Camera {
  const availW = Math.max(1, viewport.width - FIT_PADDING * 2);
  const availH = Math.max(1, viewport.height - FIT_PADDING * 2 - TITLE_ROOM);
  const scale = clampScale(
    Math.min(
      1,
      availW / Math.max(1, bounds.width),
      availH / Math.max(1, bounds.height),
    ),
  );
  return {
    scale,
    x: (viewport.width - bounds.width * scale) / 2 - bounds.x * scale,
    y:
      TITLE_ROOM +
      (viewport.height - TITLE_ROOM - bounds.height * scale) / 2 -
      bounds.y * scale,
  };
}

/** Zooms by factor keeping the board point under focus (screen coordinates)
 *  where it is. */
export function zoomAt(
  cam: Camera,
  factor: number,
  focus: { x: number; y: number },
): Camera {
  const scale = clampScale(
    cam.scale * (Number.isFinite(factor) && factor > 0 ? factor : 1),
  );
  const bx = (focus.x - cam.x) / cam.scale;
  const by = (focus.y - cam.y) / cam.scale;
  return { scale, x: focus.x - bx * scale, y: focus.y - by * scale };
}

/** Moves the board by a screen distance. */
export function pan(cam: Camera, dx: number, dy: number): Camera {
  return {
    ...cam,
    x: cam.x + (Number.isFinite(dx) ? dx : 0),
    y: cam.y + (Number.isFinite(dy) ? dy : 0),
  };
}

/** Where a board rectangle lands on the screen. */
export function toScreen(cam: Camera, r: Rect): Rect {
  return {
    x: cam.x + r.x * cam.scale,
    y: cam.y + r.y * cam.scale,
    width: r.width * cam.scale,
    height: r.height * cam.scale,
  };
}

/** The scale as the zoom control reads it. */
export function zoomPercent(cam: Camera): number {
  return Math.round(cam.scale * 100);
}
