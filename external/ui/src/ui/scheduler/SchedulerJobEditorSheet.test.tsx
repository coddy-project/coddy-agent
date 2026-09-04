import React from "react";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ConfirmProvider } from "../components/useConfirm";
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
    <ConfirmProvider>
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
      />
    </ConfirmProvider>,
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

test("edit form keeps a job's ask mode instead of collapsing it to agent", async () => {
  const api = await import("./api");
  vi.mocked(api.schedulerGetJob).mockResolvedValueOnce({
    ok: true,
    data: {
      job_id: "demo",
      description: "",
      schedule: "0 * * * *",
      body: "hello",
      cwd: "",
      model: "",
      mode: "ask",
      paused: false,
      running: false,
    },
  } as never);
  render(
    <ConfirmProvider>
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
      />
    </ConfirmProvider>,
  );
  await waitFor(() => {
    expect(screen.queryByText("Loading…")).not.toBeInTheDocument();
  });
  const select = screen.getByLabelText("mode") as HTMLSelectElement;
  expect(select.value).toBe("ask");
  expect(Array.from(select.options).map((o) => o.value)).toEqual([
    "agent",
    "plan",
    "ask",
  ]);
});
