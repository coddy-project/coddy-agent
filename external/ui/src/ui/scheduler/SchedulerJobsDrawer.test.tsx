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
