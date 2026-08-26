import { useEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import type { BackgroundTask } from "./types";
import {
  estimateProgress,
  groupTasks,
  isOverdue,
  taskStatusLabel,
  taskTimingLine,
  taskTone,
} from "./taskStatus";

/** How many finished rows render before the rest stay behind the scroll. */
const FINISHED_RENDER_CAP = 40;

function IconStop() {
  return (
    <span className="composer-send-glyph" aria-hidden="true">
      <span className="composer-stop-square" />
    </span>
  );
}

/** Live task: the card carries timing and progress toward the model's estimate. */
function RunningCard(props: {
  task: BackgroundTask;
  nowMs: number;
  onOpen: (taskId: string) => void;
  onStop: (taskId: string) => void;
}) {
  const { t } = useT();
  const task = props.task;
  const progress = estimateProgress(task, props.nowMs);
  const overdue = isOverdue(task, props.nowMs);

  return (
    <div
      className={["bgtask-card", overdue ? "is-overdue" : ""]
        .filter(Boolean)
        .join(" ")}
      data-testid={`bgtask-card-${task.id}`}
    >
      <div className="bgtask-card-head">
        <button
          type="button"
          className="bgtask-card-open"
          onClick={() => props.onOpen(task.id)}
        >
          <span
            className={`bgtask-dot bgtask-dot--${taskTone(task.status)}`}
            aria-hidden="true"
          />
          <span
            className="bgtask-card-label"
            title={task.command || task.label}
          >
            {task.label}
          </span>
        </button>
        <button
          type="button"
          className="composer-icon composer-run-icon composer-send-stop composer-run-icon--stop bgtask-stop-icon"
          aria-label={t("tasks.stopAriaLabel", { label: task.label })}
          title={t("tasks.stopTitle")}
          data-testid={`bgtask-stop-${task.id}`}
          onClick={() => props.onStop(task.id)}
        >
          <IconStop />
        </button>
      </div>
      <div className="bgtask-card-meta">
        {taskTimingLine(task, props.nowMs)}
      </div>
      {progress !== null ? (
        <div
          className="bgtask-progress"
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(progress * 100)}
          aria-label={t("tasks.progressAriaLabel", { label: task.label })}
        >
          <span
            className="bgtask-progress-fill"
            style={{ width: `${Math.round(progress * 100)}%` }}
          />
        </div>
      ) : null}
    </div>
  );
}

/** Finished task: one line, because history is scanned rather than read. */
function FinishedRow(props: {
  task: BackgroundTask;
  nowMs: number;
  onOpen: (taskId: string) => void;
}) {
  const task = props.task;
  const ended = task.finished_at ? new Date(task.finished_at) : null;
  const clock =
    ended && !Number.isNaN(ended.getTime())
      ? ended.toLocaleTimeString(undefined, {
          hour: "2-digit",
          minute: "2-digit",
        })
      : "";

  return (
    <button
      type="button"
      className="bgtask-finished-row"
      data-testid={`bgtask-finished-${task.id}`}
      onClick={() => props.onOpen(task.id)}
      title={task.command || task.label}
    >
      <span
        className={`bgtask-dot bgtask-dot--${taskTone(task.status)}`}
        aria-hidden="true"
      />
      <span className="bgtask-finished-label">{task.label}</span>
      <span className="bgtask-finished-meta">
        {typeof task.exit_code === "number" && task.status !== "succeeded"
          ? `${taskStatusLabel(task.status).toLowerCase()} · ${clock}`
          : `${taskTimingLine(task, props.nowMs).split(" · ")[0]} · ${clock}`}
      </span>
    </button>
  );
}

function TaskDetail(props: {
  task: BackgroundTask;
  output: string;
  nowMs: number;
  onBack: () => void;
  onStop: (taskId: string) => void;
}) {
  const { t } = useT();
  const task = props.task;
  const preRef = useRef<HTMLPreElement | null>(null);
  const [follow, setFollow] = useState(true);

  useEffect(() => {
    const el = preRef.current;
    if (!el || !follow) {
      return;
    }
    el.scrollTop = el.scrollHeight;
  }, [props.output, follow]);

  return (
    <div className="bgtask-detail" data-testid="bgtask-detail">
      <div className="bgtask-detail-head">
        <button
          type="button"
          className="bgtask-back"
          data-testid="bgtask-back"
          onClick={props.onBack}
        >
          {t("tasks.backToList")}
        </button>
        {task.running ? (
          <button
            type="button"
            className="scheduler-btn bgtask-detail-stop"
            data-testid="bgtask-detail-stop"
            onClick={() => props.onStop(task.id)}
          >
            {t("tasks.stopTitle")}
          </button>
        ) : null}
      </div>

      <div className="bgtask-detail-summary">
        <div className="bgtask-detail-title-line">
          <span
            className={`bgtask-dot bgtask-dot--${taskTone(task.status)}`}
            aria-hidden="true"
          />
          <span className="bgtask-detail-status">
            {taskStatusLabel(task.status)}
          </span>
          <span className="bgtask-detail-timing">
            {taskTimingLine(task, props.nowMs)}
          </span>
        </div>
        {task.command ? (
          <pre className="bgtask-detail-command">{task.command}</pre>
        ) : null}
        {task.error ? (
          <div className="bgtask-detail-error">{task.error}</div>
        ) : null}
      </div>

      <div className="bgtask-detail-output-head">
        <span>{t("tasks.outputHeading")}</span>
        {task.output_truncated ? (
          <span
            className="bgtask-detail-truncated"
            title={t("tasks.truncatedTitle")}
          >
            {t("tasks.truncated")}
          </span>
        ) : null}
      </div>
      <pre
        ref={preRef}
        className="bgtask-detail-output"
        data-testid="bgtask-output"
        onScroll={(ev) => {
          const el = ev.currentTarget;
          setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 24);
        }}
      >
        {props.output.trim() ? props.output : t("tasks.noOutput")}
      </pre>
    </div>
  );
}

/**
 * Background tasks of the session that owns this chat. The panel is docked
 * inside the session on purpose: a task belongs to the conversation that
 * started it, so there is never a question of which session a process came
 * from.
 */
export function BackgroundTasksPanel(props: {
  open: boolean;
  selectedTaskId: string | null;
  tasks: BackgroundTask[];
  selectedOutput: string;
  listError: string | null;
  loading: boolean;
  /** Milliseconds clock from the shell so every ticker advances together. */
  nowMs: number;
  onClose: () => void;
  onOpenTask: (taskId: string) => void;
  onBackToList: () => void;
  onStopTask: (taskId: string) => void;
  onClearFinished: () => void;
}) {
  const { t } = useT();
  const [finishedOpen, setFinishedOpen] = useState(false);

  if (!props.open) {
    return null;
  }

  const { running, finished } = groupTasks(props.tasks);
  const selected =
    props.selectedTaskId !== null
      ? props.tasks.find((t) => t.id === props.selectedTaskId) || null
      : null;
  const shown = finished.slice(0, FINISHED_RENDER_CAP);

  return (
    <aside
      className="bgtasks-panel"
      aria-label={t("tasks.panelTitle")}
      data-testid="bgtasks-panel"
    >
      <div className="sessions-head bgtasks-panel-head">
        <span>{t("tasks.panelTitle")}</span>
        <button
          type="button"
          className="sessions-close"
          aria-label={t("tasks.closePanel")}
          data-testid="bgtasks-panel-close"
          onClick={props.onClose}
        >
          ×
        </button>
      </div>

      {selected ? (
        <TaskDetail
          task={selected}
          output={props.selectedOutput}
          nowMs={props.nowMs}
          onBack={props.onBackToList}
          onStop={props.onStopTask}
        />
      ) : (
        <div className="bgtask-list">
          {props.listError ? (
            <div className="sessions-empty" data-testid="bgtasks-list-error">
              {props.listError}
            </div>
          ) : null}

          {!props.listError && props.loading && props.tasks.length === 0 ? (
            <div className="sessions-empty" data-testid="bgtasks-list-loading">
              {t("tasks.loading")}
            </div>
          ) : null}

          {!props.listError && !props.loading && props.tasks.length === 0 ? (
            <div className="sessions-empty" data-testid="bgtasks-list-empty">
              {t("tasks.empty")}
            </div>
          ) : null}

          {running.length > 0 ? (
            <>
              <div
                className="bgtask-section-label"
                data-testid="bgtask-section-running"
              >
                {t("tasks.sectionRunning")}
              </div>
              {running.map((t) => (
                <RunningCard
                  key={t.id}
                  task={t}
                  nowMs={props.nowMs}
                  onOpen={props.onOpenTask}
                  onStop={props.onStopTask}
                />
              ))}
            </>
          ) : null}

          {finished.length > 0 ? (
            <>
              <div className="bgtask-section-row">
                <button
                  type="button"
                  className="bgtask-section-toggle"
                  data-testid="bgtask-finished-toggle"
                  aria-expanded={finishedOpen}
                  onClick={() => setFinishedOpen((v) => !v)}
                >
                  <span
                    className={`bgtask-section-chevron ${finishedOpen ? "is-open" : ""}`}
                    aria-hidden="true"
                  />
                  {t("tasks.sectionFinished", { count: finished.length })}
                </button>
                <button
                  type="button"
                  className="bgtask-section-action"
                  data-testid="bgtask-clear-finished"
                  onClick={props.onClearFinished}
                >
                  {t("tasks.clearFinished")}
                </button>
              </div>

              {finishedOpen ? (
                <div
                  className="bgtask-finished-list"
                  data-testid="bgtask-finished-list"
                >
                  {shown.map((t) => (
                    <FinishedRow
                      key={t.id}
                      task={t}
                      nowMs={props.nowMs}
                      onOpen={props.onOpenTask}
                    />
                  ))}
                  {finished.length > shown.length ? (
                    <div
                      className="bgtask-finished-more"
                      data-testid="bgtask-finished-more"
                    >
                      {t("tasks.olderOnDisk", {
                        count: finished.length - shown.length,
                      })}
                    </div>
                  ) : null}
                </div>
              ) : null}
            </>
          ) : null}
        </div>
      )}
    </aside>
  );
}
