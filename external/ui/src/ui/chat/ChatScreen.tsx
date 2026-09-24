import type { CSSProperties, Dispatch, SetStateAction } from "react";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { HeroAccentVerb } from "./heroTitleWords";
import { useT } from "../i18n/I18nProvider";
import type { PermissionResolvedState } from "./permissionTypes";
import type { TurnOverride } from "./sessionSettings";
import type { QuestionResolvedState } from "./questionTypes";
import type { TokenUsage, TranscriptItem } from "./types";
import { UsageBanner } from "./UsageBanner";
import type { ProviderUsage } from "./providerUsage";
import { ChatHeader } from "./ChatHeader";
import { Composer } from "./Composer";
import type { QueuedMessage } from "./Composer";
import { MessageList } from "../messages/MessageList";
import type { BackgroundTask } from "../tasks/types";
import { countRunningTasks, isAwaitingPermission } from "../tasks/taskStatus";
import type { TurnProgress } from "./turnProgress";
import { SubagentPermissionCards } from "./SubagentPermissionCard";
import { SubagentReadOnlyNotice } from "./SubagentReadOnlyNotice";
import { ArchivedSessionNotice } from "./ArchivedSessionNotice";
import type { SubagentTranscriptMeta } from "./subagentTranscript";
import {
  subscribeShellStack,
  snapshotShellStack,
  serverSnapshotShellStack,
} from "../shellBreakpoint";
import { transcriptItemsAffectAutoScroll } from "./transcriptAutoScroll";
import {
  documentScrollBottom,
  documentTranscriptMetrics,
  easeTranscriptJump,
  elementScrollBottom,
  elementTranscriptMetrics,
  isTranscriptAtBottom,
  transcriptJumpDurationMs,
} from "./transcriptScrollPosition";
import { ScrollToBottomButton } from "./ScrollToBottomButton";

export function ChatScreen(props: {
  title: string;
  sessionId: string;
  /** Accent verb for "What do you want to …?" on the empty hero (session-stable or home rotation). */
  heroAccentVerb: HeroAccentVerb;
  /** Bumps when the user starts a fresh home chat so the composer can refocus. */
  heroComposerFocusEpoch: number;
  onTitleSave: (title: string) => void;
  items: TranscriptItem[];
  draft: string;
  tokenUsage: TokenUsage | null;
  /** Account usage behind the selected model's provider: the pill and the banner. */
  providerUsage?: ProviderUsage | null;
  usageBannerDismissedKey?: string;
  onUsageBannerDismiss?: (key: string) => void;
  contextPct?: number;
  maxContextTokens?: number;
  contextBreakdown?:
    | import("./ContextBreakdownPopover").ContextBreakdown
    | null;
  mode: string;
  modes: string[];
  llmModels?: string[];
  llmModel?: string;
  onLlmModelChange?: (modelId: string) => void;
  /** Whether the currently selected model accepts image/file inputs. */
  llmModelMultimodal?: boolean;
  /** Reasoning levels offered by the current model (empty hides the selector). */
  llmReasoningLevels?: string[];
  llmReasoning?: string;
  onLlmReasoningChange?: (level: string) => void;
  onModeChange: (mode: string) => void;
  /** The session's permission mode chip and the settings armed for the next
   *  turns (chat/sessionSettings.ts); passed through to the composer. */
  permissionMode?: string;
  configuredPermissionMode?: string;
  onPermissionModeChange?: ((mode: string) => void) | undefined;
  settingsOverrides?: TurnOverride[];
  onDraftChange: (v: string) => void;
  onSend: (text: string, files?: File[]) => void;
  /** The files attached in the composer, when the caller owns them: a send the
   *  server never took puts them back (App.tsx, streamResponses). */
  attachedFiles?: File[];
  onAttachedFilesChange?: Dispatch<SetStateAction<File[]>>;
  /** `/docs [page or words]` typed in the composer opens the documentation reader. */
  onDocsCommand?: (arg: string) => void;
  onContextRingOpen?: () => void;
  generating?: boolean;
  onStop?: () => void;
  /** Follow-ups waiting for the running turn to read them (the message queue). */
  queuedMessages?: QueuedMessage[];
  /** Add the draft to that queue instead of starting a turn. */
  onQueue?: (text: string) => void;
  /** Take one queued follow-up back before the agent reads it. */
  onCancelQueued?: (id: string) => void;
  /** Re-run the last turn; surfaces as a refresh button on the last error notice. */
  onRetryLast?: () => void;
  /** Fetch persisted full tool output; UI keeps preview in resultText. */
  onFetchToolCallFull?: (toolCallId: string) => Promise<void>;
  onQuestionPromptResolved?: (
    sessionId: string,
    itemId: string,
    resolved: QuestionResolvedState,
  ) => void;
  onPermissionPromptResolved?: (
    sessionId: string,
    itemId: string,
    resolved: PermissionResolvedState,
  ) => void;
  onPlanDocumentExpanded?: (itemId: string, expanded: boolean) => void;
  onPlanDocumentRun?: (slug: string) => void;
  onPlanDocumentDiscard?: (itemId: string, slug: string) => void;
  onEdit?: (content: string, userMsgIdx: number) => void;
  editingFiles?: { name: string; mimeType: string }[];
  sessionLoading?: boolean;
  sessionFadingOut?: boolean;
  knownSkillNames?: Set<string>;
  /** Background tasks of this session keyed by the tool call that started them. */
  backgroundTasksByToolCallId?: Map<string, BackgroundTask>;
  backgroundNowMs?: number;
  /** Every background task of this chat, for the header control and the live line. */
  backgroundTasks?: BackgroundTask[];
  onOpenBackgroundTasks?: () => void;
  /** The Tasks panel is showing, for the header control's expanded state. */
  backgroundTasksOpen?: boolean;
  onCloseBackgroundTasks?: () => void;
  /** Re-read the task rows: a background subagent's prompt was answered here. */
  onBackgroundTasksChanged?: () => void;
  onOpenBackgroundTask?: (taskId: string) => void;
  onStopBackgroundTask?: (taskId: string) => void;
  /** Roots this session works in - its own directory, then its worktrees -
   *  which tool rows spell paths against. */
  pathRoots?: readonly string[];
  /** The running turn's clock and generated tokens as the server reports them. */
  turnProgress?: TurnProgress | null;
  /** Workspace context chips (folder / branch / worktree) above the composer field. */
  workspaceCtx?: import("./workspaceContext").WorkspaceContext | null;
  worktreePref?: boolean;
  /** The workspace is chosen once: locked as soon as the conversation starts. */
  workspaceLocked?: boolean;
  onWorkspacePickFolder?: (path: string) => void;
  onWorkspacePickBranch?: (branch: string, worktree: boolean) => void;
  onWorktreeToggle?: () => void;
  /** Set when this session is a subagent's transcript: the composer gives way to a read-only notice. */
  subagentTranscript?: SubagentTranscriptMeta | null;
  /** True when the conversation on screen is archived: the composer gives way to the notice that offers to take it back out. */
  sessionArchived?: boolean;
  onUnarchiveSession?: () => void;
  /** True while that request is in flight. */
  unarchiving?: boolean;
  /** Opens another session in this tab (the parent chat from the notice). */
  onOpenSession?: (sessionId: string) => void;
}) {
  const { t } = useT();
  const messagesRef = useRef<HTMLDivElement | null>(null);
  const composerHostRef = useRef<HTMLDivElement | null>(null);
  const isEmpty = props.items.length === 0;
  // One count for the live line and the header control.
  const runningTasks = useMemo(
    () => countRunningTasks(props.backgroundTasks ?? []),
    [props.backgroundTasks],
  );
  const showSkeleton = isEmpty && !!props.sessionLoading;
  const stickToBottomRef = useRef(true);
  const prevItemsForScrollRef = useRef<TranscriptItem[]>([]);
  const prevPermissionsForScrollRef = useRef(new Set<string>());
  const jumpFrameRef = useRef<number | null>(null);
  const [composerReserve, setComposerReserve] = useState(200);
  const [showScrollToBottom, setShowScrollToBottom] = useState(false);
  // Shared by hero and docked composers so disabled files survive the first text turn.
  const [localAttachedFiles, setLocalAttachedFiles] = useState<File[]>([]);
  const attachedFiles = props.attachedFiles ?? localAttachedFiles;
  const setAttachedFiles = props.onAttachedFilesChange ?? setLocalAttachedFiles;
  const mobileDocScroll = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );

  useLayoutEffect(() => {
    if (isEmpty) return;
    const host = composerHostRef.current;
    if (!host) return;
    const extra = 10;
    const apply = () => {
      const h = host.getBoundingClientRect().height;
      setComposerReserve(Math.max(140, Math.ceil(h) + extra));
    };
    apply();
    const ro =
      typeof ResizeObserver !== "undefined" ? new ResizeObserver(apply) : null;
    ro?.observe(host);
    return () => ro?.disconnect();
  }, [isEmpty, props.tokenUsage]);

  // Whichever surface scrolls, it is read and written through these three, so
  // the follow, the button and the jump never disagree about where the end is.
  const transcriptScrollBottom = useCallback((): number => {
    if (mobileDocScroll) return documentScrollBottom(window);
    const el = messagesRef.current;
    return el ? elementScrollBottom(el) : 0;
  }, [mobileDocScroll]);

  const readTranscriptScrollTop = useCallback((): number => {
    if (mobileDocScroll) return window.scrollY;
    return messagesRef.current?.scrollTop ?? 0;
  }, [mobileDocScroll]);

  const writeTranscriptScrollTop = useCallback(
    (top: number) => {
      if (mobileDocScroll) {
        window.scrollTo({ top, left: 0, behavior: "auto" });
        return;
      }
      const el = messagesRef.current;
      if (el) el.scrollTop = top;
    },
    [mobileDocScroll],
  );

  const cancelTranscriptJump = useCallback((): boolean => {
    if (jumpFrameRef.current === null) return false;
    cancelAnimationFrame(jumpFrameRef.current);
    jumpFrameRef.current = null;
    return true;
  }, []);

  // One reading of the scrollport drives both behaviours: the transcript
  // follows new output while it sits in the bottom band, and the jump button
  // appears exactly when it stops following.
  const syncTranscriptPosition = useCallback(() => {
    // A jump owns the position while it travels. Reading it mid-flight would
    // put the button back on screen for every frame above the band.
    if (jumpFrameRef.current !== null) return;
    let atBottom: boolean;
    if (mobileDocScroll) {
      atBottom = isTranscriptAtBottom(documentTranscriptMetrics(window));
    } else {
      const el = messagesRef.current;
      if (!el) return;
      atBottom = isTranscriptAtBottom(elementTranscriptMetrics(el));
    }
    stickToBottomRef.current = atBottom;
    setShowScrollToBottom(!atBottom);
  }, [mobileDocScroll]);

  const jumpToNewestMessage = useCallback(() => {
    cancelTranscriptJump();
    stickToBottomRef.current = true;
    setShowScrollToBottom(false);
    const from = readTranscriptScrollTop();
    const to = transcriptScrollBottom();
    const reduceMotion =
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (to <= from || reduceMotion) {
      writeTranscriptScrollTop(to);
      return;
    }
    const duration = transcriptJumpDurationMs(to - from);
    const started = performance.now();
    const step = (now: number) => {
      const progress = (now - started) / duration;
      // The end is re-read every frame: a streaming turn keeps moving it down,
      // and the travel should land on where the transcript is now.
      const end = transcriptScrollBottom();
      writeTranscriptScrollTop(
        from + (end - from) * easeTranscriptJump(progress),
      );
      if (progress < 1) {
        jumpFrameRef.current = requestAnimationFrame(step);
        return;
      }
      jumpFrameRef.current = null;
      syncTranscriptPosition();
    };
    jumpFrameRef.current = requestAnimationFrame(step);
  }, [
    cancelTranscriptJump,
    readTranscriptScrollTop,
    syncTranscriptPosition,
    transcriptScrollBottom,
    writeTranscriptScrollTop,
  ]);

  // The reader reaching for the wheel, a finger or the scrollbar always wins
  // over a jump still in the air.
  useEffect(() => {
    const takeOver = () => {
      if (cancelTranscriptJump()) syncTranscriptPosition();
    };
    const passive = { passive: true } as const;
    window.addEventListener("wheel", takeOver, passive);
    window.addEventListener("touchstart", takeOver, passive);
    window.addEventListener("mousedown", takeOver, passive);
    return () => {
      window.removeEventListener("wheel", takeOver);
      window.removeEventListener("touchstart", takeOver);
      window.removeEventListener("mousedown", takeOver);
    };
  }, [cancelTranscriptJump, syncTranscriptPosition]);

  useEffect(() => {
    return () => {
      cancelTranscriptJump();
    };
  }, [cancelTranscriptJump]);

  useEffect(() => {
    if (isEmpty) return;
    const prev = prevItemsForScrollRef.current;
    prevItemsForScrollRef.current = props.items;
    // Polling replaces task rows even when nothing changed. Only a newly
    // waiting call should follow the reader, never an elapsed-time update.
    const permissions = new Set(
      (props.backgroundTasks ?? [])
        .filter(isAwaitingPermission)
        .map((task) =>
          JSON.stringify([
            task.id,
            task.pending_permission?.sessionId,
            task.pending_permission?.toolCall.toolCallId,
          ]),
        ),
    );
    const newPermission = [...permissions].some(
      (key) => !prevPermissionsForScrollRef.current.has(key),
    );
    prevPermissionsForScrollRef.current = permissions;
    if (!newPermission && !transcriptItemsAffectAutoScroll(prev, props.items)) {
      return;
    }
    // A jump already chases the end of a growing transcript; a hard scroll here
    // would fight its travel frame by frame.
    if (jumpFrameRef.current !== null) return;
    // Content grew under a reader who scrolled away: leave them where they are
    // and re-read the position, which is what reveals the button mid-stream.
    if (!stickToBottomRef.current) {
      syncTranscriptPosition();
      return;
    }
    const follow = () => {
      writeTranscriptScrollTop(transcriptScrollBottom());
      syncTranscriptPosition();
    };
    if (mobileDocScroll) {
      // The document takes its new height after layout, not on this tick.
      requestAnimationFrame(() => requestAnimationFrame(follow));
      return;
    }
    follow();
  }, [
    props.items,
    props.backgroundTasks,
    isEmpty,
    mobileDocScroll,
    syncTranscriptPosition,
    transcriptScrollBottom,
    writeTranscriptScrollTop,
  ]);

  useEffect(() => {
    if (isEmpty) return;
    const onScroll = () => syncTranscriptPosition();
    if (mobileDocScroll) {
      window.addEventListener("scroll", onScroll, { passive: true });
      return () => window.removeEventListener("scroll", onScroll);
    }
    const el = messagesRef.current;
    el?.addEventListener("scroll", onScroll, { passive: true });
    return () => el?.removeEventListener("scroll", onScroll);
  }, [isEmpty, mobileDocScroll, syncTranscriptPosition]);

  // A child session is read-only on the server (409 on any prompt), so the
  // notice takes the composer's slot in both the hero and the docked layout.
  // An archived conversation takes the same slot for a different reason: the
  // server would accept the prompt, and accepting it would quietly undo the
  // operator's own "not now".
  const readOnlyNotice = props.subagentTranscript ? (
    <SubagentReadOnlyNotice
      meta={props.subagentTranscript}
      {...(props.onOpenSession ? { onOpenSession: props.onOpenSession } : {})}
    />
  ) : props.sessionArchived ? (
    <ArchivedSessionNotice
      onUnarchive={() => props.onUnarchiveSession?.()}
      {...(props.unarchiving ? { busy: true } : {})}
    />
  ) : null;

  const mainClassName = [
    "main",
    isEmpty && !showSkeleton ? "is-empty" : "",
    props.sessionFadingOut ? "session-fading-out" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <main className={mainClassName}>
      {showSkeleton ? (
        <div className="chat-skeleton" aria-hidden="true">
          <div className="chat-skeleton-header">
            <div
              className="chat-skeleton-bar"
              style={{ width: "180px", height: "18px", borderRadius: "6px" }}
            />
          </div>
          <div className="chat-skeleton-messages">
            <div className="chat-skeleton-row chat-skeleton-row--user">
              <div
                className="chat-skeleton-bar"
                style={{ width: "220px", height: "38px", borderRadius: "12px" }}
              />
            </div>
            <div className="chat-skeleton-row">
              <div
                className="chat-skeleton-bar"
                style={{ width: "78%", height: "14px", borderRadius: "6px" }}
              />
              <div
                className="chat-skeleton-bar"
                style={{ width: "62%", height: "14px", borderRadius: "6px" }}
              />
              <div
                className="chat-skeleton-bar"
                style={{ width: "70%", height: "14px", borderRadius: "6px" }}
              />
            </div>
            <div className="chat-skeleton-row chat-skeleton-row--user">
              <div
                className="chat-skeleton-bar"
                style={{ width: "160px", height: "38px", borderRadius: "12px" }}
              />
            </div>
            <div className="chat-skeleton-row">
              <div
                className="chat-skeleton-bar"
                style={{ width: "72%", height: "14px", borderRadius: "6px" }}
              />
              <div
                className="chat-skeleton-bar"
                style={{ width: "50%", height: "14px", borderRadius: "6px" }}
              />
            </div>
          </div>
        </div>
      ) : isEmpty ? (
        <div className="hero" id="hero">
          <h1 className="hero-title">
            {(() => {
              const verb = t(`chat.heroVerb.${props.heroAccentVerb}`);
              // A sentinel that cannot occur in translated copy, so the split finds the
              // {verb} slot itself rather than the first space of the sentence.
              const marker = "\u0000";
              const full = t("chat.heroTitle", { verb: marker });
              const i = full.indexOf(marker);
              const before = i >= 0 ? full.slice(0, i) : full;
              const after = i >= 0 ? full.slice(i + marker.length) : "";
              return (
                <span className="hero-title-muted">
                  {before}
                  <span
                    className="hero-title-accent"
                    data-testid="hero-title-accent"
                  >
                    {verb}
                  </span>
                  {after}
                </span>
              );
            })()}
          </h1>
          <div className="hero-composer">
            {readOnlyNotice ? null : (
              <UsageBanner
                usage={props.providerUsage}
                modelId={props.llmModel ?? ""}
                {...(props.usageBannerDismissedKey
                  ? { dismissedKey: props.usageBannerDismissedKey }
                  : {})}
                {...(props.onUsageBannerDismiss
                  ? { onDismiss: props.onUsageBannerDismiss }
                  : {})}
              />
            )}
            {readOnlyNotice ?? (
              <Composer
                value={props.draft}
                isEmpty={true}
                providerUsage={props.providerUsage ?? null}
                attachedFiles={attachedFiles}
                onAttachedFilesChange={setAttachedFiles}
                focusEpoch={props.heroComposerFocusEpoch}
                sessionId={props.sessionId}
                contextIdle={!props.sessionId}
                mode={props.mode}
                modes={props.modes}
                tokenUsage={props.tokenUsage}
                {...(props.contextPct !== undefined
                  ? { contextPct: props.contextPct }
                  : {})}
                {...(props.maxContextTokens !== undefined
                  ? { maxContextTokens: props.maxContextTokens }
                  : {})}
                {...(props.contextBreakdown !== undefined
                  ? { contextBreakdown: props.contextBreakdown }
                  : {})}
                {...(props.llmModels !== undefined &&
                props.llmModels.length > 0 &&
                props.onLlmModelChange !== undefined
                  ? {
                      llmModels: props.llmModels,
                      llmModel: props.llmModel,
                      onLlmModelChange: props.onLlmModelChange,
                      llmModelMultimodal: props.llmModelMultimodal,
                      ...(props.llmReasoningLevels !== undefined &&
                      props.llmReasoningLevels.length > 0 &&
                      props.onLlmReasoningChange !== undefined
                        ? {
                            llmReasoningLevels: props.llmReasoningLevels,
                            llmReasoning: props.llmReasoning,
                            onLlmReasoningChange: props.onLlmReasoningChange,
                          }
                        : {}),
                    }
                  : {})}
                onModeChange={props.onModeChange}
                {...(props.permissionMode !== undefined
                  ? { permissionMode: props.permissionMode }
                  : {})}
                {...(props.configuredPermissionMode !== undefined
                  ? { configuredPermissionMode: props.configuredPermissionMode }
                  : {})}
                {...(props.onPermissionModeChange
                  ? { onPermissionModeChange: props.onPermissionModeChange }
                  : {})}
                {...(props.settingsOverrides
                  ? { settingsOverrides: props.settingsOverrides }
                  : {})}
                onChange={props.onDraftChange}
                onSend={props.onSend}
                {...(props.onDocsCommand ? { onDocsCommand: props.onDocsCommand } : {})}
                {...(props.onContextRingOpen
                  ? { onContextRingOpen: props.onContextRingOpen }
                  : {})}
                {...(props.generating === true && props.onStop !== undefined
                  ? { generating: true, onStop: props.onStop }
                  : {})}
                {...(props.onQueue
                  ? {
                      queuedMessages: props.queuedMessages ?? [],
                      onQueue: props.onQueue,
                      ...(props.onCancelQueued
                        ? { onCancelQueued: props.onCancelQueued }
                        : {}),
                    }
                  : {})}
                {...(props.knownSkillNames
                  ? { knownSkillNames: props.knownSkillNames }
                  : {})}
                {...(props.onWorkspacePickFolder
                  ? {
                      workspaceCtx: props.workspaceCtx ?? null,
                      worktreePref: props.worktreePref ?? false,
                      workspaceLocked: props.workspaceLocked ?? false,
                      onWorkspacePickFolder: props.onWorkspacePickFolder,
                      onWorkspacePickBranch: props.onWorkspacePickBranch,
                      onWorktreeToggle: props.onWorktreeToggle,
                    }
                  : {})}
              />
            )}
          </div>
          <div className="hero-footer">
            <a
              href="https://github.com/coddy-project/coddy-agent"
              target="_blank"
              rel="noopener"
            >
              GitHub
            </a>
            <span className="hero-footer-sep" aria-hidden>
              |
            </span>
            <a href="/docs/" target="_blank" rel="noopener">
              API docs
            </a>
          </div>
        </div>
      ) : (
        <div
          className="chat-stack"
          style={
            {
              "--chat-composer-reserve": `${composerReserve}px`,
            } as CSSProperties
          }
        >
          <div
            id="messages"
            className="chat-scroll"
            aria-live="polite"
            ref={messagesRef}
          >
            <div className="chat-scroll-sticky-head">
              <div className="chat-title-column">
                <ChatHeader
                  title={props.title}
                  editable={true}
                  onTitleSave={props.onTitleSave}
                  {...(props.onOpenBackgroundTasks
                    ? {
                        tasks: props.backgroundTasks ?? [],
                        // The header control is where the panel was opened
                        // from, so a second click puts it away again.
                        onOpenTasks:
                          props.backgroundTasksOpen === true &&
                          props.onCloseBackgroundTasks
                            ? props.onCloseBackgroundTasks
                            : props.onOpenBackgroundTasks,
                        tasksOpen: props.backgroundTasksOpen === true,
                      }
                    : {})}
                />
              </div>
            </div>
            <div className="messages-inner">
              <MessageList
                items={props.items}
                sessionId={props.sessionId}
                generating={props.generating === true}
                {...(props.pathRoots !== undefined
                  ? { pathRoots: props.pathRoots }
                  : {})}
                {...(props.turnProgress
                  ? { turnProgress: props.turnProgress }
                  : {})}
                {...(runningTasks > 0 ? { runningTasks } : {})}
                {...(props.onOpenBackgroundTasks
                  ? { onOpenTasks: props.onOpenBackgroundTasks }
                  : {})}
                {...(props.onRetryLast
                  ? { onRetryLast: props.onRetryLast }
                  : {})}
                {...(props.onFetchToolCallFull
                  ? { onFetchToolCallFull: props.onFetchToolCallFull }
                  : {})}
                {...(props.onQuestionPromptResolved
                  ? { onQuestionPromptResolved: props.onQuestionPromptResolved }
                  : {})}
                {...(props.onPermissionPromptResolved
                  ? {
                      onPermissionPromptResolved:
                        props.onPermissionPromptResolved,
                    }
                  : {})}
                {...(props.onPlanDocumentExpanded
                  ? { onPlanDocumentExpanded: props.onPlanDocumentExpanded }
                  : {})}
                {...(props.onPlanDocumentRun
                  ? { onPlanDocumentRun: props.onPlanDocumentRun }
                  : {})}
                {...(props.onPlanDocumentDiscard
                  ? { onPlanDocumentDiscard: props.onPlanDocumentDiscard }
                  : {})}
                {...(props.onEdit ? { onEdit: props.onEdit } : {})}
                {...(props.knownSkillNames
                  ? { knownSkillNames: props.knownSkillNames }
                  : {})}
                {...(props.backgroundTasksByToolCallId
                  ? {
                      backgroundTasksByToolCallId:
                        props.backgroundTasksByToolCallId,
                    }
                  : {})}
                {...(props.backgroundNowMs !== undefined
                  ? { backgroundNowMs: props.backgroundNowMs }
                  : {})}
                {...(props.onOpenBackgroundTask
                  ? { onOpenBackgroundTask: props.onOpenBackgroundTask }
                  : {})}
                {...(props.onStopBackgroundTask
                  ? { onStopBackgroundTask: props.onStopBackgroundTask }
                  : {})}
              />
              {props.backgroundTasks ? (
                <SubagentPermissionCards
                  tasks={props.backgroundTasks}
                  onAnswered={() => props.onBackgroundTasksChanged?.()}
                />
              ) : null}
            </div>
            <div className="chat-scroll-tail" aria-hidden />
          </div>

          <div className="chat-bottom">
            <div className="chat-bottom-inner" ref={composerHostRef}>
              <ScrollToBottomButton
                visible={showScrollToBottom}
                onClick={jumpToNewestMessage}
              />
              {readOnlyNotice ? null : (
                <UsageBanner
                  usage={props.providerUsage}
                  modelId={props.llmModel ?? ""}
                  {...(props.usageBannerDismissedKey
                    ? { dismissedKey: props.usageBannerDismissedKey }
                    : {})}
                  {...(props.onUsageBannerDismiss
                    ? { onDismiss: props.onUsageBannerDismiss }
                    : {})}
                />
              )}
              {readOnlyNotice ?? (
                <Composer
                  value={props.draft}
                  isEmpty={false}
                  providerUsage={props.providerUsage ?? null}
                  attachedFiles={attachedFiles}
                  onAttachedFilesChange={setAttachedFiles}
                  sessionId={props.sessionId}
                  contextIdle={false}
                  mode={props.mode}
                  modes={props.modes}
                  tokenUsage={props.tokenUsage}
                  {...(props.contextPct !== undefined
                    ? { contextPct: props.contextPct }
                    : {})}
                  {...(props.maxContextTokens !== undefined
                    ? { maxContextTokens: props.maxContextTokens }
                    : {})}
                  {...(props.contextBreakdown !== undefined
                    ? { contextBreakdown: props.contextBreakdown }
                    : {})}
                  {...(props.llmModels !== undefined &&
                  props.llmModels.length > 0 &&
                  props.onLlmModelChange !== undefined
                    ? {
                        llmModels: props.llmModels,
                        llmModel: props.llmModel,
                        onLlmModelChange: props.onLlmModelChange,
                        llmModelMultimodal: props.llmModelMultimodal,
                        ...(props.llmReasoningLevels !== undefined &&
                        props.llmReasoningLevels.length > 0 &&
                        props.onLlmReasoningChange !== undefined
                          ? {
                              llmReasoningLevels: props.llmReasoningLevels,
                              llmReasoning: props.llmReasoning,
                              onLlmReasoningChange: props.onLlmReasoningChange,
                            }
                          : {}),
                      }
                    : {})}
                  onModeChange={props.onModeChange}
                  {...(props.permissionMode !== undefined
                    ? { permissionMode: props.permissionMode }
                    : {})}
                  {...(props.configuredPermissionMode !== undefined
                    ? { configuredPermissionMode: props.configuredPermissionMode }
                    : {})}
                  {...(props.onPermissionModeChange
                    ? { onPermissionModeChange: props.onPermissionModeChange }
                    : {})}
                  {...(props.settingsOverrides
                    ? { settingsOverrides: props.settingsOverrides }
                    : {})}
                  onChange={props.onDraftChange}
                  onSend={props.onSend}
                  {...(props.onDocsCommand ? { onDocsCommand: props.onDocsCommand } : {})}
                  {...(props.onContextRingOpen
                    ? { onContextRingOpen: props.onContextRingOpen }
                    : {})}
                  {...(props.generating === true && props.onStop !== undefined
                    ? { generating: true, onStop: props.onStop }
                    : {})}
                  {...(props.onQueue
                    ? {
                        queuedMessages: props.queuedMessages ?? [],
                        onQueue: props.onQueue,
                        ...(props.onCancelQueued
                          ? { onCancelQueued: props.onCancelQueued }
                          : {}),
                      }
                    : {})}
                  {...(props.knownSkillNames
                    ? { knownSkillNames: props.knownSkillNames }
                    : {})}
                  {...(props.editingFiles && props.editingFiles.length > 0
                    ? { editingFiles: props.editingFiles }
                    : {})}
                  {...(props.onWorkspacePickFolder
                    ? {
                        workspaceCtx: props.workspaceCtx ?? null,
                        worktreePref: props.worktreePref ?? false,
                        workspaceLocked: props.workspaceLocked ?? false,
                        onWorkspacePickFolder: props.onWorkspacePickFolder,
                        onWorkspacePickBranch: props.onWorkspacePickBranch,
                        onWorktreeToggle: props.onWorktreeToggle,
                      }
                    : {})}
                />
              )}
            </div>
          </div>
        </div>
      )}
    </main>
  );
}
