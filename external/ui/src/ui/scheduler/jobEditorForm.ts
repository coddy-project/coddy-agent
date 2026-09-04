import { t as translate } from "../i18n/i18n";

export type EditorMode = "create" | "edit";

export type FieldErrors = Partial<{
  jobId: string;
  description: string;
  schedule: string;
  body: string;
}>;

export const AUTOSAVE_MS = 600;

export const JOB_MODES = ["agent", "plan", "ask"] as const;
export type JobMode = (typeof JOB_MODES)[number];

// Frontmatter `mode` values the daemon accepts (external/scheduler/daemon
// parseSessionMode); anything else falls back to agent the same way it does.
export function normalizeJobMode(raw: string | undefined): JobMode {
  const v = (raw || "agent").toLowerCase();
  return (JOB_MODES as readonly string[]).includes(v) ? (v as JobMode) : "agent";
}

/** The editable field values of the job editor form. */
export type JobEditorForm = {
  jobIdField: string;
  description: string;
  schedule: string;
  body: string;
  cwd: string;
  model: string;
  modeField: string;
  paused: boolean;
};

export function validateJobId(raw: string): string | null {
  const s = raw.trim();
  if (!s) {
    return translate("scheduler.validation.required");
  }
  if (s.length > 64) {
    return translate("scheduler.validation.tooLong");
  }
  if (/\s/.test(s)) {
    return translate("scheduler.validation.noSpaces");
  }
  if (!/^[A-Za-z0-9][A-Za-z0-9-]*$/.test(s)) {
    return translate("scheduler.validation.invalidJobId");
  }
  return null;
}

/** Canonical autosave snapshot; equality against it suppresses no-op saves. */
export function snapshotJobForm(f: JobEditorForm): string {
  return JSON.stringify({
    jobId: f.jobIdField.trim(),
    description: f.description.trim(),
    schedule: f.schedule.trim(),
    body: f.body,
    cwd: f.cwd.trim(),
    model: f.model.trim(),
    mode: f.modeField,
    paused: f.paused,
  });
}

export function collectJobFieldErrors(
  f: JobEditorForm,
  opts: { forCreate: boolean; existingJobId: string },
): FieldErrors {
  const errs: FieldErrors = {};
  const jid = f.jobIdField.trim();
  const desc = f.description.trim();
  const sch = f.schedule.trim();
  const bod = f.body;
  if (opts.forCreate) {
    const jidErr = validateJobId(jid);
    if (jidErr) {
      errs.jobId = jidErr;
    }
  } else {
    if (jid !== opts.existingJobId) {
      const jidErr = validateJobId(jid);
      if (jidErr) {
        errs.jobId = jidErr;
      }
    }
  }
  if (!desc) {
    errs.description = translate("scheduler.validation.required");
  }
  if (!sch) {
    errs.schedule = translate("scheduler.validation.required");
  }
  if (!bod.trim()) {
    errs.body = translate("scheduler.validation.required");
  }
  return errs;
}
