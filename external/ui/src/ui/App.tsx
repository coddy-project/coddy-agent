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
import { openAIStreamErrorMessage } from "./chat/streamError";
import { parseSSEBlocks } from "./chat/sse";
import { stableMemoryCopilotItemId } from "./chat/memoryStableId";
import type { TokenUsage, TranscriptItem } from "./chat/types";
import { NavRail } from "./nav/NavRail";
import { readNavRailCookie, writeNavRailCookie } from "./nav/navRailCookie";
import { readLlmModelCookie, writeLlmModelCookie } from "./chat/llmModelCookie";
import { SessionsSidebar } from "./sessions/SessionsSidebar";
import type { SessionRow } from "./sessions/types";
import { startSuggestSessionTitle } from "./sessionTitleSuggest";
import { extractAtFileAttachments } from "./skills/draftAt";
import {
  migrateWorkspaceAtRecents,
  recordWorkspaceAtRecent,
  WORKSPACE_AT_RECENTS_NO_SESSION_KEY,
} from "./skills/workspaceAtRecents";
import { schedulerCancelJob, schedulerListJobs, schedulerRunJob } from "./scheduler/api";
import {
  parseAppHash,
  setHistoryHash,
  setSessionHashInLocation,
  setSchedulerJobHash,
  setSchedulerListHash,
  stripHistorySidebarFromHash,
} from "./scheduler/hashRoute";
import { SchedulerJobEditorSheet } from "./scheduler/SchedulerJobEditorSheet";
import { SchedulerJobsDrawer } from "./scheduler/SchedulerJobsDrawer";
import type { SchedulerInfo, SchedulerJob } from "./scheduler/types";

const HDR = "X-Coddy-Session-ID";

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
};

const PROFILE_MODES = ["agent", "plan"] as const;

type SessionStats = {
  tokenUsageTotal?: {
    inputTokens: number;
    outputTokens: number;
    totalTokens: number;
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
    recallDurationMs: row.recallDurationMs,
    persistDurationMs: row.persistDurationMs,
    ...(sumMs > 0 ? { memoryWallDurationMs: sumMs } : {}),
    persistSaved: row.persistSaved,
    persistRelativePath: row.persistRelativePath,
    persistTitle: row.persistTitle,
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
  const uidx = prev.findLastIndex((x) => x.type === "user_message");
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
  if (cur.type !== "memory_copilot") {
    return prev;
  }

  let patch = { ...cur };
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
  if (cur.type !== "memory_copilot") return prev;
  const next = [...prev];
  const patch = { ...cur };
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
  const userIdx = items.findLastIndex((x) => x.type === "user_message");
  if (userIdx < 0) return items;

  let memIdx = -1;
  let thinkingIdx = -1;
  for (let i = userIdx + 1; i < items.length; i++) {
    const it = items[i];
    if (it.type === "user_message") break;
    if (it.type === "memory_copilot") memIdx = i;
    if (it.type === "thinking" && it.status === "in_progress") {
      thinkingIdx = i;
      break;
    }
  }
  if (memIdx < 0 || thinkingIdx < 0) return items;

  const m = items[memIdx];
  if (m.type !== "memory_copilot") return items;

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
  const [sessionId, setSessionId] = useState("");
  const [sessions, setSessions] = useState<SessionRow[]>([]);
  const [sessionsCursor, setSessionsCursor] = useState<string | null>(null);
  const sessionsCursorRef = useRef<string | null>(null);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  const [items, setItems] = useState<TranscriptItem[]>([]);
  const [draft, setDraft] = useState("");
  const [tokenUsage, setTokenUsage] = useState<TokenUsage | null>(null);
  const tokenBaselineRef = useRef<{
    input: number;
    output: number;
    total: number;
  }>({ input: 0, output: 0, total: 0 });
  const inFlightRef = useRef(false);
  const generationAbortRef = useRef<AbortController | null>(null);
  const activeStreamSidRef = useRef("");
  const streamingAssistantIdRef = useRef("");
  const [generating, setGenerating] = useState(false);
  const reasoningDurationMsByContentRef = useRef<Map<string, number>>(
    new Map(),
  );
  const [modelInfos, setModelInfos] = useState<ModelInfo[]>([]);
  const [sessionsOpen, setSessionsOpen] = useState(false);
  /** null until first probe of /coddy/scheduler/jobs; false when route returns 404 (binary without scheduler). */
  const [schedulerHttpLinked, setSchedulerHttpLinked] = useState<
    boolean | null
  >(null);
  const [schedulerOpen, setSchedulerOpen] = useState(false);
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
  const [drawersWide, setDrawersWide] = useState(false);
  const [railLabelsWide, setRailLabelsWide] = useState(false);
  const [mode, setMode] = useState<string>("agent");
  const [llmModelIds, setLlmModelIds] = useState<string[]>([]);
  const [llmModel, setLlmModel] = useState("");
  const [describePreview, setDescribePreview] = useState<{
    sessionId: string;
    title: string;
  } | null>(null);
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
      setSessionId(p.sessionId);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setSessionsOpen(!!p.historyOpen);
      return;
    }
    if (p.branch === "history") {
      setSessionsOpen(true);
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      return;
    }
    if (p.branch === "scheduler") {
      if (schedulerHttpLinked === false) {
        setSchedulerOpen(false);
        setSchedulerEditor(null);
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
        return;
      }
      if (schedulerHttpLinked === null) {
        return;
      }
      setSchedulerOpen(true);
      setSessionsOpen(!!p.historyOpen && drawersWide);
      if (p.jobId) {
        setSchedulerEditor({ mode: "edit", jobId: p.jobId });
      } else {
        setSchedulerEditor(null);
      }
      return;
    }
    setSessionId("");
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setSessionsOpen(!!p.historyOpen);
  }, [schedulerHttpLinked, sessionId, drawersWide]);

  const openSessionFromRoute = useCallback(
    (id: string, opts?: { historySidebar?: boolean }) => {
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setSessionHashInLocation(id, opts);
      setSessionId(id);
    },
    [],
  );

  const clearSessionRoute = useCallback(() => {
    setSchedulerOpen(false);
    setSchedulerEditor(null);
    setSessionHashInLocation("");
    setSessionId("");
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
  }, [sessionId]);

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
        }))
        .filter((d) => d.id);
      const rows: ModelInfo[] = raw.map((d) => {
        const m: ModelInfo = { id: d.id, ownedBy: d.ownedBy };
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
      const fromCookie = readLlmModelCookie();
      let next = "";
      if (fromCookie && backends.includes(fromCookie)) {
        next = fromCookie;
      } else if (defaultYaml && backends.includes(defaultYaml)) {
        next = defaultYaml;
      } else if (backends.length > 0 && backends[0]) {
        next = backends[0];
      }
      setLlmModel(next);
    })();
  }, []);

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

  useEffect(() => {
    const mq = window.matchMedia("(min-width: 1200px)");
    const apply = () => setDrawersWide(mq.matches);
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
        const hp = parseAppHash();
        const hist =
          hp.branch === "scheduler" && hp.historyOpen;
        setSchedulerListHash({ historySidebar: hist });
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
    if (!sessionsOpen) {
      return;
    }
    void loadSessionsList(true);
  }, [sessionsOpen, sessionFilterQ, loadSessionsList]);

  async function loadMessages(idOverride?: string): Promise<boolean> {
    const sid = (idOverride ?? sessionId).trim();
    if (!sid) {
      setItems([]);
      return false;
    }
    const res = await fetchJSON<{
      messages: Array<any>;
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
      setItems([]);
      return false;
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
        });
      }
    };
    const toolIdx = new Map<string, number>();
    let userTurnIdx = 0;
    for (const m of res.data.messages || []) {
      const role = (m.role || "").trim();
      if (role === "user") {
        userTurnIdx++;
        const cat = readMessageCreatedAtUTC(m as Record<string, unknown>);
        next.push({
          id: newId("u"),
          type: "user_message",
          content: m.content || "",
          ...(cat ? { createdAtUtc: cat } : {}),
        });
        const mt = memByTurn.get(userTurnIdx);
        if (mt) {
          next.push(memoryTranscriptFromApi(mt));
        }
        pushUiNoticesForTurn(userTurnIdx);
        continue;
      }
      if (role === "assistant") {
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
            id: newId("r"),
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
            id: newId("a"),
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
            id: newId("t"),
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
            id: newId("t"),
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
        if (row.argsPreview) merged.argsText = row.argsPreview;
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
    setItems(next);
    return next.some((it) => it.type === "assistant_message");
  }

  function pickSession(id: string) {
    reasoningDurationMsByContentRef.current = new Map();
    openSessionFromRoute(id, {
      historySidebar: sessionsOpen,
    });
  }

  function goHome() {
    clearSessionRoute();
    setItems([]);
    setDraft("");
    setTokenUsage(null);
    setDescribePreview(null);
    reasoningDurationMsByContentRef.current = new Map();
  }

  async function deleteSession(id: string) {
    const ok = window.confirm("Delete chat");
    if (!ok) {
      return;
    }
    await fetch(`/coddy/sessions/${encodeURIComponent(id)}`, {
      method: "DELETE",
      headers,
    });
    setSessions((prev) => prev.filter((s) => s.id !== id));
    if (id === sessionId) {
      setSchedulerOpen(false);
      setSchedulerEditor(null);
      setSessionId("");
      setItems([]);
      setDraft("");
      setTokenUsage(null);
      setDescribePreview(null);
      reasoningDurationMsByContentRef.current = new Map();
      if (sessionsOpen) {
        setHistoryHash();
      } else {
        setSessionHashInLocation("");
      }
      return;
    }
    await loadSessionsList(true);
  }

  useEffect(() => {
    if (!sessionId) {
      setItems([]);
      void loadSessionsList(true);
      return;
    }
    if (inFlightRef.current) {
      return;
    }
    setTokenUsage(null);
    tokenBaselineRef.current = { input: 0, output: 0, total: 0 };
    void (async () => {
      const list = await loadSessionsList(true);
      const exists = !!list?.some((s) => s.id === sessionId);
      if (exists) {
        const statsRes = await fetchJSON<{ stats?: SessionStats | null }>(
          `/coddy/sessions/${encodeURIComponent(sessionId)}/stats`,
          { headers },
        );
        if (statsRes.ok && statsRes.data?.stats?.tokenUsageTotal) {
          const t = statsRes.data.stats.tokenUsageTotal;
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
        await loadMessages();
      } else {
        setItems([]);
      }
    })();
  }, [sessionId]);

  function upsertToolCall(
    update: Partial<Extract<TranscriptItem, { type: "tool_call" }>> & {
      toolCallId: string;
    },
  ) {
    setItems((prev) => {
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

  async function streamResponses(text: string) {
    inFlightRef.current = true;
    const abortCtl = new AbortController();
    generationAbortRef.current = abortCtl;
    setGenerating(true);
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
        openSessionFromRoute(sid);
      }
      sidEffective = sid;
      let latestPreviewSid = sid;

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
      };
      const assistantId = newId("a");
      assistantStreamId = assistantId;
      streamingAssistantIdRef.current = assistantId;
      activeStreamSidRef.current = sid;
      setItems((prev) => [...prev, userItem]);
      setTokenUsage(null);

      const reqBody: Record<string, unknown> = {
        model: mode || "agent",
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
      const yamlSel = llmModel.trim();
      if (yamlSel) {
        reqBody.metadata = { model: yamlSel };
      }
      const res = await fetch("/v1/responses", {
        method: "POST",
        headers: { ...hdrs, "Content-Type": "application/json" },
        body: JSON.stringify(reqBody),
        signal: abortCtl.signal,
      });

      const sidHdr = res.headers.get(HDR);
      if (sidHdr && sidHdr !== sid) {
        migrateWorkspaceAtRecents(sid, sidHdr);
        sidEffective = sidHdr;
        openSessionFromRoute(sidHdr);
        setDescribePreview((p) =>
          p?.sessionId === sid ? { ...p, sessionId: sidHdr } : p,
        );
        setSessions((prev) =>
          prev.map((s) => (s.id === sid ? { ...s, id: sidHdr } : s)),
        );
      }
      latestPreviewSid = sidEffective;
      activeStreamSidRef.current = sidEffective;
      releaseSessionId?.(sidEffective);

      if (!res.ok || !res.body) {
        const msg = !res.body
          ? "Empty response body"
          : `Request failed (${res.status})`;
        setItems((prev) => [
          ...prev,
          {
            id: newId("s"),
            type: "system_notice",
            level: "error" as const,
            message: msg,
          },
        ]);
        completedNormally = true;
        return;
      }

      const reader = res.body.getReader();
      const dec = new TextDecoder();
      const carry = { buf: "" };

      const toolQueue: Array<
        Partial<Extract<TranscriptItem, { type: "tool_call" }>> & {
          toolCallId: string;
        }
      > = [];
      let raf = 0;
      const flushToolQueue = () => {
        raf = 0;
        if (toolQueue.length === 0) return;
        const pending = toolQueue.splice(0, toolQueue.length);
        setItems((prev) => {
          let next = prev;
          for (const upd of pending) {
            const idx = next.findIndex(
              (x) => x.type === "tool_call" && x.toolCallId === upd.toolCallId,
            );
            if (idx < 0) {
              const itBase: Extract<TranscriptItem, { type: "tool_call" }> = {
                id: newId("t"),
                type: "tool_call",
                toolCallId: upd.toolCallId,
                status: (upd.status as any) || "pending",
              };
              const it: Extract<TranscriptItem, { type: "tool_call" }> = {
                ...itBase,
              };
              if (upd.title !== undefined) it.title = upd.title;
              if (upd.kind !== undefined) it.kind = upd.kind;
              if (upd.argsText !== undefined) it.argsText = upd.argsText;
              if (upd.resultText !== undefined) it.resultText = upd.resultText;
              if (upd.resultWasTruncated !== undefined)
                it.resultWasTruncated = upd.resultWasTruncated;
              if (upd.fullResultText !== undefined)
                it.fullResultText = upd.fullResultText;
              if (upd.startedAtMs !== undefined)
                it.startedAtMs = upd.startedAtMs;
              if (upd.finishedAtMs !== undefined)
                it.finishedAtMs = upd.finishedAtMs;
              if (upd.durationMs !== undefined) it.durationMs = upd.durationMs;
              const aIdx = next.findIndex(
                (x) => x.type === "assistant_message" && x.id === assistantId,
              );
              if (aIdx >= 0) {
                const arr = next === prev ? [...next] : next;
                arr.splice(aIdx, 0, it);
                next = arr;
              } else {
                next = [...next, it];
              }
              continue;
            }
            const arr = next === prev ? [...next] : next;
            const cur = arr[idx] as Extract<
              TranscriptItem,
              { type: "tool_call" }
            >;
            const nextStarted =
              upd.startedAtMs !== undefined ? upd.startedAtMs : cur.startedAtMs;
            const nextFinished =
              upd.finishedAtMs !== undefined
                ? upd.finishedAtMs
                : cur.finishedAtMs;
            const nextDuration =
              upd.durationMs !== undefined
                ? upd.durationMs
                : nextStarted && nextFinished
                  ? Math.max(0, nextFinished - nextStarted)
                  : cur.durationMs;
            const merged: Extract<TranscriptItem, { type: "tool_call" }> = {
              ...cur,
              status: (upd.status as any) || cur.status,
            };
            if (nextStarted !== undefined) merged.startedAtMs = nextStarted;
            if (nextFinished !== undefined) merged.finishedAtMs = nextFinished;
            if (nextDuration !== undefined) merged.durationMs = nextDuration;
            if (upd.title !== undefined) merged.title = upd.title;
            if (upd.kind !== undefined) merged.kind = upd.kind;
            if (upd.argsText !== undefined) merged.argsText = upd.argsText;
            if (upd.resultText !== undefined)
              merged.resultText = upd.resultText;
            if (upd.resultWasTruncated !== undefined)
              merged.resultWasTruncated = upd.resultWasTruncated;
            if (upd.fullResultText !== undefined)
              merged.fullResultText = upd.fullResultText;
            arr[idx] = merged;
            next = arr;
          }
          return next;
        });
      };
      const scheduleToolFlush = () => {
        if (raf) return;
        raf = window.requestAnimationFrame(flushToolQueue);
      };

      const ensureAssistant = (
        patch?: Partial<Extract<TranscriptItem, { type: "assistant_message" }>>,
      ) => {
        setItems((prev) => {
          const idx = prev.findIndex(
            (x) => x.type === "assistant_message" && x.id === assistantId,
          );
          if (idx < 0) {
            const base: Extract<TranscriptItem, { type: "assistant_message" }> =
              {
                id: assistantId,
                type: "assistant_message",
                content: "",
                streaming: true,
              };
            return [...prev, { ...base, ...(patch || {}) }];
          }
          if (!patch) return prev;
          const next = [...prev];
          const cur = next[idx] as Extract<
            TranscriptItem,
            { type: "assistant_message" }
          >;
          next[idx] = { ...cur, ...patch };
          return next;
        });
      };

      let activeThinkingId: string | null = null;
      let activeThinkingStarted = 0;
      const appendThinking = (delta: string) => {
        const freezeAt = Date.now();
        if (!activeThinkingId) {
          activeThinkingId = newId("r");
          activeThinkingStarted = freezeAt;
        }
        const id = activeThinkingId;
        setItems((prev) => {
          const known = prev.some(
            (it) => it.type === "thinking" && it.id === id,
          );
          let next = known
            ? prev
            : [
                ...prev,
                {
                  id,
                  type: "thinking",
                  status: "in_progress",
                  content: "",
                  startedAtMs: freezeAt,
                },
              ];
          next = next.map((it) =>
            it.type === "thinking" && it.id === id
              ? { ...it, content: it.content + delta }
              : it,
          );
          return freezeMemoryWallWhenThinkingAfterRecall(next, freezeAt);
        });
      };
      const finishThinking = () => {
        if (!activeThinkingId) return;
        const id = activeThinkingId;
        const dur = Math.max(0, Date.now() - activeThinkingStarted);
        setItems((prev) =>
          prev.map((it) => {
            if (it.type !== "thinking" || it.id !== id) {
              return it;
            }
            const nextIt = {
              ...it,
              status: "completed" as const,
              durationMs: dur,
            };
            const dk = reasoningDurationCacheKey(nextIt.content);
            if (dk.length > 0) {
              reasoningDurationMsByContentRef.current.set(dk, dur);
            }
            return nextIt;
          }),
        );
        activeThinkingId = null;
      };

      const syncAssistantFromServer = async () => {
        try {
          const res = await fetchJSON<{ messages: Array<any> }>(
            `/coddy/sessions/${encodeURIComponent(sidEffective)}/messages`,
            { headers: { [HDR]: sidEffective } },
          );
          if (!res.ok || !res.data?.messages) return false;
          let last = "";
          let lastCreated: string | undefined;
          for (const m of res.data.messages) {
            if ((m.role || "").trim() !== "assistant") continue;
            const c = (m.content || "").trim();
            if (c) {
              last = c;
              lastCreated = readMessageCreatedAtUTC(m as Record<string, unknown>);
            }
          }
          if (!last) return false;
          ensureAssistant();
          setItems((prev) =>
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

      let sawDone = false;
      let streamErrorMessage: string | null = null;
      let streamHalted = false;
      while (true) {
        const step = await reader.read();
        if (step.done) {
          break;
        }
        const events = parseSSEBlocks(
          dec.decode(step.value, { stream: true }),
          carry,
        );
        for (const ev of events) {
          if (ev.data === "[DONE]") {
            sawDone = true;
            break;
          }

          if (!ev.event) {
            let delta: unknown;
            try {
              delta = JSON.parse(ev.data);
            } catch {
              continue;
            }
            const sseErr = openAIStreamErrorMessage(delta);
            if (sseErr) {
              streamErrorMessage = sseErr;
              streamHalted = true;
              try {
                await reader.cancel();
              } catch {
                // ignore
              }
              break;
            }
            const d = delta as {
              choices?: Array<{
                delta?: { content?: unknown; reasoning_content?: unknown };
              }>;
            };
            try {
              const contentDelta = d.choices?.[0]?.delta?.content;
              const c = typeof contentDelta === "string" ? contentDelta : "";
              const rRaw = d.choices?.[0]?.delta?.reasoning_content || "";
              const r = typeof rRaw === "string" ? rRaw : "";
              if (r) {
                appendThinking(r);
              }
              if (c) {
                if (/\S/.test(c)) {
                  finishThinking();
                }
                ensureAssistant();
                setItems((prev) =>
                  prev.map((it) =>
                    it.type === "assistant_message" && it.id === assistantId
                      ? { ...it, content: it.content + c }
                      : it,
                  ),
                );
              }
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "token_usage") {
            try {
              const u = JSON.parse(ev.data) as TokenUsage;
              const merged: TokenUsage = {
                inputTokens:
                  tokenBaselineRef.current.input + (u.inputTokens || 0),
                outputTokens:
                  tokenBaselineRef.current.output + (u.outputTokens || 0),
                totalTokens:
                  tokenBaselineRef.current.total + (u.totalTokens || 0),
              };
              setTokenUsage(merged);
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "memory_phase") {
            try {
              const raw = JSON.parse(ev.data) as MemoryPhaseEvt;
              setItems((prev) =>
                applyMemoryPhaseToItems(prev, {
                  memoryRowId: String(raw.memoryRowId || ""),
                  phase: String(raw.phase || ""),
                  status: String(raw.status || ""),
                  ...(typeof raw.userTurnIndex === "number"
                    ? { userTurnIndex: raw.userTurnIndex }
                    : {}),
                  ...(typeof raw.durationMs === "number"
                    ? { durationMs: raw.durationMs }
                    : {}),
                  ...(typeof raw.persistSaved === "boolean"
                    ? { persistSaved: raw.persistSaved }
                    : {}),
                  ...(raw.persistRelativePath
                    ? { persistRelativePath: raw.persistRelativePath }
                    : {}),
                  ...(raw.persistTitle
                    ? { persistTitle: raw.persistTitle }
                    : {}),
                  ...(raw.persistSavedBody
                    ? { persistSavedBody: raw.persistSavedBody }
                    : {}),
                  ...(Array.isArray(raw.recallReadPaths) &&
                  raw.recallReadPaths.length > 0
                    ? { recallReadPaths: raw.recallReadPaths }
                    : {}),
                }),
              );
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "memory_chunk") {
            try {
              const raw = JSON.parse(ev.data) as MemoryChunkEvt;
              setItems((prev) =>
                applyMemoryChunkToItems(prev, {
                  memoryRowId: String(raw.memoryRowId || ""),
                  phase: String(raw.phase || ""),
                  kind: String(raw.kind || ""),
                  delta: typeof raw.delta === "string" ? raw.delta : "",
                }),
              );
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "tool_call") {
            try {
              finishThinking();
              const t = JSON.parse(ev.data) as ToolCallUpdate;
              const now = Date.now();
              const patch: Partial<
                Extract<TranscriptItem, { type: "tool_call" }>
              > & { toolCallId: string } = {
                toolCallId: t.toolCallId,
                status: (t.status as any) || "pending",
                startedAtMs: now,
              };
              if (t.title !== undefined) patch.title = t.title;
              if (t.kind !== undefined) patch.kind = t.kind;
              toolQueue.push(patch);
              scheduleToolFlush();
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === "tool_call_update") {
            try {
              const u = JSON.parse(ev.data) as ToolCallStatusUpdate;
              const status = (u.status as any) || "in_progress";
              const text0 = u.content?.[0]?.content?.text || "";
              const now = Date.now();
              if (status === "in_progress" && text0) {
                toolQueue.push({
                  toolCallId: u.toolCallId,
                  status,
                  argsText: text0,
                  startedAtMs: now,
                });
                scheduleToolFlush();
              } else if (
                (status === "completed" ||
                  status === "failed" ||
                  status === "cancelled") &&
                text0
              ) {
                const trunc = toolSseShowsTruncatedPreview(u);
                toolQueue.push({
                  toolCallId: u.toolCallId,
                  status,
                  resultText: text0,
                  finishedAtMs: now,
                  ...(trunc ? { resultWasTruncated: true as const } : {}),
                });
                scheduleToolFlush();
              } else {
                if (
                  status === "completed" ||
                  status === "failed" ||
                  status === "cancelled"
                ) {
                  toolQueue.push({
                    toolCallId: u.toolCallId,
                    status,
                    finishedAtMs: now,
                  });
                } else {
                  toolQueue.push({
                    toolCallId: u.toolCallId,
                    status,
                    startedAtMs: now,
                  });
                }
                scheduleToolFlush();
              }
            } catch {
              // ignore
            }
            continue;
          }
        }
        if (streamHalted) {
          break;
        }
        if (sawDone) {
          break;
        }
      }
      if (sawDone) {
        try {
          await reader.cancel();
        } catch {
          // ignore
        }
      }

      if (carry.buf.trim()) {
        const tailEvents = parseSSEBlocks("\n\n", carry);
        for (const ev of tailEvents) {
          if (ev.data === "[DONE]") continue;
          if (!ev.event) {
            let delta: unknown;
            try {
              delta = JSON.parse(ev.data);
            } catch {
              continue;
            }
            const sseErr = openAIStreamErrorMessage(delta);
            if (sseErr) {
              streamErrorMessage = sseErr;
              break;
            }
            const d = delta as {
              choices?: Array<{
                delta?: { content?: unknown; reasoning_content?: unknown };
              }>;
            };
            try {
              const contentDelta = d.choices?.[0]?.delta?.content;
              const c = typeof contentDelta === "string" ? contentDelta : "";
              const rRaw = d.choices?.[0]?.delta?.reasoning_content || "";
              const r = typeof rRaw === "string" ? rRaw : "";
              if (r) {
                appendThinking(r);
              }
              if (c) {
                if (/\S/.test(c)) {
                  finishThinking();
                }
                ensureAssistant();
                setItems((prev) =>
                  prev.map((it) =>
                    it.type === "assistant_message" && it.id === assistantId
                      ? { ...it, content: it.content + c }
                      : it,
                  ),
                );
              }
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "memory_phase") {
            try {
              const raw = JSON.parse(ev.data) as MemoryPhaseEvt;
              setItems((prev) =>
                applyMemoryPhaseToItems(prev, {
                  memoryRowId: String(raw.memoryRowId || ""),
                  phase: String(raw.phase || ""),
                  status: String(raw.status || ""),
                  ...(typeof raw.userTurnIndex === "number"
                    ? { userTurnIndex: raw.userTurnIndex }
                    : {}),
                  ...(typeof raw.durationMs === "number"
                    ? { durationMs: raw.durationMs }
                    : {}),
                  ...(typeof raw.persistSaved === "boolean"
                    ? { persistSaved: raw.persistSaved }
                    : {}),
                  ...(raw.persistRelativePath
                    ? { persistRelativePath: raw.persistRelativePath }
                    : {}),
                  ...(raw.persistTitle
                    ? { persistTitle: raw.persistTitle }
                    : {}),
                  ...(raw.persistSavedBody
                    ? { persistSavedBody: raw.persistSavedBody }
                    : {}),
                  ...(Array.isArray(raw.recallReadPaths) &&
                  raw.recallReadPaths.length > 0
                    ? { recallReadPaths: raw.recallReadPaths }
                    : {}),
                }),
              );
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "memory_chunk") {
            try {
              const raw = JSON.parse(ev.data) as MemoryChunkEvt;
              setItems((prev) =>
                applyMemoryChunkToItems(prev, {
                  memoryRowId: String(raw.memoryRowId || ""),
                  phase: String(raw.phase || ""),
                  kind: String(raw.kind || ""),
                  delta: typeof raw.delta === "string" ? raw.delta : "",
                }),
              );
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "tool_call") {
            try {
              finishThinking();
              const t = JSON.parse(ev.data) as ToolCallUpdate;
              const now = Date.now();
              const patch: Partial<
                Extract<TranscriptItem, { type: "tool_call" }>
              > & { toolCallId: string } = {
                toolCallId: t.toolCallId,
                status: (t.status as any) || "pending",
                startedAtMs: now,
              };
              if (t.title !== undefined) patch.title = t.title;
              if (t.kind !== undefined) patch.kind = t.kind;
              toolQueue.push(patch);
              scheduleToolFlush();
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === "tool_call_update") {
            try {
              const u = JSON.parse(ev.data) as ToolCallStatusUpdate;
              const status = (u.status as any) || "in_progress";
              const text0 = u.content?.[0]?.content?.text || "";
              const now = Date.now();
              if (status === "in_progress" && text0) {
                toolQueue.push({
                  toolCallId: u.toolCallId,
                  status,
                  argsText: text0,
                  startedAtMs: now,
                });
                scheduleToolFlush();
              } else if (
                (status === "completed" ||
                  status === "failed" ||
                  status === "cancelled") &&
                text0
              ) {
                const trunc = toolSseShowsTruncatedPreview(u);
                toolQueue.push({
                  toolCallId: u.toolCallId,
                  status,
                  resultText: text0,
                  finishedAtMs: now,
                  ...(trunc ? { resultWasTruncated: true as const } : {}),
                });
                scheduleToolFlush();
              } else {
                if (
                  status === "completed" ||
                  status === "failed" ||
                  status === "cancelled"
                ) {
                  toolQueue.push({
                    toolCallId: u.toolCallId,
                    status,
                    finishedAtMs: now,
                  });
                } else {
                  toolQueue.push({
                    toolCallId: u.toolCallId,
                    status,
                    startedAtMs: now,
                  });
                }
                scheduleToolFlush();
              }
            } catch {
              // ignore
            }
            continue;
          }
        }
      }

      if (streamErrorMessage) {
        flushToolQueue();
        finishThinking();
        const errText = streamErrorMessage;
        setItems((prev) => {
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
      let ok = await syncAssistantFromServer();
      for (let i = 0; i < 10 && !ok; i++) {
        await new Promise((r) => setTimeout(r, 500));
        ok = await syncAssistantFromServer();
      }
      await loadMessages(sidEffective);
      completedNormally = true;
    } catch (_err: unknown) {
      // AbortError stops the stream client-side after optional POST cancel
    } finally {
      streamingAssistantIdRef.current = "";
      activeStreamSidRef.current = "";
      generationAbortRef.current = null;
      setGenerating(false);
      if (!completedNormally && assistantStreamId) {
        const aid = assistantStreamId;
        const now = Date.now();
        setItems((prev) =>
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
          }),
        );
        void loadMessages(sidEffective);
        void loadSessionsList(true);
      }
      releaseSessionId?.(sidEffective);
      inFlightRef.current = false;
    }
  }

  function stopActiveGeneration(): void {
    const sid = activeStreamSidRef.current;
    const ctl = generationAbortRef.current;
    if (!ctl) return;
    if (sid.trim()) {
      void fetch(`/coddy/sessions/${encodeURIComponent(sid)}/cancel`, {
        method: "POST",
        headers: { [HDR]: sid },
      });
    }
    ctl.abort();
  }

  const maxContextTokens = useMemo(() => {
    const row = modelInfos.find((m) => m.id === llmModel);
    return row?.maxContextTokens || 128000;
  }, [modelInfos, llmModel]);

  const onLlmModelChange = useCallback((id: string) => {
    setLlmModel(id);
    writeLlmModelCookie(id);
  }, []);

  const contextPct = useMemo(() => {
    if (!tokenUsage || !maxContextTokens) return 0;
    return Math.min(
      100,
      Math.max(0, (tokenUsage.totalTokens / maxContextTokens) * 100),
    );
  }, [tokenUsage, maxContextTokens]);

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
    const hist = drawersWide && sessionsOpen;
    if (!drawersWide) {
      setSessionsOpen(false);
    }
    setSchedulerOpen(true);
    setSchedulerEditor(null);
    setSchedulerListHash({ historySidebar: hist });
  }, [schedulerHttpLinked, drawersWide, sessionsOpen]);

  const onOpenHistoryFromNav = useCallback(() => {
    setSessionsOpen(true);
    if (drawersWide && schedulerOpen && schedulerHttpLinked === true) {
      if (schedulerEditor?.mode === "edit") {
        setSchedulerJobHash(schedulerEditor.jobId, { historySidebar: true });
      } else {
        setSchedulerListHash({ historySidebar: true });
      }
      return;
    }
    if (!drawersWide && schedulerOpen) {
      setSchedulerOpen(false);
      setSchedulerEditor(null);
    }
    setHistoryHash();
  }, [
    drawersWide,
    schedulerOpen,
    schedulerHttpLinked,
    schedulerEditor,
  ]);

  const shellBackdropOpen =
    sessionsOpen || (schedulerOpen && schedulerHttpLinked === true);

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

  const historyDrawerBesideScheduler =
    drawersWide &&
    sessionsOpen &&
    schedulerOpen &&
    schedulerHttpLinked === true;

  const sessionPanelShared = {
    sessionId,
    sessions,
    error: sessionsError,
    open: sessionsOpen,
    className: historyDrawerBesideScheduler
      ? "sessions-drawer-beside-scheduler"
      : undefined,
    onClose: () => {
      setSessionsOpen(false);
      const p = parseAppHash();
      if (p.branch === "history") {
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
        canWidenRail={viewportXL}
        railLabelsWide={railLabelsWide}
        onToggleRailLabels={toggleRailWidth}
      />

      <div
        className={[
          "shell-main",
          historyDrawerBesideScheduler ? "shell-history-beside-scheduler" : "",
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
            className="scheduler-dock-cluster"
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
                setSchedulerEditor({ mode: "create" });
                const hp = parseAppHash();
                setSchedulerListHash({
                  historySidebar:
                    hp.branch === "scheduler" && hp.historyOpen,
                });
              }}
              onOpenJob={(jid) => {
                setSchedulerEditor({ mode: "edit", jobId: jid });
                const hp = parseAppHash();
                const hist =
                  hp.branch === "scheduler" && hp.historyOpen;
                setSchedulerJobHash(jid, { historySidebar: hist });
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
                const hp = parseAppHash();
                const hist =
                  hp.branch === "scheduler" && hp.historyOpen;
                setSchedulerListHash({ historySidebar: hist });
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

        <ChatScreen
          title={currentTitle}
          sessionId={sessionId}
          onTitleSave={(t: string) => void saveSessionTitle(sessionId, t)}
          items={items}
          draft={draft}
          tokenUsage={tokenUsage}
          contextPct={contextPct}
          maxContextTokens={maxContextTokens}
          mode={mode}
          modes={[...PROFILE_MODES]}
          {...(llmModelIds.length > 0
            ? { llmModels: llmModelIds, llmModel, onLlmModelChange }
            : {})}
          onModeChange={setMode}
          onDraftChange={setDraft}
          generating={generating}
          onStop={() => stopActiveGeneration()}
          onSend={(text: string) => {
            if (generating) return;
            setDraft("");
            void streamResponses(text);
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
