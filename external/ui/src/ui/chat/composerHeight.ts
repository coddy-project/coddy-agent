/**
 * How tall the composer's field is (issue #342). The field follows its text:
 * from the height its rows and its CSS minimum give it (the floor) it grows
 * line by line up to a ceiling, and past the ceiling it scrolls. The docked
 * composer can also be expanded: the field then takes the whole height of the
 * chat under its header, for writing a long prompt, until it is collapsed or
 * the prompt is sent.
 *
 * Every height here is the field's border box in CSS pixels, the number
 * `getBoundingClientRect` reads and `scrollHeight` plus the borders reaches.
 */

/** Lines the field grows to by itself before it scrolls. */
export const COMPOSER_AUTO_MAX_LINES = 8;

/**
 * Share of the visible viewport the field may take by itself. A phone with its
 * keyboard open keeps half of the screen; the transcript is not covered by a
 * field that grew while the reader typed.
 */
export const COMPOSER_AUTO_MAX_VIEWPORT_SHARE = 0.4;

/** Room kept between the expanded composer and the chat header above it. */
export const COMPOSER_EXPANDED_GAP_PX = 8;

export type ComposerFieldMetrics = {
  /** The height the rows and the CSS minimum give the field. */
  floorPx: number;
  /** The whole text with its padding: the height that shows it all. */
  contentPx: number;
  /** One line of the field's type. */
  lineHeightPx: number;
  /** Padding and borders above and below the text. */
  chromePx: number;
  /** The height of the visible viewport (`visualViewport`, else the window). */
  viewportPx: number;
  /**
   * With the field at its floor, how far the top of the composer block (the
   * queue, the plate and the card) is from the line the expanded composer
   * may reach: the chat header's bottom edge and the gap under it.
   */
  roomAbovePx: number;
};

/** The tallest the field grows by itself. Never below its floor. */
export function composerAutoCeilingPx(m: ComposerFieldMetrics): number {
  const byLines = COMPOSER_AUTO_MAX_LINES * m.lineHeightPx + m.chromePx;
  const byViewport = COMPOSER_AUTO_MAX_VIEWPORT_SHARE * m.viewportPx;
  return Math.max(m.floorPx, Math.min(byLines, byViewport));
}

/**
 * The height the field takes: the text's, between the floor and the ceiling;
 * expanded, everything up to the chat header, and never less than the text's.
 */
export function composerFieldHeightPx(
  m: ComposerFieldMetrics,
  expanded: boolean,
): number {
  const own = Math.min(
    Math.max(m.contentPx, m.floorPx),
    composerAutoCeilingPx(m),
  );
  if (!expanded) return own;
  return Math.max(own, m.floorPx + Math.max(0, m.roomAbovePx));
}
