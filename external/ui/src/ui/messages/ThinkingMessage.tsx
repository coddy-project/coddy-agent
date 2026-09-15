import { memo, useEffect, useMemo, useState } from "react";
import { Markdown } from "../markdown/Markdown";
import { useT } from "../i18n/I18nProvider";

function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "";
  if (ms >= 60_000) {
    const mins = ms / 60_000;
    const fixed = mins < 10 ? mins.toFixed(1) : mins.toFixed(0);
    return `${fixed}m`;
  }
  return `${Math.round(ms)}ms`;
}

export const ThinkingMessage = memo(function ThinkingMessage(props: {
  status: "in_progress" | "completed";
  content: string;
  durationMs?: number;
  /** Wall clock ms when reasoning started (live elapsed until completed). */
  startedAtMs?: number;
}) {
  const { t } = useT();
  const inProgress = props.status === "in_progress";
  const label = inProgress
    ? t("messages.thinkingInProgress")
    : t("messages.thinkingCompleted");
  const text = (props.content || "").trim();

  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    if (!inProgress || typeof props.startedAtMs !== "number") return;
    const h = window.setInterval(() => setNowMs(Date.now()), 160);
    return () => window.clearInterval(h);
  }, [inProgress, props.startedAtMs]);

  // Reasoning delivered in a single flush - a model configured with `stream: false`,
  // or a provider that emits the whole block at once - leaves nothing to measure.
  // The row used to print a dash there, which reads as a failure standing next to
  // rows that report milliseconds; the floor of the same scale says "no time worth
  // reporting" in the units the column already uses.
  const durationLabel = useMemo(() => {
    if (props.status === "completed") {
      if (
        typeof props.durationMs === "number" &&
        Number.isFinite(props.durationMs)
      ) {
        return formatDuration(props.durationMs);
      }
      return formatDuration(0);
    }
    if (
      typeof props.startedAtMs === "number" &&
      Number.isFinite(props.startedAtMs)
    ) {
      return formatDuration(Math.max(0, nowMs - props.startedAtMs));
    }
    if (
      typeof props.durationMs === "number" &&
      Number.isFinite(props.durationMs)
    ) {
      return formatDuration(props.durationMs);
    }
    return formatDuration(0);
  }, [props.durationMs, props.startedAtMs, props.status, nowMs]);

  return (
    <div className="thinking-row">
      <details className="thinking-details">
        <summary className="thinking-summary" aria-label={t("messages.thinkingSummaryAriaLabel")}>
          <span className="thinking-left">
            <span className="thinking-chevron" aria-hidden="true" />
            <span className="thinking-label">{label}</span>
            <span className="thinking-dur" aria-hidden="true">
              {durationLabel}
            </span>
          </span>
        </summary>
        {text ? (
          <div className="thinking-body" aria-label={t("messages.thinkingContentAriaLabel")}>
            <Markdown text={text} />
          </div>
        ) : null}
      </details>
    </div>
  );
});
