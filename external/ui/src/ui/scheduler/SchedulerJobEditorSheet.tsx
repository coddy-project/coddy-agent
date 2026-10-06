import { useCallback, useEffect, useRef, useState } from "react";
import { useConfirm } from "../components/useConfirm";
import { useT } from "../i18n/I18nProvider";
import { t as translate } from "../i18n/i18n";
import {
  schedulerCreateJob,
  schedulerDeleteJob,
  schedulerGetJob,
  schedulerPatchJob,
  schedulerPauseJob,
  schedulerResumeJob,
  schedulerTrustJob,
  schedulerUntrustJob,
} from "./api";
import { describeCronScheduleOrError } from "./cronDescribe";
import { MarkdownLineEditor } from "./MarkdownLineEditor";
import {
  parseAppHash,
  setSchedulerJobHash,
  setSchedulerListHash,
} from "./hashRoute";
import type {
  SchedulerJob,
  SchedulerJobCreate,
  SchedulerJobPatch,
  SchedulerJobScope,
  SchedulerTrust,
} from "./types";
import { parseSchedulerJobRef, schedulerJobRef } from "./types";
import { IconShield } from "../settings/icons";
import { workspaceName } from "./SchedulerJobsDrawer";
import {
  SchedulerIconPause,
  SchedulerIconResume,
  SchedulerIconRuns,
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

/** The bare job id of a job reference (see SchedulerJobRef). */
function bareJobId(ref: string | null | undefined): string {
  return parseSchedulerJobRef(ref || "").id;
}

/** The reference of the same job under a new id (a rename keeps the scope). */
function refWithId(ref: string, id: string): string {
  const p = parseSchedulerJobRef(ref);
  return schedulerJobRef({ job_id: id, scope: p.scope, workspace: p.workspace });
}

/** What the approval block of a project job shows. */
type ProjectTrustView = {
  workspace: string;
  trust: SchedulerTrust;
  reason: string;
  digest: string;
  raw: string;
};

const JOB_MODES = ["agent", "plan", "ask"] as const;
type JobMode = (typeof JOB_MODES)[number];

// Frontmatter `permission_mode` values; "" is the unattended default (bypass).
const PERMISSION_MODES = ["", "accept_edits", "ask", "bypass"] as const;
type JobPermissionMode = (typeof PERMISSION_MODES)[number];

function normalizePermissionMode(raw: string | undefined): JobPermissionMode {
  const v = (raw || "").trim().toLowerCase();
  return (PERMISSION_MODES as readonly string[]).includes(v)
    ? (v as JobPermissionMode)
    : "";
}

// Frontmatter `mode` values the daemon accepts (external/scheduler/daemon
// parseSessionMode); anything else falls back to agent the same way it does.
function normalizeJobMode(raw: string | undefined): JobMode {
  const v = (raw || "agent").toLowerCase();
  return (JOB_MODES as readonly string[]).includes(v) ? (v as JobMode) : "agent";
}

function validateJobId(raw: string): string | null {
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
  agent: string;
  permissionMode: string;
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
  /** Opens the runs panel of the job being edited. */
  onOpenRuns?: (jobId: string) => void;
  /** The chat's session header: a project job is created in its workspace. */
  sessionHeaders?: Record<string, string>;
  /** The chat's workspace when a session exists; empty offers no project scope. */
  workspacePath?: string;
  /** scheduler.project_trust: the shield is offered under ask only. */
  projectTrust?: "ask" | "allow" | "deny";
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
  const [agent, setAgent] = useState("");
  const [permissionMode, setPermissionMode] = useState("");
  const [body, setBody] = useState("");
  const [paused, setPaused] = useState(false);
  const [scopeField, setScopeField] = useState<SchedulerJobScope>("user");
  const [projectView, setProjectView] = useState<ProjectTrustView | null>(null);
  const [trustBusy, setTrustBusy] = useState(false);
  const [reloadSeq, setReloadSeq] = useState(0);

  const lastCommittedRef = useRef<string | null>(null);
  const flushTimerRef = useRef<number>(0);
  const createdOnceRef = useRef(false);
  const onSavedRef = useRef(props.onSaved);
  onSavedRef.current = props.onSaved;
  const scopeRef = useRef<SchedulerJobScope>("user");
  scopeRef.current = scopeField;
  const sessionHeadersRef = useRef(props.sessionHeaders);
  sessionHeadersRef.current = props.sessionHeaders;
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
    agent: "",
    permissionMode: "",
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
    agent,
    permissionMode,
    paused,
    loading,
    loadErr,
  };

  const snapshotFromForm = useCallback((f: FormRef) => {
    return JSON.stringify({
      jobId: f.jobIdField.trim(),
      description: f.description.trim(),
      schedule: f.schedule.trim(),
      body: f.body,
      cwd: f.cwd.trim(),
      model: f.model.trim(),
      mode: f.modeField,
      agent: f.agent.trim(),
      permissionMode: f.permissionMode,
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
      } else {
        const existing = bareJobId(f.jobId);
        if (jid !== existing) {
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
    },
    [],
  );

  /**
   * Reads the approval block of a project job again - its file, digest and
   * trust - without touching the form, which may be mid-edit.
   */
  const refreshProjectView = useCallback(async (ref: string) => {
    const res = await schedulerGetJob(ref);
    if (!res.ok || res.data.scope !== "project") {
      return;
    }
    const j = res.data;
    setProjectView({
      workspace: (j.workspace || "").trim(),
      trust: j.trust || "trusted",
      reason: (j.trust_reason || "").trim(),
      digest: (j.digest || "").trim(),
      raw: j.raw || "",
    });
  }, []);

  const runPatch = useCallback(async () => {
    const f = formRef.current;
    if (f.mode !== "edit" || f.loading || f.loadErr) {
      return;
    }
    const existingRef = (f.jobId || "").trim();
    const existing = bareJobId(existingRef);
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
        agent: f.agent.trim(),
        permission_mode: f.permissionMode,
      };
      if (nextId && nextId !== existing) {
        patch.job_id = nextId;
      }
      const res = await schedulerPatchJob(existingRef, patch);
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
        agent: f.agent.trim(),
        permissionMode: f.permissionMode,
        paused: f.paused,
      });
      // The approval block shows the file and its digest: after an edit of a
      // project job both moved, so it is read again from the server.
      if (parseSchedulerJobRef(existingRef).scope === "project") {
        void refreshProjectView(
          outId !== existing ? refWithId(existingRef, outId) : existingRef,
        );
      }
      if (outId !== existing) {
        const nextRef = refWithId(existingRef, outId);
        const hp = parseAppHash();
        setSchedulerJobHash(nextRef, {
          historySidebar: hp.branch === "scheduler" && hp.historyOpen,
        });
        onSavedRef.current(nextRef);
      } else {
        onSavedRef.current();
      }
    } finally {
      setSaving(false);
    }
  }, [collectFieldErrors, snapshotFromForm, refreshProjectView]);

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
      ...(scopeRef.current === "project" ? { scope: "project" as const } : {}),
      job_id: jid,
      description: f.description.trim(),
      schedule: f.schedule.trim(),
      body: f.body,
      paused: f.paused,
      ...(f.cwd.trim() ? { cwd: f.cwd.trim() } : {}),
      ...(f.model.trim() ? { model: f.model.trim() } : {}),
      ...(f.modeField ? { mode: f.modeField } : {}),
      ...(f.agent.trim() ? { agent: f.agent.trim() } : {}),
      ...(f.permissionMode ? { permission_mode: f.permissionMode } : {}),
    };
    setSaving(true);
    setSaveErr(null);
    try {
      const res = await schedulerCreateJob(
        payload,
        payload.scope === "project" ? sessionHeadersRef.current : undefined,
      );
      if (!res.ok) {
        setSaveErr(res.message);
        return;
      }
      createdOnceRef.current = true;
      const created = res.data as { job_id?: string; scope?: string; workspace?: string };
      const ref =
        created && created.scope === "project"
          ? schedulerJobRef({ job_id: jid, scope: "project", workspace: created.workspace || "" })
          : jid;
      const hp = parseAppHash();
      setSchedulerJobHash(ref, {
        historySidebar: hp.branch === "scheduler" && hp.historyOpen,
      });
      onSavedRef.current(ref);
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
    setAgent("");
    setPermissionMode("");
    setBody("");
    setPaused(false);
    setScopeField("user");
    setProjectView(null);
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
      setProjectView(
        j.scope === "project"
          ? {
              workspace: (j.workspace || "").trim(),
              trust: j.trust || "trusted",
              reason: (j.trust_reason || "").trim(),
              digest: (j.digest || "").trim(),
              raw: j.raw || "",
            }
          : null,
      );
      setJobIdField(j.job_id);
      setDescription(j.description || "");
      setSchedule(j.schedule || "");
      setCwd(j.cwd || "");
      setModel(j.model || "");
      setModeField(normalizeJobMode(j.mode));
      setAgent((j.agent || "").trim());
      setPermissionMode(normalizePermissionMode(j.permission_mode));
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
        agent: (j.agent || "").trim(),
        permissionMode: normalizePermissionMode(j.permission_mode),
        paused: !!j.paused,
      });
    })();
    return () => {
      cancelled = true;
    };
  }, [props.open, props.mode, props.jobId, reloadSeq]);

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
    jobIdField,
    description,
    schedule,
    body,
    cwd,
    model,
    modeField,
    agent,
    permissionMode,
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
    agent,
    permissionMode,
    paused,
    scopeField,
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

  /**
   * Approves the project job for its workspace, bound to the digest this view
   * showed, or withdraws the approval; then reloads what the server says.
   */
  async function onToggleTrust() {
    const ref = (props.jobId || "").trim();
    if (!ref || !projectView) {
      return;
    }
    setSaveErr(null);
    setTrustBusy(true);
    try {
      const res =
        projectView.trust === "trusted"
          ? await schedulerUntrustJob(ref)
          : await schedulerTrustJob(ref, projectView.digest);
      if (!res.ok) {
        setSaveErr(res.message);
        // A digest that no longer matches means the file changed under the
        // view: show the current one, so the next approval is of what is there.
        if (res.status === 409) {
          void refreshProjectView(ref);
        }
        return;
      }
      setReloadSeq((n) => n + 1);
      onSavedRef.current();
    } finally {
      setTrustBusy(false);
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
      title: t("confirm.scheduler.deleteJob.title", { id: bareJobId(jid) }),
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
                jobId: jobIdField || bareJobId(props.jobId),
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

          {props.mode === "edit" && !loading && !loadErr && projectView ? (
            <ProjectTrustBlock
              view={projectView}
              policy={props.projectTrust || "ask"}
              busy={trustBusy}
              onToggle={() => void onToggleTrust()}
            />
          ) : null}

          {!loadErr && (props.mode === "create" || !loading) ? (
            <div className="scheduler-editor-form">
              {props.mode === "create" ? (
                <fieldset className="scheduler-field scheduler-scope-field" data-testid="scheduler-scope">
                  <legend className="scheduler-field-label">{t("scheduler.field.scope")}</legend>
                  <span className="scheduler-field-help">
                    {(props.workspacePath || "").trim()
                      ? t("scheduler.field.scopeHelp")
                      : t("scheduler.field.scopeNoSession")}
                  </span>
                  <div className="scheduler-scope-options">
                    <label className="scheduler-scope-option">
                      <input
                        type="radio"
                        name="scheduler-scope"
                        value="user"
                        checked={scopeField === "user"}
                        onChange={() => {
                          setScopeField("user");
                          setCwd(props.currentCwd || "");
                        }}
                        data-testid="scheduler-scope-user"
                      />
                      <span>{t("scheduler.scope.user")}</span>
                    </label>
                    <label className="scheduler-scope-option">
                      <input
                        type="radio"
                        name="scheduler-scope"
                        value="project"
                        checked={scopeField === "project"}
                        disabled={!(props.workspacePath || "").trim()}
                        onChange={() => {
                          // A project job works in its workspace; an
                          // absolute cwd would be refused, so it starts empty.
                          setScopeField("project");
                          setCwd("");
                        }}
                        data-testid="scheduler-scope-project"
                      />
                      <span>
                        {t("scheduler.scope.project", {
                          name: workspaceName((props.workspacePath || "").trim()) || "-",
                        })}
                      </span>
                    </label>
                  </div>
                </fieldset>
              ) : null}
              <label className="scheduler-field">
                <span className="scheduler-field-label">{t("scheduler.field.jobId")}</span>
                <span className="scheduler-field-help">
                  {t("scheduler.field.jobIdHelp")}
                </span>
                <input
                  className={[
                    "scheduler-field-input",
                    fieldErrs.jobId ? "scheduler-field-input-err" : "",
                  ]
                    .filter(Boolean)
                    .join(" ")}
                  value={jobIdField}
                  onChange={(ev) => setJobIdField(ev.target.value)}
                  autoComplete="off"
                  spellCheck={false}
                />
                {fieldErrs.jobId ? (
                  <div className="scheduler-field-err">{fieldErrs.jobId}</div>
                ) : null}
              </label>
              <label className="scheduler-field">
                <span className="scheduler-field-label">{t("scheduler.field.description")}</span>
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
                  {t("scheduler.field.schedule")}
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
                  placeholder={t("scheduler.field.schedulePlaceholder")}
                />
                {fieldErrs.schedule ? (
                  <div className="scheduler-field-err">
                    {fieldErrs.schedule}
                  </div>
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
                <span className="scheduler-field-label">{t("scheduler.field.cwd")}</span>
                <span className="scheduler-field-help">
                  {t("scheduler.field.cwdHelp")}
                </span>
                <input
                  className="scheduler-field-input"
                  value={cwd}
                  onChange={(ev) => setCwd(ev.target.value)}
                  placeholder={props.currentCwd || ""}
                />
              </label>
              <label className="scheduler-field">
                <span className="scheduler-field-label">{t("scheduler.field.mode")}</span>
                <select
                  className="scheduler-field-input"
                  value={modeField}
                  onChange={(ev) => setModeField(ev.target.value)}
                >
                  <option value="agent">{t("scheduler.mode.agent")}</option>
                  <option value="plan">{t("scheduler.mode.plan")}</option>
                  <option value="ask">{t("scheduler.mode.ask")}</option>
                </select>
              </label>
              <label className="scheduler-field">
                <span className="scheduler-field-label">{t("scheduler.field.model")}</span>
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
              <label className="scheduler-field">
                <span className="scheduler-field-label">{t("scheduler.field.agent")}</span>
                <span className="scheduler-field-help">
                  {t("scheduler.field.agentHelp")}
                </span>
                <input
                  className="scheduler-field-input"
                  value={agent}
                  onChange={(ev) => setAgent(ev.target.value)}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder={t("scheduler.field.agentPlaceholder")}
                  data-testid="scheduler-field-agent"
                />
              </label>
              <label className="scheduler-field">
                <span className="scheduler-field-label">
                  {t("scheduler.field.permissionMode")}
                </span>
                <span className="scheduler-field-help">
                  {t("scheduler.field.permissionModeHelp")}
                </span>
                <select
                  className="scheduler-field-input"
                  value={permissionMode}
                  onChange={(ev) => setPermissionMode(ev.target.value)}
                  data-testid="scheduler-field-permission-mode"
                >
                  <option value="">{t("scheduler.permission.default")}</option>
                  <option value="accept_edits">
                    {t("scheduler.permission.acceptEdits")}
                  </option>
                  <option value="ask">{t("scheduler.permission.ask")}</option>
                  <option value="bypass">{t("scheduler.permission.bypass")}</option>
                </select>
              </label>
              <div className="scheduler-field scheduler-field-stack">
                <span className="scheduler-field-label">{t("scheduler.field.body")}</span>
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
                    aria-label={t("scheduler.bodyAriaLabel")}
                    placeholder={t("scheduler.bodyPlaceholder")}
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
      </div>

      <div className="scheduler-editor-footer">
        {props.mode === "edit" && props.onOpenRuns ? (
          <button
            type="button"
            className="scheduler-btn scheduler-btn-icon-only"
            disabled={loading}
            data-testid="scheduler-editor-runs"
            title={t("scheduler.runs")}
            aria-label={t("scheduler.openRuns", { jobId: (props.jobId || "").trim() })}
            onClick={() => {
              const jid = (props.jobId || "").trim();
              if (jid) {
                props.onOpenRuns?.(jid);
              }
            }}
          >
            <SchedulerIconRuns />
          </button>
        ) : null}
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

/**
 * The approval block of a project job: where it comes from, whether it runs
 * and why not, the raw file the approval would be bound to, and the shield
 * that approves exactly those bytes (or withdraws the approval).
 */
function ProjectTrustBlock(props: {
  view: ProjectTrustView;
  policy: "ask" | "allow" | "deny";
  busy: boolean;
  onToggle: () => void;
}) {
  const { t } = useT();
  const v = props.view;
  const trusted = v.trust === "trusted";
  const canToggle =
    props.policy === "ask" && (v.trust === "needs_approval" || trusted);
  const stateKey =
    v.trust === "needs_approval" ? "needsApproval" : v.trust;
  return (
    <div
      className={`scheduler-trust-block scheduler-trust-block--${v.trust}`}
      data-testid="scheduler-trust-block"
      data-trust={v.trust}
    >
      <div className="scheduler-trust-head">
        <span className="scheduler-trust-state">
          {t(`scheduler.trust.${stateKey}`)}
        </span>
        {canToggle ? (
          <button
            type="button"
            className={`settings-btn settings-btn-icon scheduler-trust-toggle${trusted ? " is-trusted" : " settings-btn-approve"}`}
            title={trusted ? t("scheduler.trust.withdrawTitle") : t("scheduler.trust.approveTitle")}
            aria-label={trusted ? t("scheduler.trust.withdrawTitle") : t("scheduler.trust.approveTitle")}
            aria-pressed={trusted}
            disabled={props.busy}
            onClick={props.onToggle}
            data-testid="scheduler-trust-toggle"
          >
            <IconShield />
          </button>
        ) : null}
      </div>
      <p className="scheduler-trust-note">
        {t("scheduler.trust.from", { workspace: v.workspace })}
      </p>
      {!trusted && v.reason ? (
        <p className="scheduler-trust-reason">{v.reason}</p>
      ) : null}
      {!trusted ? (
        <>
          <p className="scheduler-trust-note">{t("scheduler.trust.reviewNote")}</p>
          <pre className="scheduler-trust-raw" data-testid="scheduler-trust-raw">{v.raw}</pre>
        </>
      ) : null}
    </div>
  );
}
