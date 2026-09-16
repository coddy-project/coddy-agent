import {
  type ReactElement,
  memo,
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
import { letterForOptionIndex } from "../chat/questionTypes";
import { PermissionToolPreview } from "../chat/PermissionPromptPreview";
import {
  type WebSearchReport,
  webSearchReport,
  webSearchResultMarkdown,
} from "../chat/webToolResults";
import {
  displayElapsedSeconds,
  formatDuration as formatTaskDuration,
} from "../tasks/taskStatus";
import type { BackgroundTask } from "../tasks/types";
import {
  buildToolCallPreview,
  toolCallTargetIsPath,
  toolCallTargetText,
} from "../chat/permissionToolPreview";
import type { TodoPlanEntry } from "../chat/todoToolPreview";
import { useT } from "../i18n/I18nProvider";
import { parseSpawnAgentArgs } from "../chat/spawnAgentDisplay";
import { SpawnAgentCard } from "./SpawnAgentCard";
import { relativeToolTarget } from "../chat/toolTargetPath";
import { toolDisplayName } from "./toolDisplayName";
import { Markdown } from "../markdown/Markdown";

function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "";
  if (ms >= 60_000) {
    const mins = ms / 60_000;
    const fixed = mins < 10 ? mins.toFixed(1) : mins.toFixed(0);
    return `${fixed}m`;
  }
  return `${Math.round(ms)}ms`;
}

/**
 * What the `question` tool put up, as it put it up: every question with the
 * options behind their own letters, the free-answer slot when one was offered,
 * and the letters the reader took marked among them.
 *
 * The card in the transcript keeps only the question and the answer, so this row
 * is the one place the offer survives - which of the four the reader was choosing
 * between, and what the ones they did not take said.
 */
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
      {qs.map((item, qi) => {
        const picked = (answers[qi] ?? []).filter((a) => a.length > 0);
        // Answers and option labels come out of the same parser, which collapses
        // the whitespace in both, so an answer only has to be matched case
        // -insensitively. Each answer claims one option: a model that offers the
        // same label twice lights one letter per answer rather than both.
        const claimed = new Set<number>();
        const takenOptions = new Set<number>();
        item.options.forEach((option, oi) => {
          const label = option.label.toLowerCase();
          const at = picked.findIndex(
            (a, ai) => !claimed.has(ai) && a.toLowerCase() === label,
          );
          if (at < 0) return;
          claimed.add(at);
          takenOptions.add(oi);
        });
        // Whatever matched no option is what the reader wrote themselves, and the
        // free slot is where they wrote it: it carries their words rather than the
        // name of the slot, so the answer is read where it was given.
        const ownAnswers = picked.filter((_, ai) => !claimed.has(ai));
        const offered = item.options.length > 0 || item.custom;
        // Every answer the offer accounts for is already marked among the letters.
        // The line below carries only what the letters cannot say: that nothing has
        // been answered yet, or an answer with no slot of its own to sit in.
        const strayAnswers = item.custom ? [] : ownAnswers;
        const answerLine =
          !offered || picked.length === 0 || strayAnswers.length > 0;
        return (
          <div
            key={`${qi}-${item.question}`}
            className={qi === 0 ? undefined : "question-prompt-resolved-block"}
          >
            <div className="question-prompt-resolved-q">
              {qs.length > 1 ? `${qi + 1}. ` : ""}
              {item.question}
            </div>
            {item.options.length > 0 || item.custom ? (
              <ul className="question-tool-offer">
                {item.options.map((option, oi) => (
                  <li
                    key={`${oi}-${option.label}`}
                    className={
                      "question-tool-offer-row" +
                      (takenOptions.has(oi)
                        ? " question-tool-offer-row--taken"
                        : "")
                    }
                  >
                    <span className="question-prompt-bubble" aria-hidden>
                      {letterForOptionIndex(oi)}
                    </span>
                    <span className="question-tool-offer-text">
                      {option.label}
                      {option.description ? (
                        <span className="muted"> - {option.description}</span>
                      ) : null}
                    </span>
                  </li>
                ))}
                {item.custom ? (
                  <li
                    className={
                      "question-tool-offer-row" +
                      (ownAnswers.length > 0
                        ? " question-tool-offer-row--taken"
                        : "")
                    }
                  >
                    <span className="question-prompt-bubble" aria-hidden>
                      {letterForOptionIndex(item.options.length)}
                    </span>
                    <span
                      className={
                        "question-tool-offer-text" +
                        (ownAnswers.length > 0 ? "" : " muted")
                      }
                    >
                      {ownAnswers.length > 0
                        ? ownAnswers.join(", ")
                        : t("messages.toolQuestionOwnAnswer")}
                    </span>
                  </li>
                ) : null}
              </ul>
            ) : null}
            {answerLine ? (
              terminal && picked.length > 0 ? (
                <div className="question-prompt-resolved-a">
                  {(strayAnswers.length > 0 ? strayAnswers : picked).join(", ")}
                </div>
              ) : (
                <div className="question-prompt-resolved-a muted">
                  {terminal
                    ? t("prompts.noAnswer")
                    : t("messages.toolAwaitingAnswer")}
                </div>
              )
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

/**
 * How each engine answered a search, above its hits: a count for an engine that
 * answered, the outcome and its reason for one that did not. Without it an empty
 * list cannot tell "the web has nothing" from "the engines turned us away".
 */
function WebSearchEngines(props: { report: WebSearchReport }) {
  const { t } = useT();
  return (
    <div className="web-search-engines" data-testid="web-search-engines">
      {props.report.engines.map((e) => {
        let text: string;
        if (e.status === "ok" || e.status === "empty") {
          text = `${e.engine}: ${e.results}`;
        } else {
          const word =
            e.status === "blocked"
              ? t("messages.webSearchEngineBlocked")
              : e.status === "error"
                ? t("messages.webSearchEngineError")
                : e.status;
          text = `${e.engine}: ${word}${e.reason ? ` (${e.reason})` : ""}`;
        }
        return (
          <span
            key={e.engine}
            className={`web-search-engine web-search-engine--${e.status || "unknown"}`}
          >
            {text}
          </span>
        );
      })}
    </div>
  );
}

export const ToolCallMessage = memo(function ToolCallMessage(props: {
  toolCallId: string;
  title?: string | undefined;
  kind?: string | undefined;
  status: string;
  argsText?: string | undefined;
  resultText?: string | undefined;
  fullResultText?: string | undefined;
  resultWasTruncated?: boolean | undefined;
  /** Final todo state saved with this call, used by structured todo previews. */
  todoPlan?: TodoPlanEntry[] | undefined;
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
  /** Roots this session works in - its own directory, then its worktrees -
   *  deepest match first when the row spells a path. */
  pathRoots?: readonly string[] | undefined;
}) {
  const { t } = useT();
  const preview = useMemo(
    () => (props.resultText ? props.resultText : ""),
    [props.resultText],
  );
  const full = props.fullResultText || "";
  const rawName = (
    props.title ||
    props.kind ||
    t("messages.toolDefaultName")
  ).trim();
  const toolPreview = useMemo(
    () =>
      buildToolCallPreview(
        {
          title: props.title,
          kind: props.kind,
          argsText: props.argsText,
          todoPlan: props.todoPlan,
        },
        props.argsText || "",
      ),
    [props.argsText, props.kind, props.title, props.todoPlan, t],
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
  const isSpawnAgentTool = rawNameLower === "spawn_agent" || kindLower === "spawn_agent";
  const spawnAgent = useMemo(
    () => (isSpawnAgentTool ? parseSpawnAgentArgs(props.argsText) : null),
    [isSpawnAgentTool, props.argsText],
  );
  const isLoadSkillTool = rawNameLower === "load_skill";
  const isWebSearchTool = rawNameLower === "websearch";
  const isWebFetchTool = rawNameLower === "webfetch";
  // The one thing this call acts on - the path it reads, the command it runs, the skill
  // it pulls in - next to the label, so a collapsed row still says what it touched.
  const targetContext = useMemo(
    () => ({
      ...(props.title !== undefined ? { title: props.title } : {}),
      ...(props.kind !== undefined ? { kind: props.kind } : {}),
      ...(props.argsText !== undefined ? { argsText: props.argsText } : {}),
    }),
    [props.argsText, props.kind, props.title],
  );
  const summaryTargetFull = useMemo(
    () => (isQuestionTool ? "" : toolCallTargetText(targetContext).trim()),
    [isQuestionTool, targetContext],
  );
  // The row is one line and clips its end, which is where a path carries the file
  // name. Against the session's own directory the same file is a few segments, so
  // that is what the row shows; the tooltip and the expanded card keep the path
  // the call was actually given.
  const summaryTarget = useMemo(
    () =>
      summaryTargetFull && toolCallTargetIsPath(targetContext)
        ? relativeToolTarget(summaryTargetFull, props.pathRoots || [])
        : summaryTargetFull,
    [props.pathRoots, summaryTargetFull, targetContext],
  );
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
    const label = toolDisplayName(rawName, props.argsText);
    return pendingLike ? `${label}${t("messages.toolPendingSuffix")}` : label;
  }, [isQuestionTool, pendingLike, props.argsText, rawName, t]);

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
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [searchFetchFailed, setSearchFetchFailed] = useState(false);

  useEffect(() => {
    setShowExpanded(false);
    setLoadingFull(false);
    setSearchFetchFailed(false);
  }, [props.toolCallId]);

  // The sessions list caps argsPreview at 200 chars. Fetch the saved full args when that
  // leaves a patch, write, edit, or spawn_agent payload unparseable, so restored cards match live SSE
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
      ((isWriteTool || isEditTool || isSpawnAgentTool) &&
        !!props.argsText &&
        !argsTextIsCompleteJSON);
    if (!needsFullArgs || !fetchFn || fetchAttemptedRef.current) return;
    fetchAttemptedRef.current = true;
    void fetchFn(props.toolCallId).catch(() => {
      // Preserve the readable preview if the saved arguments cannot be loaded.
    });
  }, [
    argsTextIsCompleteJSON,
    fetchFn,
    isEditTool,
    isPatchTool,
    isWriteTool,
    isSpawnAgentTool,
    patchContent,
    props.argsText,
    props.toolCallId,
  ]);

  const fetchFull = props.onFetchToolCallFull;
  // A search is read as its list of hits, and the row's preview is cut inside the
  // engine report that leads the answer - often before the first hit. Opening the
  // row is the request for the results, so the whole answer is fetched then, once,
  // and shown without a More control; a closed row costs nothing. Only a failed
  // fetch hands the reader the ordinary control to try again.
  const loadsWholeSearch =
    isWebSearchTool &&
    status === "completed" &&
    props.resultWasTruncated === true &&
    !searchFetchFailed;
  const searchFetchAttemptedRef = useRef(false);
  useEffect(() => {
    searchFetchAttemptedRef.current = false;
  }, [props.toolCallId]);
  useEffect(() => {
    if (!loadsWholeSearch || !detailsOpen || full || !fetchFull) return;
    if (searchFetchAttemptedRef.current) return;
    searchFetchAttemptedRef.current = true;
    setLoadingFull(true);
    fetchFull(props.toolCallId)
      .catch(() => setSearchFetchFailed(true))
      .finally(() => setLoadingFull(false));
  }, [detailsOpen, fetchFull, full, loadsWholeSearch, props.toolCallId]);

  const canExpand =
    !isQuestionTool &&
    !loadsWholeSearch &&
    props.resultWasTruncated === true &&
    terminalStatus;

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

  // Collapsing swaps the result body from a scrollable box back to a clipped one,
  // and a box that kept its offset reopens in the middle of the output with its
  // first line cut in half. The argument preview resets the same way.
  const resultViewportRef = useRef<HTMLDivElement | null>(null);
  const onHide = useCallback(() => {
    if (resultViewportRef.current) {
      resultViewportRef.current.scrollTop = 0;
    }
    setShowExpanded(false);
  }, []);

  const resultBody =
    (showExpanded || loadsWholeSearch) && full ? full : preview;
  const useTallViewport =
    !loadsWholeSearch &&
    (props.resultWasTruncated === true || (showExpanded && full.trim() !== ""));

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
    (toolPreview.kind === "todo" && toolPreview.entries.length > 0) ||
    toolPreview.kind === "plan_exit" ||
    toolPreview.kind === "action" ||
    (toolPreview.kind === "move" &&
      (toolPreview.sourcePath.trim() !== "" ||
        toolPreview.destinationPath.trim() !== ""));
  const backgroundTask = props.backgroundTask;
  const backgroundNowMs = props.backgroundNowMs ?? nowMs;
  // A backgrounded call returned the instant the task started, so the call's own
  // 0ms is not the duration of anything. The task's clock takes that slot; how it
  // ended - the status, the estimate, the exit code - belongs to the task, and is
  // read in the Tasks panel rather than on a transcript row.
  const backgroundElapsed = backgroundTask
    ? formatTaskDuration(displayElapsedSeconds(backgroundTask, backgroundNowMs))
    : "";
  // A completed load_skill returned a skill's markdown; a failed one returned an error,
  // which stays raw monospace text. A fetched page is markdown too, and a search
  // answers with a JSON object of hits that reads as a list of links - both are
  // documents, so both render as the prose they are rather than as their source.
  const searchResultMarkdown = useMemo(
    () =>
      isWebSearchTool && status === "completed"
        ? webSearchResultMarkdown(resultBody)
        : null,
    [isWebSearchTool, resultBody, status],
  );
  const searchReport: WebSearchReport | null = useMemo(
    () =>
      isWebSearchTool && status === "completed"
        ? webSearchReport(resultBody)
        : null,
    [isWebSearchTool, resultBody, status],
  );
  // The whole answer is on its way and the preview holds no hit to show meanwhile:
  // a loading line reads better than the raw JSON it would otherwise fall back to.
  const searchLoading =
    loadsWholeSearch && !full && searchResultMarkdown === null;
  const markdownResultBody =
    searchResultMarkdown ??
    (isWebFetchTool && status === "completed" ? resultBody : null);
  const showSkillBody =
    (isLoadSkillTool && status === "completed") || markdownResultBody !== null;
  // load_skill already names the skill on the summary row; its body is the skill itself.
  const showToolPreview =
    !isQuestionTool && !spawnAgent && !isLoadSkillTool && toolPreviewHasContent;
  const showPatchResult =
    isPatchTool &&
    !!resultBody &&
    !resultBody.trim().toLowerCase().startsWith("patch applied successfully");
  const showResult =
    !isQuestionTool &&
    !isPatchTool &&
    !(
      status === "completed" &&
      (toolPreview.kind === "todo" || toolPreview.kind === "plan_exit")
    ) &&
    !!(resultBody && resultBody.length > 0);
  const hasConnectedResult = (showToolPreview || !!spawnAgent) && (showPatchResult || showResult);
  const hasBody =
    !!spawnAgent ||
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
        onToggle={(e) => setDetailsOpen(e.currentTarget.open)}
      >
        <summary
          className="thinking-summary"
          aria-label={t("messages.toolSummaryAriaLabel")}
        >
          <span className="thinking-left">
            <span className="thinking-chevron" aria-hidden="true" />
            <span className="thinking-head">
              <span className="thinking-label">{displayLabel}</span>
              {summaryTarget ? (
                <span
                  className="tool-summary-target"
                  data-testid="tool-summary-target"
                  title={summaryTargetFull}
                >
                  {summaryTarget}
                </span>
              ) : null}
              {status === "failed" ? (
                <span
                  className="tool-failed-marker"
                  data-testid="tool-failed-marker"
                >
                  {t("messages.toolFailedMarker")}
                </span>
              ) : null}
              {backgroundTask ? (
                backgroundElapsed ? (
                  <span
                    className="thinking-dur"
                    data-testid={`tool-bgtask-elapsed-${backgroundTask.id}`}
                  >
                    {backgroundElapsed}
                  </span>
                ) : null
              ) : durationLabel.trim() !== "" ? (
                <span className="thinking-dur" aria-hidden="true">
                  {durationLabel}
                </span>
              ) : null}
            </span>
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
                toolStatus={status}
              />
            ) : null}
            {spawnAgent ? <SpawnAgentCard details={spawnAgent} /> : null}
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
                <div
                  ref={resultViewportRef}
                  data-testid="tool-result-viewport"
                  className={[
                    "tool-call-result-content",
                    showSkillBody && "tool-call-result-content--markdown",
                    useTallViewport &&
                      `tool-result-viewport tool-result-viewport--tall tool-result-viewport--${viewportMode}`,
                  ]
                    .filter(Boolean)
                    .join(" ")}
                >
                  {searchReport && searchReport.engines.length > 0 ? (
                    <WebSearchEngines report={searchReport} />
                  ) : null}
                  {searchLoading ? (
                    <div
                      className="tool-result-loading"
                      data-testid="tool-result-loading"
                    >
                      {t("messages.toolLoading")}
                    </div>
                  ) : showSkillBody ? (
                    <Markdown text={markdownResultBody ?? resultBody} />
                  ) : (
                    <pre className="tool-result-pre">{resultBody}</pre>
                  )}
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
});
