import {
  type ReactElement,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  parseQuestionToolAnswersFromResult,
  parseQuestionToolQuestionsFromArgs,
} from "../chat/questionToolDisplay";
import { PermissionToolPreview } from "../chat/PermissionPromptPreview";
import { buildToolCallPreview } from "../chat/permissionToolPreview";

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
      <p
        className="muted"
        style={{ margin: 0, fontSize: 13, lineHeight: 1.45 }}
      >
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
  /** When true, wall-clock label stops (e.g. awaiting permission). */
  permissionWaiting?: boolean;
  onFetchToolCallFull?: (toolCallId: string) => Promise<void>;
}) {
  const preview = useMemo(
    () => (props.resultText ? props.resultText : ""),
    [props.resultText],
  );
  const full = props.fullResultText || "";
  const rawName = (props.title || props.kind || "tool").trim();
  const toolPreview = useMemo(
    () =>
      buildToolCallPreview(
        {
          title: props.title,
          kind: props.kind,
          argsText: props.argsText,
        },
        props.argsText || "",
      ),
    [props.argsText, props.kind, props.title],
  );
  const status = (props.status || "").toLowerCase();
  const pendingLike = status === "pending" || status === "in_progress";

  const isQuestionTool =
    rawName.toLowerCase() === "question" ||
    (props.kind || "").toLowerCase() === "question";

  const isPatchTool = rawName.toLowerCase() === "apply_patch";

  const patchContent = useMemo(() => {
    if (!isPatchTool || !props.argsText) return null;
    try {
      const parsed = JSON.parse(props.argsText) as Record<string, unknown>;
      return typeof parsed.patch === "string"
        ? parsed.patch
        : typeof parsed.diff === "string"
          ? parsed.diff
          : null;
    } catch {
      return null;
    }
  }, [isPatchTool, props.argsText]);

  const displayLabel = useMemo(() => {
    if (isQuestionTool) {
      return "question";
    }
    return pendingLike ? `${rawName || "tool"}...` : rawName || "tool";
  }, [isQuestionTool, pendingLike, rawName]);

  const permissionWaiting = props.permissionWaiting === true;

  const [nowMs, setNowMs] = useState(() => Date.now());
  const [frozenElapsedMs, setFrozenElapsedMs] = useState<number | null>(null);

  useEffect(() => {
    if (!permissionWaiting) {
      setFrozenElapsedMs(null);
      return;
    }
    if (typeof props.startedAtMs !== "number") {
      return;
    }
    setFrozenElapsedMs(Math.max(0, Date.now() - props.startedAtMs));
  }, [permissionWaiting, props.startedAtMs, props.toolCallId]);

  useEffect(() => {
    if (isQuestionTool || permissionWaiting) return;
    if (!pendingLike || typeof props.startedAtMs !== "number") return;
    const h = window.setInterval(() => setNowMs(Date.now()), 160);
    return () => window.clearInterval(h);
  }, [isQuestionTool, permissionWaiting, pendingLike, props.startedAtMs]);

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
    if (permissionWaiting && frozenElapsedMs !== null) {
      return formatDuration(frozenElapsedMs);
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
  }, [
    frozenElapsedMs,
    isQuestionTool,
    permissionWaiting,
    props.durationMs,
    props.startedAtMs,
    props.status,
    nowMs,
  ]);

  const [showExpanded, setShowExpanded] = useState(false);
  const [loadingFull, setLoadingFull] = useState(false);

  useEffect(() => {
    setShowExpanded(false);
    setLoadingFull(false);
  }, [props.toolCallId]);

  // Auto-fetch full args for patch tools. argsPreview from the sessions list is truncated
  // (200 chars) which makes the JSON unparseable; we need the full args to render the diff.
  const fetchFn = props.onFetchToolCallFull;
  const fetchAttemptedRef = useRef(false);
  useEffect(() => {
    fetchAttemptedRef.current = false;
  }, [props.toolCallId]);
  useEffect(() => {
    if (!isPatchTool || !fetchFn || patchContent || fetchAttemptedRef.current)
      return;
    fetchAttemptedRef.current = true;
    void fetchFn(props.toolCallId);
  }, [isPatchTool, patchContent, props.toolCallId, fetchFn]);

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
  let toggleButton: ReactElement | null = null;
  if (showToggleRow) {
    if (showExpanded && full) {
      toggleButton = (
        <button
          type="button"
          className="tool-overflow-toggle"
          data-testid="tool-result-less"
          onClick={(e) => {
            e.preventDefault();
            onHide();
          }}
        >
          Less
        </button>
      );
    } else {
      toggleButton = (
        <button
          type="button"
          className="tool-overflow-toggle"
          data-testid="tool-result-more"
          disabled={loadingFull}
          onClick={(e) => {
            e.preventDefault();
            void onLoadMore();
          }}
        >
          {loadingFull ? "Loading…" : "More…"}
        </button>
      );
    }
  }

  const viewportMode = showExpanded && full ? "scroll" : "clip";

  const toolPreviewHasContent =
    toolPreview.header.trim() !== "" ||
    toolPreview.meta.length > 0 ||
    toolPreview.copyText.trim() !== "" ||
    (toolPreview.kind === "diff" && toolPreview.lines.length > 0) ||
    (toolPreview.kind === "move" &&
      (toolPreview.sourcePath.trim() !== "" ||
        toolPreview.destinationPath.trim() !== ""));
  const showToolPreview = !isQuestionTool && toolPreviewHasContent;
  const showPatchResult =
    isPatchTool &&
    !!resultBody &&
    !resultBody.trim().toLowerCase().startsWith("patch applied successfully");
  const showResult =
    !isQuestionTool && !isPatchTool && !!(resultBody && resultBody.length > 0);
  const hasConnectedResult = showToolPreview && (showPatchResult || showResult);
  const hasBody =
    isQuestionTool ||
    showToolPreview ||
    showPatchResult ||
    showResult ||
    !!toggleButton;

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
            className={[
              "thinking-body coddy-tool-call-body",
              isQuestionTool && "coddy-tool-call-body--question",
              hasConnectedResult && "coddy-tool-call-body--connected-result",
            ]
              .filter(Boolean)
              .join(" ")}
            aria-label="Tool call details"
          >
            {isQuestionTool ? (
              <QuestionToolTimelineReadout
                argsText={props.argsText}
                resultText={resultBody}
                status={props.status}
              />
            ) : null}
            {showToolPreview ? (
              <PermissionToolPreview
                preview={toolPreview}
                interactive={false}
              />
            ) : null}
            {showPatchResult || showResult ? (
              <div
                className={[
                  "tool-call-result-card",
                  status === "failed" && "tool-call-result-card--failed",
                ]
                  .filter(Boolean)
                  .join(" ")}
                aria-label="Tool result"
              >
                <div className="tool-call-result-head">
                  <span className="tool-call-result-dot" aria-hidden />
                  <span>Result</span>
                </div>
                <div
                  className={[
                    "tool-call-result-content",
                    useTallViewport &&
                      `tool-result-viewport tool-result-viewport--tall tool-result-viewport--${viewportMode}`,
                  ]
                    .filter(Boolean)
                    .join(" ")}
                >
                  <pre className="tool-result-pre">{resultBody}</pre>
                </div>
              </div>
            ) : null}
            {toggleButton ? (
              <div className="tool-result-toggle-row">{toggleButton}</div>
            ) : null}
          </div>
        ) : null}
      </details>
    </div>
  );
}
