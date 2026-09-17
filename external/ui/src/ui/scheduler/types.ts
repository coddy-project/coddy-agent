export type SchedulerInfo = {
  enabled: boolean;
  dir: string;
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

export type SchedulerJob = {
  job_id: string;
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
