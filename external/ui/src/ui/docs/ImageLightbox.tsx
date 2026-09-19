import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";

/** How large the image is drawn: fitted to the window, or a scale of its own size. */
type Zoom = "fit" | number;

const LEVELS = [1, 1.5, 2, 3];

function zoomIn(z: Zoom): Zoom {
  if (z === "fit") {
    return LEVELS[0]!;
  }
  return LEVELS[Math.min(LEVELS.length - 1, LEVELS.indexOf(z) + 1)]!;
}

function zoomOut(z: Zoom): Zoom {
  if (z === "fit") {
    return "fit";
  }
  const i = LEVELS.indexOf(z);
  return i <= 0 ? "fit" : LEVELS[i - 1]!;
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

/**
 * An image of the documentation, opened over everything: fitted to the window
 * first, then zoomed with the buttons, the keys (+, -, 0) or a click on the
 * image, and panned by scrolling; a zoom keeps the point clicked, or the
 * middle of the view, where it was. Escape, the close control or a click
 * beside the image closes it. Rendered into the body, because the reader's
 * dock blurs its backdrop and would otherwise be the frame of a fixed box.
 */
export function ImageLightbox(props: { src: string; alt: string; onClose: () => void }) {
  const { t } = useT();
  const [zoom, setZoom] = useState<Zoom>("fit");
  const [natural, setNatural] = useState<{ w: number; h: number } | null>(null);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const imgRef = useRef<HTMLImageElement | null>(null);
  const stageRef = useRef<HTMLDivElement | null>(null);
  // The point to keep in view across the next zoom: the one clicked, or the
  // middle of the view for the keys and the buttons.
  const focusRef = useRef<ZoomFocus | null>(null);

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
          disabled={zoom === LEVELS[LEVELS.length - 1]}
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
        className="docs-lightbox-stage"
        onClick={(e) => {
          if (e.target === e.currentTarget) {
            props.onClose();
          }
        }}
      >
        <img
          ref={imgRef}
          src={props.src}
          alt={props.alt}
          className={zoom === "fit" ? "is-fit" : "is-zoomed"}
          style={
            zoom !== "fit" && natural
              ? { width: `${Math.round(natural.w * zoom)}px` }
              : undefined
          }
          onLoad={(e) =>
            setNatural({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })
          }
          onClick={(e) => changeZoom((z) => (z === "fit" ? 2 : "fit"), e.clientX, e.clientY)}
        />
      </div>
    </div>,
    document.body,
  );
}
