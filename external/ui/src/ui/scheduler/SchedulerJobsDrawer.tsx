import type { SchedulerInfo, SchedulerJob, SchedulerRunEntry } from "./types";
import { schedulerJobRef } from "./types";
import { IconShield } from "../settings/icons";
import { SchedulerIconPlus, SchedulerIconRuns } from "./schedulerToolbarIcons";
import { appNavHrefSchedulerJob, appNavHrefSchedulerJobRuns } from "./hashRoute";
import { sameTabInAppNavClick } from "../nav/sameTabInAppNav";
import { useT } from "../i18n/I18nProvider";
import { taskTone } from "../tasks/taskStatus";
import type { BackgroundTaskStatus } from "../tasks/types";

/** Local clock of a run's start or end for the row's last-run mark. */
export function formatRunClock(iso: string | undefined): string {
  if (!iso || !iso.trim()) {
    return "";
  }
  const d = new Date(iso.trim());
  if (Number.isNaN(d.getTime())) {
    return "";
  }
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

/**
 * The row's last-run mark: the status dot the Tasks panel uses, with the
 * status and the clock of the run beside it, so a glance at the list says how
 * every job went last time without opening its runs.
 */
function LastRunMark(props: { jobId: string; run: SchedulerRunEntry }) {
  const { t } = useT();
  const run = props.run;
  const status = (run.status || "") as BackgroundTaskStatus;
  const clock = formatRunClock(run.running ? run.started_at : run.ended_at || run.started_at);
  const label = run.running
    ? t("scheduler.lastRun.running")
    : t(`tasks.status.${statusKey(status)}`);
  return (
    <span
      className="scheduler-job-row-last-run"
      data-testid={`scheduler-last-run-${props.jobId}`}
      title={`${label}${clock ? ` · ${clock}` : ""}`}
    >
      <span
        className={`bgtask-dot bgtask-dot--${taskTone(status)}`}
        aria-hidden="true"
      />
      <span className="scheduler-job-row-last-run-text">
        {label}
        {clock ? ` · ${clock}` : ""}
      </span>
    </span>
  );
}

/** Dictionary key of a trust state. */
function trustKey(trust: string): string {
  switch (trust) {
    case "needs_approval":
      return "needsApproval";
    case "denied":
    case "conflict":
    case "invalid":
      return trust;
    default:
      return "trusted";
  }
}

/** Dictionary key of a task status (`timed_out` is spelled `timedOut`). */
function statusKey(status: string): string {
  switch (status) {
    case "timed_out":
      return "timedOut";
    case "queued":
    case "running":
    case "succeeded":
    case "failed":
    case "stopped":
    case "orphaned":
      return status;
    default:
      return "orphaned";
  }
}

/** Renders next fire as YYYY-MM-DD HH:MM (UTC) for list rows (scheduler uses UTC five-field cron). */
export function formatNextRunUtc(iso: string | undefined): string {
  if (!iso || !iso.trim()) {
    return "—";
  }
  try {
    const d = new Date(iso.trim());
    if (Number.isNaN(d.getTime())) {
      return iso;
    }
    // Defensive: stale binaries once surfaced epoch-based "next" from CronEpoch anchoring.
    if (d.getUTCFullYear() < 1980) {
      return "—";
    }
    const y = d.getUTCFullYear();
    const mo = String(d.getUTCMonth() + 1).padStart(2, "0");
    const day = String(d.getUTCDate()).padStart(2, "0");
    const h = String(d.getUTCHours()).padStart(2, "0");
    const min = String(d.getUTCMinutes()).padStart(2, "0");
    return `${y}-${mo}-${day} ${h}:${min} (UTC)`;
  } catch {
    return iso;
  }
}

/** Last path segment of a workspace, the name its heading shows. */
export function workspaceName(path: string): string {
  const parts = (path || "").split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

/**
 * The drawer's sections: the user jobs, then one per workspace with project
 * jobs - the chat's own workspace first. Only the project sections get a
 * heading when there are no project jobs at all, the list looks as it always
 * did.
 */
export type SchedulerJobGroup = {
  key: string;
  workspace: string;
  jobs: SchedulerJob[];
};

export function groupSchedulerJobs(
  jobs: SchedulerJob[],
  currentWorkspace: string,
): SchedulerJobGroup[] {
  const user: SchedulerJob[] = [];
  const byWorkspace = new Map<string, SchedulerJob[]>();
  for (const j of jobs) {
    if (j.scope === "project" && (j.workspace || "").trim()) {
      const ws = (j.workspace || "").trim();
      const list = byWorkspace.get(ws) || [];
      list.push(j);
      byWorkspace.set(ws, list);
    } else {
      user.push(j);
    }
  }
  const groups: SchedulerJobGroup[] = [{ key: "user", workspace: "", jobs: user }];
  const order = [...byWorkspace.keys()].sort((a, b) => {
    if (a === currentWorkspace) return -1;
    if (b === currentWorkspace) return 1;
    return a.localeCompare(b);
  });
  for (const ws of order) {
    groups.push({ key: `project:${ws}`, workspace: ws, jobs: byWorkspace.get(ws) || [] });
  }
  return groups;
}

export function SchedulerJobsDrawer(props: {
  open: boolean;
  /** Job id shown in the editor; same row highlight as History `session-item.active`. */
  selectedJobId: string | null;
  /** Extra class for dock layout (e.g. scheduler-dock-drawer). */
  className?: string;
  onClose: () => void;
  scheduler: SchedulerInfo | null;
  jobs: SchedulerJob[];
  listError: string | null;
  loading: boolean;
  onAddJob: () => void;
  onOpenJob: (jobId: string) => void;
  /** Opens the job's runs panel (its run history). */
  onOpenRuns: (jobId: string) => void;
  onRunJob: (jobId: string) => void;
  onCancelJob: (jobId: string) => void;
  searchDraft: string;
  onSearchDraftChange: (v: string) => void;
  onSearchClear: () => void;
}) {
  const { t } = useT();
  if (!props.open) {
    return null;
  }
  const currentWorkspace = (props.scheduler?.workspace || "").trim();
  const groups = groupSchedulerJobs(props.jobs, currentWorkspace);
  const withHeadings = groups.length > 1;

  return (
    <aside
      className={["sessions", "scheduler-jobs", "drawer", props.className || ""]
        .filter(Boolean)
        .join(" ")}
      aria-label={t("nav.schedulerAriaLabel")}
      data-testid="scheduler-drawer"
      data-variant="drawer"
    >
      <div className="sessions-head">
        <span>{t("scheduler.title")}</span>
        <button
          type="button"
          className="sessions-close"
          aria-label={t("scheduler.close")}
          data-testid="scheduler-drawer-close"
          onClick={props.onClose}
        >
          ×
        </button>
      </div>

      <div className="sessions-search-row scheduler-search-row">
        <input
          type="search"
          className="sessions-search-input"
          placeholder={t("scheduler.searchPlaceholder")}
          value={props.searchDraft}
          onChange={(ev) => props.onSearchDraftChange(ev.target.value)}
          aria-label={t("scheduler.searchAriaLabel")}
          data-testid="scheduler-search"
        />
        {props.searchDraft.trim() ? (
          <button
            type="button"
            className="sessions-search-clear"
            aria-label={t("scheduler.clearSearch")}
            data-testid="scheduler-search-clear"
            onClick={props.onSearchClear}
          >
            ×
          </button>
        ) : null}
      </div>

      <div className="session-list scheduler-job-list">
        {props.listError ? (
          <div className="sessions-empty" data-testid="scheduler-list-error">
            {props.listError}
          </div>
        ) : null}
        {!props.listError && !props.loading && props.jobs.length === 0 ? (
          <div className="sessions-empty" data-testid="scheduler-list-empty">
            {t("scheduler.empty")}
          </div>
        ) : null}
        {props.loading && props.jobs.length === 0 && !props.listError ? (
          <div className="sessions-empty" data-testid="scheduler-list-loading">
            {t("scheduler.loading")}
          </div>
        ) : null}
        {groups.map((g) =>
          g.jobs.length === 0 && !withHeadings ? null : (
          <div
            key={g.key}
            className="scheduler-job-group"
            data-testid={g.workspace ? `scheduler-group-project` : "scheduler-group-user"}
          >
            {withHeadings ? (
              <div
                className="scheduler-job-group-head"
                title={g.workspace || props.scheduler?.dir || undefined}
              >
                {g.workspace
                  ? g.workspace === currentWorkspace
                    ? t("scheduler.group.thisProject", { name: workspaceName(g.workspace) })
                    : t("scheduler.group.project", { name: workspaceName(g.workspace) })
                  : t("scheduler.group.user")}
              </div>
            ) : null}
            {g.workspace === "" && g.jobs.length === 0 ? (
              <div className="scheduler-job-group-empty">{t("scheduler.group.userEmpty")}</div>
            ) : null}
        {g.jobs.map((j) => {
          const ref = schedulerJobRef(j);
          const selected = props.selectedJobId === ref;
          const trust = j.trust || "trusted";
          const trusted = trust === "trusted";
          return (
          <div
            key={ref}
            className={[
              "session-item",
              "scheduler-job-row",
              selected ? "active" : "",
              trusted ? "" : "scheduler-job-row-untrusted",
            ]
              .filter(Boolean)
              .join(" ")}
            data-testid={`scheduler-job-row-${j.job_id}`}
            data-scope={j.scope || "user"}
            data-trust={trust}
          >
            <a
              href={appNavHrefSchedulerJob(ref)}
              className="scheduler-job-row-main"
              aria-current={selected ? "true" : undefined}
              onClick={(ev) =>
                sameTabInAppNavClick(ev, () => props.onOpenJob(ref))
              }
            >
              <div className="scheduler-job-row-text-block">
                <div className="scheduler-job-row-title-line">
                  <div className="scheduler-job-row-id" title={j.job_id}>
                    {j.job_id}
                  </div>
                  {!trusted ? (
                    <span
                      className={`scheduler-job-trust scheduler-job-trust--${trust}`}
                      title={j.trust_reason || undefined}
                      data-testid={`scheduler-trust-${j.job_id}`}
                    >
                      {t(`scheduler.trust.${trustKey(trust)}`)}
                    </span>
                  ) : j.paused ? (
                    <span className="scheduler-job-paused">{t("scheduler.paused")}</span>
                  ) : (
                    <span
                      className="scheduler-job-row-next"
                      title={
                        j.next_run_utc && j.next_run_utc.trim()
                          ? j.next_run_utc.trim()
                          : undefined
                      }
                    >
                      {formatNextRunUtc(j.next_run_utc)}
                    </span>
                  )}
                </div>
                <div
                  className="scheduler-job-row-desc"
                  title={
                    (j.description || "").trim()
                      ? (j.description || "").trim()
                      : undefined
                  }
                >
                  {(j.description || "").trim() || t("scheduler.noDescription")}
                </div>
                {j.last_run ? (
                  <LastRunMark jobId={j.job_id} run={j.last_run} />
                ) : null}
              </div>
            </a>
            <div className="scheduler-job-row-actions">
              <a
                href={appNavHrefSchedulerJobRuns(ref)}
                className="scheduler-btn scheduler-btn-icon-only scheduler-job-runs-icon"
                aria-label={t("scheduler.openRuns", { jobId: j.job_id })}
                title={t("scheduler.runs")}
                data-testid={`scheduler-runs-${j.job_id}`}
                onClick={(ev) => {
                  ev.stopPropagation();
                  sameTabInAppNavClick(ev, () => props.onOpenRuns(ref));
                }}
              >
                <SchedulerIconRuns />
              </a>
              {trust === "needs_approval" ? (
                <a
                  href={appNavHrefSchedulerJob(ref)}
                  className="scheduler-btn scheduler-btn-icon-only scheduler-job-approve-icon"
                  aria-label={t("scheduler.trust.reviewAria", { jobId: j.job_id })}
                  title={t("scheduler.trust.review")}
                  data-testid={`scheduler-approve-${j.job_id}`}
                  onClick={(ev) => {
                    ev.stopPropagation();
                    sameTabInAppNavClick(ev, () => props.onOpenJob(ref));
                  }}
                >
                  <IconShield />
                </a>
              ) : null}
              {trust === "needs_approval" && !j.running ? null : j.running ? (
                <button
                  type="button"
                  className="composer-icon composer-run-icon composer-send-stop scheduler-job-run-icon composer-run-icon--stop"
                  aria-label={t("scheduler.stopJob")}
                  data-testid={`scheduler-stop-${j.job_id}`}
                  onClick={(ev) => {
                    ev.stopPropagation();
                    props.onCancelJob(ref);
                  }}
                >
                  <span className="composer-send-glyph" aria-hidden="true">
                    <span className="composer-stop-square" />
                  </span>
                </button>
              ) : (
                <button
                  type="button"
                  className="composer-icon composer-run-icon composer-send-play scheduler-job-run-icon composer-run-icon--play"
                  aria-label={t("scheduler.runJobNow")}
                  disabled={j.paused || !trusted}
                  data-testid={`scheduler-run-${j.job_id}`}
                  onClick={(ev) => {
                    ev.stopPropagation();
                    props.onRunJob(ref);
                  }}
                >
                  <span className="composer-send-glyph" aria-hidden="true">
                    <svg viewBox="0 0 12 12" fill="currentColor" width="14" height="14">
                      <path d="M2 0L12 6L2 12Z" />
                    </svg>
                  </span>
                </button>
              )}
            </div>
          </div>
          );
        })}
          </div>
          ),
        )}
      </div>

      <div className="scheduler-drawer-footer">
        <button
          type="button"
          className="scheduler-btn scheduler-btn-primary scheduler-btn-icon-only"
          data-testid="scheduler-add-job"
          title={t("scheduler.addJob")}
          aria-label={t("scheduler.addJob")}
          onClick={props.onAddJob}
        >
          <SchedulerIconPlus />
        </button>
      </div>
    </aside>
  );
}
