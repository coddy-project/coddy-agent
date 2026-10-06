import React from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ConfirmProvider } from "../components/useConfirm";
import { SchedulerJobsDrawer, groupSchedulerJobs } from "./SchedulerJobsDrawer";
import { SchedulerJobEditorSheet } from "./SchedulerJobEditorSheet";
import { schedulerJobUrl } from "./api";
import { parseSchedulerJobRef, schedulerJobRef, type SchedulerJob } from "./types";

vi.mock("./api", async (orig) => {
  const real = await orig<typeof import("./api")>();
  return {
    ...real,
    schedulerGetJob: vi.fn(),
    schedulerPatchJob: vi.fn(() => Promise.resolve({ ok: true })),
    schedulerCreateJob: vi.fn(() =>
      Promise.resolve({ ok: true, data: { job_id: "lint", scope: "project", workspace: "/srv/app" } }),
    ),
    schedulerDeleteJob: vi.fn(() => Promise.resolve({ ok: true })),
    schedulerPauseJob: vi.fn(() => Promise.resolve({ ok: true })),
    schedulerResumeJob: vi.fn(() => Promise.resolve({ ok: true })),
    schedulerTrustJob: vi.fn(() => Promise.resolve({ ok: true, data: { trusted: true } })),
    schedulerUntrustJob: vi.fn(() => Promise.resolve({ ok: true, data: { trusted: false } })),
  };
});

afterEach(() => cleanup());

const job = (over: Partial<SchedulerJob>): SchedulerJob => ({
  job_id: "x",
  description: "d",
  schedule: "0 * * * *",
  paused: false,
  running: false,
  ...over,
});

test("a project job's reference keeps it apart from a user job of the same id", () => {
  const ref = schedulerJobRef({ job_id: "lint", scope: "project", workspace: "/srv/app" });
  expect(ref).toBe("/srv/app/lint");
  expect(parseSchedulerJobRef(ref)).toEqual({ id: "lint", scope: "project", workspace: "/srv/app" });
  expect(parseSchedulerJobRef("lint")).toEqual({ id: "lint", scope: "user", workspace: "" });
  expect(schedulerJobUrl(ref, "/run")).toBe(
    "/coddy/scheduler/jobs/lint/run?scope=project&workspace=%2Fsrv%2Fapp",
  );
  expect(schedulerJobUrl("lint", "/run")).toBe("/coddy/scheduler/jobs/lint/run");
});

test("jobs group into global, the chat's project first, then other projects", () => {
  const groups = groupSchedulerJobs(
    [
      job({ job_id: "u" }),
      job({ job_id: "b", scope: "project", workspace: "/other" }),
      job({ job_id: "a", scope: "project", workspace: "/srv/app" }),
    ],
    "/srv/app",
  );
  expect(groups.map((g) => g.workspace)).toEqual(["", "/srv/app", "/other"]);
});

test("a project job that waits for approval shows its badge and the shield, not Run", () => {
  render(
    <SchedulerJobsDrawer
      open
      selectedJobId={null}
      onClose={() => {}}
      scheduler={{ enabled: true, dir: "/h/scheduler", timeout: "30m", max_queue: 10, runs_active: 0, retain_sessions: 5, workspace: "/srv/app" }}
      jobs={[
        job({ job_id: "nightly" }),
        job({ job_id: "lint", scope: "project", workspace: "/srv/app", trust: "needs_approval" }),
        job({ job_id: "clash", scope: "project", workspace: "/srv/app", trust: "conflict" }),
      ]}
      listError={null}
      loading={false}
      onAddJob={() => {}}
      onOpenJob={() => {}}
      onOpenRuns={() => {}}
      onRunJob={() => {}}
      onCancelJob={() => {}}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
    />,
  );
  expect(screen.getByText("Global")).toBeInTheDocument();
  expect(screen.getByText("This project · app")).toBeInTheDocument();
  expect(screen.getByTestId("scheduler-trust-lint")).toHaveTextContent("needs approval");
  expect(screen.getByTestId("scheduler-approve-lint")).toBeInTheDocument();
  expect(screen.queryByTestId("scheduler-run-lint")).toBeNull();
  expect(screen.getByTestId("scheduler-run-clash")).toBeDisabled();
  expect(screen.getByTestId("scheduler-run-nightly")).toBeEnabled();
});

test("the approval block shows the raw file and approves the digest it showed", async () => {
  const api = await import("./api");
  vi.mocked(api.schedulerGetJob).mockResolvedValue({
    ok: true,
    data: job({
      job_id: "lint",
      scope: "project",
      workspace: "/srv/app",
      trust: "needs_approval",
      digest: "sha256:abc",
      raw: "---\nschedule: \"0 * * * *\"\n---\nlint it\n",
      body: "lint it",
    }),
  } as never);
  const onSaved = vi.fn();
  render(
    <ConfirmProvider>
      <SchedulerJobEditorSheet
        open
        mode="edit"
        jobId="/srv/app/lint"
        availableModels={["m"]}
        defaultModel="m"
        currentCwd="/srv/app"
        projectTrust="ask"
        onClose={() => {}}
        onSaved={onSaved}
        onDeleted={() => {}}
      />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(screen.getByTestId("scheduler-trust-block")).toBeInTheDocument());
  expect(screen.getByTestId("scheduler-trust-raw")).toHaveTextContent("lint it");
  fireEvent.click(screen.getByTestId("scheduler-trust-toggle"));
  await waitFor(() => expect(api.schedulerTrustJob).toHaveBeenCalledWith("/srv/app/lint", "sha256:abc"));
  // The editor did not take the bare id for a rename of the reference.
  expect(api.schedulerPatchJob).not.toHaveBeenCalled();
});

test("a project job is created in the chat's workspace with the session header", async () => {
  const api = await import("./api");
  const onSaved = vi.fn();
  render(
    <ConfirmProvider>
      <SchedulerJobEditorSheet
        open
        mode="create"
        jobId={null}
        availableModels={["m"]}
        defaultModel="m"
        currentCwd="/srv/app"
        sessionHeaders={{ "X-Coddy-Session-ID": "sess_1" }}
        workspacePath="/srv/app"
        onClose={() => {}}
        onSaved={onSaved}
        onDeleted={() => {}}
      />
    </ConfirmProvider>,
  );
  fireEvent.click(screen.getByTestId("scheduler-scope-project"));
  fireEvent.change(screen.getAllByRole("textbox")[0]!, { target: { value: "lint" } });
  fireEvent.change(screen.getByRole("textbox", { name: /description/ }), { target: { value: "Lint" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Job body markdown" }), { target: { value: "lint it" } });
  await waitFor(() => expect(api.schedulerCreateJob).toHaveBeenCalled(), { timeout: 3000 });
  const [payload, headers] = vi.mocked(api.schedulerCreateJob).mock.calls[0]!;
  expect(payload.scope).toBe("project");
  expect(payload.cwd).toBeUndefined();
  expect(headers).toEqual({ "X-Coddy-Session-ID": "sess_1" });
  await waitFor(() => expect(onSaved).toHaveBeenCalledWith("/srv/app/lint"));
});

test("without a session the project scope is offered but disabled", () => {
  render(
    <ConfirmProvider>
      <SchedulerJobEditorSheet
        open
        mode="create"
        jobId={null}
        availableModels={["m"]}
        defaultModel="m"
        currentCwd="/srv/app"
        workspacePath=""
        onClose={() => {}}
        onSaved={() => {}}
        onDeleted={() => {}}
      />
    </ConfirmProvider>,
  );
  expect(screen.getByTestId("scheduler-scope-project")).toBeDisabled();
});

test("requests about a project job carry the chat's session, a user job's do not", async () => {
  const api = await import("./api");
  const calls: Array<{ url: string; headers: Record<string, string> }> = [];
  const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (url, init) => {
    calls.push({ url: String(url), headers: (init?.headers || {}) as Record<string, string> });
    return new Response(JSON.stringify({ status: "accepted" }), { status: 202 });
  });
  try {
    api.setSchedulerSessionHeaders({ "X-Coddy-Session-ID": "sess_1" });
    await api.schedulerRunJob("/srv/app/lint");
    await api.schedulerRunJob("nightly");
  } finally {
    fetchSpy.mockRestore();
    api.setSchedulerSessionHeaders({});
  }
  expect(calls[0]!.url).toContain("scope=project");
  expect(calls[0]!.headers["X-Coddy-Session-ID"]).toBe("sess_1");
  expect(calls[1]!.headers["X-Coddy-Session-ID"]).toBeUndefined();
});

test("an edit of a project job reads the approval block again without resetting the form", async () => {
  const api = await import("./api");
  const first = job({
    job_id: "lint",
    scope: "project",
    workspace: "/srv/app",
    trust: "needs_approval",
    digest: "sha256:old",
    raw: "old file",
    body: "lint it",
    description: "Lint",
  });
  vi.mocked(api.schedulerGetJob).mockReset();
  vi.mocked(api.schedulerGetJob)
    .mockResolvedValueOnce({ ok: true, data: first } as never)
    .mockResolvedValue({ ok: true, data: { ...first, digest: "sha256:new", raw: "new file" } } as never);
  vi.mocked(api.schedulerPatchJob).mockResolvedValue({ ok: true, data: { job_id: "lint" } } as never);
  render(
    <ConfirmProvider>
      <SchedulerJobEditorSheet
        open
        mode="edit"
        jobId="/srv/app/lint"
        availableModels={["m"]}
        defaultModel="m"
        currentCwd="/srv/app"
        projectTrust="ask"
        onClose={() => {}}
        onSaved={() => {}}
        onDeleted={() => {}}
      />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(screen.getByTestId("scheduler-trust-raw")).toHaveTextContent("old file"));
  fireEvent.change(screen.getByRole("textbox", { name: /description/ }), { target: { value: "Lint more" } });
  await waitFor(() => expect(screen.getByTestId("scheduler-trust-raw")).toHaveTextContent("new file"), { timeout: 3000 });
  expect((screen.getByRole("textbox", { name: /description/ }) as HTMLInputElement).value).toBe("Lint more");
  fireEvent.click(screen.getByTestId("scheduler-trust-toggle"));
  await waitFor(() => expect(api.schedulerTrustJob).toHaveBeenLastCalledWith("/srv/app/lint", "sha256:new"));
});
