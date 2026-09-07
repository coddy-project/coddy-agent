import React from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ConfirmProvider } from "../components/useConfirm";
import { SchedulerJobEditorSheet } from "./SchedulerJobEditorSheet";

const schedulerPatchJob = vi.fn();

vi.mock("./api", () => ({
  schedulerGetJob: vi.fn(() =>
    Promise.resolve({
      ok: true,
      data: {
        job_id: "old-id",
        description: "Desc",
        schedule: "0 * * * *",
        body: "body text",
        cwd: "",
        model: "",
        mode: "agent",
        paused: false,
        running: false,
      },
    }),
  ),
  schedulerPatchJob: (...args: unknown[]) => schedulerPatchJob(...args),
  schedulerCreateJob: vi.fn(),
  schedulerDeleteJob: vi.fn(),
  schedulerPauseJob: vi.fn(),
  schedulerResumeJob: vi.fn(),
}));

afterEach(() => {
  cleanup();
  schedulerPatchJob.mockReset();
});

test("edit mode can rename job_id via PATCH", async () => {
  schedulerPatchJob.mockResolvedValue({
    ok: true,
    data: { object: "coddy.scheduler_job", job_id: "new-id" },
  });
  const onSaved = vi.fn();
  render(
    <ConfirmProvider>
      <SchedulerJobEditorSheet
        open
        mode="edit"
        jobId="old-id"
        availableModels={[]}
        defaultModel=""
        currentCwd="/tmp"
        onClose={() => {}}
        onSaved={onSaved}
        onDeleted={() => {}}
      />
    </ConfirmProvider>,
  );
  await waitFor(() => {
    expect(screen.queryByText("Loading…")).not.toBeInTheDocument();
  });
  const jobIdInput = screen.getByRole("textbox", {
    name: /job_id/i,
  });
  expect(jobIdInput).not.toBeDisabled();
  fireEvent.change(jobIdInput, { target: { value: "new-id" } });
  await waitFor(
    () => {
      expect(schedulerPatchJob).toHaveBeenCalled();
    },
    { timeout: 2000 },
  );
  const [, patch] = schedulerPatchJob.mock.calls.at(-1) ?? [];
  expect(patch).toMatchObject({ job_id: "new-id" });
  await waitFor(() => {
    expect(onSaved).toHaveBeenCalledWith("new-id");
  });
});
