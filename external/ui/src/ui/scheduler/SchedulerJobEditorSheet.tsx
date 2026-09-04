import { useCallback, useEffect, useRef, useState } from "react";
import { useConfirm } from "../components/useConfirm";
import { useT } from "../i18n/I18nProvider";
import {
  schedulerCreateJob,
  schedulerDeleteJob,
  schedulerGetJob,
  schedulerPatchJob,
  schedulerPauseJob,
  schedulerResumeJob,
} from "./api";
import { describeCronScheduleOrError } from "./cronDescribe";
import {
  AUTOSAVE_MS,
  collectJobFieldErrors,
  normalizeJobMode,
  snapshotJobForm,
  type EditorMode,
  type FieldErrors,
} from "./jobEditorForm";
import {
  parseAppHash,
  setSchedulerJobHash,
  setSchedulerListHash,
} from "./hashRoute";
import { SchedulerJobEditorFields } from "./SchedulerJobEditorFields";
import type {
  SchedulerJob,
  SchedulerJobCreate,
  SchedulerJobPatch,
} from "./types";
import {
  SchedulerIconPause,
  SchedulerIconResume,
  SchedulerIconTrash,
} from "./schedulerToolbarIcons";

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
  const { t } = useT();
  const confirm = useConfirm();
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

  const runPatch = useCallback(async () => {
    const f = formRef.current;
    if (f.mode !== "edit" || f.loading || f.loadErr) {
      return;
    }
    const existing = (f.jobId || "").trim();
    if (!existing) {
      return;
    }
    const errs = collectJobFieldErrors(f, {
      forCreate: false,
      existingJobId: existing,
    });
    setFieldErrs(errs);
    if (Object.keys(errs).length > 0) {
      return;
    }
    const snap = snapshotJobForm(f);
    if (snap === lastCommittedRef.current) {
      return;
    }
    const nextId = f.jobIdField.trim();
    setSaving(true);
    setSaveErr(null);
    try {
      const patch: SchedulerJobPatch = {
        description: f.description.trim(),
        schedule: f.schedule.trim(),
        body: f.body,
        paused: f.paused,
        ...(f.cwd.trim() ? { cwd: f.cwd.trim() } : { cwd: "" }),
        ...(f.model.trim() ? { model: f.model.trim() } : { model: "" }),
        mode: f.modeField,
      };
      if (nextId && nextId !== existing) {
        patch.job_id = nextId;
      }
      const res = await schedulerPatchJob(existing, patch);
      if (!res.ok) {
        setSaveErr(res.message);
        return;
      }
      const outId =
        (res.ok && res.data && typeof res.data.job_id === "string"
          ? res.data.job_id.trim()
          : "") ||
        nextId ||
        existing;
      lastCommittedRef.current = JSON.stringify({
        jobId: outId,
        description: f.description.trim(),
        schedule: f.schedule.trim(),
        body: f.body,
        cwd: f.cwd.trim(),
        model: f.model.trim(),
        mode: f.modeField,
        paused: f.paused,
      });
      if (outId !== existing) {
        const hp = parseAppHash();
        setSchedulerJobHash(outId, {
          historySidebar: hp.branch === "scheduler" && hp.historyOpen,
        });
        onSavedRef.current(outId);
      } else {
        onSavedRef.current();
      }
    } finally {
      setSaving(false);
    }
  }, []);

  const runCreate = useCallback(async () => {
    const f = formRef.current;
    if (f.mode !== "create" || createdOnceRef.current) {
      return;
    }
    const errs = collectJobFieldErrors(f, {
      forCreate: true,
      existingJobId: "",
    });
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
  }, []);

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
      setModeField(normalizeJobMode(j.mode));
      setBody(j.body || "");
      setPaused(!!j.paused);
      lastCommittedRef.current = JSON.stringify({
        jobId: j.job_id,
        description: (j.description || "").trim(),
        schedule: (j.schedule || "").trim(),
        body: j.body || "",
        cwd: (j.cwd || "").trim(),
        model: (j.model || "").trim(),
        mode: normalizeJobMode(j.mode),
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
    if (snapshotJobForm(formRef.current) === lastCommittedRef.current) {
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
    jobIdField,
    description,
    schedule,
    body,
    cwd,
    model,
    modeField,
    paused,
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
    const ok = await confirm({
      title: t("confirm.scheduler.deleteJob.title", { id: jid }),
      message: t("confirm.scheduler.deleteJob.message"),
      confirmLabel: t("common.delete"),
      variant: "danger",
    });
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
        props.mode === "create"
          ? t("scheduler.editorNewAriaLabel")
          : t("scheduler.editorEditAriaLabel")
      }
      data-testid="scheduler-editor-panel"
    >
      <div className="sessions-head">
        <span>
          {props.mode === "create"
            ? t("scheduler.newJob")
            : t("scheduler.jobTitle", {
                jobId: jobIdField || props.jobId || "",
              })}
        </span>
        <button
          type="button"
          className="sessions-close"
          aria-label={t("scheduler.closeEditor")}
          data-testid="scheduler-editor-close"
          onClick={props.onClose}
        >
          ×
        </button>
      </div>

      <div className="scheduler-editor-scroll">
        <div className="scheduler-editor-scroll-inner">
          {loadErr ? (
            <div
              className="sessions-empty"
              data-testid="scheduler-editor-load-err"
            >
              {loadErr}
            </div>
          ) : null}
          {props.mode === "edit" && loading ? (
            <div className="sessions-empty">{t("scheduler.loading")}</div>
          ) : null}

          {!loadErr && (props.mode === "create" || !loading) ? (
            <SchedulerJobEditorFields
              jobIdField={jobIdField}
              onJobIdChange={setJobIdField}
              description={description}
              onDescriptionChange={setDescription}
              schedule={schedule}
              onScheduleChange={setSchedule}
              cwd={cwd}
              onCwdChange={setCwd}
              modeField={modeField}
              onModeChange={setModeField}
              model={model}
              onModelChange={setModel}
              body={body}
              onBodyChange={setBody}
              fieldErrs={fieldErrs}
              cronHint={cronHint}
              saveErr={saveErr}
              availableModels={props.availableModels}
              defaultModel={props.defaultModel}
              currentCwd={props.currentCwd}
            />
          ) : null}
        </div>
      </div>

      <div className="scheduler-editor-footer">
        {props.mode === "edit" && !loading && !loadErr ? (
          <button
            type="button"
            className="scheduler-btn scheduler-btn-icon-only"
            disabled={saving}
            data-testid="scheduler-editor-pause-toggle"
            title={paused ? t("scheduler.resume") : t("scheduler.pause")}
            aria-label={paused ? t("scheduler.resume") : t("scheduler.pause")}
            onClick={() => void onPauseToggle()}
          >
            {paused ? <SchedulerIconResume /> : <SchedulerIconPause />}
          </button>
        ) : null}
        {props.mode === "edit" ? (
          <button
            type="button"
            className="scheduler-btn scheduler-btn-danger scheduler-btn-icon-only"
            disabled={saving || loading}
            data-testid="scheduler-editor-delete"
            title={t("scheduler.delete")}
            aria-label={t("scheduler.delete")}
            onClick={() => void onDelete()}
          >
            <SchedulerIconTrash />
          </button>
        ) : null}
      </div>
    </div>
  );
}
