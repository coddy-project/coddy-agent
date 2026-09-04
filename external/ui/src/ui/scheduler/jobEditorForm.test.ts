import { expect, test } from "vitest";
import {
  collectJobFieldErrors,
  normalizeJobMode,
  snapshotJobForm,
  validateJobId,
  type JobEditorForm,
} from "./jobEditorForm";

function form(over: Partial<JobEditorForm>): JobEditorForm {
  return {
    jobIdField: "daily-report",
    description: "desc",
    schedule: "0 * * * *",
    body: "do it",
    cwd: "",
    model: "",
    modeField: "agent",
    paused: false,
    ...over,
  };
}

test("validateJobId accepts simple ids and rejects empty/spaced/invalid ones", () => {
  expect(validateJobId("daily-report")).toBeNull();
  expect(validateJobId("  ok2  ")).toBeNull();
  expect(validateJobId("")).not.toBeNull();
  expect(validateJobId("has space")).not.toBeNull();
  expect(validateJobId("-leading-dash")).not.toBeNull();
  expect(validateJobId("x".repeat(65))).not.toBeNull();
});

test("snapshotJobForm trims fields but keeps the body verbatim", () => {
  const a = snapshotJobForm(form({ jobIdField: " j ", cwd: " /tmp " }));
  const b = snapshotJobForm(form({ jobIdField: "j", cwd: "/tmp" }));
  expect(a).toBe(b);
  expect(snapshotJobForm(form({ body: "do it " }))).not.toBe(
    snapshotJobForm(form({ body: "do it" })),
  );
});

test("collectJobFieldErrors requires description/schedule/body", () => {
  const errs = collectJobFieldErrors(
    form({ description: " ", schedule: "", body: "  " }),
    { forCreate: true, existingJobId: "" },
  );
  expect(errs.description).toBeTruthy();
  expect(errs.schedule).toBeTruthy();
  expect(errs.body).toBeTruthy();
});

test("collectJobFieldErrors validates the id only on create or rename", () => {
  const bad = form({ jobIdField: "bad id" });
  expect(
    collectJobFieldErrors(bad, { forCreate: true, existingJobId: "" }).jobId,
  ).toBeTruthy();
  // Unchanged id in edit mode is not re-validated.
  expect(
    collectJobFieldErrors(bad, { forCreate: false, existingJobId: "bad id" })
      .jobId,
  ).toBeUndefined();
  // A rename re-validates.
  expect(
    collectJobFieldErrors(bad, { forCreate: false, existingJobId: "old-id" })
      .jobId,
  ).toBeTruthy();
});

test("normalizeJobMode accepts the daemon's modes and falls back to agent", () => {
  expect(normalizeJobMode("agent")).toBe("agent");
  expect(normalizeJobMode("plan")).toBe("plan");
  expect(normalizeJobMode("ask")).toBe("ask");
  expect(normalizeJobMode("ASK")).toBe("ask");
  expect(normalizeJobMode("nonsense")).toBe("agent");
  expect(normalizeJobMode(undefined)).toBe("agent");
});
