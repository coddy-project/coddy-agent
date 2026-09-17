/**
 * What the transcript shows for a scheduler tool call.
 *
 * The `coddy_scheduler_*` tools take a job id and answer with a line of JSON, and the
 * row used to print both as they were: `{"job_id": "ai-news-digest"}` over
 * `{"job_id":"ai-news-digest","paused":false}`. What a reader wants is what happened
 * to which job, and for the reading tools the job itself. This module turns the call
 * into that, and returns `null` for anything it does not recognise so the row keeps
 * the raw text it already had.
 */

export type SchedulerJobView = {
  jobId?: string;
  description?: string;
  schedule?: string;
  paused?: boolean;
  running?: boolean;
  mode?: string;
  model?: string;
  cwd?: string;
  body?: string;
  nextRunUtc?: string;
};

export type SchedulerRunView = {
  sessionId?: string;
  startedAt?: string;
  endedAt?: string;
  status?: string;
};

/** What an acting call did to its job, in the order the tools are listed. */
export type SchedulerOutcome =
  | "created"
  | "replaced"
  | "patched"
  | "deleted"
  | "paused"
  | "resumed"
  | "runAccepted"
  | "cancelled"
  | "notRunning";

export type SchedulerReadout =
  | {
      kind: "outcome";
      jobId: string;
      /** Set once the call completed. */
      outcome?: SchedulerOutcome;
      /** A patch that moved the job to another id. */
      renamedTo?: string;
      /** The fields a create, replace or patch set. */
      job?: SchedulerJobView;
    }
  | { kind: "job"; job: SchedulerJobView }
  | { kind: "jobs"; jobs: SchedulerJobView[] }
  | { kind: "runs"; jobId: string; runs: SchedulerRunView[] };

const PREFIX = "coddy_scheduler_";

export function isSchedulerTool(name: string | undefined): boolean {
  return (name || "").trim().toLowerCase().startsWith(PREFIX);
}

function parseObject(text: string | undefined): Record<string, unknown> | null {
  const raw = (text || "").trim();
  if (!raw.startsWith("{")) return null;
  try {
    const value = JSON.parse(raw) as unknown;
    return value && typeof value === "object" && !Array.isArray(value)
      ? (value as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

function str(obj: Record<string, unknown>, key: string): string {
  const v = obj[key];
  return typeof v === "string" ? v.trim() : "";
}

/** The fields of a job as the wire spells them, keeping only the ones present. */
function jobView(obj: Record<string, unknown>): SchedulerJobView {
  const out: SchedulerJobView = {};
  const text: Array<[keyof SchedulerJobView, string]> = [
    ["jobId", "job_id"],
    ["description", "description"],
    ["schedule", "schedule"],
    ["mode", "mode"],
    ["model", "model"],
    ["cwd", "cwd"],
    ["nextRunUtc", "next_run_utc"],
  ];
  for (const [field, key] of text) {
    const v = str(obj, key);
    if (v) (out as Record<string, unknown>)[field] = v;
  }
  if (typeof obj.body === "string" && obj.body.trim()) out.body = obj.body;
  if (typeof obj.paused === "boolean") out.paused = obj.paused;
  if (typeof obj.running === "boolean") out.running = obj.running;
  return out;
}

function runView(obj: Record<string, unknown>): SchedulerRunView {
  const out: SchedulerRunView = {};
  const sessionId = str(obj, "session_id");
  if (sessionId) out.sessionId = sessionId;
  const startedAt = str(obj, "started_at");
  if (startedAt) out.startedAt = startedAt;
  const endedAt = str(obj, "ended_at");
  if (endedAt) out.endedAt = endedAt;
  const status = str(obj, "status");
  if (status) out.status = status;
  return out;
}

function objects(value: unknown): Record<string, unknown>[] | null {
  if (!Array.isArray(value)) return null;
  return value.filter(
    (v): v is Record<string, unknown> =>
      !!v && typeof v === "object" && !Array.isArray(v),
  );
}

export function schedulerReadout(
  name: string | undefined,
  argsText: string | undefined,
  resultText: string | undefined,
  status: string,
): SchedulerReadout | null {
  if (!isSchedulerTool(name)) return null;
  const tool = (name || "").trim().toLowerCase().slice(PREFIX.length);
  const state = (status || "").toLowerCase();
  // A failed call answered with an error, which reads best as the text it is.
  if (state === "failed" || state === "cancelled") return null;
  const completed = state === "completed";
  const args = parseObject(argsText) ?? {};
  const jobId = str(args, "job_id");
  const result = completed ? parseObject(resultText) : null;

  switch (tool) {
    case "jobs_list": {
      if (!completed) return null;
      const jobs = objects(result?.jobs);
      return jobs ? { kind: "jobs", jobs: jobs.map(jobView) } : null;
    }
    case "job_get": {
      if (!completed) return null;
      return result && str(result, "job_id")
        ? { kind: "job", job: jobView(result) }
        : null;
    }
    case "job_runs": {
      if (!completed) return null;
      const runs = objects(result?.runs);
      return runs
        ? { kind: "runs", jobId: str(result!, "job_id") || jobId, runs: runs.map(runView) }
        : null;
    }
    case "job_create":
    case "job_replace":
    case "job_patch":
    case "job_delete":
    case "job_pause":
    case "job_resume":
    case "job_run":
    case "job_cancel": {
      if (!jobId) return null;
      const readout: Extract<SchedulerReadout, { kind: "outcome" }> = {
        kind: "outcome",
        jobId,
      };
      if (completed) {
        if (!result) return null;
        readout.outcome = outcomeOf(tool, result);
        if (tool === "job_patch") {
          const renamed = str(args, "new_job_id");
          if (renamed && renamed !== jobId) readout.renamedTo = renamed;
        }
      }
      if (tool === "job_create" || tool === "job_replace" || tool === "job_patch") {
        readout.job = jobView(args);
      }
      return readout;
    }
    default:
      return null;
  }
}

function outcomeOf(tool: string, result: Record<string, unknown>): SchedulerOutcome {
  switch (tool) {
    case "job_create":
      return "created";
    case "job_replace":
      return "replaced";
    case "job_patch":
      return "patched";
    case "job_delete":
      return "deleted";
    case "job_pause":
      return "paused";
    case "job_resume":
      return "resumed";
    case "job_run":
      return "runAccepted";
    default:
      return result.cancelled === false ? "notRunning" : "cancelled";
  }
}
