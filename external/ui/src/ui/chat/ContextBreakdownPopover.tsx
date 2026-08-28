import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";

export type ContextBreakdown = {
  systemPrompt: number;
  toolDefinitions: number;
  rules: number;
  skills: number;
  mcp: number;
  subagents: number;
  conversation: number;
  estimatedTotal: number;
};

type SegmentKey = keyof Omit<ContextBreakdown, "estimatedTotal">;

const SEGMENTS: {
  key: SegmentKey;
  labelKey: string;
  cssVar: string;
}[] = [
  { key: "systemPrompt", labelKey: "chat.contextSegment.systemPrompt", cssVar: "--ctx-seg-system" },
  { key: "toolDefinitions", labelKey: "chat.contextSegment.toolDefinitions", cssVar: "--ctx-seg-tools" },
  { key: "rules", labelKey: "chat.contextSegment.rules", cssVar: "--ctx-seg-rules" },
  { key: "skills", labelKey: "chat.contextSegment.skills", cssVar: "--ctx-seg-skills" },
  { key: "mcp", labelKey: "chat.contextSegment.mcp", cssVar: "--ctx-seg-mcp" },
  { key: "subagents", labelKey: "chat.contextSegment.subagents", cssVar: "--ctx-seg-subagents" },
  { key: "conversation", labelKey: "chat.contextSegment.conversation", cssVar: "--ctx-seg-conversation" },
];

function fmtInt(n: number | undefined): string {
  if (typeof n !== "number" || !Number.isFinite(n)) return "0";
  return Math.max(0, Math.trunc(n)).toLocaleString("en-US");
}

export type ContextPopoverFloatRect = {
  left: number;
  width: number;
  bottom: number;
};

export function ContextBreakdownPopover(props: {
  open: boolean;
  onClose: () => void;
  /** Stacked-shell viewports use a bottom sheet above the composer. */
  useSheet?: boolean;
  /** When set (px from viewport bottom), sheet sits above docked composer instead of screen bottom. */
  sheetBottomPx?: number | null;
  /** Docked chat composer (not hero home). */
  composerDocked?: boolean;
  /** Anchor for desktop floating position (context ring host). */
  anchorRef?: RefObject<HTMLElement | null>;
  contextIdle?: boolean;
  contextPct?: number | null;
  maxContextTokens: number;
  breakdown?: ContextBreakdown | null;
}) {
  const { t } = useT();
  const panelRef = useRef<HTMLDivElement | null>(null);
  const [floatRect, setFloatRect] = useState<ContextPopoverFloatRect | null>(
    null,
  );
  const useSheet = props.useSheet === true;

  const measureFloat = () => {
    if (useSheet || !props.open) {
      setFloatRect(null);
      return;
    }
    const el = props.anchorRef?.current;
    if (!el) {
      setFloatRect(null);
      return;
    }
    const r = el.getBoundingClientRect();
    if (r.width < 4) {
      setFloatRect(null);
      return;
    }
    const width = Math.min(320, Math.max(240, window.innerWidth - 24));
    const left = Math.max(
      12,
      Math.min(r.right - width, window.innerWidth - width - 12),
    );
    setFloatRect({
      left,
      width,
      bottom: window.innerHeight - r.top + 10,
    });
  };

  useLayoutEffect(() => {
    if (!props.open) {
      setFloatRect(null);
      return;
    }
    measureFloat();
    if (useSheet) {
      return;
    }
    window.addEventListener("resize", measureFloat);
    window.addEventListener("scroll", measureFloat, { passive: true });
    return () => {
      window.removeEventListener("resize", measureFloat);
      window.removeEventListener("scroll", measureFloat);
    };
  }, [props.open, useSheet, props.anchorRef]);

  useEffect(() => {
    if (!props.open) {
      return;
    }
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        ev.preventDefault();
        props.onClose();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [props.open, props.onClose]);

  useEffect(() => {
    if (!props.open || useSheet) {
      return;
    }
    const onPointer = (ev: MouseEvent) => {
      const t = ev.target as Node | null;
      if (!t) {
        return;
      }
      if (panelRef.current?.contains(t)) {
        return;
      }
      const host = props.anchorRef?.current;
      if (host?.contains(t)) {
        return;
      }
      props.onClose();
    };
    window.addEventListener("mousedown", onPointer);
    return () => window.removeEventListener("mousedown", onPointer);
  }, [props.open, props.onClose, useSheet, props.anchorRef]);

  if (!props.open) {
    return null;
  }

  const idle = props.contextIdle === true;
  const maxCtx = props.maxContextTokens > 0 ? props.maxContextTokens : 128000;
  const b = props.breakdown;
  const rows = SEGMENTS.map((s) => ({
    ...s,
    label: t(s.labelKey),
    tokens: b ? Math.max(0, b[s.key] || 0) : 0,
  })).filter((r) => r.key !== "subagents" || r.tokens > 0);
  const legendRows = SEGMENTS.filter((s) => s.key !== "subagents").map((s) => ({
    ...s,
    label: t(s.labelKey),
    tokens: b ? Math.max(0, b[s.key] || 0) : 0,
  }));
  const totalFromParts = rows.reduce((sum, r) => sum + r.tokens, 0);
  const used = b?.estimatedTotal && b.estimatedTotal > 0 ? b.estimatedTotal : totalFromParts;
  const showEmptyState = idle || used === 0;
  const displayRows = showEmptyState ? legendRows : rows;
  const fillPct =
    maxCtx > 0 ? Math.min(100, Math.max(0, (used / maxCtx) * 100)) : 0;
  const usedRows = displayRows.filter((r) => r.tokens > 0);
  const sheetBottom =
    useSheet && typeof props.sheetBottomPx === "number"
      ? props.sheetBottomPx
      : 0;

  const body = (
    <>
      {useSheet ? (
        <div className="slash-menu-title">{t("chat.contextTitle")}</div>
      ) : (
        <div className="context-breakdown-head">
          <span className="context-breakdown-title">{t("chat.contextTitle")}</span>
          <button
            type="button"
            className="context-breakdown-close"
            aria-label={t("chat.contextClose")}
            data-testid="context-breakdown-close"
            onClick={() => props.onClose()}
          >
            ×
          </button>
        </div>
      )}
      <div className="context-breakdown-summary">
        <span>{t("chat.contextPercentUsed", { percent: idle ? "0.0" : fillPct.toFixed(1) })}</span>
        <span className="context-breakdown-summary-sep">·</span>
        <span>
          {t("chat.contextTokensSummary", { used: fmtInt(used), max: fmtInt(maxCtx) })}
        </span>
      </div>
      {showEmptyState ? (
        <p className="context-breakdown-empty">{t("chat.contextEmpty")}</p>
      ) : null}
      <div
        className="context-meter-track"
        role="img"
        aria-label={t("chat.contextMeterAriaLabel", {
          percent: fillPct.toFixed(1),
        })}
        data-testid="context-breakdown-bar"
      >
        <div
          className="context-meter-fill"
          style={{ width: `${fillPct}%` }}
          data-testid="context-meter-fill"
        >
          {usedRows.length > 0
            ? usedRows.map((r) => (
                <span
                  key={r.key}
                  className="context-meter-seg"
                  data-segment={r.key}
                  style={{
                    flexGrow: r.tokens,
                    background: `var(${r.cssVar})`,
                  }}
                  title={t("chat.contextSegmentTooltip", {
                    label: r.label,
                    count: fmtInt(r.tokens),
                  })}
                />
              ))
            : null}
        </div>
      </div>
      <ul className="context-breakdown-legend">
        {displayRows.map((r) => (
          <li key={r.key} data-testid={`context-breakdown-row-${r.key}`}>
            <span
              className="context-breakdown-swatch"
              style={{ background: `var(${r.cssVar})` }}
            />
            <span className="context-breakdown-label">{r.label}</span>
            <span className="context-breakdown-value">{fmtInt(r.tokens)}</span>
          </li>
        ))}
      </ul>
    </>
  );

  const menuStyle: CSSProperties | undefined = useSheet
    ? {
        bottom: sheetBottom,
        ...(props.composerDocked && sheetBottom > 0
          ? { ["--context-sheet-bottom" as string]: `${sheetBottom}px` }
          : {}),
      }
    : floatRect
      ? {
          left: floatRect.left,
          width: floatRect.width,
          bottom: floatRect.bottom,
        }
      : undefined;

  const menu = (
    <div
      ref={panelRef}
      className={[
        "context-breakdown-menu",
        useSheet ? "context-breakdown-menu--sheet" : "context-breakdown-menu--portal",
        useSheet && props.composerDocked ? "context-breakdown-menu--above-composer" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      role="dialog"
      aria-label={t("chat.contextTitle")}
      data-testid="context-breakdown-popover"
      style={menuStyle}
    >
      <div className="slash-menu-surface" aria-hidden />
      <div className="context-breakdown-menu-scroll">{body}</div>
    </div>
  );

  return createPortal(
    useSheet ? (
      <>
        <button
          type="button"
          className="slash-sheet-backdrop"
          aria-label={t("chat.contextCloseBreakdown")}
          tabIndex={-1}
          data-testid="context-breakdown-backdrop"
          onMouseDown={(e) => {
            e.preventDefault();
            props.onClose();
          }}
        />
        {menu}
      </>
    ) : (
      menu
    ),
    document.body,
  );
}
