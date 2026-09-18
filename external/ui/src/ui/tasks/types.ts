/**
 * Background tasks: commands the agent started with `run_command` `background: true`
 * and subagent runs started with `spawn_agent` (`kind: "agent"`).
 * Shapes mirror `GET /coddy/sessions/{id}/background-tasks` in
 * `external/httpserver/background_http.go`.
 */

import type { CoddyPermissionPayload } from "../chat/permissionTypes";

export type BackgroundTaskStatus =
  | "queued"
  | "running"
  | "succeeded"
  | "failed"
  | "timed_out"
  | "stopped"
  | "orphaned";

export type BackgroundTask = {
  id: string;
  session_id: string;
  kind: string;
  label: string;
  command?: string;
  cwd?: string;
  tool_call_id?: string;
  /**
   * Present on `kind: "agent"` rows: the definition name and the child session
   * the run is persisted under. `session_id` is what "Show transcript" routes
   * to; a snapshot may carry the name alone when the child does not exist yet.
   * `system` marks a run the runtime started on its own behalf (the memory
   * subagent of a turn) rather than a delegation the model asked for.
   */
  agent?: {
    name: string;
    session_id?: string;
    system?: boolean;
    /** The model the child runs on. */
    model?: string;
    /** What the child's model calls have spent so far: input summed over calls, output generated. */
    input_tokens?: number;
    output_tokens?: number;
  };
  status: BackgroundTaskStatus;
  exit_code?: number;
  error?: string;
  started_at: string;
  finished_at?: string;
  expected_seconds?: number;
  timeout_seconds: number;
  output_bytes: number;
  output_truncated: boolean;
  /** Server-computed so every client agrees on the clock arithmetic. */
  elapsed_seconds: number;
  overdue: boolean;
  running: boolean;
  /**
   * Set while a background subagent behind this task is blocked on a permission
   * prompt. The parent turn that spawned it has ended, so no chat stream carries
   * the prompt: the parent chat reads it from here and shows it at the end of
   * the conversation, and the answer goes to `sessionId`, which is the child
   * session, not the parent.
   */
  pending_permission?: CoddyPermissionPayload & {
    parent_session_id?: string;
    task_id?: string;
    agent_name?: string;
    asked_at?: string;
  };
};

export type BackgroundTaskListResponse = {
  object: string;
  sessionId: string;
  running: number;
  data: BackgroundTask[];
};

export type BackgroundTaskResponse = {
  object: string;
  sessionId: string;
  task: BackgroundTask;
  output: string;
};
