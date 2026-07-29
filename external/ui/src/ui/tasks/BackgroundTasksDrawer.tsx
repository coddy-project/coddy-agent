import { useEffect, useRef, useState } from "react";
import type { BackgroundTask } from "./types";
import {
  estimateProgress,
  isOverdue,
  sortTasksForDrawer,
  taskStatusLabel,
  taskTimingLine,
  taskTone,
} from "./taskStatus";
import { appNavHrefTask } from "../scheduler/hashRoute";
import { sameTabInAppNavClick } from "../nav/sameTabInAppNav";

/** Square stop glyph, matching the composer stop control. */
function IconStop() {
  return (
    <span className="composer-send-glyph" aria-hidden="true">
      <span className="composer-stop-square" />
    </span>
  );
}

function TaskRow(props: {
  task: BackgroundTask;
  selected: boolean;
  nowMs: number;
  onOpen: (taskId: string) => void;
  onStop: (taskId: string) => void;
}) {
  const t = props.task;
  const tone = taskTone(t.status);
  const progress = estimateProgress(t, props.nowMs);

  return (
    <div
      className={[
        "session-item",
        "bgtask-row",
        props.selected ? "active" : "",
        isOverdue(t, props.nowMs) ? "is-overdue" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      data-testid={`bgtask-row-${t.id}`}
    >
      <a
        href={appNavHrefTask(t.id)}
        className="bgtask-row-main"
        aria-current={props.selected ? "true" : undefined}
        onClick={(ev) => sameTabInAppNavClick(ev, () => props.onOpen(t.id))}
      >
        <div className="bgtask-row-text-block">
          <div className="bgtask-row-title-line">
            <span
              className={`bgtask-dot bgtask-dot--${tone}`}
              aria-hidden="true"
            />
            <span className="bgtask-row-label" title={t.command || t.label}>
              {t.label}
            </span>
            <span className="bgtask-row-status">{taskStatusLabel(t.status)}</span>
          </div>
          <div className="bgtask-row-meta">{taskTimingLine(t, props.nowMs)}</div>
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
      </a>
      {t.running ? (
        <div className="bgtask-row-actions">
          <button
            type="button"
            className="composer-icon composer-run-icon composer-send-stop composer-run-icon--stop bgtask-stop-icon"
            aria-label={`Stop ${t.label}`}
            title="Stop task"
            data-testid={`bgtask-stop-${t.id}`}
            onClick={(ev) => {
              ev.stopPropagation();
              props.onStop(t.id);
            }}
          >
            <IconStop />
          </button>
        </div>
      ) : null}
    </div>
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

  // A running task keeps appending; follow the tail unless the reader scrolled
  // up, the same courtesy a terminal gives.
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
          <span
            className={`bgtask-dot bgtask-dot--${taskTone(t.status)}`}
            aria-hidden="true"
          />
          <span className="bgtask-detail-status">
            {taskStatusLabel(t.status)}
          </span>
          <span className="bgtask-detail-timing">
            {taskTimingLine(t, props.nowMs)}
          </span>
        </div>
        {t.command ? (
          <pre className="bgtask-detail-command">{t.command}</pre>
        ) : null}
        {t.error ? (
          <div className="bgtask-detail-error">{t.error}</div>
        ) : null}
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

export function BackgroundTasksDrawer(props: {
  open: boolean;
  /** Task id shown in the detail pane; same row highlight as History. */
  selectedTaskId: string | null;
  /** Extra class for dock layout (e.g. bgtask-dock-drawer). */
  className?: string;
  tasks: BackgroundTask[];
  selectedOutput: string;
  listError: string | null;
  loading: boolean;
  /** Milliseconds clock, refreshed by the shell so the ticker advances between polls. */
  nowMs: number;
  hasSession: boolean;
  onClose: () => void;
  onOpenTask: (taskId: string) => void;
  onBackToList: () => void;
  onStopTask: (taskId: string) => void;
}) {
  if (!props.open) {
    return null;
  }

  const ordered = sortTasksForDrawer(props.tasks);
  const selected =
    props.selectedTaskId !== null
      ? ordered.find((t) => t.id === props.selectedTaskId) || null
      : null;

  return (
    <aside
      className={["sessions", "bgtasks", "drawer", props.className || ""]
        .filter(Boolean)
        .join(" ")}
      aria-label="Background tasks"
      data-testid="bgtasks-drawer"
      data-variant="drawer"
    >
      <div className="sessions-head">
        <span>Tasks</span>
        <button
          type="button"
          className="sessions-close"
          aria-label="Close tasks"
          data-testid="bgtasks-drawer-close"
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
        <div className="session-list bgtask-list">
          {props.listError ? (
            <div className="sessions-empty" data-testid="bgtasks-list-error">
              {props.listError}
            </div>
          ) : null}
          {!props.hasSession && !props.listError ? (
            <div className="sessions-empty" data-testid="bgtasks-no-session">
              Open a chat to see its background tasks.
            </div>
          ) : null}
          {props.hasSession &&
          !props.listError &&
          !props.loading &&
          ordered.length === 0 ? (
            <div className="sessions-empty" data-testid="bgtasks-list-empty">
              No background tasks yet. The agent starts one when a command is
              slow enough to be worth running detached.
            </div>
          ) : null}
          {props.loading && ordered.length === 0 && !props.listError ? (
            <div className="sessions-empty" data-testid="bgtasks-list-loading">
              Loading…
            </div>
          ) : null}
          {ordered.map((t) => (
            <TaskRow
              key={t.id}
              task={t}
              selected={props.selectedTaskId === t.id}
              nowMs={props.nowMs}
              onOpen={props.onOpenTask}
              onStop={props.onStopTask}
            />
          ))}
        </div>
      )}
    </aside>
  );
}
