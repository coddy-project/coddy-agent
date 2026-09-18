/**
 * The turn a finished background task starts.
 *
 * A task the model started with `notify_on_finish` wakes the agent when it ends,
 * and the server starts a turn nobody typed. Its first message is the
 * instruction the model reads. The web UI shows nothing in its place - the turn
 * reads as the agent carrying on, and the task's card in the Tasks panel keeps a
 * bell - but still opens the turn there, with an item built from the
 * `background_wake` frame while the turn streams and from the `background_wake`
 * field of the message after a reload. The two wire shapes differ only in case:
 * camelCase on the stream (ACP), snake_case in
 * `GET /coddy/sessions/{id}/messages` (the persisted transcript).
 */

import type { TranscriptItem } from "./types";

/** One finished task a woken turn reports. */
export type BackgroundWakeTask = {
  id: string;
  /** `command` for a shell command, `agent` for a subagent run. */
  kind?: string;
  label?: string;
  /** The subagent definition behind an agent run. */
  agent?: string;
  status: string;
  exitCode?: number;
  durationMs?: number;
  error?: string;
};

function str(v: unknown): string {
  return typeof v === "string" ? v.trim() : "";
}

function num(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

/**
 * The tasks of a wake, from either wire shape. A task without an id is not a
 * task anybody can name, and is dropped.
 */
export function parseBackgroundWakeTasks(raw: unknown): BackgroundWakeTask[] {
  const list =
    raw && typeof raw === "object" && !Array.isArray(raw)
      ? (raw as { tasks?: unknown }).tasks
      : raw;
  if (!Array.isArray(list)) return [];
  const out: BackgroundWakeTask[] = [];
  for (const entry of list) {
    if (!entry || typeof entry !== "object") continue;
    const r = entry as Record<string, unknown>;
    const id = str(r.id);
    if (!id) continue;
    const task: BackgroundWakeTask = { id, status: str(r.status) };
    const kind = str(r.kind);
    if (kind) task.kind = kind;
    const label = str(r.label);
    if (label) task.label = label;
    const agent = str(r.agent);
    if (agent) task.agent = agent;
    const exitCode = num(r.exitCode) ?? num(r.exit_code);
    if (exitCode !== undefined) task.exitCode = exitCode;
    const durationMs = num(r.durationMs) ?? num(r.duration_ms);
    if (durationMs !== undefined) task.durationMs = durationMs;
    const error = str(r.error);
    if (error) task.error = error;
    out.push(task);
  }
  return out;
}

/**
 * The transcript item of a `background_wake` stream frame, or null for a frame
 * that is not JSON or names no task.
 */
export function backgroundWakeItem(
  data: string,
  id: string,
): Extract<TranscriptItem, { type: "background_wake" }> | null {
  let raw: unknown;
  try {
    raw = JSON.parse(data);
  } catch {
    return null;
  }
  const tasks = parseBackgroundWakeTasks(raw);
  if (tasks.length === 0) return null;
  return { id, type: "background_wake", tasks, createdAtUtc: new Date().toISOString() };
}

/** Whether a transcript item opens a turn: a message typed, or a wake. */
export function opensTurn(it: TranscriptItem | undefined): boolean {
  return !!it && (it.type === "user_message" || it.type === "background_wake");
}
