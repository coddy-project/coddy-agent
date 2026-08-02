import { useEffect, useRef, useState } from "react";
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
  const t = props.task;
  const progress = estimateProgress(t, props.nowMs);
  const overdue = isOverdue(t, props.nowMs);

  return (
    <div
      className={["bgtask-card", overdue ? "is-overdue" : ""].filter(Boolean).join(" ")}
      data-testid={`bgtask-card-${t.id}`}
    >
      <div className="bgtask-card-head">
        <button
          type="button"
          className="bgtask-card-open"
          onClick={() => props.onOpen(t.id)}
        >
          <span className={`bgtask-dot bgtask-dot--${taskTone(t.status)}`} aria-hidden="true" />
          <span className="bgtask-card-label" title={t.command || t.label}>
            {t.label}
          </span>
        </button>
        <button
          type="button"
          className="composer-icon composer-run-icon composer-send-stop composer-run-icon--stop bgtask-stop-icon"
          aria-label={`Stop ${t.label}`}
          title="Stop task"
          data-testid={`bgtask-stop-${t.id}`}
          onClick={() => props.onStop(t.id)}
        >
          <IconStop />
        </button>
      </div>
      <div className="bgtask-card-meta">{taskTimingLine(t, props.nowMs)}</div>
      {progress !== null ? (
        <div
          className="bgtask-progress"
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(progress * 100)}
          aria-label={`Progress toward the estimate for ${t.label}`}
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
  const t = props.task;
  const ended = t.finished_at ? new Date(t.finished_at) : null;
  const clock =
    ended && !Number.isNaN(ended.getTime())
      ? ended.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
      : "";

  return (
    <button
      type="button"
      className="bgtask-finished-row"
      data-testid={`bgtask-finished-${t.id}`}
      onClick={() => props.onOpen(t.id)}
      title={t.command || t.label}
    >
      <span className={`bgtask-dot bgtask-dot--${taskTone(t.status)}`} aria-hidden="true" />
      <span className="bgtask-finished-label">{t.label}</span>
      <span className="bgtask-finished-meta">
        {typeof t.exit_code === "number" && t.status !== "succeeded"
          ? `${taskStatusLabel(t.status).toLowerCase()} · ${clock}`
          : `${taskTimingLine(t, props.nowMs).split(" · ")[0]} · ${clock}`}
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
  const t = props.task;
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
          ← Back to tasks
        </button>
        {t.running ? (
          <button
            type="button"
            className="scheduler-btn bgtask-detail-stop"
            data-testid="bgtask-detail-stop"
            onClick={() => props.onStop(t.id)}
          >
            Stop
          </button>
        ) : null}
      </div>

      <div className="bgtask-detail-summary">
        <div className="bgtask-detail-title-line">
          <span className={`bgtask-dot bgtask-dot--${taskTone(t.status)}`} aria-hidden="true" />
          <span className="bgtask-detail-status">{taskStatusLabel(t.status)}</span>
          <span className="bgtask-detail-timing">{taskTimingLine(t, props.nowMs)}</span>
        </div>
        {t.command ? <pre className="bgtask-detail-command">{t.command}</pre> : null}
        {t.error ? <div className="bgtask-detail-error">{t.error}</div> : null}
      </div>

      <div className="bgtask-detail-output-head">
        <span>Output</span>
        {t.output_truncated ? (
          <span
            className="bgtask-detail-truncated"
            title="Earlier output scrolled out of the in-memory window; the full log stays in the session bundle"
          >
            truncated
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
        {props.output.trim() ? props.output : "(no output yet)"}
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
      aria-label="Background tasks"
      data-testid="bgtasks-panel"
    >
      <div className="sessions-head bgtasks-panel-head">
        <span>Background tasks</span>
        <button
          type="button"
          className="sessions-close"
          aria-label="Close background tasks"
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
              Loading…
            </div>
          ) : null}

          {!props.listError && !props.loading && props.tasks.length === 0 ? (
            <div className="sessions-empty" data-testid="bgtasks-list-empty">
              No background tasks in this chat yet. The agent starts one when a
              command is slow enough to be worth running detached.
            </div>
          ) : null}

          {running.length > 0 ? (
            <>
              <div className="bgtask-section-label" data-testid="bgtask-section-running">
                Running
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
                  Finished {finished.length}
                </button>
                <button
                  type="button"
                  className="bgtask-section-action"
                  data-testid="bgtask-clear-finished"
                  onClick={props.onClearFinished}
                >
                  Clear
                </button>
              </div>

              {finishedOpen ? (
                <div className="bgtask-finished-list" data-testid="bgtask-finished-list">
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
                      {finished.length - shown.length} older tasks are kept on disk
                      and not listed here
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
