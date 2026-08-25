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
import { taskStatusLabel, taskTimingLine, taskTone } from "../tasks/taskStatus";
import type { BackgroundTask } from "../tasks/types";
import { buildToolCallPreview } from "../chat/permissionToolPreview";
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

function QuestionToolTimelineReadout(props: {
  argsText?: string | undefined;
  resultText: string;
  status: string;
}) {
  const { t } = useT();
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
        {t("messages.toolQuestionMirrorHint")}
      </p>
    );
  }

  return (
    <div
      className="question-prompt-resolved-body"
      aria-label={t("messages.toolQuestionTimelineAriaLabel")}
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
                {t("messages.toolAwaitingAnswer")}
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
  /** Set when this call started a background task, so the row can keep ticking
   *  after the tool itself returned. */
  backgroundTask?: BackgroundTask | undefined;
  /** Shared clock from the shell so every ticker advances together. */
  backgroundNowMs?: number | undefined;
  onOpenBackgroundTask?: ((taskId: string) => void) | undefined;
  onStopBackgroundTask?: ((taskId: string) => void) | undefined;
}) {
  const { t } = useT();
  const preview = useMemo(
    () => (props.resultText ? props.resultText : ""),
    [props.resultText],
  );
  const full = props.fullResultText || "";
  const rawName = (props.title || props.kind || t("messages.toolDefaultName")).trim();
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
  const terminalStatus =
    status === "completed" || status === "failed" || status === "cancelled";

  const isQuestionTool =
    rawName.toLowerCase() === "question" ||
    (props.kind || "").toLowerCase() === "question";

  const rawNameLower = rawName.toLowerCase();
  const kindLower = (props.kind || "").trim().toLowerCase();
  const isPatchTool = rawNameLower === "apply_patch";
  const isWriteTool =
    !isPatchTool &&
    (rawNameLower === "write" ||
      rawNameLower === "write_file" ||
      (!props.title && kindLower === "write"));
  const isEditTool = !isPatchTool && rawNameLower === "edit";
  /** Tools whose argument preview can be arbitrarily large and needs a capped viewport. */
  const isLargePreviewTool = isPatchTool || isWriteTool || isEditTool;
  const argsTextIsCompleteJSON = useMemo(() => {
    if (!props.argsText) return false;
    try {
      JSON.parse(props.argsText);
      return true;
    } catch {
      return false;
    }
  }, [props.argsText]);

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
      return t("messages.toolQuestionLabel");
    }
    const fallback = t("messages.toolDefaultName");
    return pendingLike
      ? `${rawName || fallback}${t("messages.toolPendingSuffix")}`
      : rawName || fallback;
  }, [isQuestionTool, pendingLike, rawName, t]);

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

  // The sessions list caps argsPreview at 200 chars. Fetch the saved full args when that
  // leaves a patch, write, or edit payload unparseable, so restored cards match live SSE
  // instead of rendering an empty preview. Any status qualifies: a session restored while
  // its tool was still in_progress carries the same truncated preview.
  const fetchFn = props.onFetchToolCallFull;
  const fetchAttemptedRef = useRef(false);
  useEffect(() => {
    fetchAttemptedRef.current = false;
  }, [props.toolCallId]);
  // Complete args re-arm the fetch: a later transcript reconcile can replace them
  // with the truncated list preview again, and the card must recover once more.
  useEffect(() => {
    if (argsTextIsCompleteJSON) fetchAttemptedRef.current = false;
  }, [argsTextIsCompleteJSON]);
  useEffect(() => {
    const needsFullArgs =
      (isPatchTool && !patchContent) ||
      ((isWriteTool || isEditTool) &&
        !!props.argsText &&
        !argsTextIsCompleteJSON);
    if (!needsFullArgs || !fetchFn || fetchAttemptedRef.current) return;
    fetchAttemptedRef.current = true;
    void fetchFn(props.toolCallId);
  }, [
    argsTextIsCompleteJSON,
    fetchFn,
    isEditTool,
    isPatchTool,
    isWriteTool,
    patchContent,
    props.argsText,
    props.toolCallId,
  ]);

  const canExpand =
    !isQuestionTool && props.resultWasTruncated === true && terminalStatus;
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
          {t("messages.toolLess")}
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
          {loadingFull ? t("messages.toolLoading") : t("messages.toolMore")}
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
  const backgroundTask = props.backgroundTask;
  const backgroundNowMs = props.backgroundNowMs ?? nowMs;
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
    !!toggleButton ||
    !!backgroundTask;

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
        <summary className="thinking-summary" aria-label={t("messages.toolSummaryAriaLabel")}>
          <span className="thinking-left">
            <span className="thinking-chevron" aria-hidden="true" />
            <span className="thinking-label">{displayLabel}</span>
            {durationLabel.trim() !== "" ? (
              <span className="thinking-dur" aria-hidden="true">
                {durationLabel}
              </span>
            ) : null}
            {backgroundTask ? (
              <span
                className={[
                  "tool-bgtask-chip",
                  backgroundTask.running ? "is-running" : "",
                ]
                  .filter(Boolean)
                  .join(" ")}
                data-testid={`tool-bgtask-chip-${backgroundTask.id}`}
                title={backgroundTask.command || backgroundTask.label}
              >
                <span
                  className={`bgtask-dot bgtask-dot--${taskTone(backgroundTask.status)}`}
                  aria-hidden="true"
                />
                <span className="tool-bgtask-chip-text">
                  {taskStatusLabel(backgroundTask.status)} ·{" "}
                  {taskTimingLine(backgroundTask, backgroundNowMs)}
                </span>
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
            aria-label={t("messages.toolDetailsAriaLabel")}
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
                overflowControls={isLargePreviewTool}
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
                aria-label={t("messages.toolResultAriaLabel")}
              >
                <div className="tool-call-result-head">
                  <span className="tool-call-result-dot" aria-hidden />
                  <span>{t("messages.toolResultSection")}</span>
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
            {backgroundTask ? (
              <div
                className="tool-bgtask-actions"
                data-testid={`tool-bgtask-actions-${backgroundTask.id}`}
              >
                {props.onOpenBackgroundTask ? (
                  <button
                    type="button"
                    className="tool-overflow-toggle"
                    data-testid={`tool-bgtask-open-${backgroundTask.id}`}
                    onClick={(e) => {
                      e.preventDefault();
                      props.onOpenBackgroundTask?.(backgroundTask.id);
                    }}
                  >
                    {t("messages.toolBgTaskOpen")}
                  </button>
                ) : null}
                {backgroundTask.running && props.onStopBackgroundTask ? (
                  <button
                    type="button"
                    className="tool-overflow-toggle"
                    data-testid={`tool-bgtask-stop-${backgroundTask.id}`}
                    onClick={(e) => {
                      e.preventDefault();
                      props.onStopBackgroundTask?.(backgroundTask.id);
                    }}
                  >
                    {t("messages.toolBgTaskStop")}
                  </button>
                ) : null}
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
