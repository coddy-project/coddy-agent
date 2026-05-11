import { useCallback, useEffect, useRef, useState } from "react";
import {
  schedulerCreateJob,
  schedulerDeleteJob,
  schedulerGetJob,
  schedulerPatchJob,
  schedulerPauseJob,
  schedulerResumeJob,
} from "./api";
import { describeCronScheduleOrError } from "./cronDescribe";
import { MarkdownLineEditor } from "./MarkdownLineEditor";
import {
  parseAppHash,
  setSchedulerJobHash,
  setSchedulerListHash,
} from "./hashRoute";
import type { SchedulerJob, SchedulerJobCreate } from "./types";
import {
  SchedulerIconPause,
  SchedulerIconPlay,
  SchedulerIconTrash,
} from "./schedulerToolbarIcons";

type EditorMode = "create" | "edit";

type FieldErrors = Partial<{
  jobId: string;
  description: string;
  schedule: string;
  body: string;
}>;

const AUTOSAVE_MS = 600;

function validateJobId(raw: string): string | null {
  const s = raw.trim();
  if (!s) {
    return "Required";
  }
  if (s.length > 64) {
    return "Too long";
  }
  if (/\s/.test(s)) {
    return "No spaces - use hyphens (example: daily-report)";
  }
  if (!/^[A-Za-z0-9][A-Za-z0-9-]*$/.test(s)) {
    return "Only letters, digits, and hyphens (example: daily-report)";
  }
  return null;
}

type FormRef = {
  mode: EditorMode;
  jobId: string | null;
  jobIdField: string;
  description: string;
  schedule: string;
  body: string;
  cwd: string;
  model: string;
  modeField: string;
  paused: boolean;
  loading: boolean;
  loadErr: string | null;
};

export function SchedulerJobEditorSheet(props: {
  open: boolean;
  mode: EditorMode;
  jobId: string | null;
  availableModels: string[];
  defaultModel: string;
  currentCwd: string;
  onClose: () => void;
  onSaved: (createdJobId?: string) => void;
  onDeleted: () => void;
}) {
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [saveErr, setSaveErr] = useState<string | null>(null);
  const [fieldErrs, setFieldErrs] = useState<FieldErrors>({});
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  const [jobIdField, setJobIdField] = useState("");
  const [description, setDescription] = useState("");
  const [schedule, setSchedule] = useState("0 * * * *");
  const [cwd, setCwd] = useState("");
  const [model, setModel] = useState("");
  const [modeField, setModeField] = useState("agent");
  const [body, setBody] = useState("");
  const [paused, setPaused] = useState(false);

  const lastCommittedRef = useRef<string | null>(null);
  const flushTimerRef = useRef<number>(0);
  const createdOnceRef = useRef(false);
  const onSavedRef = useRef(props.onSaved);
  onSavedRef.current = props.onSaved;
  const formRef = useRef<FormRef>({
    mode: "create",
    jobId: null,
    jobIdField: "",
    description: "",
    schedule: "",
    body: "",
    cwd: "",
    model: "",
    modeField: "agent",
    paused: false,
    loading: false,
    loadErr: null,
  });

  formRef.current = {
    mode: props.mode,
    jobId: props.jobId,
    jobIdField,
    description,
    schedule,
    body,
    cwd,
    model,
    modeField,
    paused,
    loading,
    loadErr,
  };

  const snapshotFromForm = useCallback((f: FormRef) => {
    return JSON.stringify({
      description: f.description.trim(),
      schedule: f.schedule.trim(),
      body: f.body,
      cwd: f.cwd.trim(),
      model: f.model.trim(),
      mode: f.modeField,
      paused: f.paused,
    });
  }, []);

  const collectFieldErrors = useCallback(
    (f: FormRef, forCreate: boolean): FieldErrors => {
      const errs: FieldErrors = {};
      const jid = f.jobIdField.trim();
      const desc = f.description.trim();
      const sch = f.schedule.trim();
      const bod = f.body;
      if (forCreate) {
        const jidErr = validateJobId(jid);
        if (jidErr) {
          errs.jobId = jidErr;
        }
      }
      if (!desc) {
        errs.description = "Required";
      }
      if (!sch) {
        errs.schedule = "Required";
      }
      if (!bod.trim()) {
        errs.body = "Required";
      }
      return errs;
    },
    [],
  );

  const runPatch = useCallback(async () => {
    const f = formRef.current;
    if (f.mode !== "edit" || f.loading || f.loadErr) {
      return;
    }
    const existing = (f.jobId || "").trim();
    if (!existing) {
      return;
    }
    const errs = collectFieldErrors(f, false);
    setFieldErrs(errs);
    if (Object.keys(errs).length > 0) {
      return;
    }
    const snap = snapshotFromForm(f);
    if (snap === lastCommittedRef.current) {
      return;
    }
    setSaving(true);
    setSaveErr(null);
    try {
      const res = await schedulerPatchJob(existing, {
        description: f.description.trim(),
        schedule: f.schedule.trim(),
        body: f.body,
        paused: f.paused,
        ...(f.cwd.trim() ? { cwd: f.cwd.trim() } : { cwd: "" }),
        ...(f.model.trim() ? { model: f.model.trim() } : { model: "" }),
        mode: f.modeField,
      });
      if (!res.ok) {
        setSaveErr(res.message);
        return;
      }
      lastCommittedRef.current = snap;
      onSavedRef.current();
    } finally {
      setSaving(false);
    }
  }, [collectFieldErrors, snapshotFromForm]);

  const runCreate = useCallback(async () => {
    const f = formRef.current;
    if (f.mode !== "create" || createdOnceRef.current) {
      return;
    }
    const errs = collectFieldErrors(f, true);
    setFieldErrs(errs);
    if (Object.keys(errs).length > 0) {
      return;
    }
    const jid = f.jobIdField.trim();
    const payload: SchedulerJobCreate = {
      job_id: jid,
      description: f.description.trim(),
      schedule: f.schedule.trim(),
      body: f.body,
      paused: f.paused,
      ...(f.cwd.trim() ? { cwd: f.cwd.trim() } : {}),
      ...(f.model.trim() ? { model: f.model.trim() } : {}),
      ...(f.modeField ? { mode: f.modeField } : {}),
    };
    setSaving(true);
    setSaveErr(null);
    try {
      const res = await schedulerCreateJob(payload);
      if (!res.ok) {
        setSaveErr(res.message);
        return;
      }
      createdOnceRef.current = true;
      const hp = parseAppHash();
      setSchedulerJobHash(jid, {
        historySidebar: hp.branch === "scheduler" && hp.historyOpen,
      });
      onSavedRef.current(jid);
    } finally {
      setSaving(false);
    }
  }, [collectFieldErrors]);

  useEffect(() => {
    if (!props.open || props.mode !== "create") {
      return;
    }
    lastCommittedRef.current = null;
    createdOnceRef.current = false;
    setSaveErr(null);
    setFieldErrs({});
    setLoadErr(null);
    setJobIdField("");
    setDescription("");
    setSchedule("0 * * * *");
    setCwd(props.currentCwd || "");
    setModel(props.defaultModel || "");
    setModeField("agent");
    setBody("");
    setPaused(false);
    setLoading(false);
  }, [props.open, props.mode]);

  useEffect(() => {
    if (!props.open || props.mode !== "edit") {
      return;
    }
    const jid = (props.jobId || "").trim();
    if (!jid) {
      return;
    }
    lastCommittedRef.current = null;
    createdOnceRef.current = false;
    setSaveErr(null);
    setFieldErrs({});
    setLoadErr(null);
    let cancelled = false;
    setLoading(true);
    void (async () => {
      const res = await schedulerGetJob(jid);
      if (cancelled) {
        return;
      }
      setLoading(false);
      if (!res.ok) {
        setLoadErr(res.message);
        return;
      }
      const j: SchedulerJob = res.data;
      setJobIdField(j.job_id);
      setDescription(j.description || "");
      setSchedule(j.schedule || "");
      setCwd(j.cwd || "");
      setModel(j.model || "");
      setModeField(
        (j.mode || "agent").toLowerCase() === "plan" ? "plan" : "agent",
      );
      setBody(j.body || "");
      setPaused(!!j.paused);
      lastCommittedRef.current = JSON.stringify({
        description: (j.description || "").trim(),
        schedule: (j.schedule || "").trim(),
        body: j.body || "",
        cwd: (j.cwd || "").trim(),
        model: (j.model || "").trim(),
        mode:
          (j.mode || "agent").toLowerCase() === "plan" ? "plan" : "agent",
        paused: !!j.paused,
      });
    })();
    return () => {
      cancelled = true;
    };
  }, [props.open, props.mode, props.jobId]);

  useEffect(() => {
    if (!props.open || props.mode !== "edit" || loading || loadErr) {
      return;
    }
    const jid = (props.jobId || "").trim();
    if (!jid || lastCommittedRef.current === null) {
      return;
    }
    if (snapshotFromForm(formRef.current) === lastCommittedRef.current) {
      return;
    }
    window.clearTimeout(flushTimerRef.current);
    flushTimerRef.current = window.setTimeout(() => {
      void runPatch();
    }, AUTOSAVE_MS);
    return () => window.clearTimeout(flushTimerRef.current);
  }, [
    props.open,
    props.mode,
    props.jobId,
    loading,
    loadErr,
    description,
    schedule,
    body,
    cwd,
    model,
    modeField,
    paused,
    snapshotFromForm,
    runPatch,
  ]);

  useEffect(() => {
    if (!props.open || props.mode !== "create" || createdOnceRef.current) {
      return;
    }
    window.clearTimeout(flushTimerRef.current);
    flushTimerRef.current = window.setTimeout(() => {
      void runCreate();
    }, AUTOSAVE_MS);
    return () => window.clearTimeout(flushTimerRef.current);
  }, [
    props.open,
    props.mode,
    jobIdField,
    description,
    schedule,
    body,
    cwd,
    model,
    modeField,
    paused,
    runCreate,
  ]);

  const cronHint = describeCronScheduleOrError(schedule);

  async function onPauseToggle() {
    const jid = (props.jobId || "").trim();
    if (!jid) {
      return;
    }
    setSaveErr(null);
    setSaving(true);
    try {
      const res = paused
        ? await schedulerResumeJob(jid)
        : await schedulerPauseJob(jid);
      if (!res.ok) {
        setSaveErr(res.message);
        return;
      }
      setPaused(!paused);
      lastCommittedRef.current = null;
      onSavedRef.current();
    } finally {
      setSaving(false);
    }
  }

  async function onDelete() {
    if (props.mode !== "edit") {
      return;
    }
    const jid = (props.jobId || "").trim();
    if (!jid) {
      return;
    }
    const ok = window.confirm(`Delete scheduler job "${jid}"?`);
    if (!ok) {
      return;
    }
    setSaveErr(null);
    setSaving(true);
    try {
      const res = await schedulerDeleteJob(jid);
      if (!res.ok) {
        setSaveErr(res.message);
        return;
      }
      const p = parseAppHash();
      const hist = p.branch === "scheduler" && p.historyOpen;
      setSchedulerListHash({ historySidebar: hist });
      props.onDeleted();
    } finally {
      setSaving(false);
    }
  }

  if (!props.open) {
    return null;
  }

  return (
    <div
      className="scheduler-job-editor-dock"
      role="dialog"
      aria-modal={false}
      aria-label={
        props.mode === "create" ? "New scheduler job" : "Edit scheduler job"
      }
      data-testid="scheduler-editor-panel"
    >
      <div className="scheduler-editor-head">
        <span>
          {props.mode === "create"
            ? "New job"
            : `Job ${jobIdField || props.jobId || ""}`}
        </span>
        <button
          type="button"
          className="sessions-close"
          aria-label="Close editor"
          data-testid="scheduler-editor-close"
          onClick={props.onClose}
        >
          ×
        </button>
      </div>

      <div className="scheduler-editor-scroll">
        {loadErr ? (
          <div className="sessions-empty" data-testid="scheduler-editor-load-err">
            {loadErr}
          </div>
        ) : null}
        {props.mode === "edit" && loading ? (
          <div className="sessions-empty">Loading…</div>
        ) : null}

        {!loadErr && (props.mode === "create" || !loading) ? (
          <div className="scheduler-editor-form">
            <label className="scheduler-field">
              <span className="scheduler-field-label">job_id</span>
              <span className="scheduler-field-help">
                Filename - letters, digits, hyphens (example: daily-report).
              </span>
              <input
                className={[
                  "scheduler-field-input",
                  fieldErrs.jobId ? "scheduler-field-input-err" : "",
                ]
                  .filter(Boolean)
                  .join(" ")}
                value={jobIdField}
                disabled={props.mode === "edit"}
                onChange={(ev) => setJobIdField(ev.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
              {fieldErrs.jobId ? (
                <div className="scheduler-field-err">{fieldErrs.jobId}</div>
              ) : null}
            </label>
            <label className="scheduler-field">
              <span className="scheduler-field-label">description</span>
              <input
                className={[
                  "scheduler-field-input",
                  fieldErrs.description ? "scheduler-field-input-err" : "",
                ]
                  .filter(Boolean)
                  .join(" ")}
                value={description}
                onChange={(ev) => setDescription(ev.target.value)}
              />
              {fieldErrs.description ? (
                <div className="scheduler-field-err">
                  {fieldErrs.description}
                </div>
              ) : null}
            </label>
            <label className="scheduler-field">
              <span className="scheduler-field-label">
                schedule (UTC, 5 fields)
              </span>
              <input
                className={[
                  "scheduler-field-input",
                  "scheduler-field-input-cron",
                  fieldErrs.schedule ? "scheduler-field-input-err" : "",
                ]
                  .filter(Boolean)
                  .join(" ")}
                value={schedule}
                onChange={(ev) => setSchedule(ev.target.value)}
                spellCheck={false}
                placeholder="0 * * * *"
              />
              {fieldErrs.schedule ? (
                <div className="scheduler-field-err">{fieldErrs.schedule}</div>
              ) : null}
            </label>
            <div
              className={
                cronHint.ok
                  ? "scheduler-cron-hint"
                  : "scheduler-cron-hint scheduler-cron-hint-err"
              }
              data-testid="scheduler-cron-hint"
            >
              {cronHint.ok ? cronHint.text : cronHint.error}
            </div>
            <label className="scheduler-field">
              <span className="scheduler-field-label">cwd (optional)</span>
              <span className="scheduler-field-help">
                Defaults to the agent working directory for this instance.
              </span>
              <input
                className="scheduler-field-input"
                value={cwd}
                onChange={(ev) => setCwd(ev.target.value)}
                placeholder={props.currentCwd || ""}
              />
            </label>
            <label className="scheduler-field">
              <span className="scheduler-field-label">mode</span>
              <select
                className="scheduler-field-input"
                value={modeField}
                onChange={(ev) => setModeField(ev.target.value)}
              >
                <option value="agent">agent</option>
                <option value="plan">plan</option>
              </select>
            </label>
            <label className="scheduler-field">
              <span className="scheduler-field-label">model</span>
              {props.availableModels.length > 0 ? (
                <select
                  className="scheduler-field-input"
                  value={model}
                  onChange={(ev) => setModel(ev.target.value)}
                >
                  {props.availableModels.map((m) => (
                    <option key={m} value={m}>
                      {m}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  className="scheduler-field-input"
                  value={model}
                  onChange={(ev) => setModel(ev.target.value)}
                  spellCheck={false}
                  placeholder={props.defaultModel || ""}
                />
              )}
            </label>
            <div className="scheduler-field scheduler-field-stack">
              <span className="scheduler-field-label">body (markdown)</span>
              <div
                className={[
                  "scheduler-body-editor-wrap",
                  fieldErrs.body ? "scheduler-body-editor-wrap-err" : "",
                ]
                  .filter(Boolean)
                  .join(" ")}
              >
                <MarkdownLineEditor
                  value={body}
                  onChange={setBody}
                  aria-label="Job body markdown"
                  placeholder="Instruction for the scheduled run…"
                />
              </div>
              {fieldErrs.body ? (
                <div className="scheduler-field-err">{fieldErrs.body}</div>
              ) : null}
            </div>
            {saveErr ? (
              <div
                className="scheduler-save-err"
                data-testid="scheduler-editor-save-err"
              >
                {saveErr}
              </div>
            ) : null}
          </div>
        ) : null}
      </div>

      <div className="scheduler-editor-footer">
        {props.mode === "edit" && !loading && !loadErr ? (
          <button
            type="button"
            className="scheduler-btn scheduler-btn-icon-only"
            disabled={saving}
            data-testid="scheduler-editor-pause-toggle"
            title={paused ? "Resume" : "Pause"}
            aria-label={paused ? "Resume" : "Pause"}
            onClick={() => void onPauseToggle()}
          >
            {paused ? <SchedulerIconPlay /> : <SchedulerIconPause />}
          </button>
        ) : null}
        {props.mode === "edit" ? (
          <button
            type="button"
            className="scheduler-btn scheduler-btn-danger scheduler-btn-icon-only"
            disabled={saving || loading}
            data-testid="scheduler-editor-delete"
            title="Delete"
            aria-label="Delete"
            onClick={() => void onDelete()}
          >
            <SchedulerIconTrash />
          </button>
        ) : null}
      </div>
    </div>
  );
}
