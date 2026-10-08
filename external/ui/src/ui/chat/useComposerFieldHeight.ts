import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  type RefObject,
} from "react";

import {
  composerFieldHeightPx,
  type ComposerFieldMetrics,
} from "./composerHeight";

/** One line of the field's type when the stylesheet says nothing (15px at 1.5). */
const FALLBACK_LINE_HEIGHT_PX = 22.5;

/**
 * Sizes the composer's field to its text, or to the whole chat under its
 * header while the composer is expanded (issue #342, rules in
 * `composerHeight.ts`).
 *
 * The floor is read off the field itself: its inline height is dropped for a
 * moment, so its rows and its CSS minimum decide, and the text's height is
 * `scrollHeight` at that size. A field that is not laid out (hidden, or a test
 * without layout) measures nothing and keeps the height it had.
 */
export function useComposerFieldHeight(o: {
  taRef: RefObject<HTMLTextAreaElement | null>;
  value: string;
  /** Changes when the field's rows change (the start screen and the dock). */
  layoutKey: unknown;
  expanded: boolean;
  /**
   * With the field at its floor, the room between the top of the docked block
   * and the line it may reach; `Infinity` where there is none.
   */
  roomAbove: () => number;
}): void {
  const { taRef } = o;
  const latest = useRef(o);
  latest.current = o;

  const fit = useCallback(() => {
    const ta = taRef.current;
    if (!ta) return;
    const scrollTop = ta.scrollTop;
    const held = ta.style.height;
    ta.style.height = "";
    const floorPx = ta.getBoundingClientRect().height;
    if (!(floorPx > 0)) {
      // Not laid out: keep whatever height it had.
      ta.style.height = held;
      return;
    }
    const cs = getComputedStyle(ta);
    const px = (v: string) => {
      const n = parseFloat(v);
      return Number.isFinite(n) ? n : 0;
    };
    const borderY = px(cs.borderTopWidth) + px(cs.borderBottomWidth);
    const paddingY = px(cs.paddingTop) + px(cs.paddingBottom);
    const { expanded, roomAbove } = latest.current;
    const m: ComposerFieldMetrics = {
      floorPx,
      contentPx: ta.scrollHeight + borderY,
      lineHeightPx: px(cs.lineHeight) || FALLBACK_LINE_HEIGHT_PX,
      chromePx: paddingY + borderY,
      viewportPx: window.visualViewport?.height ?? window.innerHeight,
      roomAbovePx: roomAbove(),
    };
    const height = composerFieldHeightPx(m, expanded);
    const cssHeight =
      cs.boxSizing === "border-box" ? height : height - paddingY - borderY;
    ta.style.height = `${cssHeight}px`;
    ta.scrollTop = scrollTop;
  }, [taRef]);

  useLayoutEffect(() => {
    fit();
  }, [fit, o.value, o.layoutKey, o.expanded]);

  // A narrower field wraps the same text onto more lines, and a keyboard that
  // opens or closes changes what share of the screen the field may take and
  // how much room an expanded composer has.
  useEffect(() => {
    const ta = taRef.current;
    let lastWidth = -1;
    const ro =
      typeof ResizeObserver !== "undefined" && ta?.parentElement
        ? new ResizeObserver((entries) => {
            const w = entries[0]?.contentRect.width ?? -1;
            if (w === lastWidth) return;
            lastWidth = w;
            fit();
          })
        : null;
    if (ta?.parentElement) ro?.observe(ta.parentElement);
    const vv = window.visualViewport;
    window.addEventListener("resize", fit);
    vv?.addEventListener("resize", fit);
    return () => {
      ro?.disconnect();
      window.removeEventListener("resize", fit);
      vv?.removeEventListener("resize", fit);
    };
  }, [fit, taRef, o.layoutKey]);
}
