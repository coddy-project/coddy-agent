import { useEffect, useRef } from "react";

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
  label: string;
  cssVar: string;
}[] = [
  { key: "systemPrompt", label: "System prompt", cssVar: "--ctx-seg-system" },
  { key: "toolDefinitions", label: "Tool definitions", cssVar: "--ctx-seg-tools" },
  { key: "rules", label: "Rules", cssVar: "--ctx-seg-rules" },
  { key: "skills", label: "Skills", cssVar: "--ctx-seg-skills" },
  { key: "mcp", label: "MCP", cssVar: "--ctx-seg-mcp" },
  { key: "subagents", label: "Subagents", cssVar: "--ctx-seg-subagents" },
  { key: "conversation", label: "Conversation", cssVar: "--ctx-seg-conversation" },
];

function fmtInt(n: number | undefined): string {
  if (typeof n !== "number" || !Number.isFinite(n)) return "0";
  return Math.max(0, Math.trunc(n)).toLocaleString("en-US");
}

export function ContextBreakdownPopover(props: {
  open: boolean;
  onClose: () => void;
  contextIdle?: boolean;
  contextPct?: number | null;
  maxContextTokens: number;
  breakdown?: ContextBreakdown | null;
}) {
  const panelRef = useRef<HTMLDivElement | null>(null);

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
    if (!props.open) {
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
      const host = document.querySelector(".composer-context-tip-host");
      if (host?.contains(t)) {
        return;
      }
      props.onClose();
    };
    window.addEventListener("mousedown", onPointer);
    return () => window.removeEventListener("mousedown", onPointer);
  }, [props.open, props.onClose]);

  if (!props.open) {
    return null;
  }

  const idle = props.contextIdle === true;
  const pct =
    typeof props.contextPct === "number" && Number.isFinite(props.contextPct)
      ? props.contextPct
      : 0;
  const maxCtx = props.maxContextTokens > 0 ? props.maxContextTokens : 128000;
  const b = props.breakdown;
  const rows = SEGMENTS.map((s) => ({
    ...s,
    tokens: b ? Math.max(0, b[s.key] || 0) : 0,
  })).filter((r) => r.key !== "subagents" || r.tokens > 0);
  const totalFromParts = rows.reduce((sum, r) => sum + r.tokens, 0);
  const used = b?.estimatedTotal && b.estimatedTotal > 0 ? b.estimatedTotal : totalFromParts;
  const barTotal = Math.max(used, 1);

  return (
    <div
      ref={panelRef}
      className="context-breakdown-popover"
      role="dialog"
      aria-label="Context"
      data-testid="context-breakdown-popover"
    >
      <div className="context-breakdown-head">
        <span className="context-breakdown-title">Context</span>
        <button
          type="button"
          className="context-breakdown-close"
          aria-label="Close"
          data-testid="context-breakdown-close"
          onClick={() => props.onClose()}
        >
          ×
        </button>
      </div>
      <div className="context-breakdown-summary">
        <span>{idle ? "0.0" : pct.toFixed(1)}% Full</span>
        <span className="context-breakdown-summary-sep">·</span>
        <span>
          ~{fmtInt(used)} / {fmtInt(maxCtx)} Tokens
        </span>
      </div>
      {idle || used === 0 ? (
        <p className="context-breakdown-empty">No context usage yet</p>
      ) : (
        <>
          <div
            className="context-breakdown-bar"
            role="img"
            aria-label="Context breakdown bar"
            data-testid="context-breakdown-bar"
          >
            {rows.map((r) =>
              r.tokens > 0 ? (
                <span
                  key={r.key}
                  className="context-breakdown-seg"
                  data-segment={r.key}
                  style={{
                    flexGrow: r.tokens,
                    background: `var(${r.cssVar})`,
                  }}
                  title={`${r.label}: ${fmtInt(r.tokens)}`}
                />
              ) : null,
            )}
          </div>
          <ul className="context-breakdown-legend">
            {rows.map((r) => (
              <li key={r.key} data-testid={`context-breakdown-row-${r.key}`}>
                <span className="context-breakdown-swatch" style={{ background: `var(${r.cssVar})` }} />
                <span className="context-breakdown-label">{r.label}</span>
                <span className="context-breakdown-value">{fmtInt(r.tokens)}</span>
              </li>
            ))}
          </ul>
        </>
      )}
      <span className="sr-only">Bar segments sum to {fmtInt(barTotal)} estimated tokens</span>
    </div>
  );
}
