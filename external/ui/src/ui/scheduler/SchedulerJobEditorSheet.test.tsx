import React from "react";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { SchedulerJobEditorSheet } from "./SchedulerJobEditorSheet";

vi.mock("./api", () => ({
  schedulerGetJob: vi.fn(() =>
    Promise.resolve({
      ok: true,
      data: {
        job_id: "demo",
        description: "",
        schedule: "0 * * * *",
        body: "hello",
        cwd: "",
        model: "",
        mode: "agent",
        paused: false,
        running: false,
      },
    }),
  ),
  schedulerPatchJob: vi.fn(() => Promise.resolve({ ok: true })),
  schedulerCreateJob: vi.fn(() =>
    Promise.resolve({ ok: true, job_id: "new" }),
  ),
  schedulerDeleteJob: vi.fn(() => Promise.resolve({ ok: true })),
  schedulerPauseJob: vi.fn(() => Promise.resolve({ ok: true })),
  schedulerResumeJob: vi.fn(() => Promise.resolve({ ok: true })),
}));

afterEach(() => cleanup());

test("body markdown wrap keeps editor spacing hook class for layout CSS", async () => {
  render(
    <SchedulerJobEditorSheet
      open
      mode="edit"
      jobId="demo"
      availableModels={["m"]}
      defaultModel="m"
      currentCwd="/tmp"
      onClose={() => {}}
      onSaved={() => {}}
      onDeleted={() => {}}
    />,
  );
  await waitFor(() => {
    expect(screen.queryByText("Loading…")).not.toBeInTheDocument();
  });
  const wrap = document.querySelector(".scheduler-body-editor-wrap");
  expect(wrap).not.toBeNull();
  expect(
    document.querySelector(".scheduler-editor-scroll-inner"),
  ).not.toBeNull();
});
