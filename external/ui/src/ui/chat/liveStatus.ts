/**
 * Derives the live status shown next to the typing dots while a turn is running:
 * what phase the agent is in right now, and since when.
 *
 * The line carries the phase and *nothing it acts on* — no path, no command, no url.
 * What a step acts on is named once, by the transcript row above the line, so every
 * phrase here has to read as a complete phrase on its own (DESIGN.md, States → Working).
 *
 * Everything comes from the transcript the SPA already holds — no backend or SSE change.
 * The module stays free of React and of locale state: it returns i18n *keys*, so the
 * component owns translation and the tests can assert on locale-independent values.
 *
 * The console TUI carries the same phrase table in Go (external/cli/status.go). The two
 * cannot share code across the language boundary, so a tool added to one belongs in the
 * other as well.
 */

import { parseMcpToolName } from "../messages/toolDisplayName";
import type { TranscriptItem } from "./types";

export type LiveStatusKind =
  | "permission"
  | "question"
  | "tool"
  | "thinking"
  | "memory"
  | "writing"
  | "waiting";

export type LiveStatus = {
  kind: LiveStatusKind;
  /** i18n key of the verb phrase. Takes slots only through keyParams. */
  key: string;
  /**
   * What the phrase's {slots} resolve to, when it has any. Only a call Coddy does
   * not define needs them: its own name is the only thing the phrase can say.
   */
  keyParams?: Record<string, string>;
  /**
   * What this step is, for telling one step from the next. Nothing renders it: the
   * phrase is all the reader sees, and two calls in a row can share it, so without
   * an id of its own the second would go on counting the first one's clock.
   */
  step?: string;
  /** Wall clock ms to count elapsed from; omitted when the start is unknown. */
  startedAtMs?: number;
  /**
   * When the turn's user message was created. It stands in for the turn's start until
   * the server's turn_progress names the real one (an older server never does). A
   * follow-up read from the message queue is a user message too, so on that fallback
   * the clock restarts from the follow-up.
   */
  turnStartedAtMs?: number;
};

/**
 * Whether a step shows a clock of its own after the phrase. The turn's clock leads the
 * line and covers the model's own phases - waiting, thinking, writing - so only a step
 * that runs something other than the model (a tool call, the memory run) keeps a second
 * one. The two operator gates never count: nothing runs while the operator decides.
 */
export function stepShowsItsOwnClock(
  kind: LiveStatusKind | undefined,
): boolean {
  return kind === "tool" || kind === "memory";
}

/** Waiting longer than this reads as "slower than usual". */
export const WAITING_SLOW_MS = 15_000;
/** Waiting longer than this reads as "still nothing from the server". */
export const WAITING_STUCK_MS = 60_000;

const WAITING_KEY = "status.waitingModel";

/**
 * Which waiting phrase fits the elapsed time. Split out of deriveLiveStatus because only
 * the ticking component knows how long the wait has been running.
 */
export function waitingStatusKey(elapsedMs: number): string {
  if (!Number.isFinite(elapsedMs) || elapsedMs < WAITING_SLOW_MS) {
    return WAITING_KEY;
  }
  if (elapsedMs < WAITING_STUCK_MS) {
    return "status.waitingSlow";
  }
  return "status.waitingStuck";
}

/**
 * Present-progressive phrase key for a backend tool id. Tool ids are the raw registry
 * names (internal/tools, internal/agent/toolsets.go); unknown ones fall back to a
 * generic phrase. A tool an MCP server serves takes that generic phrase too, since
 * nothing here knows what it does, and deriveLiveStatus swaps in the one phrase whose
 * slots name the server and the tool.
 *
 * Every phrase behind these keys stands on its own: the line renders the phrase and
 * nothing else, so a key whose value reads as a fragment ("Reading") is a bug the
 * dictionary test catches (external/ui/src/ui/i18n/statusPhrases.test.ts).
 */
export function statusKeyForTool(toolName: string): string {
  const n = (toolName || "").trim().toLowerCase();
  if (!n) {
    return "status.tool";
  }
  if (n.startsWith("coddy_todo_")) {
    return n.endsWith("_read") ? "status.planRead" : "status.plan";
  }
  if (n.startsWith("coddy_scheduler_")) {
    return "status.schedule";
  }
  if (n.startsWith("coddy_memory_")) {
    return "status.memory";
  }
  if (n.startsWith("config_")) {
    return "status.config";
  }
  // The background family runs long by design - background_wait alone parks for up to a
  // minute - so a generic phrase here reads as a frozen row rather than as work.
  if (n.startsWith("background_")) {
    switch (n) {
      case "background_wait":
        return "status.backgroundWait";
      case "background_output":
        return "status.backgroundOutput";
      case "background_stop":
        return "status.backgroundStop";
      case "background_reap":
        return "status.backgroundReap";
      default:
        return "status.backgroundList";
    }
  }
  switch (n) {
    case "read":
      return "status.read";
    case "list_dir":
    case "print_tree":
      return "status.list";
    case "grep":
    case "glob":
      return "status.search";
    case "edit":
    case "apply_patch":
      return "status.edit";
    case "write":
      return "status.write";
    case "run_command":
      return "status.run";
    case "ssh_run_command":
      return "status.runRemote";
    case "spawn_agent":
      return "status.spawnAgent";
    case "mkdir":
      return "status.createDir";
    case "touch":
      return "status.touch";
    case "mv":
      return "status.move";
    case "rm":
    case "rmdir":
      return "status.delete";
    case "websearch":
      return "status.webSearch";
    case "coddy_docs_search":
      return "status.docsSearch";
    case "coddy_docs_read":
      return "status.docsRead";
    case "webfetch":
      return "status.webFetch";
    case "http_request":
      return "status.httpRequest";
    case "load_skill":
      return "status.skill";
    case "plan_write":
    case "plan_exit":
      return "status.plan";
    case "plan_read":
    case "plan_list":
      return "status.planRead";
    case "question":
      return "status.awaitingAnswer";
    default:
      return "status.tool";
  }
}

/** Elapsed as whole seconds: 0s, 59s, 1m 05s, 59m 59s, 1h 00m. */
export function formatElapsedSeconds(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) {
    return "";
  }
  const total = Math.floor(ms / 1000);
  if (total < 60) {
    return `${total}s`;
  }
  const minutes = Math.floor(total / 60);
  if (minutes < 60) {
    return `${minutes}m ${String(total % 60).padStart(2, "0")}s`;
  }
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, "0")}m`;
}

/** RFC3339 to epoch ms, ignoring future timestamps (server/client clock skew). */
function parseCreatedAt(raw: string | undefined): number | undefined {
  if (!raw) {
    return undefined;
  }
  const at = Date.parse(raw);
  if (!Number.isFinite(at) || at > Date.now()) {
    return undefined;
  }
  return at;
}

const PREPARING: LiveStatus = { kind: "waiting", key: WAITING_KEY };

type ToolItem = Extract<TranscriptItem, { type: "tool_call" }>;
type ThinkingItem = Extract<TranscriptItem, { type: "thinking" }>;
type MemoryItem = Extract<TranscriptItem, { type: "memory_run" }>;

/**
 * Current live status for a running turn. Scans backward and stops at the last
 * user_message: rows above it belong to a finished turn, and a stale in_progress tool up
 * there would otherwise drive the label forever (branch switches, reloads).
 */
export function deriveLiveStatus(
  items: readonly TranscriptItem[],
): LiveStatus {
  let permissionPending = false;
  let questionPending = false;
  let toolRunning: ToolItem | null = null;
  let toolPending: ToolItem | null = null;
  let thinking: ThinkingItem | null = null;
  let memory: MemoryItem | null = null;
  // The turn's newest row is answer text: whatever runs next has not shown up yet,
  // so the model is still writing it.
  let writing = false;
  let sawStep = false;
  // When the model went quiet: end of the most recent finished step in this turn.
  let waitingFrom: number | undefined;
  let turnStartedAtMs: number | undefined;

  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (!it) {
      continue;
    }
    if (it.type === "user_message" || it.type === "background_wake") {
      turnStartedAtMs = parseCreatedAt(it.createdAtUtc);
      break;
    }
    if (!sawStep) {
      if (it.type === "assistant_message") {
        if (it.content.trim()) {
          writing = true;
          sawStep = true;
        }
      } else if (
        it.type === "tool_call" ||
        it.type === "thinking" ||
        it.type === "memory_run"
      ) {
        sawStep = true;
      }
    }
    switch (it.type) {
      case "permission_prompt":
        if (!it.resolved) {
          permissionPending = true;
        }
        break;
      case "question_prompt":
        if (!it.resolved) {
          questionPending = true;
        }
        break;
      case "tool_call":
        // The agent announces every call as pending while the response streams,
        // then executes sequentially: a running call beats any later pending one.
        if (!toolRunning && it.status === "in_progress") {
          toolRunning = it;
        } else if (!toolPending && it.status === "pending") {
          toolPending = it;
        }
        if (waitingFrom === undefined && typeof it.finishedAtMs === "number") {
          waitingFrom = it.finishedAtMs;
        }
        break;
      case "thinking":
        if (!thinking && it.status === "in_progress") {
          thinking = it;
        }
        if (
          waitingFrom === undefined &&
          it.status === "completed" &&
          typeof it.startedAtMs === "number" &&
          typeof it.durationMs === "number"
        ) {
          waitingFrom = it.startedAtMs + it.durationMs;
        }
        break;
      case "memory_run":
        if (!memory && it.status === "started") {
          memory = it;
        }
        break;
      default:
        break;
    }
  }

  const turn =
    turnStartedAtMs !== undefined ? { turnStartedAtMs } : ({} as const);

  // Blocked on the user: the gated tool row still reads in_progress, but nothing is
  // running, so no step counter (same reasoning as ToolCallMessage's frozen timer).
  if (permissionPending) {
    return {
      kind: "permission",
      key: "status.awaitingPermission",
      ...turn,
    };
  }
  if (questionPending) {
    return {
      kind: "question",
      key: "status.awaitingAnswer",
      ...turn,
    };
  }

  const tool = toolRunning ?? toolPending;
  if (tool) {
    const rawName = (tool.title || tool.kind || "").trim();
    const key = statusKeyForTool(rawName);
    // A generic phrase over an MCP call says nothing and its `server__tool` id says
    // it badly, so the one phrase that takes slots names the server and the tool,
    // the way the transcript row does. What the call acts on is that row's, not the
    // line's: the arguments are never read here.
    const mcp = key === "status.tool" ? parseMcpToolName(rawName) : null;
    return {
      kind: "tool",
      key: mcp ? "status.mcp" : key,
      ...(mcp ? { keyParams: { server: mcp.server, tool: mcp.tool } } : {}),
      step: tool.toolCallId,
      // startedAtMs is rewritten on every in_progress update, i.e. it is the time of the
      // last status transition rather than the tool start. That is what we want here —
      // the counter measures the current step. Do not "fix" it.
      ...(typeof tool.startedAtMs === "number"
        ? { startedAtMs: tool.startedAtMs }
        : {}),
      ...turn,
    };
  }

  if (thinking) {
    return {
      kind: "thinking",
      key: "status.thinking",
      step: thinking.id,
      ...(typeof thinking.startedAtMs === "number"
        ? { startedAtMs: thinking.startedAtMs }
        : {}),
      ...turn,
    };
  }

  // Below tool/thinking on purpose: the memory run keeps going in the background
  // after the main model has moved on, and the model's own step is the news then.
  if (memory) {
    return {
      kind: "memory",
      key: "status.memory",
      step: memory.taskId ?? memory.id,
      ...(typeof memory.startedAtMs === "number"
        ? { startedAtMs: memory.startedAtMs }
        : {}),
      ...turn,
    };
  }

  if (writing) {
    return { kind: "writing", key: "status.writing", ...turn };
  }

  const startedAtMs = waitingFrom ?? turnStartedAtMs;
  if (startedAtMs === undefined) {
    return PREPARING;
  }
  return {
    kind: "waiting",
    key: WAITING_KEY,
    startedAtMs,
    ...turn,
  };
}
