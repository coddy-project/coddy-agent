import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";

/** How large the image is drawn: fitted to the window, or a scale of its own size. */
export type Zoom = "fit" | number;

/**
 * The ladder the keys and the buttons walk. A pinch is continuous and lands
 * between the rungs; the buttons then take the next rung above or below,
 * which is what they always did from a rung.
 */
export const ZOOM_LEVELS = [1, 1.5, 2, 3];

/** As far in as anything - a button, a key or a pinch - can go. */
export const MAX_ZOOM = ZOOM_LEVELS[ZOOM_LEVELS.length - 1]!;

// Levels are compared with a hair of room, so a factor a pinch rounded to
// 1.5 still counts as standing on that rung.
const LEVEL_EPSILON = 1e-6;

export function zoomIn(z: Zoom): Zoom {
  if (z === "fit") {
    return ZOOM_LEVELS[0]!;
  }
  return ZOOM_LEVELS.find((level) => level > z + LEVEL_EPSILON) ?? MAX_ZOOM;
}

export function zoomOut(z: Zoom): Zoom {
  if (z === "fit") {
    return "fit";
  }
  const under = ZOOM_LEVELS.filter((level) => level < z - LEVEL_EPSILON);
  return under.length === 0 ? "fit" : under[under.length - 1]!;
}

/** A picture fitted in a frame: the image drawn to the size of the window. */
function IconFit() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M4 9V5a1 1 0 0 1 1-1h4" />
      <path d="M15 4h4a1 1 0 0 1 1 1v4" />
      <path d="M20 15v4a1 1 0 0 1-1 1h-4" />
      <path d="M9 20H5a1 1 0 0 1-1-1v-4" />
      <rect x="8" y="9" width="8" height="6" rx="1" />
    </svg>
  );
}

/** A point of the image, as fractions of its size, and where in the stage it was seen. */
export type ZoomFocus = { fx: number; fy: number; vx: number; vy: number };

type Box = { left: number; top: number; width: number; height: number };

/** A pointer, or one finger of a pinch, in the client's coordinates. */
export type Point = { x: number; y: number };

const clamp01 = (n: number) => (Number.isFinite(n) ? Math.min(1, Math.max(0, n)) : 0.5);

/**
 * The point of the image under a spot of the stage (the stage's middle when
 * none is given), so a zoom can bring the same point back under it.
 */
export function focusAt(stage: Box, img: Box, clientX?: number, clientY?: number): ZoomFocus {
  const vx = clientX === undefined ? stage.width / 2 : clientX - stage.left;
  const vy = clientY === undefined ? stage.height / 2 : clientY - stage.top;
  return {
    fx: img.width > 0 ? clamp01((stage.left + vx - img.left) / img.width) : 0.5,
    fy: img.height > 0 ? clamp01((stage.top + vy - img.top) / img.height) : 0.5,
    vx,
    vy,
  };
}

/** The scroll offsets that put the focused point of the redrawn image back where it was seen. */
export function scrollToKeep(
  focus: ZoomFocus,
  stage: Box,
  img: Box,
  scroll: { left: number; top: number },
): { left: number; top: number } {
  return {
    left: Math.max(0, img.left - stage.left + scroll.left + focus.fx * img.width - focus.vx),
    top: Math.max(0, img.top - stage.top + scroll.top + focus.fy * img.height - focus.vy),
  };
}

/** How far a press may travel and still be the click that toggles the zoom. */
export const DRAG_SLOP_PX = 4;

/** Whether a press has travelled far enough to be a pan rather than a click. */
export function movedPastSlop(dx: number, dy: number): boolean {
  return Math.hypot(dx, dy) > DRAG_SLOP_PX;
}

function clampScroll(n: number, max: number): number {
  const far = Math.max(0, Number.isFinite(max) ? max : 0);
  return Number.isFinite(n) ? Math.min(far, Math.max(0, n)) : 0;
}

/**
 * Where the stage stands after a drag of (dx, dy) from where it stood when the
 * press began. The picture follows the hand, so the stage scrolls against it,
 * and neither axis runs past what there is to scroll.
 */
export function panScroll(
  scroll: { left: number; top: number },
  dx: number,
  dy: number,
  max: { left: number; top: number },
): { left: number; top: number } {
  return {
    left: clampScroll(scroll.left - dx, max.left),
    top: clampScroll(scroll.top - dy, max.top),
  };
}

/** The point between two fingers: what a pinch keeps where it is. */
export function touchMidpoint(a: Point, b: Point): Point {
  return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
}

/** How far apart two fingers are. */
export function touchSpan(a: Point, b: Point): number {
  return Math.hypot(b.x - a.x, b.y - a.y);
}

/**
 * The fitted image as a fraction of its own size, which is where a pinch
 * starts from and where it returns to. An image smaller than the stage is
 * drawn at its own size, and nothing measured yet reads as that too.
 */
export function fittedScale(drawnWidth: number, naturalWidth: number): number {
  if (!(drawnWidth > 0) || !(naturalWidth > 0)) {
    return 1;
  }
  return Math.min(1, drawnWidth / naturalWidth);
}

/**
 * The zoom a continuous gesture has reached: the scale it started from, times
 * how far the gesture went. It is a factor, not a rung of the ladder - a
 * gesture that only ever landed on 100%, 150%, 200% and 300% would quantise
 * the picture under the fingers - held between the fitted size, where it turns
 * back into `fit`, and the top of the ladder. The buttons and the keys still
 * walk the rungs, from wherever a gesture left the picture.
 */
export function scaleZoom(start: Zoom, fit: number, factor: number): Zoom {
  if (!(factor > 0) || !Number.isFinite(factor)) {
    return start;
  }
  const base = start === "fit" ? fit : start;
  const next = base * factor;
  if (!Number.isFinite(next) || next <= fit) {
    return "fit";
  }
  // Whole percents: what the level reads, and one re-render per step of it.
  return Math.min(MAX_ZOOM, Math.max(fit, Math.round(next * 100) / 100));
}

/** How far apart the fingers went, as the factor the zoom is scaled by. */
export function pinchZoom(start: Zoom, fit: number, startSpan: number, span: number): Zoom {
  if (!(startSpan > 0) || !(span > 0)) {
    return start;
  }
  return scaleZoom(start, fit, span / startSpan);
}

// A wheel reports its delta in pixels, lines or pages (`deltaMode` 0, 1, 2),
// and a trackpad sends many small ones where a mouse sends a few large: one
// reading in pixels is what the zoom is driven by.
export const WHEEL_LINE_PX = 16;
export const WHEEL_PAGE_PX = 400;

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

/** The pixels of wheel that double the picture, up or down. */
export const WHEEL_DOUBLING_PX = 500;

/**
 * The zoom a wheel has reached: continuous, like a pinch, and between the same
 * two ends. Up (a negative delta) grows the picture, from the fitted size when
 * it is still fitted; down shrinks it back to fit and no further, so a wheel
 * down over a fitted picture does nothing at all.
 */
export function wheelZoom(start: Zoom, fit: number, pixels: number): Zoom {
  if (!Number.isFinite(pixels) || pixels === 0 || (start === "fit" && pixels > 0)) {
    return start;
  }
  return scaleZoom(start, fit, Math.pow(2, -pixels / WHEEL_DOUBLING_PX));
}

// jsdom has neither, and a browser refuses a pointer that has already ended.
function capturePointer(el: Element | null, id: number) {
  try {
    if (el && typeof el.setPointerCapture === "function") {
      el.setPointerCapture(id);
    }
  } catch {
    // A pointer gone before the capture is one nothing needs to follow.
  }
}

function releasePointer(el: Element | null, id: number) {
  try {
    if (el && typeof el.releasePointerCapture === "function" && el.hasPointerCapture(id)) {
      el.releasePointerCapture(id);
    }
  } catch {
    // Released with the pointer itself.
  }
}

/**
 * An image opened over everything: fitted to the window first, then zoomed
 * with the buttons, the keys (+, -, 0), a click on the image or a pinch of two
 * fingers, and panned by dragging it or by scrolling the stage; a zoom keeps
 * the point clicked, the point between the fingers, or the middle of the view,
 * where it was. A press that travelled is a pan and never also the click that
 * toggles the zoom. Escape, the close control or a click beside the image
 * closes it. Rendered into the body, because a blurred backdrop above it - the
 * reader's dock, the composer card - would otherwise be the frame of a fixed box.
 *
 * The one viewer of the SPA: the documentation reader, the composer's
 * attachment cards and the sent bubble's all open this. Its `docs-lightbox*`
 * class names are older than that and stay as they are.
 */
export function ImageLightbox(props: { src: string; alt: string; onClose: () => void }) {
  const { t } = useT();
  const [zoom, setZoom] = useState<Zoom>("fit");
  const [natural, setNatural] = useState<{ w: number; h: number } | null>(null);
  const [panning, setPanning] = useState(false);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const imgRef = useRef<HTMLImageElement | null>(null);
  const stageRef = useRef<HTMLDivElement | null>(null);
  // The point to keep in view across the next zoom: the one clicked, or the
  // middle of the view for the keys and the buttons.
  const focusRef = useRef<ZoomFocus | null>(null);
  // Every pointer down on the stage, so the second finger of a pinch is seen
  // the moment it lands, and the one left over when it lifts.
  const pointersRef = useRef(new Map<number, Point>());
  const dragRef = useRef<
    { id: number; x: number; y: number; left: number; top: number } | null
  >(null);
  const pinchRef = useRef<{ span: number; zoom: Zoom; fit: number } | null>(null);
  // A press that travelled: its click is the end of a gesture, not a zoom.
  const movedRef = useRef(false);
  // The fitted image as a fraction of its own size, measured while it is
  // fitted and remembered for the gesture that starts once it is not.
  const fitRatioRef = useRef(1);
  // The live zoom for the wheel listener, which is attached once and would
  // otherwise read the zoom of the render that attached it - a burst of wheel
  // events arrives faster than React re-renders, and each one builds on the last.
  const zoomRef = useRef<Zoom>("fit");

  const changeZoom = (next: (z: Zoom) => Zoom, clientX?: number, clientY?: number) => {
    const stage = stageRef.current;
    const img = imgRef.current;
    if (stage && img) {
      focusRef.current = focusAt(
        stage.getBoundingClientRect(),
        img.getBoundingClientRect(),
        clientX,
        clientY,
      );
    }
    setZoom(next);
  };

  useLayoutEffect(() => {
    const focus = focusRef.current;
    const stage = stageRef.current;
    const img = imgRef.current;
    focusRef.current = null;
    if (!focus || !stage || !img) {
      return;
    }
    const to = scrollToKeep(focus, stage.getBoundingClientRect(), img.getBoundingClientRect(), {
      left: stage.scrollLeft,
      top: stage.scrollTop,
    });
    stage.scrollLeft = to.left;
    stage.scrollTop = to.top;
  }, [zoom]);

  // The fitted size is the floor of a pinch and of the wheel, so it is read
  // whenever the image is fitted: on the first draw, after it loads, and on
  // every return to fit.
  useLayoutEffect(() => {
    zoomRef.current = zoom;
    const img = imgRef.current;
    if (zoom === "fit" && img && natural) {
      fitRatioRef.current = fittedScale(img.getBoundingClientRect().width, natural.w);
    }
  }, [zoom, natural]);

  // An image the page already loaded is complete before the load handler
  // is attached: read its size straight away.
  useEffect(() => {
    const img = imgRef.current;
    if (img && img.complete && img.naturalWidth > 0) {
      setNatural({ w: img.naturalWidth, h: img.naturalHeight });
    }
  }, []);

  useEffect(() => {
    const before = document.activeElement as HTMLElement | null;
    closeRef.current?.focus();
    return () => before?.focus?.();
  }, []);

  // Leaving the screen closes the viewer. It renders into the body, so a host
  // the SPA hides rather than unmounts - a transcript the reader navigated away
  // from - would otherwise leave it over the new screen, swallowing every click.
  useEffect(() => {
    const leave = () => props.onClose();
    window.addEventListener("hashchange", leave);
    window.addEventListener("popstate", leave);
    return () => {
      window.removeEventListener("hashchange", leave);
      window.removeEventListener("popstate", leave);
    };
  }, [props]);

  // The wheel zooms around the pointer rather than scrolling the stage, and it
  // must not scroll the page behind the viewer either - which a passive
  // listener cannot prevent, and React's own `onWheel` is passive on some
  // engines. Hence one listener of our own, attached to the stage.
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) {
      return;
    }
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const current = zoomRef.current;
      const next = wheelZoom(current, fittedRatio(current), wheelPixels(e.deltaY, e.deltaMode));
      if (next === current) {
        return;
      }
      zoomRef.current = next;
      changeZoom(() => next, e.clientX, e.clientY);
    };
    stage.addEventListener("wheel", onWheel, { passive: false });
    return () => stage.removeEventListener("wheel", onWheel);
    // Only the picture's own size moves what the wheel reads; the zoom itself
    // travels in the ref, so a burst is not split across two listeners.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [natural]);

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      props.onClose();
    } else if (e.key === "+" || e.key === "=") {
      e.preventDefault();
      changeZoom(zoomIn);
    } else if (e.key === "-") {
      e.preventDefault();
      changeZoom(zoomOut);
    } else if (e.key === "0") {
      e.preventDefault();
      changeZoom(() => "fit");
    }
  };

  /** The fitted size a pinch or a wheel measures itself against. */
  const fittedRatio = (current: Zoom): number => {
    const img = imgRef.current;
    if (current === "fit" && img && natural) {
      return fittedScale(img.getBoundingClientRect().width, natural.w);
    }
    return fitRatioRef.current;
  };

  const twoFingers = (): [Point, Point] | null => {
    const points = Array.from(pointersRef.current.values());
    return points.length >= 2 ? [points[0]!, points[1]!] : null;
  };

  /** A finger or a pointer takes the picture, as long as there is room to move it. */
  const startDrag = (id: number, at: Point) => {
    const stage = stageRef.current;
    if (!stage || zoom === "fit") {
      return;
    }
    // Captured on the picture, so a drag that wanders off it keeps coming, and
    // so the click it ends with is still the picture's own.
    capturePointer(imgRef.current, id);
    dragRef.current = { id, x: at.x, y: at.y, left: stage.scrollLeft, top: stage.scrollTop };
    setPanning(true);
  };

  const endDrag = (id: number) => {
    if (dragRef.current?.id === id) {
      dragRef.current = null;
      setPanning(false);
      releasePointer(imgRef.current, id);
    }
  };

  const onPointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.pointerType === "mouse" && e.button !== 0) {
      return;
    }
    pointersRef.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const fingers = twoFingers();
    if (fingers) {
      // The second finger turns a pan into a pinch, and a pinch is never the
      // click that toggles the zoom.
      endDrag(dragRef.current?.id ?? e.pointerId);
      movedRef.current = true;
      pinchRef.current = {
        span: touchSpan(fingers[0], fingers[1]),
        zoom,
        fit: fittedRatio(zoom),
      };
      e.preventDefault();
      return;
    }
    movedRef.current = false;
    // Only the picture is dragged: a press beside it is the click that closes
    // the viewer, and capturing that one would take its click away.
    if (e.target === imgRef.current) {
      startDrag(e.pointerId, { x: e.clientX, y: e.clientY });
    }
  };

  const onPointerMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    const known = pointersRef.current.has(e.pointerId);
    if (!known) {
      return;
    }
    pointersRef.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const pinch = pinchRef.current;
    const fingers = twoFingers();
    if (pinch && fingers) {
      e.preventDefault();
      const next = pinchZoom(pinch.zoom, pinch.fit, pinch.span, touchSpan(fingers[0], fingers[1]));
      if (next === zoom) {
        return;
      }
      const middle = touchMidpoint(fingers[0], fingers[1]);
      changeZoom(() => next, middle.x, middle.y);
      return;
    }
    const drag = dragRef.current;
    const stage = stageRef.current;
    if (!drag || !stage || drag.id !== e.pointerId) {
      return;
    }
    e.preventDefault();
    const dx = e.clientX - drag.x;
    const dy = e.clientY - drag.y;
    if (movedPastSlop(dx, dy)) {
      movedRef.current = true;
    }
    const to = panScroll({ left: drag.left, top: drag.top }, dx, dy, {
      left: stage.scrollWidth - stage.clientWidth,
      top: stage.scrollHeight - stage.clientHeight,
    });
    stage.scrollLeft = to.left;
    stage.scrollTop = to.top;
  };

  const onPointerEnd = (e: ReactPointerEvent<HTMLDivElement>) => {
    pointersRef.current.delete(e.pointerId);
    if (pointersRef.current.size < 2) {
      pinchRef.current = null;
    }
    endDrag(e.pointerId);
    // The finger left over when a pinch ends takes the picture for a pan.
    const rest = Array.from(pointersRef.current.entries());
    if (!dragRef.current && rest.length === 1) {
      startDrag(rest[0]![0], rest[0]![1]);
    }
  };

  /** The click a gesture ends with, swallowed once, by whoever gets it first. */
  const gestureClick = (): boolean => {
    if (!movedRef.current) {
      return false;
    }
    movedRef.current = false;
    return true;
  };

  const fit = t("docs.lightbox.fit");
  const percent = zoom === "fit" ? null : `${Math.round(zoom * 100)}%`;

  return createPortal(
    <div
      className="docs-lightbox"
      role="dialog"
      aria-modal="true"
      aria-label={props.alt || t("docs.lightbox.label")}
      data-zoom={String(zoom)}
      onKeyDown={onKey}
    >
      <div className="docs-lightbox-bar">
        <span className="docs-lightbox-caption">{props.alt}</span>
        <button
          type="button"
          className="docs-lightbox-btn"
          data-testid="docs-lightbox-zoom-out"
          aria-label={t("docs.lightbox.zoomOut")}
          title={t("docs.lightbox.zoomOut")}
          disabled={zoom === "fit"}
          onClick={() => changeZoom(zoomOut)}
        >
          −
        </button>
        <button
          type="button"
          className="docs-lightbox-level"
          data-testid="docs-lightbox-fit"
          title={fit}
          aria-label={percent ? `${percent}, ${fit}` : fit}
          onClick={() => changeZoom(() => "fit")}
        >
          {percent ?? <IconFit />}
        </button>
        <button
          type="button"
          className="docs-lightbox-btn"
          data-testid="docs-lightbox-zoom-in"
          aria-label={t("docs.lightbox.zoomIn")}
          title={t("docs.lightbox.zoomIn")}
          disabled={zoom !== "fit" && zoom >= MAX_ZOOM}
          onClick={() => changeZoom(zoomIn)}
        >
          +
        </button>
        <button
          ref={closeRef}
          type="button"
          className="sessions-close"
          data-testid="docs-lightbox-close"
          aria-label={t("docs.lightbox.close")}
          title={t("docs.lightbox.close")}
          onClick={props.onClose}
        >
          ×
        </button>
      </div>
      <div
        ref={stageRef}
        className={panning ? "docs-lightbox-stage is-panning" : "docs-lightbox-stage"}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerEnd}
        onPointerCancel={onPointerEnd}
        onClick={(e) => {
          if (gestureClick()) {
            return;
          }
          if (e.target === e.currentTarget) {
            props.onClose();
          }
        }}
      >
        <img
          ref={imgRef}
          src={props.src}
          alt={props.alt}
          draggable={false}
          className={zoom === "fit" ? "is-fit" : "is-zoomed"}
          style={
            zoom !== "fit" && natural
              ? { width: `${Math.round(natural.w * zoom)}px` }
              : undefined
          }
          onLoad={(e) =>
            setNatural({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })
          }
          onClick={(e) => {
            if (gestureClick()) {
              return;
            }
            changeZoom((z) => (z === "fit" ? 2 : "fit"), e.clientX, e.clientY);
          }}
        />
      </div>
    </div>,
    document.body,
  );
}
