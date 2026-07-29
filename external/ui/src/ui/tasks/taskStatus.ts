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
      return "Queued";
    case "running":
      return "Running";
    case "succeeded":
      return "Succeeded";
    case "failed":
      return "Failed";
    case "timed_out":
      return "Timed out";
    case "stopped":
      return "Stopped";
    case "orphaned":
      return "Orphaned";
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
    parts.push(`est. ${formatDuration(task.expected_seconds)}`);
  }
  if (!task.running && typeof task.exit_code === "number") {
    parts.push(`exit ${task.exit_code}`);
  }
  if (isOverdue(task, nowMs)) {
    parts.push("overdue");
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

/** Newest first: the drawer is about what is happening now. */
export function sortTasksForDrawer(tasks: BackgroundTask[]): BackgroundTask[] {
  return [...tasks].sort((a, b) => {
    if (a.running !== b.running) {
      return a.running ? -1 : 1;
    }
    const at = Date.parse(a.started_at) || 0;
    const bt = Date.parse(b.started_at) || 0;
    return bt - at;
  });
}
