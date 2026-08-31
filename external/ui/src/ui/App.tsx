import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { CSSProperties } from "react";
import { ChatScreen } from "./chat/ChatScreen";
import { useStableHandler } from "./components/useStableHandler";
import {
  contextUsagePercent,
  withContextUsedTokens,
} from "./chat/contextUsage";
import { HERO_ACCENT_VERBS, pickHeroAccentVerb } from "./chat/heroTitleWords";
import { insertNewThinkingBeforeStreamingAssistant } from "./chat/transcriptThinkingPlacement";
import { openAIStreamErrorMessage } from "./chat/streamError";
import { optimisticUserFiles } from "./chat/optimisticUserFiles";
import { sessionMessageFiles } from "./chat/sessionMessageFiles";
import { getEnv } from "./env/remoteEnv";
import {
  isAbortError,
  remoteHttpErrorMessage,
  remoteSendErrorMessage,
} from "./env/remoteErrors";
import { EnvHealthBanner } from "./env/EnvHealthBanner";
import { isNoLiveTurnRelayError } from "./chat/composerStreamError";
import { subscribeServerEvents } from "./chat/serverEvents";
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
} from "./chat/permissionPromptSessionStore";
import {
  parseToolsPermissionPolicy,
  type ToolsPermissionPolicy,
} from "./chat/toolsPermissionPolicy";
import { reattachLocalQuestionPrompts } from "./chat/transcriptQuestionReattach";
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
import { stableMemoryCopilotItemId } from "./chat/memoryStableId";
import type { TokenUsage, TranscriptItem } from "./chat/types";
import { useWorkspace } from "./chat/useWorkspace";
import {
  injectBranchNavItems,
  deduplicateBranchNavs,
  type BranchPointData,
} from "./chat/branchInject";
import { resolveLatestLeaf } from "./chat/resolveLatestLeaf";
import { NavRail } from "./nav/NavRail";
import { useShellLayout } from "./nav/useShellLayout";
import { useLlmModelSelection } from "./chat/useLlmModelSelection";
import { SessionsSidebar } from "./sessions/SessionsSidebar";
import { useSessionsList } from "./sessions/useSessionsList";
import { useConfirm } from "./components/useConfirm";
import { useT } from "./i18n/I18nProvider";
import type { SessionRow } from "./sessions/types";
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
  parseAppHash,
  setDraftHashInLocation,
  setHistoryHash,
  setSessionHashInLocation,
  schedulerEditorFromParsedHash,
  setSchedulerListHash,
  setSessionTasksHash,
  setSettingsHash,
  stripHistorySidebarFromHash,
} from "./scheduler/hashRoute";
import { SchedulerDockCluster } from "./scheduler/SchedulerDockCluster";
import { useSchedulerDockWidth } from "./scheduler/useSchedulerDockWidth";
import { useSchedulerJobs } from "./scheduler/useSchedulerJobs";
import { BackgroundTasksPanel } from "./tasks/BackgroundTasksPanel";
import { useBackgroundTasks } from "./tasks/useBackgroundTasks";
import {
  isSubagentSessionId,
  parseSubagentTranscriptMeta,
  type SubagentTranscriptMeta,
} from "./chat/subagentTranscript";
import { Settings } from "./settings/Settings";
import {
  HDR,
  fetchJSON,
  markCoddySessionActivityRead,
  newId,
  randomSessionId,
} from "./coddyApi";
import {
  applyMemoryChunkToItems,
  applyMemoryPhaseToItems,
  freezeMemoryWallWhenThinkingAfterRecall,
  memoryTranscriptFromApi,
  type MemoryChunkEvt,
  type MemoryPhaseEvt,
  type MemoryTurnApi,
} from "./chat/memoryTranscript";
import {
  readMessageCreatedAtUTC,
  toolSseShowsTruncatedPreview,
  type ToolCallListRow,
  type ToolCallStatusUpdate,
  type ToolCallUpdate,
} from "./chat/toolCallStream";
import {
  parseRFC3339ms,
  reasoningDurationCacheKey,
} from "./chat/reasoningTiming";

const PROFILE_MODES = ["agent", "plan", "ask"] as const;

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

export function App() {
  const { t } = useT();
  const confirm = useConfirm();
  const [knownSkillNames, setKnownSkillNames] = useState<Set<string>>(
    () => new Set(),
  );
  const [sessionId, setSessionId] = useState("");
  /** Increments on each explicit "new chat" home transition so the hero verb rotates. */
  const [heroHomeGeneration, setHeroHomeGeneration] = useState(() =>
    Math.floor(Math.random() * HERO_ACCENT_VERBS.length),
  );
  const headers = useMemo(
    () => (sessionId ? { [HDR]: sessionId } : {}),
    [sessionId],
  );
  const {
    sessions,
    setSessions,
    sessionsError,
    loadSessionsList,
    sessionFilterDraft,
    setSessionFilterDraft,
    sessionFilterQ,
    sessionsHasMore,
    sessionsLoadingMore,
  } = useSessionsList({ headers });
  const [items, setItems] = useState<TranscriptItem[]>([]);
  const [sessionLoading, setSessionLoading] = useState(false);
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
  const pendingBranchSendRef = useRef<{ text: string; sid: string } | null>(
    null,
  );
  // Sessions explicitly chosen via branch nav — skip resolveLatestLeaf for these.
  const skipLeafResolveRef = useRef<Set<string>>(new Set());
  const [draft, setDraft] = useState("");
  const {
    workspaceCtx,
    worktreePref,
    setWorktreePref,
    switchWorkspace,
    applyPendingWorkspace,
  } = useWorkspace({ sessionId });
  const [clientDraftSessions, setClientDraftSessions] = useState<
    ClientDraftSession[]
  >(() => readClientDraftSessions());
  const [activeDraftId, setActiveDraftId] = useState("");
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
      const statsRes = await fetchJSON<{ stats?: SessionStats | null }>(
        `/coddy/sessions/${encodeURIComponent(key)}/stats`,
        { headers: { [HDR]: key } },
      );
      if (!statsRes.ok) {
        return;
      }
      applySessionStatsPayload(
        statsRes.data?.stats,
        viewedSessionIdRef.current.trim() === key,
      );
    },
    [applySessionStatsPayload],
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
  /** Last composer relay frame id seen per session, so a re-attach can resume from it. */
  const relayLastEventIdBySidRef = useRef<Map<string, string>>(new Map());
  const streamingAssistantBySidRef = useRef<Map<string, string>>(new Map());
  /** Session ids with an active client-side composer POST or GET relay. */
  const activeComposerSidRef = useRef<Set<string>>(new Set());
  const [composerActivityEpoch, setComposerActivityEpoch] = useState(0);
  /** Session id currently shown in the transcript (updated synchronously on navigation). */
  const viewedSessionIdRef = useRef("");
  /** True while GET /coddy/events is connected; gates the fallback sessions poll. */
  const [serverEventsConnected, setServerEventsConnected] = useState(false);
  const serverEventHandlersRef = useRef<{
    turnStarted: (sid: string) => void;
    turnEnded: (sid: string) => void;
  }>({ turnStarted: () => {}, turnEnded: () => {} });
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

  const generating = useMemo(() => {
    const sid = sessionId.trim();
    if (!sid) return false;
    return activeComposerSidRef.current.has(sid);
  }, [sessionId, composerActivityEpoch]);

  // Text of the most recent user turn, used to re-run it from the retry button
  // on a failed/system notice (e.g. "model did not respond").
  const lastUserText = useMemo(() => {
    for (let i = items.length - 1; i >= 0; i--) {
      const it = items[i];
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

  const sessionsForSidebar = useMemo(
    () => mergeSessionsWithDrafts(sessions, clientDraftSessions),
    [sessions, clientDraftSessions, t],
  );

  const reasoningDurationMsByContentRef = useRef<Map<string, number>>(
    new Map(),
  );
  const [sessionsOpen, setSessionsOpen] = useState(false);
  const {
    schedulerHttpLinked,
    schedulerOpen,
    setSchedulerOpen,
    schedulerEditor,
    setSchedulerEditor,
    schedulerInfo,
    filteredSchedulerJobs,
    schedulerListError,
    schedulerListLoading,
    schedulerFilterDraft,
    setSchedulerFilterDraft,
    refreshSchedulerJobs,
    onSchedulerRunJob,
    onSchedulerCancelJob,
  } = useSchedulerJobs({ sessionId });
  const { clusterRef: schedulerDockClusterRef, widthPx: schedDockClusterWidthPx } =
    useSchedulerDockWidth({
      open: schedulerOpen,
      httpLinked: schedulerHttpLinked,
      editor: schedulerEditor,
    });
  const [settingsRoute, setSettingsRoute] = useState(false);
  // Active Settings section id from `#/settings/<section>` (null = default/grid).
  const [settingsSection, setSettingsSection] = useState<string | null>(null);
  const {
    tasksOpen,
    setTasksOpen,
    tasksSelectedId,
    setTasksSelectedId,
    backgroundTasks,
    backgroundOutput,
    backgroundListError,
    backgroundListLoading,
    backgroundNowMs,
    backgroundTasksByToolCallId,
    refreshBackgroundTasks,
    stopBackgroundTaskById,
    clearFinishedTasks,
  } = useBackgroundTasks({ sessionId });
  /** Set while the viewed session is a subagent's transcript (read-only, no composer). */
  const [subagentTranscript, setSubagentTranscript] =
    useState<SubagentTranscriptMeta | null>(null);
  const { viewportXL, railLabelsWide, toggleRailWidth } = useShellLayout();
  const [mode, setMode] = useState<string>("agent");
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
        return [
          ...withoutDup,
          {
            id: `qp_${ridInner}`,
            type: "question_prompt" as const,
            payload: p,
          },
        ];
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
    // A child session has no History row to name it, so name it by its role.
    if (subagentTranscript) {
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


  const {
    llmModelIds,
    llmModel,
    llmReasoning,
    maxContextTokens,
    llmModelMultimodal,
    llmReasoningLevels,
    onLlmModelChange,
    onLlmReasoningChange,
    setOpenSessionSelection,
    resetForNewChat: resetLlmSelectionForNewChat,
    bumpModelsEpoch,
  } = useLlmModelSelection({ sessionId, headers, viewedSessionIdRef });

  const applyLocationHash = useCallback(() => {
    const p = parseAppHash();
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
      setTasksSelectedId(p.taskId);
      setSessionsOpen(!!p.historyOpen);
      return;
    }
    if (p.branch === "draft") {
      setSettingsRoute(false);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setTasksOpen(false);
      setTasksSelectedId(null);
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
      setTasksSelectedId(null);
      return;
    }
    if (p.branch === "settings") {
      setSettingsRoute(true);
      setSettingsSection(p.section);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setTasksOpen(false);
      setTasksSelectedId(null);
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
      setTasksSelectedId(null);
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
    setTasksSelectedId(null);
    setSessionsOpen(!!p.historyOpen);
  }, [schedulerHttpLinked]);

  const openSessionFromRoute = useCallback(
    (id: string, opts?: { historySidebar?: boolean }) => {
      setActiveDraftId("");
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setTasksOpen(false);
      setTasksSelectedId(null);
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
    setTasksSelectedId(null);
    viewedSessionIdRef.current = "";
    setSessionHashInLocation("");
    setSessionId("");
    setActiveDraftId("");
  }, []);

  const closeSchedulerDrawer = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    setTasksSelectedId(null);
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
    setTasksSelectedId(null);
    if (parseAppHash().branch === "settings") {
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
    if (prevSessionsOpenRef.current && !sessionsOpen) {
      requestAnimationFrame(() => {
        document.getElementById("composer")?.focus();
      });
    }
    prevSessionsOpenRef.current = sessionsOpen;
  }, [sessionsOpen]);

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
  }, []);

  useEffect(() => {
    setDescribePreview((p) => (p && p.sessionId !== sessionId ? null : p));
  }, [sessionId]);

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
    turnStarted: (sid: string) => {
      void loadSessionsList(true);
      const key = sid.trim();
      if (!key || key !== viewedSessionIdRef.current.trim()) return;
      if (activeComposerSidRef.current.has(key)) return;
      void (async () => {
        const loaded = await loadMessages(key, { preserveOnError: true });
        if (!loaded || activeComposerSidRef.current.has(key)) return;
        await rejoinComposerLiveStream(key, loaded);
      })();
    },
    turnEnded: (sid: string) => {
      void loadSessionsList(true);
      const key = sid.trim();
      if (!key || key !== viewedSessionIdRef.current.trim()) return;
      if (activeComposerSidRef.current.has(key)) return;
      void loadMessages(key, { preserveOnError: true });
      void refreshSessionStats(key);
    },
  };

  useEffect(() => {
    const ctl = new AbortController();
    void subscribeServerEvents({
      onTurnStarted: (sid) => serverEventHandlersRef.current.turnStarted(sid),
      onTurnEnded: (sid) => serverEventHandlersRef.current.turnEnded(sid),
      onConnectedChange: setServerEventsConnected,
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
    const res = await fetchJSON<{
      messages: Array<any>;
      model?: string;
      selectedModelId?: string;
      selectedReasoning?: string;
      memoryTurns?: MemoryTurnApi[];
      subagent?: {
        parentSessionId?: string;
        name?: string;
        taskId?: string;
      } | null;
      readOnly?: boolean;
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
      // A child session locks the composer; an ordinary one carries no marker.
      setSubagentTranscript(parseSubagentTranscriptMeta(res.data));
    }
    type UILogRow = {
      id: string;
      level: string;
      message: string;
      createdAt: string;
    };
    const noticesByTurn = new Map<number, UILogRow[]>();
    for (const raw of res.data.uiLog || []) {
      const msg = typeof raw.message === "string" ? raw.message.trim() : "";
      if (!msg) continue;
      const turn =
        typeof raw.userTurnIndex === "number" &&
        Number.isFinite(raw.userTurnIndex) &&
        raw.userTurnIndex >= 1
          ? Math.floor(raw.userTurnIndex)
          : 1;
      const id =
        typeof raw.id === "string" && raw.id.trim() !== ""
          ? raw.id.trim()
          : newId("s");
      const level = (raw.level || "error").trim() || "error";
      const createdAt = typeof raw.createdAt === "string" ? raw.createdAt : "";
      const row: UILogRow = { id, level, message: msg, createdAt };
      const bucket = noticesByTurn.get(turn) ?? [];
      bucket.push(row);
      noticesByTurn.set(turn, bucket);
    }
    for (const [turn, bucket] of noticesByTurn) {
      bucket.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
      noticesByTurn.set(turn, bucket);
    }

    const memByTurn = new Map<number, MemoryTurnApi>();
    for (const row of res.data.memoryTurns || []) {
      if (typeof row.userTurnIndex === "number" && row.userTurnIndex > 0) {
        memByTurn.set(row.userTurnIndex, row);
      }
    }
    const next: TranscriptItem[] = [];
    const pushUiNoticesForTurn = (turn: number) => {
      for (const row of noticesByTurn.get(turn) || []) {
        if (row.level !== "error") continue;
        next.push({
          id: row.id,
          type: "system_notice",
          level: "error",
          message: row.message,
          createdAtUtc: row.createdAt,
        });
      }
    };
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
        // Flush notices for the previous turn before starting a new one so
        // error notices land at the end of the turn they belong to, not at
        // the top of the next one.
        if (userTurnIdx > 0) {
          pushUiNoticesForTurn(userTurnIdx);
        }
        userTurnIdx++;
        thinkingInTurn = 0;
        assistantInTurn = 0;
        const cat = readMessageCreatedAtUTC(m as Record<string, unknown>);
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
        const mt = memByTurn.get(userTurnIdx);
        if (mt) {
          next.push(memoryTranscriptFromApi(mt));
        }
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
        if (content) {
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
    // Flush notices for the last turn (no subsequent user message to trigger it).
    pushUiNoticesForTurn(userTurnIdx);

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
    const prevShadow = streamShadowBySidRef.current.get(sid);
    // freshLoad: don't inherit stale items from a previous session (e.g. when first loading a branch).
    const localForMerge = opts?.freshLoad
      ? prevShadow && prevShadow.length > 0
        ? prevShadow
        : undefined
      : prevShadow && prevShadow.length > 0
        ? prevShadow
        : viewingNow === sid
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
    if (opts?.skipSetItems) {
      streamShadowBySidRef.current.set(sid, applied);
      evictStaleSessionCaches(viewedSessionIdRef.current);
      return applied;
    }

    // Fetch branch points and inject branch_nav items if any exist.
    let withBranches = applied;
    try {
      const brRes = await fetchJSON<{ branchPoints?: BranchPointData[] }>(
        `/coddy/sessions/${encodeURIComponent(sid)}/branches`,
        { headers: sid === sessionId ? headers : { [HDR]: sid } },
      );
      if (brRes.ok && brRes.data?.branchPoints?.length) {
        withBranches = deduplicateBranchNavs(
          injectBranchNavItems(
            applied.filter((it) => it.type !== "branch_nav"),
            brRes.data.branchPoints,
          ),
        );
        if (sid === viewedSessionIdRef.current.trim()) {
          setSessionHashInLocation(sid, { historySidebar: sessionsOpen });
        }
      }
    } catch {
      // ignore — branch nav is optional
    }

    streamShadowBySidRef.current.set(sid, withBranches);
    evictStaleSessionCaches(viewedSessionIdRef.current);
    // The viewer moved on while this fetch was in flight (the user picked
    // another session or went home): keep the shadow for the next visit, but
    // never paint a stale transcript under the current route.
    if (viewedSessionIdRef.current.trim() !== sid) {
      return withBranches;
    }
    if (fadeOutTimerRef.current !== null) {
      clearTimeout(fadeOutTimerRef.current);
      fadeOutTimerRef.current = null;
    }
    setSessionFadingOut(false);
    setItems(withBranches);
    setSessionLoading(false);
    return withBranches;
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

  function switchBranch(id: string) {
    skipLeafResolveRef.current.add(id);
    pickSession(id);
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

  function goHome() {
    persistComposerDraftBeforeLeave();
    setSessionsOpen(false);
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    setTasksSelectedId(null);
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
    resetLlmSelectionForNewChat();
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

  async function handleBranchSend(text: string, userMsgIdx: number) {
    const sourceSid = sessionId.trim();
    if (!sourceSid) return;

    const showBranchError = (msg: string) => {
      applyStreamItemsForSession(sourceSid, (prev) => [
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

    let data: { newSessionId?: string; error?: { message?: string } } = {};
    try {
      const res = await fetch(
        `/coddy/sessions/${encodeURIComponent(sourceSid)}/branches`,
        {
          method: "POST",
          headers: { ...headers, "Content-Type": "application/json" },
          body: JSON.stringify({ userMessageIndex: userMsgIdx }),
        },
      );
      if (!res.ok) {
        let errMsg = `Branch creation failed (${res.status})`;
        try {
          const body = (await res.json()) as { error?: { message?: string } };
          if (body?.error?.message) errMsg = body.error.message;
        } catch {
          /* ignore */
        }
        showBranchError(errMsg);
        return;
      }
      data = (await res.json()) as { newSessionId?: string };
    } catch (err) {
      showBranchError(
        `Branch creation error: ${err instanceof Error ? err.message : String(err)}`,
      );
      return;
    }
    const newSid = (data.newSessionId || "").trim();
    if (!newSid) {
      showBranchError(t("app.branchCreationNoSessionId"));
      return;
    }
    pendingBranchSendRef.current = { text, sid: newSid };
    pickSession(newSid);
  }

  useEffect(() => {
    setEditingUserMsgIdx(null);
    setEditingAssetNote("");
    setEditingFiles([]);
    setSubagentTranscript(null);
    if (!sessionId) {
      setItems([]);
      setDraft("");
      setSessionLoading(false);
      void loadSessionsList(true);
      return;
    }
    const pending = pendingBranchSendRef.current;
    if (pending && pending.sid === sessionId) {
      pendingBranchSendRef.current = null;
      const text = pending.text;
      const branchSid = sessionId;
      void (async () => {
        // Clear any stale shadow cache for the new branch session so loadMessages
        // doesn't inherit a branch_nav from the previous session's items.
        streamShadowBySidRef.current.delete(branchSid);
        // Load the shared prefix first so the user sees prior context while streaming.
        // freshLoad: skip itemsRef.current as localForMerge so old session items don't bleed in.
        await loadMessages(branchSid, { freshLoad: true });
        void streamResponses(text).then(async () => {
          // After streaming completes, inject the branch_nav so the user can navigate threads.
          try {
            const brRes = await fetchJSON<{ branchPoints?: BranchPointData[] }>(
              `/coddy/sessions/${encodeURIComponent(branchSid)}/branches`,
              { headers: { [HDR]: branchSid } },
            );
            if (brRes.ok && brRes.data?.branchPoints?.length) {
              applyStreamItemsForSession(branchSid, (prev) =>
                deduplicateBranchNavs(
                  injectBranchNavItems(
                    prev.filter((it) => it.type !== "branch_nav"),
                    brRes.data!.branchPoints!,
                  ),
                ),
              );
              if (branchSid === viewedSessionIdRef.current.trim()) {
                setSessionHashInLocation(branchSid, {
                  historySidebar: sessionsOpen,
                });
              }
            }
          } catch {
            // ignore
          }
        });
      })();
      return;
    }
    setDraft("");
    setTokenUsage(null);
    setContextBreakdown(null);
    tokenBaselineRef.current = { input: 0, output: 0, total: 0 };
    const lifecycle = new AbortController();
    void (async () => {
      // If the user explicitly navigated here via branch nav, skip leaf resolution
      // so they stay on the session they chose rather than being redirected to the newest thread.
      const skipLeaf = skipLeafResolveRef.current.has(sessionId);
      skipLeafResolveRef.current.delete(sessionId);

      if (!skipLeaf) {
        // Resolve the most-recently-active leaf in the branch tree.
        // If a more recent thread exists, navigate there instead of loading this one.
        const leafId = await resolveLatestLeaf(sessionId, async (sid) => {
          const r = await fetchJSON<{ branchPoints?: BranchPointData[] }>(
            `/coddy/sessions/${encodeURIComponent(sid)}/branches`,
            { headers: { [HDR]: sid } },
          );
          return r.ok ? (r.data ?? null) : null;
        });
        if (lifecycle.signal.aborted) return;
        if (
          leafId !== sessionId &&
          viewedSessionIdRef.current.trim() === sessionId
        ) {
          openSessionFromRoute(leafId, { historySidebar: sessionsOpen });
          return;
        }
      }

      const list = await loadSessionsList(true);
      if (lifecycle.signal.aborted) {
        return;
      }
      // Child sessions are hidden from History, so a `sub_*` id is fetched on
      // its own: the messages endpoint serves it and marks it read-only.
      const listed = !!list?.some((s) => s.id === sessionId);
      const exists = listed || isSubagentSessionId(sessionId);
      if (exists) {
        const sess = list?.find((s) => s.id === sessionId);
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
            // A `sub_*` id the server no longer serves: nothing to keep a
            // skeleton up for, so it lands on the empty state like any
            // unknown id.
            setSessionLoading(false);
          }
          if (activeComposerSidRef.current.has(sessionId)) {
            const sh = streamShadowBySidRef.current.get(sessionId);
            if (sh && sh.length > 0) {
              setItems([...sh]);
              setSessionLoading(false);
            }
          }
          if (
            loaded &&
            sess?.turnActive &&
            !activeComposerSidRef.current.has(sessionId)
          ) {
            void rejoinComposerLiveStream(sessionId, loaded);
          }
        }
      } else {
        const shElse = streamShadowBySidRef.current.get(sessionId);
        if (
          activeComposerSidRef.current.has(sessionId) ||
          (shElse && shElse.length > 0)
        ) {
          if (shElse && shElse.length > 0) {
            setItems([...shElse]);
            setSessionLoading(false);
          }
        } else {
          setItems([]);
          setSessionLoading(false);
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

    relayAbortBySidRef.current.get(key)?.abort();
    const fetchCtl = new AbortController();
    relayAbortBySidRef.current.set(key, fetchCtl);

    addActiveComposer(key);
    const assistantId = newId("a");
    streamingAssistantBySidRef.current.set(key, assistantId);
    streamShadowBySidRef.current.set(key, [...baseline]);
    if (viewedSessionIdRef.current.trim() === key) {
      setItems([...baseline]);
    }

    const applyStreamItems = (
      fn: (prev: TranscriptItem[]) => TranscriptItem[],
    ) => applyStreamItemsForSession(key, fn);

    const branchTokenUsage = (u: TokenUsage | null) => {
      if (u === null) return;
      if (viewedSessionIdRef.current.trim() === key) {
        setTokenUsage(u);
        debouncedRefreshSessionStats(key);
      }
    };
    const branchContextUsage = (u: ContextUsageUpdate) => {
      if (viewedSessionIdRef.current.trim() === key) {
        setContextBreakdown((prev) => withContextUsedTokens(prev, u.used));
        debouncedRefreshSessionStats(key);
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
      const res = await fetch(
        `/coddy/sessions/${encodeURIComponent(key)}/composer-stream`,
        { headers, signal: fetchCtl.signal },
      );
      if (!res.ok || !res.body) {
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
        applyMemoryPhaseToItems,
        applyMemoryChunkToItems,
        onQuestion: handleComposerSseQuestion,
        onPermission: handleComposerSsePermission,
      });

      const syncAssistantFromServer = async () => {
        try {
          const res2 = await fetchJSON<{ messages: Array<any> }>(
            `/coddy/sessions/${encodeURIComponent(key)}/messages`,
            { headers: { [HDR]: key } },
          );
          if (!res2.ok || !res2.data?.messages) return false;
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
        return;
      }

      flushToolQueue();
      finishThinking();
      ensureAssistant({
        streaming: false,
        createdAtUtc: new Date().toISOString(),
      });

      void loadSessionsList(true);
      let ok = await syncAssistantFromServer();
      for (let i = 0; i < 10 && !ok; i++) {
        await new Promise((r) => setTimeout(r, 500));
        ok = await syncAssistantFromServer();
      }
      const viewing = viewedSessionIdRef.current.trim();
      await loadMessages(key, {
        skipSetItems: viewing !== key,
      });
    } catch {
      // AbortError when relay superseded or fetch aborted
    } finally {
      if (relayAbortBySidRef.current.get(key) === fetchCtl) {
        relayAbortBySidRef.current.delete(key);
      }
      streamingAssistantBySidRef.current.delete(key);
      removeActiveComposer(key);
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
    opts?: { modeOverride?: string; runPlanSlug?: string; files?: File[] },
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
      postAbortBySidRef.current.set(postSessionKey, abortCtl);
      relayAbortBySidRef.current.get(postSessionKey)?.abort();
      relayAbortBySidRef.current.delete(postSessionKey);

      let streamKey = postSessionKey;
      const applyStreamItems = (
        fn: (prev: TranscriptItem[]) => TranscriptItem[],
      ) => applyStreamItemsForSession(streamKey, fn);

      const branchTokenUsage = (u: TokenUsage | null) => {
        if (u === null) return;
        if (viewedSessionIdRef.current.trim() === streamKey) {
          setTokenUsage(u);
          debouncedRefreshSessionStats(streamKey);
        }
      };
      const branchContextUsage = (u: ContextUsageUpdate) => {
        if (viewedSessionIdRef.current.trim() === streamKey) {
          setContextBreakdown((prev) => withContextUsedTokens(prev, u.used));
          debouncedRefreshSessionStats(streamKey);
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
      const atts = extractAtFileAttachments(text);
      const profileModel = (PROFILE_MODES as readonly string[]).includes(mode);
      if (atts.length > 0 && profileModel) {
        reqBody.attachments = atts;
        const wk = sid.trim() || WORKSPACE_AT_RECENTS_NO_SESSION_KEY;
        for (const a of atts) {
          recordWorkspaceAtRecent(wk, { path_rel: a.path, kind: "file" });
        }
      }
      if (opts?.files && opts.files.length > 0) {
        const inlineFiles = await Promise.all(
          opts.files.map(
            (f) =>
              new Promise<{ name: string; data_url: string }>(
                (resolve, reject) => {
                  const reader = new FileReader();
                  reader.onload = () =>
                    resolve({
                      name: f.name,
                      data_url: reader.result as string,
                    });
                  reader.onerror = reject;
                  reader.readAsDataURL(f);
                },
              ),
          ),
        );
        reqBody.inline_files = inlineFiles;
      }
      const yamlSel = llmModel.trim();
      const reasoningSel = llmReasoning.trim();
      const runSlug = (opts?.runPlanSlug || "").trim();
      if (yamlSel || reasoningSel || runSlug) {
        const meta: Record<string, string> = {};
        if (yamlSel) meta.model = yamlSel;
        if (reasoningSel) meta.reasoning = reasoningSel;
        if (runSlug) meta.runPlanSlug = runSlug;
        reqBody.metadata = meta;
      }
      // Mark this session busy before awaiting fetch so hung POST still blocks same-session resend.
      addActiveComposer(postSessionKey);
      const res = await fetch("/v1/responses", {
        method: "POST",
        headers: { ...hdrs, "Content-Type": "application/json" },
        body: JSON.stringify(reqBody),
        signal: abortCtl.signal,
      });

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
        postAbortBySidRef.current.delete(postSessionKey);
        streamingAssistantBySidRef.current.delete(postSessionKey);
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
        postAbortBySidRef.current.delete(oldKey);
        postAbortBySidRef.current.set(postSessionKey, abortCtl);
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
          : remoteHttpErrorMessage(res.status, getEnv());
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
        postAbortBySidRef.current.delete(postSessionKey);
        streamingAssistantBySidRef.current.delete(postSessionKey);
        completedNormally = true;
        return;
      }

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
        applyMemoryPhaseToItems,
        applyMemoryChunkToItems,
        onQuestion: handleComposerSseQuestion,
        onPermission: handleComposerSsePermission,
      });

      const syncAssistantFromServer = async () => {
        try {
          const res2 = await fetchJSON<{ messages: Array<any> }>(
            `/coddy/sessions/${encodeURIComponent(sidEffective)}/messages`,
            { headers: { [HDR]: sidEffective } },
          );
          if (!res2.ok || !res2.data?.messages) return false;
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
          ok = await syncAssistantFromServer();
        }
      }
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
    } finally {
      postAbortBySidRef.current.delete(postSessionKey);
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
      releaseSessionId?.(sidEffective);
      // A background stream that just finished on a no-longer-recent session
      // should release its transcript without waiting for the next navigation.
      evictStaleSessionCaches(viewedSessionIdRef.current);
    }
  }

  function stopActiveGeneration(): void {
    const sid = sessionId.trim();
    if (!sid) return;
    // Always send the server-side cancel so Stop works even after page reload.
    void fetch(`/coddy/sessions/${encodeURIComponent(sid)}/cancel`, {
      method: "POST",
      headers: { [HDR]: sid },
    });
    // Also abort the in-progress fetch request if we have one from this page session.
    postAbortBySidRef.current.get(sid)?.abort();
  }

  const contextPct = useMemo(
    () => contextUsagePercent(maxContextTokens, contextBreakdown),
    [maxContextTokens, contextBreakdown],
  );

  const openSchedulerFromNav = useCallback(() => {
    if (schedulerHttpLinked !== true) {
      return;
    }
    setSessionsOpen(false);
    setTasksOpen(false);
    setTasksSelectedId(null);
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
    setTasksSelectedId(null);
    setSessionTasksHash(sid);
  }, [sessionId]);

  const closeTasksDrawer = useCallback(() => {
    setTasksOpen(false);
    setTasksSelectedId(null);
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

  const openBackgroundTask = useCallback(
    (taskId: string) => {
      const sid = sessionId.trim();
      if (!sid) {
        return;
      }
      setTasksOpen(true);
      setTasksSelectedId(taskId);
      setSessionTasksHash(sid, taskId);
    },
    [sessionId],
  );

  const backToBackgroundTaskList = useCallback(() => {
    const sid = sessionId.trim();
    setTasksSelectedId(null);
    if (sid) {
      setSessionTasksHash(sid);
    }
  }, [sessionId]);

  /** Opens a session in this tab: the child transcript behind an agent task,
   *  or the parent chat from a read-only notice. Same path as a History pick,
   *  so the panel closes and the hash becomes `#/s/<id>`. */
  const openSessionInPlace = (targetId: string) => {
    const id = targetId.trim();
    if (id) {
      pickSession(id);
    }
  };

  const openSettingsFromNav = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setTasksOpen(false);
    setTasksSelectedId(null);
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
    setTasksSelectedId(null);
    setSettingsRoute(false);
    setSessionsOpen(true);
    setHistoryHash();
  }, []);

  // The panel belongs to a chat, so it only exists when one is open.
  const tasksPanelOpen = tasksOpen && !!sessionId.trim();

  const shellBackdropOpen =
    sessionsOpen ||
    (schedulerOpen && schedulerHttpLinked === true) ||
    settingsRoute;

  const sessionPanelShared = {
    sessionId: sidebarActiveId,
    permissionPendingSessionIds: permissionPendingSids,
    questionPendingSessionIds: questionPendingSids,
    sessions: sessionsForSidebar,
    ...(sessionsError ? { error: sessionsError } : {}),
    open: sessionsOpen,
    onClose: () => {
      setSessionsOpen(false);
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
    onDelete: deleteSession as (id: string) => void | Promise<void>,
    searchDraft: sessionFilterDraft,
    onSearchDraftChange: setSessionFilterDraft,
    onSearchClear: () => setSessionFilterDraft(""),
    hasMore: sessionsHasMore,
    loadingMore: sessionsLoadingMore,
    onLoadMore: () => void loadSessionsList(false),
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
        showScheduler={schedulerHttpLinked === true}
        onOpenScheduler={openSchedulerFromNav}
        schedulerOpen={schedulerOpen}
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
          <SchedulerDockCluster
            clusterRef={schedulerDockClusterRef}
            editor={schedulerEditor}
            setEditor={setSchedulerEditor}
            info={schedulerInfo}
            jobs={filteredSchedulerJobs}
            listError={schedulerListError}
            loading={schedulerListLoading}
            filterDraft={schedulerFilterDraft}
            setFilterDraft={setSchedulerFilterDraft}
            onClose={closeSchedulerDrawer}
            onRunJob={onSchedulerRunJob}
            onCancelJob={onSchedulerCancelJob}
            refreshJobs={refreshSchedulerJobs}
            availableModels={llmModelIds}
            defaultModel={llmModel}
            currentCwd={currentSessionCwd}
          />
        ) : null}

        {settingsRoute ? (
          <div className="settings-dock-cluster">
            <Settings
              onClose={onCloseSettings}
              onConfigSaved={bumpModelsEpoch}
              initialSection={settingsSection}
            />
          </div>
        ) : null}
        {tasksPanelOpen ? (
          <BackgroundTasksPanel
            open
            selectedTaskId={tasksSelectedId}
            tasks={backgroundTasks}
            selectedOutput={backgroundOutput}
            listError={backgroundListError}
            loading={backgroundListLoading}
            nowMs={backgroundNowMs}
            onClose={closeTasksDrawer}
            onOpenTask={openBackgroundTask}
            onBackToList={backToBackgroundTaskList}
            onStopTask={(id) => {
              void stopBackgroundTaskById(id);
            }}
            onClearFinished={() => {
              void clearFinishedTasks();
            }}
            onOpenSession={openSessionInPlace}
          />
        ) : null}

        <ChatScreen
          title={currentTitle}
          sessionId={sessionId}
          backgroundTasks={backgroundTasks}
          onOpenBackgroundTasks={openTasksFromNav}
          backgroundTasksByToolCallId={backgroundTasksByToolCallId}
          backgroundNowMs={backgroundNowMs}
          onOpenBackgroundTask={openBackgroundTask}
          onStopBackgroundTask={handleStopBackgroundTask}
          subagentTranscript={subagentTranscript}
          onOpenSession={openSessionInPlace}
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
                    activeComposerSidRef.current.has(sessionId.trim())
                  ) {
                    return;
                  }
                  void streamResponses(t("chat.runPlanMessage"), {
                    modeOverride: "agent",
                    runPlanSlug: slug,
                  });
                },
                onPlanDocumentDiscard: async (itemId: string, slug: string) => {
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
          onBranchSwitch={(sid) => switchBranch(sid)}
          {...(knownSkillNames.size > 0 ? { knownSkillNames } : {})}
          onSend={(text: string, files?: File[]) => {
            // A subagent transcript is read-only: the server answers 409.
            if (subagentTranscript) {
              return;
            }
            if (
              sessionId.trim() &&
              activeComposerSidRef.current.has(sessionId.trim())
            ) {
              return;
            }
            setDraft("");
            if (editingUserMsgIdx !== null) {
              const idx = editingUserMsgIdx;
              const note = editingAssetNote;
              setEditingUserMsgIdx(null);
              setEditingAssetNote("");
              setEditingFiles([]);
              const textWithAssets = note ? `${text}\n${note}` : text;
              void handleBranchSend(textWithAssets, idx);
            } else {
              void streamResponses(text, files ? { files } : undefined);
            }
          }}
          onFetchToolCallFull={handleFetchToolCallFull}
        />
      </div>
    </div>
  );
}
