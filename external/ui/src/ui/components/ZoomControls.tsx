import type { PointerEvent as ReactPointerEvent } from "react";

export interface ZoomControlsProps {
  /** The scale as a percentage, or null while the view is fitted. */
  percent: number | null;
  onZoomOut: () => void;
  onZoomIn: () => void;
  /** Fits the view: the level button's click. */
  onFit: () => void;
  canZoomOut?: boolean;
  canZoomIn?: boolean;
  labels: { group: string; zoomOut: string; zoomIn: string; fit: string };
  /** The prefix of the controls' test ids: <prefix>-zoom-out, -fit, -zoom-in. */
  testId: string;
  /** In a bar of its own surface (the picture viewer) rather than floating
   *  over the corner of what it zooms. */
  inline?: boolean;
}

/**
 * The zoom controls of every surface that zooms, the same everywhere: minus,
 * the level (the percentage, or the fit glyph while the view is fitted; a click
 * fits), plus, in one glass group at the top right. DESIGN.md, Zoom.
 */
export function ZoomControls(props: ZoomControlsProps) {
  const { labels, percent } = props;
  const level = percent === null ? null : `${percent}%`;
  return (
    <div
      className={
        "zoom-controls" + (props.inline ? " zoom-controls--inline" : "")
      }
      role="group"
      aria-label={labels.group}
      data-testid={`${props.testId}-zoom`}
      // A press on the controls is theirs, never the start of a pan.
      onPointerDown={(e: ReactPointerEvent<HTMLDivElement>) =>
        e.stopPropagation()
      }
    >
      <button
        type="button"
        className="zoom-control"
        data-testid={`${props.testId}-zoom-out`}
        aria-label={labels.zoomOut}
        title={labels.zoomOut}
        disabled={props.canZoomOut === false}
        onClick={props.onZoomOut}
      >
        <IconMinus />
      </button>
      <button
        type="button"
        className="zoom-control zoom-control--level"
        data-testid={`${props.testId}-fit`}
        aria-label={level ? `${level}, ${labels.fit}` : labels.fit}
        title={labels.fit}
        onClick={props.onFit}
      >
        {level ?? <IconFit />}
      </button>
      <button
        type="button"
        className="zoom-control"
        data-testid={`${props.testId}-zoom-in`}
        aria-label={labels.zoomIn}
        title={labels.zoomIn}
        disabled={props.canZoomIn === false}
        onClick={props.onZoomIn}
      >
        <IconPlus />
      </button>
    </div>
  );
}

function IconMinus() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M6 12h12" />
    </svg>
  );
}

function IconPlus() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 6v12M6 12h12" />
    </svg>
  );
}

export function IconFit() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M4 9V5a1 1 0 0 1 1-1h4" />
      <path d="M15 4h4a1 1 0 0 1 1 1v4" />
      <path d="M20 15v4a1 1 0 0 1-1 1h-4" />
      <path d="M9 20H5a1 1 0 0 1-1-1v-4" />
    </svg>
  );
}
