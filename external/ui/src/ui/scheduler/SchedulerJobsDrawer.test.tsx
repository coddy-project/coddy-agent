import React from "react";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
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

function renderDrawer(selectedJobId: string | null, jobs: SchedulerJob[]) {
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
  expect(
    screen.getByRole("button", { name: "Add job" }),
  ).toBeInTheDocument();
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
  expect(stop.querySelector(".composer-send-glyph .composer-stop-square")).toBeTruthy();
  expect(stop.textContent?.trim()).toBe("");
});
