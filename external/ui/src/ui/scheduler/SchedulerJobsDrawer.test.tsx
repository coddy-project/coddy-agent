import React from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { SchedulerJobsDrawer } from "./SchedulerJobsDrawer";
import type { SchedulerJob } from "./types";

afterEach(() => cleanup());

const baseJob = (id: string): SchedulerJob => ({
  job_id: id,
  description: "d",
  schedule: "0 * * * *",
  paused: false,
  running: false,
});

function renderDrawer(
  selectedJobId: string | null,
  jobs: SchedulerJob[],
  onOpenRuns: (jobId: string) => void = () => {},
) {
  return render(
    <SchedulerJobsDrawer
      open
      selectedJobId={selectedJobId}
      onClose={() => {}}
      scheduler={null}
      jobs={jobs}
      listError={null}
      loading={false}
      onAddJob={() => {}}
      onOpenJob={() => {}}
      onOpenRuns={onOpenRuns}
      onRunJob={() => {}}
      onCancelJob={() => {}}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
    />,
  );
}

test("selected job row has active class like History", () => {
  renderDrawer("b", [baseJob("a"), baseJob("b")]);
  expect(screen.getByTestId("scheduler-job-row-a")).not.toHaveClass("active");
  expect(screen.getByTestId("scheduler-job-row-b")).toHaveClass("active");
});

test("no active row when selectedJobId is null", () => {
  renderDrawer(null, [baseJob("a")]);
  expect(screen.getByTestId("scheduler-job-row-a")).not.toHaveClass("active");
});

test("drawer footer has Add job control without Refresh", () => {
  renderDrawer(null, [baseJob("a")]);
  expect(screen.getByRole("button", { name: "Add job" })).toBeInTheDocument();
  expect(screen.queryByTestId("scheduler-refresh")).toBeNull();
});

test("job row shows formatted next run without Next prefix", () => {
  renderDrawer(null, [
    {
      ...baseJob("demo"),
      next_run_utc: "2026-05-11T22:37:00Z",
    },
  ]);
  const row = screen.getByTestId("scheduler-job-row-demo");
  expect(row.textContent).toContain("2026-05-11 22:37 (UTC)");
  expect(row.textContent).not.toContain("Next ");
});

test("paused row shows only badge, no next run time", () => {
  renderDrawer(null, [
    {
      ...baseJob("demo"),
      paused: true,
      next_run_utc: "2026-05-11T22:37:00Z",
    },
  ]);
  const row = screen.getByTestId("scheduler-job-row-demo");
  expect(row.querySelector(".scheduler-job-paused")).toBeTruthy();
  expect(row.textContent).not.toContain("2026-05-11");
  expect(row.textContent).not.toContain("(UTC)");
});

test("job row main control exposes scheduler hash href", () => {
  renderDrawer(null, [baseJob("my-job")]);
  const main = screen
    .getByTestId("scheduler-job-row-my-job")
    .querySelector("a.scheduler-job-row-main");
  expect(main).toBeTruthy();
  expect(main).toHaveAttribute("href", "#/scheduler/jobs/my-job");
});

// The stop glyph of a running job was the text character "■" inside a wrapper whose
// stop rule sets font-size: 0 for the composer's drawn square, so the button showed an
// empty red circle. It is the same drawn square the composer's Stop carries.
test("a running job's Stop carries the drawn stop square", () => {
  renderDrawer(null, [{ ...baseJob("busy"), running: true }]);
  const stop = screen.getByTestId("scheduler-stop-busy");
  expect(
    stop.querySelector(".composer-send-glyph .composer-stop-square"),
  ).toBeTruthy();
  expect(stop.textContent?.trim()).toBe("");
});

// Every row opens the job's runs: a real href for new-tab gestures, and the
// in-place handler for a plain click. The list stays a list of jobs; the
// history of one job is the panel that link opens.
test("job row carries a Runs control with the runs hash", () => {
  const onOpenRuns = vi.fn();
  renderDrawer(null, [baseJob("nightly")], onOpenRuns);
  const runs = screen.getByTestId("scheduler-runs-nightly");
  expect(runs).toHaveAttribute("href", "#/scheduler/jobs/nightly/runs");
  fireEvent.click(runs);
  expect(onOpenRuns).toHaveBeenCalledWith("nightly");
});

test("job row shows the last run's status and clock, and nothing before the first run", () => {
  renderDrawer(null, [
    {
      ...baseJob("quiet"),
    },
    {
      ...baseJob("nightly"),
      last_run: {
        task_id: "bg_4",
        session_id: "sess_run",
        job_session_id: "sess_job",
        status: "failed",
        running: false,
        started_at: "2026-09-18T10:00:00Z",
        ended_at: "2026-09-18T10:03:00Z",
        elapsed_seconds: 180,
      },
    },
  ]);
  expect(screen.queryByTestId("scheduler-last-run-quiet")).toBeNull();
  expect(screen.queryByTestId("scheduler-last-run-dot-quiet")).toBeNull();
  const mark = screen.getByTestId("scheduler-last-run-nightly");
  expect(mark.textContent).toContain("Failed");
  // The dot stands on the name's line, in the marks column a History row
  // has; the status and the clock stay under the description.
  const dot = screen.getByTestId("scheduler-last-run-dot-nightly");
  expect(dot).toHaveClass("bgtask-dot--danger");
  expect(dot.closest(".scheduler-job-row-mark")).toBeTruthy();
  expect(mark.querySelector(".bgtask-dot")).toBeNull();
});

test("a job row is laid out like a History row: marks column, title, lines under it", () => {
  renderDrawer(null, [{ ...baseJob("nightly") }]);
  const block = screen
    .getByTestId("scheduler-job-row-nightly")
    .querySelector(".scheduler-job-row-text-block")!;
  const children = [...block.children].map((el) => el.className);
  expect(children[0]).toContain("scheduler-job-row-mark");
  expect(children[1]).toContain("scheduler-job-row-title-line");
  expect(children[2]).toContain("scheduler-job-row-desc");
});

test("a running job shows its pulsing dot alone: no status line under the description", () => {
  renderDrawer(null, [
    {
      ...baseJob("busy"),
      running: true,
      last_run: {
        task_id: "bg_5",
        session_id: "sess_run",
        job_session_id: "sess_job",
        status: "running",
        running: true,
        started_at: "2026-09-18T10:00:00Z",
        elapsed_seconds: 12,
      },
    },
  ]);
  // The dot pulses and the row's button is Stop: a "Running · 10:00" line
  // would only say it a third time.
  expect(screen.queryByTestId("scheduler-last-run-busy")).toBeNull();
  const dot = screen.getByTestId("scheduler-last-run-dot-busy");
  expect(dot).toHaveClass("bgtask-dot--running");
  expect(dot).toHaveAccessibleName(/Running/);
});
