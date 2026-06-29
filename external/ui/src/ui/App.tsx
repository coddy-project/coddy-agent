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
import { contextUsagePercent } from "./chat/contextUsage";
import {
  HERO_ACCENT_VERBS,
  pickHeroAccentVerb,
} from "./chat/heroTitleWords";
import { insertNewThinkingBeforeStreamingAssistant } from "./chat/transcriptThinkingPlacement";
import { openAIStreamErrorMessage } from "./chat/streamError";
import { parseSSEBlocks } from "./chat/sse";
import { consumeComposerSseReader } from "./chat/consumeComposerSse";
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
} from "./chat/transcriptServerSnapshot";
import { pickStreamMutationBase } from "./chat/streamMutationBase";
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
import { injectBranchNavItems, deduplicateBranchNavs, type BranchPointData } from "./chat/branchInject";
import { resolveLatestLeaf } from "./chat/resolveLatestLeaf";
import { NavRail } from "./nav/NavRail";
import { readNavRailCookie, writeNavRailCookie } from "./nav/navRailCookie";
import { readLlmModelCookie, writeLlmModelCookie } from "./chat/llmModelCookie";
import {
  pickDefaultLlmModelForNewChat,
  pickLlmModelForOpenSession,
} from "./chat/llmModelSelection";
import { readReasoningCookie, writeReasoningCookie } from "./chat/reasoningCookie";
import { pickReasoningLevel } from "./chat/reasoningSelection";
import { SessionsSidebar } from "./sessions/SessionsSidebar";
import {
  armSessionDeleteBackdropSuppressUntil,
  shouldSuppressShellBackdropClose,
} from "./sessions/sessionDeleteBackdropSuppress";
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
import { schedulerCancelJob, schedulerListJobs, schedulerRunJob } from "./scheduler/api";
import {
  parseAppHash,
  setDraftHashInLocation,
  setHistoryHash,
  setSessionHashInLocation,
  schedulerEditorFromParsedHash,
  setSchedulerCreateHash,
  setSchedulerJobHash,
  setSchedulerListHash,
  setSettingsHash,
  stripHistorySidebarFromHash,
} from "./scheduler/hashRoute";
import { SchedulerJobEditorSheet } from "./scheduler/SchedulerJobEditorSheet";
import { SchedulerJobsDrawer } from "./scheduler/SchedulerJobsDrawer";
import type { SchedulerInfo, SchedulerJob } from "./scheduler/types";
import { Settings } from "./settings/Settings";

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
  | { mode: "edit"; jobId: string };

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
};

function readMessageCreatedAtUTC(m: Record<string, unknown>): string | undefined {
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

type MemoryPhaseEvt = {
  memoryRowId: string;
  phase: string;
  status: string;
  userTurnIndex?: number;
  durationMs?: number;
  persistSaved?: boolean;
  persistRelativePath?: string;
  persistTitle?: string;
  persistSavedBody?: string;
  recallReadPaths?: string[];
};

type MemoryChunkEvt = {
  memoryRowId: string;
  phase: string;
  kind: string;
  delta: string;
};

type MemoryTurnApi = {
  userTurnIndex: number;
  memoryRowId?: string;
  memoryMode?: string;
  memoryDurationMs?: number;
  memoryContextText?: string;
  recallSkipped?: boolean;
  recallText?: string;
  recallReasoningText?: string;
  recallDurationMs?: number;
  persistJudgeText?: string;
  persistDurationMs?: number;
  persistSaved?: boolean;
  persistRelativePath?: string;
  persistTitle?: string;
  persistSavedBody?: string;
  recallReadPaths?: string[];
};

type ModelInfo = {
  id: string;
  ownedBy?: string;
  maxContextTokens?: number | undefined;
  multimodal?: boolean;
  reasoningLevels?: string[];
  reasoningDefault?: string;
};

const PROFILE_MODES = ["agent", "plan"] as const;

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

function memoryTranscriptFromApi(
  row: MemoryTurnApi,
): Extract<TranscriptItem, { type: "memory_copilot" }> {
  const rowId = (row.memoryRowId || "").trim() || `mem-${row.userTurnIndex}`;
  const unifiedCtx = (row.memoryContextText || "").trim();
  const rt = (row.recallText || "").trim();
  const rr = (row.recallReasoningText || "").trim();
  const paths = Array.isArray(row.recallReadPaths)
    ? row.recallReadPaths.filter(
        (x) => typeof x === "string" && x.trim() !== "",
      )
    : [];
  const hasRecallTrail = !!(
    row.recallDurationMs ||
    rt ||
    rr ||
    paths.length > 0
  );
  const pt = (row.persistJudgeText || "").trim();
  const hasPersistTrail = !!(row.persistDurationMs || pt || row.persistSaved);
  const hasUnified = !!(row.memoryDurationMs || unifiedCtx);
  const sumMs =
    typeof row.memoryDurationMs === "number" && row.memoryDurationMs > 0
      ? row.memoryDurationMs
      : (row.recallDurationMs ?? 0) + (row.persistDurationMs ?? 0);
  const legacyCombined = [row.recallText, row.persistJudgeText]
    .filter((x) => typeof x === "string" && x.trim() !== "")
    .join("\n\n");
  const memoryTextOut = unifiedCtx || legacyCombined;
  return {
    id: stableMemoryCopilotItemId(rowId, row.userTurnIndex),
    type: "memory_copilot",
    memoryRowId: rowId,
    userTurnIndex: row.userTurnIndex,
    ...(hasUnified
      ? { memoryStatus: "completed" as const, memoryText: memoryTextOut }
      : {}),
    recallStatus: hasRecallTrail ? "completed" : "idle",
    persistStatus: hasPersistTrail ? "completed" : "idle",
    recallText: row.recallText || "",
    recallReasoning: row.recallReasoningText || "",
    persistText: row.persistJudgeText || "",
    persistReasoning: "",
    ...(typeof row.recallDurationMs === "number"
      ? { recallDurationMs: row.recallDurationMs }
      : {}),
    ...(typeof row.persistDurationMs === "number"
      ? { persistDurationMs: row.persistDurationMs }
      : {}),
    ...(sumMs > 0 ? { memoryWallDurationMs: sumMs } : {}),
    ...(typeof row.persistSaved === "boolean" ? { persistSaved: row.persistSaved } : {}),
    ...(row.persistRelativePath ? { persistRelativePath: row.persistRelativePath } : {}),
    ...(row.persistTitle ? { persistTitle: row.persistTitle } : {}),
    ...(row.persistSavedBody ? { persistSavedBody: row.persistSavedBody } : {}),
    ...(paths.length > 0 ? { recallReadPaths: paths } : {}),
  };
}

function applyMemoryPhaseToItems(
  prev: TranscriptItem[],
  p: MemoryPhaseEvt,
): TranscriptItem[] {
  const now = Date.now();
  let idx = prev.findIndex(
    (x) => x.type === "memory_copilot" && x.memoryRowId === p.memoryRowId,
  );
  const next = [...prev];
  let uidx = -1;
  for (let i = prev.length - 1; i >= 0; i--) {
    const it = prev[i];
    if (it && it.type === "user_message") {
      uidx = i;
      break;
    }
  }
  const insertAt = uidx >= 0 ? uidx + 1 : next.length;

  const baseMemory = (): Extract<
    TranscriptItem,
    { type: "memory_copilot" }
  > => ({
    id: stableMemoryCopilotItemId(
      p.memoryRowId,
      typeof p.userTurnIndex === "number" ? p.userTurnIndex : 0,
    ),
    type: "memory_copilot",
    memoryRowId: p.memoryRowId,
    userTurnIndex: typeof p.userTurnIndex === "number" ? p.userTurnIndex : 0,
    memoryStatus: "idle",
    memoryText: "",
    recallStatus: "idle",
    persistStatus: "idle",
    recallText: "",
    recallReasoning: "",
    persistText: "",
    persistReasoning: "",
  });

  if (idx < 0) {
    next.splice(insertAt, 0, baseMemory());
    idx = insertAt;
  }

  const cur = next[idx];
  if (!cur || cur.type !== "memory_copilot") {
    return prev;
  }

  let patch: Extract<TranscriptItem, { type: "memory_copilot" }> = { ...cur };
  const st = (p.status || "").trim();

  if (p.phase === "memory") {
    if (st === "started") {
      patch.memoryStatus = "in_progress";
      patch.recallStatus = "in_progress";
      patch.persistStatus = "idle";
      if (patch.memoryWallStartedAtMs == null)
        patch.memoryWallStartedAtMs = now;
    }
    if (st === "completed") {
      patch.memoryStatus = "completed";
      patch.recallStatus = "completed";
      patch.persistStatus = p.persistSaved ? "completed" : "idle";
      const rp = p.recallReadPaths;
      if (Array.isArray(rp) && rp.length > 0) {
        const cleaned = rp.map((x) => String(x).trim()).filter((x) => x !== "");
        if (cleaned.length > 0) patch.recallReadPaths = cleaned;
      }
      if (typeof p.persistSaved === "boolean") {
        patch.persistSaved = p.persistSaved;
      }
      const pr = (p.persistRelativePath || "").trim();
      if (pr) patch.persistRelativePath = pr;
      const tt = (p.persistTitle || "").trim();
      if (tt) patch.persistTitle = tt;
      const pb = (p.persistSavedBody || "").trim();
      if (pb) patch.persistSavedBody = pb;
      if (typeof patch.memoryWallStartedAtMs === "number") {
        patch.memoryWallDurationMs = Math.max(
          0,
          now - patch.memoryWallStartedAtMs,
        );
      }
    }
  }
  if (p.phase === "recall") {
    if (st === "started") {
      patch.recallStatus = "in_progress";
      if (patch.memoryWallStartedAtMs == null)
        patch.memoryWallStartedAtMs = now;
    }
    if (st === "completed") {
      patch.recallStatus = "completed";
      if (typeof p.durationMs === "number" && p.durationMs > 0)
        patch.recallDurationMs = p.durationMs;
      const rp = p.recallReadPaths;
      if (Array.isArray(rp) && rp.length > 0) {
        const cleaned = rp.map((x) => String(x).trim()).filter((x) => x !== "");
        if (cleaned.length > 0) patch.recallReadPaths = cleaned;
      }
    }
  }
  if (p.phase === "persist") {
    if (st === "started") {
      patch.persistStatus = "in_progress";
      if (patch.memoryWallStartedAtMs == null)
        patch.memoryWallStartedAtMs = now;
      const wallStart = patch.memoryWallStartedAtMs;
      const wallElapsed =
        typeof wallStart === "number" ? Math.max(0, now - wallStart) : 0;
      if (
        typeof patch.memoryWallLiveCapMs === "number" &&
        Number.isFinite(patch.memoryWallLiveCapMs)
      ) {
        patch.memoryWallLiveCapMs = Math.max(
          patch.memoryWallLiveCapMs,
          wallElapsed,
        );
      } else {
        patch.memoryWallLiveCapMs = wallElapsed;
      }
    }
    if (st === "completed") {
      patch.persistStatus = "completed";
      if (typeof p.durationMs === "number" && p.durationMs > 0)
        patch.persistDurationMs = p.durationMs;
      if (typeof p.persistSaved === "boolean") {
        patch.persistSaved = p.persistSaved;
      }
      const pr = (p.persistRelativePath || "").trim();
      if (pr) patch.persistRelativePath = pr;
      const tt = (p.persistTitle || "").trim();
      if (tt) patch.persistTitle = tt;
      const pb = (p.persistSavedBody || "").trim();
      if (pb) patch.persistSavedBody = pb;
      if (typeof patch.memoryWallStartedAtMs === "number") {
        patch.memoryWallDurationMs = Math.max(
          0,
          now - patch.memoryWallStartedAtMs,
        );
      }
    }
  }

  next[idx] = patch;
  return next;
}

function applyMemoryChunkToItems(
  prev: TranscriptItem[],
  c: MemoryChunkEvt,
): TranscriptItem[] {
  const idx = prev.findIndex(
    (x) => x.type === "memory_copilot" && x.memoryRowId === c.memoryRowId,
  );
  if (idx < 0) return prev;
  const cur = prev[idx];
  if (!cur || cur.type !== "memory_copilot") return prev;
  const next = [...prev];
  const patch: Extract<TranscriptItem, { type: "memory_copilot" }> = { ...cur };
  const ph = (c.phase || "").trim();
  const kd = (c.kind || "").trim();
  const d = typeof c.delta === "string" ? c.delta : "";
  if (!d) return prev;
  if (ph === "memory") {
    if (kd !== "reasoning") patch.memoryText = (patch.memoryText || "") + d;
  } else if (ph === "recall") {
    if (kd !== "reasoning") patch.recallText += d;
  } else if (ph === "persist") {
    if (kd !== "reasoning") patch.persistText += d;
  } else {
    return prev;
  }
  next[idx] = patch;
  return next;
}

/** Freeze the memory wall-clock label once main-model reasoning starts while recall/persist are still SSE-busy (events can arrive after reasoning deltas). */
function freezeMemoryWallWhenThinkingAfterRecall(
  items: TranscriptItem[],
  freezeAtMs: number,
): TranscriptItem[] {
  let userIdx = -1;
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (!it) continue;
    if (it && it.type === "user_message") {
      userIdx = i;
      break;
    }
  }
  if (userIdx < 0) return items;

  let memIdx = -1;
  let thinkingIdx = -1;
  for (let i = userIdx + 1; i < items.length; i++) {
    const it = items[i];
    if (!it) continue;
    if (it.type === "user_message") break;
    if (it.type === "memory_copilot") memIdx = i;
    if (it.type === "thinking" && it.status === "in_progress") {
      thinkingIdx = i;
      break;
    }
  }
  if (memIdx < 0 || thinkingIdx < 0) return items;

  const m = items[memIdx];
  if (!m || m.type !== "memory_copilot") return items;

  const memBusy =
    m.memoryStatus === "in_progress" ||
    m.recallStatus === "in_progress" ||
    m.persistStatus === "in_progress";
  if (!memBusy || typeof m.memoryWallLiveCapMs === "number") return items;

  const startMs = m.memoryWallStartedAtMs;
  if (typeof startMs !== "number") return items;

  const cap = Math.max(0, freezeAtMs - startMs);
  const next = [...items];
  next[memIdx] = { ...m, memoryWallLiveCapMs: cap };
  return next;
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
  const [knownSkillNames, setKnownSkillNames] = useState<Set<string>>(
    () => new Set(),
  );
  const [sessionId, setSessionId] = useState("");
  /** Increments on each explicit "new chat" home transition so the hero verb rotates. */
  const [heroHomeGeneration, setHeroHomeGeneration] = useState(() =>
    Math.floor(Math.random() * HERO_ACCENT_VERBS.length),
  );
  const [sessions, setSessions] = useState<SessionRow[]>([]);
  const [sessionsCursor, setSessionsCursor] = useState<string | null>(null);
  const sessionsCursorRef = useRef<string | null>(null);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  const [items, setItems] = useState<TranscriptItem[]>([]);
  const [sessionLoading, setSessionLoading] = useState(false);
  const [sessionFadingOut, setSessionFadingOut] = useState(false);
  const fadeOutTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const itemsRef = useRef<TranscriptItem[]>([]);
  itemsRef.current = items;
  const [editingUserMsgIdx, setEditingUserMsgIdx] = useState<number | null>(null);
  const [editingAssetNote, setEditingAssetNote] = useState("");
  const [editingFiles, setEditingFiles] = useState<{ name: string; mimeType: string }[]>([]);
  const pendingBranchSendRef = useRef<{ text: string; sid: string } | null>(null);
  // Sessions explicitly chosen via branch nav — skip resolveLatestLeaf for these.
  const skipLeafResolveRef = useRef<Set<string>>(new Set());
  const [draft, setDraft] = useState("");
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
  const [contextBreakdown, setContextBreakdown] = useState<
    NonNullable<SessionStats["contextBreakdown"]> | null
  >(null);

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
  /** Per-session shadow transcript while that session streams in the background. */
  const streamShadowBySidRef = useRef<Map<string, TranscriptItem[]>>(new Map());
  const postAbortBySidRef = useRef<Map<string, AbortController>>(new Map());
  const relayAbortBySidRef = useRef<Map<string, AbortController>>(new Map());
  const streamingAssistantBySidRef = useRef<Map<string, string>>(new Map());
  /** Session ids with an active client-side composer POST or GET relay. */
  const activeComposerSidRef = useRef<Set<string>>(new Set());
  const [composerActivityEpoch, setComposerActivityEpoch] = useState(0);
  /** Session id currently shown in the transcript (updated synchronously on navigation). */
  const viewedSessionIdRef = useRef("");
  /** Ignore shell backdrop close briefly after session-delete confirm (stray click). */
  const sessionDeleteBackdropSuppressUntilRef = useRef(0);
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

  const generating = useMemo(() => {
    const sid = sessionId.trim();
    if (!sid) return false;
    return activeComposerSidRef.current.has(sid);
  }, [sessionId, composerActivityEpoch]);

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
    [sessions, clientDraftSessions],
  );

  const reasoningDurationMsByContentRef = useRef<Map<string, number>>(
    new Map(),
  );
  const [modelInfos, setModelInfos] = useState<ModelInfo[]>([]);
  const [modelsEpoch, setModelsEpoch] = useState(0);
  const [sessionsOpen, setSessionsOpen] = useState(false);
  /** null until first probe of /coddy/scheduler/jobs; false when route returns 404 (binary without scheduler). */
  const [schedulerHttpLinked, setSchedulerHttpLinked] = useState<
    boolean | null
  >(null);
  const [schedulerOpen, setSchedulerOpen] = useState(false);
  const [settingsRoute, setSettingsRoute] = useState(false);
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
  const [schedDockClusterWidthPx, setSchedDockClusterWidthPx] = useState(0);
  const [sessionFilterDraft, setSessionFilterDraft] = useState("");
  const [sessionFilterQ, setSessionFilterQ] = useState("");
  const [sessionsHasMore, setSessionsHasMore] = useState(false);
  const [sessionsLoadingMore, setSessionsLoadingMore] = useState(false);
  const sessionsHasMoreRef = useRef(false);
  const sessionsLoadingMoreRef = useRef(false);
  const [viewportXL, setViewportXL] = useState(false);
  const [railLabelsWide, setRailLabelsWide] = useState(false);
  const [mode, setMode] = useState<string>("agent");
  const [llmModelIds, setLlmModelIds] = useState<string[]>([]);
  const [defaultAgentYamlModel, setDefaultAgentYamlModel] = useState("");
  const [llmModel, setLlmModel] = useState("");
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
          (x) =>
            !(
              x.type === "question_prompt" &&
              !x.resolved
            ),
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
    (
      sessionId: string,
      itemId: string,
      resolved: QuestionResolvedState,
    ) => {
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
            ...(hit.resolved !== undefined
              ? { resolved: hit.resolved }
              : {}),
          });
        }
        return next;
      });
    },
    [],
  );

  const resolvePermissionPrompt = useCallback(
    (
      sessionId: string,
      itemId: string,
      resolved: PermissionResolvedState,
    ) => {
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
      return "New chat";
    }
    if (describePreview?.sessionId === sessionId) {
      const hint = describePreview.title.trim();
      if (hint) {
        return hint;
      }
    }
    const row = sessions.find((s) => s.id === sessionId);
    const t = (row?.title || "").trim();
    return t || "New chat";
  }, [sessionId, sessions, describePreview]);

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

  const headers = useMemo(
    () => (sessionId ? { [HDR]: sessionId } : {}),
    [sessionId],
  );

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
          msg =
            "Scheduler API is not available in this build (rebuild with http,scheduler).";
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
          msg =
            "Scheduler is disabled (set scheduler.enabled or pass -scheduler-enabled).";
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
    [sessionId],
  );

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
      setSessionsOpen(!!p.historyOpen);
      return;
    }
    if (p.branch === "draft") {
      setSettingsRoute(false);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
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
      return;
    }
    if (p.branch === "settings") {
      setSettingsRoute(true);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
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
      setSchedulerEditor(schedulerEditorFromParsedHash(p));
      return;
    }
    viewedSessionIdRef.current = "";
    setSessionId("");
    setActiveDraftId("");
    setSettingsRoute(false);
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setSessionsOpen(!!p.historyOpen);
  }, [schedulerHttpLinked]);

  const openSessionFromRoute = useCallback(
    (id: string, opts?: { historySidebar?: boolean }) => {
      setActiveDraftId("");
      setSchedulerOpen(false);
      setSchedulerEditor(null);
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
    viewedSessionIdRef.current = "";
    setSessionHashInLocation("");
    setSessionId("");
    setActiveDraftId("");
  }, []);

  const closeSchedulerDrawer = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
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
  }, []);

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

  useEffect(() => {
    void (async () => {
      const res = await fetchJSON<{
        default_agent_model?: string;
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
      const defaultYaml = (res.data.default_agent_model || "").trim();
      setDefaultAgentYamlModel(defaultYaml);
      if (!viewedSessionIdRef.current.trim()) {
        setLlmModel(
          pickDefaultLlmModelForNewChat({
            backends,
            cookie: readLlmModelCookie(),
            defaultAgentModel: defaultYaml,
          }),
        );
      }
    })();
  // modelsEpoch bumps after config save so the multimodal flag refreshes without a page reload.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modelsEpoch]);

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
    setLlmModel(
      pickLlmModelForOpenSession({
        backends: llmModelIds,
        sessionModel: openSessionSelection.model,
        cookie: readLlmModelCookie(),
        defaultAgentModel: defaultAgentYamlModel,
      }),
    );
    setLlmReasoning(openSessionSelection.reasoning);
  }, [openSessionSelection, llmModelIds, defaultAgentYamlModel]);

  useEffect(() => {
    setDescribePreview((p) => (p && p.sessionId !== sessionId ? null : p));
  }, [sessionId]);

  useEffect(() => {
    const mq = window.matchMedia("(min-width: 1920px)");
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
  }, [
    sessionsOpen,
    schedulerOpen,
    schedulerEditor,
    closeSchedulerDrawer,
  ]);

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
        const cur = sessionsCursorRef.current;
        if (cur) {
          ps.set("cursor", cur);
        }
      }
      if (sessionFilterQ) {
        ps.set("q", sessionFilterQ);
      }
      ps.set("include_activity", "true");
      const res = await fetchJSON<{
        sessions: SessionRow[];
        nextCursor?: string | null;
        hasMore?: boolean;
      }>(`/coddy/sessions?${ps.toString()}`, {
        headers,
      });
      if (!reset) {
        sessionsLoadingMoreRef.current = false;
        setSessionsLoadingMore(false);
      }
      if (!res.ok || !res.data) {
        setSessionsError(`Backend is unavailable (${res.status})`);
        return null;
      }
      setSessionsError(null);
      const next = res.data.sessions || [];
      setSessions((prev) => {
        if (reset) {
          return next;
        }
        const seen = new Set(prev.map((s) => s.id));
        return [...prev, ...next.filter((s) => !seen.has(s.id))];
      });
      const nextCur = res.data.nextCursor ?? null;
      setSessionsCursor(nextCur);
      sessionsCursorRef.current = nextCur;
      const hm = !!res.data.hasMore;
      setSessionsHasMore(hm);
      sessionsHasMoreRef.current = hm;
      return next;
    },
    [sessionFilterQ, headers],
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
    if (!anyLocalComposer && !hasBackgroundTurn) {
      return;
    }
    const timer = window.setInterval(() => {
      void loadSessionsList(true);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [composerActivityEpoch, sessions, sessionId, loadSessionsList]);

  useEffect(() => {
    if (!sessionsOpen) {
      return;
    }
    void loadSessionsList(true);
  }, [sessionsOpen, sessionFilterQ, loadSessionsList]);

  async function loadMessages(
    idOverride?: string,
    opts?: { skipSetItems?: boolean; preserveOnError?: boolean; freshLoad?: boolean },
  ): Promise<TranscriptItem[] | null> {
    const sid = (idOverride ?? sessionId).trim();
    if (!sid) {
      setItems([]);
      return null;
    }
    const viewingTrim = viewedSessionIdRef.current.trim();
    const res = await fetchJSON<{
      messages: Array<any>;
      model?: string;
      selectedModelId?: string;
      selectedReasoning?: string;
      memoryTurns?: MemoryTurnApi[];
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
    if (!res.ok || !res.data) {
      if (!opts?.preserveOnError) {
        if (viewingTrim === sid) {
          setItems([]);
        }
      }
      return null;
    }
    if (viewingTrim === sid) {
      // Stash the session's saved selection; an effect applies it once the
      // backends list is loaded (the two fetches race on reload). The reasoning
      // level is later validated by the clamp effect against the chosen model.
      setOpenSessionSelection({
        sid,
        model: (res.data.model || res.data.selectedModelId || "").trim(),
        reasoning: (res.data.selectedReasoning || "").trim(),
      });
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
    for (const m of res.data.messages || []) {
      const role = (m.role || "").trim();
      if (role === "user") {
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
        const parsedAssets = parseSessionAssetFiles(rawContent);
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
              ...(pd.updatedAt
                ? { updatedAtUtc: String(pd.updatedAt) }
                : {}),
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
              : row.argsPreview;
          if (pickedArgs) merged.argsText = pickedArgs;
        }
        if (row.resultPreview) merged.resultText = row.resultPreview;
        if (row.resultPreviewTruncated === true)
          merged.resultWasTruncated = true;
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
        : viewingTrim === sid
          ? itemsRef.current
          : undefined;
    const mergedBase = preserveUserMessageFiles(
      mergeTranscriptPreferLocalSuffix(next, localForMerge),
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
        viewingSid: viewingTrim,
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
        withBranches = deduplicateBranchNavs(injectBranchNavItems(
          applied.filter((it) => it.type !== "branch_nav"),
          brRes.data.branchPoints,
        ));
        if (sid === viewedSessionIdRef.current.trim()) {
          setSessionHashInLocation(sid, { historySidebar: sessionsOpen });
        }
      }
    } catch {
      // ignore — branch nav is optional
    }

    streamShadowBySidRef.current.set(sid, withBranches);
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
  }

  function goHome() {
    persistComposerDraftBeforeLeave();
    setSessionsOpen(false);
    setSchedulerOpen(false);
    setSchedulerEditor(null);
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
    // Drop any stashed session selection so its restore effect cannot reapply
    // the old session's model over the new chat default.
    setOpenSessionSelection(null);
    if (llmModelIds.length > 0) {
      setLlmModel(
        pickDefaultLlmModelForNewChat({
          backends: llmModelIds,
          cookie: readLlmModelCookie(),
          defaultAgentModel: defaultAgentYamlModel,
        }),
      );
    }
  }

  async function deleteSession(id: string) {
    if (isClientDraftSessionId(id)) {
      const ok = window.confirm("Delete draft");
      if (!ok) {
        return;
      }
      armSessionDeleteBackdropSuppressUntil(sessionDeleteBackdropSuppressUntilRef);
      const rows = removeClientDraftSession(id);
      setClientDraftSessions(rows);
      if (id === activeDraftId || id === sidebarActiveId) {
        setSessionsOpen(false);
        goHome();
      }
      return;
    }
    const ok = window.confirm("Delete chat");
    if (!ok) {
      return;
    }
    armSessionDeleteBackdropSuppressUntil(sessionDeleteBackdropSuppressUntilRef);
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
        { id: newId("s"), type: "system_notice" as const, level: "error" as const, message: msg, createdAtUtc: new Date().toISOString() },
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
        } catch { /* ignore */ }
        showBranchError(errMsg);
        return;
      }
      data = (await res.json()) as { newSessionId?: string };
    } catch (err) {
      showBranchError(`Branch creation error: ${err instanceof Error ? err.message : String(err)}`);
      return;
    }
    const newSid = (data.newSessionId || "").trim();
    if (!newSid) {
      showBranchError("Branch creation returned no session ID");
      return;
    }
    pendingBranchSendRef.current = { text, sid: newSid };
    pickSession(newSid);
  }

  useEffect(() => {
    setEditingUserMsgIdx(null);
    setEditingAssetNote("");
    setEditingFiles([]);
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
                deduplicateBranchNavs(injectBranchNavItems(
                  prev.filter((it) => it.type !== "branch_nav"),
                  brRes.data!.branchPoints!,
                )),
              );
              if (branchSid === viewedSessionIdRef.current.trim()) {
                setSessionHashInLocation(branchSid, { historySidebar: sessionsOpen });
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
        const leafId = await resolveLatestLeaf(
          sessionId,
          async (sid) => {
            const r = await fetchJSON<{ branchPoints?: BranchPointData[] }>(
              `/coddy/sessions/${encodeURIComponent(sid)}/branches`,
              { headers: { [HDR]: sid } },
            );
            return r.ok ? (r.data ?? null) : null;
          },
        );
        if (lifecycle.signal.aborted) return;
        if (leafId !== sessionId && viewedSessionIdRef.current.trim() === sessionId) {
          openSessionFromRoute(leafId, { historySidebar: sessionsOpen });
          return;
        }
      }

      const list = await loadSessionsList(true);
      if (lifecycle.signal.aborted) {
        return;
      }
      const exists = !!list?.some((s) => s.id === sessionId);
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

    const applyStreamItems = (fn: (prev: TranscriptItem[]) => TranscriptItem[]) =>
      applyStreamItemsForSession(key, fn);

    const branchTokenUsage = (u: TokenUsage | null) => {
      if (u === null) return;
      if (viewedSessionIdRef.current.trim() === key) {
        setTokenUsage(u);
        debouncedRefreshSessionStats(key);
      }
    };

    try {
      const res = await fetch(
        `/coddy/sessions/${encodeURIComponent(key)}/composer-stream`,
        { headers: { [HDR]: key }, signal: fetchCtl.signal },
      );
      if (!res.ok || !res.body) {
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
      } = await consumeComposerSseReader({
        reader,
        dec,
        carry,
        assistantId,
        applyStreamItems,
        setTokenUsage: branchTokenUsage,
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
              lastCreated = readMessageCreatedAtUTC(m as Record<string, unknown>);
            }
          }
          if (!last) return false;
          ensureAssistant();
          applyStreamItems((prev) =>
            prev.map((it) =>
              it.type === "assistant_message" && it.id === assistantId
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
                it.id === assistantId &&
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
        if (activeDraftId.trim()) {
          setClientDraftSessions(removeClientDraftSession(activeDraftId.trim()));
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
      const applyStreamItems = (fn: (prev: TranscriptItem[]) => TranscriptItem[]) =>
        applyStreamItemsForSession(streamKey, fn);

      const branchTokenUsage = (u: TokenUsage | null) => {
        if (u === null) return;
        if (viewedSessionIdRef.current.trim() === streamKey) {
          setTokenUsage(u);
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
          ? {
              files: opts.files.map((f) => ({
                name: f.name,
                mimeType: f.type || "application/octet-stream",
                sizeBytes: f.size,
              })),
            }
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
      const profileModel = mode === "agent" || mode === "plan";
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
              new Promise<{ name: string; data_url: string }>((resolve, reject) => {
                const reader = new FileReader();
                reader.onload = () =>
                  resolve({ name: f.name, data_url: reader.result as string });
                reader.onerror = reject;
                reader.readAsDataURL(f);
              }),
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
        let msg =
          "This chat is busy in another client. Try again in a moment.";
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
        const sh = streamShadowBySidRef.current.get(oldKey);
        streamShadowBySidRef.current.delete(oldKey);
        if (sh) {
          streamShadowBySidRef.current.set(postSessionKey, sh);
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
          ? "Empty response body"
          : `Request failed (${res.status})`;
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
      } = await consumeComposerSseReader({
        reader,
        dec,
        carry,
        assistantId,
        applyStreamItems,
        setTokenUsage: branchTokenUsage,
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
              lastCreated = readMessageCreatedAtUTC(m as Record<string, unknown>);
            }
          }
          if (!last) return false;
          ensureAssistant();
          applyStreamItems((prev) =>
            prev.map((it) =>
              it.type === "assistant_message" && it.id === assistantId
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
                it.id === assistantId &&
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
    } catch (_err: unknown) {
      // AbortError stops the stream client-side after optional POST cancel
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

  const maxContextTokens = useMemo(() => {
    const row = modelInfos.find((m) => m.id === llmModel);
    return row?.maxContextTokens || 128000;
  }, [modelInfos, llmModel]);

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
    const levels = row?.reasoningLevels ?? [];
    setLlmReasoning((prev) =>
      pickReasoningLevel({
        levels,
        cookie: readReasoningCookie(),
        sessionLevel: prev,
        modelDefault: row?.reasoningDefault ?? null,
      }),
    );
  }, [llmModel, modelInfos]);

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
      void fetch(`/coddy/sessions/${encodeURIComponent(sid)}`, {
        method: "PATCH",
        headers: { ...headers, "Content-Type": "application/json" },
        body: JSON.stringify({ selectedReasoning: lv }),
      });
    },
    [sessionId, headers],
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
      void fetch(`/coddy/sessions/${encodeURIComponent(sid)}`, {
        method: "PATCH",
        headers: { ...headers, "Content-Type": "application/json" },
        body: JSON.stringify({ selectedModelId: mid }),
      });
    },
    [sessionId, llmModelIds, headers],
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

  const openSchedulerFromNav = useCallback(() => {
    if (schedulerHttpLinked !== true) {
      return;
    }
    setSessionsOpen(false);
    setSchedulerOpen(true);
    setSchedulerEditor(null);
    setSchedulerListHash();
  }, [schedulerHttpLinked]);

  const openSettingsFromNav = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
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
    setSettingsRoute(false);
    setSessionsOpen(true);
    setHistoryHash();
  }, []);

  const shellBackdropOpen =
    sessionsOpen ||
    (schedulerOpen && schedulerHttpLinked === true) ||
    settingsRoute;

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

  const toggleRailWidth = () => {
    setRailLabelsWide((prev) => {
      const next = !prev;
      writeNavRailCookie(next ? "wide" : "narrow");
      return next;
    });
  };

  return (
    <div
      className={[
        "shell",
        viewportXL && railLabelsWide ? "shell-rail-wide" : "",
      ]
        .filter(Boolean)
        .join(" ")}
    >
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
            if (
              shouldSuppressShellBackdropClose(
                sessionDeleteBackdropSuppressUntilRef,
              )
            ) {
              return;
            }
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
                schedulerEditor?.mode === "edit"
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
              onRunJob={(jid) => void onSchedulerRunJob(jid)}
              onCancelJob={(jid) => void onSchedulerCancelJob(jid)}
              searchDraft={schedulerFilterDraft}
              onSearchDraftChange={setSchedulerFilterDraft}
              onSearchClear={() => setSchedulerFilterDraft("")}
            />

            <SchedulerJobEditorSheet
              open={schedulerHttpLinked === true && !!schedulerEditor}
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
            />
          </div>
        ) : null}

        {settingsRoute ? (
          <div className="settings-dock-cluster">
            <Settings
              onClose={onCloseSettings}
              onConfigSaved={() => setModelsEpoch((e) => e + 1)}
            />
          </div>
        ) : null}
        <ChatScreen
          title={currentTitle}
          sessionId={sessionId}
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
          onPlanDocumentRun={(slug) => {
            if (
              sessionId.trim() &&
              activeComposerSidRef.current.has(sessionId.trim())
            ) {
              return;
            }
            void streamResponses("Implement the plan.", {
              modeOverride: "agent",
              runPlanSlug: slug,
            });
          }}
          onPlanDocumentDiscard={async (itemId, slug) => {
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
          }}
          onEdit={(content, userMsgIdx) => {
            const assetNote = extractSessionAssetsXml(content);
            setDraft(stripCoddyAttachmentsForUserDisplay(content));
            setEditingUserMsgIdx(userMsgIdx);
            setEditingAssetNote(assetNote);
            setEditingFiles(parseSessionAssetFiles(content));
          }}
          {...(editingFiles.length > 0 ? { editingFiles } : {})}
          onBranchSwitch={(sid) => switchBranch(sid)}
          {...(knownSkillNames.size > 0 ? { knownSkillNames } : {})}
          onSend={(text: string, files?: File[]) => {
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
          onFetchToolCallFull={async (toolCallId: string) => {
            if (!sessionId) return;
            const det = await fetchJSON<{
              args?: string;
              result?: string;
              meta?: { status?: string; kind?: string; name?: string };
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
            if (det.data.args) patch.argsText = det.data.args;
            if (det.data.result !== undefined)
              patch.fullResultText = det.data.result;
            upsertToolCall(patch as any);
          }}
        />
      </div>
    </div>
  );
}
