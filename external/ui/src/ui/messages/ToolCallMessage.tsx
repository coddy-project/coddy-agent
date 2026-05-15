import {
  type ReactElement,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";

import {
  parseQuestionToolAnswersFromResult,
  parseQuestionToolQuestionsFromArgs,
} from "../chat/questionToolDisplay";

function safePrettyJSON(text: string): string {
  try {
    const v = JSON.parse(text);
    return JSON.stringify(v, null, 2);
  } catch {
    return text;
  }
}

function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "";
  if (ms >= 60_000) {
    const mins = ms / 60_000;
    const fixed = mins < 10 ? mins.toFixed(1) : mins.toFixed(0);
    return `${fixed}m`;
  }
  return `${Math.round(ms)}ms`;
}

function QuestionToolTimelineReadout(props: {
  argsText?: string | undefined;
  resultText: string;
  status: string;
}) {
  const qs = parseQuestionToolQuestionsFromArgs(props.argsText);
  const terminal = ["completed", "failed", "cancelled"].includes(
    (props.status || "").toLowerCase(),
  );
  const answers = parseQuestionToolAnswersFromResult(props.resultText);

  if (qs.length === 0) {
    return (
      <p className="muted" style={{ margin: 0, fontSize: 13, lineHeight: 1.45 }}>
        Answer using the Questions card in this chat. This row only mirrors the
        tool state.
      </p>
    );
  }

  return (
    <div
      className="question-prompt-resolved-body"
      aria-label="Question tool timeline"
    >
      {qs.map((item, qi) => (
        <div
          key={`${qi}-${item.question}`}
          className={qi === 0 ? undefined : "question-prompt-resolved-block"}
        >
          <div className="question-prompt-resolved-pair">
            <div className="question-prompt-resolved-q">{item.question}</div>
            {terminal && (answers[qi] ?? []).filter(Boolean).length ? (
              <div className="question-prompt-resolved-a">
                {answers[qi]!.join(", ")}
              </div>
            ) : (
              <div className="question-prompt-resolved-a muted">
                Awaiting answer
              </div>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

export function ToolCallMessage(props: {
  toolCallId: string;
  title?: string | undefined;
  kind?: string | undefined;
  status: string;
  argsText?: string | undefined;
  resultText?: string | undefined;
  fullResultText?: string | undefined;
  resultWasTruncated?: boolean | undefined;
  durationMs?: number;
  /** Wall-clock start for live elapsed while pending/in_progress. */
  startedAtMs?: number;
  onFetchToolCallFull?: (toolCallId: string) => Promise<void>;
}) {
  const args = useMemo(
    () => (props.argsText ? safePrettyJSON(props.argsText) : ""),
    [props.argsText],
  );
  const preview = useMemo(
    () => (props.resultText ? props.resultText : ""),
    [props.resultText],
  );
  const full = props.fullResultText || "";
  const rawName = (props.title || props.kind || "tool").trim();
  const status = (props.status || "").toLowerCase();
  const pendingLike = status === "pending" || status === "in_progress";

  const isQuestionTool =
    rawName.toLowerCase() === "question" ||
    (props.kind || "").toLowerCase() === "question";

  const displayLabel = useMemo(() => {
    if (isQuestionTool) {
      return "question";
    }
    return pendingLike ? `${rawName || "tool"}...` : rawName || "tool";
  }, [isQuestionTool, pendingLike, rawName]);

  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    if (isQuestionTool) return;
    if (!pendingLike || typeof props.startedAtMs !== "number") return;
    const h = window.setInterval(() => setNowMs(Date.now()), 160);
    return () => window.clearInterval(h);
  }, [isQuestionTool, pendingLike, props.startedAtMs]);

  const durationLabel = useMemo(() => {
    if (isQuestionTool) {
      return "";
    }
    const terminal =
      status === "completed" || status === "failed" || status === "cancelled";
    if (terminal) {
      if (
        typeof props.durationMs === "number" &&
        Number.isFinite(props.durationMs) &&
        props.durationMs >= 0
      ) {
        return formatDuration(props.durationMs);
      }
      return "-";
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
    return "-";
  }, [isQuestionTool, props.durationMs, props.startedAtMs, props.status, nowMs]);

  const [showExpanded, setShowExpanded] = useState(false);
  const [loadingFull, setLoadingFull] = useState(false);

  useEffect(() => {
    setShowExpanded(false);
    setLoadingFull(false);
  }, [props.toolCallId]);

  const canExpand =
    !isQuestionTool &&
    props.resultWasTruncated === true &&
    (status === "completed" || status === "failed" || status === "cancelled");
  const fetchFull = props.onFetchToolCallFull;

  const onLoadMore = useCallback(async () => {
    if (!fetchFull) return;
    if (full) {
      setShowExpanded(true);
      return;
    }
    setLoadingFull(true);
    try {
      await fetchFull(props.toolCallId);
      setShowExpanded(true);
    } finally {
      setLoadingFull(false);
    }
  }, [fetchFull, full, props.toolCallId]);

  const onHide = useCallback(() => setShowExpanded(false), []);

  const resultBody = showExpanded && full ? full : preview;
  const useTallViewport =
    props.resultWasTruncated === true || (showExpanded && full.trim() !== "");

  const showToggleRow = canExpand && !!fetchFull && !!(preview || full);
  let toggleLink: ReactElement | null = null;
  if (showToggleRow) {
    if (showExpanded && full) {
      toggleLink = (
        <button
          type="button"
          className="tool-result-text-link"
          data-testid="tool-result-hide-link"
          onClick={(e) => {
            e.preventDefault();
            onHide();
          }}
        >
          Hide
        </button>
      );
    } else {
      toggleLink = (
        <button
          type="button"
          className="tool-result-text-link"
          data-testid="tool-result-more-link"
          disabled={loadingFull}
          onClick={(e) => {
            e.preventDefault();
            void onLoadMore();
          }}
        >
          {loadingFull ? "Loading..." : "Load more results"}
        </button>
      );
    }
  }

  const viewportMode = showExpanded && full ? "scroll" : "clip";

  const showJsonArgs = !!args && !isQuestionTool;
  const showJsonResult =
    !isQuestionTool && !!(resultBody && resultBody.length > 0);
  const hasBody =
    isQuestionTool ||
    showJsonArgs ||
    showJsonResult ||
    !!toggleLink;

  return (
    <div
      className="thinking-row coddy-tool-call-row"
      data-kind={props.kind || ""}
      data-status={props.status}
    >
      <details
        className="thinking-details coddy-tool-details"
        data-testid={`tool-details-${props.toolCallId}`}
      >
        <summary className="thinking-summary" aria-label="Tool summary">
          <span className="thinking-left">
            <span className="thinking-chevron" aria-hidden="true" />
            <span className="thinking-label">{displayLabel}</span>
            {durationLabel.trim() !== "" ? (
              <span className="thinking-dur" aria-hidden="true">
                {durationLabel}
              </span>
            ) : null}
          </span>
        </summary>
        {hasBody ? (
          <div
            className="thinking-body coddy-tool-call-body"
            aria-label="Tool call details"
          >
            {isQuestionTool ? (
              <QuestionToolTimelineReadout
                argsText={props.argsText}
                resultText={resultBody}
                status={props.status}
              />
            ) : null}
            {showJsonArgs ? (
              <pre className="tool-block" aria-label="Tool arguments">
                {args}
              </pre>
            ) : null}
            {showJsonResult ? (
              <div
                className={[
                  "tool-block tool-result tool-result-raw",
                  useTallViewport &&
                    `tool-result-viewport tool-result-viewport--tall tool-result-viewport--${viewportMode}`,
                ]
                  .filter(Boolean)
                  .join(" ")}
                aria-label="Tool result"
              >
                <pre className="tool-result-pre">{resultBody}</pre>
              </div>
            ) : null}
            {toggleLink ? (
              <div className="tool-result-toggle-row">{toggleLink}</div>
            ) : null}
          </div>
        ) : null}
      </details>
    </div>
  );
}
