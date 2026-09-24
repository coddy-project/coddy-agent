import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type RefObject,
} from "react";
import { flushSync } from "react-dom";

import {
  CHUNK_ROWS,
  growRenderWindowDown,
  growRenderWindowUp,
  MAX_ROWS,
  OPENING_RENDER_WINDOW,
  resolveRenderWindow,
  rowIndexById,
  tailRenderWindow,
  TRIM_SLACK_ROWS,
  trimBottomTo,
  trimRenderWindowBottom,
  trimRenderWindowTop,
  trimTopTo,
  type MeasuredRow,
  type RenderWindow,
} from "./transcriptRenderWindow";
import type { TranscriptItem } from "./types";

/**
 * Whether the render window runs here. It measures rows, so it needs a real
 * layout engine; jsdom, which has none, lacks IntersectionObserver too, and
 * there the whole list renders as it always did.
 */
export function transcriptWindowSupported(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.IntersectionObserver === "function"
  );
}

/** A row is looked up by the id MessageList stamps on it. */
function rowSelector(id: string): string {
  const escaped =
    typeof CSS !== "undefined" && typeof CSS.escape === "function"
      ? CSS.escape(id)
      : id.replace(/["\\]/g, "\\$&");
  return `[data-row-id="${escaped}"]`;
}

/**
 * Keeps a bounded slice of a transcript in the DOM (issue #338).
 *
 * The window opens on the last rows and grows by a chunk per frame toward the
 * edge the reader approaches, until that edge is a screen away or the list
 * ends; at the top of the list it asks for an older page. Past `MAX_ROWS` it
 * drops rows on the far side that lie more than a screen out of view. A change
 * above the visible area keeps the first visible row where it was, so nothing
 * moves under the reader in any engine (WebKit does not anchor scrolling by
 * itself). While the window reaches the newest row it is attached to the tail
 * and new rows join it.
 */
export function useTranscriptWindow(p: {
  items: TranscriptItem[];
  /** The conversation on screen; a new one opens on its last rows. */
  resetKey: string;
  enabled: boolean;
  /** `.messages-inner`, whose direct children are the rows. */
  listRef: RefObject<HTMLElement | null>;
  /** `.chat-scroll` on the split shell. */
  scrollerRef: RefObject<HTMLElement | null>;
  /** The stacked shell scrolls the document instead. */
  docScroll: boolean;
  /** The server holds history above the first row. */
  hasOlder: boolean;
  olderLoading: boolean;
  /** The last read of the page above failed: the window waits for the
   *  reader's Retry instead of asking again every frame. */
  olderFailed: boolean;
  onLoadOlder: () => void;
  /** The reader follows the newest message: a change above keeps the bottom
   *  in place rather than the first visible row. */
  stickToBottomRef: RefObject<boolean>;
  /** Something at the tail waits for the reader - a permission or a question
   *  prompt with its choices and typed text: the window never drops the
   *  bottom, so it stays mounted however far up the reader goes. */
  pinTail?: boolean;
}): {
  start: number;
  end: number;
  attached: boolean;
  topSentinelRef: RefObject<HTMLDivElement | null>;
  bottomSentinelRef: RefObject<HTMLDivElement | null>;
  /** Renders the rows above the window now (the "earlier" control). */
  showEarlier: () => void;
  /** Puts the window back on the newest rows; `then` runs once it is. */
  attachToTail: (then?: () => void) => void;
} {
  const { items, enabled } = p;
  const [win, setWin] = useState<RenderWindow>(OPENING_RENDER_WINDOW);
  const [lastKey, setLastKey] = useState(p.resetKey);
  if (lastKey !== p.resetKey) {
    setLastKey(p.resetKey);
    setWin(OPENING_RENDER_WINDOW);
  }
  const index = useMemo(() => rowIndexById(items), [items]);
  const range = useMemo(
    () =>
      enabled
        ? resolveRenderWindow(items, index, win)
        : { start: 0, end: items.length },
    [enabled, items, index, win],
  );
  const attached = !enabled || win.endId === null;

  const topSentinelRef = useRef<HTMLDivElement | null>(null);
  const bottomSentinelRef = useRef<HTMLDivElement | null>(null);
  const latest = useRef({ items, index, range, attached, p });
  useLayoutEffect(() => {
    latest.current = { items, index, range, attached, p };
  });
  const anchorRef = useRef<
    { pinBottom: true } | { pinBottom: false; id: string; top: number } | null
  >(null);
  const afterAttachRef = useRef<(() => void) | null>(null);
  const frameRef = useRef<number | null>(null);

  const viewport = useCallback((): { top: number; bottom: number } => {
    const { p: cur } = latest.current;
    if (cur.docScroll) {
      const vv = window.visualViewport;
      const top = vv ? vv.offsetTop : 0;
      return { top, bottom: top + (vv ? vv.height : window.innerHeight) };
    }
    const el = cur.scrollerRef.current;
    if (!el) return { top: 0, bottom: window.innerHeight };
    const r = el.getBoundingClientRect();
    return { top: r.top, bottom: r.bottom };
  }, []);

  const measureRows = useCallback((): MeasuredRow[] => {
    const list = latest.current.p.listRef.current;
    if (!list) return [];
    const out: MeasuredRow[] = [];
    for (const el of Array.from(list.children)) {
      const id = (el as HTMLElement).dataset?.rowId;
      if (!id) continue;
      const r = el.getBoundingClientRect();
      out.push({ id, top: r.top, bottom: r.bottom });
    }
    return out;
  }, []);

  // What must not move when the rows above change: the newest message for a
  // reader who follows it, otherwise the first row in view where it stands.
  const captureAnchor = useCallback(() => {
    if (latest.current.p.stickToBottomRef.current) {
      anchorRef.current = { pinBottom: true };
      return;
    }
    const { top } = viewport();
    const row = measureRows().find((r) => r.bottom > top);
    anchorRef.current = row
      ? { pinBottom: false, id: row.id, top: row.top }
      : null;
  }, [measureRows, viewport]);

  // A change above the visible area is measured and committed in one task:
  // were the commit left to React's scheduler, the reader could scroll in
  // between, and keeping the row where it stood before would undo that scroll.
  const commitAbove = useCallback(
    (next: RenderWindow) => {
      captureAnchor();
      flushSync(() => setWin(next));
    },
    [captureAnchor],
  );

  // A row the reader is typing in stays: a plan being edited, an answer
  // being written. Focus on a button - a copy control clicked before
  // scrolling away - holds nothing, and keeping its row would let the window
  // grow without bound.
  const focusedRowId = useCallback((): string | null => {
    const list = latest.current.p.listRef.current;
    const active = document.activeElement as HTMLElement | null;
    if (!list || !active || !list.contains(active)) return null;
    const editable =
      active.isContentEditable ||
      active instanceof HTMLTextAreaElement ||
      active instanceof HTMLSelectElement ||
      (active instanceof HTMLInputElement &&
        !["button", "checkbox", "radio", "submit", "reset"].includes(active.type));
    if (!editable) return null;
    const row = (active as HTMLElement).closest?.("[data-row-id]");
    return row instanceof HTMLElement ? (row.dataset.rowId ?? null) : null;
  }, []);

  // One look per frame: grow toward the edge the reader nears, and past the
  // bound drop rows on the other side in the same commit, so a reader
  // scrolling without pause does not outrun the trim.
  const check = useCallback(() => {
    frameRef.current = null;
    const cur = latest.current;
    if (!cur.p.enabled) return;
    const list = cur.p.listRef.current;
    if (!list) return;
    const { top: vTop, bottom: vBottom } = viewport();
    const margin = Math.max(400, vBottom - vTop);
    const len = cur.items.length;
    let range = cur.range;
    let next: RenderWindow | null = null;
    let above = false;
    let grewUp = false;

    const topS = topSentinelRef.current?.getBoundingClientRect();
    if (topS && topS.bottom > vTop - margin) {
      if (range.start > 0) {
        next = growRenderWindowUp(cur.items, range, cur.attached);
        range = { start: Math.max(0, range.start - CHUNK_ROWS), end: range.end };
        above = true;
        grewUp = true;
      } else if (cur.p.hasOlder && !cur.p.olderLoading && !cur.p.olderFailed) {
        cur.p.onLoadOlder();
      }
    }
    if (!next) {
      const botS = bottomSentinelRef.current?.getBoundingClientRect();
      if (range.end < len && botS && botS.top < vBottom + margin) {
        next = growRenderWindowDown(cur.items, range);
        range = {
          start: range.start,
          end: Math.min(len, range.end + CHUNK_ROWS),
        };
      }
    }
    if (range.end - range.start > MAX_ROWS + TRIM_SLACK_ROWS) {
      const rows = measureRows();
      const focused = focusedRowId();
      const attached = next ? next.endId === null : cur.attached;
      const newStart = grewUp
        ? range.start
        : trimTopTo(rows, cur.index, range, vTop, margin, focused);
      if (newStart > range.start) {
        next = trimRenderWindowTop(cur.items, range, attached, newStart);
        above = true;
      } else if (!cur.p.pinTail) {
        const newEnd = trimBottomTo(
          rows,
          cur.index,
          range,
          vBottom,
          margin,
          focused,
        );
        if (newEnd < range.end) {
          next = trimRenderWindowBottom(cur.items, range, newEnd);
        }
      }
    }
    if (!next) return;
    if (above) commitAbove(next);
    else setWin(next);
  }, [commitAbove, focusedRowId, measureRows, viewport]);

  const schedule = useCallback(() => {
    if (frameRef.current !== null) return;
    frameRef.current = requestAnimationFrame(check);
  }, [check]);

  useEffect(
    () => () => {
      if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
      frameRef.current = null;
    },
    [],
  );

  // After every commit of a new slice: keep the anchor row still, run a
  // pending jump, then look again - a sentinel still in range grows the window
  // by another chunk next frame, which is what renders a long transcript in
  // short tasks instead of one long one.
  useLayoutEffect(() => {
    if (!enabled) return;
    const anchor = anchorRef.current;
    anchorRef.current = null;
    const list = p.listRef.current;
    if (anchor?.pinBottom) {
      if (p.docScroll) {
        const doc = document.scrollingElement ?? document.documentElement;
        window.scrollTo({ top: doc.scrollHeight, left: 0, behavior: "auto" });
      } else if (p.scrollerRef.current) {
        const el = p.scrollerRef.current;
        el.scrollTop = el.scrollHeight - el.clientHeight;
      }
    } else if (anchor && list) {
      const el = list.querySelector(rowSelector(anchor.id));
      if (el) {
        const delta = el.getBoundingClientRect().top - anchor.top;
        if (Math.abs(delta) >= 0.5) {
          if (p.docScroll) {
            window.scrollBy(0, delta);
          } else if (p.scrollerRef.current) {
            p.scrollerRef.current.scrollTop += delta;
          }
        }
      }
    }
    if (attached && afterAttachRef.current) {
      const then = afterAttachRef.current;
      afterAttachRef.current = null;
      then();
    }
    schedule();
  }, [enabled, range.start, range.end, attached, p.listRef, p.docScroll, p.scrollerRef, schedule]);

  // Anything that can bring an edge into range asks for a look.
  useEffect(() => {
    if (enabled) schedule();
  }, [enabled, items, p.hasOlder, p.olderLoading, schedule]);

  useEffect(() => {
    if (!enabled) return undefined;
    const target: HTMLElement | Window | null = p.docScroll
      ? window
      : p.scrollerRef.current;
    const onScroll = () => schedule();
    target?.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll);
    const io = new IntersectionObserver(() => schedule(), {
      root: p.docScroll ? null : p.scrollerRef.current,
      rootMargin: "600px 0px",
    });
    if (topSentinelRef.current) io.observe(topSentinelRef.current);
    if (bottomSentinelRef.current) io.observe(bottomSentinelRef.current);
    return () => {
      target?.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
      io.disconnect();
    };
  }, [enabled, p.docScroll, p.scrollerRef, schedule, range.start > 0 || p.hasOlder, range.end < items.length, attached]);

  const showEarlier = useCallback(() => {
    const cur = latest.current;
    if (cur.range.start > 0) {
      commitAbove(
        growRenderWindowUp(cur.items, cur.range, cur.attached, CHUNK_ROWS),
      );
      return;
    }
    if (cur.p.hasOlder && !cur.p.olderLoading) cur.p.onLoadOlder();
  }, [commitAbove]);

  const attachToTail = useCallback((then?: () => void) => {
    const cur = latest.current;
    if (cur.attached) {
      then?.();
      return;
    }
    afterAttachRef.current = then ?? null;
    setWin(tailRenderWindow(cur.items));
  }, []);

  return {
    start: range.start,
    end: range.end,
    attached,
    topSentinelRef,
    bottomSentinelRef,
    showEarlier,
    attachToTail,
  };
}
