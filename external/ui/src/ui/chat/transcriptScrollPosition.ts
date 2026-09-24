/**
 * How close to the end still counts as the bottom of the transcript. The band
 * is what keeps stick-to-bottom following a stream whose last row is still
 * growing, and it is the same band that decides whether the scroll-to-bottom
 * button has anywhere to take the reader: the button appears exactly when the
 * transcript has stopped following the newest output.
 */
export const TRANSCRIPT_BOTTOM_THRESHOLD_PX = 80;

/** What a scroll viewport says about its position, whichever surface scrolls. */
export type TranscriptScrollMetrics = {
  scrollHeight: number;
  scrollTop: number;
  clientHeight: number;
};

/** Pixels left below the viewport; zero while overscrolling past the end. */
export function transcriptDistanceFromBottom(
  metrics: TranscriptScrollMetrics,
): number {
  return Math.max(
    0,
    metrics.scrollHeight - metrics.scrollTop - metrics.clientHeight,
  );
}

export function isTranscriptAtBottom(
  metrics: TranscriptScrollMetrics,
): boolean {
  return transcriptDistanceFromBottom(metrics) < TRANSCRIPT_BOTTOM_THRESHOLD_PX;
}

/** Desktop shell: `.chat-scroll` is the scrollport. */
export function elementTranscriptMetrics(
  el: HTMLElement,
): TranscriptScrollMetrics {
  return {
    scrollHeight: el.scrollHeight,
    scrollTop: el.scrollTop,
    clientHeight: el.clientHeight,
  };
}

/**
 * The part of the page the reader actually sees, as an offset from the layout
 * viewport's top and a height. An on-screen keyboard that overlays the page
 * (iOS Safari, and any browser that ignores `interactive-widget=
 * resizes-content`) leaves `innerHeight` alone and shrinks the visual viewport,
 * and the page can then scroll past the end `innerHeight` implies: measured
 * against the layout viewport, "the bottom" lands up the page by the keyboard's
 * height. Without the API the layout viewport is all there is.
 */
function visibleViewport(view: Window): { offsetTop: number; height: number } {
  const vv = view.visualViewport;
  if (vv && vv.height > 0) {
    return { offsetTop: vv.offsetTop || 0, height: vv.height };
  }
  return { offsetTop: 0, height: view.innerHeight };
}

/** Narrow shell (`max-width: 1199px`): the document itself scrolls. */
export function documentTranscriptMetrics(
  view: Window,
): TranscriptScrollMetrics {
  const visible = visibleViewport(view);
  return {
    scrollHeight: view.document.documentElement.scrollHeight,
    scrollTop: view.scrollY + visible.offsetTop,
    clientHeight: visible.height,
  };
}

/**
 * How much of the layout viewport an on-screen keyboard covers: the docked
 * composer is fixed to the layout viewport's bottom, so it is lifted by this
 * much to stay above the keyboard. Zero when the keyboard resizes the page
 * (Chrome with `interactive-widget=resizes-content`), when the browser has
 * panned the visual viewport down to the layout viewport's bottom, and during a
 * pinch zoom, which shrinks the visual viewport too and is not a keyboard.
 */
export function keyboardInset(view: Window): number {
  const vv = view.visualViewport;
  if (!vv || Math.abs((vv.scale || 1) - 1) > 0.01) return 0;
  return Math.max(0, Math.round(view.innerHeight - vv.height - (vv.offsetTop || 0)));
}

/** Furthest `scrollTop` of a scrollport: where "the newest message" actually is. */
export function elementScrollBottom(el: HTMLElement): number {
  return Math.max(0, el.scrollHeight - el.clientHeight);
}

/**
 * The same for the narrow shell, where the document is the scrollport: the
 * window scroll that brings the end of the document to the bottom of what the
 * reader sees (see `visibleViewport`).
 */
export function documentScrollBottom(view: Window): number {
  const doc = view.document;
  const height = Math.max(
    doc.body.scrollHeight,
    doc.documentElement.scrollHeight,
  );
  const visible = visibleViewport(view);
  return Math.max(0, height - visible.height - visible.offsetTop);
}

/**
 * How long the jump to the newest message takes. Short hops stay brisk, and a
 * transcript of any length lands in under half a second: the travel is there to
 * keep the reader oriented, not to tour what they scrolled past.
 */
export function transcriptJumpDurationMs(distancePx: number): number {
  return Math.min(460, Math.max(220, Math.abs(distancePx) * 0.22));
}

/**
 * Ease-out cubic: leaves at full speed and settles into the last pixels rather
 * than stopping dead against the end of the transcript.
 */
export function easeTranscriptJump(progress: number): number {
  const t = Math.min(1, Math.max(0, progress));
  return 1 - (1 - t) ** 3;
}
