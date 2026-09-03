import { useEffect, useMemo, useState } from "react";
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

function SummaryHeader(props: {
  label: string;
  durationLabel: string;
  showChevron: boolean;
}) {
  return (
    <span className="thinking-left coddy-memory-copilot-head-left">
      {props.showChevron ? (
        <span className="thinking-chevron" aria-hidden="true" />
      ) : null}
      <span className="thinking-label">{props.label}</span>
      <span className="thinking-dur" aria-hidden="true">
        {props.durationLabel}
      </span>
    </span>
  );
}

/** One before-main-agent memory pass. `memoryText` is the streamed context for the main agent; legacy rows use recallText / persistText. */
export function MemoryCopilotMessage(props: {
  mainThinkingInProgress?: boolean;
  memoryStatus?: "idle" | "in_progress" | "completed";
  memoryText?: string;
  recallStatus: "idle" | "in_progress" | "completed";
  persistStatus: "idle" | "in_progress" | "completed";
  recallText: string;
  persistText: string;
  recallDurationMs?: number;
  persistDurationMs?: number;
  memoryWallStartedAtMs?: number;
  memoryWallLiveCapMs?: number;
  memoryWallDurationMs?: number;
  persistSaved?: boolean;
  persistSavedBody?: string;
  persistRelativePath?: string;
  persistTitle?: string;
  recallReadPaths?: string[];
}) {
  const { t } = useT();
  const memSt = props.memoryStatus;
  const legacyBusy =
    props.recallStatus === "in_progress" ||
    props.persistStatus === "in_progress";
  const busy = memSt === "in_progress" || (memSt == null && legacyBusy);

  const streamingBody =
    props.memoryText !== undefined
      ? props.memoryText
      : [props.recallText, props.persistText]
          .filter((x) => x && x.length > 0)
          .join("\n\n");

  const savedBodyTrim = (props.persistSavedBody || "").trim();
  const hasStructuredSave = !!(props.persistSaved && savedBodyTrim);

  /** Wait for first streamed text token (tools may run with no visible prose yet). */
  const awaitingFirstText = busy && streamingBody.length === 0;

  const label = busy
    ? t("messages.memoryInProgress")
    : t("messages.memoryCompleted");

  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    if (!busy || typeof props.memoryWallStartedAtMs !== "number") return;
    const h = window.setInterval(() => setNowMs(Date.now()), 160);
    return () => window.clearInterval(h);
  }, [busy, props.memoryWallStartedAtMs]);

  /** Controlled so React re-renders do not reset native <details> open state; default closed unless user toggles. */
  const [memoryDetailsOpen, setMemoryDetailsOpen] = useState(false);

  const durationLabel = useMemo(() => {
    if (
      typeof props.memoryWallDurationMs === "number" &&
      Number.isFinite(props.memoryWallDurationMs)
    ) {
      return formatDuration(props.memoryWallDurationMs);
    }
    if (
      props.mainThinkingInProgress &&
      typeof props.memoryWallLiveCapMs === "number" &&
      Number.isFinite(props.memoryWallLiveCapMs) &&
      props.memoryWallLiveCapMs >= 0
    ) {
      return formatDuration(props.memoryWallLiveCapMs);
    }
    if (!busy && (props.recallDurationMs || props.persistDurationMs)) {
      const sum =
        (props.recallDurationMs ?? 0) + (props.persistDurationMs ?? 0);
      return formatDuration(sum);
    }
    if (
      busy &&
      typeof props.memoryWallStartedAtMs === "number" &&
      Number.isFinite(props.memoryWallStartedAtMs)
    ) {
      const rawEl = Math.max(0, nowMs - props.memoryWallStartedAtMs);
      if (
        typeof props.memoryWallLiveCapMs === "number" &&
        Number.isFinite(props.memoryWallLiveCapMs) &&
        props.memoryWallLiveCapMs >= 0
      ) {
        return formatDuration(Math.min(rawEl, props.memoryWallLiveCapMs));
      }
      return formatDuration(rawEl);
    }
    return "-";
  }, [
    busy,
    nowMs,
    props.mainThinkingInProgress,
    props.memoryWallDurationMs,
    props.memoryWallLiveCapMs,
    props.memoryWallStartedAtMs,
    props.persistDurationMs,
    props.recallDurationMs,
  ]);

  const displayTrim = streamingBody.trim();

  const finished =
    memSt === "completed" ||
    (props.recallStatus === "completed" &&
      props.persistStatus !== "in_progress");
  const emptyCompleted =
    finished &&
    !busy &&
    displayTrim === "" &&
    !props.persistSaved &&
    !hasStructuredSave;

  const hideRow =
    props.recallStatus === "idle" &&
    props.persistStatus === "idle" &&
    (memSt === "idle" || memSt === undefined) &&
    displayTrim === "" &&
    !hasStructuredSave &&
    !props.persistSaved;

  if (hideRow && !busy) {
    return null;
  }

  if (awaitingFirstText) {
    return (
      <div className="thinking-row" data-testid="memory-copilot-row">
        <div
          className="thinking-summary coddy-memory-copilot-flat-banner"
          aria-label={t("messages.memoryInProgressAriaLabel")}
          aria-busy="true"
        >
          <SummaryHeader
            label={label}
            durationLabel={durationLabel}
            showChevron={false}
          />
        </div>
      </div>
    );
  }

  return (
    <div className="thinking-row">
      <details
        className="thinking-details coddy-memory-copilot-details"
        data-testid="memory-copilot-row"
        open={memoryDetailsOpen}
        onToggle={(e) => setMemoryDetailsOpen(e.currentTarget.open)}
      >
        <summary
          className="thinking-summary"
          aria-label={t("messages.memorySummaryAriaLabel")}
        >
          <SummaryHeader
            label={label}
            durationLabel={durationLabel}
            showChevron={true}
          />
        </summary>
        <div
          className="thinking-body coddy-memory-copilot-body"
          aria-label={t("messages.memoryContentAriaLabel")}
        >
          {displayTrim ? (
            <div className="coddy-memory-phase coddy-memory-recall">
              <div className="coddy-memory-stream md-wrap coddy-memory-for-agent">
                <Markdown text={streamingBody} />
              </div>
            </div>
          ) : null}
          {hasStructuredSave ? (
            <div className="coddy-memory-phase coddy-memory-persist">
              <div className="coddy-memory-saved-summary">
                {(props.persistTitle || "").trim() ? (
                  <span className="coddy-memory-saved-title">
                    {(props.persistTitle || "").trim()}
                  </span>
                ) : null}
                {props.persistRelativePath ? (
                  <span className="coddy-memory-path">
                    {props.persistRelativePath}
                  </span>
                ) : null}
              </div>
              <div className="coddy-memory-stream md-wrap">
                <Markdown text={savedBodyTrim} />
              </div>
            </div>
          ) : null}
          {!hasStructuredSave && props.persistSaved ? (
            <p className="coddy-memory-saved">
              {t("messages.memoryMarkedSaved", {
                title:
                  (props.persistTitle || "").trim() ||
                  t("messages.memoryMarkedSavedDefaultTitle"),
              })}
            </p>
          ) : null}
          {emptyCompleted ? (
            <p className="coddy-memory-empty">
              {t("messages.memoryEmpty")}
            </p>
          ) : null}
        </div>
      </details>
    </div>
  );
}
