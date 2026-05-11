export type SchedulerInfo = {
  enabled: boolean;
  dir: string;
  poll_interval: string;
  timeout: string;
  max_queue: number;
  runs_active: number;
  retain_sessions: number;
};

export type SchedulerJob = {
  job_id: string;
  description?: string;
  schedule: string;
  paused: boolean;
  cwd?: string;
  model?: string;
  mode?: string;
  body?: string;
  last_scheduled_slot_utc?: string;
  next_run_utc?: string;
  running: boolean;
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
  body: string;
};

export type SchedulerJobPatch = {
  description?: string;
  schedule?: string;
  paused?: boolean;
  cwd?: string;
  model?: string;
  mode?: string;
  body?: string;
};
