import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useState,
  type RefObject,
} from "react";
import { shellStackMaxWidthMediaQuery } from "../shellBreakpoint";
import type { PickerFloatRect } from "./composerPickerApi";

/**
 * Layout measurement for the composer's overlay surfaces: whether pickers use
 * the bottom sheet (stacked shells), the sheet's bottom offset above the
 * docked composer, and the floating picker rect anchored to the field wrap.
 */
export function useComposerSheetLayout({
  isEmpty,
  pickerOpen,
  sheetOverlayOpen,
  composerCardRef,
  composerFieldWrapRef,
}: {
  isEmpty: boolean;
  pickerOpen: boolean;
  sheetOverlayOpen: boolean;
  composerCardRef: RefObject<HTMLDivElement | null>;
  composerFieldWrapRef: RefObject<HTMLDivElement | null>;
}): {
  pickerUseSheet: boolean;
  sheetBottomPx: number | null;
  pickerFloatRect: PickerFloatRect | null;
} {
  /** Stacked-shell viewports (`max-width`) use a bottom sheet so the picker is not clipped off-screen. */
  const [pickerUseSheet, setPickerUseSheet] = useState(() => {
    if (typeof window === "undefined") {
      return false;
    }
    return window.matchMedia(shellStackMaxWidthMediaQuery).matches;
  });
  const [sheetBottomPx, setSheetBottomPx] = useState<number | null>(null);
  const [pickerFloatRect, setPickerFloatRect] =
    useState<PickerFloatRect | null>(null);

  const measureSheetBottom = useCallback(() => {
    if (typeof window === "undefined") {
      return;
    }
    const useSheet = window.matchMedia(shellStackMaxWidthMediaQuery).matches;
    if (!useSheet) {
      setSheetBottomPx(null);
      return;
    }
    if (isEmpty) {
      setSheetBottomPx(0);
      return;
    }
    const el =
      composerCardRef.current ??
      document.querySelector<HTMLElement>(
        ".composer-wrap-docked .composer-card",
      );
    if (!el) {
      setSheetBottomPx(null);
      return;
    }
    const r = el.getBoundingClientRect();
    setSheetBottomPx(Math.max(0, Math.round(window.innerHeight - r.top + 8)));
  }, [isEmpty]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const mq = window.matchMedia(shellStackMaxWidthMediaQuery);
    const sync = () => setPickerUseSheet(mq.matches);
    sync();
    mq.addEventListener("change", sync);
    return () => mq.removeEventListener("change", sync);
  }, []);

  useLayoutEffect(() => {
    if (!sheetOverlayOpen) {
      setSheetBottomPx(null);
      return;
    }
    if (typeof window !== "undefined") {
      setPickerUseSheet(
        window.matchMedia(shellStackMaxWidthMediaQuery).matches,
      );
    }
    measureSheetBottom();
    window.addEventListener("resize", measureSheetBottom);
    window.addEventListener("scroll", measureSheetBottom, { passive: true });
    const card =
      composerCardRef.current ??
      document.querySelector<HTMLElement>(
        ".composer-wrap-docked .composer-card",
      );
    const ro =
      typeof ResizeObserver !== "undefined" && card
        ? new ResizeObserver(() => measureSheetBottom())
        : null;
    if (card) {
      ro?.observe(card);
    }
    return () => {
      window.removeEventListener("resize", measureSheetBottom);
      window.removeEventListener("scroll", measureSheetBottom);
      ro?.disconnect();
    };
  }, [sheetOverlayOpen, measureSheetBottom]);

  const measurePickerFloat = useCallback(() => {
    if (!pickerOpen) {
      setPickerFloatRect(null);
      return;
    }
    if (pickerUseSheet) {
      setPickerFloatRect(null);
      return;
    }
    const el = composerFieldWrapRef.current;
    if (!el) {
      setPickerFloatRect(null);
      return;
    }
    const r = el.getBoundingClientRect();
    if (r.width < 8) {
      setPickerFloatRect(null);
      return;
    }
    const maxH = Math.min(260, Math.round(window.innerHeight * 0.42));
    setPickerFloatRect({
      left: r.left,
      width: r.width,
      bottom: window.innerHeight - r.top + 8,
      maxH,
    });
  }, [pickerOpen, pickerUseSheet]);

  useLayoutEffect(() => {
    if (!pickerOpen) {
      setPickerFloatRect(null);
      return;
    }
    if (pickerUseSheet) {
      setPickerFloatRect(null);
      return;
    }
    measurePickerFloat();
    const el = composerFieldWrapRef.current;
    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== "undefined" && el) {
      ro = new ResizeObserver(() => measurePickerFloat());
      ro.observe(el);
    }
    window.addEventListener("resize", measurePickerFloat);
    const onMsgs = () => measurePickerFloat();
    const shellMobile =
      typeof document !== "undefined" &&
      window.matchMedia(shellStackMaxWidthMediaQuery).matches;
    if (shellMobile) {
      window.addEventListener("scroll", onMsgs, { passive: true });
    } else {
      const msgEl =
        typeof document !== "undefined"
          ? document.getElementById("messages")
          : null;
      msgEl?.addEventListener("scroll", onMsgs, { passive: true });
    }
    return () => {
      ro?.disconnect();
      window.removeEventListener("resize", measurePickerFloat);
      if (shellMobile) {
        window.removeEventListener("scroll", onMsgs);
      } else {
        const msgEl =
          typeof document !== "undefined"
            ? document.getElementById("messages")
            : null;
        msgEl?.removeEventListener("scroll", onMsgs);
      }
    };
  }, [pickerOpen, pickerUseSheet, measurePickerFloat, isEmpty]);

  return { pickerUseSheet, sheetBottomPx, pickerFloatRect };
}
