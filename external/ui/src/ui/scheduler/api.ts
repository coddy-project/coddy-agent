import type {
  JobsListResponse,
  SchedulerJob,
  SchedulerJobCreate,
  SchedulerJobPatch,
  SchedulerJobRef,
} from "./types";
import { parseSchedulerJobRef } from "./types";
import {
  applyWorkspaceQuery,
  type WorkspaceScope,
} from "../chat/workspaceScope";

/**
 * The session header of the chat the scheduler drawer belongs to. A request
 * about a project job carries it, so the server can tell the job's workspace
 * is the session's own even before the scheduler scans it (a job that came
 * with a checkout the operator just opened). App keeps it current.
 */
let sessionHeaders: Record<string, string> = {};

export function setSchedulerSessionHeaders(headers: Record<string, string>): void {
  sessionHeaders = { ...headers };
}

/** fetch of one job's route, with the session header for a project job. */
function jobFetch(
  ref: SchedulerJobRef,
  suffix: string,
  init?: RequestInit,
): Promise<Response> {
  const project = parseSchedulerJobRef(ref).scope === "project";
  const headers = {
    ...((init?.headers as Record<string, string> | undefined) || {}),
    ...(project ? sessionHeaders : {}),
  };
  return fetch(schedulerJobUrl(ref, suffix), { ...(init || {}), headers });
}

/**
 * URL of one job's route: `/coddy/scheduler/jobs/<id><suffix>`, with
 * `scope=project&workspace=<path>` for a project job, so the server never
 * mistakes it for the user job of the same id.
 */
export function schedulerJobUrl(ref: SchedulerJobRef, suffix = ""): string {
  const { id, scope, workspace } = parseSchedulerJobRef(ref);
  const path = `/coddy/scheduler/jobs/${encodeURIComponent(id)}${suffix}`;
  if (scope !== "project") {
    return path;
  }
  const sp = new URLSearchParams({ scope: "project", workspace });
  return `${path}?${sp.toString()}`;
}

async function readErrorMessage(res: Response): Promise<string> {
  try {
    const j = (await res.json()) as {
      error?: { message?: string };
    };
    const m = j?.error?.message;
    if (typeof m === "string" && m.trim()) {
      return m.trim();
    }
  } catch {
    /* ignore */
  }
  return `HTTP ${res.status}`;
}

export type ApiResult<T> =
  | { ok: true; data: T }
  | { ok: false; status: number; message: string };

async function parseJson<T>(res: Response): Promise<ApiResult<T>> {
  if (!res.ok) {
    return {
      ok: false,
      status: res.status,
      message: await readErrorMessage(res),
    };
  }
  const data = (await res.json()) as T;
  return { ok: true, data };
}

/**
 * Lists the jobs. With a workspace scope (the chat's session, or the folder a
 * new chat picked) the project jobs follow the user jobs: those of that
 * workspace and of every other workspace the scheduler runs.
 */
export async function schedulerListJobs(
  includeBody?: boolean,
  scope?: WorkspaceScope,
): Promise<ApiResult<JobsListResponse>> {
  const sp = new URLSearchParams();
  if (includeBody) {
    sp.set("include_body", "true");
  }
  if (scope) {
    applyWorkspaceQuery(sp, scope);
  }
  const q = sp.toString();
  const path = q ? `/coddy/scheduler/jobs?${q}` : "/coddy/scheduler/jobs";
  const res = await fetch(path, scope ? { headers: scope.headers } : undefined);
  return parseJson<JobsListResponse>(res);
}

export async function schedulerGetJob(
  jobId: string,
): Promise<ApiResult<SchedulerJob>> {
  const res = await jobFetch(jobId, "",
  );
  return parseJson<SchedulerJob>(res);
}

/**
 * Creates a job. A project job is written to the workspace of the session in
 * `sessionHeaders` and approved at once: the operator made it.
 */
export async function schedulerCreateJob(
  body: SchedulerJobCreate,
  sessionHeaders?: Record<string, string>,
): Promise<ApiResult<{ object?: string; job_id?: string }>> {
  const res = await fetch("/coddy/scheduler/jobs", {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(sessionHeaders || {}) },
    body: JSON.stringify(body),
  });
  return parseJson(res);
}

/** Approves a project job for its workspace, bound to the digest shown. */
export async function schedulerTrustJob(
  ref: SchedulerJobRef,
  digest: string,
): Promise<ApiResult<{ trusted?: boolean }>> {
  const res = await jobFetch(ref, "/trust", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ digest }),
  });
  return parseJson(res);
}

/** Withdraws the approval of a project job. */
export async function schedulerUntrustJob(
  ref: SchedulerJobRef,
): Promise<ApiResult<{ trusted?: boolean; removed?: boolean }>> {
  const res = await jobFetch(ref, "/untrust", { method: "POST" });
  return parseJson(res);
}

export async function schedulerPatchJob(
  jobId: string,
  patch: SchedulerJobPatch,
): Promise<
  ApiResult<{ object?: string; job_id?: string }>
> {
  const res = await jobFetch(jobId, "",
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    },
  );
  return parseJson(res);
}

export async function schedulerDeleteJob(
  jobId: string,
): Promise<ApiResult<null>> {
  const res = await jobFetch(jobId, "",
    { method: "DELETE" },
  );
  if (res.ok && (res.status === 204 || res.status === 200)) {
    return { ok: true, data: null };
  }
  return {
    ok: false,
    status: res.status,
    message: await readErrorMessage(res),
  };
}

export async function schedulerPauseJob(
  jobId: string,
): Promise<ApiResult<{ object?: string; job_id?: string }>> {
  const res = await jobFetch(jobId, "/pause",
    { method: "POST" },
  );
  return parseJson(res);
}

export async function schedulerResumeJob(
  jobId: string,
): Promise<ApiResult<{ object?: string; job_id?: string }>> {
  const res = await jobFetch(jobId, "/resume",
    { method: "POST" },
  );
  return parseJson(res);
}

export async function schedulerRunJob(
  jobId: string,
): Promise<
  ApiResult<{ object?: string; job_id?: string; status?: string }>
> {
  const res = await jobFetch(jobId, "/run",
    { method: "POST" },
  );
  return parseJson(res);
}

export async function schedulerCancelJob(
  jobId: string,
): Promise<
  ApiResult<{ object?: string; job_id?: string; cancelled?: boolean }>
> {
  const res = await jobFetch(jobId, "/cancel",
    { method: "POST" },
  );
  return parseJson(res);
}

/**
 * Clears the finished runs of a job: the task records and the run
 * transcripts. The panel's Clear goes here rather than to the session's
 * background-tasks DELETE, which would leave the transcripts behind.
 */
export async function schedulerClearJobRuns(
  jobId: string,
): Promise<ApiResult<{ object?: string; job_id?: string; cleared?: number }>> {
  const res = await jobFetch(jobId, "/runs",
    { method: "DELETE" },
  );
  return parseJson(res);
}
