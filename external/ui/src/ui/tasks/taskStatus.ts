import { t } from "../i18n/i18n";
import type { BackgroundTask, BackgroundTaskStatus } from "./types";

/**
 * Pure presentation helpers for background tasks. Kept out of the components so
 * the label, tone, and progress rules are testable and stay identical between
 * the tasks drawer and the transcript ticker card.
 */

/** Tone drives the status dot color and the row accent. */
export type TaskTone = "running" | "success" | "danger" | "warning" | "muted";

export function taskTone(status: BackgroundTaskStatus): TaskTone {
  switch (status) {
    case "queued":
    case "running":
      return "running";
    case "succeeded":
      return "success";
    case "failed":
    case "timed_out":
      return "danger";
    case "stopped":
      return "warning";
    default:
      return "muted";
  }
}

/** Short human label for a status, used in rows and in the ticker summary. */
export function taskStatusLabel(status: BackgroundTaskStatus): string {
  switch (status) {
    case "queued":
      return t("tasks.status.queued");
    case "running":
      return t("tasks.status.running");
    case "succeeded":
      return t("tasks.status.succeeded");
    case "failed":
      return t("tasks.status.failed");
    case "timed_out":
      return t("tasks.status.timedOut");
    case "stopped":
      return t("tasks.status.stopped");
    case "orphaned":
      return t("tasks.status.orphaned");
    default:
      return status;
  }
}

/** Compact duration in the same vocabulary the tool output uses (45s, 1m35s, 1h30m). */
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  if (s < 60) {
    return `${s}s`;
  }
  if (s < 3600) {
    const rem = s % 60;
    return rem ? `${Math.floor(s / 60)}m${rem}s` : `${Math.floor(s / 60)}m`;
  }
  const rem = Math.floor((s % 3600) / 60);
  return rem ? `${Math.floor(s / 3600)}h${rem}m` : `${Math.floor(s / 3600)}h`;
}

/**
 * Elapsed seconds for display. A running task keeps ticking between polls, so
 * the client extrapolates from `started_at`; a finished task keeps whatever the
 * server measured.
 */
export function displayElapsedSeconds(
  task: BackgroundTask,
  nowMs: number,
): number {
  if (!task.running) {
    return Math.max(0, task.elapsed_seconds);
  }
  const started = Date.parse(task.started_at);
  if (Number.isNaN(started)) {
    return Math.max(0, task.elapsed_seconds);
  }
  return Math.max(0, Math.floor((nowMs - started) / 1000));
}

/**
 * Progress toward the model's own estimate, clamped to 0..1. Returns null when
 * there is nothing honest to draw: no estimate, or the task already finished.
 */
export function estimateProgress(
  task: BackgroundTask,
  nowMs: number,
): number | null {
  if (!task.running) {
    return null;
  }
  const expected = task.expected_seconds || 0;
  if (expected <= 0) {
    return null;
  }
  const elapsed = displayElapsedSeconds(task, nowMs);
  return Math.min(1, Math.max(0, elapsed / expected));
}

/** One-line summary under a task label: elapsed, estimate, and how it ended. */
export function taskTimingLine(task: BackgroundTask, nowMs: number): string {
  const parts = [formatDuration(displayElapsedSeconds(task, nowMs))];
  if (task.expected_seconds && task.expected_seconds > 0) {
    parts.push(
      t("tasks.estimate", { value: formatDuration(task.expected_seconds) }),
    );
  }
  // An agent task has no process behind it, so its exit code is synthetic:
  // the status already says how the run ended, and "exit 0" would only
  // suggest a shell that never existed.
  if (
    !task.running &&
    !isAgentTask(task) &&
    typeof task.exit_code === "number"
  ) {
    parts.push(t("tasks.exitCode", { code: task.exit_code }));
  }
  if (isOverdue(task, nowMs)) {
    parts.push(t("tasks.overdue"));
  }
  return parts.join(" · ");
}

/**
 * The word in a card's tag: what stands behind the task. A subagent run is known by its
 * agent's name, the memory run of a turn by what it is, a shell command by being one.
 */
export function taskTag(task: BackgroundTask): string {
  if (task.agent?.system) {
    return t("tasks.tag.memory");
  }
  if (isAgentTask(task)) {
    return agentTaskName(task) || t("tasks.tag.agent");
  }
  return t("tasks.tag.shell");
}

/**
 * The title of a card: the work itself. The pool labels an agent run
 * `agent <name>: <description>` and a memory run `memory: <first line>`; the tag already
 * says the first half, so the title keeps the second. A run nobody described gets a
 * plain name rather than an empty title.
 */
export function taskTitle(task: BackgroundTask): string {
  const label = (task.label || "").trim();
  if (!isAgentTask(task)) {
    return label || (task.command || "").trim();
  }
  const colon = label.indexOf(":");
  const head = colon >= 0 ? label.slice(0, colon).trim().toLowerCase() : "";
  const name = (agentTaskName(task) || "").toLowerCase();
  if (colon >= 0 && (head === "memory" || head === `agent ${name}`)) {
    return label.slice(colon + 1).trim() || t("tasks.untitledAgentRun");
  }
  if (!label || label.toLowerCase() === `agent ${name}`) {
    return t("tasks.untitledAgentRun");
  }
  return label;
}

/** Wall clock of a finished task, HH:MM in the reader's locale; "" while it runs. */
export function taskFinishedClock(task: BackgroundTask): string {
  const ended = task.finished_at ? new Date(task.finished_at) : null;
  if (!ended || Number.isNaN(ended.getTime())) {
    return "";
  }
  return ended.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * The line under a card's title. A running task counts against its estimate; a finished
 * one says how it ended, how long it took and when. The exit code is left to the foot of
 * the open card, where the whole ending is read.
 */
export function taskMetaLine(task: BackgroundTask, nowMs: number): string {
  if (task.running) {
    return taskTimingLine(task, nowMs);
  }
  const parts = [
    taskStatusLabel(task.status),
    formatDuration(displayElapsedSeconds(task, nowMs)),
  ];
  const clock = taskFinishedClock(task);
  if (clock) {
    parts.push(clock);
  }
  return parts.join(" · ");
}

/**
 * Overdue is recomputed client-side so the badge appears between polls rather
 * than only after the next refresh.
 */
export function isOverdue(task: BackgroundTask, nowMs: number): boolean {
  if (!task.running) {
    return false;
  }
  const expected = task.expected_seconds || 0;
  if (expected <= 0) {
    return false;
  }
  return displayElapsedSeconds(task, nowMs) > expected;
}

/**
 * Poll cadence: fast while something is in flight so the ticker feels live,
 * slow otherwise so an idle drawer is not a busy loop.
 */
export const TASKS_POLL_ACTIVE_MS = 2500;
export const TASKS_POLL_IDLE_MS = 15000;

export function tasksPollIntervalMs(runningCount: number): number {
  return runningCount > 0 ? TASKS_POLL_ACTIVE_MS : TASKS_POLL_IDLE_MS;
}

/**
 * A background subagent is blocked on a permission prompt its parent chat can
 * answer. The task keeps reporting itself as running while it waits - it is,
 * and its timeout still applies - so "waiting" is a state on top of running,
 * not instead of it. A prompt missing either id cannot be answered, so it is
 * not one.
 */
export function isAwaitingPermission(task: BackgroundTask): boolean {
  const pending = task.pending_permission;
  return (
    task.running &&
    !!pending &&
    !!(pending.sessionId || "").trim() &&
    !!(pending.toolCall?.toolCallId || "").trim()
  );
}

/**
 * Ordered purely by when the task was started, newest first. Running tasks are
 * not floated to the top: they already live in their own section, and mixing
 * two orderings makes a list that never sits still to read.
 */
export function sortTasksByStart(tasks: BackgroundTask[]): BackgroundTask[] {
  return [...tasks].sort(
    (a, b) => (Date.parse(b.started_at) || 0) - (Date.parse(a.started_at) || 0),
  );
}

/**
 * Splits a session's tasks the way the panel shows them: what is happening now,
 * and the history behind it. Both halves keep start-time order.
 */
export function groupTasks(tasks: BackgroundTask[]): {
  running: BackgroundTask[];
  finished: BackgroundTask[];
} {
  const ordered = sortTasksByStart(tasks);
  return {
    running: ordered.filter((t) => t.running),
    finished: ordered.filter((t) => !t.running),
  };
}

/** A subagent run in the pool, as opposed to a shell command. */
export function isAgentTask(task: BackgroundTask): boolean {
  return task.kind === "agent";
}

/** Definition name of an agent task; empty for commands. */
export function agentTaskName(task: BackgroundTask): string {
  return isAgentTask(task) ? (task.agent?.name || "").trim() : "";
}

/**
 * Child session an agent task can be opened on, or null when there is nothing
 * to open: a command task, or an agent row whose child session is not known.
 */
export function agentTranscriptSessionId(task: BackgroundTask): string | null {
  if (!isAgentTask(task)) {
    return null;
  }
  const sid = (task.agent?.session_id || "").trim();
  return sid ? sid : null;
}

/**
 * How many tasks a chat has and how many of them run right now, as its surfaces count
 * them: the live status line and the control in the chat header. A system task - the
 * memory run the runtime starts for every turn - is left out of both numbers, like
 * `Pool.RunningCount` leaves it out on the server: it is not work the model or the
 * operator started, and counting it would make every turn read as one running task.
 */
export function countTasks(tasks: readonly BackgroundTask[]): {
  running: number;
  total: number;
} {
  let running = 0;
  let total = 0;
  for (const task of tasks) {
    if (task.agent?.system) {
      continue;
    }
    total++;
    if (task.running) {
      running++;
    }
  }
  return { running, total };
}

/** The running half of `countTasks`. */
export function countRunningTasks(tasks: readonly BackgroundTask[]): number {
  return countTasks(tasks).running;
}
