import { memo, useEffect, useMemo, useState } from "react";
import { Chevron } from "../components/Chevron";
import { Markdown } from "../markdown/Markdown";
import { useT } from "../i18n/I18nProvider";
import { formatStepDuration } from "./formatStepDuration";

export const ThinkingMessage = memo(function ThinkingMessage(props: {
  status: "in_progress" | "completed";
  /** The transcript row id, stamped on the row so the transcript window can
   *  find it on screen. */
  rowId?: string;
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
  // The body is rendered while the row is open. Every reasoning delta changes the
  // content, and a closed row parsing its whole Markdown again for each one is the
  // work that kept the page busy through a long reasoning block with nothing on
  // screen to show for it.
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!inProgress || typeof props.startedAtMs !== "number") return;
    const h = window.setInterval(() => setNowMs(Date.now()), 160);
    return () => window.clearInterval(h);
  }, [inProgress, props.startedAtMs]);

  // Reasoning delivered in a single flush - a model configured with `stream: false`,
  // or a provider that emits the whole block at once - leaves nothing to measure, and
  // neither does history saved without a length. A dash there read as a failure and
  // "0ms" as a clock that had stopped, so a length nobody measured is not shown.
  const durationLabel = useMemo(() => {
    if (props.status === "completed") {
      if (
        typeof props.durationMs === "number" &&
        Number.isFinite(props.durationMs)
      ) {
        return formatStepDuration(props.durationMs);
      }
      return "";
    }
    if (
      typeof props.startedAtMs === "number" &&
      Number.isFinite(props.startedAtMs)
    ) {
      return formatStepDuration(Math.max(0, nowMs - props.startedAtMs));
    }
    if (
      typeof props.durationMs === "number" &&
      Number.isFinite(props.durationMs)
    ) {
      return formatStepDuration(props.durationMs);
    }
    return formatStepDuration(0);
  }, [props.durationMs, props.startedAtMs, props.status, nowMs]);

  return (
    <div className="thinking-row" data-row-id={props.rowId}>
      <details
        className="thinking-details"
        onToggle={(e) => setOpen(e.currentTarget.open)}
      >
        <summary className="thinking-summary" aria-label={t("messages.thinkingSummaryAriaLabel")}>
          <span className="thinking-left">
            <Chevron className="thinking-chevron" />
            <span className="thinking-label">{label}</span>
            {durationLabel ? (
              <span className="thinking-dur" aria-hidden="true">
                {durationLabel}
              </span>
            ) : null}
          </span>
        </summary>
        {open && text ? (
          <div className="thinking-body" aria-label={t("messages.thinkingContentAriaLabel")}>
            <Markdown text={text} />
          </div>
        ) : null}
      </details>
    </div>
  );
});
