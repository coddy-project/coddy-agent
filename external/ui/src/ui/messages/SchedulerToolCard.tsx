import { memo } from "react";

import { todoStatusMark } from "../chat/PermissionPromptPreview";
import type {
  SchedulerJobView,
  SchedulerReadout,
  SchedulerRunView,
} from "../chat/schedulerToolDisplay";
import { useT } from "../i18n/I18nProvider";
import { Markdown } from "../markdown/Markdown";
import { describeCronScheduleUTC } from "../scheduler/cronDescribe";
import { formatNextRunUtc } from "../scheduler/SchedulerJobsDrawer";
import { formatStepDuration } from "./formatStepDuration";
import { formatUtcToLocalFullDetail } from "./formatMessageTime";

function hasFields(job: SchedulerJobView | undefined): job is SchedulerJobView {
  if (!job) return false;
  return Object.keys(job).some((k) => k !== "jobId");
}

function JobState(props: { job: SchedulerJobView }) {
  const { t } = useT();
  if (props.job.running) return <>{t("schedulerTool.state.running")}</>;
  if (props.job.paused === true) return <>{t("schedulerTool.state.paused")}</>;
  if (props.job.paused === false) return <>{t("schedulerTool.state.active")}</>;
  return null;
}

/** A job as labelled fields, its instruction rendered as the Markdown it is. */
function JobFields(props: { job: SchedulerJobView }) {
  const { t } = useT();
  const { job } = props;
  const human = job.schedule ? describeCronScheduleUTC(job.schedule) : null;
  const rows: Array<[string, React.ReactNode]> = [];
  if (job.description) rows.push([t("schedulerTool.field.description"), job.description]);
  if (job.schedule) {
    rows.push([
      t("schedulerTool.field.schedule"),
      <>
        <code className="scheduler-tool-cron">{job.schedule}</code>
        {human ? <span className="scheduler-tool-muted"> · {human}</span> : null}
      </>,
    ]);
  }
  if (job.paused !== undefined || job.running !== undefined) {
    rows.push([t("schedulerTool.field.state"), <JobState job={job} />]);
  }
  if (job.nextRunUtc) {
    rows.push([t("schedulerTool.field.nextRun"), formatNextRunUtc(job.nextRunUtc)]);
  }
  if (job.mode) rows.push([t("schedulerTool.field.mode"), job.mode]);
  if (job.model) rows.push([t("schedulerTool.field.model"), job.model]);
  if (job.cwd) rows.push([t("schedulerTool.field.cwd"), job.cwd]);
  return (
    <>
      {rows.length > 0 ? (
        <dl className="scheduler-tool-fields">
          {rows.map(([label, value]) => (
            <div className="scheduler-tool-field" key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      ) : null}
      {job.body ? (
        <div className="scheduler-tool-instruction">
          <Markdown text={job.body} />
        </div>
      ) : null}
    </>
  );
}

function JobRows(props: { jobs: SchedulerJobView[] }) {
  const { t } = useT();
  if (props.jobs.length === 0) {
    return <div className="scheduler-tool-muted">{t("schedulerTool.noJobs")}</div>;
  }
  return (
    <ul className="scheduler-tool-rows">
      {props.jobs.map((job, i) => {
        const human = job.schedule ? describeCronScheduleUTC(job.schedule) : null;
        return (
          <li className="scheduler-tool-row" key={job.jobId || i}>
            <span className="scheduler-tool-row-id">{job.jobId}</span>
            {job.description ? (
              <span className="scheduler-tool-row-text">{job.description}</span>
            ) : null}
            <span className="scheduler-tool-muted">
              {human || job.schedule}
              {job.running || job.paused !== undefined ? (
                <>
                  {" · "}
                  <JobState job={job} />
                </>
              ) : null}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

function RunRows(props: { runs: SchedulerRunView[] }) {
  const { t } = useT();
  if (props.runs.length === 0) {
    return <div className="scheduler-tool-muted">{t("schedulerTool.noRuns")}</div>;
  }
  return (
    <ul className="scheduler-tool-rows">
      {props.runs.map((run, i) => {
        const started = run.startedAt ? Date.parse(run.startedAt) : Number.NaN;
        const ended = run.endedAt ? Date.parse(run.endedAt) : Number.NaN;
        const took =
          Number.isFinite(started) && Number.isFinite(ended) && ended >= started
            ? formatStepDuration(ended - started)
            : "";
        return (
          <li className="scheduler-tool-row" key={run.sessionId || i}>
            <span className="scheduler-tool-row-text">
              {run.startedAt ? formatUtcToLocalFullDetail(run.startedAt) : "—"}
            </span>
            <span className="scheduler-tool-muted">
              {[run.status, took].filter(Boolean).join(" · ")}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * A scheduler call as one card in the voice of the other tool cards: the preview bar
 * with the status mark the plan rows use, naming the job and what happened to it,
 * and under it the job, the list or the runs the call read.
 */
export const SchedulerToolCard = memo(function SchedulerToolCard(props: {
  readout: SchedulerReadout;
  status: string;
}) {
  const { t, tp } = useT();
  const { readout } = props;
  const status = (props.status || "").toLowerCase();
  const settled = ["completed", "failed", "cancelled"].includes(status)
    ? status
    : "in_progress";

  let header = "";
  let meta = "";
  let body: React.ReactNode = null;
  switch (readout.kind) {
    case "outcome":
      header = readout.renamedTo
        ? `${readout.jobId} → ${readout.renamedTo}`
        : readout.jobId;
      meta = readout.outcome
        ? t(`schedulerTool.outcome.${readout.outcome}`)
        : t("toolAction.running");
      if (hasFields(readout.job)) body = <JobFields job={readout.job} />;
      break;
    case "job":
      header = readout.job.jobId || "";
      body = <JobFields job={readout.job} />;
      break;
    case "jobs":
      header = t("schedulerTool.allJobs");
      meta = tp("schedulerTool.jobs", readout.jobs.length);
      body = <JobRows jobs={readout.jobs} />;
      break;
    case "runs":
      header = readout.jobId;
      meta = tp("schedulerTool.runs", readout.runs.length);
      body = <RunRows runs={readout.runs} />;
      break;
  }

  return (
    <div className="permission-preview scheduler-tool-card" data-testid="scheduler-tool-card">
      <div
        className={
          "permission-preview-bar" +
          (body ? "" : " permission-preview-bar--standalone")
        }
      >
        <span
          className={`todo-tool-preview-mark todo-tool-preview-mark--${settled}`}
          aria-hidden="true"
        >
          {todoStatusMark(settled)}
        </span>
        <div className="permission-preview-location" title={header}>
          {header}
        </div>
        {meta ? (
          <div className="permission-preview-meta scheduler-tool-meta">
            <span>{meta}</span>
          </div>
        ) : null}
      </div>
      {body ? <div className="scheduler-tool-body">{body}</div> : null}
    </div>
  );
});
