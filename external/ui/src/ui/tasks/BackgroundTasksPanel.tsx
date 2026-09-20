import { useCallback, useEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { formatTurnTokens } from "../chat/turnProgress";
import { BellIcon } from "../components/BellIcon";
import { Chevron } from "../components/Chevron";
import { CodeBlockCopyButton } from "../messages/CodeBlockCopyButton";
import type { BackgroundTask } from "./types";
import {
  agentTranscriptSessionId,
  agentUsage,
  estimateProgress,
  groupTasks,
  isAgentTask,
  isOverdue,
  isServerTask,
  serverTaskUrl,
  taskErrorText,
  taskMetaLine,
  taskStatusLabel,
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
 * The card is one control. Its summary - everything but the Stop button and the way
 * into the task itself - is a single button stretched over the card, so a click
 * anywhere expands the card in place; the controls above that surface keep working on
 * their own. There is no second pane: the open card shows the command, the captured
 * output and how the run ended right where it stands in the list, and any number of
 * cards can be open at once.
 *
 * What a task costs and where it leads is read without opening anything: the model and
 * the tokens an agent run has spent, how long it has run, and the one way in the card
 * has - Show transcript for a subagent run, the address for a preview server - are all
 * on the folded card, and none of them is said again inside it.
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
  const { t, tp, locale } = useT();
  const task = props.task;
  const progress = estimateProgress(task, props.nowMs);
  const overdue = isOverdue(task, props.nowMs);
  const title = taskTitle(task);
  const usage = agentUsage(task);
  // Where the task leads, read off the card the operator already sees: a
  // subagent run is the conversation it holds, a preview server is the page it
  // answers with. A shell command is neither, so both come back empty for it.
  const agentSid = isAgentTask(task) ? agentTranscriptSessionId(task) : null;
  const serverUrl = serverTaskUrl(task);
  // A bell after the title: the running task will wake the agent when it ends,
  // or the finished one did - the one place the web UI says what woke it, since
  // the turn it started shows nothing of its own in the transcript.
  const wakeTitle = task.running
    ? task.notify_on_finish
      ? t("tasks.notifyTitle")
      : ""
    : task.woke_agent
      ? t("tasks.wokeTitle")
      : "";
  // The opener is stretched over the whole summary, so its title is the card's hover
  // text: the work, then for an agent run the full model id and the exact split of
  // the tokens the card shortens.
  const number = new Intl.NumberFormat(locale);
  const hover = [
    task.command || task.label,
    serverUrl,
    usage?.modelId || "",
    usage && usage.tokens > 0
      ? t("tasks.agentTokensTitle", {
          input: number.format(usage.inputTokens),
          output: number.format(usage.outputTokens),
        })
      : "",
  ]
    .filter(Boolean)
    .join("\n");

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
      data-task-card={task.id}
    >
      <div className="bgtask-card-summary">
        <div className="bgtask-card-head">
          <button
            type="button"
            className="bgtask-card-open"
            data-testid={`bgtask-open-${task.id}`}
            aria-expanded={props.open}
            title={hover}
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
            {wakeTitle ? (
              <span
                className="bgtask-notify"
                role="img"
                aria-label={wakeTitle}
                title={wakeTitle}
                data-testid={`bgtask-notify-${task.id}`}
              >
                <BellIcon />
              </span>
            ) : null}
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
          <span className="bgtask-card-meta-line">
            {taskMetaLine(task, props.nowMs)}
          </span>
          {usage ? (
            <span
              className="bgtask-card-usage"
              data-testid={`bgtask-usage-${task.id}`}
            >
              {usage.model ? (
                <span
                  className="bgtask-card-model"
                  data-testid={`bgtask-model-${task.id}`}
                >
                  {usage.model}
                </span>
              ) : null}
              {usage.model && usage.tokens > 0 ? (
                <span className="bgtask-card-usage-sep" aria-hidden="true">
                  {" · "}
                </span>
              ) : null}
              {usage.tokens > 0 ? (
                <span className="bgtask-card-tokens">
                  {tp("status.turnTokens", usage.tokens, {
                    shown: formatTurnTokens(usage.tokens),
                  })}
                </span>
              ) : null}
            </span>
          ) : null}
          {/* The way into the task, on a row of its own under the status and the
              usage: the conversation a subagent run holds, the page a preview
              server answers with. A card carries at most one of the two, because a
              task is one kind or the other. */}
          {isAgentTask(task) ? (
            <span className="bgtask-card-transcript-row">
              <button
                type="button"
                className="bgtask-card-transcript"
                data-testid={`bgtask-open-transcript-${task.id}`}
                disabled={agentSid === null}
                aria-label={t("tasks.openTranscriptAria", { label: title })}
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
            </span>
          ) : task.running && serverUrl ? (
            // A server that has stopped carries no address: the page is gone, and
            // a link to it would only lead to a refused connection.
            <span className="bgtask-card-address-row">
              <a
                className="bgtask-card-link"
                href={serverUrl}
                target="_blank"
                rel="noopener noreferrer"
                title={t("tasks.openServer")}
                data-testid={`bgtask-link-${task.id}`}
              >
                {serverUrl}
              </a>
            </span>
          ) : null}
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
      {props.open ? <TaskCardBody task={task} output={props.output} /> : null}
    </div>
  );
}

/**
 * What an open card adds under its summary: the command with a copy control (a shell
 * task; neither an agent run nor a preview server has a shell behind it, so there is
 * nothing to put here for them), the error the run ended with unless it only repeats
 * the exit code, the captured output in a box of its own height, and - once the task
 * has finished - a foot that says how it ended and, for a command, with what exit
 * code. How long it ran and the way into the task - an agent run's transcript, a
 * preview server's address - are the summary's, which is read whether the card is
 * open or not.
 */
function TaskCardBody(props: { task: BackgroundTask; output: string }) {
  const { t } = useT();
  const task = props.task;
  const preRef = useRef<HTMLPreElement | null>(null);
  const [follow, setFollow] = useState(true);
  const agent = isAgentTask(task);
  const server = isServerTask(task);

  useEffect(() => {
    const el = preRef.current;
    if (!el || !follow) {
      return;
    }
    el.scrollTop = el.scrollHeight;
  }, [props.output, follow]);

  const errorText = taskErrorText(task);
  const footParts: string[] = [];
  if (!task.running) {
    // How the task ended leads the foot: a folded card leaves it to the dot.
    footParts.push(taskStatusLabel(task.status));
    // Neither an agent run nor a preview server has a process behind it: the
    // pool's exit code for them is synthetic, and the status already says how
    // the run ended.
    if (!agent && !server && typeof task.exit_code === "number") {
      footParts.push(t("tasks.footExitCode", { code: task.exit_code }));
    }
    // How long the task ran is not repeated here: the summary above says it,
    // and it says it whether this card is open or folded.
  }

  return (
    <div className="bgtask-card-body" data-testid={`bgtask-body-${task.id}`}>
      {!agent && !server && task.command ? (
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

      {errorText ? <div className="bgtask-card-error">{errorText}</div> : null}

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
        data-testid={`bgtask-output-${task.id}`}
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

/** How often the output of an open card is read again while its task runs. */
const OPEN_CARD_POLL_MS = 2500;

/**
 * A card the shell asks the panel to open: "Open in Tasks" on a transcript row, or a
 * link that names a task. `seq` tells a repeated request for the same task from the
 * request the panel has already honoured.
 */
export type TaskFocus = { taskId: string; seq: number };

/**
 * Background tasks of the session that owns this chat. The panel is docked
 * inside the session on purpose: a task belongs to the conversation that
 * started it, so there is never a question of which session a process came
 * from.
 *
 * Which cards are open is the panel's own business: the reader opens as many as they
 * like, and the address says only that the panel is showing. The panel reads the output
 * of every open card through `loadOutput`, again while the card's task runs and once
 * more when it ends.
 */
export function BackgroundTasksPanel(props: {
  open: boolean;
  tasks: BackgroundTask[];
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
  /** A card to open on the shell's behalf. */
  focus?: TaskFocus | null;
  /**
   * The card `focus` named is open. A pointer is good for one use: the shell drops it
   * here, or the next mount of the panel - which forgets what it has honoured - would
   * open the card again, in whichever chat is on screen by then.
   */
  onFocusHonoured?: (seq: number) => void;
  /** Reads the captured output of one task; null when it cannot be read right now. */
  loadOutput: (taskId: string) => Promise<string | null>;
  onClose: () => void;
  onStopTask: (taskId: string) => void | Promise<void>;
  onClearFinished: () => void;
  /** Routes to another session: the child transcript behind an agent task. */
  onOpenSession: (sessionId: string) => void;
}) {
  const { t } = useT();
  const [finishedOpen, setFinishedOpen] = useState(false);
  const [openIds, setOpenIds] = useState<readonly string[]>([]);
  const [outputs, setOutputs] = useState<Record<string, string>>({});
  const loadOutputRef = useRef(props.loadOutput);
  loadOutputRef.current = props.loadOutput;
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Reads of one card overlap - the poll, the final read, the read after Stop - and the
  // network may answer them out of order. Every read takes a number, and an answer
  // older than the one the card already shows is dropped: nothing reads a finished
  // card again, so a stale answer would otherwise stay for good.
  const readSeqRef = useRef(0);
  const appliedSeqRef = useRef<Record<string, number>>({});
  const readOutput = useCallback(async (taskId: string) => {
    const seq = ++readSeqRef.current;
    const text = await loadOutputRef.current(taskId);
    // An unreadable answer keeps what the card already shows.
    if (text === null || !mountedRef.current) {
      return;
    }
    if ((appliedSeqRef.current[taskId] ?? 0) > seq) {
      return;
    }
    appliedSeqRef.current[taskId] = seq;
    setOutputs((prev) =>
      prev[taskId] === text ? prev : { ...prev, [taskId]: text },
    );
  }, []);

  const openCard = useCallback(
    (taskId: string) => {
      setOpenIds((prev) => (prev.includes(taskId) ? prev : [...prev, taskId]));
      void readOutput(taskId);
    },
    [readOutput],
  );

  const toggleCard = (taskId: string) => {
    if (openIds.includes(taskId)) {
      setOpenIds((prev) => prev.filter((id) => id !== taskId));
      return;
    }
    openCard(taskId);
  };

  // The shell points at a card: open it, its section with it, and bring it into view.
  const focusSeq = props.focus?.seq;
  const focusTaskId = props.focus?.taskId;
  const honouredFocusRef = useRef<number | undefined>(undefined);
  const onFocusHonouredRef = useRef(props.onFocusHonoured);
  onFocusHonouredRef.current = props.onFocusHonoured;
  useEffect(() => {
    if (
      !props.open ||
      !focusTaskId ||
      focusSeq === undefined ||
      honouredFocusRef.current === focusSeq ||
      !props.tasks.some((task) => task.id === focusTaskId)
    ) {
      return;
    }
    honouredFocusRef.current = focusSeq;
    const task = props.tasks.find((row) => row.id === focusTaskId);
    if (task && !task.running) {
      setFinishedOpen(true);
    }
    openCard(focusTaskId);
    onFocusHonouredRef.current?.(focusSeq);
    const handle = window.requestAnimationFrame(() => {
      for (const el of document.querySelectorAll("[data-task-card]")) {
        if (el.getAttribute("data-task-card") === focusTaskId) {
          el.scrollIntoView?.({ block: "nearest" });
        }
      }
    });
    return () => window.cancelAnimationFrame(handle);
  }, [props.open, props.tasks, focusTaskId, focusSeq, openCard]);

  // While an open card's task runs its output is read again; when the task ends between
  // two reads the card reads what it printed last.
  const running = new Set(
    props.tasks.filter((task) => task.running).map((task) => task.id),
  );
  const openRunningKey = openIds
    .filter((id) => running.has(id))
    .sort()
    .join("\n");
  useEffect(() => {
    if (!props.open || !openRunningKey) {
      return;
    }
    const ids = openRunningKey.split("\n");
    const handle = window.setInterval(() => {
      for (const id of ids) {
        void readOutput(id);
      }
    }, OPEN_CARD_POLL_MS);
    return () => {
      window.clearInterval(handle);
      // These cards were running a moment ago: whichever of them has ended since
      // reads its final output.
      for (const id of ids) {
        void readOutput(id);
      }
    };
  }, [props.open, openRunningKey, readOutput]);

  // An open card whose task has just ended moves under the Finished counter; the
  // section opens with it, or the card the reader was watching would vanish.
  const runningKey = [...running].sort().join("\n");
  const wasRunningRef = useRef<ReadonlySet<string>>(new Set());
  useEffect(() => {
    const now = new Set(runningKey ? runningKey.split("\n") : []);
    const ended = [...wasRunningRef.current].filter((id) => !now.has(id));
    wasRunningRef.current = now;
    if (ended.some((id) => openIds.includes(id))) {
      setFinishedOpen(true);
    }
    // openIds is read, not watched: only a task ending is the trigger.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runningKey]);

  // A task that left the list (Clear, retention) takes its card state with it.
  const knownKey = props.tasks
    .map((task) => task.id)
    .sort()
    .join("\n");
  useEffect(() => {
    const known = new Set(knownKey ? knownKey.split("\n") : []);
    for (const id of Object.keys(appliedSeqRef.current)) {
      if (!known.has(id)) {
        delete appliedSeqRef.current[id];
      }
    }
    setOpenIds((prev) =>
      prev.every((id) => known.has(id))
        ? prev
        : prev.filter((id) => known.has(id)),
    );
    setOutputs((prev) => {
      const stale = Object.keys(prev).filter((id) => !known.has(id));
      if (stale.length === 0) {
        return prev;
      }
      const next = { ...prev };
      for (const id of stale) {
        delete next[id];
      }
      return next;
    });
  }, [knownKey]);

  if (!props.open) {
    return null;
  }

  const { running: live, finished } = groupTasks(props.tasks);
  // The cap keeps a long history cheap; a card that is open is shown wherever it
  // stands, or "Open in Tasks" on an early row would open a card nobody can see.
  const shown = finished.filter(
    (task, i) => i < FINISHED_RENDER_CAP || openIds.includes(task.id),
  );
  const stop = (taskId: string) => {
    void Promise.resolve(props.onStopTask(taskId)).then(() => {
      if (openIds.includes(taskId)) {
        void readOutput(taskId);
      }
    });
  };
  const card = (task: BackgroundTask) => (
    <TaskCard
      key={task.id}
      task={task}
      nowMs={props.nowMs}
      open={openIds.includes(task.id)}
      output={outputs[task.id] ?? ""}
      onToggle={toggleCard}
      onStop={stop}
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
        {live.map(card)}

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
                <Chevron open={finishedOpen} />
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
