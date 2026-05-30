import type { SchedulerInfo, SchedulerJob } from "./types";
import { SchedulerIconPlus } from "./schedulerToolbarIcons";
import { appNavHrefSchedulerJob } from "./hashRoute";
import { sameTabInAppNavClick } from "../nav/sameTabInAppNav";

/** Renders next fire as YYYY-MM-DD HH:MM (UTC) for list rows (scheduler uses UTC five-field cron). */
function formatNextRunUtc(iso: string | undefined): string {
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
  onRunJob: (jobId: string) => void;
  onCancelJob: (jobId: string) => void;
  searchDraft: string;
  onSearchDraftChange: (v: string) => void;
  onSearchClear: () => void;
}) {
  if (!props.open) {
    return null;
  }

  return (
    <aside
      className={["sessions", "scheduler-jobs", "drawer", props.className || ""]
        .filter(Boolean)
        .join(" ")}
      aria-label="Scheduler jobs"
      data-testid="scheduler-drawer"
      data-variant="drawer"
    >
      <div className="sessions-head">
        <span>Scheduler</span>
        <button
          type="button"
          className="sessions-close"
          aria-label="Close scheduler"
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
          placeholder="Search by description or job id"
          value={props.searchDraft}
          onChange={(ev) => props.onSearchDraftChange(ev.target.value)}
          aria-label="Search scheduler jobs by description or job id"
          data-testid="scheduler-search"
        />
        {props.searchDraft.trim() ? (
          <button
            type="button"
            className="sessions-search-clear"
            aria-label="Clear scheduler search"
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
            No jobs yet
          </div>
        ) : null}
        {props.loading && props.jobs.length === 0 && !props.listError ? (
          <div className="sessions-empty" data-testid="scheduler-list-loading">
            Loading…
          </div>
        ) : null}
        {props.jobs.map((j) => {
          const selected = props.selectedJobId === j.job_id;
          return (
          <div
            key={j.job_id}
            className={[
              "session-item",
              "scheduler-job-row",
              selected ? "active" : "",
            ]
              .filter(Boolean)
              .join(" ")}
            data-testid={`scheduler-job-row-${j.job_id}`}
          >
            <a
              href={appNavHrefSchedulerJob(j.job_id)}
              className="scheduler-job-row-main"
              aria-current={selected ? "true" : undefined}
              onClick={(ev) =>
                sameTabInAppNavClick(ev, () => props.onOpenJob(j.job_id))
              }
            >
              <div className="scheduler-job-row-text-block">
                <div className="scheduler-job-row-title-line">
                  <div className="scheduler-job-row-id" title={j.job_id}>
                    {j.job_id}
                  </div>
                  {j.paused ? (
                    <span className="scheduler-job-paused">paused</span>
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
                  {(j.description || "").trim() || "—"}
                </div>
              </div>
            </a>
            <div className="scheduler-job-row-actions">
              {j.running ? (
                <button
                  type="button"
                  className="composer-icon composer-run-icon composer-send-stop scheduler-job-run-icon composer-run-icon--stop"
                  aria-label="Stop job"
                  data-testid={`scheduler-stop-${j.job_id}`}
                  onClick={(ev) => {
                    ev.stopPropagation();
                    props.onCancelJob(j.job_id);
                  }}
                >
                  <span className="composer-send-glyph" aria-hidden="true">
                    ■
                  </span>
                </button>
              ) : (
                <button
                  type="button"
                  className="composer-icon composer-run-icon composer-send-play scheduler-job-run-icon composer-run-icon--play"
                  aria-label="Run job now"
                  disabled={j.paused}
                  data-testid={`scheduler-run-${j.job_id}`}
                  onClick={(ev) => {
                    ev.stopPropagation();
                    props.onRunJob(j.job_id);
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

      <div className="scheduler-drawer-footer">
        <button
          type="button"
          className="scheduler-btn scheduler-btn-primary scheduler-btn-icon-only"
          data-testid="scheduler-add-job"
          title="Add job"
          aria-label="Add job"
          onClick={props.onAddJob}
        >
          <SchedulerIconPlus />
        </button>
      </div>
    </aside>
  );
}
