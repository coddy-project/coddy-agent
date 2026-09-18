import { useEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { Chevron } from "../components/Chevron";
import { CodeBlockCopyButton } from "../messages/CodeBlockCopyButton";
import type { BackgroundTask } from "./types";
import {
  agentTranscriptSessionId,
  displayElapsedSeconds,
  estimateProgress,
  formatDuration,
  groupTasks,
  isAgentTask,
  isOverdue,
  taskMetaLine,
  taskTag,
  taskTitle,
  taskTone,
} from "./taskStatus";

/** How many finished cards render before the rest stay behind the scroll. */
const FINISHED_RENDER_CAP = 40;

function IconStop() {
  return (
    <span className="composer-send-glyph" aria-hidden="true">
      <span className="composer-stop-square" />
    </span>
  );
}

/**
 * One task of the panel, whatever it is - a shell command, a subagent run, the memory
 * run of a turn - and whether it runs or has finished. Every card has the same parts in
 * the same place: the status dot, a tag that says what stands behind the task, the title
 * of the work, and a meta line under them.
 *
 * The card is one control. Its summary - everything but the Stop button - is a single
 * button stretched over the card, so a click anywhere expands the card in place; Stop
 * sits above that surface and keeps working on its own. There is no second pane: the
 * open card shows the command, the captured output and how the run ended right where
 * it stands in the list.
 */
function TaskCard(props: {
  task: BackgroundTask;
  nowMs: number;
  open: boolean;
  /** Output of the open card; ignored while the card is folded. */
  output: string;
  onToggle: (taskId: string) => void;
  onStop: (taskId: string) => void;
  onOpenSession: (sessionId: string) => void;
}) {
  const { t } = useT();
  const task = props.task;
  const progress = estimateProgress(task, props.nowMs);
  const overdue = isOverdue(task, props.nowMs);
  const title = taskTitle(task);

  return (
    <div
      className={[
        "bgtask-card",
        props.open ? "is-open" : "",
        overdue ? "is-overdue" : "",
        task.running ? "" : "is-finished",
      ]
        .filter(Boolean)
        .join(" ")}
      data-testid={`bgtask-card-${task.id}`}
    >
      <div className="bgtask-card-summary">
        <div className="bgtask-card-head">
          <button
            type="button"
            className="bgtask-card-open"
            data-testid={`bgtask-open-${task.id}`}
            aria-expanded={props.open}
            title={task.command || task.label}
            onClick={() => props.onToggle(task.id)}
          >
            <span
              className={`bgtask-dot bgtask-dot--${taskTone(task.status)}`}
              data-part="dot"
              aria-hidden="true"
            />
            <span
              className="bgtask-tag"
              data-part="tag"
              data-testid={`bgtask-tag-${task.id}`}
            >
              {taskTag(task)}
            </span>
            <span
              className="bgtask-card-label"
              data-part="title"
              data-testid={`bgtask-title-${task.id}`}
            >
              {title}
            </span>
          </button>
          {task.running ? (
            <button
              type="button"
              className="composer-icon composer-run-icon composer-send-stop composer-run-icon--stop bgtask-stop-icon"
              aria-label={t("tasks.stopAriaLabel", { label: title })}
              title={t("tasks.stopTitle")}
              data-testid={`bgtask-stop-${task.id}`}
              onClick={() => props.onStop(task.id)}
            >
              <IconStop />
            </button>
          ) : null}
        </div>
        <div
          className="bgtask-card-meta"
          data-part="meta"
          data-testid={`bgtask-meta-${task.id}`}
        >
          {taskMetaLine(task, props.nowMs)}
        </div>
        {progress !== null ? (
          <div
            className="bgtask-progress"
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round(progress * 100)}
            aria-label={t("tasks.progressAriaLabel", { label: title })}
          >
            <span
              className="bgtask-progress-fill"
              style={{ width: `${Math.round(progress * 100)}%` }}
            />
          </div>
        ) : null}
      </div>
      {props.open ? (
        <TaskCardBody
          task={task}
          output={props.output}
          nowMs={props.nowMs}
          onOpenSession={props.onOpenSession}
        />
      ) : null}
    </div>
  );
}

/**
 * What an open card adds under its summary: the command with a copy control (a shell
 * task) or the way to the child transcript (an agent run), the error the run ended
 * with, the captured output in a box of its own height, and - once the task has
 * finished - the exit code and how long it ran.
 */
function TaskCardBody(props: {
  task: BackgroundTask;
  output: string;
  nowMs: number;
  onOpenSession: (sessionId: string) => void;
}) {
  const { t } = useT();
  const task = props.task;
  const preRef = useRef<HTMLPreElement | null>(null);
  const [follow, setFollow] = useState(true);
  const agent = isAgentTask(task);
  const agentSid = agentTranscriptSessionId(task);

  useEffect(() => {
    const el = preRef.current;
    if (!el || !follow) {
      return;
    }
    el.scrollTop = el.scrollHeight;
  }, [props.output, follow]);

  const footParts: string[] = [];
  if (!task.running) {
    // An agent run has no process behind it: the pool's exit code for it is
    // synthetic, and the status already says how the run ended.
    if (!agent && typeof task.exit_code === "number") {
      footParts.push(t("tasks.footExitCode", { code: task.exit_code }));
    }
    footParts.push(
      t("tasks.footDuration", {
        value: formatDuration(displayElapsedSeconds(task, props.nowMs)),
      }),
    );
  }

  return (
    <div className="bgtask-card-body" data-testid={`bgtask-body-${task.id}`}>
      {agent ? (
        <div className="bgtask-card-actions">
          <button
            type="button"
            className="scheduler-btn bgtask-open-transcript"
            data-testid="bgtask-open-transcript"
            disabled={agentSid === null}
            title={
              agentSid === null
                ? t("tasks.openTranscriptUnavailable")
                : undefined
            }
            onClick={() => {
              if (agentSid !== null) {
                props.onOpenSession(agentSid);
              }
            }}
          >
            {t("tasks.openTranscript")}
          </button>
        </div>
      ) : task.command ? (
        <div className="bgtask-card-command">
          <pre
            className="bgtask-card-command-text"
            data-testid={`bgtask-command-${task.id}`}
          >
            {task.command}
          </pre>
          <CodeBlockCopyButton
            textToCopy={task.command}
            dataTestId={`bgtask-copy-command-${task.id}`}
          />
        </div>
      ) : null}

      {task.error ? (
        <div className="bgtask-card-error">{task.error}</div>
      ) : null}

      <div className="bgtask-card-output-head">
        <span>{t("tasks.outputHeading")}</span>
        {task.output_truncated ? (
          <span
            className="bgtask-card-truncated"
            title={t("tasks.truncatedTitle")}
          >
            {t("tasks.truncated")}
          </span>
        ) : null}
      </div>
      <pre
        ref={preRef}
        className="bgtask-card-output"
        data-testid="bgtask-output"
        onScroll={(ev) => {
          const el = ev.currentTarget;
          setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 24);
        }}
      >
        {props.output.trim() ? props.output : t("tasks.noOutput")}
      </pre>

      {footParts.length > 0 ? (
        <div
          className="bgtask-card-foot"
          data-testid={`bgtask-foot-${task.id}`}
        >
          {footParts.join(" · ")}
        </div>
      ) : null}
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
  /** The one card that is expanded, or null. */
  selectedTaskId: string | null;
  tasks: BackgroundTask[];
  /** Captured output of the expanded card. */
  selectedOutput: string;
  listError: string | null;
  loading: boolean;
  /** Milliseconds clock from the shell so every ticker advances together. */
  nowMs: number;
  /**
   * Extra class for a docked placement: the scheduler shows a job's runs with
   * this same panel inside its own cluster instead of beside the transcript.
   */
  className?: string;
  /** Panel heading; "Background tasks" unless the caller names it. */
  title?: string;
  /** Copy for an empty list; the chat's wording unless the caller names it. */
  emptyText?: string;
  onClose: () => void;
  /** Expands a card; the route carries its id, so a reload opens it again. */
  onOpenTask: (taskId: string) => void;
  /** Folds the open card. */
  onBackToList: () => void;
  onStopTask: (taskId: string) => void;
  onClearFinished: () => void;
  /** Routes to another session: the child transcript behind an agent task. */
  onOpenSession: (sessionId: string) => void;
}) {
  const { t } = useT();
  const [finishedOpen, setFinishedOpen] = useState(false);

  if (!props.open) {
    return null;
  }

  const { running, finished } = groupTasks(props.tasks);
  const selectedId = props.selectedTaskId;
  // A finished task that is open - from the route, or from "Open in Tasks" on a
  // transcript row - brings its section with it.
  const selectedIsFinished =
    selectedId !== null && finished.some((task) => task.id === selectedId);
  const historyOpen = finishedOpen || selectedIsFinished;
  const shown = finished.slice(0, FINISHED_RENDER_CAP);
  const toggle = (taskId: string) => {
    if (taskId === selectedId) {
      props.onBackToList();
    } else {
      props.onOpenTask(taskId);
    }
  };
  const card = (task: BackgroundTask) => (
    <TaskCard
      key={task.id}
      task={task}
      nowMs={props.nowMs}
      open={task.id === selectedId}
      output={task.id === selectedId ? props.selectedOutput : ""}
      onToggle={toggle}
      onStop={props.onStopTask}
      onOpenSession={props.onOpenSession}
    />
  );

  return (
    <aside
      className={["bgtasks-panel", props.className || ""]
        .filter(Boolean)
        .join(" ")}
      aria-label={props.title || t("tasks.panelTitle")}
      data-testid="bgtasks-panel"
    >
      <div className="sessions-head bgtasks-panel-head">
        <span>{props.title || t("tasks.panelTitle")}</span>
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
            {props.emptyText || t("tasks.empty")}
          </div>
        ) : null}

        {/* No heading over the live cards: a card that is not under the
            finished counter below is running, and saying so twice only
            costs a line of the panel. */}
        {running.map(card)}

        {finished.length > 0 ? (
          <>
            <div className="bgtask-section-row">
              <button
                type="button"
                className="bgtask-section-toggle"
                data-testid="bgtask-finished-toggle"
                aria-expanded={historyOpen}
                onClick={() => {
                  // Folding the history folds the card that kept it open.
                  if (historyOpen && selectedIsFinished) {
                    props.onBackToList();
                  }
                  setFinishedOpen(!historyOpen);
                }}
              >
                <Chevron open={historyOpen} />
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

            {historyOpen ? (
              <div
                className="bgtask-finished-list"
                data-testid="bgtask-finished-list"
              >
                {shown.map(card)}
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
    </aside>
  );
}
