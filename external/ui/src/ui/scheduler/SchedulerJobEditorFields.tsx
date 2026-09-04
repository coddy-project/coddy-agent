import { useT } from "../i18n/I18nProvider";
import type { describeCronScheduleOrError } from "./cronDescribe";
import type { FieldErrors } from "./jobEditorForm";
import { MarkdownLineEditor } from "./MarkdownLineEditor";

/** The job editor form fields: id, description, cron schedule + hint, cwd, mode, model, body. */
export function SchedulerJobEditorFields(props: {
  jobIdField: string;
  onJobIdChange: (v: string) => void;
  description: string;
  onDescriptionChange: (v: string) => void;
  schedule: string;
  onScheduleChange: (v: string) => void;
  cwd: string;
  onCwdChange: (v: string) => void;
  modeField: string;
  onModeChange: (v: string) => void;
  model: string;
  onModelChange: (v: string) => void;
  body: string;
  onBodyChange: (v: string) => void;
  fieldErrs: FieldErrors;
  cronHint: ReturnType<typeof describeCronScheduleOrError>;
  saveErr: string | null;
  availableModels: string[];
  defaultModel: string;
  currentCwd: string;
}) {
  const { t } = useT();
  const { fieldErrs, cronHint, saveErr } = props;
  return (
    <div className="scheduler-editor-form">
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
          value={props.jobIdField}
          onChange={(ev) => props.onJobIdChange(ev.target.value)}
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
          value={props.description}
          onChange={(ev) => props.onDescriptionChange(ev.target.value)}
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
          value={props.schedule}
          onChange={(ev) => props.onScheduleChange(ev.target.value)}
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
          value={props.cwd}
          onChange={(ev) => props.onCwdChange(ev.target.value)}
          placeholder={props.currentCwd || ""}
        />
      </label>
      <label className="scheduler-field">
        <span className="scheduler-field-label">{t("scheduler.field.mode")}</span>
        <select
          className="scheduler-field-input"
          value={props.modeField}
          onChange={(ev) => props.onModeChange(ev.target.value)}
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
            value={props.model}
            onChange={(ev) => props.onModelChange(ev.target.value)}
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
            value={props.model}
            onChange={(ev) => props.onModelChange(ev.target.value)}
            spellCheck={false}
            placeholder={props.defaultModel || ""}
          />
        )}
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
            value={props.body}
            onChange={props.onBodyChange}
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
  );
}
