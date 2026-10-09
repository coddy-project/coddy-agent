import { useCallback, useLayoutEffect, useRef } from "react";
import { middleTruncate } from "./workspaceContext";

/** The fewest characters a cut name keeps: a few of each end and the dots. */
const MIN_CHARS = 5;

/**
 * Text shown whole while its host has room for it, and cut in the middle
 * (middleTruncate, both ends kept) to the longest form that fits when it has
 * not. The host is the element the span sits in, a flex item that shrinks to
 * the room it is given (min-width: 0, overflow hidden, one line): the text
 * fits while the host's scroll width is not above its client width. It is
 * fitted again on every render, when the host or its row changes size and
 * when the window does. The span's text is written here, not by React.
 */
export function FitMiddleText(props: { text: string; className?: string }) {
  const ref = useRef<HTMLSpanElement | null>(null);
  const textRef = useRef(props.text);
  textRef.current = props.text;

  const fit = useCallback(() => {
    const el = ref.current;
    const host = el?.parentElement;
    if (!el || !host) return;
    const full = textRef.current;
    const fits = () => host.scrollWidth <= host.clientWidth;
    el.textContent = full;
    if (fits() || full.length <= MIN_CHARS) return;
    let best = middleTruncate(full, MIN_CHARS);
    let lo = MIN_CHARS + 1;
    let hi = full.length - 1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      const cut = middleTruncate(full, mid);
      el.textContent = cut;
      if (fits()) {
        best = cut;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    el.textContent = best;
  }, []);

  useLayoutEffect(() => {
    fit();
  });

  useLayoutEffect(() => {
    const host = ref.current?.parentElement;
    if (!host) return;
    const observer =
      typeof ResizeObserver === "undefined" ? null : new ResizeObserver(fit);
    observer?.observe(host);
    if (host.parentElement) observer?.observe(host.parentElement);
    window.addEventListener("resize", fit);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", fit);
    };
  }, [fit]);

  return <span ref={ref} className={props.className} />;
}
