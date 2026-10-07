import { useT } from "../i18n/I18nProvider";
import { sameTabInAppNavClick } from "../nav/sameTabInAppNav";
import {
  appNavHrefSchedulerJobRuns,
  appNavHrefSession,
} from "../scheduler/hashRoute";
import type { SubagentTranscriptMeta } from "./subagentTranscript";
import { schedulerJobRef } from "../scheduler/types";

/**
 * Stands in for the composer on a read-only transcript. A child session is
 * the record of one delegated run and the server refuses prompts against it
 * (409), so the SPA offers nothing to type into and points back at the parent
 * chat, which is where the operator steers the work. A run the scheduler
 * started points at the job's runs instead, and the session of a scheduler
 * job - which only holds those runs - points there too.
 */
export function SubagentReadOnlyNotice(props: {
  meta: SubagentTranscriptMeta;
  /** Same-tab open of the parent chat; modifier clicks fall through to the href. */
  onOpenSession?: (sessionId: string) => void;
}) {
  const { t } = useT();
  const name = props.meta.name.trim();
  const parent = props.meta.parentSessionId.trim();
  const model = props.meta.model?.trim();
  const onOpen = props.onOpenSession;
  const sched = props.meta.scheduler;

  if (sched) {
    const jobId = sched.jobId;
    // A project job is addressed by its workspace too, so the link opens
    // that job and not a user job of the same id.
    const jobRef = schedulerJobRef({
      job_id: jobId,
      scope: sched.workspace ? "project" : "user",
      workspace: sched.workspace || "",
    });
    const runsHref = props.meta.jobSession
      ? appNavHrefSchedulerJobRuns(jobRef)
      : appNavHrefSchedulerJobRuns(jobRef, props.meta.taskId);
    return (
      <div
        className="subagent-readonly-notice"
        role="note"
        data-testid="subagent-readonly-notice"
      >
        <span className="subagent-readonly-copy">
          <span className="subagent-readonly-text">
            {props.meta.jobSession
              ? t("chat.schedulerJobSession.notice", { jobId })
              : t("chat.scheduledRunReadOnly.notice", { jobId })}
          </span>
          {model ? (
            <span className="subagent-readonly-model">
              {t("chat.subagentReadOnly.model", { model })}
            </span>
          ) : null}
        </span>
        <a
          className="subagent-readonly-link"
          href={runsHref}
          data-testid="subagent-readonly-runs-link"
        >
          {t("chat.scheduledRunReadOnly.openRuns")}
        </a>
      </div>
    );
  }

  return (
    <div
      className="subagent-readonly-notice"
      role="note"
      data-testid="subagent-readonly-notice"
    >
      <span className="subagent-readonly-copy">
        <span className="subagent-readonly-text">
          {name
            ? t("chat.subagentReadOnly.notice", { name })
            : t("chat.subagentReadOnly.noticeUnnamed")}
        </span>
        {model ? (
          <span className="subagent-readonly-model">
            {t("chat.subagentReadOnly.model", { model })}
          </span>
        ) : null}
      </span>
      {parent ? (
        <a
          className="subagent-readonly-link"
          href={appNavHrefSession(parent)}
          data-testid="subagent-readonly-parent-link"
          onClick={(ev) => {
            if (!onOpen) {
              return;
            }
            sameTabInAppNavClick(ev, () => onOpen(parent));
          }}
        >
          {t("chat.subagentReadOnly.openParent")}
        </a>
      ) : null}
    </div>
  );
}
