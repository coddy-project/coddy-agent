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

/** Narrow shell (`max-width: 1199px`): the document itself scrolls. */
export function documentTranscriptMetrics(
  view: Window,
): TranscriptScrollMetrics {
  return {
    scrollHeight: view.document.documentElement.scrollHeight,
    scrollTop: view.scrollY,
    clientHeight: view.innerHeight,
  };
}

/** Furthest `scrollTop` of a scrollport: where "the newest message" actually is. */
export function elementScrollBottom(el: HTMLElement): number {
  return Math.max(0, el.scrollHeight - el.clientHeight);
}

/** The same for the narrow shell, where the document is the scrollport. */
export function documentScrollBottom(view: Window): number {
  const doc = view.document;
  const height = Math.max(
    doc.body.scrollHeight,
    doc.documentElement.scrollHeight,
  );
  return Math.max(0, height - view.innerHeight);
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
