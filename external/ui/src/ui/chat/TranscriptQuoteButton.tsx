import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import { useT } from "../i18n/I18nProvider";

/** How long a selection rests before the button offers it. */
const SETTLE_MS = 120;

/** Room between the selection and the button, and the button's own height. */
const GAP_PX = 10;
const BUTTON_PX = 32;

type Offer = {
  text: string;
  /** Viewport coordinates of the point the button hangs from. */
  x: number;
  y: number;
  /** The button stands above that point, or below it. */
  above: boolean;
};

/**
 * Offers the text the reader selected in the transcript as a quote for the
 * next message (issue #342). A **Quote** button appears by the selection once
 * it rests; a press hands the text to `onQuote` and clears the selection.
 *
 * With a mouse it stands above the selection, out of the way of the next line
 * the reader is reading; on a touch screen it stands below, since the
 * browser's own selection menu takes the space above. It keeps inside the
 * band between the chat header and the docked composer, and leaves when the
 * selection scrolls out of that band or collapses.
 */
export function TranscriptQuoteButton(props: {
  /** The transcript's column: only a selection inside it is offered. */
  root: () => HTMLElement | null;
  /** The top of the band the button keeps in (the chat header's bottom). */
  topLimit: () => number;
  /** The bottom of that band (the docked composer's top). */
  bottomLimit: () => number;
  touch: boolean;
  onQuote: (text: string) => void;
}) {
  const { t } = useT();
  const [offer, setOffer] = useState<Offer | null>(null);
  const latest = useRef(props);
  latest.current = props;
  // A press on the button keeps it while the browser takes the selection
  // down under the finger: the text it offers was read when it appeared.
  const pressingRef = useRef(false);

  const read = useCallback(() => {
    if (pressingRef.current) return;
    const p = latest.current;
    const sel = document.getSelection();
    const root = p.root();
    if (!sel || sel.isCollapsed || sel.rangeCount === 0 || !root) {
      setOffer(null);
      return;
    }
    const range = sel.getRangeAt(0);
    if (!root.contains(range.commonAncestorContainer)) {
      setOffer(null);
      return;
    }
    const text = sel.toString();
    if (!text.trim() || typeof range.getBoundingClientRect !== "function") {
      setOffer(null);
      return;
    }
    const box = range.getBoundingClientRect();
    let top = p.topLimit();
    let bottom = p.bottomLimit();
    if (bottom - top < BUTTON_PX) {
      // Nothing laid out to keep clear of: the window is the band.
      top = 0;
      bottom = window.innerHeight;
    }
    if (box.bottom < top || box.top > bottom) {
      setOffer(null);
      return;
    }
    const fitsAbove = box.top - GAP_PX - BUTTON_PX >= top;
    const fitsBelow = box.bottom + GAP_PX + BUTTON_PX <= bottom;
    let above = p.touch ? !fitsBelow && fitsAbove : fitsAbove || !fitsBelow;
    let y = above ? box.top - GAP_PX : box.bottom + GAP_PX;
    if (!fitsAbove && !fitsBelow) {
      // A selection taller than the band: the button waits at its bottom.
      above = true;
      y = bottom - GAP_PX;
    }
    const half = 56;
    const x = Math.min(
      Math.max(box.left + box.width / 2, half),
      window.innerWidth - half,
    );
    setOffer({ text, x, y, above });
  }, []);

  useEffect(() => {
    let timer: number | null = null;
    const settle = () => {
      if (timer !== null) window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        timer = null;
        read();
      }, SETTLE_MS);
    };
    // Scrolling moves the selection under a button that is already out.
    let frame: number | null = null;
    const follow = () => {
      if (frame !== null) return;
      frame = requestAnimationFrame(() => {
        frame = null;
        read();
      });
    };
    document.addEventListener("selectionchange", settle);
    window.addEventListener("scroll", follow, { capture: true, passive: true });
    window.addEventListener("resize", follow);
    return () => {
      if (timer !== null) window.clearTimeout(timer);
      if (frame !== null) cancelAnimationFrame(frame);
      document.removeEventListener("selectionchange", settle);
      window.removeEventListener("scroll", follow, { capture: true });
      window.removeEventListener("resize", follow);
    };
  }, [read]);

  if (!offer) return null;
  const label = t("chat.quoteSelection");
  return createPortal(
    <button
      type="button"
      className={
        offer.above
          ? "transcript-quote-btn"
          : "transcript-quote-btn transcript-quote-btn--below"
      }
      data-testid="transcript-quote"
      style={{ left: `${offer.x}px`, top: `${offer.y}px` }}
      title={t("chat.quoteSelectionTitle")}
      onPointerDown={(e) => {
        // Keep the selection (and the caret where it was) through the press.
        e.preventDefault();
        pressingRef.current = true;
        // A press that never became a click (dragged off) lets go again.
        window.setTimeout(() => {
          pressingRef.current = false;
        }, 1000);
      }}
      onPointerCancel={() => {
        pressingRef.current = false;
      }}
      onClick={() => {
        pressingRef.current = false;
        const text = offer.text;
        document.getSelection()?.removeAllRanges();
        setOffer(null);
        props.onQuote(text);
      }}
    >
      <svg
        width="14"
        height="14"
        viewBox="0 0 16 16"
        fill="currentColor"
        xmlns="http://www.w3.org/2000/svg"
        aria-hidden
      >
        <path d="M3 4.5A1.5 1.5 0 0 1 4.5 3h2A1.5 1.5 0 0 1 8 4.5v2.2c0 2.5-1.2 4.4-3.4 5.5a.6.6 0 0 1-.55-1.07C5.2 10.5 5.9 9.6 6.15 8H4.5A1.5 1.5 0 0 1 3 6.5v-2Zm6 0A1.5 1.5 0 0 1 10.5 3h2A1.5 1.5 0 0 1 14 4.5v2.2c0 2.5-1.2 4.4-3.4 5.5a.6.6 0 0 1-.55-1.07C11.2 10.5 11.9 9.6 12.15 8H10.5A1.5 1.5 0 0 1 9 6.5v-2Z" />
      </svg>
      <span>{label}</span>
    </button>,
    document.body,
  );
}
