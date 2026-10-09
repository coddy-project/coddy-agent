import {
  chatWorkspacePath,
  withWorkspaceQuery,
  workspaceScope,
} from "./chat/workspaceScope";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { CSSProperties } from "react";
import { ChatScreen } from "./chat/ChatScreen";
import { useStableHandler } from "./components/useStableHandler";
import type { QueuedMessage, QueueMode } from "./chat/Composer";
import { normalizeClipboardImageFile } from "./chat/clipboardImageFile";
import { fileFromDataUrl } from "./chat/dataUrlFile";
import {
  contextUsagePercent,
  withContextUsedTokens,
} from "./chat/contextUsage";
import { HERO_ACCENT_VERBS, pickHeroAccentVerb } from "./chat/heroTitleWords";
import { insertNewThinkingBeforeStreamingAssistant } from "./chat/transcriptThinkingPlacement";
import { openAIStreamErrorMessage } from "./chat/streamError";
import { optimisticUserFiles } from "./chat/optimisticUserFiles";
import {
  applyToolCallRows,
  readMessageCreatedAtUTC,
  reasoningDurationCacheKey,
  transcriptItemsFromMessages,
  type ToolCallListRow,
} from "./chat/transcriptFromMessages";
import type { RawUiLogRow } from "./chat/uiLogNotices";
import {
  alignedTranscriptSuffix,
  LIVE_WINDOW_REBASE_MESSAGES,
  parseTranscriptWindow,
  prependOlderPage,
  transcriptPageQuery,
  transcriptToolCallsQuery,
  type OlderTranscript,
  type TranscriptPageRequest,
  type TranscriptWindow,
} from "./chat/transcriptWindow";
import { getEnv, notifyLocalApiUnauthorized } from "./env/remoteEnv";
import {
  isAbortError,
  remoteHttpErrorMessage,
  remoteSendErrorMessage,
  errorDetail,
} from "./env/remoteErrors";
import { EnvHealthBanner } from "./env/EnvHealthBanner";
import { isNoLiveTurnRelayError } from "./chat/composerStreamError";
import { subscribeSharedServerEvents } from "./chat/sharedServerEvents";
import { readOpening, type OpeningRead } from "./chat/openingRead";
import {
  isNewerSettings,
  parseSessionSettings,
  permissionModeOfInfo,
  type SessionSettings,
  type SessionSettingsEvent,
  type TurnOverride,
} from "./chat/sessionSettings";
import {
  GOAL_RESUME_PROMPT,
  isNewerGoal,
  parseSessionGoalUpdate,
  type SessionGoal,
  type SessionGoalUpdate,
} from "./chat/goal";
import type { GoalActions } from "./chat/GoalPopover";
import { useSessionTurnActivity } from "./chat/useSessionTurnActivity";
import { mergeTurnProgress, type TurnProgress } from "./chat/turnProgress";
import type { QueuedMessageEvent } from "./chat/serverEvents";
import { QueueDeliveryOrder } from "./chat/messageQueueState";
import { useProviderUsage } from "./chat/useProviderUsage";
import { parseSSEBlocks } from "./chat/sse";
import {
  consumeComposerSseReader,
  type ContextUsageUpdate,
} from "./chat/consumeComposerSse";
import {
  parseCoddyPermissionPayload,
  type PermissionResolvedState,
} from "./chat/permissionTypes";
import {
  parseCoddyQuestionPayload,
  type QuestionResolvedState,
} from "./chat/questionTypes";
import { createDebouncedSessionStatsRefresh } from "./chat/sessionStatsPoll";
import {
  preserveTranscriptItemIds,
  stablePermissionPromptItemId,
} from "./chat/transcriptItemIds";
import {
  dedupeAdjacentDuplicateThinkingCompleted,
  keepLocalTranscriptIfServerEmpty,
  mergeTranscriptPreferLocalSuffix,
  preserveUserMessageFiles,
  revokeSupersededUserMessagePreviews,
} from "./chat/transcriptServerSnapshot";
import { pickStreamMutationBase } from "./chat/streamMutationBase";
import { ShadowTranscriptCache } from "./chat/sessionTranscriptCache";
import {
  mergePermissionPromptsIntoTranscript,
  permissionPendingSessionIdsFromStorage,
  upsertPermissionPromptRecord,
  clearPermissionPromptRecords,
} from "./chat/permissionPromptSessionStore";
import {
  parseToolsPermissionPolicy,
  type ToolsPermissionPolicy,
} from "./chat/toolsPermissionPolicy";
import { reattachLocalQuestionPrompts } from "./chat/transcriptQuestionReattach";
import { retireRelayedPermissionPrompts } from "./chat/relayedPermissionPrompts";
import { normalizeTodoPlanSnapshot } from "./chat/todoToolPreview";
import {
  clearQuestionPromptRecords,
  hasUnresolvedQuestionPrompt,
  mergeStoredQuestionPromptsIntoTranscript,
  patchQuestionToolArgsFromPromptRecords,
  upsertQuestionPromptRecord,
} from "./chat/questionPromptSessionStore";
import { transcriptHasFilledAssistant } from "./chat/streamSyncLocalAssistant";
import { applyMemoryRunToItems } from "./chat/memoryRun";
import type { TokenUsage, TranscriptItem } from "./chat/types";
import type { ProviderUsage } from "./chat/providerUsage";
import type {
  WorkspaceBranchFetch,
  WorkspaceContext,
} from "./chat/workspaceContext";
import { setHostShell } from "./chat/hostShell";
import { NavRail } from "./nav/NavRail";
import { shellStackMaxWidthMediaQuery } from "./shellBreakpoint";
import { SwarmView } from "./swarm/SwarmView";
import { probeSwarm } from "./swarm/api";
import {
  connectLocal,
  connectSwarmNode,
  connectSwarmRelay,
  environmentKey,
  localFetch,
  snapshotEnv,
  subscribeEnv,
  swarmMountPath,
  swarmRootRelay,
} from "./env/remoteEnv";
import {
  configuredRemoteFor,
  connectConfiguredRemote,
  refreshConfiguredRemotes,
  useConfiguredRemotes,
} from "./env/configuredRemotes";
import {
  isKnownRelayHome,
  knownPageServer,
  rememberPageServer,
  rememberRelayHome,
  rememberSchedulerLinked,
  schedulerLinkedGuess,
} from "./env/pageMemory";
import type { SessionsEnvironmentOption } from "./sessions/SessionsFilterMenu";
import {
  newChatWorkspaceIsReady,
  type PendingNewChatWorkspace,
} from "./sessions/newChatWorkspace";
import { readNavRailCookie, writeNavRailCookie } from "./nav/navRailCookie";
import { railCloseGuard, useRailScreenEscape } from "./nav/railEscape";
import { readLlmModelCookie, writeLlmModelCookie } from "./chat/llmModelCookie";
import {
  pickDefaultLlmModelForNewChat,
  pickLlmModelForOpenSession,
  sessionScopedModelCommand,
} from "./chat/llmModelSelection";
import {
  readReasoningCookie,
  writeReasoningCookie,
} from "./chat/reasoningCookie";
import { pickReasoningLevel } from "./chat/reasoningSelection";
import { SessionsSidebar } from "./sessions/SessionsSidebar";
import {
  reconcilePermissionPendingSessionIds,
  reconcileQuestionPendingSessionIds,
} from "./sessions/sessionRowActivity";
import { useConfirm } from "./components/useConfirm";
import { useT } from "./i18n/I18nProvider";
import { composerAutoFocusAllowed } from "./chat/composerFocus";
import type { SessionRow } from "./sessions/types";
import {
  type ArchiveMove,
  cursorAfterRemovals,
  overlayArchiveMoves,
  pruneArchiveBookkeeping,
  restoreRow,
  rowPlace,
  rowVisibleUnder,
} from "./sessions/archiveMoves";
import {
  DEFAULT_SESSION_GROUP_MODE,
  readSessionGroupCookie,
  writeSessionGroupCookie,
  type SessionGroupMode,
} from "./sessions/sessionGroups";
import {
  defaultSortOrder,
  DEFAULT_ARCHIVE_FILTER,
  DEFAULT_SESSION_SORT_KEY,
  isHistorySortKey,
  isSessionArchiveFilter,
  isSessionOriginFilter,
  type SessionArchiveFilter,
  type SessionOriginFilter,
  type SessionSortKey,
} from "./sessions/sessionQuery";
import {
  readSessionPref,
  SESSION_PREF_COOKIES,
  writeSessionPref,
} from "./sessions/sessionPrefs";
import {
  isClientDraftSessionId,
  mergeSessionsWithDrafts,
  newClientDraftId,
  readClientDraftSessions,
  removeClientDraftSession,
  upsertClientDraftSession,
  type ClientDraftSession,
} from "./sessions/draftSessions";
import { isRedundantSessionPick } from "./sessions/pickSessionGuard";
import { startSuggestSessionTitle } from "./sessionTitleSuggest";
import { extractAtFileAttachments } from "./skills/draftAt";
import {
  extractSessionAssetsXml,
  parseSessionAssetFiles,
  stripCoddyAttachmentsForUserDisplay,
} from "./skills/stripCoddyAttachments";
import {
  migrateWorkspaceAtRecents,
  recordWorkspaceAtRecent,
  WORKSPACE_AT_RECENTS_NO_SESSION_KEY,
} from "./skills/workspaceAtRecents";
import {
  schedulerCancelJob,
  schedulerClearJobRuns,
  schedulerListJobs,
  schedulerRunJob,
  setSchedulerSessionHeaders,
} from "./scheduler/api";
import {
  parseAppHash,
  setDraftHashInLocation,
  setHistoryHash,
  setSessionHashInLocation,
  schedulerEditorFromParsedHash,
  setSchedulerCreateHash,
  setSchedulerJobHash,
  setSchedulerJobRunsHash,
  setSchedulerListHash,
  setSessionTasksHash,
  setSettingsHash,
  setSettingsSectionHash,
  stripHistorySidebarFromHash,
  appNavHrefSwarm,
  appNavHrefDocs,
  setDocsHash,
} from "./scheduler/hashRoute";
import { DocsView } from "./docs/DocsView";
import { FilesView } from "./files/FilesView";
import { useFilesWindowKey } from "./files/filesWindowKey";
import { isFilesHotkey } from "./files/filesHotkey";
import {
  readLastWorkspaceDir,
  readWorktreePref,
  writeLastWorkspaceDir,
  writeWorktreePref,
} from "./chat/workspaceCookies";
import { onOpenWorkspaceFile } from "./files/fileBus";
import { relativeFilePath } from "./files/api";
import { useRightDock, useRightDockEscape } from "./components/useRightDock";
import { finishedToolCalls } from "./changes/toolActivity";
import {
  setSessionFilesHash,
  setSessionChangesHash,
} from "./scheduler/hashRoute";
import { fetchDocsPage } from "./docs/api";
import { docsCommandOpensPage } from "./docs/docsCommand";
import { SchedulerJobEditorSheet } from "./scheduler/SchedulerJobEditorSheet";
import { SchedulerJobsDrawer } from "./scheduler/SchedulerJobsDrawer";
import {
  BackgroundTasksPanel,
  type TaskFocus,
} from "./tasks/BackgroundTasksPanel";
import { EditsView } from "./changes/EditsView";
import { emitChangesSettled } from "./changes/sessionChangesBus";
import {
  clearFinishedBackgroundTasks,
  getBackgroundTask,
  listBackgroundTasks,
  stopBackgroundTask,
} from "./tasks/api";
import { tasksPollIntervalMs } from "./tasks/taskStatus";
import {
  parseSubagentTranscriptMeta,
  type SubagentTranscriptMeta,
} from "./chat/subagentTranscript";
import type { BackgroundTask } from "./tasks/types";
import type { SchedulerInfo, SchedulerJob } from "./scheduler/types";
import { parseSchedulerJobRef, schedulerJobRef } from "./scheduler/types";
import { Settings } from "./settings/Settings";
import { noteSettingsConfigReloaded } from "./settings/settingsConfigStore";
import { MCP_SECTION_ID } from "./settings/settingsSections";
import { wideRailMinWidthMediaQuery } from "./shellBreakpoint";

const HDR = "X-Coddy-Session-ID";

async function markCoddySessionActivityRead(id: string): Promise<void> {
  const t = id.trim();
  if (!t) {
    return;
  }
  try {
    await fetch(`/coddy/sessions/${encodeURIComponent(t)}`, {
      method: "PATCH",
      headers: {
        "Content-Type": "application/json",
        [HDR]: t,
      },
      body: JSON.stringify({ markActivityRead: true }),
    });
  } catch {
    // ignore
  }
}

/** Poll job list while scheduler UI is open (running, next_run_utc, paused). */
const SCHEDULER_JOBS_POLL_MS = 12_000;

type SchedulerEditorState =
  | null
  | { mode: "create" }
  | { mode: "edit"; jobId: string }
  /** The job's runs panel, docked where the editor docks; taskId is the run open in it. */
  | { mode: "runs"; jobId: string; taskId: string | null };

type ToolCallUpdate = {
  toolCallId: string;
  title?: string;
  kind?: string;
  status?: string;
};

type ToolCallStatusUpdate = {
  toolCallId: string;
  status?: string;
  content?: Array<{ type: string; content: { type: string; text?: string } }>;
  _meta?: {
    coddy?: {
      toolResultPreview?: { truncated?: boolean; totalLines?: number };
    };
  };
};

function toolSseShowsTruncatedPreview(u: ToolCallStatusUpdate): boolean {
  const p = u._meta?.coddy?.toolResultPreview;
  return !!(p && p.truncated === true);
}

type ModelInfo = {
  id: string;
  ownedBy?: string;
  maxContextTokens?: number | undefined;
  multimodal?: boolean;
  reasoningLevels?: string[];
  reasoningDefault?: string;
};

const PROFILE_MODES = ["agent", "plan", "ask"] as const;

// SessionContextWindow is the window GET .../stats reports for a session,
// named with the model it belongs to.
type SessionContextWindow = {
  model: string;
  tokens: number;
  source?: string;
};

type SessionStats = {
  tokenUsageTotal?: {
    inputTokens: number;
    outputTokens: number;
    totalTokens: number;
  };
  contextBreakdown?: {
    systemPrompt: number;
    toolDefinitions: number;
    rules: number;
    skills: number;
    mcp: number;
    subagents: number;
    conversation: number;
    estimatedTotal: number;
  };
};

function randomSessionId(): string {
  const hex = [...crypto.getRandomValues(new Uint8Array(18))]
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  return `sess_${hex}`;
}

/** One page of GET /coddy/sessions. */
type SessionsPage = {
  sessions: SessionRow[];
  nextCursor?: string | null;
  hasMore?: boolean;
  active_count?: number;
};

async function fetchJSON<T>(
  path: string,
  init?: RequestInit,
): Promise<{ ok: boolean; status: number; data?: T }> {
  const res = await fetch(path, init);
  const status = res.status;
  if (!res.ok) {
    return { ok: false, status };
  }
  const data = (await res.json()) as T;
  return { ok: true, status, data };
}

function newId(prefix: string): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(16).slice(2)}`;
}

function isStackedShell(): boolean {
  return (
    typeof window !== "undefined" &&
    window.matchMedia(shellStackMaxWidthMediaQuery).matches
  );
}

export function App() {
  const { t, locale } = useT();
  const confirm = useConfirm();
  const [knownSkillNames, setKnownSkillNames] = useState<Set<string>>(
    () => new Set(),
  );
  // The URL hash is known before the first paint: parse it once and seed
  // the route-derived state, otherwise the first frame renders the hero and
  // applyLocationHash's mount effect swaps the whole layout one frame later.
  // initialRoute is a one-shot boot snapshot - hash changes are handled by
  // applyLocationHash, never read this value for the current route.
  const [initialRoute] = useState(() => {
    try {
      return parseAppHash();
    } catch {
      // A malformed escape must not take the router down, and a boot-time
      // throw during render is worse than a late one in the mount effect.
      return { branch: "none" as const, historyOpen: false };
    }
  });
  const [sessionId, setSessionId] = useState(() =>
    initialRoute.branch === "session" ? initialRoute.sessionId : "",
  );
  /** Increments on each explicit "new chat" home transition so the hero verb rotates. */
  const [heroHomeGeneration, setHeroHomeGeneration] = useState(() =>
    Math.floor(Math.random() * HERO_ACCENT_VERBS.length),
  );
  const [sessions, setSessions] = useState<SessionRow[]>([]);
  const [historyActiveCount, setHistoryActiveCount] = useState(0);
  const [sessionsCursor, setSessionsCursor] = useState<string | null>(null);
  const sessionsCursorRef = useRef<string | null>(null);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  const [items, setItems] = useState<TranscriptItem[]>([]);
  const [sessionLoading, setSessionLoading] = useState(
    () => initialRoute.branch === "session",
  );
  // Whether the session on screen still waits, as the retries of its opening
  // read ask it after a pause: the state their render held is long gone.
  const sessionLoadingRef = useRef(sessionLoading);
  sessionLoadingRef.current = sessionLoading;
  /**
   * The status of a session's last transcript read that failed, 0 when it got
   * no answer: for the read that opens the session, a 404 says the server has
   * no such session and anything else is read again.
   */
  const failedReadStatusRef = useRef(new Map<string, number>());
  const [sessionFadingOut, setSessionFadingOut] = useState(false);
  const fadeOutTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const itemsRef = useRef<TranscriptItem[]>([]);
  itemsRef.current = items;
  const [editingUserMsgIdx, setEditingUserMsgIdx] = useState<number | null>(
    null,
  );
  const [editingAssetNote, setEditingAssetNote] = useState("");
  const [editingFiles, setEditingFiles] = useState<
    { name: string; mimeType: string }[]
  >([]);
  // What the edit banner names: the text of the prompt being edited.
  const [editingSnippet, setEditingSnippet] = useState("");
  // The draft and attachments the pencil replaced, put back when the edit is
  // cancelled; null while no edit is open.
  const editingPrevDraftRef = useRef<{ draft: string; files: File[] } | null>(
    null,
  );
  // The prompt the last rewind edited while the server can still take it
  // back (rewindUndo of GET .../messages), for the session it was read for.
  const [rewindUndo, setRewindUndo] = useState<{
    sid: string;
    userMsgIdx: number;
  } | null>(null);
  // "sid:index" of an undo whose composer banner was put away; the Undo on the
  // prompt stays.
  const [rewindUndoDismissed, setRewindUndoDismissed] = useState("");
  const [rewindUndoBusy, setRewindUndoBusy] = useState(false);
  // Held synchronously, so two clicks inside one render post one undo.
  const rewindUndoBusyRef = useRef(false);
  const [draft, setDraft] = useState(() => {
    if (initialRoute.branch !== "draft") return "";
    const id = initialRoute.draftId.trim();
    const row = readClientDraftSessions().find((r) => r.localId === id);
    return row?.draftText || "";
  });
  // The files attached in the composer. Held here, not in the composer, so a
  // send the server never took can put them back next to the text.
  const [composerFiles, setComposerFiles] = useState<File[]>([]);
  // Workspace context chips: folder / git branch / worktree state per session.
  const [workspaceCtx, setWorkspaceCtx] = useState<WorkspaceContext | null>(
    null,
  );
  // The worktree checkbox is this browser's choice for every folder (cookie).
  const [worktreePref, setWorktreePref] = useState(() => readWorktreePref());
  // Pre-session workspace choices, applied right before the first send creates the session.
  const pendingWorkspaceRef = useRef<{
    path?: string;
    branch?: string;
    worktree?: boolean;
  } | null>(null);
  // The folder of pendingWorkspaceRef as state, set the moment it is picked:
  // what a new chat lists and highlights (skills, mentions, subagents) follows
  // the pick at once, never a preview answer that may still be in flight.
  const [pendingWorkspacePath, setPendingWorkspacePath] = useState("");
  const setPendingWorkspace = useCallback(
    (next: { path?: string; branch?: string; worktree?: boolean } | null) => {
      pendingWorkspaceRef.current = next;
      setPendingWorkspacePath(next?.path ?? "");
    },
    [],
  );
  // Every answer that sets workspaceCtx takes a ticket; only the latest one
  // applies, so a slow preview of a folder picked earlier never paints over
  // the folder picked after it.
  const workspaceCtxGenRef = useRef(0);
  const chatWorkspace = chatWorkspacePath(
    sessionId,
    pendingWorkspacePath,
    workspaceCtx?.path,
  );
  const filesWindowKey = useFilesWindowKey(sessionId, workspaceCtx?.path || "");
  const [clientDraftSessions, setClientDraftSessions] = useState<
    ClientDraftSession[]
  >(() => readClientDraftSessions());
  const [activeDraftId, setActiveDraftId] = useState(() =>
    initialRoute.branch === "draft" ? initialRoute.draftId.trim() : "",
  );
  const [permissionPendingSids, setPermissionPendingSids] = useState<
    Set<string>
  >(() => new Set(permissionPendingSessionIdsFromStorage()));
  const [toolsPermissionPolicy, setToolsPermissionPolicy] =
    useState<ToolsPermissionPolicy | null>(null);
  const toolsPermissionPolicyRef = useRef<ToolsPermissionPolicy | null>(null);
  const [questionPendingSids, setQuestionPendingSids] = useState<Set<string>>(
    () => new Set(),
  );
  const [tokenUsage, setTokenUsage] = useState<TokenUsage | null>(null);
  const [contextBreakdown, setContextBreakdown] = useState<NonNullable<
    SessionStats["contextBreakdown"]
  > | null>(null);
  const [compactionSettings, setCompactionSettings] = useState({
    enabled: true,
    autoEnabled: true,
    threshold: 80,
  });
  // A provider listing can arrive after /v1/models returned its fallback.
  // Keep the live window per session, scoped to its model and config version;
  // stats refreshes must not replace it with the earlier model-list value.
  const [sessionContextWindows, setSessionContextWindows] = useState<
    Record<string, { model: string; epoch: number; size: number }>
  >({});
  // The live config version, readable from handlers declared above the state
  // that holds it.
  const configEpochRef = useRef(0);
  // The window GET .../stats reports, which names its own model instead of
  // borrowing the composer's. It is the authority when the two disagree: a
  // live frame is labelled with what the tab was showing when it arrived.
  const recordSessionContextWindow = useStableHandler(
    (sid: string, w: SessionContextWindow | null | undefined) => {
      const key = sid.trim();
      if (!key || !w || !(w.tokens > 0) || !w.model) {
        return;
      }
      setSessionContextWindows((prev) => ({
        ...prev,
        [key]: {
          model: w.model,
          epoch: configEpochRef.current,
          size: w.tokens,
        },
      }));
    },
  );

  const applySessionStatsPayload = useCallback(
    (stats: SessionStats | null | undefined, viewing: boolean) => {
      if (!viewing) {
        return;
      }
      if (stats?.tokenUsageTotal) {
        const t = stats.tokenUsageTotal;
        tokenBaselineRef.current = {
          input: t.inputTokens || 0,
          output: t.outputTokens || 0,
          total: t.totalTokens || 0,
        };
        setTokenUsage({
          inputTokens: tokenBaselineRef.current.input,
          outputTokens: tokenBaselineRef.current.output,
          totalTokens: tokenBaselineRef.current.total,
        });
      }
      if (stats?.contextBreakdown) {
        setContextBreakdown(stats.contextBreakdown);
      }
    },
    [],
  );

  const refreshSessionStats = useCallback(
    async (sid: string) => {
      const key = sid.trim();
      if (!key) {
        return;
      }
      const statsRes = await fetchJSON<{
        stats?: SessionStats | null;
        contextWindow?: SessionContextWindow | null;
      }>(`/coddy/sessions/${encodeURIComponent(key)}/stats`, {
        headers: { [HDR]: key },
      });
      if (!statsRes.ok) {
        return;
      }
      // The session's own window, named with the model it belongs to. Recorded
      // whether or not this session is the one on screen: a window learnt while
      // the tab was showing another chat is exactly what would otherwise be
      // missed, leaving the ring on the model-list fallback until the next turn.
      recordSessionContextWindow(key, statsRes.data?.contextWindow);
      applySessionStatsPayload(
        statsRes.data?.stats,
        viewedSessionIdRef.current.trim() === key,
      );
    },
    [applySessionStatsPayload, recordSessionContextWindow],
  );

  const debouncedRefreshSessionStats = useMemo(
    () =>
      createDebouncedSessionStatsRefresh((sid) => {
        void refreshSessionStats(sid);
      }),
    [refreshSessionStats],
  );

  const markViewedSessionActivityRead = useCallback((sid: string) => {
    const key = sid.trim();
    if (!key) return;
    if (viewedSessionIdRef.current.trim() !== key) return;
    void markCoddySessionActivityRead(key);
  }, []);
  const tokenBaselineRef = useRef<{
    input: number;
    output: number;
    total: number;
  }>({ input: 0, output: 0, total: 0 });
  /**
   * Per-session shadow transcript while that session streams in the
   * background, kept as a small LRU (see sessionTranscriptCache.ts). Every
   * write through `set` records recency, so `evictStaleSessionCaches` sees
   * every entry.
   */
  const streamShadowBySidRef = useRef(
    new ShadowTranscriptCache<TranscriptItem[]>(),
  );
  /**
   * The live window of each session this client holds a transcript for: the
   * page of the history its items start at (issue #338), and `seq`, the read
   * that established it. Every reload of a session re-reads from that page,
   * so a long history is never read whole again after the first screen.
   */
  const liveWindowBySidRef = useRef(
    new Map<string, TranscriptWindow & { seq: number }>(),
  );
  /** Counter of reads that start a live window over (the newest page). */
  const windowRebaseSeqRef = useRef(0);
  /** The latest window-starting read in flight per session. */
  const windowRebaseInFlightRef = useRef(new Map<string, number>());
  /** The live window of the session on screen, for rendering. */
  const [viewedLiveWindow, setViewedLiveWindow] = useState<
    (TranscriptWindow & { sessionId: string; seq: number }) | null
  >(null);
  /**
   * Older pages of the session on screen, read while the reader scrolled up
   * past the live window. History that no longer changes: it takes no part
   * in the reloads and merges of the live window and is shown above it.
   */
  const [olderTranscript, setOlderTranscript] =
    useState<OlderTranscript | null>(null);
  const olderTranscriptRef = useRef<OlderTranscript | null>(null);
  olderTranscriptRef.current = olderTranscript;
  const [olderTranscriptLoad, setOlderTranscriptLoad] = useState<
    "idle" | "loading" | "error"
  >("idle");
  const olderLoadInFlightRef = useRef<string>("");
  /** Whether the reader sits at the newest message (ChatScreen reports it). */
  const readerAtTailRef = useRef(true);
  const postAbortBySidRef = useRef<Map<string, AbortController>>(new Map());
  const relayAbortBySidRef = useRef<Map<string, AbortController>>(new Map());
  const pendingPostBySidRef = useRef(new Map<string, AbortController>());
  const streamGenerationBySidRef = useRef(new Map<string, number>());

  const activatedMCPSelectionsRef = useRef(new Set<string>());
  const relayAttachPendingRef = useRef(new Set<string>());
  const stopPendingBySidRef = useRef(
    new Map<string, { superseded: boolean }>(),
  );
  const stoppedTurnBySidRef = useRef(new Map<string, number>());
  /** Last composer relay frame id seen per session, so a re-attach can resume from it. */
  const relayLastEventIdBySidRef = useRef<Map<string, string>>(new Map());
  const streamingAssistantBySidRef = useRef<Map<string, string>>(new Map());
  /** Session ids with an active client-side composer POST or GET relay. */
  const activeComposerSidRef = useRef<Set<string>>(new Set());
  const [composerActivityEpoch, setComposerActivityEpoch] = useState(0);
  /** Session id currently shown in the transcript (updated synchronously on navigation). */
  const viewedSessionIdRef = useRef(
    initialRoute.branch === "session" ? initialRoute.sessionId.trim() : "",
  );
  /** True while GET /coddy/events is connected; gates the fallback sessions poll. */
  const [serverEventsConnected, setServerEventsConnected] = useState(false);
  const serverEventHandlersRef = useRef<{
    turnStarted: (sid: string) => void;
    turnEnded: (sid: string) => void;
    providerUsage: (usage: ProviderUsage) => void;
    configReloaded: () => void;
    messageQueue: (sid: string, queue: QueuedMessageEvent) => void;
    sessionSettings: (event: SessionSettingsEvent) => void;
    sessionGoal: (update: SessionGoalUpdate) => void;
    subagentPermission: (parentSid: string) => void;
    questionPending: (sid: string) => void;
    sessionRewound: (sid: string) => void;
    ready: () => void;
  }>({
    turnStarted: () => {},
    turnEnded: () => {},
    providerUsage: () => {},
    configReloaded: () => {},
    messageQueue: () => {},
    sessionSettings: () => {},
    sessionGoal: () => {},
    subagentPermission: () => {},
    questionPending: () => {},
    sessionRewound: () => {},
    ready: () => {},
  });
  // Provider account usage for the composer pill and banner: read over REST
  // at session open, model change and after each viewed turn, pushed by the
  // events stream in between (chat/useProviderUsage.ts).
  const [providerUsageTurnEpoch, setProviderUsageTurnEpoch] = useState(0);
  const noteUsageTurnEnded = useCallback((sid: string) => {
    if (viewedSessionIdRef.current.trim() === sid.trim()) {
      setProviderUsageTurnEpoch((n) => n + 1);
    }
  }, []);
  const bumpComposerActivity = () =>
    setComposerActivityEpoch((n) => (n + 1) % 1_000_000_000);

  function addActiveComposer(sid: string) {
    const k = sid.trim();
    if (!k) return;
    if (activeComposerSidRef.current.has(k)) return;
    activeComposerSidRef.current.add(k);
    bumpComposerActivity();
  }

  function removeActiveComposer(sid: string) {
    const k = sid.trim();
    if (!k) return;
    if (!activeComposerSidRef.current.delete(k)) return;
    bumpComposerActivity();
  }

  function applyStreamItemsForSession(
    streamSid: string,
    fn: (prev: TranscriptItem[]) => TranscriptItem[],
  ) {
    const key = streamSid.trim();
    if (!key) return;
    const viewing = viewedSessionIdRef.current.trim();
    const base = pickStreamMutationBase({
      mutationSessionId: key,
      viewingSid: viewing,
      shadow: streamShadowBySidRef.current.get(key),
      hasActiveComposer: activeComposerSidRef.current.has(key),
      itemsWhenViewingMatches: itemsRef.current,
    });
    const prevShadowLen = streamShadowBySidRef.current.get(key)?.length ?? 0;
    const next = fn(base);
    if (next.length === 0 && prevShadowLen > 0 && base.length === 0) {
      return;
    }
    streamShadowBySidRef.current.set(key, next);
    if (viewing === key) {
      itemsRef.current = next;
    }
    setItems((prev) => {
      const v = viewedSessionIdRef.current.trim();
      if (v === key) {
        return next;
      }
      return prev;
    });
  }

  /**
   * Drops least-recently-used shadow transcripts beyond the cache cap so old
   * dialogs stop accumulating in memory. The session about to be viewed and
   * any session with a live composer stream are pinned; evicted sessions are
   * simply re-fetched via loadMessages on the next visit.
   */
  function evictStaleSessionCaches(nextViewedSid: string) {
    const active = new Set<string>(activeComposerSidRef.current);
    for (const k of postAbortBySidRef.current.keys()) active.add(k);
    for (const k of relayAbortBySidRef.current.keys()) active.add(k);
    for (const k of streamingAssistantBySidRef.current.keys()) active.add(k);
    const victims = streamShadowBySidRef.current.evict({
      viewedSid: nextViewedSid,
      activeStreamSids: active,
    });
    for (const sid of victims) {
      relayLastEventIdBySidRef.current.delete(sid);
      if (sid !== nextViewedSid.trim()) {
        liveWindowBySidRef.current.delete(sid);
        windowRebaseInFlightRef.current.delete(sid);
      }
    }
  }

  /**
   * The live window this tab holds for `sid`. A session it has rows of
   * without having read a page - one it started and streamed from its first
   * message - is held from message 0.
   */
  function heldLiveWindow(
    sid: string,
  ): { offset: number; seq: number } | undefined {
    const held = liveWindowBySidRef.current.get(sid);
    if (held) return held;
    const hasLocalRows =
      (streamShadowBySidRef.current.get(sid)?.length ?? 0) > 0 ||
      (viewedSessionIdRef.current.trim() === sid &&
        itemsRef.current.length > 0);
    return hasLocalRows ? { offset: 0, seq: 0 } : undefined;
  }

  /** The query that re-reads the live window held for `sid`. */
  function liveWindowQuery(sid: string): string {
    const held = heldLiveWindow(sid.trim());
    return transcriptPageQuery(
      held ? { kind: "from", offset: held.offset } : { kind: "tail" },
    );
  }

  /** Records the live window a read of `sid` established. */
  function noteLiveWindow(sid: string, win: TranscriptWindow, seq: number) {
    const prev = liveWindowBySidRef.current.get(sid);
    liveWindowBySidRef.current.set(sid, { ...win, seq });
    if (prev && prev.seq !== seq) {
      // A window started over: the older pages read against the old one are
      // released rather than joined to a window they no longer border.
      setOlderTranscript((cur) =>
        cur && cur.sessionId === sid && cur.generation !== seq ? null : cur,
      );
    }
    if (viewedSessionIdRef.current.trim() === sid) {
      setViewedLiveWindow({ ...win, sessionId: sid, seq });
    }
  }

  /**
   * The message queue per session: follow-ups the operator wrote while a turn
   * was running, waiting for it to read them at its next step.
   *
   * The server owns the list. Every entry here came back from the queue routes
   * or from a `message_queue` frame on the turn's own stream, so a second tab
   * watching the same session shows the same queue, and a message the agent has
   * just read disappears from it without the client guessing.
   */
  const [queueBySid, setQueueBySid] = useState<Record<string, QueuedMessage[]>>(
    {},
  );
  const [queueMode, setQueueMode] = useState<QueueMode | undefined>();
  /**
   * Highest queue version applied per session.
   *
   * The same change reaches this client twice - once on the turn's own stream,
   * once on `GET /coddy/events` - and those are separate connections, so the
   * frames can arrive in either order. The version decides; without it a stale
   * frame would put a cancelled message back on screen.
   */
  const queueOrderRef = useRef(new QueueDeliveryOrder());
  const applyQueue = useCallback(
    (sid: string, rows: QueuedMessage[], version: number, epoch?: number) => {
      const key = sid.trim();
      if (!queueOrderRef.current.accept(key, version, epoch)) {
        return;
      }
      setQueueBySid((prev) => ({ ...prev, [key]: rows }));
    },
    [],
  );
  // The running turn's clock and generated tokens per session, from the turn's own
  // stream and from the activity read that covers a tab which joined late.
  const [turnProgressBySid, setTurnProgressBySid] = useState<
    Record<string, TurnProgress>
  >({});
  const applyTurnProgress = useStableHandler(
    (sid: string, next: TurnProgress, source: "stream" | "activity") => {
      const key = sid.trim();
      if (!key) return;
      setTurnProgressBySid((prev) => {
        const merged = mergeTurnProgress(prev[key], next, source);
        return merged === prev[key] ? prev : { ...prev, [key]: merged };
      });
    },
  );
  const clearTurnProgress = useStableHandler((sid: string) => {
    const key = sid.trim();
    setTurnProgressBySid((prev) => {
      if (!(key in prev)) return prev;
      const { [key]: _ended, ...rest } = prev;
      return rest;
    });
  });

  const turnActivity = useSessionTurnActivity({
    sessionId,
    connected: serverEventsConnected,
    onTurnProgress: (sid, progress) =>
      progress
        ? applyTurnProgress(sid, progress, "activity")
        : clearTurnProgress(sid),
    postPending: (sid) => pendingPostBySidRef.current.has(sid),
    onQueueRead: (sid) => {
      const fence = queueOrderRef.current.capture(sid);
      return (queue) => {
        if (queueOrderRef.current.acceptSnapshot(sid, queue.version, fence)) {
          setQueueBySid((prev) => ({ ...prev, [sid]: queue.messages }));
        }
      };
    },
    onReconcile: (sid, active, wasActive) => {
      if (active) void attachViewedComposer(sid);
      else if (wasActive) reconcileEndedTurn(sid);
    },
  });
  const generating =
    sessionId.trim() !== "" &&
    (turnActivity.get(sessionId) ??
      activeComposerSidRef.current.has(sessionId.trim()));
  const queuedMessages = queueBySid[sessionId.trim()] ?? [];

  function reconcileEndedTurn(sid: string) {
    removeActiveComposer(sid);
    // The next turn starts its own clock; what this one reached is history.
    clearTurnProgress(sid);
    if (
      sid !== viewedSessionIdRef.current.trim() &&
      !streamShadowBySidRef.current.has(sid)
    )
      return;
    noteUsageTurnEnded(sid);
    // A window that grew through many turns starts over from its end here,
    // where the turn is over, unless the reader is up in its history: a
    // session that runs turn after turn is never idle long enough for the
    // slide below, and every reload would read everything since it opened.
    const viewing = viewedSessionIdRef.current.trim() === sid;
    const live = liveWindowBySidRef.current.get(sid);
    const rebase =
      !!live &&
      live.total - live.offset > LIVE_WINDOW_REBASE_MESSAGES &&
      (!viewing || readerAtTailRef.current);
    void loadMessages(sid, {
      preserveOnError: true,
      skipSetItems: !viewing,
      ...(rebase ? { rebase: true } : {}),
    }).then(() => {
      if (viewedSessionIdRef.current.trim() === sid) slideTranscriptToTail();
    });
    void refreshSessionStats(sid);
  }

  async function attachViewedComposer(sid: string) {
    const canAttach = () =>
      viewedSessionIdRef.current.trim() === sid &&
      turnActivity.get(sid) === true &&
      !postAbortBySidRef.current.has(sid) &&
      !relayAbortBySidRef.current.has(sid) &&
      // A Stop in flight has released the stream on purpose; its outcome
      // decides whether this turn is watched again.
      !stopPendingBySidRef.current.has(sid) &&
      stoppedTurnBySidRef.current.get(sid) !== turnActivity.generation(sid);
    if (!canAttach() || relayAttachPendingRef.current.has(sid)) return;
    relayAttachPendingRef.current.add(sid);
    try {
      const generation = turnActivity.generation(sid);
      const loaded = await loadMessages(sid, {
        preserveOnError: true,
        freshLoad: !streamShadowBySidRef.current.has(sid),
      });
      if (
        loaded &&
        canAttach() &&
        turnActivity.generation(sid) === generation
      ) {
        void rejoinComposerLiveStream(sid, loaded);
      }
    } catch {
      // An attach read failed; the activity reconciler retries without declaring idle.
    } finally {
      relayAttachPendingRef.current.delete(sid);
    }
  }

  // Text of the most recent user turn, used to re-run it from the retry button
  // on a failed/system notice (e.g. "model did not respond"). A turn a finished
  // background task or the goal supervisor started has no text anybody typed,
  // so there is nothing to re-run and no retry is offered.
  const lastUserText = useMemo(() => {
    for (let i = items.length - 1; i >= 0; i--) {
      const it = items[i];
      if (it && (it.type === "background_wake" || it.type === "goal_turn"))
        return "";
      if (it && it.type === "user_message") {
        return typeof it.content === "string" ? it.content : "";
      }
    }
    return "";
  }, [items]);

  useEffect(() => {
    const sid = sessionId.trim();
    if (!sid || !generating) {
      return;
    }
    void refreshSessionStats(sid);
    const timer = window.setInterval(() => {
      void refreshSessionStats(sid);
    }, 800);
    return () => window.clearInterval(timer);
  }, [sessionId, generating, refreshSessionStats]);

  const sidebarActiveId = sessionId.trim() || activeDraftId.trim();
  // Read by the stable Settings callback below, which must see the session on
  // screen now rather than the one captured when it was created.
  const sidebarActiveIdRef = useRef(sidebarActiveId);
  sidebarActiveIdRef.current = sidebarActiveId;

  const sessionsForSidebar = useMemo(() => {
    const rows = mergeSessionsWithDrafts(sessions, clientDraftSessions);
    // The open conversation's row follows this tab's own view of its turn, not the
    // last listing: the listing is refreshed on a poll, and the activity dot must
    // not trail a turn the reader is watching start or end. Only turnActive is
    // this tab's to override - the rest of the row, backgroundRunning included,
    // is the server's answer and is carried through untouched.
    const open = sessionId.trim();
    if (!open) return rows;
    return rows.map((row) =>
      row.id === open && !!row.turnActive !== generating
        ? { ...row, turnActive: generating }
        : row,
    );
  }, [sessions, clientDraftSessions, sessionId, generating, t]);

  const reasoningDurationMsByContentRef = useRef<Map<string, number>>(
    new Map(),
  );
  const [modelInfos, setModelInfos] = useState<ModelInfo[]>([]);
  /**
   * Bumped whenever the server's configuration moved: a settings save here, or a
   * `config_reloaded` event from a swap made elsewhere (the agent's `config_commit`,
   * a skill install, another tab). Everything derived from the config re-reads on it,
   * so a model added mid-session reaches the picker without a page reload.
   */
  const [configEpoch, setConfigEpoch] = useState(0);
  useEffect(() => {
    configEpochRef.current = configEpoch;
  }, [configEpoch]);
  const [sessionsOpen, setSessionsOpen] = useState(
    () =>
      initialRoute.branch === "history" ||
      ((initialRoute.branch === "session" ||
        initialRoute.branch === "draft" ||
        initialRoute.branch === "none") &&
        initialRoute.historyOpen),
  );
  // The environment this app was started on. A switch to another remote
  // starts another app (EnvScope), so an answer that lands here after one is
  // the old environment's, and must not be kept for the new one.
  const [appEnvKey] = useState(() => environmentKey(getEnv()));
  const isAppEnvironment = useCallback(
    () => environmentKey(getEnv()) === appEnvKey,
    [appEnvKey],
  );
  /**
   * null until first probe of /coddy/scheduler/jobs; false when route returns
   * 404 (binary without scheduler). Started over by a switch between remotes
   * (EnvScope), it begins from the last answer so the rail keeps its shape.
   */
  const [schedulerHttpLinked, setSchedulerHttpLinkedState] = useState<
    boolean | null
  >(() => schedulerLinkedGuess(appEnvKey));
  const setSchedulerHttpLinked = useCallback(
    (linked: boolean) => {
      if (isAppEnvironment()) {
        rememberSchedulerLinked(appEnvKey, linked);
      }
      setSchedulerHttpLinkedState(linked);
    },
    [appEnvKey, isAppEnvironment],
  );
  const [schedulerOpen, setSchedulerOpen] = useState(false);
  const [settingsRoute, setSettingsRoute] = useState(
    () => initialRoute.branch === "settings",
  );
  const [swarmRoute, setSwarmRoute] = useState(
    () => initialRoute.branch === "swarm",
  );
  // The documentation reader, open on a page (and section) of the built-in
  // documentation; null when it is closed. lastDocsSlugRef remembers the page
  // the reader was left on, so the rail and F1 reopen the book where it was.
  const [docsRoute, setDocsRoute] = useState<{
    slug: string | null;
    anchor: string | null;
  } | null>(() =>
    initialRoute.branch === "docs"
      ? { slug: initialRoute.slug, anchor: initialRoute.anchor }
      : null,
  );
  const lastDocsSlugRef = useRef<string | null>(
    initialRoute.branch === "docs" ? initialRoute.slug : null,
  );
  // Where the reader was opened from (a chat, the swarm, the scheduler), so
  // closing it goes back there rather than home.
  const docsReturnHashRef = useRef("");
  // The Swarm entry only appears when the environment answers as a relay: on a
  // plain agent there is no swarm to show. A node reached through a relay is in
  // one from the start, and a remote last found to be a relay starts as one
  // (a switch in place starts the app over; pageMemory.ts).
  const [isSwarmEnv, setIsSwarmEnv] = useState(() => {
    const env = getEnv();
    return (
      env.mode === "remote" &&
      (!!env.swarmRelay || isKnownRelayHome(env.baseUrl))
    );
  });
  /**
   * True while the environment is a relay itself rather than a node reached
   * through one.
   *
   * A relay holds no sessions, no workspace and no model of its own - it serves
   * no /coddy/* at all - so a chat box and a history drawer there are furniture
   * for a room nobody can enter. What it does have is the swarm, so that is
   * what it shows.
   */
  const [atSwarmRoot, setAtSwarmRoot] = useState(() => {
    const env = getEnv();
    return (
      env.mode === "remote" && !env.swarmRelay && isKnownRelayHome(env.baseUrl)
    );
  });
  // Active Settings section id from `#/settings/<section>` (null = default/grid).
  const [settingsSection, setSettingsSection] = useState<string | null>(() =>
    initialRoute.branch === "settings" ? initialRoute.section : null,
  );
  // The row a list section has open, from `?id=` (a provider or model name).
  const [settingsItem, setSettingsItem] = useState<string | null>(() =>
    initialRoute.branch === "settings" ? initialRoute.item : null,
  );
  const [schedulerEditor, setSchedulerEditor] =
    useState<SchedulerEditorState>(null);
  const [schedulerJobs, setSchedulerJobs] = useState<SchedulerJob[]>([]);
  const [schedulerInfo, setSchedulerInfo] = useState<SchedulerInfo | null>(
    null,
  );
  const [schedulerListError, setSchedulerListError] = useState<string | null>(
    null,
  );
  const [schedulerListLoading, setSchedulerListLoading] = useState(false);
  const [schedulerFilterDraft, setSchedulerFilterDraft] = useState("");
  const [schedulerFilterQ, setSchedulerFilterQ] = useState("");
  const schedulerDockClusterRef = useRef<HTMLDivElement>(null);
  // The runs of the job whose runs panel is open: the background tasks of the
  // job's session, polled the way the chat's Tasks panel polls its own.
  const [schedulerRunsTasks, setSchedulerRunsTasks] = useState<
    BackgroundTask[]
  >([]);
  const [schedulerRunsRunning, setSchedulerRunsRunning] = useState(0);
  const [schedulerRunsError, setSchedulerRunsError] = useState<string | null>(
    null,
  );
  const [schedulerRunsLoading, setSchedulerRunsLoading] = useState(false);
  const { open: tasksOpen, setOpen: setTasksOpen } = useRightDock(
    initialRoute.branch === "session" && initialRoute.tasksOpen,
  );
  // The Files window over the chat: open, and on which file and line. It is not
  // a face of the dock, whose tasks stay as they were under it.
  const [filesOpen, setFilesOpen] = useState(
    initialRoute.branch === "session" && initialRoute.filesOpen === true,
  );
  // Bumps on every request to show a file, so the same path asked for again
  // (its tab closed meanwhile) opens again.
  const [fileOpenSeq, setFileOpenSeq] = useState(0);
  const [filePath, setFilePath] = useState(
    initialRoute.branch === "session" ? initialRoute.filePath || "" : "",
  );
  const [fileLine, setFileLine] = useState(
    initialRoute.branch === "session" ? initialRoute.fileLine || 1 : 1,
  );
  // The edits window (EditsView) is the one view of the edits, a window over
  // the chat framed like the Files window.
  const [changesViewerOpen, setChangesViewerOpen] = useState(
    initialRoute.branch === "session" && initialRoute.editsOpen === true,
  );
  // A card the shell asks the Tasks panel to open from a task-targeted link. Which
  // cards are open otherwise is the panel's own business and is not part of the address.
  //
  // The pointer names its chat and is good for one use. Every session numbers its
  // tasks from bg_1 and the panel unmounts with the drawer, so a pointer that outlived
  // its use would open a card on the next mount - in whichever chat is on screen.
  const [tasksFocus, setTasksFocus] = useState<
    (TaskFocus & { sid: string }) | null
  >(() =>
    initialRoute.branch === "session" &&
    initialRoute.tasksOpen &&
    initialRoute.taskId
      ? {
          sid: initialRoute.sessionId.trim(),
          taskId: initialRoute.taskId,
          seq: 1,
        }
      : null,
  );
  const tasksFocusSeqRef = useRef(
    initialRoute.branch === "session" &&
      initialRoute.tasksOpen &&
      initialRoute.taskId
      ? 1
      : 0,
  );
  const focusBackgroundTask = useCallback(
    (sid: string, taskId: string | null) => {
      const id = (taskId || "").trim();
      const key = sid.trim();
      if (!id || !key) {
        return;
      }
      tasksFocusSeqRef.current += 1;
      setTasksFocus({ sid: key, taskId: id, seq: tasksFocusSeqRef.current });
    },
    [],
  );
  const spendTasksFocus = useCallback((seq: number) => {
    setTasksFocus((prev) => (prev && prev.seq === seq ? null : prev));
  }, []);
  const [schedulerRunsFocus, setSchedulerRunsFocus] =
    useState<TaskFocus | null>(null);
  const [backgroundTasks, setBackgroundTasks] = useState<BackgroundTask[]>([]);
  const [backgroundRunning, setBackgroundRunning] = useState(0);
  const [backgroundListError, setBackgroundListError] = useState<string | null>(
    null,
  );
  const [backgroundListLoading, setBackgroundListLoading] = useState(false);
  /** Ticks once a second so elapsed times advance between polls. */
  const [backgroundNowMs, setBackgroundNowMs] = useState(() => Date.now());
  /** Set while the viewed session is a subagent's transcript (read-only, no composer). */
  // Whether the conversation on screen is archived, and whether the request to
  // take it out of the archive is in flight.
  const [viewedArchived, setViewedArchived] = useState(false);
  const [unarchiving, setUnarchiving] = useState(false);
  const [subagentTranscript, setSubagentTranscript] =
    useState<SubagentTranscriptMeta | null>(null);
  // Mirrored for the stable Settings callback, which has to know that the
  // transcript on screen belongs to a parent whose tree may have been removed.
  const subagentParentIdRef = useRef("");
  subagentParentIdRef.current = subagentTranscript?.parentSessionId ?? "";
  const [schedDockClusterWidthPx, setSchedDockClusterWidthPx] = useState(0);
  const [sessionFilterDraft, setSessionFilterDraft] = useState("");
  const [sessionFilterQ, setSessionFilterQ] = useState("");
  // How History divides the list, remembered across visits; the archive stays
  // hidden until it is asked for, so a conversation put aside is out of the way
  // on the next open too.
  const [sessionGroupMode, setSessionGroupMode] = useState<SessionGroupMode>(
    () => readSessionGroupCookie() ?? DEFAULT_SESSION_GROUP_MODE,
  );
  // Every one of these survives a reload: a filter forgotten by the next page
  // load is not a setting, it is a gesture.
  const [sessionsArchiveFilter, setSessionsArchiveFilter] =
    useState<SessionArchiveFilter>(
      () =>
        readSessionPref(SESSION_PREF_COOKIES.status, isSessionArchiveFilter) ??
        DEFAULT_ARCHIVE_FILTER,
    );
  // The filter on screen now, for an archive request that settles after the
  // operator changed it (see runArchiveSession).
  const sessionsArchiveFilterRef = useRef(sessionsArchiveFilter);
  sessionsArchiveFilterRef.current = sessionsArchiveFilter;
  const [sessionsSortKey, setSessionsSortKey] = useState<SessionSortKey>(
    () =>
      readSessionPref(SESSION_PREF_COOKIES.sort, isHistorySortKey) ??
      DEFAULT_SESSION_SORT_KEY,
  );
  // Which surface's conversations History shows: every one, the ones opened on
  // this host, or the chats a messenger gateway is holding.
  const [sessionsOrigin, setSessionsOrigin] = useState<SessionOriginFilter>(
    () =>
      readSessionPref(SESSION_PREF_COOKIES.origin, isSessionOriginFilter) ?? "",
  );
  // The remotes this server offers as environments, read from the local config
  // rather than the active one - the list of places to go must not travel with
  // the place you are. Shared with the rail's environment menu and read
  // again on every config reload (env/configuredRemotes.ts).
  const configuredRemotes = useConfiguredRemotes();
  // A folder picked from a History heading, waiting for the conversation on
  // screen to be gone before it is applied (see newChatWorkspace.ts). The value
  // is a ref and the trigger a counter, so the one effect that owns "the
  // session changed" applies it - two effects racing to set the workspace
  // context would be decided by whichever fetch answered last.
  const newChatWorkspaceRef = useRef<PendingNewChatWorkspace>(null);
  // Sessions with an archive change in flight; see archiveSession.
  const archivingRef = useRef<Set<string>>(new Set());
  // History moves a row the moment it is archived, so a listing issued before
  // the server had the new flag must not put it back (archiveMoves.ts): the
  // moves this tab made, the listings issued so far and the ones still out,
  // and the points at which an archive took a loaded row out of the server's
  // listing, which the offset of the next page has to account for.
  const archiveMovesRef = useRef<Map<string, ArchiveMove>>(new Map());
  const sessionsListSeqRef = useRef(0);
  const sessionsListOpenRef = useRef<Set<number>>(new Set());
  // The number of the latest listing read from the first page: a page issued
  // before it belongs to the list that read replaced.
  const sessionsListResetSeqRef = useRef(0);
  const archiveRemovalsRef = useRef<number[]>([]);
  // A refused archive, said on the row it put back: History is usually
  // scrolled away from the top of the list, where a list error is shown.
  const [sessionRowErrors, setSessionRowErrors] = useState<
    Record<string, string>
  >({});
  // The archive flag's last write this client made, so a transcript read that
  // was issued before the PATCH settled cannot put the older flag back (see
  // loadMessages, which compares the read's issue time against this).
  const viewedArchiveWriteRef = useRef<{
    sid: string;
    archived: boolean;
    at: number;
  } | null>(null);
  // The same, for pinning.
  const pinningRef = useRef<Set<string>>(new Set());
  const [newChatWorkspaceEpoch, setNewChatWorkspaceEpoch] = useState(0);
  const [sessionsHasMore, setSessionsHasMore] = useState(false);
  const [sessionsLoadingMore, setSessionsLoadingMore] = useState(false);
  const sessionsHasMoreRef = useRef(false);
  const sessionsLoadingMoreRef = useRef(false);
  const [viewportXL, setViewportXL] = useState(false);
  const [railLabelsWide, setRailLabelsWide] = useState(false);
  const [mode, setMode] = useState<string>("agent");
  /**
   * The viewed session's permission mode and the one a restart would give it
   * back, the settings changed for the next turns, and the version of the
   * snapshot they came from (chat/sessionSettings.ts). The server is the
   * source of truth: every surface's change arrives as a versioned snapshot.
   */
  const [permissionMode, setPermissionMode] = useState("ask");
  const [configuredPermissionMode, setConfiguredPermissionMode] =
    useState("ask");
  /**
   * The permission mode the server says a new session starts under (GET
   * /coddy/info, read again after every configuration reload and every
   * reconnect of the events stream): the start screen has no session and so
   * no snapshot, and this is the mode its first turn runs under unless the
   * operator picks another. Null while unknown - before the answer, while a
   * reload is read again, from a server that does not say - and a pick is then
   * always sent. The ref is what a send compares with, the state what renders.
   */
  const [serverPermissionMode, setServerPermissionMode] = useState<
    string | null
  >(null);
  const serverPermissionModeRef = useRef<string | null>(null);
  const serverPermissionReadRef = useRef(0);
  /** The read of the mode in flight, which a first send waits for. */
  const serverPermissionReadingRef = useRef<Promise<void> | null>(null);
  const [settingsOverrides, setSettingsOverrides] = useState<TurnOverride[]>(
    [],
  );
  const settingsVersionRef = useRef<{ sid: string; version: number }>({
    sid: "",
    version: 0,
  });
  /**
   * The viewed session's goal (chat/goal.ts) and the version of the snapshot
   * it came from, kept the way the settings are: the messages read, the turn
   * stream, the events stream and the answers of the goal's own routes all
   * deliver it, and the highest version wins. A goal of another session is
   * not shown: the chip reads `goal` only while `sid` is the viewed session.
   */
  const [viewedGoal, setViewedGoal] = useState<{
    sid: string;
    goal: SessionGoal | null;
  }>({ sid: "", goal: null });
  const goalVersionRef = useRef<{ sid: string; version: number }>({
    sid: "",
    version: 0,
  });
  /**
   * A permission mode picked before the chat has a session: it rides in as a
   * command at the start of the first message, where the server takes it,
   * unless it is the mode the server named as its configured one (decided
   * when sending, against what the server said last). `sid` is empty on the
   * start screen and names the session the first send creates from then on:
   * the pick stays until the server has taken a message of that session, so a
   * first send that never reached it (Recoverable composer sends) does not
   * leave the pick behind for the next try. The state mirrors the mode for the
   * chip.
   */
  const pendingPermissionModeRef = useRef({ mode: "", sid: "" });
  const [pendingPermissionMode, setPendingPermissionModeState] = useState("");
  const setPendingPermissionMode = useCallback((mode: string, sid = "") => {
    pendingPermissionModeRef.current = { mode, sid };
    setPendingPermissionModeState(mode);
  }, []);
  /**
   * readServerPermissionMode asks the server which permission mode a new
   * session starts under. The fetch shim sends it to the environment the app
   * is on: the local server, a remote, a node behind a relay's mount. The mode
   * held so far stops counting as known at once, because the read follows a
   * reload or a reconnect, either of which may have moved it: a pick sent
   * against an unknown mode is at worst redundant, while one left out against
   * a stale mode runs the first turn under a mode nobody chose.
   */
  const readServerPermissionMode = useStableHandler(() => {
    const read = ++serverPermissionReadRef.current;
    serverPermissionModeRef.current = null;
    const reading = fetch("/coddy/info", {
      headers: { Accept: "application/json" },
    })
      .then((res) => (res.ok ? res.json() : null))
      .then(permissionModeOfInfo)
      .catch(() => null)
      .then((mode) => {
        if (read !== serverPermissionReadRef.current || !isAppEnvironment()) {
          return;
        }
        serverPermissionReadingRef.current = null;
        serverPermissionModeRef.current = mode;
        setServerPermissionMode(mode);
        if (mode) {
          setConfiguredPermissionMode(mode);
        }
      });
    serverPermissionReadingRef.current = reading;
    return reading;
  });
  // configEpoch bumps after every configuration swap, which can move
  // tools.permission_mode as well as the models.
  useEffect(() => {
    void readServerPermissionMode();
  }, [configEpoch, readServerPermissionMode]);
  // The configured mode the chip names: the server's own answer when it gave
  // one, else the last snapshot's (a server that does not say it in /coddy/info).
  const shownConfiguredPermissionMode =
    serverPermissionMode ?? configuredPermissionMode;
  // What the start screen's chip names, and the first turn runs under: the
  // mode picked there, else the configured one.
  const startPermissionMode =
    pendingPermissionMode || shownConfiguredPermissionMode;
  const [llmModelIds, setLlmModelIds] = useState<string[]>([]);
  const [llmModel, setLlmModel] = useState("");
  const applyContextUsage = useStableHandler(
    (sid: string, u: ContextUsageUpdate) => {
      setContextBreakdown((prev) => withContextUsedTokens(prev, u.used));
      setSessionContextWindows((prev) => ({
        ...prev,
        [sid]: { model: llmModel, epoch: configEpoch, size: u.size },
      }));
      debouncedRefreshSessionStats(sid);
    },
  );
  const providerUsageState = useProviderUsage({
    sessionId,
    llmModel,
    turnEpoch: providerUsageTurnEpoch,
  });
  const [llmReasoning, setLlmReasoning] = useState("");
  /**
   * The opened session's own model and reasoning, as its settings snapshot
   * names them, with the levels the snapshot says it may hold. Held until the
   * backends list (`llmModelIds`) is available so the restore survives
   * whichever of `/v1/models` and `/coddy/sessions/.../messages` resolves
   * first on reload.
   */
  const [openSessionSelection, setOpenSessionSelection] = useState<{
    sid: string;
    model: string;
    reasoning: string;
    choices: string[];
  } | null>(null);
  /** The selection object already applied to the composer; see the effect below. */
  const appliedSessionSelectionRef = useRef<{
    sid: string;
    model: string;
    reasoning: string;
    choices: string[];
  } | null>(null);
  /**
   * The reasoning levels the viewed session's last snapshot named for its
   * model (`reasoningChoices`): the menu's levels plus `off` where the
   * provider can turn thinking off, which `GET /v1/models` does not list. The
   * level is checked against them wherever the composer re-validates it, so a
   * session running with thinking off is shown and sent as such.
   */
  const sessionReasoningChoicesRef = useRef<{
    sid: string;
    model: string;
    choices: string[];
  }>({ sid: "", model: "", choices: [] });
  /** The same record as state, for what renders from it (the level menu). */
  const [sessionReasoningChoices, setSessionReasoningChoicesState] = useState<{
    sid: string;
    model: string;
    choices: string[];
  }>({ sid: "", model: "", choices: [] });
  const setSessionReasoningChoices = useCallback(
    (next: { sid: string; model: string; choices: string[] }) => {
      sessionReasoningChoicesRef.current = next;
      setSessionReasoningChoicesState(next);
    },
    [],
  );
  /**
   * Set while the level on the chip is the one the chooser resolved for an
   * existing session that has none of its own (a model with no
   * `reasoning_default`): shown so the chip names what the turn runs at, and
   * never sent, so the session is not pinned to a level nobody chose.
   */
  const reasoningImpliedRef = useRef(false);
  const [describePreview, setDescribePreview] = useState<{
    sessionId: string;
    title: string;
  } | null>(null);
  const heroAccentVerb = useMemo(
    () => pickHeroAccentVerb(sessionId, heroHomeGeneration),
    [sessionId, heroHomeGeneration],
  );

  const handleComposerSseQuestion = useCallback(
    (raw: Record<string, unknown>) => {
      const p = parseCoddyQuestionPayload(raw);
      if (!p) return;
      const key = p.sessionId.trim();
      if (!key) return;
      setQuestionPendingSids((prev) => {
        const next = new Set(prev);
        next.add(key);
        return next;
      });
      upsertQuestionPromptRecord(key, {
        requestId: p.requestId.trim(),
        payload: p,
      });
      applyStreamItemsForSession(key, (prev) => {
        const ridInner = p.requestId;
        const withoutStalePending = prev.filter(
          (x) => !(x.type === "question_prompt" && !x.resolved),
        );
        const withoutDup = withoutStalePending.filter(
          (x) =>
            !(x.type === "question_prompt" && x.payload.requestId === ridInner),
        );
        const row = {
          id: `qp_${ridInner}`,
          type: "question_prompt" as const,
          payload: p,
        };
        // Insert right after the tool call that raised it, like the permission
        // gate below: the card belongs under its own row, not above it.
        const tcid = (p.toolCallId || "").trim();
        const tcIdx = tcid
          ? withoutDup.findIndex(
              (x) => x.type === "tool_call" && x.toolCallId === tcid,
            )
          : -1;
        if (tcIdx >= 0) {
          const result = [...withoutDup];
          result.splice(tcIdx + 1, 0, row);
          return result;
        }
        return [...withoutDup, row];
      });
    },
    [],
  );

  const handleComposerSsePermission = useCallback(
    (raw: Record<string, unknown>) => {
      const p = parseCoddyPermissionPayload(raw);
      if (!p) return;
      const key = p.sessionId.trim();
      if (!key) return;
      const tcid = p.toolCall.toolCallId.trim();
      setPermissionPendingSids((prev) => {
        const next = new Set(prev);
        next.add(key);
        return next;
      });
      applyStreamItemsForSession(key, (prev) => {
        const withoutStalePending = prev.filter(
          (x) => !(x.type === "permission_prompt" && !x.resolved),
        );
        const withoutDup = withoutStalePending.filter(
          (x) =>
            !(
              x.type === "permission_prompt" &&
              x.payload.toolCall.toolCallId === tcid
            ),
        );
        const row = {
          id: stablePermissionPromptItemId(tcid),
          type: "permission_prompt" as const,
          payload: p,
        };
        upsertPermissionPromptRecord(key, {
          toolCallId: tcid,
          payload: p,
        });
        // Insert right after the corresponding tool_call if it's already in the transcript.
        const tcIdx = withoutDup.findIndex(
          (x) => x.type === "tool_call" && x.toolCallId === tcid,
        );
        if (tcIdx >= 0) {
          const result = [...withoutDup];
          result.splice(tcIdx + 1, 0, row);
          return result;
        }
        return [...withoutDup, row];
      });
    },
    [],
  );

  const resolveQuestionPrompt = useCallback(
    (sessionId: string, itemId: string, resolved: QuestionResolvedState) => {
      const key = sessionId.trim();
      if (!key) return;
      setQuestionPendingSids((prev) => {
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
      applyStreamItemsForSession(key, (prev) => {
        const next = prev.map((x) =>
          x.id === itemId && x.type === "question_prompt"
            ? { ...x, resolved }
            : x,
        );
        const hit = next.find(
          (x) => x.id === itemId && x.type === "question_prompt",
        );
        if (hit?.type === "question_prompt") {
          upsertQuestionPromptRecord(key, {
            requestId: hit.payload.requestId.trim(),
            payload: hit.payload,
            ...(hit.resolved !== undefined ? { resolved: hit.resolved } : {}),
          });
        }
        return next;
      });
    },
    [],
  );

  const resolvePermissionPrompt = useCallback(
    (sessionId: string, itemId: string, resolved: PermissionResolvedState) => {
      const key = sessionId.trim();
      if (!key) return;
      setPermissionPendingSids((prev) => {
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
      applyStreamItemsForSession(key, (prev) => {
        const hit = prev.find(
          (x) => x.type === "permission_prompt" && x.id === itemId,
        );
        if (hit?.type === "permission_prompt") {
          // Keep the record marked as resolved so restorePermissionPromptsForPendingTools
          // won't re-synthesize a prompt for the same tool call on subsequent loadMessages.
          upsertPermissionPromptRecord(key, {
            toolCallId: hit.payload.toolCall.toolCallId.trim(),
            payload: hit.payload,
            resolved,
          });
        }
        return prev.filter(
          (x) => !(x.type === "permission_prompt" && x.id === itemId),
        );
      });
      for (const delayMs of [0, 250, 900]) {
        window.setTimeout(() => {
          void loadMessages(key, {
            preserveOnError: true,
            skipSetItems: viewedSessionIdRef.current.trim() !== key,
          });
          void loadSessionsList(true);
        }, delayMs);
      }
    },
    [],
  );

  // What the transcript shows: the older pages read while scrolling up, then
  // the live window. The base numbers the prompts the way the server does
  // (an edit rewinds by that index) and says whether history lies above.
  const olderOnScreen =
    olderTranscript &&
    olderTranscript.sessionId === sessionId &&
    viewedLiveWindow?.sessionId === sessionId &&
    olderTranscript.generation === viewedLiveWindow.seq
      ? olderTranscript
      : null;
  const transcriptItems = useMemo(
    () => (olderOnScreen ? [...olderOnScreen.items, ...items] : items),
    [olderOnScreen, items],
  );
  const transcriptTopWindow: TranscriptWindow | null = olderOnScreen
    ? olderOnScreen.window
    : viewedLiveWindow?.sessionId === sessionId
      ? viewedLiveWindow
      : null;

  const currentTitle = useMemo(() => {
    if (!sessionId) {
      return t("chat.newChat");
    }
    if (describePreview?.sessionId === sessionId) {
      const hint = describePreview.title.trim();
      if (hint) {
        return hint;
      }
    }
    const row = sessions.find((s) => s.id === sessionId);
    const rowTitle = (row?.title || "").trim();
    if (rowTitle) {
      return rowTitle;
    }
    // A child session has no History row to name it, so name it by its role;
    // a run the scheduler started, and a job's own session, by their job.
    if (subagentTranscript) {
      const sched = subagentTranscript.scheduler;
      if (sched) {
        return subagentTranscript.jobSession
          ? t("chat.schedulerJobSessionTitle", { jobId: sched.jobId })
          : t("chat.scheduledRunTitle", { jobId: sched.jobId });
      }
      const name = subagentTranscript.name.trim();
      return name
        ? t("chat.subagentTitle", { name })
        : t("chat.subagentTitleUnnamed");
    }
    return t("chat.newChat");
  }, [sessionId, sessions, describePreview, t, subagentTranscript]);

  const currentSessionCwd = useMemo(() => {
    const sid = sessionId.trim();
    if (!sid) {
      return "";
    }
    return (sessions.find((s) => s.id === sid)?.cwd || "").trim();
  }, [sessionId, sessions]);

  // What a transcript row spells a path against: the session's own directory,
  // then the worktrees of its workspace. Work inside a worktree reads against
  // that worktree, so the deepest match wins (relativeToolTarget).
  const transcriptPathRoots = useMemo(() => {
    const roots = [currentSessionCwd];
    for (const worktree of workspaceCtx?.worktrees || []) {
      const path = (worktree.path || "").trim();
      if (path) roots.push(path);
    }
    return roots.filter((root) => root !== "");
  }, [currentSessionCwd, workspaceCtx]);

  async function saveSessionTitle(id: string, title: string) {
    const t = title.trim();
    if (!t) {
      return;
    }
    await fetch(`/coddy/sessions/${encodeURIComponent(id)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ title: t }),
    });
    setSessions((prev) =>
      prev.map((s) => (s.id === id ? { ...s, title: t } : s)),
    );
  }

  /**
   * Writes the labels of one conversation.
   *
   * The list takes the new set **before** the request: the editor builds each
   * gesture on the row it is shown, so a second gesture made while the first is
   * still in flight would otherwise start from the set before both and undo
   * one of them. The server folds what it stores and answers with the set it
   * kept, which then replaces the optimistic one - a chip drawn here never
   * changes spelling one refresh later - and a refused write puts back what the
   * row carried, so the list never claims something the server does not hold.
   */
  async function saveSessionTags(id: string, tags: string[]): Promise<boolean> {
    let previous: string[] | undefined;
    setSessions((prev) =>
      prev.map((s) => {
        if (s.id !== id) {
          return s;
        }
        previous = s.tags ?? [];
        return { ...s, tags };
      }),
    );
    const restore = () =>
      setSessions((prev) =>
        prev.map((s) => (s.id === id ? { ...s, tags: previous ?? [] } : s)),
      );
    let stored: string[];
    try {
      const res = await fetch(`/coddy/sessions/${encodeURIComponent(id)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ tags }),
      });
      if (!res.ok) {
        throw new Error(String(res.status));
      }
      const data = (await res.json()) as { tags?: string[] };
      stored = data.tags ?? [];
    } catch {
      // A dropped connection and a refused write end the same way: the row goes
      // back to what the server still holds, and the caller says so.
      restore();
      return false;
    }
    setSessions((prev) =>
      prev.map((s) => (s.id === id ? { ...s, tags: stored } : s)),
    );
    return true;
  }

  const headers = useMemo(
    () => (sessionId ? { [HDR]: sessionId } : {}),
    [sessionId],
  );

  const refreshWorkspaceContext = useCallback(
    async (sid: string) => {
      const gen = ++workspaceCtxGenRef.current;
      try {
        const res = await fetch("/coddy/workspace/context", {
          headers: sid ? { [HDR]: sid } : {},
        });
        if (res.ok) {
          const ctx = (await res.json()) as WorkspaceContext;
          if (gen !== workspaceCtxGenRef.current) {
            return;
          }
          setWorkspaceCtx(ctx);
          // Host fact, not a workspace one: the tool cards name the interpreter.
          // An answer from the environment the app was switched away from is
          // not this host's.
          if (isAppEnvironment()) {
            setHostShell(ctx.shell);
          }
        }
      } catch {
        // ignore: chips keep the previous context
      }
    },
    [isAppEnvironment],
  );

  /**
   * Reads the start screen's folder again - the pending pick, else the
   * server's default - so it names the branch the folder is on now: one
   * switched with git switch since it was last read shows. A branch picked on
   * the start screen stays the choice over the folder's own.
   */
  const refreshHomeWorkspace = useCallback(async () => {
    const path = pendingWorkspaceRef.current?.path;
    if (!path) {
      await refreshWorkspaceContext("");
      return;
    }
    const gen = ++workspaceCtxGenRef.current;
    try {
      const res = await fetch(
        "/coddy/workspace/context?path=" + encodeURIComponent(path),
      );
      if (gen !== workspaceCtxGenRef.current) {
        return;
      }
      if (res.status === 400) {
        // The folder is gone: forget it and fall back to the server's default.
        if (readLastWorkspaceDir() === path) {
          writeLastWorkspaceDir("");
        }
        setPendingWorkspace(null);
        await refreshWorkspaceContext("");
        return;
      }
      if (!res.ok) {
        return;
      }
      const ctx = (await res.json()) as WorkspaceContext;
      if (gen !== workspaceCtxGenRef.current) {
        return;
      }
      const picked = pendingWorkspaceRef.current;
      setWorkspaceCtx(
        picked?.branch
          ? {
              ...ctx,
              branch: picked.branch,
              is_worktree: Boolean(picked.worktree),
            }
          : ctx,
      );
    } catch {
      // ignore: the plate keeps the context it has
    }
  }, [refreshWorkspaceContext]);

  /**
   * Fetches the remotes of the folder the branch list is about - the session's,
   * or before a session exists the folder the new chat picked - and takes the
   * context the server read after the fetch, so a branch pushed since the last
   * fetch is listed. A branch picked on the start screen stays the choice. The
   * outcome goes back to the list, which warns when the refresh failed: the
   * context it shows then is the one from before.
   */
  // The chat on screen now, for an answer that arrives after it may have
  // changed (the branch refresh below).
  const viewedSessionRef = useRef(sessionId);
  useEffect(() => {
    viewedSessionRef.current = sessionId;
  }, [sessionId]);

  const refreshWorkspaceBranches =
    useCallback(async (): Promise<WorkspaceBranchFetch | null> => {
      const sid = sessionId.trim();
      const path = sid ? "" : (pendingWorkspaceRef.current?.path ?? "");
      const gen = ++workspaceCtxGenRef.current;
      try {
        const res = await fetch(
          "/coddy/workspace/fetch" +
            (path ? "?path=" + encodeURIComponent(path) : ""),
          { method: "POST", headers: sid ? { [HDR]: sid } : {} },
        );
        if (!res.ok) {
          return { status: "failed", error: `HTTP ${res.status}` };
        }
        const body = (await res.json()) as WorkspaceContext & {
          fetch?: WorkspaceBranchFetch;
        };
        const { fetch: outcome, ...ctx } = body;
        // Another chat or another folder since: the list this was for is gone.
        const stillHere =
          viewedSessionRef.current.trim() === sid &&
          (sid || (pendingWorkspaceRef.current?.path ?? "") === path);
        if (!stillHere) {
          return null;
        }
        // A read of the same folder that started while the fetch ran (the
        // page got the focus back) saw the refs from before it: the fetched
        // context wins whichever answered first, and a read still out is
        // dropped by taking a newer ticket.
        if (gen !== workspaceCtxGenRef.current) {
          workspaceCtxGenRef.current += 1;
        }
        const picked = sid ? null : pendingWorkspaceRef.current;
        setWorkspaceCtx(
          picked?.branch
            ? {
                ...ctx,
                branch: picked.branch,
                is_worktree: Boolean(picked.worktree),
              }
            : ctx,
        );
        return outcome ?? null;
      } catch {
        return { status: "failed" };
      }
    }, [sessionId]);

  // Load the workspace context whenever the viewed session changes. A pending
  // home workspace survives the route change so the next chat starts where the
  // user left off, and is read again for the branch it is on now.
  //
  // A folder picked from a History heading is applied here rather than where it
  // was picked: leaving a conversation is asynchronous, and a workspace change
  // issued before the session is gone lands on the conversation being left.
  // It replaces the default probe rather than running beside it - two context
  // fetches in flight would be decided by whichever answered last.
  useEffect(() => {
    const wanted = newChatWorkspaceRef.current;
    if (newChatWorkspaceIsReady(wanted, sessionId)) {
      newChatWorkspaceRef.current = null;
      void switchWorkspace({ path: String(wanted?.path ?? "") });
      return;
    }
    if (!sessionId && pendingWorkspaceRef.current?.path) {
      void refreshHomeWorkspace();
      return;
    }
    // The start screen opens on the folder last picked in this browser.
    const remembered = sessionId ? "" : readLastWorkspaceDir();
    if (remembered) {
      setPendingWorkspace({ path: remembered });
      void refreshHomeWorkspace();
      return;
    }
    setPendingWorkspace(null);
    void refreshWorkspaceContext(sessionId);
  }, [
    sessionId,
    refreshWorkspaceContext,
    refreshHomeWorkspace,
    newChatWorkspaceEpoch,
    setPendingWorkspace,
  ]);

  // On the start screen, coming back to the page reads the folder again: its
  // branch may have been switched in a terminal meanwhile.
  useEffect(() => {
    if (sessionId.trim()) {
      return undefined;
    }
    const reread = () => void refreshHomeWorkspace();
    const visible = () => {
      if (document.visibilityState === "visible") reread();
    };
    window.addEventListener("focus", reread);
    document.addEventListener("visibilitychange", visible);
    return () => {
      window.removeEventListener("focus", reread);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [sessionId, refreshHomeWorkspace]);

  async function switchWorkspace(payload: {
    path?: string;
    branch?: string;
    worktree?: boolean;
  }) {
    const sid = sessionId.trim();
    if (!sid) {
      // No session yet: remember the choice and preview the target context.
      // A folder picked is this browser's for the next start screen too.
      if (payload.path) {
        writeLastWorkspaceDir(payload.path);
      }
      setPendingWorkspace({
        ...(pendingWorkspaceRef.current || {}),
        ...payload,
      });
      if (payload.path) {
        const gen = ++workspaceCtxGenRef.current;
        try {
          const res = await fetch(
            "/coddy/workspace/context?path=" + encodeURIComponent(payload.path),
          );
          if (res.ok) {
            const ctx = (await res.json()) as WorkspaceContext;
            if (gen === workspaceCtxGenRef.current) {
              setWorkspaceCtx(ctx);
            }
          }
        } catch {
          // ignore
        }
      } else if (payload.branch) {
        const nextBranch = payload.branch;
        setWorkspaceCtx((prev) =>
          prev
            ? {
                ...prev,
                branch: nextBranch,
                is_worktree: Boolean(payload.worktree),
              }
            : prev,
        );
      }
      return;
    }
    try {
      const res = await fetch(
        `/coddy/sessions/${encodeURIComponent(sid)}/workspace`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json", [HDR]: sid },
          body: JSON.stringify(payload),
        },
      );
      if (res.ok) {
        const gen = ++workspaceCtxGenRef.current;
        const ctx = (await res.json()) as WorkspaceContext;
        if (gen === workspaceCtxGenRef.current) {
          setWorkspaceCtx(ctx);
        }
      } else {
        await refreshWorkspaceContext(sid);
      }
    } catch {
      // network error: keep the current chips
    }
  }

  // Applies pre-session workspace choices to the freshly created session id
  // right before the first send.
  async function applyPendingWorkspace(sid: string) {
    const pending = pendingWorkspaceRef.current;
    if (!pending || (!pending.path && !pending.branch)) {
      setPendingWorkspace(null);
      return;
    }
    const base = { "Content-Type": "application/json", [HDR]: sid };
    try {
      // The answer is the new session's workspace context: taking it here
      // keeps the chips and everything scoped by chatWorkspace on the picked
      // folder from the moment the pending pick is cleared, instead of on a
      // preview that may not have answered yet.
      const applied = async (res: Response) => {
        if (!res.ok) {
          return;
        }
        const gen = ++workspaceCtxGenRef.current;
        const ctx = (await res.json()) as WorkspaceContext;
        if (gen === workspaceCtxGenRef.current) {
          setWorkspaceCtx(ctx);
        }
      };
      if (pending.path) {
        await applied(
          await fetch(`/coddy/sessions/${encodeURIComponent(sid)}/workspace`, {
            method: "POST",
            headers: base,
            body: JSON.stringify({ path: pending.path }),
          }),
        );
      }
      if (pending.branch) {
        await applied(
          await fetch(`/coddy/sessions/${encodeURIComponent(sid)}/workspace`, {
            method: "POST",
            headers: base,
            body: JSON.stringify({
              branch: pending.branch,
              worktree: Boolean(pending.worktree),
            }),
          }),
        );
      }
    } catch {
      // ignore: the session still starts in the default workspace
    } finally {
      // Cleared once the session holds the pick, so the folder the chat is
      // scoped to never falls back to a stale preview in between.
      setPendingWorkspace(null);
    }
  }

  const refreshBackgroundTasks = useCallback(
    async (opts?: { silent?: boolean }) => {
      const sid = sessionId.trim();
      if (!sid) {
        setBackgroundTasks([]);
        setBackgroundRunning(0);
        return;
      }
      const silent = !!opts?.silent;
      if (!silent) {
        setBackgroundListLoading(true);
        setBackgroundListError(null);
      }
      const res = await listBackgroundTasks(sid);
      if (!silent) {
        setBackgroundListLoading(false);
      }
      if (!res.ok) {
        if (!silent) {
          setBackgroundListError(res.message);
          setBackgroundTasks([]);
          setBackgroundRunning(0);
        }
        return;
      }
      setBackgroundListError(null);
      setBackgroundTasks(res.data.data || []);
      setBackgroundRunning(res.data.running || 0);
    },
    [sessionId, t],
  );

  // The Tasks panel reads the output of every card it has open through this.
  const loadBackgroundTaskOutput = useCallback(
    async (taskId: string): Promise<string | null> => {
      const sid = sessionId.trim();
      if (!sid || !taskId) {
        return null;
      }
      const res = await getBackgroundTask(sid, taskId);
      return res.ok ? res.data.output || "" : null;
    },
    [sessionId],
  );

  const stopBackgroundTaskById = useCallback(
    async (taskId: string) => {
      const sid = sessionId.trim();
      if (!sid || !taskId) {
        return;
      }
      await stopBackgroundTask(sid, taskId);
      await refreshBackgroundTasks({ silent: true });
    },
    [sessionId, refreshBackgroundTasks],
  );

  const clearFinishedTasks = useCallback(async () => {
    const sid = sessionId.trim();
    if (!sid) {
      return;
    }
    await clearFinishedBackgroundTasks(sid);
    void refreshBackgroundTasks({ silent: true });
  }, [sessionId, refreshBackgroundTasks]);

  // Requests about one project job carry the chat's session, so a job of the
  // chat's workspace opens and is approved before the scheduler scans it.
  useEffect(() => {
    setSchedulerSessionHeaders(
      workspaceScope(sessionId, chatWorkspace).headers,
    );
  }, [sessionId, chatWorkspace]);

  const refreshSchedulerJobs = useCallback(
    async (opts?: { silent?: boolean }) => {
      const silent = !!opts?.silent;
      if (!silent) {
        setSchedulerListLoading(true);
        setSchedulerListError(null);
      }
      const res = await schedulerListJobs(
        false,
        workspaceScope(sessionId, chatWorkspace),
      );
      if (!silent) {
        setSchedulerListLoading(false);
      }
      if (!res.ok) {
        let msg = res.message;
        if (res.status === 404) {
          setSchedulerHttpLinked(false);
          setSchedulerOpen(false);
          setSchedulerEditor(null);
          msg = t("scheduler.apiNotAvailable");
          // The answer is about the scheduler screen: the address is left
          // alone once the reader has moved on from it, or the app from the
          // environment it asked (a switch in place keeps the request alive).
          if (isAppEnvironment() && parseAppHash().branch === "scheduler") {
            const sid = sessionId.trim();
            if (sid) {
              setSessionHashInLocation(sid);
            } else if (window.location.hash) {
              history.replaceState(
                null,
                "",
                `${window.location.pathname}${window.location.search}`,
              );
            }
          }
          setSchedulerListError(msg);
          setSchedulerJobs([]);
          setSchedulerInfo(null);
          return;
        }
        if (res.status === 503) {
          msg = t("scheduler.disabled");
          if (!silent) {
            setSchedulerListError(msg);
            setSchedulerJobs([]);
            setSchedulerInfo(null);
          }
          return;
        }
        if (!silent) {
          setSchedulerListError(msg);
          setSchedulerJobs([]);
          setSchedulerInfo(null);
        }
        return;
      }
      setSchedulerInfo(res.data.scheduler);
      setSchedulerJobs(res.data.jobs || []);
    },
    [sessionId, chatWorkspace, t, isAppEnvironment],
  );

  const applyLocationHash = useCallback(() => {
    const p = parseAppHash();
    // The Files and the edits windows have addresses of their own; any other
    // one puts them away.
    if (!(p.branch === "session" && p.filesOpen)) setFilesOpen(false);
    if (!(p.branch === "session" && p.editsOpen)) setChangesViewerOpen(false);
    if (p.branch === "docs") {
      setDocsRoute({ slug: p.slug, anchor: p.anchor });
      if (p.slug) {
        lastDocsSlugRef.current = p.slug;
      }
      setSwarmRoute(false);
      setSettingsRoute(false);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      if (isStackedShell()) setTasksOpen(false);
      setSessionsOpen(false);
      return;
    }
    setDocsRoute(null);
    if (p.branch === "session") {
      setSettingsRoute(false);
      setActiveDraftId("");
      const switchingSession =
        viewedSessionIdRef.current !== p.sessionId.trim();
      viewedSessionIdRef.current = p.sessionId.trim();
      setSessionId(p.sessionId);
      if (switchingSession) setSessionLoading(true);
      void markCoddySessionActivityRead(p.sessionId);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      // The Files and the edits windows open over the dock and leave it as it
      // was, on the stacked shell too, where any other chat address puts the
      // dock away.
      setTasksOpen(
        (wasOpen) =>
          p.tasksOpen ||
          ((p.filesOpen === true ||
            p.editsOpen === true ||
            !isStackedShell()) &&
            wasOpen),
      );
      if (p.filesOpen) {
        setFilesOpen(true);
        setFilePath(p.filePath || "");
        setFileLine(p.fileLine || 1);
      }
      setChangesViewerOpen(p.editsOpen === true);
      if (p.tasksOpen && p.taskId) {
        // A link that names a task opens its card once; the address goes back to
        // saying only that the panel is showing.
        focusBackgroundTask(p.sessionId, p.taskId);
        setSessionTasksHash(p.sessionId, null, {
          historySidebar: !!p.historyOpen,
        });
      }
      setSessionsOpen(!!p.historyOpen);
      return;
    }
    if (p.branch === "draft") {
      setSettingsRoute(false);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      if (isStackedShell()) setTasksOpen(false);
      setSessionId("");
      viewedSessionIdRef.current = "";
      setActiveDraftId(p.draftId.trim());
      const row = readClientDraftSessions().find(
        (r) => r.localId === p.draftId.trim(),
      );
      setDraft(row?.draftText || "");
      setItems([]);
      setSessionsOpen(!!p.historyOpen);
      return;
    }
    if (p.branch === "history") {
      setSettingsRoute(false);
      // The map lies over History in the stack of the rail's screens: opened
      // under it, the drawer would be out of sight.
      setSwarmRoute(false);
      setSessionsOpen(true);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      if (isStackedShell()) setTasksOpen(false);
      return;
    }
    if (p.branch === "swarm") {
      setSwarmRoute(true);
      setSettingsRoute(false);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      if (isStackedShell()) setTasksOpen(false);
      setSessionsOpen(false);
      return;
    }
    setSwarmRoute(false);
    if (p.branch === "settings") {
      setSettingsRoute(true);
      setSettingsSection(p.section);
      setSettingsItem(p.item);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      if (isStackedShell()) setTasksOpen(false);
      setSessionsOpen(false);
      return;
    }
    if (p.branch === "scheduler") {
      setSettingsRoute(false);
      if (schedulerHttpLinked === false) {
        setSchedulerOpen(false);
        setSchedulerEditor(null);
        const sid = viewedSessionIdRef.current.trim();
        if (sid) {
          setSessionHashInLocation(sid);
        } else if (window.location.hash) {
          history.replaceState(
            null,
            "",
            `${window.location.pathname}${window.location.search}`,
          );
        }
        return;
      }
      if (schedulerHttpLinked === null) {
        return;
      }
      setSchedulerOpen(true);
      setSessionsOpen(false);
      if (isStackedShell()) setTasksOpen(false);
      setSchedulerEditor(schedulerEditorFromParsedHash(p));
      return;
    }
    viewedSessionIdRef.current = "";
    setSessionId("");
    setActiveDraftId("");
    setSettingsRoute(false);
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    setSessionsOpen(!!p.historyOpen);
  }, [schedulerHttpLinked]);

  const openSessionFromRoute = useCallback(
    (id: string, opts?: { historySidebar?: boolean; tasksOpen?: boolean }) => {
      setActiveDraftId("");
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      const keepTasksOpen = opts?.tasksOpen === true;
      setTasksOpen(keepTasksOpen);
      viewedSessionIdRef.current = id.trim();
      if (keepTasksOpen) {
        setSessionTasksHash(id, null, opts);
      } else {
        setSessionHashInLocation(id, opts);
      }
      setSessionId(id);
      void markCoddySessionActivityRead(id);
    },
    [],
  );

  const clearSessionRoute = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    viewedSessionIdRef.current = "";
    setSessionHashInLocation("");
    setSessionId("");
    setActiveDraftId("");
  }, []);

  const closeSchedulerDrawer = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    if (sessionsOpen) {
      setHistoryHash();
      return;
    }
    const sid = sessionId.trim();
    if (sid) {
      setSessionHashInLocation(sid);
    } else if (window.location.hash) {
      history.replaceState(
        null,
        "",
        `${window.location.pathname}${window.location.search}`,
      );
    }
  }, [sessionId, sessionsOpen]);

  // An open job, or its runs, back onto the list: the editor's close control,
  // and the step Escape takes before it closes the drawer.
  const closeSchedulerEditor = useCallback(() => {
    setSchedulerEditor(null);
    setSchedulerListHash();
  }, []);

  const closeAllShellDrawers = useCallback(() => {
    setSessionsOpen(false);
    setFilesOpen(false);
    setChangesViewerOpen(false);
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    // On desktop Tasks is the side panel of the chat on screen, not a screen of
    // the rail: the backdrop that takes History down after a pick there leaves
    // it open on the chat picked. The stacked shell shows it full screen, so
    // there the backdrop closes it with the rest.
    if (isStackedShell()) setTasksOpen(false);
    setDocsRoute(null);
    // The chat's address that follows does not take the swarm screen down by
    // itself (applyLocationHash leaves it on a session), so it goes here.
    setSwarmRoute(false);
    if (
      parseAppHash().branch === "settings" ||
      parseAppHash().branch === "docs"
    ) {
      const sid = sessionId.trim();
      if (sid) {
        setSessionHashInLocation(sid);
      } else {
        clearSessionRoute();
      }
      return;
    }
    const sid = sessionId.trim();
    if (sid) {
      setSessionHashInLocation(sid);
    } else if (window.location.hash) {
      history.replaceState(
        null,
        "",
        `${window.location.pathname}${window.location.search}`,
      );
    }
  }, [sessionId, clearSessionRoute]);

  const prevSessionsOpenRef = useRef(false);
  useEffect(() => {
    // Back to the chat from History: the caret returns to the composer, except
    // on a phone or a tablet, where it would open the keyboard (composerFocus.ts).
    if (
      prevSessionsOpenRef.current &&
      !sessionsOpen &&
      composerAutoFocusAllowed()
    ) {
      requestAnimationFrame(() => {
        document.getElementById("composer")?.focus();
      });
    }
    prevSessionsOpenRef.current = sessionsOpen;
  }, [sessionsOpen]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const r = await fetch("/coddy/scheduler/jobs");
        if (cancelled) {
          return;
        }
        setSchedulerHttpLinked(r.status !== 404);
      } catch {
        if (!cancelled) {
          setSchedulerHttpLinked(true);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    applyLocationHash();
  }, [applyLocationHash]);

  useEffect(() => {
    // A switch to another remote writes the new environment's route while
    // this app may still be listening; the route is the next app's to apply.
    const onHash = () => {
      if (isAppEnvironment()) {
        applyLocationHash();
      }
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, [applyLocationHash, isAppEnvironment]);

  // The known skill names describe the workspace of the chat: the session's
  // own, or before the first message the folder picked on the start screen.
  // They are asked again for every other folder and every other session, and
  // an answer for a scope left since is thrown away. The names held are
  // dropped at once unless the chat stays in the same folder on its way from
  // no session to its first one (the first send of a new chat), so a skill of
  // another workspace is never highlighted while the next answer is on its way
  // and a new chat does not blank its own chips.
  const knownSkillsGenRef = useRef(0);
  const knownSkillsScopeRef = useRef({ sid: "", path: "" });
  useEffect(() => {
    const gen = ++knownSkillsGenRef.current;
    const sid = sessionId.trim();
    const held = knownSkillsScopeRef.current;
    knownSkillsScopeRef.current = { sid, path: chatWorkspace };
    const keep =
      held.path === chatWorkspace && (held.sid === sid || held.sid === "");
    if (!keep) {
      setKnownSkillNames((prev) => (prev.size === 0 ? prev : new Set()));
    }
    const scope = workspaceScope(sid, chatWorkspace);
    void (async () => {
      const res = await fetchJSON<{ items?: Array<{ name: string }> }>(
        withWorkspaceQuery("/coddy/slash-commands?page=1&page_size=200", scope),
        { headers: scope.headers },
      );
      if (gen !== knownSkillsGenRef.current) {
        return;
      }
      if (res.ok && res.data?.items) {
        setKnownSkillNames(new Set(res.data.items.map((i) => i.name)));
      }
    })();
    // Slash commands are derived from skills.dirs, so a config swap moves them
    // too.
  }, [configEpoch, chatWorkspace, sessionId]);

  // Background tasks outlive the SSE stream of the turn that started them, so
  // the drawer and the nav badge are kept honest by polling rather than by the
  // composer stream. The cadence drops to a slow heartbeat when nothing runs.
  useEffect(() => {
    if (!sessionId.trim()) {
      setBackgroundTasks([]);
      setBackgroundRunning(0);
      return;
    }
    void refreshBackgroundTasks({ silent: !tasksOpen });
  }, [sessionId, tasksOpen, refreshBackgroundTasks]);

  useEffect(() => {
    if (!sessionId.trim()) {
      return;
    }
    // A background subagent waiting for a permission answer is still a running
    // task, so the fast cadence also brings its prompt into the chat when the
    // events stream is down, and takes it away once answered.
    const id = window.setInterval(() => {
      void refreshBackgroundTasks({ silent: true });
    }, tasksPollIntervalMs(backgroundRunning));
    return () => window.clearInterval(id);
  }, [sessionId, backgroundRunning, refreshBackgroundTasks]);

  // Elapsed labels must advance between polls, so the clock ticks on its own
  // while something is actually running.
  useEffect(() => {
    if (backgroundRunning <= 0) {
      return;
    }
    const id = window.setInterval(() => setBackgroundNowMs(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [backgroundRunning]);

  useEffect(() => {
    if (!schedulerOpen || schedulerHttpLinked === false) {
      return;
    }
    void refreshSchedulerJobs();
  }, [schedulerOpen, schedulerHttpLinked, refreshSchedulerJobs]);

  useEffect(() => {
    if (!schedulerOpen || schedulerHttpLinked !== true) {
      return;
    }
    const id = window.setInterval(() => {
      void refreshSchedulerJobs({ silent: true });
    }, SCHEDULER_JOBS_POLL_MS);
    return () => window.clearInterval(id);
  }, [schedulerOpen, schedulerHttpLinked, refreshSchedulerJobs]);

  const schedulerRunsJobId =
    schedulerEditor?.mode === "runs" ? schedulerEditor.jobId : "";
  // A link that names a run opens its card once, like a task link in a chat.
  const schedulerRunsLinkedTaskId =
    schedulerEditor?.mode === "runs" ? schedulerEditor.taskId : null;
  useEffect(() => {
    if (!schedulerRunsJobId || !schedulerRunsLinkedTaskId) {
      return;
    }
    tasksFocusSeqRef.current += 1;
    setSchedulerRunsFocus({
      taskId: schedulerRunsLinkedTaskId,
      seq: tasksFocusSeqRef.current,
    });
    setSchedulerEditor({
      mode: "runs",
      jobId: schedulerRunsJobId,
      taskId: null,
    });
    setSchedulerJobRunsHash(schedulerRunsJobId);
  }, [schedulerRunsJobId, schedulerRunsLinkedTaskId]);
  /** The job session the runs live under; empty until the job ran once. */
  const schedulerRunsSessionId = useMemo(() => {
    if (!schedulerRunsJobId) {
      return "";
    }
    const job = schedulerJobs.find(
      (j) => schedulerJobRef(j) === schedulerRunsJobId,
    );
    return (job?.session_id || "").trim();
  }, [schedulerJobs, schedulerRunsJobId]);

  const refreshSchedulerRuns = useCallback(
    async (opts?: { silent?: boolean }) => {
      const sid = schedulerRunsSessionId;
      if (!sid) {
        setSchedulerRunsTasks([]);
        setSchedulerRunsRunning(0);
        setSchedulerRunsError(null);
        return;
      }
      if (!opts?.silent) {
        setSchedulerRunsLoading(true);
        setSchedulerRunsError(null);
      }
      const res = await listBackgroundTasks(sid);
      if (!opts?.silent) {
        setSchedulerRunsLoading(false);
      }
      if (!res.ok) {
        if (!opts?.silent) {
          setSchedulerRunsError(res.message);
          setSchedulerRunsTasks([]);
          setSchedulerRunsRunning(0);
        }
        return;
      }
      setSchedulerRunsError(null);
      setSchedulerRunsTasks(res.data.data || []);
      setSchedulerRunsRunning(res.data.running || 0);
    },
    [schedulerRunsSessionId],
  );

  const loadSchedulerRunOutput = useCallback(
    async (taskId: string): Promise<string | null> => {
      const sid = schedulerRunsSessionId;
      if (!sid || !taskId) {
        return null;
      }
      const res = await getBackgroundTask(sid, taskId);
      return res.ok ? res.data.output || "" : null;
    },
    [schedulerRunsSessionId],
  );

  useEffect(() => {
    if (!schedulerRunsJobId) {
      setSchedulerRunsTasks([]);
      setSchedulerRunsRunning(0);
      setSchedulerRunsError(null);
      return;
    }
    void refreshSchedulerRuns();
  }, [schedulerRunsJobId, schedulerRunsSessionId, refreshSchedulerRuns]);

  useEffect(() => {
    if (!schedulerRunsJobId || !schedulerRunsSessionId) {
      return;
    }
    const id = window.setInterval(() => {
      void refreshSchedulerRuns({ silent: true });
    }, tasksPollIntervalMs(schedulerRunsRunning));
    return () => window.clearInterval(id);
  }, [
    schedulerRunsJobId,
    schedulerRunsSessionId,
    schedulerRunsRunning,
    refreshSchedulerRuns,
  ]);

  // A running run keeps the panel's clock ticking like the chat's panel does.
  useEffect(() => {
    if (schedulerRunsRunning <= 0) {
      return;
    }
    const id = window.setInterval(() => setBackgroundNowMs(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [schedulerRunsRunning]);

  useEffect(() => {
    void (async () => {
      const res = await fetchJSON<{
        data?: Array<{
          id?: string;
          owned_by?: string;
          max_context_tokens?: number;
          multimodal?: boolean;
          reasoning_levels?: string[];
          reasoning_default?: string;
        }>;
      }>("/v1/models");
      if (!res.ok || !res.data?.data) {
        return;
      }
      const raw = res.data.data
        .map((d) => ({
          id: (d.id || "").trim(),
          ownedBy: (d.owned_by || "").trim(),
          ...(d.max_context_tokens !== undefined
            ? { maxContextTokens: d.max_context_tokens }
            : {}),
          multimodal: !!d.multimodal,
          reasoningLevels: Array.isArray(d.reasoning_levels)
            ? d.reasoning_levels.map((s) => `${s}`.trim()).filter(Boolean)
            : [],
          reasoningDefault: (d.reasoning_default || "").trim(),
        }))
        .filter((d) => d.id);
      const rows: ModelInfo[] = raw.map((d) => {
        const m: ModelInfo = {
          id: d.id,
          ownedBy: d.ownedBy,
          multimodal: d.multimodal,
          reasoningLevels: d.reasoningLevels,
          reasoningDefault: d.reasoningDefault,
        };
        if (d.maxContextTokens !== undefined) {
          m.maxContextTokens = d.maxContextTokens;
        }
        return m;
      });
      setModelInfos(rows);
      const backends = raw
        .filter((r) => r.ownedBy !== "coddy")
        .map((r) => r.id);
      setLlmModelIds(backends);
      if (!viewedSessionIdRef.current.trim()) {
        setLlmModel(
          pickDefaultLlmModelForNewChat({
            backends,
            cookie: readLlmModelCookie(),
          }),
        );
      }
    })();
    // configEpoch bumps after every config swap, so a model added to models[] - and the
    // multimodal flag on one already there - reaches the picker without a page reload.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [configEpoch]);

  // Apply the opened session's saved model/reasoning once the backends list is
  // known. Runs whenever either input lands, so the restore is independent of
  // whether /v1/models or the session messages resolve first after a reload.
  useEffect(() => {
    if (!openSessionSelection || llmModelIds.length === 0) {
      return;
    }
    if (openSessionSelection.sid !== viewedSessionIdRef.current.trim()) {
      return;
    }
    // What the session was opened with is a snapshot, not a standing order. The
    // models list is refetched on every configuration reload - a settings save,
    // the agent's own config_commit - and this effect reads that list, so without
    // a guard the snapshot lands a second time and undoes the model or the level
    // the reader picked in between. Each load of a session carries its own
    // object, so applying one exactly once is the whole rule.
    if (appliedSessionSelectionRef.current === openSessionSelection) {
      return;
    }
    appliedSessionSelectionRef.current = openSessionSelection;
    // The session's own model. What the start page picked, and the cookie that
    // remembers it, are a new chat's default: shown here they would ride into
    // this session with its next message (#362).
    const nextModel = pickLlmModelForOpenSession({
      backends: llmModelIds,
      sessionModel: openSessionSelection.model,
    });
    setLlmModel(nextModel);
    // A session carries a reasoning level only once something chose one for it,
    // and a model that names no `reasoning_default` makes the server report the
    // effective level as empty. Applied as it comes, that empties the composer
    // while the turn still runs at the model's default - so it goes through the
    // same chooser as every other path, with the session's value as the
    // preference rather than as the answer, and without the cookie.
    const openRow = modelInfos.find((m) => m.id === nextModel);
    const choices =
      nextModel === openSessionSelection.model
        ? openSessionSelection.choices
        : [];
    setSessionReasoningChoices({
      sid: openSessionSelection.sid,
      model: nextModel,
      choices,
    });
    reasoningImpliedRef.current = !openSessionSelection.reasoning.trim();
    setLlmReasoning(
      pickReasoningLevel({
        levels: openRow?.reasoningLevels ?? [],
        cookie: null,
        sessionLevel: openSessionSelection.reasoning,
        sessionChoices: choices,
        modelDefault: openRow?.reasoningDefault ?? null,
      }),
    );
  }, [openSessionSelection, llmModelIds, modelInfos]);

  useEffect(() => {
    setDescribePreview((p) => (p && p.sessionId !== sessionId ? null : p));
  }, [sessionId]);

  useEffect(() => {
    const mq = window.matchMedia(wideRailMinWidthMediaQuery);
    const apply = () => setViewportXL(mq.matches);
    apply();
    mq.addEventListener("change", apply);
    return () => mq.removeEventListener("change", apply);
  }, []);

  useLayoutEffect(() => {
    if (!schedulerOpen || schedulerHttpLinked !== true) {
      setSchedDockClusterWidthPx(0);
      return;
    }
    const el = schedulerDockClusterRef.current;
    if (!el) {
      setSchedDockClusterWidthPx(0);
      return;
    }
    const ro = new ResizeObserver(() => {
      setSchedDockClusterWidthPx(Math.round(el.getBoundingClientRect().width));
    });
    ro.observe(el);
    setSchedDockClusterWidthPx(Math.round(el.getBoundingClientRect().width));
    return () => ro.disconnect();
  }, [schedulerOpen, schedulerHttpLinked, schedulerEditor]);

  useEffect(() => {
    if (!viewportXL) {
      return;
    }
    const c = readNavRailCookie();
    setRailLabelsWide(c === "wide");
  }, [viewportXL]);

  useEffect(() => {
    const t = window.setTimeout(
      () => setSessionFilterQ(sessionFilterDraft.trim()),
      300,
    );
    return () => window.clearTimeout(t);
  }, [sessionFilterDraft]);

  useEffect(() => {
    const t = window.setTimeout(
      () => setSchedulerFilterQ(schedulerFilterDraft.trim()),
      200,
    );
    return () => window.clearTimeout(t);
  }, [schedulerFilterDraft]);

  useEffect(() => {
    sessionsCursorRef.current = sessionsCursor;
  }, [sessionsCursor]);

  useEffect(() => {
    sessionsHasMoreRef.current = sessionsHasMore;
  }, [sessionsHasMore]);

  useEffect(() => {
    sessionsLoadingMoreRef.current = sessionsLoadingMore;
  }, [sessionsLoadingMore]);

  // Forgets the archive moves and removals that no listing still out, or yet
  // to be issued, can be affected by (archiveMoves.ts).
  const pruneArchiveLists = useCallback(() => {
    const open = sessionsListOpenRef.current;
    const oldest =
      open.size > 0 ? Math.min(...open) : sessionsListSeqRef.current + 1;
    archiveRemovalsRef.current = pruneArchiveBookkeeping(
      archiveMovesRef.current,
      archiveRemovalsRef.current,
      oldest,
    );
  }, []);

  const loadSessionsList = useCallback(
    async (reset: boolean): Promise<SessionRow[] | null> => {
      if (reset) {
        sessionsCursorRef.current = null;
        setSessionsCursor(null);
      } else if (
        !sessionsHasMoreRef.current ||
        sessionsLoadingMoreRef.current
      ) {
        return null;
      }
      if (!reset) {
        sessionsLoadingMoreRef.current = true;
        setSessionsLoadingMore(true);
      }
      const ps = new URLSearchParams();
      ps.set("limit", "30");
      if (!reset) {
        // An archive still in flight may reach the server before this page
        // does and move every row after it up by one; starting a row earlier
        // per such archive costs at worst a row fetched twice, which the merge
        // below drops by id, where the plain offset would skip one.
        let inFlight = 0;
        for (const move of archiveMovesRef.current.values()) {
          if (move.settledAtSeq === null && move.removesLoaded) inFlight++;
        }
        const cur = cursorAfterRemovals(sessionsCursorRef.current, inFlight);
        if (cur) {
          ps.set("cursor", cur);
        }
      }
      if (sessionFilterQ) {
        ps.set("q", sessionFilterQ);
      }
      ps.set("archived", sessionsArchiveFilter);
      if (sessionsOrigin) {
        ps.set("origin", sessionsOrigin);
      }
      ps.set("sort", sessionsSortKey);
      ps.set("order", defaultSortOrder(sessionsSortKey));
      ps.set("include_activity", "true");
      const seq = ++sessionsListSeqRef.current;
      if (reset) sessionsListResetSeqRef.current = seq;
      sessionsListOpenRef.current.add(seq);
      let res: { ok: boolean; status: number; data?: SessionsPage };
      try {
        res = await fetchJSON<SessionsPage>(
          `/coddy/sessions?${ps.toString()}`,
          {
            headers,
          },
        );
      } catch {
        // A request that never got an answer; without this the loading flag
        // below stayed up and History never asked for the page again.
        res = { ok: false, status: 0 };
      } finally {
        sessionsListOpenRef.current.delete(seq);
      }
      if (!reset) {
        sessionsLoadingMoreRef.current = false;
        setSessionsLoadingMore(false);
      }
      // The list was read again from the top after this request left: its
      // rows and its offset belong to the listing that read replaced (another
      // filter, another order, or the same one before an archive).
      if (seq < sessionsListResetSeqRef.current) {
        pruneArchiveLists();
        return null;
      }
      if (!res.ok || !res.data) {
        setSessionsError(t("app.backendUnavailable", { status: res.status }));
        return null;
      }
      setSessionsError(null);
      if (typeof res.data.active_count === "number") {
        setHistoryActiveCount(res.data.active_count);
      }
      // A listing issued before an archive this tab made had settled still
      // lists the row as it was, and counts it in the offset it hands back.
      const next = overlayArchiveMoves(
        res.data.sessions || [],
        archiveMovesRef.current,
        seq,
        sessionsArchiveFilter,
      );
      const removedSince = archiveRemovalsRef.current.filter(
        (at) => at >= seq,
      ).length;
      pruneArchiveLists();
      setSessions((prev) => {
        if (reset) {
          return next;
        }
        const seen = new Set(prev.map((s) => s.id));
        return [...prev, ...next.filter((s) => !seen.has(s.id))];
      });
      const nextCur = cursorAfterRemovals(
        res.data.nextCursor ?? null,
        removedSince,
      );
      setSessionsCursor(nextCur);
      sessionsCursorRef.current = nextCur;
      const hm = !!res.data.hasMore;
      setSessionsHasMore(hm);
      sessionsHasMoreRef.current = hm;
      return next;
    },
    [
      sessionFilterQ,
      sessionsArchiveFilter,
      sessionsOrigin,
      sessionsSortKey,
      headers,
      t,
      pruneArchiveLists,
    ],
  );

  useEffect(() => {
    void (async () => {
      const res = await fetchJSON<Record<string, unknown>>("/coddy/config", {
        headers,
      });
      if (res.ok && res.data) {
        const policy = parseToolsPermissionPolicy(res.data);
        toolsPermissionPolicyRef.current = policy;
        setToolsPermissionPolicy(policy);
        const agent = res.data.agent as { queue_mode?: QueueMode } | undefined;
        const preferred = agent?.queue_mode;
        setQueueMode(
          preferred === "steer" || preferred === "after_turn"
            ? preferred
            : undefined,
        );
        const compaction = res.data.compaction as
          | Record<string, unknown>
          | undefined;
        const threshold = Number(compaction?.threshold_percent);
        setCompactionSettings({
          enabled: compaction?.enable !== false,
          autoEnabled: compaction?.auto_enable !== false,
          threshold: threshold >= 1 && threshold <= 100 ? threshold : 80,
        });
      }
    })();
  }, [headers, configEpoch]);

  useEffect(() => {
    const sid = sessionId.trim();
    const viewedPromptPending =
      sid !== "" &&
      items.some((x) => x.type === "permission_prompt" && !x.resolved);
    setPermissionPendingSids((prev) =>
      reconcilePermissionPendingSessionIds(
        sessions,
        prev,
        sid,
        viewedPromptPending,
      ),
    );
  }, [sessions, items, sessionId]);

  useEffect(() => {
    const sid = sessionId.trim();
    const viewedPromptPending =
      sid !== "" &&
      items.some((x) => x.type === "question_prompt" && !x.resolved);
    setQuestionPendingSids((prev) =>
      reconcileQuestionPendingSessionIds(
        sessions,
        prev,
        sid,
        viewedPromptPending,
      ),
    );
  }, [sessions, items, sessionId]);

  // /coddy/config may resolve after the first loadMessages; re-synthesize permission_prompt rows then.
  useEffect(() => {
    const sid = sessionId.trim();
    if (!sid || !toolsPermissionPolicy) {
      return;
    }
    setItems((prev) => {
      if (prev.length === 0) {
        return prev;
      }
      const merged = mergePermissionPromptsIntoTranscript(
        prev,
        sid,
        toolsPermissionPolicy,
      );
      const hadPending = prev.some(
        (x) => x.type === "permission_prompt" && !x.resolved,
      );
      const hasPending = merged.some(
        (x) => x.type === "permission_prompt" && !x.resolved,
      );
      if (hadPending === hasPending && merged.length === prev.length) {
        return prev;
      }
      return merged;
    });
  }, [toolsPermissionPolicy, sessionId]);

  useEffect(() => {
    const hasBackgroundTurn = sessions.some(
      (s) => !!s.turnActive && s.id !== sessionId,
    );
    const anyLocalComposer = activeComposerSidRef.current.size > 0;
    // With the events stream up, a background turn announces itself, so the poll only
    // has to keep a local composer's stats and unread flags fresh. When it is down (an
    // older server, a proxy that eats SSE) the previous behaviour is restored verbatim
    // rather than leaving the sidebar frozen.
    const pollForBackground = hasBackgroundTurn && !serverEventsConnected;
    if (!anyLocalComposer && !pollForBackground) {
      return;
    }
    const timer = window.setInterval(() => {
      void loadSessionsList(true);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [
    composerActivityEpoch,
    sessions,
    sessionId,
    loadSessionsList,
    serverEventsConnected,
  ]);

  // Handlers are read through a ref so the subscription below can mount once: it must
  // survive re-renders, and the callbacks it needs are redefined on every one of them.
  serverEventHandlersRef.current = {
    sessionSettings: (event: SessionSettingsEvent) =>
      applySessionSettings(event.settings),
    // A goal set in a console, paused in another tab or checked by the
    // supervisor shows here too, not only in the tab reading the turn.
    sessionGoal: (update: SessionGoalUpdate) => applySessionGoal(update),
    turnStarted: (sid: string) => {
      void loadSessionsList(true);
      const key = sid.trim();
      if (
        key !== viewedSessionIdRef.current.trim() &&
        turnActivity.get(key) === undefined
      )
        return;
      stoppedTurnBySidRef.current.delete(key);
      const pendingStop = stopPendingBySidRef.current.get(key);
      if (pendingStop) pendingStop.superseded = true;
      turnActivity.observe(key, true);
      void attachViewedComposer(key);
    },
    turnEnded: (sid: string) => {
      void loadSessionsList(true);
      const key = sid.trim();
      if (!key) return;
      if (
        key !== viewedSessionIdRef.current.trim() &&
        turnActivity.get(key) === undefined
      )
        return;
      // The event has no turn identity: an owned or observed successor may
      // already be active. Keep polling until REST confirms admission is idle.
      void turnActivity.refresh(key);
    },
    providerUsage: providerUsageState.applyPushed,
    // The configuration swapped: every config-derived list reads again, and
    // so does the copy of the config the Settings drawer keeps.
    configReloaded: () => {
      setConfigEpoch((e) => e + 1);
      noteSettingsConfigReloaded();
      void refreshConfiguredRemotes();
    },
    // A session is shared: this is what someone else queued, in another
    // browser or from a console attached over --remote.
    messageQueue: (sid: string, queue: QueuedMessageEvent) =>
      applyQueue(sid, queue.messages, queue.version),
    // A background subagent of the chat on screen started or stopped waiting
    // for an answer; its prompt lives on the task row the chat renders.
    subagentPermission: (parentSid: string) => {
      if (parentSid.trim() === viewedSessionIdRef.current.trim()) {
        void refreshBackgroundTasks({ silent: true });
      }
    },
    // The question itself stays on the composer stream. The global event only
    // tells every History view to re-read the row's questionPending flag.
    questionPending: () => {
      void loadSessionsList(true);
    },
    // A surface - this tab, another tab, a console over --remote - rewound the
    // session: the tail it held is gone, so the shadow transcript and the
    // prompts of the removed turns are dropped and the kept prefix reloads.
    sessionRewound: (sid: string) => {
      const key = sid.trim();
      if (!key) return;
      // A tab that is streaming a turn - or holding an in-flight POST - for
      // that session owns its transcript right now; most often this is the
      // tab that issued the rewind itself and is already clearing the shadow
      // and resending, so a refetch here would paint the truncated snapshot
      // over the live stream.
      if (
        activeComposerSidRef.current.has(key) ||
        postAbortBySidRef.current.has(key)
      ) {
        return;
      }
      streamShadowBySidRef.current.delete(key);
      clearPermissionPromptRecords(key);
      void loadMessages(key, { freshLoad: true });
    },
    ready: () => {
      void loadSessionsList(true);
      // A config_reloaded may have been missed while the stream was down: the
      // Settings copy is read again if the drawer ever held one, and so is the
      // permission mode a new chat starts under.
      noteSettingsConfigReloaded();
      void readServerPermissionMode();
      // Recovery can miss the idle edge. Retire pending acknowledgements too,
      // so an old Stop cannot re-establish the fence after this reconnect.
      stoppedTurnBySidRef.current.clear();
      for (const request of stopPendingBySidRef.current.values())
        request.superseded = true;
      turnActivity.ready();
    },
  };

  useEffect(() => {
    const ctl = new AbortController();
    // One connection for every tab of this environment where the browser allows
    // it: a browser keeps six HTTP/1.1 connections per host for all of its tabs.
    // The environment read here holds for this app: a switch to another one,
    // or a token rotated under this one, starts another app (EnvScope).
    const env = getEnv();
    void subscribeSharedServerEvents({
      env,
      onRefused: (status) => {
        if (status === 401 && env.mode === "local")
          notifyLocalApiUnauthorized();
      },
      onTurnStarted: (sid) => serverEventHandlersRef.current.turnStarted(sid),
      onTurnEnded: (sid) => {
        // Whatever surface ran it, the turn may have edited the folder.
        emitChangesSettled(sid);
        serverEventHandlersRef.current.turnEnded(sid);
      },
      onProviderUsage: (_sid, usage) =>
        serverEventHandlersRef.current.providerUsage(usage),
      onConfigReloaded: () => serverEventHandlersRef.current.configReloaded(),
      onMessageQueue: (sid, queue) =>
        serverEventHandlersRef.current.messageQueue(sid, queue),
      onSessionChanges: (sid) => emitChangesSettled(sid),
      onSessionSettings: (event) =>
        serverEventHandlersRef.current.sessionSettings(event),
      onSessionGoal: (update) =>
        serverEventHandlersRef.current.sessionGoal(update),
      onSubagentPermission: (parentSid) =>
        serverEventHandlersRef.current.subagentPermission(parentSid),
      onQuestionPending: (sid) =>
        serverEventHandlersRef.current.questionPending(sid),
      onSessionRewound: (sid) =>
        serverEventHandlersRef.current.sessionRewound(sid),
      onConnectedChange: setServerEventsConnected,
      onReady: () => serverEventHandlersRef.current.ready(),
      signal: ctl.signal,
    });
    return () => ctl.abort();
  }, []);

  useEffect(() => {
    if (!sessionsOpen) {
      return;
    }
    void loadSessionsList(true);
  }, [sessionsOpen, sessionFilterQ, loadSessionsList]);

  /**
   * The messages revision each transcript returned by loadMessages was read at, for
   * a transcript that is the server's own snapshot and nothing more. Attaching to a
   * running turn with it asks the relay only for the frames that snapshot lacks;
   * a transcript that kept local rows past the snapshot has none, and attaches the
   * way it always did.
   */
  const snapshotRevByTranscript = useRef(
    new WeakMap<readonly TranscriptItem[], number>(),
  );

  type LoadMessagesOpts = {
    skipSetItems?: boolean;
    preserveOnError?: boolean;
    freshLoad?: boolean;
    /** Start the live window over from the newest page while keeping the
     *  merges of an ordinary reload (a turn has just ended). */
    rebase?: boolean;
  };

  /**
   * Reads a session's transcript into its live window. A session opens on
   * its newest page (and so does a fresh load: a rewind, a switch); every
   * later read re-reads the live window it holds, from the same message, so
   * the server list and the local one start together and the positional
   * merges line up. Older pages never take part: they sit above the live
   * window in `olderTranscript` (issue #338).
   */
  /**
   * Mirrors what a messages read says about the settings of the session on
   * screen: the whole snapshot - the model, the level, the mode, the
   * permission mode, the overrides for the next turns, and the version the
   * next send names. The transcript read and the settings-only read of a
   * running chat both come here.
   */
  function mirrorSettingsRead(
    sid: string,
    data: {
      settings?: unknown;
      model?: string;
      selectedModelId?: string;
      selectedReasoning?: string;
    },
  ) {
    const snap = parseSessionSettings(data.settings);
    // Stash the session's own selection; an effect applies it once the
    // backends list is loaded (the two fetches race on reload). It is the
    // snapshot's: the top-level model and selectedReasoning name what a
    // running turn holds, and a turn override taken for the session's
    // model would become it with the next message. A read whose snapshot
    // is older than the one this tab already applied - the events stream
    // got ahead of a slow read - says nothing new and moves nothing back.
    // The same version still stashes a model the backends list did not hold
    // when the events stream applied that snapshot first, and dropped it.
    const held =
      settingsVersionRef.current.sid === sid
        ? settingsVersionRef.current.version
        : 0;
    const modelDropped =
      !!snap &&
      snap.sessionId === sid &&
      snap.version === held &&
      !llmModelIds.includes(snap.model);
    if (!snap || isNewerSettings(held, sid, snap) || modelDropped) {
      setOpenSessionSelection({
        sid,
        model: snap
          ? snap.model
          : (data.model || data.selectedModelId || "").trim(),
        reasoning: snap
          ? snap.reasoning
          : (data.selectedReasoning || "").trim(),
        choices: snap?.reasoningChoices ?? [],
      });
    }
    if (snap) {
      applySessionSettings(snap);
    }
  }

  /**
   * Reads the settings of the session on screen alone, for the visit that
   * keeps its rows from the shadow of a turn this tab runs and so reads no
   * transcript.
   */
  async function readViewedSettings(sid: string): Promise<OpeningRead> {
    try {
      const res = await fetchJSON<{
        settings?: unknown;
        model?: string;
        selectedModelId?: string;
        selectedReasoning?: string;
      }>(`/coddy/sessions/${encodeURIComponent(sid)}/messages?limit=1`, {
        headers: sid === sessionId ? headers : { [HDR]: sid },
      });
      if (viewedSessionIdRef.current.trim() !== sid) return "superseded";
      if (res.status === 404) return "missing";
      if (!res.ok || !res.data) return "failed";
      mirrorSettingsRead(sid, res.data);
      return "read";
    } catch {
      return "failed";
    }
  }

  /**
   * The server has no session under the id on screen: there is nothing to
   * wait for, and the first message creates it with what the selectors show.
   * They must not show another session's settings, so a snapshot of another
   * session gives way to a new chat's; the start screen's picks for a chat
   * this tab is creating (no snapshot held yet) stay.
   */
  function settleUnknownSession(sid: string) {
    if (viewedSessionIdRef.current.trim() !== sid) return;
    const held = settingsVersionRef.current.sid;
    if (held !== "" && held !== sid) resetNewChatSettings();
    setSessionLoading(false);
  }

  async function loadMessages(
    idOverride?: string,
    opts?: LoadMessagesOpts,
  ): Promise<TranscriptItem[] | null> {
    const sid = (idOverride ?? sessionId).trim();
    if (!sid) {
      setItems([]);
      return null;
    }
    const held = opts?.freshLoad ? undefined : heldLiveWindow(sid);
    const request: TranscriptPageRequest =
      opts?.freshLoad ||
      opts?.rebase ||
      !held ||
      windowRebaseInFlightRef.current.has(sid)
        ? { kind: "tail" }
        : { kind: "from", offset: held.offset };
    // A read that starts the window over supersedes every read issued before
    // it: what they bring back was asked against a window that is gone.
    let seq: number;
    if (request.kind === "tail") {
      windowRebaseSeqRef.current += 1;
      seq = windowRebaseSeqRef.current;
      windowRebaseInFlightRef.current.set(sid, seq);
    } else {
      seq = held!.seq;
    }
    const isCurrentWindowRead = () =>
      request.kind === "tail"
        ? windowRebaseInFlightRef.current.get(sid) === seq
        : !windowRebaseInFlightRef.current.has(sid) &&
          (liveWindowBySidRef.current.get(sid)?.seq ?? 0) === seq;
    try {
      return await readTranscriptWindow(
        sid,
        opts,
        request,
        seq,
        isCurrentWindowRead,
      );
    } finally {
      if (
        request.kind === "tail" &&
        windowRebaseInFlightRef.current.get(sid) === seq
      ) {
        windowRebaseInFlightRef.current.delete(sid);
      }
    }
  }

  async function readTranscriptWindow(
    sid: string,
    opts: LoadMessagesOpts | undefined,
    request: TranscriptPageRequest,
    seq: number,
    isCurrentWindowRead: () => boolean,
  ): Promise<TranscriptItem[] | null> {
    const streamGeneration = streamGenerationBySidRef.current.get(sid);
    const sameStream = () =>
      streamGenerationBySidRef.current.get(sid) === streamGeneration;
    // When the read was issued, for the archive flag ordering below: a reply
    // that lands after this client's archive PATCH may carry the flag from
    // before the write, and the write is what must stay on screen.
    const issuedAt = Date.now();
    const activateMCP =
      request.kind === "tail" &&
      sid === sessionId &&
      !activatedMCPSelectionsRef.current.has(sid);
    if (activateMCP) activatedMCPSelectionsRef.current.add(sid);
    const pageQuery = transcriptPageQuery(request);
    const activationQuery = activateMCP
      ? `${pageQuery}${pageQuery ? "&" : "?"}activate_mcp=1`
      : pageQuery;
    const res = await fetchJSON<{
      window?: unknown;
      messages: Array<any>;
      model?: string;
      selectedModelId?: string;
      selectedReasoning?: string;
      settings?: unknown;
      goal?: unknown;
      subagent?: {
        parentSessionId?: string;
        name?: string;
        taskId?: string;
      } | null;
      readOnly?: boolean;
      archived?: boolean;
      rewindUndo?: { userMessageIndex?: number } | null;
      messagesRev?: number;
      uiLog?: Array<{
        id?: string;
        level?: string;
        message?: string;
        userTurnIndex?: number;
        createdAt?: string;
      }>;
    }>(
      `/coddy/sessions/${encodeURIComponent(sid)}/messages${activationQuery}`,
      {
        headers: sid === sessionId ? headers : { [HDR]: sid },
      },
    );
    if (!isCurrentWindowRead()) return null;
    // Re-read the viewed session after the await: the viewer may have moved
    // on while the request was in flight, and a stale response must neither
    // clear the new session's rows nor merge them into this session's shadow.
    const viewingNow = viewedSessionIdRef.current.trim();
    if (!sameStream()) return null;
    if (!res.ok || !res.data) {
      failedReadStatusRef.current.set(sid, res.status);
      if (!opts?.preserveOnError) {
        if (viewingNow === sid) {
          setItems([]);
        }
      }
      return null;
    }
    if (viewingNow === sid) {
      mirrorSettingsRead(sid, res.data);
      // The session goal, versioned like the settings: the chip and the
      // popover mirror it, and a read older than an event already applied
      // moves nothing back.
      const goalUpdate = parseSessionGoalUpdate(res.data.goal, sid);
      if (goalUpdate) {
        applySessionGoal(goalUpdate);
      }
      // A child session locks the composer; an ordinary one carries no marker.
      setSubagentTranscript(parseSubagentTranscriptMeta(res.data));
      // The composer learns from the transcript, not from the session list: the
      // list skips the archive, so the conversation on screen may be in no page
      // the client holds. A transcript read issued before this client's own
      // archive PATCH settled may carry the flag from before the write; the
      // PATCH's answer is newer, so it wins while it is younger than the read.
      const archiveWrite = viewedArchiveWriteRef.current;
      setViewedArchived(
        archiveWrite && archiveWrite.sid === sid && archiveWrite.at >= issuedAt
          ? archiveWrite.archived
          : !!res.data.archived,
      );
      // Every read says whether the last rewind can still be taken back.
      const undoIdx = res.data.rewindUndo?.userMessageIndex;
      const offered = typeof undoIdx === "number" && undoIdx >= 0;
      setRewindUndo(offered ? { sid, userMsgIdx: undoIdx } : null);
      // A banner put away belongs to that undo only: once it is gone, the
      // next edit of the same prompt announces itself again.
      if (!offered) setRewindUndoDismissed("");
    }
    const pageMessages = res.data.messages || [];
    const pageWindow = parseTranscriptWindow(
      res.data.window,
      pageMessages.length,
    );
    // A window re-read that finds nothing where the held one starts: the
    // history was cut there (a rewind from another surface) or shortened
    // under it. What the client holds no longer lines up with the server, so
    // it starts over from the newest page.
    if (
      request.kind === "from" &&
      (pageWindow.total < request.offset ||
        (pageMessages.length === 0 &&
          ((viewingNow === sid && itemsRef.current.length > 0) ||
            (streamShadowBySidRef.current.get(sid)?.length ?? 0) > 0)))
    ) {
      return loadMessages(sid, { ...opts, freshLoad: true });
    }
    const mapped = transcriptItemsFromMessages({
      messages: pageMessages,
      window: pageWindow,
      uiLog: res.data.uiLog,
      newId,
      reasoningDurations: reasoningDurationMsByContentRef.current,
    });
    const next = mapped.items;

    // Enrich tool calls with persisted previews when available.
    const tcRes = await fetchJSON<{ toolCalls: ToolCallListRow[] }>(
      `/coddy/sessions/${encodeURIComponent(sid)}/tool-calls${transcriptToolCallsQuery(pageWindow, pageMessages.length)}`,
      {
        headers: sid === sessionId ? headers : { [HDR]: sid },
      },
    );
    if (tcRes.ok && tcRes.data?.toolCalls) {
      applyToolCallRows(next, mapped.toolIndex, tcRes.data.toolCalls);
    }
    if (!isCurrentWindowRead()) return null;
    if (!sameStream()) return null;
    // The local lists start where the live window held so far starts. When
    // this read starts it somewhere else (the newest page after a slide back
    // or a rewind), they are not merged position by position with it.
    const heldOffset = liveWindowBySidRef.current.get(sid)?.offset ?? 0;
    const localAligned = heldOffset === pageWindow.offset;
    const prevShadow = localAligned
      ? streamShadowBySidRef.current.get(sid)
      : undefined;
    // freshLoad: don't inherit stale items from a previous session (e.g. when first loading a session).
    const localForMerge = !localAligned
      ? undefined
      : opts?.freshLoad
        ? prevShadow && prevShadow.length > 0
          ? prevShadow
          : undefined
        : prevShadow && prevShadow.length > 0
          ? prevShadow
          : viewedSessionIdRef.current.trim() === sid
            ? itemsRef.current
            : undefined;
    const mergedTranscript = mergeTranscriptPreferLocalSuffix(
      next,
      localForMerge,
    );
    const mergedBase = preserveUserMessageFiles(
      mergedTranscript,
      localForMerge,
    );
    revokeSupersededUserMessagePreviews(mergedBase, localForMerge);
    let merged = reattachLocalQuestionPrompts(mergedBase, localForMerge);
    merged = mergePermissionPromptsIntoTranscript(
      merged,
      sid,
      toolsPermissionPolicyRef.current,
    );
    merged = mergeStoredQuestionPromptsIntoTranscript(merged, sid);
    merged = patchQuestionToolArgsFromPromptRecords(merged, sid);
    const appliedRaw = localAligned
      ? (keepLocalTranscriptIfServerEmpty({
          serverNext: merged,
          sid,
          viewingSid: viewingNow,
          prevShadow,
          prevItems: itemsRef.current,
        }) ?? merged)
      : merged;
    const withStableIds = preserveTranscriptItemIds(
      appliedRaw,
      localAligned
        ? (localForMerge ?? prevShadow ?? itemsRef.current)
        : alignedTranscriptSuffix(
            appliedRaw,
            streamShadowBySidRef.current.get(sid) ??
              (viewedSessionIdRef.current.trim() === sid
                ? itemsRef.current
                : undefined),
          ),
    );
    const applied = dedupeAdjacentDuplicateThinkingCompleted(withStableIds);
    const snapshotRev = res.data.messagesRev;
    const serverOnly = mergedTranscript === next && appliedRaw === merged;
    const hasPendingPermission = applied.some(
      (x) => x.type === "permission_prompt" && !x.resolved,
    );
    setPermissionPendingSids((prev) => {
      const next = new Set(prev);
      if (hasPendingPermission) {
        next.add(sid);
      } else {
        next.delete(sid);
      }
      return next;
    });
    const hasPendingQuestion = hasUnresolvedQuestionPrompt(applied);
    setQuestionPendingSids((prev) => {
      const next = new Set(prev);
      if (hasPendingQuestion) {
        next.add(sid);
      } else {
        next.delete(sid);
      }
      return next;
    });
    const noteSnapshotRev = (items: readonly TranscriptItem[]) => {
      if (serverOnly && typeof snapshotRev === "number") {
        snapshotRevByTranscript.current.set(items, snapshotRev);
      }
    };
    if (opts?.skipSetItems) {
      streamShadowBySidRef.current.set(sid, applied);
      noteLiveWindow(sid, pageWindow, seq);
      evictStaleSessionCaches(viewedSessionIdRef.current);
      noteSnapshotRev(applied);
      return applied;
    }

    if (!sameStream()) return null;
    // Frames may have arrived while the messages request was in flight.
    const finalItems = localAligned
      ? mergeTranscriptPreferLocalSuffix(
          applied,
          streamShadowBySidRef.current.get(sid),
        )
      : applied;
    if (finalItems === applied) noteSnapshotRev(finalItems);
    streamShadowBySidRef.current.set(sid, finalItems);
    noteLiveWindow(sid, pageWindow, seq);
    evictStaleSessionCaches(viewedSessionIdRef.current);
    // The viewer moved on while this fetch was in flight (the user picked
    // another session or went home): keep the shadow for the next visit, but
    // never paint a stale transcript under the current route.
    if (viewedSessionIdRef.current.trim() !== sid) {
      return finalItems;
    }
    if (fadeOutTimerRef.current !== null) {
      clearTimeout(fadeOutTimerRef.current);
      fadeOutTimerRef.current = null;
    }
    setSessionFadingOut(false);
    setItems(finalItems);
    setSessionLoading(false);
    return finalItems;
  }

  /**
   * Reads the page of history just above what the session on screen holds
   * and puts it on top, when the reader has scrolled up to the oldest row.
   * The page is history that no longer changes: it is mapped with the same
   * function as the live window, gets its tool previews and stored prompts,
   * and then stays out of every reload. It is kept only if nothing moved while
   * it was read - the session on screen, the live window it borders, the
   * older pages it joins.
   */
  async function loadOlderTranscript(): Promise<void> {
    const sid = viewedSessionIdRef.current.trim();
    const live = sid ? liveWindowBySidRef.current.get(sid) : undefined;
    if (!sid || !live || olderLoadInFlightRef.current) return;
    const heldOlder = olderTranscriptRef.current;
    const older =
      heldOlder &&
      heldOlder.sessionId === sid &&
      heldOlder.generation === live.seq
        ? heldOlder
        : null;
    const before = older ? older.window.offset : live.offset;
    if (before <= 0) return;
    olderLoadInFlightRef.current = sid;
    setOlderTranscriptLoad("loading");
    // Every way out settles the control: a page that arrived, a read that
    // failed, or a read made moot by a window that moved meanwhile, which
    // leaves the control ready to ask again rather than loading forever.
    let settled = false;
    const stillCurrent = () => {
      const nowLive = liveWindowBySidRef.current.get(sid);
      const cur = olderTranscriptRef.current;
      const curOlder =
        cur && cur.sessionId === sid && cur.generation === live.seq
          ? cur
          : null;
      return (
        viewedSessionIdRef.current.trim() === sid &&
        nowLive?.seq === live.seq &&
        (curOlder ? curOlder.window.offset : nowLive.offset) === before
      );
    };
    try {
      const reqHeaders = { [HDR]: sid };
      const res = await fetchJSON<{
        window?: unknown;
        messages?: Array<any>;
        uiLog?: RawUiLogRow[];
      }>(
        `/coddy/sessions/${encodeURIComponent(sid)}/messages${transcriptPageQuery({ kind: "older", before })}`,
        { headers: reqHeaders },
      );
      if (!stillCurrent()) return;
      const pageMessages = res.data?.messages ?? [];
      const pageWindow = parseTranscriptWindow(
        res.data?.window,
        pageMessages.length,
      );
      // A server that ignores the window hands back the whole history; a page
      // that does not end where the held rows start cannot be joined to them.
      if (
        !res.ok ||
        pageMessages.length === 0 ||
        pageWindow.offset + pageMessages.length !== before
      ) {
        setOlderTranscriptLoad("error");
        settled = true;
        return;
      }
      const mapped = transcriptItemsFromMessages({
        messages: pageMessages,
        window: pageWindow,
        uiLog: res.data?.uiLog,
        newId,
        reasoningDurations: reasoningDurationMsByContentRef.current,
      });
      const tcRes = await fetchJSON<{ toolCalls: ToolCallListRow[] }>(
        `/coddy/sessions/${encodeURIComponent(sid)}/tool-calls${transcriptToolCallsQuery(pageWindow, pageMessages.length)}`,
        { headers: reqHeaders },
      );
      if (!stillCurrent()) return;
      if (tcRes.ok && tcRes.data?.toolCalls) {
        applyToolCallRows(mapped.items, mapped.toolIndex, tcRes.data.toolCalls);
      }
      let page = mergePermissionPromptsIntoTranscript(
        mapped.items,
        sid,
        toolsPermissionPolicyRef.current,
      );
      page = mergeStoredQuestionPromptsIntoTranscript(page, sid);
      page = patchQuestionToolArgsFromPromptRecords(page, sid);
      page = dedupeAdjacentDuplicateThinkingCompleted(page);
      const joined = prependOlderPage(
        page,
        older?.items ?? [],
        itemsRef.current,
        newId,
      );
      setOlderTranscript({
        sessionId: sid,
        generation: live.seq,
        items: joined,
        window: pageWindow,
      });
      setOlderTranscriptLoad("idle");
      settled = true;
    } catch {
      if (stillCurrent()) {
        setOlderTranscriptLoad("error");
        settled = true;
      }
    } finally {
      if (olderLoadInFlightRef.current === sid)
        olderLoadInFlightRef.current = "";
      if (!settled) {
        setOlderTranscriptLoad((cur) => (cur === "loading" ? "idle" : cur));
      }
    }
  }

  /**
   * The reader is back at the newest message: what was read of the history
   * on the way up is released, and a live window that grew through many turns
   * starts over from the newest page. Only while nothing runs in the session,
   * so no stream, prompt or answer in flight is touched; the rows it drops
   * are far above the screen, which stays pinned to the newest message.
   */
  function slideTranscriptToTail(): void {
    const sid = viewedSessionIdRef.current.trim();
    if (!sid || !readerAtTailRef.current) return;
    if (
      turnActivity.get(sid) === true ||
      activeComposerSidRef.current.has(sid) ||
      postAbortBySidRef.current.has(sid) ||
      relayAbortBySidRef.current.has(sid) ||
      itemsRef.current.some(
        (x) =>
          (x.type === "permission_prompt" || x.type === "question_prompt") &&
          !x.resolved,
      )
    ) {
      return;
    }
    if (olderTranscriptRef.current) {
      setOlderTranscript(null);
      setOlderTranscriptLoad("idle");
    }
    const live = liveWindowBySidRef.current.get(sid);
    if (live && live.total - live.offset > LIVE_WINDOW_REBASE_MESSAGES) {
      void loadMessages(sid, { freshLoad: true, preserveOnError: true });
    }
  }

  function persistComposerDraftBeforeLeave() {
    if (sessionId.trim()) {
      return;
    }
    const text = draft.trim();
    const existing = activeDraftId.trim();
    if (!text && !existing) {
      return;
    }
    const localId = existing || newClientDraftId();
    const rows = upsertClientDraftSession({
      localId,
      draftText: text,
      updatedAt: new Date().toISOString(),
    });
    setClientDraftSessions(rows);
  }

  function pickSession(id: string) {
    if (isRedundantSessionPick(id, sessionId)) {
      return;
    }
    persistComposerDraftBeforeLeave();
    reasoningDurationMsByContentRef.current = new Map();
    if (fadeOutTimerRef.current !== null) {
      clearTimeout(fadeOutTimerRef.current);
      fadeOutTimerRef.current = null;
    }
    if (isClientDraftSessionId(id)) {
      setSessionFadingOut(false);
      setItems([]);
      setActiveDraftId(id);
      setSessionId("");
      viewedSessionIdRef.current = "";
      const row = readClientDraftSessions().find((r) => r.localId === id);
      setDraft(row?.draftText || "");
      setDraftHashInLocation(id, { historySidebar: sessionsOpen });
      evictStaleSessionCaches("");
      return;
    }
    setSessionLoading(true);
    setActiveDraftId("");
    const keepTasksOpen =
      tasksOpen &&
      sessionsOpen &&
      !window.matchMedia(shellStackMaxWidthMediaQuery).matches;
    openSessionFromRoute(id, {
      historySidebar: sessionsOpen,
      tasksOpen: keepTasksOpen,
    });
    if (itemsRef.current.length > 0) {
      setSessionFadingOut(true);
      fadeOutTimerRef.current = setTimeout(() => {
        fadeOutTimerRef.current = null;
        setSessionFadingOut(false);
        setItems([]);
      }, 110);
    } else {
      setItems([]);
    }
    streamShadowBySidRef.current.touch(id);
    evictStaleSessionCaches(id);
  }

  /** "Ask the agent" in the reader: a fresh chat with the page mentioned and
   *  the selection quoted, ready to be finished and sent. */
  function askAboutDocs(draft: string) {
    goHome();
    setDocsRoute(null);
    setDraft(draft);
  }

  /**
   * Puts the composer's selectors back to a new chat's: Agent mode, the default
   * model and level, the configured permission mode. goHome does it, and so
   * does every other way to the start screen (the Back button to "#/", a draft
   * in History) and a session the server does not have: the first message
   * creates the session with whatever the selectors show.
   */
  function resetNewChatSettings() {
    // Drop any stashed session selection so its restore effect cannot reapply
    // the old session's model over the new chat default.
    setOpenSessionSelection(null);
    setSessionReasoningChoices({ sid: "", model: "", choices: [] });
    // A new chat sends its level with the first message: that is its own.
    reasoningImpliedRef.current = false;
    // A new chat runs under the configured permission mode until it is changed.
    settingsVersionRef.current = { sid: "", version: 0 };
    // The mode is one more of the session's settings: the chat left may have
    // been in Plan or Ask, and a new one is created in the mode shown.
    setMode("agent");
    // The start screen's chip names the configured mode again (or a mode
    // picked there): it is derived, so nothing of the session left is shown.
    setPendingPermissionMode("");
    setSettingsOverrides([]);
    if (llmModelIds.length > 0) {
      const model = pickDefaultLlmModelForNewChat({
        backends: llmModelIds,
        cookie: readLlmModelCookie(),
      });
      setLlmModel(model);
      // A new chat starts from this surface's defaults, never from the level
      // the session it left behind ran at (#362): the cookie, then the model's
      // default. A model whose row has not arrived is left to the clamp effect.
      const row = modelInfos.find((m) => m.id === model);
      if (row) {
        setLlmReasoning(
          pickReasoningLevel({
            levels: row.reasoningLevels ?? [],
            cookie: readReasoningCookie(),
            modelDefault: row.reasoningDefault ?? null,
          }),
        );
      }
    }
  }
  function goHome() {
    // The chat left hands nothing of its workspace to the next one: the start
    // screen opens on the folder last picked in this browser (the effect on
    // the session id reads it once the chat is gone), on that folder's branch.
    persistComposerDraftBeforeLeave();
    setSessionsOpen(false);
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    if (fadeOutTimerRef.current !== null) {
      clearTimeout(fadeOutTimerRef.current);
      fadeOutTimerRef.current = null;
    }
    clearSessionRoute();
    setHeroHomeGeneration((g) => g + 1);
    setItems([]);
    setSessionLoading(false);
    setSessionFadingOut(false);
    setDraft("");
    setTokenUsage(null);
    setContextBreakdown(null);
    setDescribePreview(null);
    reasoningDurationMsByContentRef.current = new Map();
    evictStaleSessionCaches("");
    resetNewChatSettings();
    // Nor does it hold the goal of the chat just left: the next snapshot of
    // that session is read afresh, whatever version a restarted server gives.
    goalVersionRef.current = { sid: "", version: 0 };
    setViewedGoal({ sid: "", goal: null });
  }

  async function deleteSession(id: string) {
    if (isClientDraftSessionId(id)) {
      const ok = await confirm({
        title: t("confirm.session.deleteDraft.title"),
        message: t("confirm.session.deleteDraft.message"),
        confirmLabel: t("common.delete"),
        variant: "danger",
      });
      if (!ok) {
        return;
      }
      const rows = removeClientDraftSession(id);
      setClientDraftSessions(rows);
      if (id === activeDraftId || id === sidebarActiveId) {
        setSessionsOpen(false);
        goHome();
      }
      return;
    }
    const ok = await confirm({
      title: t("confirm.session.deleteChat.title"),
      message: t("confirm.session.deleteChat.message"),
      confirmLabel: t("common.delete"),
      variant: "danger",
      // The row's trash does one thing, and this dialog asks about that one
      // thing: focus the answer so Enter finishes what the click started.
      initialFocus: "confirm",
    });
    if (!ok) {
      return;
    }
    clearQuestionPromptRecords(id);
    await fetch(`/coddy/sessions/${encodeURIComponent(id)}`, {
      method: "DELETE",
      headers,
    });
    setSessions((prev) => prev.filter((s) => s.id !== id));
    const viewingId = sidebarActiveId.trim();
    if (id === viewingId) {
      setSessionsOpen(false);
      goHome();
      return;
    }
    await loadSessionsList(true);
  }

  /**
   * Puts a conversation in the archive, or takes it back out.
   *
   * The row moves at once and the PATCH goes behind it, and the list is not
   * read again: a re-read from the first page dropped the rows scrolling had
   * loaded, and with them the place the next conversation to archive sat at.
   * A refused PATCH puts the row back where it stood, with the reason said on
   * the row. Until the server has the new flag, and until every listing issued
   * before that has come back, a listing is shown with the move applied, so a
   * read already in flight cannot put the row back either (archiveMoves.ts).
   */
  async function archiveSession(id: string, archived: boolean) {
    // One conversation, one request at a time. Two PATCHes for the same session
    // in flight together settle in whatever order the network gives them, so a
    // quick archive-then-unarchive could leave the archive flag opposite to the
    // last thing the operator pressed.
    if (archivingRef.current.has(id)) {
      return;
    }
    archivingRef.current.add(id);
    try {
      await runArchiveSession(id, archived);
    } finally {
      archivingRef.current.delete(id);
    }
  }

  async function runArchiveSession(id: string, archived: boolean) {
    const rowStays = rowVisibleUnder(sessionsArchiveFilter, archived);
    const place = rowPlace(sessions, id);
    const removesLoaded = !!place && !rowStays;
    archiveMovesRef.current.set(id, {
      archived,
      removesLoaded,
      settledAtSeq: null,
    });
    setSessionRowErrors((prev) => {
      if (!(id in prev)) return prev;
      const rest = { ...prev };
      delete rest[id];
      return rest;
    });
    setSessions((prev) =>
      rowStays
        ? prev.map((s) => (s.id === id ? { ...s, archived } : s))
        : prev.filter((s) => s.id !== id),
    );
    let ok = false;
    try {
      const res = await fetch(`/coddy/sessions/${encodeURIComponent(id)}`, {
        method: "PATCH",
        headers: { ...headers, "Content-Type": "application/json" },
        body: JSON.stringify({ archived }),
      });
      ok = res.ok;
    } catch {
      ok = false;
    }
    if (!ok) {
      archiveMovesRef.current.delete(id);
      pruneArchiveLists();
      const message = t(
        archived ? "sessions.archiveFailed" : "sessions.unarchiveFailed",
      );
      // Only back into the listing it came from: the operator may have
      // switched to the archive (or out of it) while the request was out.
      if (
        place &&
        rowVisibleUnder(sessionsArchiveFilterRef.current, !!place.row.archived)
      ) {
        setSessions((prev) => restoreRow(prev, place));
        setSessionRowErrors((prev) => ({ ...prev, [id]: message }));
      } else {
        setSessionsError(message);
      }
      return;
    }
    archiveMovesRef.current.set(id, {
      archived,
      removesLoaded,
      settledAtSeq: sessionsListSeqRef.current,
    });
    if (removesLoaded) {
      // The row was part of what History had loaded and has now left the
      // server's listing, so the next page starts one row earlier.
      archiveRemovalsRef.current.push(sessionsListSeqRef.current);
      const cursor = cursorAfterRemovals(sessionsCursorRef.current, 1);
      sessionsCursorRef.current = cursor;
      setSessionsCursor(cursor);
    }
    pruneArchiveLists();
    // The conversation on screen learns its new state with the row: the
    // composer swaps for the archived notice (or comes back) without waiting
    // for the next transcript load. The ref is re-read here, not captured
    // before the request - the viewer may have moved on while it was in
    // flight, and a flag that belongs to a session no longer on screen must
    // not mark the one that replaced it. The write is recorded either way, so
    // a transcript read issued before the PATCH settled cannot put the older
    // flag back.
    viewedArchiveWriteRef.current = { sid: id, archived, at: Date.now() };
    if (viewedSessionIdRef.current.trim() === id) {
      setViewedArchived(archived);
    }
  }

  /**
   * Keeps a conversation at the top of every listing, or lets it back into the
   * order. Like archiving, the row moves only once the server has agreed, and
   * one request per session is in flight at a time.
   */
  async function pinSession(id: string, pinned: boolean) {
    if (pinningRef.current.has(id)) {
      return;
    }
    pinningRef.current.add(id);
    try {
      const res = await fetch(`/coddy/sessions/${encodeURIComponent(id)}`, {
        method: "PATCH",
        headers: { ...headers, "Content-Type": "application/json" },
        body: JSON.stringify({ pinned }),
      });
      if (!res.ok) {
        setSessionsError(t("app.backendUnavailable", { status: res.status }));
        return;
      }
      // The row does not move here: a pin changes the order, and the order is
      // the server's answer, not something to guess at from one row.
      await loadSessionsList(true);
    } catch {
      setSessionsError(t("app.backendUnavailable", { status: 0 }));
    } finally {
      pinningRef.current.delete(id);
    }
  }

  /**
   * Writes the order the operator dragged the pinned conversations into. The
   * whole order travels, not the one that moved: a list rewritten from what was
   * on screen cannot interleave with a concurrent change into an order nobody
   * asked for. The rows are re-read afterwards, because the order is the
   * server's answer.
   */
  async function reorderPinnedSessions(ids: string[]) {
    if (ids.length === 0) {
      return;
    }
    try {
      const res = await fetch("/coddy/sessions/pins/reorder", {
        method: "POST",
        headers: { ...headers, "Content-Type": "application/json" },
        body: JSON.stringify({ ids }),
      });
      if (!res.ok) {
        setSessionsError(t("app.backendUnavailable", { status: res.status }));
      }
    } catch {
      setSessionsError(t("app.backendUnavailable", { status: 0 }));
    }
    await loadSessionsList(true);
  }

  /**
   * Takes the conversation on screen out of the archive, from the notice that
   * stands where its composer would be. The flag is cleared as soon as the
   * server agrees, so the composer comes back without waiting for the listing.
   */
  async function unarchiveViewedSession() {
    const sid = sessionId.trim();
    if (!sid || unarchiving || archivingRef.current.has(sid)) {
      return;
    }
    setUnarchiving(true);
    archivingRef.current.add(sid);
    try {
      const res = await fetch(`/coddy/sessions/${encodeURIComponent(sid)}`, {
        method: "PATCH",
        headers: { ...headers, "Content-Type": "application/json" },
        body: JSON.stringify({ archived: false }),
      });
      if (!res.ok) {
        setSessionsError(t("app.backendUnavailable", { status: res.status }));
        return;
      }
      // Same ordering as archiveSession: the write is recorded before the
      // flag moves, and the flag moves only while this session is still the
      // one on screen - the viewer may have navigated away while the PATCH
      // was in flight.
      viewedArchiveWriteRef.current = {
        sid,
        archived: false,
        at: Date.now(),
      };
      if (viewedSessionIdRef.current.trim() === sid) {
        setViewedArchived(false);
      }
      setSessions((prev) =>
        prev.map((s) => (s.id === sid ? { ...s, archived: false } : s)),
      );
      await loadSessionsList(true);
    } catch {
      setSessionsError(t("app.backendUnavailable", { status: 0 }));
    } finally {
      archivingRef.current.delete(sid);
      setUnarchiving(false);
    }
  }

  // The session table in Settings removes bundles behind the open panel. Drop
  // the rows from History right away, and when the conversation on screen was
  // one of them, reset the chat to a new one - without leaving Settings, which
  // is where the user still is, so the route is re-anchored on that tab.
  const onSessionsDeletedInSettings = useCallback((ids: string[]) => {
    if (ids.length === 0) {
      return;
    }
    const gone = new Set(ids);
    for (const id of ids) {
      clearQuestionPromptRecords(id);
    }
    setSessions((prev) => prev.filter((row) => !gone.has(row.id)));
    // A subagent transcript is not listed and is never a target of its own: it
    // goes with the parent whose tree was removed, so the viewed child has to
    // follow its parent home.
    const viewing = sidebarActiveIdRef.current.trim();
    const viewedParent = subagentParentIdRef.current.trim();
    if (gone.has(viewing) || (viewedParent !== "" && gone.has(viewedParent))) {
      goHome();
      setSettingsSectionHash("sessions_manager");
    }
  }, []);

  async function handleRewindSend(text: string, userMsgIdx: number) {
    const sid = sessionId.trim();
    if (!sid) return;

    const showRewindError = (msg: string) => {
      applyStreamItemsForSession(sid, (prev) => [
        ...prev,
        {
          id: newId("s"),
          type: "system_notice" as const,
          level: "error" as const,
          message: msg,
          createdAtUtc: new Date().toISOString(),
        },
      ]);
    };

    try {
      const res = await fetch(
        `/coddy/sessions/${encodeURIComponent(sid)}/rewind`,
        {
          method: "POST",
          headers: { ...headers, "Content-Type": "application/json" },
          body: JSON.stringify({ userMessageIndex: userMsgIdx }),
        },
      );
      if (!res.ok) {
        let errMsg = `Rewind failed (${res.status})`;
        try {
          const body = (await res.json()) as { error?: { message?: string } };
          if (body?.error?.message) errMsg = body.error.message;
        } catch {
          /* ignore */
        }
        // The draft and the editing state stay untouched, so the message is not
        // lost and the edit can be retried.
        showRewindError(errMsg);
        return;
      }
    } catch (err) {
      showRewindError(
        `Rewind error: ${err instanceof Error ? err.message : String(err)}`,
      );
      return;
    }
    // The history is cut on the server: drop everything this client kept of the
    // tail - the shadow transcript and persisted permission prompts - then
    // reload the kept prefix and send the edited message. The draft and the
    // editing state stay until the reload and the send have both gone through,
    // so a failure after the rewind does not lose the edited text.
    streamShadowBySidRef.current.delete(sid);
    clearPermissionPromptRecords(sid);
    try {
      await loadMessages(sid, { freshLoad: true });
    } catch (err) {
      // The edited text stays in the field as an ordinary draft; the edit
      // itself is over, so nothing of it may outlive this branch.
      setEditingUserMsgIdx(null);
      setEditingAssetNote("");
      setEditingFiles([]);
      setEditingSnippet("");
      editingPrevDraftRef.current = null;
      showRewindError(
        `Rewind applied but reloading failed: ${err instanceof Error ? err.message : String(err)}`,
      );
      return;
    }
    // The edit is on its way: the draft the pencil set aside comes back, so a
    // follow-up written before the edit is not lost with it.
    const prev = editingPrevDraftRef.current;
    editingPrevDraftRef.current = null;
    setDraft(prev?.draft ?? "");
    setComposerFiles(prev?.files ?? []);
    setEditingUserMsgIdx(null);
    setEditingAssetNote("");
    setEditingFiles([]);
    setEditingSnippet("");
    void streamResponses(text);
  }

  /**
   * Takes the last edit back: a turn the edit started is stopped first, then
   * the server restores the conversation as it was before the rewind and the
   * transcript is read again.
   */
  async function handleUndoEdit() {
    const sid = sessionId.trim();
    if (!sid || rewindUndoBusyRef.current) return;
    rewindUndoBusyRef.current = true;
    setRewindUndoBusy(true);
    const showUndoError = (error: string) => {
      applyStreamItemsForSession(sid, (prev) => [
        ...prev,
        {
          id: newId("s"),
          type: "system_notice" as const,
          level: "error" as const,
          message: t("app.undoEditFailed", { error }),
          createdAtUtc: new Date().toISOString(),
        },
      ]);
    };
    try {
      if (turnActivity.get(sid) ?? activeComposerSidRef.current.has(sid)) {
        // Releases this tab's stream of the turn; the server cancels the turn
        // and waits for it to end before restoring anyway.
        await stopActiveGeneration();
      }
      let res: Response;
      try {
        res = await fetch(
          `/coddy/sessions/${encodeURIComponent(sid)}/rewind/undo`,
          { method: "POST", headers: { ...headers } },
        );
      } catch (err) {
        showUndoError(err instanceof Error ? err.message : String(err));
        return;
      }
      if (!res.ok) {
        let errMsg = `HTTP ${res.status}`;
        try {
          const body = (await res.json()) as { error?: { message?: string } };
          if (body?.error?.message) errMsg = body.error.message;
        } catch {
          /* ignore */
        }
        showUndoError(errMsg);
        return;
      }
      // The history changed under this client the way a rewind changes it.
      setRewindUndo(null);
      setRewindUndoDismissed("");
      streamShadowBySidRef.current.delete(sid);
      clearPermissionPromptRecords(sid);
      try {
        await loadMessages(sid, { freshLoad: true });
      } catch (err) {
        showUndoError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      rewindUndoBusyRef.current = false;
      setRewindUndoBusy(false);
    }
  }

  useEffect(() => {
    setEditingUserMsgIdx(null);
    setEditingAssetNote("");
    setEditingFiles([]);
    setEditingSnippet("");
    // Attachments outlive a switch of conversation; the ones an open edit set
    // aside go back to the composer instead of being dropped with the edit.
    const setAside = editingPrevDraftRef.current;
    editingPrevDraftRef.current = null;
    if (setAside && setAside.files.length > 0) setComposerFiles(setAside.files);
    // Another conversation: the older pages read for the last one go, and the
    // reader starts at the newest message of this one.
    setOlderTranscript(null);
    setOlderTranscriptLoad("idle");
    readerAtTailRef.current = true;
    {
      const sid = sessionId.trim();
      const held = sid ? liveWindowBySidRef.current.get(sid) : undefined;
      setViewedLiveWindow(held ? { ...held, sessionId: sid } : null);
    }
    setSubagentTranscript(null);
    setViewedArchived(false);
    if (!sessionId) {
      setItems([]);
      setDraft("");
      setSessionLoading(false);
      void loadSessionsList(true);
      // The Back button to "#/" or a draft in History leaves the chat without
      // going through goHome.
      resetNewChatSettings();
      return;
    }
    setDraft("");
    setTokenUsage(null);
    setContextBreakdown(null);
    tokenBaselineRef.current = { input: 0, output: 0, total: 0 };
    const lifecycle = new AbortController();
    void (async () => {
      await loadSessionsList(true);
      if (lifecycle.signal.aborted) {
        return;
      }
      // A session spawned by another one is hidden from History, and History
      // holds one page, so an id it does not carry is still fetched: the
      // messages endpoint serves it (a child marked read-only), and only its
      // 404 says the session is not there.
      // A block, so `sess` stays out of the way of the rest of the effect.
      {
        const statsRes = await fetchJSON<{ stats?: SessionStats | null }>(
          `/coddy/sessions/${encodeURIComponent(sessionId)}/stats`,
          { headers },
        );
        if (lifecycle.signal.aborted) {
          return;
        }
        if (statsRes.ok && statsRes.data?.stats) {
          applySessionStatsPayload(
            statsRes.data.stats,
            viewedSessionIdRef.current.trim() === sessionId,
          );
        }
        const shadowSnap = streamShadowBySidRef.current.get(sessionId);
        if (
          activeComposerSidRef.current.has(sessionId) &&
          shadowSnap &&
          shadowSnap.length > 0
        ) {
          setItems([...shadowSnap]);
          // The rows come from the shadow of a turn this tab runs here, so no
          // transcript is read and the selectors would keep naming the session
          // visited before: its settings are read alone, and Send waits for
          // them. A snapshot of the session that comes another way (the
          // events stream) ends the wait too, and so does the read the turn
          // ends with.
          const held = () => settingsVersionRef.current.sid === sessionId;
          const outcome = await readOpening(
            () => readViewedSettings(sessionId),
            () => sessionLoadingRef.current && !held(),
            lifecycle.signal,
          );
          if (
            lifecycle.signal.aborted ||
            viewedSessionIdRef.current.trim() !== sessionId
          ) {
            return;
          }
          if (outcome === "missing") settleUnknownSession(sessionId);
          else if (outcome === "read" || held()) setSessionLoading(false);
        } else {
          // freshLoad when no shadow: prevents stale itemsRef from a previous session
          // bleeding into this session (e.g. React StrictMode double-invoke of effects).
          const noShadow = !shadowSnap || shadowSnap.length === 0;
          const read: { items: TranscriptItem[] | null } = { items: null };
          const outcome = await readOpening(
            async () => {
              failedReadStatusRef.current.delete(sessionId);
              try {
                read.items = await loadMessages(undefined, {
                  freshLoad: noShadow,
                });
              } catch {
                failedReadStatusRef.current.set(sessionId, 0);
              }
              if (read.items) return "read";
              const status = failedReadStatusRef.current.get(sessionId);
              if (status === undefined) return "superseded";
              return status === 404 ? "missing" : "failed";
            },
            () => sessionLoadingRef.current,
            lifecycle.signal,
          );
          if (lifecycle.signal.aborted) {
            return;
          }
          // An id the server does not serve: nothing to keep a skeleton up
          // for, so it lands on the empty state like any unknown id, and its
          // first message creates it. A read that fails keeps the skeleton
          // and runs again: the session may well be there.
          if (outcome === "missing") settleUnknownSession(sessionId);
          const loaded = read.items;
          // The rows of a turn this tab runs in it are shown; they say nothing
          // of the settings, which the read above has settled by now.
          if (
            activeComposerSidRef.current.has(sessionId) &&
            viewedSessionIdRef.current.trim() === sessionId
          ) {
            const sh = streamShadowBySidRef.current.get(sessionId);
            if (sh && sh.length > 0) {
              setItems([...sh]);
            }
          }
          if (loaded && turnActivity.get(sessionId) === true) {
            void attachViewedComposer(sessionId);
          }
        }
      }
    })();
    return () => {
      lifecycle.abort();
      failedReadStatusRef.current.delete(sessionId);
    };
    // Intentionally sessionId only for loadMessages coalescing; rejoin runs detached.
  }, [sessionId]);

  function upsertToolCall(
    update: Partial<Extract<TranscriptItem, { type: "tool_call" }>> & {
      toolCallId: string;
    },
  ) {
    const targetSid = sessionId.trim();
    if (!targetSid) return;
    applyStreamItemsForSession(targetSid, (prev) => {
      const idx = prev.findIndex(
        (x) => x.type === "tool_call" && x.toolCallId === update.toolCallId,
      );
      if (idx < 0) {
        const itBase: Extract<TranscriptItem, { type: "tool_call" }> = {
          id: newId("t"),
          type: "tool_call",
          toolCallId: update.toolCallId,
          status: (update.status as any) || "pending",
        };
        const it: Extract<TranscriptItem, { type: "tool_call" }> = {
          ...itBase,
        };
        if (update.title !== undefined) it.title = update.title;
        if (update.kind !== undefined) it.kind = update.kind;
        if (update.argsText !== undefined) it.argsText = update.argsText;
        if (update.resultText !== undefined) it.resultText = update.resultText;
        if (update.resultWasTruncated !== undefined)
          it.resultWasTruncated = update.resultWasTruncated;
        if (update.fullResultText !== undefined)
          it.fullResultText = update.fullResultText;
        if (update.todoPlan !== undefined) it.todoPlan = update.todoPlan;
        if (update.startedAtMs !== undefined)
          it.startedAtMs = update.startedAtMs;
        if (update.finishedAtMs !== undefined)
          it.finishedAtMs = update.finishedAtMs;
        if (update.durationMs !== undefined) it.durationMs = update.durationMs;
        return [...prev, it];
      }
      const next = [...prev];
      const cur = next[idx] as Extract<TranscriptItem, { type: "tool_call" }>;
      const nextStarted =
        update.startedAtMs !== undefined ? update.startedAtMs : cur.startedAtMs;
      const nextFinished =
        update.finishedAtMs !== undefined
          ? update.finishedAtMs
          : cur.finishedAtMs;
      const nextDuration =
        update.durationMs !== undefined
          ? update.durationMs
          : nextStarted && nextFinished
            ? Math.max(0, nextFinished - nextStarted)
            : cur.durationMs;
      const merged: Extract<TranscriptItem, { type: "tool_call" }> = {
        ...cur,
        status: (update.status as any) || cur.status,
      };
      if (nextStarted !== undefined) merged.startedAtMs = nextStarted;
      if (nextFinished !== undefined) merged.finishedAtMs = nextFinished;
      if (nextDuration !== undefined) merged.durationMs = nextDuration;
      if (update.title !== undefined) merged.title = update.title;
      if (update.kind !== undefined) merged.kind = update.kind;
      if (update.argsText !== undefined) merged.argsText = update.argsText;
      if (update.resultText !== undefined)
        merged.resultText = update.resultText;
      if (update.resultWasTruncated !== undefined)
        merged.resultWasTruncated = update.resultWasTruncated;
      if (update.fullResultText !== undefined)
        merged.fullResultText = update.fullResultText;
      if (update.todoPlan !== undefined) merged.todoPlan = update.todoPlan;
      next[idx] = merged;
      return next;
    });
  }

  async function rejoinComposerLiveStream(
    sid: string,
    baseline: TranscriptItem[],
  ): Promise<void> {
    const key = sid.trim();
    if (!key) return;
    if (
      postAbortBySidRef.current.has(key) ||
      relayAbortBySidRef.current.has(key)
    )
      return;
    const fetchCtl = new AbortController();
    relayAbortBySidRef.current.set(key, fetchCtl);
    streamGenerationBySidRef.current.set(
      key,
      (streamGenerationBySidRef.current.get(key) ?? 0) + 1,
    );
    const ownsRelay = () => relayAbortBySidRef.current.get(key) === fetchCtl;
    const queueEpoch = queueOrderRef.current.capture(key).epoch;

    addActiveComposer(key);
    const assistantId = newId("a");
    streamingAssistantBySidRef.current.set(key, assistantId);
    streamShadowBySidRef.current.set(key, [...baseline]);
    if (viewedSessionIdRef.current.trim() === key) {
      setItems([...baseline]);
    }

    const applyStreamItems = (
      fn: (prev: TranscriptItem[]) => TranscriptItem[],
    ) => {
      if (ownsRelay() && !fetchCtl.signal.aborted)
        applyStreamItemsForSession(key, fn);
    };

    const branchTokenUsage = (u: TokenUsage | null) => {
      if (u === null || !ownsRelay() || fetchCtl.signal.aborted) return;
      if (viewedSessionIdRef.current.trim() === key) {
        setTokenUsage(u);
        debouncedRefreshSessionStats(key);
      }
    };
    const branchContextUsage = (u: ContextUsageUpdate) => {
      if (!ownsRelay() || fetchCtl.signal.aborted) return;
      if (viewedSessionIdRef.current.trim() === key) {
        applyContextUsage(key, u);
      }
    };

    try {
      // Resume after the last frame this tab consumed, so a dropped connection costs
      // a gap rather than a replay of the whole turn.
      const resumeFrom = relayLastEventIdBySidRef.current.get(key) ?? "";
      const headers: Record<string, string> = { [HDR]: key };
      if (resumeFrom) {
        headers["Last-Event-ID"] = resumeFrom;
      }
      // Without a frame cursor - a reloaded tab - the baseline is the transcript just
      // loaded, and the relay is asked only for what that snapshot lacks: replaying
      // the whole turn on top of it put every finished step on screen a second time.
      const sinceRev = resumeFrom
        ? undefined
        : snapshotRevByTranscript.current.get(baseline);
      const res = await fetch(
        `/coddy/sessions/${encodeURIComponent(key)}/composer-stream` +
          (sinceRev !== undefined ? `?since_rev=${sinceRev}` : ""),
        { headers, signal: fetchCtl.signal },
      );
      if (!ownsRelay() || fetchCtl.signal.aborted || !res.ok || !res.body) {
        return;
      }
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      const carry = { buf: "" };
      const {
        streamErrorMessage,
        streamErrorCode,
        lastEventId,
        desynced,
        flushToolQueue,
        finishThinking,
        ensureAssistant,
        lastAssistantId,
      } = await consumeComposerSseReader({
        reader,
        dec,
        carry,
        assistantId,
        applyStreamItems,
        setTokenUsage: branchTokenUsage,
        setContextUsage: branchContextUsage,
        tokenBaselineRef,
        reasoningDurationMsByContentRef,
        newId,
        applyMemoryRunToItems,
        onQuestion: handleComposerSseQuestion,
        onPermission: handleComposerSsePermission,
        onProviderUsage: providerUsageState.applyPushed,
        onMessageQueue: (q) => {
          if (ownsRelay() && !fetchCtl.signal.aborted) {
            applyQueue(key, q.messages, q.version, queueEpoch);
          }
        },
        onSessionSettings: (e) => applySessionSettings(e.settings),
        onSessionGoal: (u) => applySessionGoal(u),
        onTurnProgress: (progress) => {
          if (ownsRelay() && !fetchCtl.signal.aborted) {
            applyTurnProgress(key, progress, "stream");
          }
        },
      });
      if (!ownsRelay() || fetchCtl.signal.aborted) return;

      const syncAssistantFromServer = async () => {
        try {
          const res2 = await fetchJSON<{ messages: Array<any> }>(
            `/coddy/sessions/${encodeURIComponent(key)}/messages${liveWindowQuery(key)}`,
            { headers: { [HDR]: key } },
          );
          if (
            !ownsRelay() ||
            fetchCtl.signal.aborted ||
            !res2.ok ||
            !res2.data?.messages
          )
            return false;
          let last = "";
          let lastCreated: string | undefined;
          for (const m of res2.data.messages) {
            if ((m.role || "").trim() !== "assistant") continue;
            const c = (m.content || "").trim();
            if (c) {
              last = c;
              lastCreated = readMessageCreatedAtUTC(
                m as Record<string, unknown>,
              );
            }
          }
          if (!last) return false;
          ensureAssistant();
          applyStreamItems((prev) =>
            prev.map((it) =>
              it.type === "assistant_message" && it.id === lastAssistantId
                ? {
                    ...it,
                    content: last,
                    ...(lastCreated ? { createdAtUtc: lastCreated } : {}),
                  }
                : it,
            ),
          );
          return true;
        } catch {
          return false;
        }
      };

      if (lastEventId) {
        relayLastEventIdBySidRef.current.set(key, lastEventId);
      }
      // The relay had already dropped frames this tab never saw, so the transcript on
      // screen has a hole in it. The persisted messages are the source of truth.
      if (desynced) {
        await loadMessages(key, {
          skipSetItems: viewedSessionIdRef.current.trim() !== key,
          preserveOnError: true,
        });
      }

      // The relay had nothing to attach to. That is a state, not a failure: the turn
      // finished while we were reconnecting, or it ran somewhere this process cannot
      // see. Drop the placeholder bubble and let the finally block reconcile from the
      // persisted transcript instead of accusing the user of an error.
      if (isNoLiveTurnRelayError(streamErrorCode, streamErrorMessage)) {
        flushToolQueue();
        finishThinking();
        applyStreamItems((prev) =>
          prev.filter(
            (it) =>
              !(
                it.type === "assistant_message" &&
                it.id === lastAssistantId &&
                !it.content.trim()
              ),
          ),
        );
        return;
      }

      if (streamErrorMessage) {
        flushToolQueue();
        finishThinking();
        const errText = streamErrorMessage;
        applyStreamItems((prev) => {
          const withoutEmptyAssistant = retireRelayedPermissionPrompts(
            prev,
          ).filter(
            (it) =>
              !(
                it.type === "assistant_message" &&
                it.id === lastAssistantId &&
                !it.content.trim()
              ),
          );
          return [
            ...withoutEmptyAssistant,
            {
              id: newId("s"),
              type: "system_notice",
              level: "error" as const,
              message: errText,
              createdAtUtc: new Date().toISOString(),
            },
          ];
        });
        void loadSessionsList(true);
        return;
      }

      flushToolQueue();
      finishThinking();
      // A prompt this turn relayed on behalf of a subagent ended with the
      // stream that carried it: the relay withdrew it and, for a background
      // child, raised it again as the card at the end of the chat.
      applyStreamItems(retireRelayedPermissionPrompts);
      ensureAssistant({
        streaming: false,
        createdAtUtc: new Date().toISOString(),
      });

      void loadSessionsList(true);
      let ok = transcriptHasFilledAssistant(
        streamShadowBySidRef.current.get(key) ?? [],
        lastAssistantId,
      );
      if (!ok) ok = await syncAssistantFromServer();
      for (let i = 0; i < 10 && !ok; i++) {
        await new Promise((r) => setTimeout(r, 500));
        if (!ownsRelay() || fetchCtl.signal.aborted) return;
        ok = await syncAssistantFromServer();
      }
      if (!ownsRelay() || fetchCtl.signal.aborted) return;
      const viewing = viewedSessionIdRef.current.trim();
      await loadMessages(key, {
        skipSetItems: viewing !== key,
      });
    } catch {
      // AbortError when relay superseded or fetch aborted
    } finally {
      if (!ownsRelay()) return;
      relayAbortBySidRef.current.delete(key);
      streamingAssistantBySidRef.current.delete(key);
      removeActiveComposer(key);
      void turnActivity.refresh(key, false);
      // A relay this client cut short (its own POST or a newer relay took
      // over) did not end the turn; only a stream that ran to its end
      // spent quota.
      if (!fetchCtl.signal.aborted) noteUsageTurnEnded(key);
      // The session is no longer pinned; bound the cache now rather than
      // only after the reconciliation below succeeds.
      evictStaleSessionCaches(viewedSessionIdRef.current);
      void loadSessionsList(true);
      const viewing = viewedSessionIdRef.current.trim();
      void loadMessages(key, { skipSetItems: viewing !== key });
      void refreshSessionStats(key);
      markViewedSessionActivityRead(key);
    }
  }

  async function streamResponses(
    text: string,
    opts?: {
      modeOverride?: string;
      runPlanSlug?: string;
      files?: File[];
      /**
       * Put the text and the files back in the composer when the server never
       * took this send: a refusal by status, a request that failed on the way,
       * an attachment the browser could not read. Set for what the operator
       * wrote - the composer and the message queue's fallback (the queue
       * closes a moment before the turn releases its admission, so a
       * follow-up written in that gap is told "no turn is running" and then
       * refused as busy) - and never for a retry, whose text was not typed.
       */
      restoreOnRefusal?: boolean;
    },
  ) {
    const abortCtl = new AbortController();
    let postSessionKey = "";
    let completedNormally = false;
    let assistantStreamId = "";
    const isNewChatFirstSend = !sessionId.trim();
    let releaseSessionId: ((id: string) => void) | undefined;
    const sessionIdWhenKnown = isNewChatFirstSend
      ? new Promise<string>((resolve) => {
          releaseSessionId = resolve;
        })
      : null;

    let sidEffective = "";
    // Set once the POST has an answer: a failure before it means the server
    // never admitted this send, and the prompt goes back to the composer.
    let responded = false;
    // The server never took this send: the bubble drawn for it (once drawn)
    // leaves the transcript, and what the operator wrote returns to the
    // composer - text and files - ahead of anything typed since. Defined
    // before anything can throw, so a failure on the way to the POST (the
    // new chat's workspace, the file read) loses nothing either.
    let restoreKey = "";
    let userItemId = "";
    const giveBack = () => {
      const key = restoreKey || sessionId.trim();
      if (userItemId)
        applyStreamItemsForSession(key, (prev) =>
          prev.filter((it) => it.id !== userItemId),
        );
      if (
        !opts?.restoreOnRefusal ||
        (key && viewedSessionIdRef.current.trim() !== key)
      )
        return;
      setDraft((current) =>
        !current.trim() || current.trim() === text.trim()
          ? text
          : `${text}\n\n${current}`,
      );
      const files = opts.files ?? [];
      if (files.length > 0)
        setComposerFiles((prev) => [
          ...files,
          ...prev.filter((f) => !files.includes(f)),
        ]);
    };
    // The opened session's settings are still being read, so the selectors
    // name another session's, and what a prompt takes from them (the mode as
    // the top-level `model`, metadata.model, metadata.reasoning) would be
    // applied to this session and kept. The composer holds Send back; this is
    // the one door every other sender passes.
    if (sessionLoading) {
      giveBack();
      return;
    }
    const ownsPost = () =>
      postAbortBySidRef.current.get(postSessionKey) === abortCtl;

    try {
      let sid = sessionId;
      if (!sid) {
        sid = randomSessionId();
        // The start screen's pick now belongs to the session this send
        // creates, and the chip keeps naming what the start screen showed
        // until that session's own snapshot arrives. With no pick and no word
        // from the server on its configured mode, what the chip shows is sent
        // as the pick: the first turn runs under the mode the operator saw,
        // never under one the page could not name.
        // A read of the mode in flight (a reload, a reconnect) is waited for,
        // briefly: the chip may still show the mode it is replacing, and a
        // first turn pinned to that one would outlive the change.
        const reading = serverPermissionReadingRef.current;
        if (reading) {
          await Promise.race([
            reading,
            new Promise((resolve) => setTimeout(resolve, 1500)),
          ]);
        }
        setPendingPermissionMode(
          pendingPermissionModeRef.current.mode ||
            (serverPermissionModeRef.current === null
              ? startPermissionMode
              : ""),
          sid,
        );
        setPermissionMode(startPermissionMode);
        migrateWorkspaceAtRecents(WORKSPACE_AT_RECENTS_NO_SESSION_KEY, sid);
        await applyPendingWorkspace(sid);
        if (activeDraftId.trim()) {
          setClientDraftSessions(
            removeClientDraftSession(activeDraftId.trim()),
          );
          setActiveDraftId("");
        }
        openSessionFromRoute(sid);
      }
      sidEffective = sid;
      let latestPreviewSid = sid;
      postSessionKey = sid.trim();
      restoreKey = postSessionKey;
      postAbortBySidRef.current.set(postSessionKey, abortCtl);
      pendingPostBySidRef.current.set(postSessionKey, abortCtl);
      streamGenerationBySidRef.current.set(
        postSessionKey,
        (streamGenerationBySidRef.current.get(postSessionKey) ?? 0) + 1,
      );
      stoppedTurnBySidRef.current.delete(postSessionKey);
      turnActivity.observe(postSessionKey, true);
      addActiveComposer(postSessionKey);
      relayAbortBySidRef.current.get(postSessionKey)?.abort();
      relayAbortBySidRef.current.delete(postSessionKey);

      let streamKey = postSessionKey;
      let queueEpoch = queueOrderRef.current.capture(streamKey).epoch;
      const applyStreamItems = (
        fn: (prev: TranscriptItem[]) => TranscriptItem[],
      ) => {
        if (ownsPost() && !abortCtl.signal.aborted)
          applyStreamItemsForSession(streamKey, fn);
      };

      const branchTokenUsage = (u: TokenUsage | null) => {
        if (u === null || !ownsPost() || abortCtl.signal.aborted) return;
        if (viewedSessionIdRef.current.trim() === streamKey) {
          setTokenUsage(u);
          debouncedRefreshSessionStats(streamKey);
        }
      };
      const branchContextUsage = (u: ContextUsageUpdate) => {
        if (!ownsPost() || abortCtl.signal.aborted) return;
        if (viewedSessionIdRef.current.trim() === streamKey) {
          applyContextUsage(streamKey, u);
        }
      };

      if (isNewChatFirstSend && sessionIdWhenKnown) {
        startSuggestSessionTitle({
          userText: text,
          sessionIdPromise: sessionIdWhenKnown,
          getPreviewSessionId: () => latestPreviewSid,
          onShortReady: (cid, ttl) => {
            setDescribePreview({ sessionId: cid, title: ttl });
            setSessions((prev) => {
              const i = prev.findIndex((s) => s.id === cid);
              if (i >= 0) {
                return prev.map((s) =>
                  s.id === cid ? { ...s, title: ttl } : s,
                );
              }
              return [{ id: cid, title: ttl }, ...prev];
            });
          },
          onApplied: (id, appliedTitle) => {
            setSessions((prev) =>
              prev.map((s) =>
                s.id === id ? { ...s, title: appliedTitle } : s,
              ),
            );
            setDescribePreview((p) => (p?.sessionId === id ? null : p));
          },
        });
      }

      const hdrs = sid ? { [HDR]: sid } : {};
      const userItem: TranscriptItem = {
        id: newId("u"),
        type: "user_message",
        content: text,
        createdAtUtc: new Date().toISOString(),
        ...(opts?.files && opts.files.length > 0
          ? { files: optimisticUserFiles(opts.files) }
          : {}),
      };
      const assistantId = newId("a");
      assistantStreamId = assistantId;
      // The server never took this send: the bubble drawn for it leaves the
      // transcript, and what the operator wrote returns to the composer - text
      // and files - ahead of anything typed since.
      userItemId = userItem.id;
      let settingsOnly = false;
      streamingAssistantBySidRef.current.set(streamKey, assistantId);
      const viewingNow = viewedSessionIdRef.current.trim();
      const baseItems = pickStreamMutationBase({
        mutationSessionId: streamKey,
        viewingSid: viewingNow,
        shadow: streamShadowBySidRef.current.get(streamKey),
        hasActiveComposer: activeComposerSidRef.current.has(streamKey),
        itemsWhenViewingMatches: itemsRef.current,
        assumeActiveForBase: true,
      });
      const nextShadow = [...baseItems, userItem];
      streamShadowBySidRef.current.set(streamKey, nextShadow);
      if (viewingNow === streamKey) {
        setItems(nextShadow);
      }
      if (viewingNow === streamKey) {
        setTokenUsage(null);
      }

      const reqBody: Record<string, unknown> = {
        model: opts?.modeOverride || mode || "agent",
        input: text,
        stream: true,
      };
      // The "@" mentions of the text are resolved by the server, when the
      // message is sent (internal/session/mentions.go): one grammar for
      // every surface, so nothing is derived here but the recent picks.
      const profileModel = (PROFILE_MODES as readonly string[]).includes(mode);
      if (profileModel) {
        const wk = sid.trim() || WORKSPACE_AT_RECENTS_NO_SESSION_KEY;
        for (const a of extractAtFileAttachments(text)) {
          recordWorkspaceAtRecent(wk, { path_rel: a.path, kind: "file" });
        }
      }
      // A typed "/model <id>" is a session-scoped pick made on this surface -
      // the same memory the Model menu writes (turn-scoped forms return null).
      const typedModel = sessionScopedModelCommand(text);
      if (typedModel && llmModelIds.includes(typedModel)) {
        writeLlmModelCookie(typedModel);
      }
      if (opts?.files && opts.files.length > 0) {
        let unreadable: { name: string; reason: string } | null = null;
        const inlineFiles = await Promise.all(
          opts.files.map(
            (f) =>
              new Promise<{ name: string; data_url: string } | null>(
                (resolve) => {
                  const reader = new FileReader();
                  reader.onload = () =>
                    resolve({
                      name: f.name,
                      data_url: reader.result as string,
                    });
                  // A picked file the browser can no longer read (moved,
                  // changed on disk, a cloud photo not on the device) is
                  // named, and nothing is sent without it.
                  reader.onerror = () => {
                    unreadable ??= {
                      name: f.name,
                      reason: errorDetail(reader.error) || "NotReadableError",
                    };
                    resolve(null);
                  };
                  reader.readAsDataURL(normalizeClipboardImageFile(f));
                },
              ),
          ),
        );
        if (unreadable !== null) {
          const { name, reason } = unreadable as {
            name: string;
            reason: string;
          };
          applyStreamItems((prev) => [
            ...prev,
            {
              id: newId("s"),
              type: "system_notice",
              level: "error" as const,
              message: t("composer.attachReadFailed", { name, reason }),
              createdAtUtc: new Date().toISOString(),
            },
          ]);
          giveBack();
          completedNormally = true;
          return;
        }
        reqBody.inline_files = inlineFiles.filter(
          (f): f is { name: string; data_url: string } => f !== null,
        );
      }
      const yamlSel = llmModel.trim();
      const reasoningSel = llmReasoning.trim();
      const runSlug = (opts?.runPlanSlug || "").trim();
      // The version of the settings snapshot these selectors mirror: when a
      // newer one was published meanwhile (the model switched from another
      // surface), the server keeps its own values instead of these.
      const heldVersion =
        settingsVersionRef.current.sid === sid.trim()
          ? settingsVersionRef.current.version
          : 0;
      // The level the chip names for a session with none of its own is shown,
      // not chosen: sent, it would pin the session to it (#362).
      const sendReasoning =
        reasoningSel !== "" && !(sid.trim() && reasoningImpliedRef.current);
      // The web UI names itself, so the turn's system prompt says what it
      // draws: Mermaid and SVG fences as pictures, LaTeX as formulas
      // (external/httpserver/webui_prompt.go).
      const meta: Record<string, string> = { surface: "webui" };
      // The turn's @coddy: mentions and the agent's documentation tools
      // follow the interface's language (webui_prompt.go, langFromHTTP).
      meta.lang = locale;
      if (yamlSel) meta.model = yamlSel;
      if (sendReasoning) meta.reasoning = reasoningSel;
      if (runSlug) meta.runPlanSlug = runSlug;
      if (heldVersion > 0) meta.settingsVersion = String(heldVersion);
      reqBody.metadata = meta;
      // A permission mode picked before the chat had a session goes first,
      // as the command that asks for it; the server takes it off the text.
      // It is left out only when it is the mode the server named as the one
      // a new session starts under: against a mode not known (no answer yet,
      // a reload being read again, a server that does not say) it is sent,
      // because the configured mode may be another one - bypass, say, under
      // an explicit "Ask first".
      const pick = pendingPermissionModeRef.current;
      if (
        pick.mode &&
        pick.sid === sid.trim() &&
        pick.mode !== serverPermissionModeRef.current
      ) {
        reqBody.input = `/permissions ${pick.mode}\n${text}`;
      }
      if (!ownsPost() || abortCtl.signal.aborted) return;
      const res = await fetch("/v1/responses", {
        method: "POST",
        headers: { ...hdrs, "Content-Type": "application/json" },
        body: JSON.stringify(reqBody),
        signal: abortCtl.signal,
      });
      responded = true;
      // The server took the session's first message, and with it the pick.
      if (res.ok && pendingPermissionModeRef.current.sid === sid.trim()) {
        setPendingPermissionMode("");
      }
      if (!ownsPost() || abortCtl.signal.aborted) return;
      pendingPostBySidRef.current.delete(postSessionKey);

      if (res.status === 409) {
        let msg = t("app.chatBusy");
        try {
          const body = (await res.json()) as {
            error?: { message?: string };
          };
          const m = body?.error?.message;
          if (typeof m === "string" && m.trim()) {
            msg = m.trim();
          }
        } catch {
          // ignore
        }
        applyStreamItems((prev) => [
          ...prev,
          {
            id: newId("s"),
            type: "system_notice",
            level: "error" as const,
            message: msg,
            createdAtUtc: new Date().toISOString(),
          },
        ]);
        giveBack();
        completedNormally = true;
        return;
      }

      const sidHdr = res.headers.get(HDR);
      if (sidHdr && sidHdr !== sid) {
        const oldKey = postSessionKey;
        migrateWorkspaceAtRecents(sid, sidHdr);
        sidEffective = sidHdr;
        postSessionKey = sidHdr.trim();
        streamKey = postSessionKey;
        restoreKey = postSessionKey;
        queueEpoch = queueOrderRef.current.capture(streamKey).epoch;
        postAbortBySidRef.current.delete(oldKey);
        postAbortBySidRef.current.set(postSessionKey, abortCtl);
        streamGenerationBySidRef.current.set(
          postSessionKey,
          (streamGenerationBySidRef.current.get(postSessionKey) ?? 0) + 1,
        );
        turnActivity.observe(oldKey, false);
        turnActivity.observe(postSessionKey, true);
        relayAbortBySidRef.current.get(oldKey)?.abort();
        relayAbortBySidRef.current.delete(oldKey);
        streamShadowBySidRef.current.rename(oldKey, postSessionKey);
        const relayCursor = relayLastEventIdBySidRef.current.get(oldKey);
        relayLastEventIdBySidRef.current.delete(oldKey);
        if (relayCursor !== undefined) {
          relayLastEventIdBySidRef.current.set(postSessionKey, relayCursor);
        }
        streamingAssistantBySidRef.current.delete(oldKey);
        streamingAssistantBySidRef.current.set(postSessionKey, assistantId);
        if (viewedSessionIdRef.current.trim() === oldKey)
          openSessionFromRoute(sidHdr);
        setDescribePreview((p) =>
          p?.sessionId === sid ? { ...p, sessionId: sidHdr } : p,
        );
        setSessions((prev) =>
          prev.map((s) => (s.id === sid ? { ...s, id: sidHdr } : s)),
        );
        removeActiveComposer(oldKey);
        addActiveComposer(postSessionKey);
      }
      latestPreviewSid = sidEffective;
      releaseSessionId?.(sidEffective);

      if (!res.ok || !res.body) {
        const msg = !res.body
          ? t("app.emptyResponseBody")
          : remoteHttpErrorMessage(
              res.status,
              getEnv(),
              await res
                .json()
                .then((b: { error?: { message?: unknown } }) =>
                  typeof b?.error?.message === "string" ? b.error.message : "",
                )
                .catch(() => ""),
            );
        applyStreamItems((prev) => [
          ...prev,
          {
            id: newId("s"),
            type: "system_notice",
            level: "error" as const,
            message: msg,
            createdAtUtc: new Date().toISOString(),
          },
        ]);
        // A refusal by status: nothing of this send is in the conversation.
        if (!res.ok) giveBack();
        completedNormally = true;
        return;
      }

      // Admission is now settled. Reconcile events ignored while the POST was
      // pending, including a real end whose response reader never finishes.
      void turnActivity.refresh(streamKey);
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      const carry = { buf: "" };

      const {
        streamErrorMessage,
        flushToolQueue,
        finishThinking,
        ensureAssistant,
        lastAssistantId,
      } = await consumeComposerSseReader({
        reader,
        dec,
        carry,
        assistantId,
        applyStreamItems,
        setTokenUsage: branchTokenUsage,
        setContextUsage: branchContextUsage,
        tokenBaselineRef,
        reasoningDurationMsByContentRef,
        newId,
        applyMemoryRunToItems,
        onQuestion: handleComposerSseQuestion,
        onPermission: handleComposerSsePermission,
        onProviderUsage: providerUsageState.applyPushed,
        onMessageQueue: (q) => {
          if (ownsPost() && !abortCtl.signal.aborted) {
            applyQueue(streamKey, q.messages, q.version, queueEpoch);
          }
        },
        onSessionSettings: (e) => applySessionSettings(e.settings),
        onSessionGoal: (u) => applySessionGoal(u),
        onTurnProgress: (progress) => {
          if (ownsPost() && !abortCtl.signal.aborted) {
            applyTurnProgress(streamKey, progress, "stream");
          }
        },
        onSettingsOnly: () => {
          settingsOnly = true;
        },
      });
      if (!ownsPost() || abortCtl.signal.aborted) return;
      assistantStreamId = lastAssistantId;
      // Only settings commands: the exchange drawn for it is not part of the
      // conversation, and the change is on the selectors. The reload below
      // shows the transcript as the server holds it.
      if (settingsOnly) {
        applyStreamItems((prev) =>
          prev.filter(
            (it) =>
              it.id !== userItem.id &&
              !(it.type === "assistant_message" && it.id === lastAssistantId),
          ),
        );
        void loadSessionsList(true);
        await loadMessages(sidEffective, {
          skipSetItems: viewedSessionIdRef.current.trim() !== postSessionKey,
          preserveOnError: true,
        });
        completedNormally = true;
        return;
      }

      const syncAssistantFromServer = async () => {
        try {
          const res2 = await fetchJSON<{ messages: Array<any> }>(
            `/coddy/sessions/${encodeURIComponent(sidEffective)}/messages${liveWindowQuery(sidEffective)}`,
            { headers: { [HDR]: sidEffective } },
          );
          if (
            !ownsPost() ||
            abortCtl.signal.aborted ||
            !res2.ok ||
            !res2.data?.messages
          )
            return false;
          let last = "";
          let lastCreated: string | undefined;
          for (const m of res2.data.messages) {
            if ((m.role || "").trim() !== "assistant") continue;
            const c = (m.content || "").trim();
            if (c) {
              last = c;
              lastCreated = readMessageCreatedAtUTC(
                m as Record<string, unknown>,
              );
            }
          }
          if (!last) return false;
          ensureAssistant();
          applyStreamItems((prev) =>
            prev.map((it) =>
              it.type === "assistant_message" && it.id === lastAssistantId
                ? {
                    ...it,
                    content: last,
                    ...(lastCreated ? { createdAtUtc: lastCreated } : {}),
                  }
                : it,
            ),
          );
          return true;
        } catch {
          return false;
        }
      };

      if (streamErrorMessage) {
        flushToolQueue();
        finishThinking();
        const errText = streamErrorMessage;
        applyStreamItems((prev) => {
          const withoutEmptyAssistant = prev.filter(
            (it) =>
              !(
                it.type === "assistant_message" &&
                it.id === lastAssistantId &&
                !it.content.trim()
              ),
          );
          return [
            ...withoutEmptyAssistant,
            {
              id: newId("s"),
              type: "system_notice",
              level: "error" as const,
              message: errText,
              createdAtUtc: new Date().toISOString(),
            },
          ];
        });
        void loadSessionsList(true);
        completedNormally = true;
        return;
      }

      flushToolQueue();

      finishThinking();
      ensureAssistant({
        streaming: false,
        createdAtUtc: new Date().toISOString(),
      });

      void loadSessionsList(true);
      const kProbe = postSessionKey.trim();
      let mergedForSyncProbe: TranscriptItem[] = [];
      for (let attempt = 0; attempt < 40; attempt++) {
        if (!ownsPost() || abortCtl.signal.aborted) return;
        const sh = streamShadowBySidRef.current.get(kProbe);
        if (sh && sh.length > 0) {
          mergedForSyncProbe = sh;
        } else if (viewedSessionIdRef.current.trim() === kProbe) {
          mergedForSyncProbe = itemsRef.current;
        } else {
          mergedForSyncProbe = sh ?? [];
        }
        if (transcriptHasFilledAssistant(mergedForSyncProbe, assistantId)) {
          break;
        }
        await new Promise((r) => setTimeout(r, 16));
      }
      const localStreamingAssistantReady = transcriptHasFilledAssistant(
        mergedForSyncProbe,
        assistantId,
      );
      let ok = localStreamingAssistantReady;
      if (!ok) {
        ok = await syncAssistantFromServer();
        for (let i = 0; i < 10 && !ok; i++) {
          await new Promise((r) => setTimeout(r, 500));
          if (!ownsPost() || abortCtl.signal.aborted) return;
          ok = await syncAssistantFromServer();
        }
      }
      if (!ownsPost() || abortCtl.signal.aborted) return;
      const viewingEnd = viewedSessionIdRef.current.trim();
      await loadMessages(sidEffective, {
        skipSetItems: viewingEnd !== postSessionKey,
        preserveOnError: true,
      });
      void refreshSessionStats(sidEffective);
      markViewedSessionActivityRead(sidEffective);
      completedNormally = true;
    } catch (err: unknown) {
      // Stay silent only for the user's own Stop (AbortError). Surface real transport failures —
      // an unreachable remote, DNS/TLS error, refused connection, or a CORS-blocked response all
      // reject fetch() with no Response, so without this they vanish (issue #60).
      // applyStreamItems is scoped to the try block; use the component-level session helper here
      // (same one the finally uses), keyed by the session this turn targeted.
      if (
        !isAbortError(err) &&
        ownsPost() &&
        !abortCtl.signal.aborted &&
        postSessionKey.trim() !== ""
      ) {
        applyStreamItemsForSession(postSessionKey, (prev) => [
          ...prev,
          {
            id: newId("s"),
            type: "system_notice",
            level: "error" as const,
            message: remoteSendErrorMessage(err, getEnv()),
            createdAtUtc: new Date().toISOString(),
          },
        ]);
      }
      // The request failed before any answer (a dropped upload, a refused
      // connection): the prompt returns to the composer, and the transcript is
      // not read again, because that read would wipe the notice saying why and
      // could not show a message the server does not have. Should the server
      // have taken the turn after all, the activity refresh below attaches to
      // it and its end reloads the transcript.
      if (
        !responded &&
        !isAbortError(err) &&
        (ownsPost() || !postSessionKey.trim())
      ) {
        giveBack();
        completedNormally = true;
      }
    } finally {
      releaseSessionId?.(sidEffective);
      if (pendingPostBySidRef.current.get(postSessionKey) === abortCtl) {
        pendingPostBySidRef.current.delete(postSessionKey);
      }
      if (!ownsPost()) return;
      postAbortBySidRef.current.delete(postSessionKey);
      noteUsageTurnEnded(postSessionKey);
      if (!completedNormally && assistantStreamId) {
        const aid = assistantStreamId;
        const now = Date.now();
        const patchIncomplete = (prev: TranscriptItem[]) =>
          prev.map((it) => {
            if (it.type === "thinking" && it.status === "in_progress") {
              const dur = Math.max(0, now - (it.startedAtMs || now));
              const nextIt = {
                ...it,
                status: "completed" as const,
                durationMs: dur,
              };
              const dk = reasoningDurationCacheKey(nextIt.content);
              if (dk.length > 0)
                reasoningDurationMsByContentRef.current.set(dk, dur);
              return nextIt;
            }
            if (it.type === "assistant_message" && it.id === aid) {
              return { ...it, streaming: false };
            }
            return it;
          });
        if (postSessionKey.trim() !== "") {
          applyStreamItemsForSession(postSessionKey, patchIncomplete);
        }
        const viewingFin = viewedSessionIdRef.current.trim();
        void loadMessages(sidEffective, {
          skipSetItems: viewingFin !== postSessionKey.trim(),
          preserveOnError: true,
        });
        void loadSessionsList(true);
        markViewedSessionActivityRead(postSessionKey.trim());
      }
      removeActiveComposer(postSessionKey);
      streamingAssistantBySidRef.current.delete(postSessionKey);
      void turnActivity.refresh(postSessionKey, false);
      // A background stream that just finished on a no-longer-recent session
      // should release its transcript without waiting for the next navigation.
      evictStaleSessionCaches(viewedSessionIdRef.current);
    }
  }

  async function stopActiveGeneration(): Promise<void> {
    const sid = sessionId.trim();
    if (!sid || stopPendingBySidRef.current.has(sid)) return;
    const request = { superseded: false };
    stopPendingBySidRef.current.set(sid, request);
    const post = postAbortBySidRef.current.get(sid);
    const relay = relayAbortBySidRef.current.get(sid);
    const generation = turnActivity.generation(sid);
    const streamGeneration = streamGenerationBySidRef.current.get(sid);
    const cancelCtl = new AbortController();
    const timer = window.setTimeout(() => cancelCtl.abort(), 5000);
    const errorId = `stop_error_${sid}`;
    applyStreamItemsForSession(sid, (prev) =>
      prev.filter((it) => it.id !== errorId),
    );
    let fenced = false;
    try {
      const cancelled = fetch(
        `/coddy/sessions/${encodeURIComponent(sid)}/cancel`,
        {
          method: "POST",
          headers: { [HDR]: sid },
          signal: cancelCtl.signal,
        },
      );
      // Release this tab's stream of the turn before the answer, not after it.
      // Over HTTP/1.1 a browser keeps six connections to a host for all of its
      // tabs, and a few open tabs of Coddy hold every one of them with event and
      // turn streams: the cancel request then waits for a connection that only
      // this abort frees. The turn does not depend on the stream, and a failed
      // Stop rejoins it through the relay.
      post?.abort();
      relay?.abort();
      const res = await cancelled;
      if (!res.ok) throw new Error(`cancel failed (${res.status})`);
      // The acknowledgement neither proves idle nor gives an old Stop ownership
      // of a newer turn in this session.
      fenced =
        !request.superseded &&
        turnActivity.generation(sid) === generation &&
        streamGenerationBySidRef.current.get(sid) === streamGeneration;
      if (fenced) {
        stoppedTurnBySidRef.current.set(sid, generation);
        void turnActivity.refresh(sid, false);
      }
    } catch {
      applyStreamItemsForSession(sid, (prev) => [
        ...prev.filter((it) => it.id !== errorId),
        {
          id: errorId,
          type: "system_notice",
          level: "error",
          message: t("app.stopFailed"),
          createdAtUtc: new Date().toISOString(),
        },
      ]);
    } finally {
      window.clearTimeout(timer);
      if (stopPendingBySidRef.current.get(sid) === request) {
        stopPendingBySidRef.current.delete(sid);
        // The stream was released for a Stop that did not take this turn: the
        // request failed, or a successor started meanwhile. Rejoin it without
        // waiting for the next reconciliation tick, one task later, so the
        // released stream has let go of the session before the attach looks.
        if (!fenced) window.setTimeout(() => void turnActivity.refresh(sid), 0);
      }
    }
  }

  const maxContextTokens = useMemo(() => {
    const live = sessionContextWindows[sessionId.trim()];
    if (live?.model === llmModel && live.epoch === configEpoch) {
      return live.size;
    }
    const row = modelInfos.find((m) => m.id === llmModel);
    return row?.maxContextTokens || 128000;
  }, [modelInfos, llmModel, sessionId, sessionContextWindows, configEpoch]);

  const llmModelMultimodal = useMemo(() => {
    const row = modelInfos.find((m) => m.id === llmModel);
    return row?.multimodal ?? false;
  }, [modelInfos, llmModel]);

  // Every configured model's levels: /goal --reasoning completes the levels
  // of the model its --model names.
  const llmReasoningLevelsByModel = useMemo(() => {
    const out: Record<string, readonly string[]> = {};
    for (const m of modelInfos) {
      if (m.ownedBy !== "coddy") {
        out[m.id] = m.reasoningLevels ?? [];
      }
    }
    return out;
  }, [modelInfos]);

  const llmReasoningLevels = useMemo(() => {
    const row = modelInfos.find((m) => m.id === llmModel);
    const levels = row?.reasoningLevels ?? [];
    // Off is not a level GET /v1/models lists; the viewed session's snapshot
    // names it where the provider can turn thinking off, and the menu offers
    // it there, so a session can be switched back to it as well as shown so.
    const known = sessionReasoningChoices;
    if (
      levels.length > 0 &&
      known.sid !== "" &&
      known.sid === sessionId.trim() &&
      known.model === llmModel &&
      known.choices.includes("off") &&
      !levels.includes("off")
    ) {
      return [...levels, "off"];
    }
    return levels;
  }, [modelInfos, llmModel, sessionReasoningChoices, sessionId]);

  // Keep the selected reasoning level valid for the current model: keep the user's
  // pick when the new model still offers it, else fall back (the cookie for a new
  // chat, then the model default).
  useEffect(() => {
    const row = modelInfos.find((m) => m.id === llmModel);
    // Nothing is known about a model whose row has not arrived - the list is still
    // in flight, or the id was just set. Clearing the level there loses the one the
    // session asked for, and the run that follows cannot bring it back: it only
    // sees the emptied value. Leave the selection alone until the row says what
    // the model actually offers.
    if (!row) {
      return;
    }
    const levels = row.reasoningLevels ?? [];
    const viewed = viewedSessionIdRef.current.trim();
    const known = sessionReasoningChoicesRef.current;
    setLlmReasoning((prev) =>
      pickReasoningLevel({
        levels,
        // An open session's level is its own; the cookie seeds a new chat only.
        cookie: viewed ? null : readReasoningCookie(),
        sessionLevel: prev,
        sessionChoices:
          viewed && known.sid === viewed && known.model === llmModel
            ? known.choices
            : [],
        modelDefault: row.reasoningDefault ?? null,
      }),
    );
  }, [llmModel, modelInfos]);

  /**
   * applySessionSettings mirrors a settings snapshot of the viewed session in
   * the composer: the model, the reasoning level, the mode, the permission
   * mode and what is changed for the next turns. A snapshot of another
   * session, or one older than the snapshot already shown, is dropped: the
   * same change reaches a tab down the turn stream and the events stream.
   * Cookies are left alone: they are the default of a new chat, not the
   * record of an existing session.
   */
  const applySessionSettings = useStableHandler((snap: SessionSettings) => {
    const viewed = viewedSessionIdRef.current.trim();
    const held =
      settingsVersionRef.current.sid === snap.sessionId
        ? settingsVersionRef.current.version
        : 0;
    if (!isNewerSettings(held, viewed, snap)) {
      return;
    }
    settingsVersionRef.current = { sid: snap.sessionId, version: snap.version };
    setPermissionMode(snap.permissionMode);
    setConfiguredPermissionMode(snap.configuredPermissionMode);
    setSettingsOverrides(snap.overrides);
    if (snap.mode) {
      setMode(snap.mode);
    }
    if (snap.model && llmModelIds.includes(snap.model)) {
      setLlmModel(snap.model);
    }
    setSessionReasoningChoices({
      sid: snap.sessionId,
      model: snap.model,
      choices: snap.reasoningChoices,
    });
    reasoningImpliedRef.current = !snap.reasoning.trim();
    // An empty level is the model's own default when the model names none:
    // shown as it comes, it blanks the chip while the session still runs at
    // that default, so it goes through the chooser the open path uses. A
    // model whose row has not arrived is left to the clamp effect.
    const row = modelInfos.find((m) => m.id === snap.model);
    setLlmReasoning(
      row
        ? pickReasoningLevel({
            levels: row.reasoningLevels ?? [],
            cookie: null,
            sessionLevel: snap.reasoning,
            sessionChoices: snap.reasoningChoices,
            modelDefault: row.reasoningDefault ?? null,
          })
        : snap.reasoning,
    );
  });

  /**
   * applySessionGoal mirrors a goal snapshot of the viewed session in the
   * composer's chip and popover. A snapshot of another session, or one older
   * than the one shown, is dropped: the same change reaches a tab down the
   * turn stream and the events stream, and an action's answer can land after
   * the event of a later change.
   */
  const applySessionGoal = useStableHandler((update: SessionGoalUpdate) => {
    const viewed = viewedSessionIdRef.current.trim();
    const held =
      goalVersionRef.current.sid === update.sessionId
        ? goalVersionRef.current.version
        : 0;
    if (!isNewerGoal(held, viewed, update)) {
      return;
    }
    goalVersionRef.current = {
      sid: update.sessionId,
      version: update.version,
    };
    setViewedGoal({ sid: update.sessionId, goal: update.goal });
  });

  /**
   * sendGoalRequest pauses or clears the viewed session's goal over REST and
   * mirrors the answer, which has the shape of the events stream's frame.
   * Resolves false when the server refused (no goal, an unknown session).
   */
  const sendGoalRequest = useCallback(
    async (method: "PATCH" | "DELETE", body?: unknown): Promise<boolean> => {
      const sid = sessionId.trim();
      if (!sid) return false;
      try {
        const res = await fetch(
          `/coddy/sessions/${encodeURIComponent(sid)}/goal`,
          {
            method,
            headers: {
              ...headers,
              ...(body !== undefined
                ? { "Content-Type": "application/json" }
                : {}),
            },
            ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
          },
        );
        if (!res.ok) return false;
        const update = parseSessionGoalUpdate(await res.json(), sid);
        if (update) applySessionGoal(update);
        return true;
      } catch {
        return false;
      }
    },
    [sessionId, headers, applySessionGoal],
  );

  /** patchSessionSettings sends a settings change and mirrors the answer. */
  const patchSessionSettings = useCallback(
    (sid: string, body: Record<string, unknown>) =>
      fetch(`/coddy/sessions/${encodeURIComponent(sid)}`, {
        method: "PATCH",
        headers: { ...headers, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      })
        .then((r) => (r.ok ? r.json() : null))
        .then((b: { settings?: unknown } | null) => {
          const snap = parseSessionSettings(b?.settings);
          if (snap) {
            applySessionSettings(snap);
          }
        })
        .catch(() => {}),
    [headers, applySessionSettings],
  );

  const onPermissionModeChange = useCallback(
    (next: string) => {
      const pm = next.trim();
      if (!pm) {
        return;
      }
      setPermissionMode(pm);
      const sid = sessionId.trim();
      if (!sid) {
        // No session yet: the choice rides in with the first message. It is
        // kept even when it is the configured mode, which may move before
        // the message is sent; the send decides against what the server says
        // then. A first send already under way (its workspace being applied)
        // has bound the pick to the session it creates: the new choice keeps
        // that binding, or neither choice would ride in.
        setPendingPermissionMode(pm, pendingPermissionModeRef.current.sid);
        return;
      }
      if (pendingPermissionModeRef.current.sid === sid) {
        // A session whose first message the server has not taken yet (the
        // send failed on the way): the choice rides in with the next try, and
        // the PATCH below applies it at once should the server have taken it
        // after all.
        setPendingPermissionMode(pm, sid);
      }
      void patchSessionSettings(sid, { permissionMode: pm });
    },
    [sessionId, patchSessionSettings, setPendingPermissionMode],
  );

  const onLlmReasoningChange = useCallback(
    (level: string) => {
      const lv = level.trim();
      if (!lv) {
        return;
      }
      setLlmReasoning(lv);
      writeReasoningCookie(lv);
      reasoningImpliedRef.current = false;
      const sid = sessionId.trim();
      if (!sid) {
        return;
      }
      void patchSessionSettings(sid, { selectedReasoning: lv });
    },
    [sessionId, patchSessionSettings],
  );

  const onLlmModelChange = useCallback(
    (id: string) => {
      const mid = id.trim();
      if (!mid) {
        return;
      }
      setLlmModel(mid);
      writeLlmModelCookie(mid);
      const sid = sessionId.trim();
      if (!sid || !llmModelIds.includes(mid)) {
        return;
      }
      void patchSessionSettings(sid, { selectedModelId: mid });
    },
    [sessionId, llmModelIds, patchSessionSettings],
  );

  const contextPct = useMemo(
    () => contextUsagePercent(maxContextTokens, contextBreakdown),
    [maxContextTokens, contextBreakdown],
  );

  const onSchedulerRunJob = useCallback(
    async (jobId: string) => {
      const r = await schedulerRunJob(jobId);
      if (!r.ok) {
        setSchedulerListError(r.message);
        return;
      }
      void refreshSchedulerJobs({ silent: true });
    },
    [refreshSchedulerJobs],
  );

  const onSchedulerCancelJob = useCallback(
    async (jobId: string) => {
      const r = await schedulerCancelJob(jobId);
      if (!r.ok) {
        setSchedulerListError(r.message);
        return;
      }
      void refreshSchedulerJobs({ silent: true });
    },
    [refreshSchedulerJobs],
  );

  const openSchedulerRuns = useCallback((jobId: string) => {
    const jid = jobId.trim();
    if (!jid) {
      return;
    }
    setSchedulerEditor({ mode: "runs", jobId: jid, taskId: null });
    setSchedulerJobRunsHash(jid);
  }, []);

  const closeSchedulerRuns = useCallback(() => {
    if (!schedulerRunsJobId) {
      return;
    }
    setSchedulerEditor({ mode: "edit", jobId: schedulerRunsJobId });
    setSchedulerJobHash(schedulerRunsJobId);
    setSchedulerRunsFocus(null);
  }, [schedulerRunsJobId]);

  const stopSchedulerRun = useCallback(
    async (taskId: string) => {
      const sid = schedulerRunsSessionId;
      if (!sid) {
        return;
      }
      await stopBackgroundTask(sid, taskId);
      await refreshSchedulerRuns({ silent: true });
      void refreshSchedulerJobs({ silent: true });
    },
    [schedulerRunsSessionId, refreshSchedulerRuns, refreshSchedulerJobs],
  );

  const clearSchedulerRuns = useCallback(async () => {
    if (!schedulerRunsJobId) {
      return;
    }
    const res = await schedulerClearJobRuns(schedulerRunsJobId);
    if (!res.ok) {
      setSchedulerRunsError(res.message);
      return;
    }
    setSchedulerEditor({
      mode: "runs",
      jobId: schedulerRunsJobId,
      taskId: null,
    });
    setSchedulerJobRunsHash(schedulerRunsJobId);
    void refreshSchedulerRuns({ silent: true });
    void refreshSchedulerJobs({ silent: true });
  }, [schedulerRunsJobId, refreshSchedulerRuns, refreshSchedulerJobs]);

  const openSchedulerFromNav = useCallback(() => {
    if (schedulerHttpLinked !== true) {
      return;
    }
    setSessionsOpen(false);
    if (isStackedShell()) setTasksOpen(false);
    setSchedulerOpen(true);
    setSchedulerEditor(null);
    setSchedulerListHash();
  }, [schedulerHttpLinked]);

  const openTasksFromNav = useCallback(() => {
    const sid = sessionId.trim();
    if (!sid) {
      return;
    }
    setSessionsOpen(false);
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setSettingsRoute(false);
    setTasksOpen(true);
    if (isStackedShell()) {
      setSessionTasksHash(sid);
    }
  }, [sessionId]);

  /** Opens the edits window, from git's count in the bar over the composer;
   *  it takes the Files window's place, so one Escape never closes two
   *  layers. */
  const openEditsWindow = useCallback(() => {
    const sid = sessionId.trim();
    if (!sid) {
      return;
    }
    setSessionsOpen(false);
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setSettingsRoute(false);
    setDocsRoute(null);
    setSwarmRoute(false);
    setFilesOpen(false);
    setChangesViewerOpen(true);
    setSessionChangesHash(sid);
  }, [sessionId]);

  /** Puts the edits window away; the address goes back to the chat, or to the
   *  tasks when the dock under it shows them on the stacked shell. */
  const closeEditsWindow = useCallback(() => {
    setChangesViewerOpen(false);
    const sid = sessionId.trim();
    if (!sid) return;
    if (tasksOpen && isStackedShell()) setSessionTasksHash(sid);
    else setSessionHashInLocation(sid);
  }, [sessionId, tasksOpen]);

  const closeTasksDrawer = useCallback(() => {
    setTasksOpen(false);
    // A pointer at a task that never showed up does not wait for the next opening.
    setTasksFocus(null);
    setSessionHashInLocation(sessionId.trim());
    if (!isStackedShell()) {
      return;
    }
    if (sessionsOpen) {
      setHistoryHash();
      return;
    }
    const sid = sessionId.trim();
    if (sid) {
      setSessionHashInLocation(sid);
    } else if (window.location.hash) {
      history.replaceState(
        null,
        "",
        `${window.location.pathname}${window.location.search}`,
      );
    }
  }, [sessionId, sessionsOpen]);

  /** Opens the Files window over the chat. Without a path it opens on the
   *  files it had open; with one, on that file and line. */
  const openFilesWindow = useCallback(
    (path?: string, line?: number) => {
      const sid = sessionId.trim();
      if (!sid) return;
      setSessionsOpen(false);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setSettingsRoute(false);
      setDocsRoute(null);
      setSwarmRoute(false);
      // One window over the chat at a time: the files take the edits' place.
      setChangesViewerOpen(false);
      setFilePath(path || "");
      setFileLine(line || 1);
      if (path) setFileOpenSeq((n) => n + 1);
      setFilesOpen(true);
      setSessionFilesHash(sid, path || "", line || 1);
    },
    [sessionId],
  );

  /** Puts the Files window away. The address goes back to the chat, or to the
   *  tasks when the dock under it shows them on the stacked shell. */
  const closeFilesWindow = useCallback(() => {
    setFilesOpen(false);
    const sid = sessionId.trim();
    if (!sid) return;
    if (tasksOpen && isStackedShell()) setSessionTasksHash(sid);
    else setSessionHashInLocation(sid);
  }, [sessionId, tasksOpen]);

  // Ctrl+Shift+F (Cmd+Shift+F) opens and closes the Files window of the chat
  // on screen, wherever the focus is, the composer included.
  useEffect(() => {
    if (!sessionId.trim()) return undefined;
    const onKey = (ev: KeyboardEvent) => {
      if (!isFilesHotkey(ev)) return;
      ev.preventDefault();
      if (filesOpen) closeFilesWindow();
      else openFilesWindow();
    };
    document.addEventListener("keydown", onKey, true);
    return () => document.removeEventListener("keydown", onKey, true);
  }, [sessionId, filesOpen, openFilesWindow, closeFilesWindow]);

  useEffect(
    () =>
      onOpenWorkspaceFile((request) => {
        if (!sessionId.trim()) return;
        const match = /^(.*?)(?:(?::|#L)(\d+)(?:-L?\d*)?)?$/i.exec(
          request.path,
        );
        const raw = (match?.[1] || request.path).replace(
          /^(["'])(.*)\1$/,
          "$2",
        );
        const path = relativeFilePath(raw, "", workspaceCtx?.path || "");
        if (path === null) return;
        openFilesWindow(path, request.line || Number(match?.[2]) || 1);
      }),
    [sessionId, workspaceCtx?.path, openFilesWindow],
  );

  /** Opens a session in this tab: the child transcript behind an agent task,
   *  or the parent chat from a read-only notice. Same path as a History pick,
   *  so the panel closes and the hash becomes `#/s/<id>`. */
  const openSessionInPlace = (targetId: string) => {
    const id = targetId.trim();
    if (id) {
      pickSession(id);
    }
  };

  useEffect(() => {
    const env = getEnv();
    // Inside a node the relay's own routes are no longer under the base URL, so
    // asking again would say "not a swarm" and take away the way back. What we
    // came through is remembered instead.
    if (env.mode === "remote" && env.swarmRelay) {
      setIsSwarmEnv(true);
      setAtSwarmRoot(false);
      return undefined;
    }
    const ac = new AbortController();
    void probeSwarm(ac.signal).then((info) => {
      if (env.mode === "remote") {
        rememberRelayHome(env.baseUrl, !!info);
      }
      setIsSwarmEnv(!!info);
      setAtSwarmRoot(!!info);
    });
    return () => ac.abort();
  }, []);

  // What serves this page: an agent, or a relay serving its own page. The
  // documentation is read from it (docs/api.ts), so on a relay's own page the
  // reader is left out of the rail; and only an agent is a machine of its own
  // the swarm map draws above the relay as where the connection starts
  // (issue #401). Null until it answers.
  // It never changes while the page is open, so it is asked once and kept
  // (pageMemory.ts): an app started over by a switch in place knows it at once.
  const [localServer, setLocalServer] = useState<"agent" | "relay" | null>(
    () => knownPageServer()?.kind ?? null,
  );
  // The host name of the machine the page runs on (GET /coddy/info of the
  // page's own server), which is how the swarm map names it.
  const [localHost, setLocalHost] = useState(
    () => knownPageServer()?.host ?? "",
  );
  useEffect(() => {
    if (knownPageServer()) {
      return undefined;
    }
    let alive = true;
    const readJSON = (path: string) =>
      localFetch(path)
        .then((res) => (res.ok ? res.json() : null))
        .catch(() => null) as Promise<Record<string, unknown> | null>;
    void (async () => {
      const info = await readJSON("/swarm/info");
      const kind = info?.swarm === true ? "relay" : "agent";
      // Only an agent is a machine of its own on the map; the map says
      // "Local" when it does not say its name.
      const about = kind === "agent" ? await readJSON("/coddy/info") : null;
      const host =
        typeof about?.hostname === "string" ? about.hostname.trim() : "";
      rememberPageServer({ kind, host });
      if (alive) {
        setLocalServer(kind);
        setLocalHost(host);
      }
    })();
    return () => {
      alive = false;
    };
  }, []);
  const localDocs = localServer !== "relay";
  // The machine the page runs on, for the swarm map: drawn only while the app
  // is on a remote environment the page reached from it, and named by its host
  // name (Local when the system reports none).
  const swarmClient = useMemo(() => {
    if (localServer !== "agent" || getEnv().mode !== "remote") {
      return undefined;
    }
    return { name: localHost || t("env.local") };
  }, [localServer, localHost, t]);

  // Entering a node points the whole app at that node's mount, so every screen
  // that already existed works against it with a relay in the middle.
  // `hash` is a session to open; `landing` is where a switch to another node
  // lands without one - the map, kept open over the node, for a node clicked
  // on it.
  const openSwarmNode = useCallback(
    (nodePath: string[], hash?: string, landing?: string) => {
      const env = getEnv();
      // The node the app is already on: nothing to switch, and no reload. A
      // session of it a search row picked opens in place; without one the map
      // closes onto the node. (The map itself leaves this node alone.)
      if (
        env.mode === "remote" &&
        env.swarmRelay &&
        env.swarmNode === nodePath.join("/")
      ) {
        setSwarmRoute(false);
        if (hash) {
          window.location.hash = hash;
          return;
        }
        const sid = viewedSessionIdRef.current.trim();
        if (sid) {
          setSessionHashInLocation(sid);
        } else {
          window.location.hash = "#/";
        }
        return;
      }
      // Served by the relay from its own root, the environment is plain
      // same-origin: the relay is then this page's origin. Without that fallback
      // the one entry point this screen exists for silently did nothing.
      const relay =
        env.mode === "remote" ? swarmRootRelay(env) : window.location.origin;
      if (!relay) {
        return;
      }
      connectSwarmNode(
        relay,
        nodePath,
        env.mode === "remote" ? env.token : "",
        hash ?? landing,
      );
    },
    [],
  );

  // A relay on the map opens as a relay: the one the map is drawn for is
  // connected to itself, a relay chained under it opens through its mount -
  // staying on the same map, which is always drawn by the outermost relay.
  const openSwarmRelay = useCallback(
    (relayPath: string[], name: string) => {
      const env = getEnv();
      // Already on the relay this path names - the map's root for a home or
      // a local env, a chained relay for its own mount env: nothing to
      // connect to, and a reload would only blink the page. A node env never
      // matches: leaving it for a relay is a switch.
      const onRelayPath =
        env.mode === "remote"
          ? env.swarmRelay
            ? null
            : swarmMountPath(env.baseUrl)
          : swarmMountPath(window.location.origin);
      if (onRelayPath && onRelayPath.join("/") === relayPath.join("/")) {
        return;
      }
      const relay =
        env.mode === "remote" ? swarmRootRelay(env) : window.location.origin;
      if (!relay) {
        return;
      }
      // The chip names the relay the way the environment menu does: by its
      // entry in httpserver.remotes, else by the name it goes by on the map.
      const label =
        relayPath.length === 0
          ? configuredRemoteFor(relay, configuredRemotes)?.name || name
          : name;
      connectSwarmRelay(
        relay,
        relayPath,
        env.mode === "remote" ? env.token : "",
        label,
      );
    },
    [configuredRemotes],
  );

  /**
   * Where the app is in this swarm, as a route: inside a node, that node. The
   * map marks it and draws the path to it, so the screen can say where we are
   * rather than only what exists.
   */
  const swarmCurrentNode = useMemo(() => {
    const env = getEnv();
    if (env.mode !== "remote") {
      return [] as string[];
    }
    const path = (env.swarmNode || "").split("/").filter(Boolean);
    // A chained relay's env carries no swarmNode, but its mount URL spells
    // the same path - the map marks where the app stands on the whole swarm.
    return path.length ? path : swarmMountPath(env.baseUrl);
  }, []);

  /**
   * The relay the map reads when the app is inside one of its nodes. The
   * environment is the node's mount, which the relay's own routes are not
   * under, so the map asks the relay directly - with the same client token the
   * mount takes - and opening it leaves the node, its History and its
   * Scheduler where they are (issue #401). Undefined on the relay itself,
   * where the environment shim already reaches it.
   */
  const swarmRelayTarget = useMemo(() => {
    const env = getEnv();
    if (env.mode !== "remote") {
      return undefined;
    }
    // The map is drawn by the outermost relay of the chain the environment
    // hangs off: entering a node or a chained relay keeps the whole swarm in
    // view and only moves the mark of where the app stands (env.swarmRelay
    // for a node, the mount in its own baseUrl for a chained relay).
    const relay =
      env.swarmRelay || swarmMountPath(env.baseUrl).length
        ? swarmRootRelay(env)
        : "";
    return relay ? { baseUrl: relay, token: env.token } : undefined;
  }, []);

  const openSwarmFromNav = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    if (isStackedShell()) setTasksOpen(false);
    setSessionsOpen(false);
    setSettingsRoute(false);
    window.location.hash = appNavHrefSwarm();
  }, []);

  /** Opens the reader over whatever is on screen, remembering it for the close. */
  const openDocsAt = useCallback(
    (slug: string | null, anchor: string | null) => {
      if (parseAppHash().branch !== "docs") {
        docsReturnHashRef.current = window.location.hash;
      }
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      if (isStackedShell()) setTasksOpen(false);
      setSessionsOpen(false);
      setSettingsRoute(false);
      window.location.hash = appNavHrefDocs(slug, anchor);
    },
    [],
  );

  const openDocsFromNav = useCallback(() => {
    openDocsAt(lastDocsSlugRef.current, null);
  }, [openDocsAt]);

  // A search /docs <words> brings into the reader; cleared when the reader closes.
  const [docsSearchSeed, setDocsSearchSeed] = useState<{
    query: string;
    nonce: number;
  } | null>(null);

  /**
   * `/docs [page or words]` in the composer, as in the console: the command
   * alone reopens the book, a page's address or title opens that page (and
   * section), anything else opens the reader on that search.
   */
  const openDocsCommand = useCallback(
    (arg: string) => {
      setDraft("");
      if (!arg) {
        setDocsSearchSeed(null);
        openDocsFromNav();
        return;
      }
      void fetchDocsPage(arg, locale).then((res) => {
        if (res.ok && docsCommandOpensPage(arg, res.data.title)) {
          setDocsSearchSeed(null);
          openDocsAt(res.data.slug, res.data.anchor || null);
          return;
        }
        setDocsSearchSeed({ query: arg, nonce: Date.now() });
        openDocsFromNav();
      });
    },
    [openDocsAt, openDocsFromNav, locale],
  );

  /** Following a link of the reader adds a history entry, so Back returns to
   *  the page before; settling on the first page of the book does not. */
  const openDocsPage = useCallback(
    (slug: string, anchor?: string | null, opts?: { replace?: boolean }) => {
      if (opts?.replace) {
        setDocsHash(slug, anchor);
        return;
      }
      window.location.hash = appNavHrefDocs(slug, anchor);
    },
    [],
  );

  const onCloseDocs = useCallback(() => {
    setDocsRoute(null);
    setDocsSearchSeed(null);
    const back = docsReturnHashRef.current;
    docsReturnHashRef.current = "";
    if (back && !back.startsWith("#/docs")) {
      window.location.hash = back;
      return;
    }
    const sid = sessionId.trim();
    if (sid) {
      setSessionHashInLocation(sid);
    } else {
      clearSessionRoute();
    }
  }, [sessionId, clearSessionRoute]);

  // F1 opens the documentation, as it does in the console, and closes it again.
  const docsOpenRef = useRef(false);
  docsOpenRef.current = docsRoute !== null;
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== "F1" || e.altKey || e.ctrlKey || e.metaKey) {
        return;
      }
      e.preventDefault();
      if (docsOpenRef.current) {
        onCloseDocs();
      } else {
        openDocsFromNav();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCloseDocs, openDocsFromNav]);

  const openSettingsFromNav = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    if (isStackedShell()) setTasksOpen(false);
    setSessionsOpen(false);
    setSettingsHash();
  }, []);

  // Back to the chat on screen, or to the start screen when there is none:
  // what closing Settings does.
  const closeToChat = useCallback(() => {
    const sid = sessionId.trim();
    if (sid) {
      setSessionHashInLocation(sid);
    } else {
      clearSessionRoute();
    }
  }, [sessionId, clearSessionRoute]);

  // The swarm screen over a chat has no close control of its own: Escape
  // takes it down as the backdrop does, back to the chat.
  const onCloseSwarm = useCallback(() => {
    setSwarmRoute(false);
    closeToChat();
  }, [closeToChat]);

  const onOpenHistoryFromNav = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    if (isStackedShell()) setTasksOpen(false);
    setSettingsRoute(false);
    setSwarmRoute(false);
    setSessionsOpen(true);
    setHistoryHash();
  }, []);

  /** Background tasks indexed by the tool call that started them, so a
   *  transcript row can keep ticking after the tool itself returned. */
  const backgroundTasksByToolCallId = useMemo(() => {
    const byToolCall = new Map<string, BackgroundTask>();
    for (const t of backgroundTasks) {
      const tc = (t.tool_call_id || "").trim();
      if (tc) {
        byToolCall.set(tc, t);
      }
    }
    return byToolCall;
  }, [backgroundTasks]);

  // The panel belongs to a chat, so it only exists when one is open.
  const tasksPanelOpen = tasksOpen && !!sessionId.trim();
  // The dock beside the chat holds the background tasks; `tasksOpen` is its
  // open state, which every Escape path and stacked-shell rule speaks.
  const dockOpen = tasksPanelOpen;

  // A window over the chat - the files or the edits - is one more layer the
  // shell's backdrop covers the chat under.
  const chatWindowOpen = (filesOpen || changesViewerOpen) && !!sessionId.trim();
  const shellBackdropOpen =
    chatWindowOpen ||
    sessionsOpen ||
    (schedulerOpen && schedulerHttpLinked === true) ||
    settingsRoute ||
    swarmRoute ||
    docsRoute !== null;

  // A window over the dock (FilesView, EditsView) takes Escape itself, and the
  // backdrop covers it, so the dock waits for the next one.
  useRightDockEscape(dockOpen && !shellBackdropOpen, closeTasksDrawer);

  const filteredSchedulerJobs = useMemo(() => {
    const q = schedulerFilterQ.trim().toLowerCase();
    if (!q) {
      return schedulerJobs;
    }
    return schedulerJobs.filter((j) => {
      const id = (j.job_id || "").toLowerCase();
      const desc = (j.description || "").toLowerCase();
      return id.includes(q) || desc.includes(q);
    });
  }, [schedulerJobs, schedulerFilterQ]);

  const activeEnv = useSyncExternalStore(
    subscribeEnv,
    snapshotEnv,
    snapshotEnv,
  );

  /**
   * The environments the History filter offers. Two kinds share the list,
   * because to an operator they are one question - where is this conversation:
   * the origin rows narrow the listing of whichever server is being read, and a
   * remote row points the whole app at another server, the way the rail's
   * environment menu does.
   */
  const sessionEnvironments = useMemo<SessionsEnvironmentOption[]>(() => {
    const onRemote = activeEnv.mode === "remote";
    const activeConfiguredRemote = onRemote
      ? configuredRemoteFor(activeEnv.baseUrl, configuredRemotes)
      : undefined;
    // The origin rows filter whichever server is active. Configured
    // remote rows below switch the server the whole app reads instead.
    const narrowTo = (origin: SessionOriginFilter) => () => {
      setSessionsOrigin(origin);
      writeSessionPref(SESSION_PREF_COOKIES.origin, origin);
    };
    const rows: SessionsEnvironmentOption[] = [
      {
        kind: "origin",
        key: "all",
        label: t("sessions.filter.env.all"),
        active: sessionsOrigin === "",
        onPick: narrowTo(""),
      },
      {
        kind: "origin",
        key: "local",
        label: t("sessions.filter.env.local"),
        active: sessionsOrigin === "local",
        onPick: narrowTo("local"),
      },
      {
        kind: "origin",
        key: "gateway",
        label: t("sessions.filter.env.gateway"),
        active: sessionsOrigin === "gateway",
        onPick: narrowTo("gateway"),
      },
      {
        kind: "origin",
        key: "print",
        label: t("sessions.filter.env.print"),
        active: sessionsOrigin === "print",
        onPick: narrowTo("print"),
      },
    ];
    for (const remote of configuredRemotes) {
      rows.push({
        kind: "switch",
        key: remote.url,
        label: remote.name.trim() || remote.url,
        active: remote === activeConfiguredRemote,
        // A configured remote is an environment switch, not an origin filter.
        onPick: () => connectConfiguredRemote(remote),
      });
    }
    return rows;
  }, [activeEnv, configuredRemotes, sessionsOrigin, t]);

  const sessionPanelShared = {
    sessionId: sidebarActiveId,
    permissionPendingSessionIds: permissionPendingSids,
    questionPendingSessionIds: questionPendingSids,
    sessions: sessionsForSidebar,
    ...(sessionsError ? { error: sessionsError } : {}),
    rowErrors: sessionRowErrors,
    open: sessionsOpen,
    onClose: () => {
      setSessionsOpen(false);
      setSessionRowErrors({});
      const p = parseAppHash();
      if (p.branch === "history") {
        const sid = sessionId.trim();
        const did = activeDraftId.trim();
        if (sid) {
          setSessionHashInLocation(sid);
        } else if (did) {
          setDraftHashInLocation(did);
        } else if (window.location.hash) {
          history.replaceState(
            null,
            "",
            `${window.location.pathname}${window.location.search}`,
          );
        }
        return;
      }
      stripHistorySidebarFromHash();
    },
    onPick: pickSession,
    onTitleSave: saveSessionTitle as (id: string, title: string) => void,
    onTagsSave: (id: string, tags: string[]) => saveSessionTags(id, tags),
    onDelete: deleteSession as (id: string) => void | Promise<void>,
    onArchive: (id: string, archived: boolean) =>
      void archiveSession(id, archived),
    onPin: (id: string, pinned: boolean) => void pinSession(id, pinned),
    onReorderPins: (ids: string[]) => void reorderPinnedSessions(ids),
    groupMode: sessionGroupMode,
    onGroupModeChange: (mode: SessionGroupMode) => {
      setSessionGroupMode(mode);
      writeSessionGroupCookie(mode);
    },
    archiveFilter: sessionsArchiveFilter,
    onArchiveFilterChange: (value: SessionArchiveFilter) => {
      setSessionsArchiveFilter(value);
      writeSessionPref(SESSION_PREF_COOKIES.status, value);
    },
    environments: sessionEnvironments,
    sortKey: sessionsSortKey,
    onSortKeyChange: (key: SessionSortKey) => {
      setSessionsSortKey(key);
      writeSessionPref(SESSION_PREF_COOKIES.sort, key);
    },
    onNewChatInWorkspace: (cwd: string) => {
      // Park the folder and leave; the effect below applies it on the first
      // render with no session current. Doing it here would post the folder to
      // the conversation being left and leave the new chat in the default one.
      newChatWorkspaceRef.current = { path: cwd, nonce: Date.now() };
      setNewChatWorkspaceEpoch((n) => n + 1);
      goHome();
    },
    searchDraft: sessionFilterDraft,
    onSearchDraftChange: setSessionFilterDraft,
    onSearchClear: () => setSessionFilterDraft(""),
    hasMore: sessionsHasMore,
    loadingMore: sessionsLoadingMore,
    onLoadMore: () => void loadSessionsList(false),
  };

  // Every screen of the rail, whether it is on screen and what Escape does to
  // it: the step its close control takes (nav/railEscape.ts).
  useRailScreenEscape({
    history: { open: sessionsOpen, close: sessionPanelShared.onClose },
    scheduler: {
      open: schedulerOpen && schedulerHttpLinked === true,
      // An open job or its runs first, back onto the list; the drawer next.
      close: schedulerEditor ? closeSchedulerEditor : closeSchedulerDrawer,
    },
    // On a relay the swarm is the home screen, with nothing under it.
    swarm: { open: swarmRoute && !atSwarmRoot, close: onCloseSwarm },
    docs: { open: docsRoute !== null, close: onCloseDocs },
    settings: { open: settingsRoute, close: closeToChat },
  });

  const toggleRailWidth = () => {
    setRailLabelsWide((prev) => {
      const next = !prev;
      writeNavRailCookie(next ? "wide" : "narrow");
      return next;
    });
  };

  // Identity-stable handlers for the React.memo message rows: a shell
  // re-render (every streamed token) must not invalidate their props.
  const handleEditUserMessage = useStableHandler(
    (content: string, userMsgIdx: number) => {
      const assetNote = extractSessionAssetsXml(content);
      const text = stripCoddyAttachmentsForUserDisplay(content);
      // The draft to come back to is the one written before the first pencil:
      // a second pencil only switches the message being edited.
      if (editingPrevDraftRef.current === null) {
        editingPrevDraftRef.current = { draft, files: composerFiles };
      }
      // An edit resends its own attachments; files attached to the draft it
      // replaced wait for it in editingPrevDraftRef.
      setComposerFiles([]);
      setDraft(text);
      setEditingUserMsgIdx(userMsgIdx);
      setEditingSnippet(text);
      setEditingAssetNote(assetNote);
      setEditingFiles(parseSessionAssetFiles(content));
    },
  );
  const handleCancelEdit = useStableHandler(() => {
    const prev = editingPrevDraftRef.current;
    editingPrevDraftRef.current = null;
    setEditingUserMsgIdx(null);
    setEditingSnippet("");
    setEditingAssetNote("");
    setEditingFiles([]);
    setDraft(prev?.draft ?? "");
    setComposerFiles(prev?.files ?? []);
  });
  /**
   * Queue the draft for the turn that is running instead of refusing it.
   *
   * The turn can end between the keystroke and the request; the server says so
   * with `no_active_turn`, and what the operator wrote is sent as an ordinary
   * prompt rather than dropped. Any other refusal puts the text back in the
   * composer, because losing it is worse than a second attempt.
   */
  const handleQueueModeChange = useStableHandler((mode: QueueMode) => {
    setQueueMode(mode);
    void (async () => {
      const current = await fetchJSON<Record<string, unknown>>(
        "/coddy/config",
        { headers },
      );
      if (!current.ok || !current.data) return;
      const agent = (current.data.agent ?? {}) as Record<string, unknown>;
      const res = await fetch("/coddy/config", {
        method: "PUT",
        headers: { ...headers, "Content-Type": "application/json" },
        body: JSON.stringify({
          ...current.data,
          agent: { ...agent, queue_mode: mode },
        }),
      });
      if (res.ok) setConfigEpoch((e) => e + 1);
    })();
  });

  /**
   * streamResponses as the latest render has it, for a send that resumes
   * after an await (the queue's fallback): the one its own render held would
   * read the loading, the mode, the model and the level of that moment, not
   * of the moment it sends.
   */
  const sendLatest = useStableHandler(
    (text: string, opts?: Parameters<typeof streamResponses>[1]) =>
      streamResponses(text, opts),
  );
  const handleQueueMessage = useStableHandler(
    (text: string, mode: QueueMode, files: File[] = []) => {
      const sid = sessionId.trim();
      const generation = turnActivity.generation(sid);
      const queueEpoch = queueOrderRef.current.capture(sid).epoch;
      const body = text.trim();
      if (!sid || (!body && files.length === 0)) return;
      setDraft("");
      void (async () => {
        type QueueAnswer = {
          messages?: QueuedMessage[];
          version?: number;
          error?: { code?: string; message?: string };
        };
        let payload: QueueAnswer | null = null;
        let status = 0;
        try {
          const inlineFiles = await Promise.all(
            files.map(
              (file) =>
                new Promise<{ name: string; data_url: string }>(
                  (resolve, reject) => {
                    const reader = new FileReader();
                    reader.onload = () =>
                      resolve({
                        name: file.name,
                        data_url: reader.result as string,
                      });
                    reader.onerror = () => reject(reader.error);
                    reader.readAsDataURL(normalizeClipboardImageFile(file));
                  },
                ),
            ),
          );
          const res = await fetch(
            `/coddy/sessions/${encodeURIComponent(sid)}/queue`,
            {
              method: "POST",
              headers: { [HDR]: sid, "Content-Type": "application/json" },
              body: JSON.stringify({
                text: body,
                mode,
                inline_files: inlineFiles,
              }),
            },
          );
          status = res.status;
          payload = (await res.json().catch(() => null)) as QueueAnswer | null;
        } catch {
          // Network failure: treated as a refusal below.
        }
        if (status === 201 && Array.isArray(payload?.messages)) {
          applyQueue(sid, payload.messages, payload.version ?? 0, queueEpoch);
          return;
        }
        if (
          payload?.error?.code === "no_active_turn" &&
          queueOrderRef.current.capture(sid).epoch === queueEpoch &&
          turnActivity.generation(sid) === generation &&
          viewedSessionIdRef.current.trim() === sid
        ) {
          // The turn ended between the keystroke and the request. Send it as an
          // ordinary prompt; if the admission has not been released yet and that
          // is refused too, the text comes back to the composer rather than
          // being lost between the two answers.
          void sendLatest(body, { files, restoreOnRefusal: true });
          return;
        }
        if (viewedSessionIdRef.current.trim() === sid) {
          setDraft((current) => current || body);
          setComposerFiles((current) => [...files, ...current]);
        }
        applyStreamItemsForSession(sid, (prev) => [
          ...prev,
          {
            id: newId("s"),
            type: "system_notice",
            level: "error" as const,
            message:
              payload?.error?.code === "queue_full"
                ? t("composer.queueFull")
                : t("composer.queueFailed"),
            createdAtUtc: new Date().toISOString(),
          },
        ]);
      })();
    },
  );

  /**
   * Take one queued follow-up back. The list is updated at once so the card
   * disappears under the click; the server's answer (or the next
   * `message_queue` frame) is what it settles on.
   */
  const handleCancelQueued = useStableHandler((id: string) => {
    const sid = sessionId.trim();
    const queueEpoch = queueOrderRef.current.capture(sid).epoch;
    const messageID = id.trim();
    if (!sid || !messageID) return;
    const taken = (queueBySid[sid] ?? []).find((q) => q.id === messageID);
    setQueueBySid((prev) => ({
      ...prev,
      [sid]: (prev[sid] ?? []).filter((q) => q.id !== messageID),
    }));
    void (async () => {
      try {
        const res = await fetch(
          `/coddy/sessions/${encodeURIComponent(sid)}/queue/${encodeURIComponent(messageID)}`,
          { method: "DELETE", headers: { [HDR]: sid } },
        );
        const data = (await res.json().catch(() => null)) as {
          messages?: QueuedMessage[];
          version?: number;
          message?: QueuedMessage & {
            inline_files?: { name?: string; data_url?: string }[];
          };
        } | null;
        if (Array.isArray(data?.messages)) {
          applyQueue(sid, data.messages, data.version ?? 0, queueEpoch);
        }
        // Taken back before the agent read it: the text returns to the composer to be
        // edited, ahead of anything typed since. A 404 means the agent read it first,
        // and it is already in the conversation.
        const text = data?.message?.text ?? taken?.text ?? "";
        if (
          res.ok &&
          text.trim() &&
          viewedSessionIdRef.current.trim() === sid
        ) {
          setDraft((current) =>
            current.trim() ? `${text}\n\n${current}` : text,
          );
        }
        // Its images come back only in this answer: the queue every client is
        // sent names them and never carries them.
        const files = (data?.message?.inline_files ?? [])
          .map((f) => fileFromDataUrl(f.data_url ?? "", f.name ?? ""))
          .filter((f): f is File => f !== null);
        if (
          res.ok &&
          files.length > 0 &&
          viewedSessionIdRef.current.trim() === sid
        ) {
          setComposerFiles((current) => [...files, ...current]);
        }
      } catch {
        // The next message_queue frame corrects the list.
      }
    })();
  });
  /**
   * Switch a waiting message between steering the running turn and waiting for
   * its answer. The answer carries the whole queue; a message the agent read a
   * moment ago answers 404 and the next `message_queue` frame settles the list.
   */
  const handleSetQueuedMode = useStableHandler(
    (id: string, mode: QueueMode) => {
      const sid = sessionId.trim();
      const messageID = id.trim();
      if (!sid || !messageID) return;
      const queueEpoch = queueOrderRef.current.capture(sid).epoch;
      void (async () => {
        try {
          const res = await fetch(
            `/coddy/sessions/${encodeURIComponent(sid)}/queue/${encodeURIComponent(messageID)}`,
            {
              method: "PATCH",
              headers: { [HDR]: sid, "Content-Type": "application/json" },
              body: JSON.stringify({ mode }),
            },
          );
          const data = (await res.json().catch(() => null)) as {
            messages?: QueuedMessage[];
            version?: number;
          } | null;
          if (res.ok && Array.isArray(data?.messages)) {
            applyQueue(sid, data.messages, data.version ?? 0, queueEpoch);
          }
        } catch {
          // The next message_queue frame corrects the list.
        }
      })();
    },
  );
  const handleRetryLast = useStableHandler(
    () => void streamResponses(lastUserText),
  );
  /**
   * The goal popover's prompts (`/goal resume`, `/goal <objective>`) go the
   * way a typed message goes - a turn of this chat, its optimistic row, the
   * stream - without touching the draft or an edit in progress. Nothing is
   * sent while a turn runs (the popover says so and waits): like the plan
   * card's Run plan. An objective the server never took returns to the
   * composer, since somebody wrote it.
   */
  const sendGoalPrompt = useStableHandler((text: string) => {
    if (subagentTranscript) return;
    if (
      sessionId.trim() &&
      (turnActivity.get(sessionId) ??
        activeComposerSidRef.current.has(sessionId.trim()))
    ) {
      return;
    }
    void streamResponses(text, {
      restoreOnRefusal: text.trim() !== GOAL_RESUME_PROMPT,
    });
  });
  const goalActions = useMemo<GoalActions>(
    () => ({
      pause: () => sendGoalRequest("PATCH", { status: "paused" }),
      clear: () => sendGoalRequest("DELETE"),
      sendPrompt: sendGoalPrompt,
    }),
    [sendGoalRequest, sendGoalPrompt],
  );
  const viewedSessionGoal =
    viewedGoal.sid !== "" && viewedGoal.sid === sessionId.trim()
      ? viewedGoal.goal
      : null;
  const handleFetchToolCallFull = useStableHandler(
    async (toolCallId: string) => {
      if (!sessionId) return;
      const det = await fetchJSON<{
        args?: string;
        result?: string;
        meta?: {
          status?: string;
          kind?: string;
          name?: string;
          planSnapshot?: unknown;
        };
      }>(
        `/coddy/sessions/${encodeURIComponent(sessionId)}/tool-calls/${encodeURIComponent(toolCallId)}`,
        { headers },
      );
      if (!det.ok || !det.data) return;
      const meta = det.data.meta || {};
      const patch: Record<string, unknown> = { toolCallId };
      if (meta.name) patch.title = meta.name;
      if (meta.kind) patch.kind = meta.kind;
      if (meta.status) patch.status = meta.status;
      const todoPlan = normalizeTodoPlanSnapshot(meta.planSnapshot);
      if (todoPlan !== undefined) patch.todoPlan = todoPlan;
      if (det.data.args) patch.argsText = det.data.args;
      if (det.data.result !== undefined) patch.fullResultText = det.data.result;
      upsertToolCall(patch as any);
    },
  );

  return (
    <div
      className={[
        "shell",
        viewportXL && railLabelsWide ? "shell-rail-wide" : "",
      ]
        .filter(Boolean)
        .join(" ")}
    >
      <EnvHealthBanner />
      <NavRail
        // A relay has no chat to start: its home is the map, and the brand
        // leads there as it leads an agent's page to a new chat.
        onNewChat={atSwarmRoot ? openSwarmFromNav : goHome}
        onOpenHistory={onOpenHistoryFromNav}
        historyOpen={sessionsOpen}
        historyActiveCount={historyActiveCount}
        showHistory={!atSwarmRoot}
        showScheduler={schedulerHttpLinked === true && !atSwarmRoot}
        onOpenScheduler={openSchedulerFromNav}
        schedulerOpen={schedulerOpen}
        schedulerActiveCount={schedulerInfo?.runs_active ?? 0}
        showSwarm={isSwarmEnv}
        onOpenSwarm={openSwarmFromNav}
        // On a relay the map is the home screen, so its entry stays lit while
        // the map is what is on screen, whichever address shows it.
        swarmOpen={swarmRoute || (atSwarmRoot && !settingsRoute && !docsRoute)}
        {...(localDocs ? { onOpenDocs: openDocsFromNav } : {})}
        docsOpen={docsRoute !== null}
        settingsOpen={settingsRoute}
        onOpenSettings={openSettingsFromNav}
        canWidenRail={viewportXL}
        railLabelsWide={railLabelsWide}
        onToggleRailLabels={toggleRailWidth}
      />

      <div
        className={[
          "shell-main",
          sessionsOpen ? "shell-history-open" : "",
          dockOpen ? "shell-tasks-open" : "",
        ]
          .filter(Boolean)
          .join(" ")}
        style={
          schedDockClusterWidthPx > 0
            ? ({
                "--sched-dock-cluster-width": `${schedDockClusterWidthPx}px`,
              } as CSSProperties)
            : undefined
        }
      >
        <div
          className={`backdrop ${shellBackdropOpen ? "is-open" : ""}`}
          onClick={() => {
            if (!shellBackdropOpen) return;
            // Over the chat a window is all the backdrop covers: closing it
            // gives the address back to what the dock under it shows.
            if (chatWindowOpen) {
              if (filesOpen) closeFilesWindow();
              else closeEditsWindow();
              setSessionsOpen(false);
              setSchedulerOpen(false);
              setSchedulerEditor(null);
            } else {
              // Settings over unsaved edits asks first, as its close
              // button does.
              const guard = settingsRoute ? railCloseGuard("settings") : null;
              if (guard) guard();
              else closeAllShellDrawers();
            }
          }}
          aria-hidden={!shellBackdropOpen}
        />

        {sessionsOpen ? <SessionsSidebar {...sessionPanelShared} /> : null}

        {schedulerOpen && schedulerHttpLinked === true ? (
          <div
            ref={schedulerDockClusterRef}
            className={[
              "scheduler-dock-cluster",
              schedulerEditor ? "scheduler-dock-cluster-editor-active" : "",
            ]
              .filter(Boolean)
              .join(" ")}
          >
            <SchedulerJobsDrawer
              open={schedulerOpen}
              selectedJobId={
                schedulerEditor?.mode === "edit" ||
                schedulerEditor?.mode === "runs"
                  ? schedulerEditor.jobId
                  : null
              }
              className="scheduler-dock-drawer"
              onClose={closeSchedulerDrawer}
              scheduler={schedulerInfo}
              jobs={filteredSchedulerJobs}
              listError={schedulerListError}
              loading={schedulerListLoading}
              onAddJob={() => {
                setSchedulerCreateHash();
              }}
              onOpenJob={(jid) => {
                setSchedulerEditor({ mode: "edit", jobId: jid });
                setSchedulerJobHash(jid);
              }}
              onOpenRuns={openSchedulerRuns}
              onRunJob={(jid) => void onSchedulerRunJob(jid)}
              onCancelJob={(jid) => void onSchedulerCancelJob(jid)}
              searchDraft={schedulerFilterDraft}
              onSearchDraftChange={setSchedulerFilterDraft}
              onSearchClear={() => setSchedulerFilterDraft("")}
            />

            {schedulerEditor?.mode === "runs" ? (
              <BackgroundTasksPanel
                open
                className="scheduler-runs-dock"
                title={t("scheduler.runsTitle", {
                  jobId: parseSchedulerJobRef(schedulerEditor.jobId).id,
                })}
                emptyText={t("scheduler.runsEmpty")}
                focus={schedulerRunsFocus}
                onFocusHonoured={(seq) =>
                  setSchedulerRunsFocus((prev) =>
                    prev && prev.seq === seq ? null : prev,
                  )
                }
                tasks={schedulerRunsTasks}
                loadOutput={loadSchedulerRunOutput}
                listError={schedulerRunsError}
                loading={schedulerRunsLoading}
                nowMs={backgroundNowMs}
                onClose={closeSchedulerRuns}
                onStopTask={stopSchedulerRun}
                onClearFinished={() => {
                  void clearSchedulerRuns();
                }}
                onOpenSession={openSessionInPlace}
              />
            ) : null}

            <SchedulerJobEditorSheet
              open={
                schedulerHttpLinked === true &&
                !!schedulerEditor &&
                schedulerEditor.mode !== "runs"
              }
              mode={schedulerEditor?.mode === "create" ? "create" : "edit"}
              jobId={
                schedulerEditor?.mode === "edit" ? schedulerEditor.jobId : null
              }
              availableModels={llmModelIds}
              defaultModel={llmModel}
              currentCwd={currentSessionCwd}
              sessionHeaders={workspaceScope(sessionId, chatWorkspace).headers}
              workspacePath={sessionId.trim() ? chatWorkspace : ""}
              projectTrust={schedulerInfo?.project_trust || "ask"}
              onClose={closeSchedulerEditor}
              onSaved={(createdId) => {
                void refreshSchedulerJobs({ silent: true });
                if (createdId) {
                  setSchedulerEditor({ mode: "edit", jobId: createdId });
                }
              }}
              onDeleted={() => {
                setSchedulerEditor(null);
                void refreshSchedulerJobs({ silent: true });
              }}
              onOpenRuns={openSchedulerRuns}
            />
          </div>
        ) : null}

        {swarmRoute || (atSwarmRoot && !settingsRoute && !docsRoute) ? (
          <div className="swarm-dock-cluster">
            <SwarmView
              onOpenNode={(nodePath: string[]) =>
                openSwarmNode(nodePath, undefined, "#/swarm")
              }
              onOpenRelay={openSwarmRelay}
              {...(swarmRelayTarget ? { relay: swarmRelayTarget } : {})}
              {...(swarmClient
                ? { client: swarmClient, onOpenLocal: connectLocal }
                : {})}
              onOpenSession={(s) => openSwarmNode(s.node_path, `#/s/${s.id}`)}
              {...(swarmCurrentNode.length > 0
                ? { currentNode: swarmCurrentNode }
                : {})}
              {...(atSwarmRoot && swarmCurrentNode.length === 0
                ? { rootCurrent: true }
                : {})}
              {...(atSwarmRoot ? {} : { onClose: onCloseSwarm })}
            />
          </div>
        ) : null}
        {docsRoute ? (
          <div className="docs-dock-cluster">
            <DocsView
              slug={docsRoute.slug}
              anchor={docsRoute.anchor}
              onOpen={openDocsPage}
              onClose={onCloseDocs}
              {...(docsSearchSeed ? { searchSeed: docsSearchSeed } : {})}
              {...(atSwarmRoot ? {} : { onAsk: askAboutDocs })}
            />
          </div>
        ) : null}
        {settingsRoute ? (
          <div className="settings-dock-cluster">
            <Settings
              onClose={closeToChat}
              onConfigSaved={() => {
                setConfigEpoch((e) => e + 1);
                void refreshConfiguredRemotes();
              }}
              initialSection={settingsSection}
              initialItem={settingsItem}
              activeSessionId={sidebarActiveId}
              onSessionsDeleted={onSessionsDeletedInSettings}
              // spawn_agent resolves definitions against the session's own
              // cwd: the viewed session's workspace (or the folder a new chat
              // picked) is the one the Subagents and Skills tabs list.
              workspacePath={chatWorkspace || undefined}
              onSessionTagsChanged={(id: string, tags: string[]) =>
                setSessions((prev) =>
                  prev.map((s) => (s.id === id ? { ...s, tags } : s)),
                )
              }
              // On a relay the drawer edits the relay's own deployment.
              relay={atSwarmRoot}
            />
          </div>
        ) : null}
        {dockOpen ? (
          <BackgroundTasksPanel
            open
            focus={
              tasksFocus && tasksFocus.sid === sessionId.trim()
                ? tasksFocus
                : null
            }
            onFocusHonoured={spendTasksFocus}
            tasks={backgroundTasks}
            loadOutput={loadBackgroundTaskOutput}
            listError={backgroundListError}
            loading={backgroundListLoading}
            nowMs={backgroundNowMs}
            onClose={closeTasksDrawer}
            onStopTask={stopBackgroundTaskById}
            onClearFinished={() => {
              void clearFinishedTasks();
            }}
            onOpenSession={openSessionInPlace}
          />
        ) : null}

        {/* After the dock, so on the stacked shell, where both are sheets over
            the chat, the window is the one on top. */}
        {changesViewerOpen && sessionId.trim() ? (
          <EditsView
            key={`edits:${sessionId}`}
            sessionId={sessionId}
            workspacePath={workspaceCtx?.path || ""}
            onClose={closeEditsWindow}
          />
        ) : null}
        {filesOpen && sessionId.trim() ? (
          <FilesView
            key={filesWindowKey}
            sessionId={sessionId}
            workspacePath={workspaceCtx?.path || ""}
            initialPath={filePath}
            initialLine={fileLine}
            openSeq={fileOpenSeq}
            toolActivity={finishedToolCalls(transcriptItems)}
            onNavigate={(path, line) =>
              setSessionFilesHash(sessionId, path, line)
            }
            onClose={closeFilesWindow}
          />
        ) : null}

        {atSwarmRoot ? null : (
          <ChatScreen
            title={currentTitle}
            sessionId={sessionId}
            onOpenEdits={openEditsWindow}
            onOpenFiles={() =>
              filesOpen ? closeFilesWindow() : openFilesWindow()
            }
            filesOpen={filesOpen}
            backgroundTasks={backgroundTasks}
            onOpenBackgroundTasks={openTasksFromNav}
            onBackgroundTasksChanged={() => {
              void refreshBackgroundTasks({ silent: true });
            }}
            backgroundTasksByToolCallId={backgroundTasksByToolCallId}
            backgroundNowMs={backgroundNowMs}
            subagentTranscript={subagentTranscript}
            sessionArchived={viewedArchived}
            unarchiving={unarchiving}
            onUnarchiveSession={() => void unarchiveViewedSession()}
            onOpenSession={openSessionInPlace}
            pathRoots={transcriptPathRoots}
            turnProgress={turnProgressBySid[sessionId.trim()] ?? null}
            backgroundTasksOpen={tasksPanelOpen}
            onCloseBackgroundTasks={closeTasksDrawer}
            workspaceCtx={workspaceCtx}
            chatWorkspacePath={chatWorkspace}
            worktreePref={worktreePref}
            workspaceLocked={items.length > 0}
            onWorkspacePickFolder={(p: string) =>
              void switchWorkspace({ path: p })
            }
            onWorkspacePickBranch={(b: string, wt: boolean) =>
              void switchWorkspace({ branch: b, worktree: wt })
            }
            onWorkspaceRefreshBranches={refreshWorkspaceBranches}
            onWorktreeToggle={() =>
              setWorktreePref((v) => {
                writeWorktreePref(!v);
                return !v;
              })
            }
            sessionLoading={sessionLoading}
            sessionFadingOut={sessionFadingOut}
            heroAccentVerb={heroAccentVerb}
            heroComposerFocusEpoch={heroHomeGeneration}
            onTitleSave={(t: string) => void saveSessionTitle(sessionId, t)}
            items={transcriptItems}
            userMsgIndexBase={transcriptTopWindow?.turnsBefore ?? 0}
            transcriptHasOlder={(transcriptTopWindow?.offset ?? 0) > 0}
            olderTranscriptLoad={olderTranscriptLoad}
            onLoadOlderTranscript={() => void loadOlderTranscript()}
            onReaderAtTailChange={(atTail: boolean) => {
              readerAtTailRef.current = atTail;
              if (atTail) slideTranscriptToTail();
            }}
            draft={draft}
            tokenUsage={tokenUsage}
            providerUsage={providerUsageState.usage}
            usageBannerDismissedKey={providerUsageState.dismissedKey}
            onUsageBannerDismiss={providerUsageState.dismissBanner}
            contextPct={contextPct}
            maxContextTokens={maxContextTokens}
            contextBreakdown={contextBreakdown}
            compactionSettings={{
              ...compactionSettings,
              enabled:
                compactionSettings.enabled &&
                !subagentTranscript &&
                !viewedArchived,
            }}
            onContextCompacted={() => {
              const sid = sessionId.trim();
              if (sid) {
                void loadMessages(sid, { freshLoad: true });
                void refreshSessionStats(sid);
              }
            }}
            mode={mode}
            modes={[...PROFILE_MODES]}
            {...(llmModelIds.length > 0
              ? {
                  llmModels: llmModelIds,
                  llmModel,
                  onLlmModelChange,
                  llmModelMultimodal,
                  llmReasoningLevelsByModel,
                  ...(llmReasoningLevels.length > 0
                    ? {
                        llmReasoningLevels,
                        llmReasoning,
                        onLlmReasoningChange,
                      }
                    : {}),
                }
              : {})}
            onModeChange={setMode}
            permissionMode={
              sessionId.trim() ? permissionMode : startPermissionMode
            }
            configuredPermissionMode={shownConfiguredPermissionMode}
            onPermissionModeChange={
              subagentTranscript ? undefined : onPermissionModeChange
            }
            settingsOverrides={settingsOverrides}
            {...(subagentTranscript
              ? {}
              : { goal: viewedSessionGoal, goalActions })}
            onDraftChange={setDraft}
            onMentionArtifact={(path: string) =>
              setDraft(
                (current) => `${current}${current.trim() ? " " : ""}@${path}`,
              )
            }
            generating={generating}
            {...(!generating && lastUserText.trim() && !subagentTranscript
              ? { onRetryLast: handleRetryLast }
              : {})}
            onContextRingOpen={() => {
              const sid = sessionId.trim();
              if (sid) {
                void refreshSessionStats(sid);
              }
            }}
            onStop={() => stopActiveGeneration()}
            {...(subagentTranscript
              ? {}
              : {
                  queuedMessages,
                  onQueue: handleQueueMessage,
                  ...(queueMode ? { queueMode } : {}),
                  onQueueModeChange: handleQueueModeChange,
                  onSetQueuedMode: handleSetQueuedMode,
                  onCancelQueued: handleCancelQueued,
                })}
            onQuestionPromptResolved={resolveQuestionPrompt}
            onPermissionPromptResolved={resolvePermissionPrompt}
            onPlanDocumentExpanded={(itemId, expanded) => {
              setItems((prev) =>
                prev.map((x) =>
                  x.id === itemId && x.type === "plan_document"
                    ? { ...x, expanded }
                    : x,
                ),
              );
            }}
            // A subagent transcript is read-only: like onEdit below, Run plan and
            // Discard are withheld rather than stubbed, so the plan card renders
            // without its footer and its editor is read-only.
            {...(subagentTranscript
              ? {}
              : {
                  onPlanDocumentRun: (slug: string) => {
                    if (
                      sessionId.trim() &&
                      (turnActivity.get(sessionId) ??
                        activeComposerSidRef.current.has(sessionId.trim()))
                    ) {
                      return;
                    }
                    void streamResponses(t("chat.runPlanMessage"), {
                      modeOverride: "agent",
                      runPlanSlug: slug,
                    });
                  },
                  onPlanDocumentDiscard: async (
                    itemId: string,
                    slug: string,
                  ) => {
                    const sid = sessionId.trim();
                    if (!sid) return;
                    try {
                      await fetch(
                        `/coddy/sessions/${encodeURIComponent(sid)}/plans/${encodeURIComponent(slug)}`,
                        {
                          method: "DELETE",
                          headers,
                        },
                      );
                    } catch {
                      return;
                    }
                    setItems((prev) =>
                      prev.map((x) =>
                        x.id === itemId && x.type === "plan_document"
                          ? { ...x, discarded: true }
                          : x,
                      ),
                    );
                  },
                })}
            {...(subagentTranscript ? {} : { onEdit: handleEditUserMessage })}
            {...(editingFiles.length > 0 ? { editingFiles } : {})}
            {...(!subagentTranscript && editingUserMsgIdx !== null
              ? {
                  editingUserMsgIdx,
                  editingSnippet,
                  onCancelEdit: handleCancelEdit,
                }
              : {})}
            {...(!subagentTranscript
              ? (() => {
                  const undoIdx =
                    rewindUndo && rewindUndo.sid === sessionId.trim()
                      ? rewindUndo.userMsgIdx
                      : null;
                  return {
                    rewindUndoUserMsgIdx: undoIdx,
                    rewindUndoBanner:
                      undoIdx !== null &&
                      rewindUndoDismissed !== `${sessionId.trim()}:${undoIdx}`,
                    rewindUndoBusy,
                    onUndoEdit: () => void handleUndoEdit(),
                    onDismissRewindUndo: () =>
                      setRewindUndoDismissed(`${sessionId.trim()}:${undoIdx}`),
                  };
                })()
              : {})}
            {...(knownSkillNames.size > 0 ? { knownSkillNames } : {})}
            onDocsCommand={openDocsCommand}
            onMCPCommand={() => {
              setDraft("");
              setSchedulerOpen(false);
              setSchedulerEditor(null);
              setTasksOpen(false);
              setSessionsOpen(false);
              setSettingsSectionHash(MCP_SECTION_ID);
            }}
            attachedFiles={composerFiles}
            onAttachedFilesChange={setComposerFiles}
            onSend={(text: string, files?: File[]) => {
              // A subagent transcript is read-only: the server answers 409.
              if (subagentTranscript) {
                return;
              }
              if (
                sessionId.trim() &&
                (turnActivity.get(sessionId) ??
                  activeComposerSidRef.current.has(sessionId.trim()))
              ) {
                // Not sent: the composer already let go of the files.
                if (files && files.length > 0)
                  setComposerFiles((prev) => [...files, ...prev]);
                return;
              }
              if (editingUserMsgIdx !== null) {
                const idx = editingUserMsgIdx;
                const note = editingAssetNote;
                const textWithAssets = note ? `${text}\n${note}` : text;
                // The draft and the editing state stay until the rewind lands;
                // handleRewindSend clears them on success.
                void handleRewindSend(textWithAssets, idx);
              } else {
                setDraft("");
                // A prompt after the edited turn ends the undo on the server.
                setRewindUndo(null);
                setRewindUndoDismissed("");
                void streamResponses(text, {
                  restoreOnRefusal: true,
                  ...(files ? { files } : {}),
                });
              }
            }}
            onFetchToolCallFull={handleFetchToolCallFull}
          />
        )}
      </div>
    </div>
  );
}
