import {
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
} from "react";

/** Minimum visible rows; editor grows with content, outer panel scrolls only. */
export const MARKDOWN_LINE_EDITOR_MIN_ROWS = 10;

export function MarkdownLineEditor(props: {
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
  "aria-label"?: string;
  placeholder?: string;
}) {
  const rootRef = useRef<HTMLDivElement>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);

  const gutterText = useMemo(() => {
    const n = props.value === "" ? 1 : props.value.split("\n").length;
    return Array.from({ length: n }, (_, i) => String(i + 1)).join("\n");
  }, [props.value]);

  const syncHeight = useCallback(() => {
    const root = rootRef.current;
    const ta = taRef.current;
    if (!root || !ta) return;

    ta.style.height = "0px";
    const cs = getComputedStyle(ta);
    const lhPx = parseFloat(cs.lineHeight);
    const lineHeight = Number.isFinite(lhPx) ? lhPx : 12 * 1.45;
    const padY =
      (parseFloat(cs.paddingTop) || 0) + (parseFloat(cs.paddingBottom) || 0);
    const minTextareaPx = Math.ceil(
      lineHeight * MARKDOWN_LINE_EDITOR_MIN_ROWS + padY,
    );

    const contentPx = ta.scrollHeight;
    const h = Math.max(contentPx, minTextareaPx);

    ta.style.height = `${h}px`;
    root.style.height = `${h}px`;
    root.style.removeProperty("max-height");
  }, []);

  useLayoutEffect(() => {
    syncHeight();
  }, [props.value, props.disabled, syncHeight]);

  useLayoutEffect(() => {
    const onLayout = () => syncHeight();
    window.addEventListener("resize", onLayout);
    const root = rootRef.current;
    let ro: ResizeObserver | undefined;
    if (root && typeof ResizeObserver !== "undefined") {
      ro = new ResizeObserver(onLayout);
      ro.observe(root.parentElement ?? root);
    }
    return () => {
      window.removeEventListener("resize", onLayout);
      ro?.disconnect();
    };
  }, [syncHeight]);

  return (
    <div ref={rootRef} className="md-line-editor">
      <pre className="md-line-editor-gutter" aria-hidden>
        {gutterText}
      </pre>
      <textarea
        ref={taRef}
        className="md-line-editor-textarea"
        value={props.value}
        disabled={props.disabled}
        spellCheck={false}
        placeholder={props.placeholder}
        aria-label={props["aria-label"]}
        onChange={(ev) => props.onChange(ev.target.value)}
      />
    </div>
  );
}
