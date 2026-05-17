import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
} from "react";

import {
  applyMeasureProbeStyles,
  buildGutterRows,
  cursorLineIndex,
  lineHeightPxFromComputed,
  measureAllVisualRows,
  textareaTextWidthPx,
} from "./markdownLineGutter";

/** Default minimum logical rows (scheduler job body). */
export const MARKDOWN_LINE_EDITOR_MIN_ROWS = 10;

export type MarkdownLineEditorProps = {
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
  readOnly?: boolean;
  "aria-label"?: string;
  placeholder?: string;
  /** Pad with blank logical lines up to this count (default 10). */
  minRows?: number;
  /** Extra root class, e.g. `md-line-editor--plan`. */
  className?: string;
  spellCheck?: boolean;
  gutterTestId?: string;
  rootTestId?: string;
};

function logicalLineCount(text: string): number {
  const n = text.split("\n").length;
  return n > 0 ? n : 1;
}

export function MarkdownLineEditor(props: MarkdownLineEditorProps) {
  const minRows = props.minRows ?? MARKDOWN_LINE_EDITOR_MIN_ROWS;
  const rootRef = useRef<HTMLDivElement>(null);
  const gutterRef = useRef<HTMLDivElement>(null);
  const backdropRef = useRef<HTMLDivElement>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const measureRef = useRef<HTMLDivElement>(null);
  const [activeLine, setActiveLine] = useState(0);
  const [visualRows, setVisualRows] = useState<number[]>([1]);
  const [lineHeightPx, setLineHeightPx] = useState(17.4);

  const rawLines = useMemo(() => {
    const lines = props.value.split("\n");
    return lines.length === 0 ? [""] : lines;
  }, [props.value]);

  const logicalCount = Math.max(logicalLineCount(props.value), minRows);
  const gutterWidthCh = Math.max(String(logicalCount).length, 2) + 1;

  const gutterEntries = useMemo(
    () => buildGutterRows(visualRows),
    [visualRows],
  );

  const disabled = props.disabled === true || props.readOnly === true;

  const rootClass = ["md-line-editor", props.className ?? ""]
    .filter(Boolean)
    .join(" ");

  const rootStyle = useMemo(
    () =>
      ({
        "--md-editor-line-px": `${lineHeightPx}px`,
      }) as CSSProperties,
    [lineHeightPx],
  );

  const syncActiveLine = useCallback(() => {
    const ta = taRef.current;
    if (!ta) return;
    setActiveLine(cursorLineIndex(ta.value, ta.selectionStart));
  }, []);

  const syncScroll = useCallback(() => {
    const ta = taRef.current;
    if (!ta) return;
    const top = ta.scrollTop;
    if (gutterRef.current) {
      gutterRef.current.scrollTop = top;
    }
    if (backdropRef.current) {
      backdropRef.current.scrollTop = top;
    }
  }, []);

  const remeasureVisualRows = useCallback(() => {
    const ta = taRef.current;
    const probe = measureRef.current;
    if (!ta || !probe) return;
    const textWidth = textareaTextWidthPx(ta);
    const lh = applyMeasureProbeStyles(probe, ta, textWidth);
    setLineHeightPx(lh);
    const counts = measureAllVisualRows(rawLines, textWidth, lh, probe);
    while (counts.length < minRows) {
      counts.push(1);
    }
    setVisualRows(counts);
  }, [rawLines, minRows]);

  const syncHeight = useCallback(() => {
    const root = rootRef.current;
    const ta = taRef.current;
    if (!root || !ta) return;

    ta.style.height = "0px";
    const cs = getComputedStyle(ta);
    const lhPx = lineHeightPxFromComputed(cs);
    const padY =
      (parseFloat(cs.paddingTop) || 0) + (parseFloat(cs.paddingBottom) || 0);
    const minTextareaPx = Math.ceil(lhPx * minRows + padY);
    const contentPx = ta.scrollHeight;
    const h = Math.max(contentPx, minTextareaPx);

    ta.style.height = `${h}px`;
    root.style.height = `${h}px`;
    root.style.removeProperty("max-height");
  }, [minRows]);

  useLayoutEffect(() => {
    remeasureVisualRows();
    syncHeight();
  }, [props.value, props.disabled, remeasureVisualRows, syncHeight]);

  useLayoutEffect(() => {
    syncActiveLine();
  }, [props.value, syncActiveLine]);

  useEffect(() => {
    const ta = taRef.current;
    if (!ta || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      remeasureVisualRows();
      syncHeight();
    });
    ro.observe(ta);
    return () => ro.disconnect();
  }, [remeasureVisualRows, syncHeight]);

  useEffect(() => {
    const onLayout = () => {
      remeasureVisualRows();
      syncHeight();
    };
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
  }, [remeasureVisualRows, syncHeight]);

  return (
    <div
      ref={rootRef}
      className={rootClass}
      style={rootStyle}
      data-testid={props.rootTestId}
    >
      <div
        ref={gutterRef}
        className="md-line-editor-gutter"
        style={{ width: `${gutterWidthCh}ch` }}
        aria-hidden
        data-testid={props.gutterTestId}
      >
        {gutterEntries.map((entry) => (
          <div
            key={`${entry.logicalLine}-${entry.visualIndex}`}
            className={[
              "md-line-editor-gutter-line",
              entry.logicalLine === activeLine ? "is-active" : "",
              entry.showNumber ? "" : "is-continuation",
            ]
              .filter(Boolean)
              .join(" ")}
          >
            {entry.showNumber ? entry.logicalLine + 1 : ""}
          </div>
        ))}
      </div>
      <div className="md-line-editor-stack">
        <div
          ref={backdropRef}
          className="md-line-editor-backdrop"
          aria-hidden
        >
          {gutterEntries.map((entry) => (
            <div
              key={`hl-${entry.logicalLine}-${entry.visualIndex}`}
              className={[
                "md-line-editor-hl-band",
                entry.logicalLine === activeLine ? "is-current" : "",
              ]
                .filter(Boolean)
                .join(" ")}
            />
          ))}
        </div>
        <textarea
          ref={taRef}
          className="md-line-editor-textarea"
          value={props.value}
          disabled={disabled}
          readOnly={props.readOnly}
          spellCheck={props.spellCheck ?? false}
          placeholder={props.placeholder}
          aria-label={props["aria-label"]}
          onChange={(ev) => {
            props.onChange(ev.target.value);
            syncActiveLine();
          }}
          onScroll={syncScroll}
          onClick={syncActiveLine}
          onKeyUp={syncActiveLine}
          onSelect={syncActiveLine}
          onFocus={syncActiveLine}
        />
        <div
          ref={measureRef}
          className="md-line-editor-hl-line md-line-editor-measure"
          aria-hidden
        />
      </div>
    </div>
  );
}
