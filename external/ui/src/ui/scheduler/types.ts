export type SchedulerInfo = {
  enabled: boolean;
  /** Folder of the user jobs, ${CODDY_HOME}/scheduler. */
  dir: string;
  /** Folder of a workspace's project jobs, relative to it (.coddy/scheduler). */
  project_dir?: string;
  /** scheduler.project_trust: ask, allow or deny. */
  project_trust?: "ask" | "allow" | "deny";
  /** The workspace the request named (its session or its cwd). */
  workspace?: string;
  timeout: string;
  max_queue: number;
  runs_active: number;
  retain_sessions: number;
};

/**
 * One run of a job as the scheduler routes report it: the run's background
 * task under the job session (`task_id`, `job_session_id`) and the run session
 * that holds the transcript (`session_id`). Mirrors `SchedulerRunEntry` in
 * `external/scheduler/service/types.go`.
 */
export type SchedulerRunEntry = {
  task_id: string;
  session_id: string;
  job_session_id: string;
  trigger?: string;
  status: string;
  running: boolean;
  started_at?: string;
  ended_at?: string;
  elapsed_seconds: number;
  error?: string;
  label?: string;
};

/** user: ${CODDY_HOME}/scheduler; project: <workspace>/.coddy/scheduler. */
export type SchedulerJobScope = "user" | "project";

/** Only a trusted job runs. */
export type SchedulerTrust =
  | "trusted"
  | "needs_approval"
  | "denied"
  | "conflict"
  | "invalid";

export type SchedulerJob = {
  job_id: string;
  scope?: SchedulerJobScope;
  /** Canonical workspace of a project job. */
  workspace?: string;
  trust?: SchedulerTrust;
  /** Why a job that is not trusted does not run. */
  trust_reason?: string;
  /** sha256 of the job file: what an approval is bound to. */
  digest?: string;
  /** False for a project job of a workspace the scheduler does not scan. */
  scheduled?: boolean;
  /** Raw file of a project job (GET of one job): what the approval view shows. */
  raw?: string;
  description?: string;
  schedule: string;
  paused: boolean;
  cwd?: string;
  model?: string;
  mode?: string;
  /** Subagent definition the run is made under; empty runs a general agent. */
  agent?: string;
  /** ask | accept_edits | bypass; empty is bypass, the unattended default. */
  permission_mode?: string;
  body?: string;
  last_scheduled_slot_utc?: string;
  next_run_utc?: string;
  running: boolean;
  /**
   * The job session every run is a child of: what the runs panel polls
   * through the background tasks route. Empty until the job ran once.
   */
  session_id?: string;
  /** The newest run, in flight or finished. */
  last_run?: SchedulerRunEntry | null;
};

export type JobsListResponse = {
  scheduler: SchedulerInfo;
  jobs: SchedulerJob[];
};

export type SchedulerJobCreate = {
  /** user (default) or project: a project job lands in the session's workspace. */
  scope?: SchedulerJobScope;
  job_id: string;
  description: string;
  schedule: string;
  paused?: boolean;
  cwd?: string;
  model?: string;
  mode?: string;
  agent?: string;
  permission_mode?: string;
  body: string;
};

export type SchedulerJobPatch = {
  job_id?: string;
  description?: string;
  schedule?: string;
  paused?: boolean;
  cwd?: string;
  model?: string;
  mode?: string;
  agent?: string;
  permission_mode?: string;
  body?: string;
};

/**
 * A job's reference inside the SPA: the bare id for a user job,
 * `<workspace>/<id>` for a project job. A job id never contains a slash, so
 * the last one splits the two; everything that carried a job id as an opaque
 * string (hash routes, the editor state, the runs panel) carries this instead
 * and a user job keeps the address it always had.
 */
export type SchedulerJobRef = string;

export function schedulerJobRef(job: Pick<SchedulerJob, "job_id" | "scope" | "workspace">): SchedulerJobRef {
  if (job.scope === "project" && (job.workspace || "").trim()) {
    return `${(job.workspace || "").trim()}/${job.job_id}`;
  }
  return job.job_id;
}

export function parseSchedulerJobRef(ref: SchedulerJobRef): {
  id: string;
  scope: SchedulerJobScope;
  workspace: string;
} {
  const r = (ref || "").trim();
  const cut = r.lastIndexOf("/");
  if (cut < 0) {
    return { id: r, scope: "user", workspace: "" };
  }
  return { id: r.slice(cut + 1), scope: "project", workspace: r.slice(0, cut) };
}
