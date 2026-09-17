/**
 * Subagent transcripts are read-only. `GET /coddy/sessions/{id}/messages`
 * marks a child session with `subagent {parentSessionId, name, taskId}` and
 * `readOnly: true`; a run the scheduler started carries a `scheduler {jobId,
 * trigger}` block inside it, and the session of a scheduler job carries
 * `schedulerJob {jobId}` next to `readOnly`. An ordinary session carries none
 * of these fields, so their absence means "a normal chat" and the composer
 * stays in place.
 */
export type SubagentTranscriptMeta = {
  /** The chat that spawned the run; where prompts go. */
  parentSessionId: string;
  /** Definition name (`explore`, `reviewer`, ...). */
  name: string;
  /** Background task id of the run in the parent session. */
  taskId: string;
  /**
   * Set when the transcript belongs to the scheduler: the job the run (or the
   * job session itself) belongs to, and how a run was triggered.
   */
  scheduler?: { jobId: string; trigger: string };
  /** True for the session of a scheduler job, which holds the job's runs. */
  jobSession?: boolean;
};

/** The fields of the messages payload this module reads; the rest is ignored. */
export type SubagentTranscriptPayload = {
  subagent?: {
    parentSessionId?: unknown;
    name?: unknown;
    taskId?: unknown;
    scheduler?: { jobId?: unknown; trigger?: unknown } | null;
  } | null;
  schedulerJob?: { jobId?: unknown } | null;
  readOnly?: unknown;
};

function str(v: unknown): string {
  return typeof v === "string" ? v.trim() : "";
}

/**
 * Reads the child-session marker off a messages payload. Either signal is
 * enough to lock the composer: the `subagent` block, the `schedulerJob` block,
 * or a bare `readOnly` flag (then the notice has no name or parent to show,
 * but still no composer).
 */
export function parseSubagentTranscriptMeta(
  payload: SubagentTranscriptPayload | null | undefined,
): SubagentTranscriptMeta | null {
  const raw = payload?.subagent;
  const block = raw && typeof raw === "object" ? raw : null;
  const jobRaw = payload?.schedulerJob;
  const jobBlock = jobRaw && typeof jobRaw === "object" ? jobRaw : null;
  if (!block && !jobBlock && payload?.readOnly !== true) {
    return null;
  }
  const meta: SubagentTranscriptMeta = {
    parentSessionId: str(block?.parentSessionId),
    name: str(block?.name),
    taskId: str(block?.taskId),
  };
  const sched = block?.scheduler;
  if (sched && typeof sched === "object" && str(sched.jobId)) {
    meta.scheduler = { jobId: str(sched.jobId), trigger: str(sched.trigger) };
  } else if (jobBlock && str(jobBlock.jobId)) {
    meta.scheduler = { jobId: str(jobBlock.jobId), trigger: "" };
    meta.jobSession = true;
  }
  return meta;
}
