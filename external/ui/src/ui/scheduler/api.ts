import type {
  JobsListResponse,
  SchedulerJob,
  SchedulerJobCreate,
  SchedulerJobPatch,
} from "./types";

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

export async function schedulerListJobs(
  includeBody?: boolean,
): Promise<ApiResult<JobsListResponse>> {
  const sp = new URLSearchParams();
  if (includeBody) {
    sp.set("include_body", "true");
  }
  const q = sp.toString();
  const path = q ? `/coddy/scheduler/jobs?${q}` : "/coddy/scheduler/jobs";
  const res = await fetch(path);
  return parseJson<JobsListResponse>(res);
}

export async function schedulerGetJob(
  jobId: string,
): Promise<ApiResult<SchedulerJob>> {
  const res = await fetch(
    `/coddy/scheduler/jobs/${encodeURIComponent(jobId)}`,
  );
  return parseJson<SchedulerJob>(res);
}

export async function schedulerCreateJob(
  body: SchedulerJobCreate,
): Promise<ApiResult<{ object?: string; job_id?: string }>> {
  const res = await fetch("/coddy/scheduler/jobs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  return parseJson(res);
}

export async function schedulerPatchJob(
  jobId: string,
  patch: SchedulerJobPatch,
): Promise<ApiResult<{ object?: string; job_id?: string }>> {
  const res = await fetch(
    `/coddy/scheduler/jobs/${encodeURIComponent(jobId)}`,
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
  const res = await fetch(
    `/coddy/scheduler/jobs/${encodeURIComponent(jobId)}`,
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
  const res = await fetch(
    `/coddy/scheduler/jobs/${encodeURIComponent(jobId)}/pause`,
    { method: "POST" },
  );
  return parseJson(res);
}

export async function schedulerResumeJob(
  jobId: string,
): Promise<ApiResult<{ object?: string; job_id?: string }>> {
  const res = await fetch(
    `/coddy/scheduler/jobs/${encodeURIComponent(jobId)}/resume`,
    { method: "POST" },
  );
  return parseJson(res);
}

export async function schedulerRunJob(
  jobId: string,
): Promise<
  ApiResult<{ object?: string; job_id?: string; status?: string }>
> {
  const res = await fetch(
    `/coddy/scheduler/jobs/${encodeURIComponent(jobId)}/run`,
    { method: "POST" },
  );
  return parseJson(res);
}

export async function schedulerCancelJob(
  jobId: string,
): Promise<
  ApiResult<{ object?: string; job_id?: string; cancelled?: boolean }>
> {
  const res = await fetch(
    `/coddy/scheduler/jobs/${encodeURIComponent(jobId)}/cancel`,
    { method: "POST" },
  );
  return parseJson(res);
}
