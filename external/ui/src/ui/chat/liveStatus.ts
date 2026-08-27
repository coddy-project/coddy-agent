/**
 * Derives the live status shown next to the typing dots while a turn is running:
 * what the agent is doing right now, what it is doing it to, and since when.
 *
 * Everything comes from the transcript the SPA already holds — no backend or SSE change.
 * The module stays free of React and of locale state: it returns i18n *keys* plus the raw
 * target, so the component owns translation and truncation and the tests can assert on
 * locale-independent values.
 *
 * The console TUI carries the same phrase table in Go (external/cli/status.go). The two
 * cannot share code across the language boundary, so a tool added to one belongs in the
 * other as well.
 */

import { toolCallTargetText } from "./permissionToolPreview";
import type { TranscriptItem } from "./types";

export type LiveStatusKind =
  | "permission"
  | "question"
  | "tool"
  | "thinking"
  | "memory"
  | "waiting";

export type LiveStatus = {
  kind: LiveStatusKind;
  /** i18n key of the verb phrase. Carries no {param} slots. */
  key: string;
  /** Untruncated target (path / command / pattern); "" when the phrase takes none. */
  target: string;
  /** Wall clock ms to count elapsed from; omitted when the start is unknown. */
  startedAtMs?: number;
};

/** Waiting longer than this reads as "slower than usual". */
export const WAITING_SLOW_MS = 15_000;
/** Waiting longer than this reads as "still nothing from the server". */
export const WAITING_STUCK_MS = 60_000;

/** Longest target rendered inline; CSS ellipsizes further, this caps the DOM text node. */
export const MAX_TARGET_CHARS = 56;

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
 * names (internal/tools, internal/agent/toolsets.go); unknown ones — MCP tools included —
 * fall back to a generic phrase and keep their id as the target so the row stays
 * debuggable.
 *
 * Note: the rendered order is "verb target" (two separate spans so CSS can ellipsize the
 * target alone). A locale needing target-first would have to restructure the markup.
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
    case "webfetch":
      return "status.webFetch";
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

/** Collapse newlines/tabs so a heredoc command cannot break the single-line row. */
function collapseWhitespace(raw: string): string {
  return raw.replace(/\s+/g, " ").trim();
}

/** Path-shaped: has a separator and no spaces once collapsed. */
function looksLikePath(value: string): boolean {
  return /[\\/]/.test(value) && !value.includes(" ");
}

/**
 * Shorten a target for inline display. Paths lose leading segments (the tail identifies
 * the file); everything else loses its tail (the leading program name identifies a
 * command). The untruncated value belongs in a title attribute.
 */
export function truncateStatusTarget(
  raw: string,
  max: number = MAX_TARGET_CHARS,
): string {
  const collapsed = collapseWhitespace(raw || "");
  if (!looksLikePath(collapsed)) {
    return collapsed.length <= max
      ? collapsed
      : collapsed.slice(0, Math.max(1, max - 1)) + "…";
  }
  const segments = collapsed.split(/[\\/]+/).filter((s) => s !== "");
  // Display separators are always "/" so a Windows path reads the same as a POSIX one;
  // the caller keeps the raw value for the title attribute.
  const value = segments.join("/");
  if (value.length <= max) {
    return value;
  }
  const last = segments[segments.length - 1] || value;
  if (last.length + 2 > max) {
    return "…/" + last.slice(0, Math.max(1, max - 3)) + "…";
  }
  let tail = last;
  for (let i = segments.length - 2; i >= 0; i--) {
    const segment = segments[i];
    if (segment === undefined) {
      break;
    }
    const next = segment + "/" + tail;
    if (next.length + 2 > max) {
      break;
    }
    tail = next;
  }
  return "…/" + tail;
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

const PREPARING: LiveStatus = { kind: "waiting", key: WAITING_KEY, target: "" };

type ToolItem = Extract<TranscriptItem, { type: "tool_call" }>;
type ThinkingItem = Extract<TranscriptItem, { type: "thinking" }>;
type MemoryItem = Extract<TranscriptItem, { type: "memory_copilot" }>;

/**
 * Current live status for a running turn. Scans backward and stops at the last
 * user_message: rows above it belong to a finished turn, and a stale in_progress tool up
 * there would otherwise drive the label forever (branch switches, reloads).
 */
export function deriveLiveStatus(items: readonly TranscriptItem[]): LiveStatus {
  let permissionPending = false;
  let questionPending = false;
  let toolRunning: ToolItem | null = null;
  let toolPending: ToolItem | null = null;
  let thinking: ThinkingItem | null = null;
  let memory: MemoryItem | null = null;
  // When the model went quiet: end of the most recent finished step in this turn.
  let waitingFrom: number | undefined;
  let turnStartedAtMs: number | undefined;

  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (!it) {
      continue;
    }
    if (it.type === "user_message") {
      turnStartedAtMs = parseCreatedAt(it.createdAtUtc);
      break;
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
      case "memory_copilot":
        if (
          !memory &&
          (it.memoryStatus === "in_progress" ||
            it.recallStatus === "in_progress" ||
            it.persistStatus === "in_progress")
        ) {
          memory = it;
        }
        break;
      default:
        break;
    }
  }

  // Blocked on the user: the gated tool row still reads in_progress, but nothing is
  // running, so no elapsed counter (same reasoning as ToolCallMessage's frozen timer).
  if (permissionPending) {
    return { kind: "permission", key: "status.awaitingPermission", target: "" };
  }
  if (questionPending) {
    return { kind: "question", key: "status.awaitingAnswer", target: "" };
  }

  const tool = toolRunning ?? toolPending;
  if (tool) {
    const rawName = (tool.title || tool.kind || "").trim();
    const key = statusKeyForTool(rawName);
    const target =
      toolCallTargetText({
        ...(tool.title !== undefined ? { title: tool.title } : {}),
        ...(tool.kind !== undefined ? { kind: tool.kind } : {}),
        ...(tool.argsText !== undefined ? { argsText: tool.argsText } : {}),
      }) || (key === "status.tool" ? rawName : "");
    return {
      kind: "tool",
      key,
      target,
      // startedAtMs is rewritten on every in_progress update, i.e. it is the time of the
      // last status transition rather than the tool start. That is what we want here —
      // the counter measures the current step. Do not "fix" it.
      ...(typeof tool.startedAtMs === "number"
        ? { startedAtMs: tool.startedAtMs }
        : {}),
    };
  }

  if (thinking) {
    return {
      kind: "thinking",
      key: "status.thinking",
      target: "",
      ...(typeof thinking.startedAtMs === "number"
        ? { startedAtMs: thinking.startedAtMs }
        : {}),
    };
  }

  // Below tool/thinking on purpose: recall and persist stay flagged busy after the main
  // model has moved on (see memoryWallLiveCapMs in types.ts).
  if (memory) {
    return {
      kind: "memory",
      key: "status.memory",
      target: "",
      ...(typeof memory.memoryWallStartedAtMs === "number"
        ? { startedAtMs: memory.memoryWallStartedAtMs }
        : {}),
    };
  }

  const startedAtMs = waitingFrom ?? turnStartedAtMs;
  if (startedAtMs === undefined) {
    return PREPARING;
  }
  return { kind: "waiting", key: WAITING_KEY, target: "", startedAtMs };
}
