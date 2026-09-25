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
import type { QueuedMessage } from "./chat/Composer";
import {
  contextUsagePercent,
  withContextUsedTokens,
} from "./chat/contextUsage";
import { HERO_ACCENT_VERBS, pickHeroAccentVerb } from "./chat/heroTitleWords";
import { insertNewThinkingBeforeStreamingAssistant } from "./chat/transcriptThinkingPlacement";
import { openAIStreamErrorMessage } from "./chat/streamError";
import { optimisticUserFiles } from "./chat/optimisticUserFiles";
import { sessionMessageFiles } from "./chat/sessionMessageFiles";
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
import {
  isNewerSettings,
  parseSessionSettings,
  type SessionSettings,
  type SessionSettingsEvent,
  type TurnOverride,
} from "./chat/sessionSettings";
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
  stableAssistantItemId,
  stablePermissionPromptItemId,
  stableThinkingItemId,
  stableToolCallItemId,
  stableUserItemId,
  stableWakeItemId,
} from "./chat/transcriptItemIds";
import { parseBackgroundWakeTasks } from "./chat/backgroundWake";
import { uiLogNoticeFeed } from "./chat/uiLogNotices";
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
import { pickRicherToolArgs } from "./chat/toolCallArgs";
import { normalizeTodoPlanSnapshot } from "./chat/todoToolPreview";
import {
  clearQuestionPromptRecords,
  mergeStoredQuestionPromptsIntoTranscript,
  patchQuestionToolArgsFromPromptRecords,
  pickRicherQuestionToolArgs,
  upsertQuestionPromptRecord,
} from "./chat/questionPromptSessionStore";
import { transcriptHasFilledAssistant } from "./chat/streamSyncLocalAssistant";
import { applyMemoryRunToItems } from "./chat/memoryRun";
import type { TokenUsage, TranscriptItem } from "./chat/types";
import type { ProviderUsage } from "./chat/providerUsage";
import type { WorkspaceContext } from "./chat/workspaceContext";
import { setHostShell } from "./chat/hostShell";
import { NavRail } from "./nav/NavRail";
import { SwarmView } from "./swarm/SwarmView";
import { EnvironmentChip } from "./chat/EnvironmentChip";
import { probeSwarm } from "./swarm/api";
import {
  connectLocal,
  connectRemote,
  connectSwarmNode,
  getRemoteToken,
  localFetch,
  returnToSwarm,
  snapshotEnv,
  subscribeEnv,
} from "./env/remoteEnv";
import type { SessionsEnvironmentOption } from "./sessions/SessionsFilterMenu";
import {
  newChatWorkspaceIsReady,
  type PendingNewChatWorkspace,
} from "./sessions/newChatWorkspace";
import { readNavRailCookie, writeNavRailCookie } from "./nav/navRailCookie";
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
import { fetchDocsPage } from "./docs/api";
import { docsCommandOpensPage } from "./docs/docsCommand";
import { SchedulerJobEditorSheet } from "./scheduler/SchedulerJobEditorSheet";
import { SchedulerJobsDrawer } from "./scheduler/SchedulerJobsDrawer";
import {
  BackgroundTasksPanel,
  type TaskFocus,
} from "./tasks/BackgroundTasksPanel";
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
import { Settings } from "./settings/Settings";
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

type ToolCallListRow = {
  toolCallId: string;
  name?: string;
  kind?: string;
  status?: string;
  startedAt?: string;
  finishedAt?: string;
  argsPreview?: string;
  resultPreview?: string;
  resultPreviewTruncated?: boolean;
  planSnapshot?: unknown;
};

function readMessageCreatedAtUTC(
  m: Record<string, unknown>,
): string | undefined {
  const raw = m.created_at ?? m.createdAt;
  if (typeof raw !== "string") {
    return undefined;
  }
  const s = raw.trim();
  return s === "" ? undefined : s;
}

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

function parseRFC3339ms(s: string | undefined): number | null {
  const t = (s || "").trim();
  if (!t) return null;
  const ms = Date.parse(t);
  return Number.isFinite(ms) ? ms : null;
}

function reasoningDurationCacheKey(text: string): string {
  return text.trim().replace(/\s+/g, " ");
}

export function App() {
  const { t } = useT();
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
  const [sessionsCursor, setSessionsCursor] = useState<string | null>(null);
  const sessionsCursorRef = useRef<string | null>(null);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  const [items, setItems] = useState<TranscriptItem[]>([]);
  const [sessionLoading, setSessionLoading] = useState(
    () => initialRoute.branch === "session",
  );
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
  const [worktreePref, setWorktreePref] = useState(false);
  // Pre-session workspace choices, applied right before the first send creates the session.
  const pendingWorkspaceRef = useRef<{
    path?: string;
    branch?: string;
    worktree?: boolean;
  } | null>(null);
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
        [key]: { model: w.model, epoch: configEpochRef.current, size: w.tokens },
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
  const postAbortBySidRef = useRef<Map<string, AbortController>>(new Map());
  const relayAbortBySidRef = useRef<Map<string, AbortController>>(new Map());
  const pendingPostBySidRef = useRef(new Map<string, AbortController>());
  const streamGenerationBySidRef = useRef(new Map<string, number>());
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
    subagentPermission: (parentSid: string) => void;
    sessionRewound: (sid: string) => void;
    ready: () => void;
  }>({
    turnStarted: () => {},
    turnEnded: () => {},
    providerUsage: () => {},
    configReloaded: () => {},
    messageQueue: () => {},
    sessionSettings: () => {},
    subagentPermission: () => {},
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
  const queuedMessages = generating ? (queueBySid[sessionId.trim()] ?? []) : [];

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
    void loadMessages(sid, {
      preserveOnError: true,
      skipSetItems: viewedSessionIdRef.current.trim() !== sid,
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
  // background task started has no text anybody typed, so there is nothing to
  // re-run and no retry is offered.
  const lastUserText = useMemo(() => {
    for (let i = items.length - 1; i >= 0; i--) {
      const it = items[i];
      if (it && it.type === "background_wake") return "";
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
  /** null until first probe of /coddy/scheduler/jobs; false when route returns 404 (binary without scheduler). */
  const [schedulerHttpLinked, setSchedulerHttpLinked] = useState<
    boolean | null
  >(null);
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
  // plain agent there is no swarm to show.
  const [isSwarmEnv, setIsSwarmEnv] = useState(false);
  /**
   * True while the environment is a relay itself rather than a node reached
   * through one.
   *
   * A relay holds no sessions, no workspace and no model of its own - it serves
   * no /coddy/* at all - so a chat box and a history drawer there are furniture
   * for a room nobody can enter. What it does have is the swarm, so that is
   * what it shows.
   */
  const [atSwarmRoot, setAtSwarmRoot] = useState(false);
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
  const [schedulerRunsTasks, setSchedulerRunsTasks] = useState<BackgroundTask[]>([]);
  const [schedulerRunsRunning, setSchedulerRunsRunning] = useState(0);
  const [schedulerRunsError, setSchedulerRunsError] = useState<string | null>(null);
  const [schedulerRunsLoading, setSchedulerRunsLoading] = useState(false);
  const [tasksOpen, setTasksOpen] = useState(
    () => initialRoute.branch === "session" && initialRoute.tasksOpen,
  );
  // A card the shell asks the Tasks panel to open ("Open in Tasks" on a transcript
  // row, a link that names a task). Which cards are open otherwise is the panel's own
  // business and is not part of the address.
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
  // the place you are.
  const [configuredRemotes, setConfiguredRemotes] = useState<
    { name: string; url: string }[]
  >([]);
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
  const viewedArchiveWriteRef = useRef<{ sid: string; archived: boolean; at: number } | null>(
    null,
  );
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
  const [settingsOverrides, setSettingsOverrides] = useState<TurnOverride[]>(
    [],
  );
  const settingsVersionRef = useRef<{ sid: string; version: number }>({
    sid: "",
    version: 0,
  });
  /** A permission mode picked before the chat has a session: it rides in as a
   *  command at the start of the first message, where the server takes it. */
  const pendingPermissionModeRef = useRef("");
  const [llmModelIds, setLlmModelIds] = useState<string[]>([]);
  const [llmModel, setLlmModel] = useState("");
  const applyContextUsage = useStableHandler((sid: string, u: ContextUsageUpdate) => {
    setContextBreakdown((prev) => withContextUsedTokens(prev, u.used));
    setSessionContextWindows((prev) => ({
      ...prev,
      [sid]: { model: llmModel, epoch: configEpoch, size: u.size },
    }));
    debouncedRefreshSessionStats(sid);
  });
  const providerUsageState = useProviderUsage({
    sessionId,
    llmModel,
    turnEpoch: providerUsageTurnEpoch,
  });
  const [llmReasoning, setLlmReasoning] = useState("");
  /**
   * Raw model/reasoning stored on the opened session. Held until the backends
   * list (`llmModelIds`) is available so the restore survives whichever of
   * `/v1/models` and `/coddy/sessions/.../messages` resolves first on reload.
   */
  const [openSessionSelection, setOpenSessionSelection] = useState<{
    sid: string;
    model: string;
    reasoning: string;
  } | null>(null);
  /** The selection object already applied to the composer; see the effect below. */
  const appliedSessionSelectionRef = useRef<{
    sid: string;
    model: string;
    reasoning: string;
  } | null>(null);
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

  const refreshWorkspaceContext = useCallback(async (sid: string) => {
    try {
      const res = await fetch("/coddy/workspace/context", {
        headers: sid ? { [HDR]: sid } : {},
      });
      if (res.ok) {
        const ctx = (await res.json()) as WorkspaceContext;
        setWorkspaceCtx(ctx);
        // Host fact, not a workspace one: the tool cards name the interpreter.
        setHostShell(ctx.shell);
      }
    } catch {
      // ignore: chips keep the previous context
    }
  }, []);

  // Load the workspace context whenever the viewed session changes; a fresh
  // home/draft view also drops stale pre-session workspace choices.
  //
  // A folder picked from a History heading is applied here rather than where it
  // was picked: leaving a conversation is asynchronous, and a workspace change
  // issued before the session is gone lands on the conversation being left.
  // It replaces the default probe rather than running beside it - two context
  // fetches in flight would be decided by whichever answered last.
  useEffect(() => {
    pendingWorkspaceRef.current = null;
    const wanted = newChatWorkspaceRef.current;
    if (newChatWorkspaceIsReady(wanted, sessionId)) {
      newChatWorkspaceRef.current = null;
      void switchWorkspace({ path: String(wanted?.path ?? "") });
      return;
    }
    void refreshWorkspaceContext(sessionId);
  }, [sessionId, refreshWorkspaceContext, newChatWorkspaceEpoch]);

  async function switchWorkspace(payload: {
    path?: string;
    branch?: string;
    worktree?: boolean;
  }) {
    const sid = sessionId.trim();
    if (!sid) {
      // No session yet: remember the choice and preview the target context.
      pendingWorkspaceRef.current = {
        ...(pendingWorkspaceRef.current || {}),
        ...payload,
      };
      if (payload.path) {
        try {
          const res = await fetch(
            "/coddy/workspace/context?path=" + encodeURIComponent(payload.path),
          );
          if (res.ok) {
            setWorkspaceCtx((await res.json()) as WorkspaceContext);
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
        setWorkspaceCtx((await res.json()) as WorkspaceContext);
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
    pendingWorkspaceRef.current = null;
    if (!pending || (!pending.path && !pending.branch)) {
      return;
    }
    const base = { "Content-Type": "application/json", [HDR]: sid };
    try {
      if (pending.path) {
        await fetch(`/coddy/sessions/${encodeURIComponent(sid)}/workspace`, {
          method: "POST",
          headers: base,
          body: JSON.stringify({ path: pending.path }),
        });
      }
      if (pending.branch) {
        await fetch(`/coddy/sessions/${encodeURIComponent(sid)}/workspace`, {
          method: "POST",
          headers: base,
          body: JSON.stringify({
            branch: pending.branch,
            worktree: Boolean(pending.worktree),
          }),
        });
      }
    } catch {
      // ignore: the session still starts in the default workspace
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

  const refreshSchedulerJobs = useCallback(
    async (opts?: { silent?: boolean }) => {
      const silent = !!opts?.silent;
      if (!silent) {
        setSchedulerListLoading(true);
        setSchedulerListError(null);
      }
      const res = await schedulerListJobs(false);
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
    [sessionId, t],
  );

  const applyLocationHash = useCallback(() => {
    const p = parseAppHash();
    if (p.branch === "docs") {
      setDocsRoute({ slug: p.slug, anchor: p.anchor });
      if (p.slug) {
        lastDocsSlugRef.current = p.slug;
      }
      setSwarmRoute(false);
      setSettingsRoute(false);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setTasksOpen(false);
      setSessionsOpen(false);
      return;
    }
    setDocsRoute(null);
    if (p.branch === "session") {
      setSettingsRoute(false);
      setActiveDraftId("");
      viewedSessionIdRef.current = p.sessionId.trim();
      setSessionId(p.sessionId);
      setSessionLoading(true);
      void markCoddySessionActivityRead(p.sessionId);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setTasksOpen(p.tasksOpen);
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
      setTasksOpen(false);
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
      setSessionsOpen(true);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setTasksOpen(false);
      return;
    }
    if (p.branch === "swarm") {
      setSwarmRoute(true);
      setSettingsRoute(false);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setTasksOpen(false);
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
      setTasksOpen(false);
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
      setTasksOpen(false);
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
    (id: string, opts?: { historySidebar?: boolean }) => {
      setActiveDraftId("");
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setTasksOpen(false);
      viewedSessionIdRef.current = id.trim();
      setSessionHashInLocation(id, opts);
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

  const closeAllShellDrawers = useCallback(() => {
    setSessionsOpen(false);
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    setDocsRoute(null);
    if (parseAppHash().branch === "settings" || parseAppHash().branch === "docs") {
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
    if (prevSessionsOpenRef.current && !sessionsOpen && composerAutoFocusAllowed()) {
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
    const onHash = () => applyLocationHash();
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, [applyLocationHash]);

  useEffect(() => {
    void (async () => {
      const res = await fetchJSON<{ items?: Array<{ name: string }> }>(
        "/coddy/slash-commands?page=1&page_size=200",
      );
      if (res.ok && res.data?.items) {
        setKnownSkillNames(new Set(res.data.items.map((i) => i.name)));
      }
    })();
    // Slash commands are derived from skills.dirs, so a config swap moves them too.
  }, [configEpoch]);

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
    setSchedulerEditor({ mode: "runs", jobId: schedulerRunsJobId, taskId: null });
    setSchedulerJobRunsHash(schedulerRunsJobId);
  }, [schedulerRunsJobId, schedulerRunsLinkedTaskId]);
  /** The job session the runs live under; empty until the job ran once. */
  const schedulerRunsSessionId = useMemo(() => {
    if (!schedulerRunsJobId) {
      return "";
    }
    const job = schedulerJobs.find((j) => j.job_id === schedulerRunsJobId);
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
    const nextModel = pickLlmModelForOpenSession({
      backends: llmModelIds,
      sessionModel: openSessionSelection.model,
      cookie: readLlmModelCookie(),
    });
    setLlmModel(nextModel);
    // A session carries a reasoning level only once something chose one for it,
    // and a model that names no `reasoning_default` makes the server report the
    // effective level as empty. Applied as it comes, that empties the composer
    // while the turn still runs at the model's default - so it goes through the
    // same chooser as every other path, with the session's value as the
    // preference rather than as the answer.
    const openRow = modelInfos.find((m) => m.id === nextModel);
    setLlmReasoning(
      pickReasoningLevel({
        levels: openRow?.reasoningLevels ?? [],
        cookie: readReasoningCookie(),
        sessionLevel: openSessionSelection.reasoning,
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

  useEffect(() => {
    if (!sessionsOpen && !schedulerOpen) {
      return;
    }
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key !== "Escape") {
        return;
      }
      if (schedulerEditor) {
        setSchedulerEditor(null);
        setSchedulerListHash();
        return;
      }
      if (schedulerOpen) {
        closeSchedulerDrawer();
        return;
      }
      if (sessionsOpen) {
        setSessionsOpen(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [sessionsOpen, schedulerOpen, schedulerEditor, closeSchedulerDrawer]);

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
        res = await fetchJSON<SessionsPage>(`/coddy/sessions?${ps.toString()}`, {
          headers,
        });
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
      }
    })();
  }, [headers]);

  useEffect(() => {
    const ids = new Set(permissionPendingSessionIdsFromStorage());
    for (const row of sessions) {
      if (row.permissionPending) {
        ids.add(row.id);
      }
    }
    const sid = sessionId.trim();
    if (
      sid &&
      items.some((x) => x.type === "permission_prompt" && !x.resolved)
    ) {
      ids.add(sid);
    }
    setPermissionPendingSids(ids);
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
    configReloaded: () => setConfigEpoch((e) => e + 1),
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
    // Changing the environment reloads the page, so the one read here holds.
    const env = getEnv();
    void subscribeSharedServerEvents({
      env,
      onRefused: (status) => {
        if (status === 401 && env.mode === "local") notifyLocalApiUnauthorized();
      },
      onTurnStarted: (sid) => serverEventHandlersRef.current.turnStarted(sid),
      onTurnEnded: (sid) => serverEventHandlersRef.current.turnEnded(sid),
      onProviderUsage: (_sid, usage) =>
        serverEventHandlersRef.current.providerUsage(usage),
      onConfigReloaded: () => serverEventHandlersRef.current.configReloaded(),
      onMessageQueue: (sid, queue) =>
        serverEventHandlersRef.current.messageQueue(sid, queue),
      onSessionSettings: (event) =>
        serverEventHandlersRef.current.sessionSettings(event),
      onSubagentPermission: (parentSid) =>
        serverEventHandlersRef.current.subagentPermission(parentSid),
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

  async function loadMessages(
    idOverride?: string,
    opts?: {
      skipSetItems?: boolean;
      preserveOnError?: boolean;
      freshLoad?: boolean;
    },
  ): Promise<TranscriptItem[] | null> {
    const sid = (idOverride ?? sessionId).trim();
    if (!sid) {
      setItems([]);
      return null;
    }
    const streamGeneration = streamGenerationBySidRef.current.get(sid);
    const sameStream = () =>
      streamGenerationBySidRef.current.get(sid) === streamGeneration;
    // When the read was issued, for the archive flag ordering below: a reply
    // that lands after this client's archive PATCH may carry the flag from
    // before the write, and the write is what must stay on screen.
    const issuedAt = Date.now();
    const res = await fetchJSON<{
      messages: Array<any>;
      model?: string;
      selectedModelId?: string;
      selectedReasoning?: string;
      settings?: unknown;
      subagent?: {
        parentSessionId?: string;
        name?: string;
        taskId?: string;
      } | null;
      readOnly?: boolean;
      archived?: boolean;
      messagesRev?: number;
      uiLog?: Array<{
        id?: string;
        level?: string;
        message?: string;
        userTurnIndex?: number;
        createdAt?: string;
      }>;
    }>(`/coddy/sessions/${encodeURIComponent(sid)}/messages`, {
      headers: sid === sessionId ? headers : { [HDR]: sid },
    });
    // Re-read the viewed session after the await: the viewer may have moved
    // on while the request was in flight, and a stale response must neither
    // clear the new session's rows nor merge them into this session's shadow.
    const viewingNow = viewedSessionIdRef.current.trim();
    if (!sameStream()) return null;
    if (!res.ok || !res.data) {
      if (!opts?.preserveOnError) {
        if (viewingNow === sid) {
          setItems([]);
        }
      }
      return null;
    }
    if (viewingNow === sid) {
      // Stash the session's saved selection; an effect applies it once the
      // backends list is loaded (the two fetches race on reload). The reasoning
      // level is later validated by the clamp effect against the chosen model.
      setOpenSessionSelection({
        sid,
        model: (res.data.model || res.data.selectedModelId || "").trim(),
        reasoning: (res.data.selectedReasoning || "").trim(),
      });
      // The whole snapshot: the mode, the permission mode, the overrides for
      // the next turns, and the version the next send names.
      const snap = parseSessionSettings(res.data.settings);
      if (snap) {
        applySessionSettings(snap);
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
    }
    const next: TranscriptItem[] = [];
    // Notices are stamped with the server's count of user-role messages, so
    // every user-role row below - a compaction summary and a wake too - asks
    // the feed for the notices that end the turn before it.
    const notices = uiLogNoticeFeed(res.data.uiLog, newId);
    const toolIdx = new Map<string, number>();
    let userTurnIdx = 0;
    let thinkingInTurn = 0;
    let assistantInTurn = 0;
    const stripCompactionPreamble = (s: string): string => {
      const marker = "Summary of the compacted part:";
      const i = s.indexOf(marker);
      return i >= 0 ? s.slice(i + marker.length).trimStart() : s.trim();
    };
    for (const m of res.data.messages || []) {
      const role = (m.role || "").trim();
      if (role === "user") {
        next.push(...notices.beforeUserRow());
        // A compaction summary row is a user-role message flagged by the server;
        // render it as its own "context compacted" foldout, not a user bubble,
        // and do not count it as a real user turn.
        if ((m as Record<string, unknown>).compaction_summary === true) {
          const ccat = readMessageCreatedAtUTC(m as Record<string, unknown>);
          next.push({
            id: newId("compaction"),
            type: "compaction",
            summary: stripCompactionPreamble(m.content || ""),
            ...(ccat ? { createdAtUtc: ccat } : {}),
          });
          continue;
        }
        userTurnIdx++;
        thinkingInTurn = 0;
        assistantInTurn = 0;
        const cat = readMessageCreatedAtUTC(m as Record<string, unknown>);
        // Nobody typed the first message of a turn a finished background
        // task started, and nothing shows in its place: the turn reads as the
        // agent carrying on. It still opens a turn, so the ids of the turn
        // line up with the server's count of user messages for a rewind.
        const wakeTasks = parseBackgroundWakeTasks(
          (m as Record<string, unknown>).background_wake,
        );
        if (wakeTasks.length > 0) {
          next.push({
            id: stableWakeItemId(userTurnIdx),
            type: "background_wake",
            tasks: wakeTasks,
            ...(cat ? { createdAtUtc: cat } : {}),
          });
          continue;
        }
        const rawContent = m.content || "";
        const parsedAssets = sessionMessageFiles(
          (m as Record<string, unknown>).files,
          rawContent,
        );
        next.push({
          id: stableUserItemId(userTurnIdx),
          type: "user_message",
          content: rawContent,
          ...(cat ? { createdAtUtc: cat } : {}),
          ...(parsedAssets.length > 0 ? { files: parsedAssets } : {}),
        });
        continue;
      }
      if (role === "assistant") {
        const pdRaw = (m as Record<string, unknown>).plan_document;
        if (pdRaw && typeof pdRaw === "object" && !Array.isArray(pdRaw)) {
          const pd = pdRaw as Record<string, unknown>;
          const slug = String(pd.slug ?? "").trim();
          if (slug) {
            next.push({
              id: newId("pd"),
              type: "plan_document",
              slug,
              name: String(pd.name ?? ""),
              overview: String(pd.overview ?? ""),
              content: String(pd.content ?? ""),
              body: String(pd.body ?? ""),
              expanded: false,
              ...(pd.path ? { path: String(pd.path) } : {}),
              ...(pd.discarded === true ? { discarded: true } : {}),
              ...(pd.updatedAt ? { updatedAtUtc: String(pd.updatedAt) } : {}),
            });
          }
        }
        const reasoning = (m.reasoning || "").trim();
        if (reasoning) {
          const dk = reasoningDurationCacheKey(reasoning);
          const cachedMs = dk
            ? reasoningDurationMsByContentRef.current.get(dk)
            : undefined;
          const durRaw = (m as { reasoning_duration_ms?: unknown })
            .reasoning_duration_ms;
          let fromApi: number | undefined;
          if (
            typeof durRaw === "number" &&
            Number.isFinite(durRaw) &&
            durRaw >= 0
          ) {
            fromApi = Math.round(durRaw);
          } else if (typeof durRaw === "string" && durRaw.trim() !== "") {
            const n = Number(durRaw);
            if (Number.isFinite(n) && n >= 0) {
              fromApi = Math.round(n);
            }
          }
          const durationMs = fromApi !== undefined ? fromApi : cachedMs;
          if (fromApi !== undefined && dk.length > 0) {
            reasoningDurationMsByContentRef.current.set(dk, fromApi);
          }
          next.push({
            id: stableThinkingItemId(userTurnIdx, thinkingInTurn++),
            type: "thinking",
            status: "completed",
            content: reasoning,
            ...(durationMs !== undefined ? { durationMs } : {}),
          });
        }
        const content = m.content || "";
        if (content.trim()) {
          const acat = readMessageCreatedAtUTC(m as Record<string, unknown>);
          next.push({
            id: stableAssistantItemId(userTurnIdx, assistantInTurn++),
            type: "assistant_message",
            content,
            ...(acat ? { createdAtUtc: acat } : {}),
          });
        }
        const tcs = Array.isArray(m.tool_calls) ? m.tool_calls : [];
        for (const tc of tcs) {
          const id = tc?.id || "";
          const fn = tc?.function || {};
          const name = (fn?.name || "").trim();
          const args = fn?.arguments || "";
          if (!id) continue;
          if (toolIdx.has(id)) continue;
          const it: Extract<TranscriptItem, { type: "tool_call" }> = {
            id: stableToolCallItemId(id),
            type: "tool_call",
            toolCallId: id,
            status: "pending",
          };
          if (name) it.title = name;
          if (args) it.argsText = args;
          toolIdx.set(id, next.length);
          next.push(it);
        }
        continue;
      }
      if (role === "tool") {
        const id = (m.tool_call_id || "").trim();
        if (!id) continue;
        const idx = toolIdx.get(id);
        if (idx === undefined) {
          const it: Extract<TranscriptItem, { type: "tool_call" }> = {
            id: stableToolCallItemId(id),
            type: "tool_call",
            toolCallId: id,
            status: "completed",
            resultText: m.content || "",
          };
          toolIdx.set(id, next.length);
          next.push(it);
          continue;
        }
        const cur = next[idx] as Extract<TranscriptItem, { type: "tool_call" }>;
        next[idx] = {
          ...cur,
          status: "completed",
          resultText: m.content || "",
        };
      }
    }
    // Notices of the last turn, and any the history no longer reaches.
    next.push(...notices.end());

    // Enrich tool calls with persisted previews when available.
    const tcRes = await fetchJSON<{ toolCalls: ToolCallListRow[] }>(
      `/coddy/sessions/${encodeURIComponent(sid)}/tool-calls`,
      {
        headers: sid === sessionId ? headers : { [HDR]: sid },
      },
    );
    if (tcRes.ok && tcRes.data?.toolCalls) {
      for (const row of tcRes.data.toolCalls) {
        const id = (row.toolCallId || "").trim();
        if (!id) continue;
        const idx = toolIdx.get(id);
        if (idx === undefined) continue;
        const cur = next[idx] as Extract<TranscriptItem, { type: "tool_call" }>;
        const title = (row.name || cur.title || "").trim() || undefined;
        const kind = (row.kind || cur.kind || "").trim() || undefined;
        const status = (row.status as any) || cur.status;
        const merged: Extract<TranscriptItem, { type: "tool_call" }> = {
          ...cur,
          status,
        };
        if (title) merged.title = title;
        if (kind) merged.kind = kind;
        if (row.argsPreview) {
          const titleLower = (title || "").trim().toLowerCase();
          const pickedArgs =
            titleLower === "question"
              ? pickRicherQuestionToolArgs(cur.argsText, row.argsPreview)
              : pickRicherToolArgs(cur.argsText, row.argsPreview);
          if (pickedArgs) merged.argsText = pickedArgs;
        }
        if (row.resultPreview) merged.resultText = row.resultPreview;
        if (row.resultPreviewTruncated === true)
          merged.resultWasTruncated = true;
        const todoPlan = normalizeTodoPlanSnapshot(row.planSnapshot);
        if (todoPlan !== undefined) merged.todoPlan = todoPlan;
        const st = parseRFC3339ms(row.startedAt);
        const fin = parseRFC3339ms(row.finishedAt);
        if (st != null && fin != null && fin >= st) {
          merged.durationMs = fin - st;
        }
        next[idx] = merged;
      }
    }
    if (!sameStream()) return null;
    const prevShadow = streamShadowBySidRef.current.get(sid);
    // freshLoad: don't inherit stale items from a previous session (e.g. when first loading a session).
    const localForMerge = opts?.freshLoad
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
    revokeSupersededUserMessagePreviews(mergedTranscript, localForMerge);
    const mergedBase = preserveUserMessageFiles(
      mergedTranscript,
      localForMerge,
    );
    let merged = reattachLocalQuestionPrompts(mergedBase, localForMerge);
    merged = mergePermissionPromptsIntoTranscript(
      merged,
      sid,
      toolsPermissionPolicyRef.current,
    );
    merged = mergeStoredQuestionPromptsIntoTranscript(merged, sid);
    merged = patchQuestionToolArgsFromPromptRecords(merged, sid);
    const appliedRaw =
      keepLocalTranscriptIfServerEmpty({
        serverNext: merged,
        sid,
        viewingSid: viewingNow,
        prevShadow,
        prevItems: itemsRef.current,
      }) ?? merged;
    const withStableIds = preserveTranscriptItemIds(
      appliedRaw,
      localForMerge ?? prevShadow ?? itemsRef.current,
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
    const noteSnapshotRev = (items: readonly TranscriptItem[]) => {
      if (serverOnly && typeof snapshotRev === "number") {
        snapshotRevByTranscript.current.set(items, snapshotRev);
      }
    };
    if (opts?.skipSetItems) {
      streamShadowBySidRef.current.set(sid, applied);
      evictStaleSessionCaches(viewedSessionIdRef.current);
      noteSnapshotRev(applied);
      return applied;
    }

    if (!sameStream()) return null;
    // Frames may have arrived while the messages request was in flight.
    const finalItems = mergeTranscriptPreferLocalSuffix(
      applied,
      streamShadowBySidRef.current.get(sid),
    );
    if (finalItems === applied) noteSnapshotRev(finalItems);
    streamShadowBySidRef.current.set(sid, finalItems);
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
    openSessionFromRoute(id, { historySidebar: sessionsOpen });
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

  function goHome() {
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
    // Drop any stashed session selection so its restore effect cannot reapply
    // the old session's model over the new chat default.
    setOpenSessionSelection(null);
    // A new chat runs under the configured permission mode until it is changed.
    settingsVersionRef.current = { sid: "", version: 0 };
    pendingPermissionModeRef.current = "";
    setPermissionMode(configuredPermissionMode);
    setSettingsOverrides([]);
    if (llmModelIds.length > 0) {
      setLlmModel(
        pickDefaultLlmModelForNewChat({
          backends: llmModelIds,
          cookie: readLlmModelCookie(),
        }),
      );
    }
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
      setEditingUserMsgIdx(null);
      setEditingAssetNote("");
      setEditingFiles([]);
      showRewindError(
        `Rewind applied but reloading failed: ${err instanceof Error ? err.message : String(err)}`,
      );
      return;
    }
    setDraft("");
    setEditingUserMsgIdx(null);
    setEditingAssetNote("");
    setEditingFiles([]);
    void streamResponses(text);
  }

  useEffect(() => {
    setEditingUserMsgIdx(null);
    setEditingAssetNote("");
    setEditingFiles([]);
    setSubagentTranscript(null);
    setViewedArchived(false);
    if (!sessionId) {
      setItems([]);
      setDraft("");
      setSessionLoading(false);
      void loadSessionsList(true);
      return;
    }
    setDraft("");
    setTokenUsage(null);
    setContextBreakdown(null);
    tokenBaselineRef.current = { input: 0, output: 0, total: 0 };
    const lifecycle = new AbortController();
    void (async () => {
      const list = await loadSessionsList(true);
      if (lifecycle.signal.aborted) {
        return;
      }
      // A session spawned by another one is hidden from History and nothing
      // about its id sets it apart, so an id History does not carry is still
      // fetched: the messages endpoint serves it and marks it read-only, and
      // answers 404 when it names nothing.
      const listed = !!list?.some((s) => s.id === sessionId);
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
          setSessionLoading(false);
        } else {
          // freshLoad when no shadow: prevents stale itemsRef from a previous session
          // bleeding into this session (e.g. React StrictMode double-invoke of effects).
          const noShadow = !shadowSnap || shadowSnap.length === 0;
          const loaded = await loadMessages(undefined, { freshLoad: noShadow });
          if (lifecycle.signal.aborted) {
            return;
          }
          if (
            loaded === null &&
            !listed &&
            viewedSessionIdRef.current.trim() === sessionId
          ) {
            // An id the server does not serve: nothing to keep a skeleton up
            // for, so it lands on the empty state like any unknown id.
            setSessionLoading(false);
          }
          if (activeComposerSidRef.current.has(sessionId)) {
            const sh = streamShadowBySidRef.current.get(sessionId);
            if (sh && sh.length > 0) {
              setItems([...sh]);
              setSessionLoading(false);
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
            `/coddy/sessions/${encodeURIComponent(key)}/messages`,
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
    const ownsPost = () =>
      postAbortBySidRef.current.get(postSessionKey) === abortCtl;

    try {
      let sid = sessionId;
      if (!sid) {
        sid = randomSessionId();
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
                      reason:
                        errorDetail(reader.error) || "NotReadableError",
                    };
                    resolve(null);
                  };
                  reader.readAsDataURL(f);
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
      if (yamlSel || reasoningSel || runSlug || heldVersion > 0) {
        const meta: Record<string, string> = {};
        if (yamlSel) meta.model = yamlSel;
        if (reasoningSel) meta.reasoning = reasoningSel;
        if (runSlug) meta.runPlanSlug = runSlug;
        if (heldVersion > 0) meta.settingsVersion = String(heldVersion);
        reqBody.metadata = meta;
      }
      // A permission mode picked before the chat had a session goes first,
      // as the command that asks for it; the server takes it off the text.
      if (!sid.trim() && pendingPermissionModeRef.current) {
        reqBody.input = `/permissions ${pendingPermissionModeRef.current}\n${text}`;
        pendingPermissionModeRef.current = "";
      }
      if (!ownsPost() || abortCtl.signal.aborted) return;
      const res = await fetch("/v1/responses", {
        method: "POST",
        headers: { ...hdrs, "Content-Type": "application/json" },
        body: JSON.stringify(reqBody),
        signal: abortCtl.signal,
      });
      responded = true;
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
                .then(
                  (b: { error?: { message?: unknown } }) =>
                    typeof b?.error?.message === "string"
                      ? b.error.message
                      : "",
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
      // conversation. The transcript's log holds the notice, which the reload
      // below renders in the place a reload of the page would.
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
            `/coddy/sessions/${encodeURIComponent(sidEffective)}/messages`,
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

  const llmReasoningLevels = useMemo(() => {
    const row = modelInfos.find((m) => m.id === llmModel);
    return row?.reasoningLevels ?? [];
  }, [modelInfos, llmModel]);

  // Keep the selected reasoning level valid for the current model: keep the user's
  // pick when the new model still offers it, else fall back (cookie -> model default).
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
    setLlmReasoning((prev) =>
      pickReasoningLevel({
        levels,
        cookie: readReasoningCookie(),
        sessionLevel: prev,
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
    setLlmReasoning(snap.reasoning);
  });

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
        // No session yet: the choice rides in with the first message.
        pendingPermissionModeRef.current =
          pm === configuredPermissionMode ? "" : pm;
        return;
      }
      void patchSessionSettings(sid, { permissionMode: pm });
    },
    [sessionId, configuredPermissionMode, patchSessionSettings],
  );

  const onLlmReasoningChange = useCallback(
    (level: string) => {
      const lv = level.trim();
      if (!lv) {
        return;
      }
      setLlmReasoning(lv);
      writeReasoningCookie(lv);
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
    setSchedulerEditor({ mode: "runs", jobId: schedulerRunsJobId, taskId: null });
    setSchedulerJobRunsHash(schedulerRunsJobId);
    void refreshSchedulerRuns({ silent: true });
    void refreshSchedulerJobs({ silent: true });
  }, [schedulerRunsJobId, refreshSchedulerRuns, refreshSchedulerJobs]);

  const openSchedulerFromNav = useCallback(() => {
    if (schedulerHttpLinked !== true) {
      return;
    }
    setSessionsOpen(false);
    setTasksOpen(false);
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
    setSessionTasksHash(sid);
  }, [sessionId]);

  const closeTasksDrawer = useCallback(() => {
    setTasksOpen(false);
    // A pointer at a task that never showed up does not wait for the next opening.
    setTasksFocus(null);
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

  // "Open in Tasks" on a transcript row: the panel opens with that task's card open.
  const openBackgroundTask = useCallback(
    (taskId: string) => {
      const sid = sessionId.trim();
      if (!sid) {
        return;
      }
      setTasksOpen(true);
      focusBackgroundTask(sid, taskId);
      setSessionTasksHash(sid);
    },
    [sessionId, focusBackgroundTask],
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
      setIsSwarmEnv(!!info);
      setAtSwarmRoot(!!info);
    });
    return () => ac.abort();
  }, []);

  // Entering a node points the whole app at that node's mount, so every screen
  // that already existed works against it with a relay in the middle.
  const openSwarmNode = useCallback((nodePath: string[], hash?: string) => {
    const env = getEnv();
    // Served by the relay from its own root, the environment is plain
    // same-origin: the relay is then this page's origin. Without that fallback
    // the one entry point this screen exists for silently did nothing.
    const relay =
      env.mode === "remote"
        ? (env.swarmRelay ?? env.baseUrl)
        : window.location.origin;
    if (!relay) {
      return;
    }
    connectSwarmNode(
      relay,
      nodePath,
      env.mode === "remote" ? env.token : "",
      hash,
    );
  }, []);

  /**
   * Where the app has been in this swarm, as a route.
   *
   * Inside a node it is that node; back on the relay it is whatever
   * `returnToSwarm` remembered. The map marks it and draws the path to it, so
   * the screen can say where we are rather than only what exists.
   */
  const swarmCurrentNode = useMemo(() => {
    const env = getEnv();
    if (env.mode !== "remote") {
      return [] as string[];
    }
    const route = env.swarmNode || env.swarmFrom || "";
    return route.split("/").filter(Boolean);
  }, []);

  const openSwarmFromNav = useCallback(() => {
    const env = getEnv();
    // Inside a node, going to the swarm means going back out to its relay.
    if (env.mode === "remote" && env.swarmRelay) {
      returnToSwarm();
      return;
    }
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    setSessionsOpen(false);
    setSettingsRoute(false);
    window.location.hash = appNavHrefSwarm();
  }, []);

  /** Opens the reader over whatever is on screen, remembering it for the close. */
  const openDocsAt = useCallback((slug: string | null, anchor: string | null) => {
    if (parseAppHash().branch !== "docs") {
      docsReturnHashRef.current = window.location.hash;
    }
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    setSessionsOpen(false);
    setSettingsRoute(false);
    window.location.hash = appNavHrefDocs(slug, anchor);
  }, []);

  const openDocsFromNav = useCallback(() => {
    openDocsAt(lastDocsSlugRef.current, null);
  }, [openDocsAt]);

  // A search /docs <words> brings into the reader; cleared when the reader closes.
  const [docsSearchSeed, setDocsSearchSeed] = useState<{ query: string; nonce: number } | null>(
    null,
  );

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
      void fetchDocsPage(arg).then((res) => {
        if (res.ok && docsCommandOpensPage(arg, res.data.title)) {
          setDocsSearchSeed(null);
          openDocsAt(res.data.slug, res.data.anchor || null);
          return;
        }
        setDocsSearchSeed({ query: arg, nonce: Date.now() });
        openDocsFromNav();
      });
    },
    [openDocsAt, openDocsFromNav],
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
    setTasksOpen(false);
    setSessionsOpen(false);
    setSettingsHash();
  }, []);

  const onCloseSettings = useCallback(() => {
    const sid = sessionId.trim();
    if (sid) {
      setSessionHashInLocation(sid);
    } else {
      clearSessionRoute();
    }
  }, [sessionId, clearSessionRoute]);

  const onOpenHistoryFromNav = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    setSettingsRoute(false);
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

  const shellBackdropOpen =
    sessionsOpen ||
    (schedulerOpen && schedulerHttpLinked === true) ||
    settingsRoute ||
    swarmRoute ||
    docsRoute !== null;

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

  // Read the environments this server offers once: the list lives in the local
  // config, so it is fetched off the origin rather than through the shim.
  useEffect(() => {
    let alive = true;
    localFetch("/coddy/config")
      .then((r) => (r.ok ? r.json() : null))
      .then((cfg) => {
        if (!alive || !cfg) {
          return;
        }
        const list = (cfg as Record<string, unknown>)?.httpserver as
          | Record<string, unknown>
          | undefined;
        const raw = list?.remotes;
        if (!Array.isArray(raw)) {
          return;
        }
        setConfiguredRemotes(
          raw
            .map((item) => {
              const o = (item ?? {}) as Record<string, unknown>;
              return { name: String(o.name ?? ""), url: String(o.url ?? "") };
            })
            .filter((r) => r.url.trim() !== ""),
        );
      })
      .catch(() => {
        /* configured remotes are optional */
      });
    return () => {
      alive = false;
    };
  }, []);

  const activeEnv = useSyncExternalStore(
    subscribeEnv,
    snapshotEnv,
    snapshotEnv,
  );

  /**
   * The environments the History filter offers. Two kinds share the list,
   * because to an operator they are one question - where is this conversation:
   * the origin rows narrow the listing of whichever server is being read, and a
   * remote row points the whole app at another server, the way the composer's
   * environment chip does.
   */
  const sessionEnvironments = useMemo<SessionsEnvironmentOption[]>(() => {
    const onRemote = activeEnv.mode === "remote";
    // Narrowing by origin is a filter on the server being read; it must not
    // reach for connectLocal, which reloads the page and would throw the choice
    // away before it was used. Only coming *back* from a remote is a switch,
    // and that reload resets the filter along with everything else.
    const narrowTo = (origin: SessionOriginFilter) => () => {
      if (onRemote) {
        connectLocal();
        return;
      }
      setSessionsOrigin(origin);
      writeSessionPref(SESSION_PREF_COOKIES.origin, origin);
    };
    const rows: SessionsEnvironmentOption[] = [
      {
        key: "all",
        label: t("sessions.filter.env.all"),
        active: !onRemote && sessionsOrigin === "",
        onPick: narrowTo(""),
      },
      {
        key: "local",
        label: t("sessions.filter.env.local"),
        active: !onRemote && sessionsOrigin === "local",
        onPick: narrowTo("local"),
      },
      {
        key: "gateway",
        label: t("sessions.filter.env.gateway"),
        active: !onRemote && sessionsOrigin === "gateway",
        onPick: narrowTo("gateway"),
      },
    ];
    for (const remote of configuredRemotes) {
      rows.push({
        key: remote.url,
        label: remote.name.trim() || remote.url,
        active:
          onRemote && activeEnv.baseUrl === remote.url.replace(/\/+$/, ""),
        // connectRemote reloads the page, so nothing of this session's state
        // reaches the other server - the origin filter included.
        onPick: () =>
          connectRemote(remote.url, getRemoteToken(remote.url), remote.name),
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
      setDraft(stripCoddyAttachmentsForUserDisplay(content));
      setEditingUserMsgIdx(userMsgIdx);
      setEditingAssetNote(assetNote);
      setEditingFiles(parseSessionAssetFiles(content));
    },
  );
  const handleStopBackgroundTask = useStableHandler((id: string) => {
    void stopBackgroundTaskById(id);
  });

  /**
   * Queue the draft for the turn that is running instead of refusing it.
   *
   * The turn can end between the keystroke and the request; the server says so
   * with `no_active_turn`, and what the operator wrote is sent as an ordinary
   * prompt rather than dropped. Any other refusal puts the text back in the
   * composer, because losing it is worse than a second attempt.
   */
  const handleQueueMessage = useStableHandler((text: string) => {
    const sid = sessionId.trim();
    const generation = turnActivity.generation(sid);
    const queueEpoch = queueOrderRef.current.capture(sid).epoch;
    const body = text.trim();
    if (!sid || !body) return;
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
        const res = await fetch(
          `/coddy/sessions/${encodeURIComponent(sid)}/queue`,
          {
            method: "POST",
            headers: { [HDR]: sid, "Content-Type": "application/json" },
            body: JSON.stringify({ text: body }),
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
        void streamResponses(body, { restoreOnRefusal: true });
        return;
      }
      if (viewedSessionIdRef.current.trim() === sid)
        setDraft((current) => current || body);
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
  });

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
        } | null;
        if (Array.isArray(data?.messages)) {
          applyQueue(sid, data.messages, data.version ?? 0, queueEpoch);
        }
        // Taken back before the agent read it: the text returns to the composer to be
        // edited, ahead of anything typed since. A 404 means the agent read it first,
        // and it is already in the conversation.
        const text = taken?.text ?? "";
        if (res.ok && text.trim() && viewedSessionIdRef.current.trim() === sid) {
          setDraft((current) =>
            current.trim() ? `${text}\n\n${current}` : text,
          );
        }
      } catch {
        // The next message_queue frame corrects the list.
      }
    })();
  });
  const handleRetryLast = useStableHandler(
    () => void streamResponses(lastUserText),
  );
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
        onNewChat={goHome}
        onOpenHistory={onOpenHistoryFromNav}
        historyOpen={sessionsOpen}
        showHistory={!atSwarmRoot}
        showScheduler={schedulerHttpLinked === true && !atSwarmRoot}
        onOpenScheduler={openSchedulerFromNav}
        schedulerOpen={schedulerOpen}
        showSwarm={isSwarmEnv}
        onOpenSwarm={openSwarmFromNav}
        swarmOpen={swarmRoute}
        onOpenDocs={openDocsFromNav}
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
          tasksPanelOpen ? "shell-tasks-open" : "",
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
            if (shellBackdropOpen) {
              closeAllShellDrawers();
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
                schedulerEditor?.mode === "edit" || schedulerEditor?.mode === "runs"
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
                title={t("scheduler.runsTitle", { jobId: schedulerEditor.jobId })}
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
              onClose={() => {
                setSchedulerEditor(null);
                setSchedulerListHash();
              }}
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
              onOpenNode={(nodePath: string[]) => openSwarmNode(nodePath)}
              onOpenSession={(s) => openSwarmNode(s.node_path, `#/s/${s.id}`)}
              {...(swarmCurrentNode.length > 0
                ? { currentNode: swarmCurrentNode }
                : {})}
              {...(atSwarmRoot ? { headerSlot: <EnvironmentChip /> } : {})}
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
              onClose={onCloseSettings}
              onConfigSaved={() => setConfigEpoch((e) => e + 1)}
              initialSection={settingsSection}
              initialItem={settingsItem}
              activeSessionId={sidebarActiveId}
              onSessionsDeleted={onSessionsDeletedInSettings}
              // spawn_agent resolves definitions against the session's own
              // cwd: the viewed session's workspace is the one the Subagents
              // tab lists.
              workspacePath={workspaceCtx?.path || undefined}
              onSessionTagsChanged={(id: string, tags: string[]) =>
                setSessions((prev) =>
                  prev.map((s) => (s.id === id ? { ...s, tags } : s)),
                )
              }
            />
          </div>
        ) : null}
        {tasksPanelOpen ? (
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

        {atSwarmRoot ? null : (
          <ChatScreen
            title={currentTitle}
            sessionId={sessionId}
            backgroundTasks={backgroundTasks}
            onOpenBackgroundTasks={openTasksFromNav}
            onBackgroundTasksChanged={() => {
              void refreshBackgroundTasks({ silent: true });
            }}
            backgroundTasksByToolCallId={backgroundTasksByToolCallId}
            backgroundNowMs={backgroundNowMs}
            onOpenBackgroundTask={openBackgroundTask}
            onStopBackgroundTask={handleStopBackgroundTask}
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
            worktreePref={worktreePref}
            workspaceLocked={items.length > 0}
            onWorkspacePickFolder={(p: string) =>
              void switchWorkspace({ path: p })
            }
            onWorkspacePickBranch={(b: string, wt: boolean) =>
              void switchWorkspace({ branch: b, worktree: wt })
            }
            onWorktreeToggle={() => setWorktreePref((v) => !v)}
            sessionLoading={sessionLoading}
            sessionFadingOut={sessionFadingOut}
            heroAccentVerb={heroAccentVerb}
            heroComposerFocusEpoch={heroHomeGeneration}
            onTitleSave={(t: string) => void saveSessionTitle(sessionId, t)}
            items={items}
            draft={draft}
            tokenUsage={tokenUsage}
            providerUsage={providerUsageState.usage}
            usageBannerDismissedKey={providerUsageState.dismissedKey}
            onUsageBannerDismiss={providerUsageState.dismissBanner}
            contextPct={contextPct}
            maxContextTokens={maxContextTokens}
            contextBreakdown={contextBreakdown}
            mode={mode}
            modes={[...PROFILE_MODES]}
            {...(llmModelIds.length > 0
              ? {
                  llmModels: llmModelIds,
                  llmModel,
                  onLlmModelChange,
                  llmModelMultimodal,
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
            permissionMode={permissionMode}
            configuredPermissionMode={configuredPermissionMode}
            onPermissionModeChange={
              subagentTranscript ? undefined : onPermissionModeChange
            }
            settingsOverrides={settingsOverrides}
            onDraftChange={setDraft}
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
            {...(knownSkillNames.size > 0 ? { knownSkillNames } : {})}
            onDocsCommand={openDocsCommand}
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
