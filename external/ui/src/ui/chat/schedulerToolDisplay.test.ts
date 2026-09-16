import { expect, test } from "vitest";
import { schedulerReadout } from "./schedulerToolDisplay";

const job = JSON.stringify({ job_id: "ai-news-digest" });

// The scheduler tools answer with a line of JSON the transcript used to print as it
// was. What the reader wants from a pause or a run is what happened to which job.
test("a job action reads as its outcome", () => {
  expect(
    schedulerReadout("coddy_scheduler_job_resume", job, '{"job_id":"ai-news-digest","paused":false}', "completed"),
  ).toEqual({ kind: "outcome", jobId: "ai-news-digest", outcome: "resumed" });
  expect(
    schedulerReadout("coddy_scheduler_job_pause", job, '{"job_id":"ai-news-digest","paused":true}', "completed"),
  ).toMatchObject({ kind: "outcome", outcome: "paused" });
  expect(
    schedulerReadout(
      "coddy_scheduler_job_run",
      job,
      '{"object":"coddy.scheduler_job_run_accepted","job_id":"ai-news-digest","status":"accepted"}',
      "completed",
    ),
  ).toMatchObject({ kind: "outcome", outcome: "runAccepted" });
  expect(
    schedulerReadout("coddy_scheduler_job_delete", job, '{"object":"coddy.scheduler_job_deleted","job_id":"ai-news-digest"}', "completed"),
  ).toMatchObject({ kind: "outcome", outcome: "deleted" });
});

test("stopping a job that was not running says so", () => {
  const cancel = (cancelled: boolean) =>
    schedulerReadout(
      "coddy_scheduler_job_cancel",
      job,
      JSON.stringify({ object: "coddy.scheduler_job_cancel", job_id: "ai-news-digest", cancelled }),
      "completed",
    );
  expect(cancel(true)).toMatchObject({ outcome: "cancelled" });
  expect(cancel(false)).toMatchObject({ outcome: "notRunning" });
});

test("a job that is still being acted on has no outcome yet", () => {
  expect(schedulerReadout("coddy_scheduler_job_resume", job, "", "in_progress")).toEqual({
    kind: "outcome",
    jobId: "ai-news-digest",
  });
});

test("creating or editing a job carries the fields the call set", () => {
  const args = JSON.stringify({
    job_id: "digest",
    description: "Daily AI news",
    schedule: "0 8 * * *",
    body: "Collect the news",
    mode: "agent",
  });
  expect(
    schedulerReadout("coddy_scheduler_job_create", args, '{"object":"coddy.scheduler_job_created","job_id":"digest"}', "completed"),
  ).toEqual({
    kind: "outcome",
    jobId: "digest",
    outcome: "created",
    job: {
      jobId: "digest",
      description: "Daily AI news",
      schedule: "0 8 * * *",
      body: "Collect the news",
      mode: "agent",
    },
  });
  expect(
    schedulerReadout(
      "coddy_scheduler_job_patch",
      JSON.stringify({ job_id: "digest", new_job_id: "news", paused: true }),
      '{"object":"coddy.scheduler_job_patched","job_id":"news"}',
      "completed",
    ),
  ).toEqual({
    kind: "outcome",
    jobId: "digest",
    outcome: "patched",
    renamedTo: "news",
    job: { jobId: "digest", paused: true },
  });
});

test("reading a job, the job list and its runs keeps their structure", () => {
  const got = schedulerReadout(
    "coddy_scheduler_job_get",
    job,
    JSON.stringify({ job_id: "ai-news-digest", schedule: "0 8 * * *", paused: false, running: true, body: "x", next_run_utc: "2026-09-17T08:00:00Z" }),
    "completed",
  );
  expect(got).toEqual({
    kind: "job",
    job: { jobId: "ai-news-digest", schedule: "0 8 * * *", paused: false, running: true, body: "x", nextRunUtc: "2026-09-17T08:00:00Z" },
  });
  const list = schedulerReadout(
    "coddy_scheduler_jobs_list",
    "{}",
    JSON.stringify({ scheduler: { enabled: true }, jobs: [{ job_id: "a", schedule: "* * * * *", paused: true, running: false }] }),
    "completed",
  );
  expect(list).toEqual({ kind: "jobs", jobs: [{ jobId: "a", schedule: "* * * * *", paused: true, running: false }] });
  const runs = schedulerReadout(
    "coddy_scheduler_job_runs",
    job,
    JSON.stringify({ object: "coddy.scheduler_job_runs", job_id: "ai-news-digest", runs: [{ session_id: "sess_1", started_at: "2026-09-16T08:00:00Z", ended_at: "2026-09-16T08:01:05Z", status: "completed" }] }),
    "completed",
  );
  expect(runs).toEqual({
    kind: "runs",
    jobId: "ai-news-digest",
    runs: [{ sessionId: "sess_1", startedAt: "2026-09-16T08:00:00Z", endedAt: "2026-09-16T08:01:05Z", status: "completed" }],
  });
});

test("an answer that is not the expected shape falls back to the raw text", () => {
  expect(schedulerReadout("coddy_scheduler_job_get", job, '{\n  "job_id": "cut', "completed")).toBeNull();
  expect(schedulerReadout("coddy_scheduler_job_resume", job, "error: no such job", "failed")).toBeNull();
  expect(schedulerReadout("read", job, "{}", "completed")).toBeNull();
});
