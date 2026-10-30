/**
 * The room under a transcript for the docked block over it (the composer, and
 * the usage banner when one rises into the block): the height of the
 * transcript's spacer, `--chat-composer-reserve`.
 *
 * A transcript parked at its end puts its newest row two row gaps above the
 * block's first visible edge (`TRANSCRIPT_END_GAP_ROWS`): one row gap read as
 * pressed against the composer's card. The live line stands the same two row
 * gaps under the row above it by default (its padding, styles/chat/_messages.scss); without a
 * checklist the active line reduces that top gap by 4px. The
 * block's padding over its card is empty and does not count, and where the
 * scroll content ends is not always where the block ends: on the stacked shell
 * the document's content stops above the shell's own bottom padding while the
 * fixed block stands on its own.
 */

/** Row gaps between a parked transcript's newest row and the docked block. */
export const TRANSCRIPT_END_GAP_ROWS = 2;

/** No transcript reserves less than this under it. */
export const COMPOSER_RESERVE_FLOOR_PX = 140;

export function composerReserveFor(m: {
  /** The docked block's height. */
  blockHeight: number;
  /** Its empty padding over its first visible edge. */
  blockPaddingTop: number;
  /** The transcript's gap between two rows. */
  rowGap: number;
  /** How far above the block's bottom the scroll content ends. */
  contentEndAboveBlockEnd: number;
}): number {
  return Math.max(
    COMPOSER_RESERVE_FLOOR_PX,
    Math.ceil(
      m.blockHeight -
        m.blockPaddingTop +
        TRANSCRIPT_END_GAP_ROWS * m.rowGap -
        m.contentEndAboveBlockEnd,
    ),
  );
}

/**
 * The block's empty padding over its first visible edge: the card wrapper's
 * own padding while the card is the block's top, none while a usage banner
 * stands over it (the wrapper's padding then lies under the banner).
 */
export function blockEdgePaddingTop(host: Element): number {
  if (host.querySelector(":scope > .usage-banner")) return 0;
  const wrap = host.querySelector(":scope > .composer-wrap");
  if (!wrap) return 0;
  const v = parseFloat(getComputedStyle(wrap).paddingTop);
  return Number.isFinite(v) ? v : 0;
}
