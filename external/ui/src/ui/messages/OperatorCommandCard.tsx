import { useCallback, useState } from "react";

import { ShellCommandBlock } from "../chat/PermissionPromptPreview";
import {
  parseOperatorCommandArgs,
  submitOperatorCommandAction,
  type OperatorCommandAction,
  type OperatorCommandState,
} from "../chat/operatorCommand";
import {
  parseOperatorCommandResult,
  type OperatorCommandOutcome,
} from "../chat/operatorCommandResult";
import {
  dropOperatorCommandRecord,
  markOperatorCommandAnswered,
} from "../chat/operatorCommandSessionStore";
import { useT } from "../i18n/I18nProvider";
import { Chevron } from "../components/Chevron";
import "./OperatorCommandCard.css";

/** Where the card stands: what it shows and which buttons it offers. */
type View =
  | "pending" // the arguments are in, the request is not (or was never seen)
  | "waiting" // the server waits for Run or Decline
  | "declining"
  | "running"
  | "stopping"
  | "finished"; // the run ended, or the call did

function isTerminal(status: string): boolean {
  return (
    status === "completed" || status === "failed" || status === "cancelled"
  );
}

/**
 * The card of an operator_command call: the command the agent was refused,
 * exactly as it will run, for the person at the web UI to run or decline.
 * Nothing on it is editable - the command, the folder and the reason are text
 * - and nothing takes the focus when it arrives: a key pressed for the
 * composer must never run a command. While the command runs its output streams
 * here and Stop interrupts it (a second press kills it); once the call is over
 * the card is the record of it, rebuilt from the arguments and the result.
 */
export function OperatorCommandCard(props: {
  toolCallId: string;
  /** The session on screen: the one the request belongs to when the row holds
   *  the run's frames but not the request itself. */
  sessionId?: string | undefined;
  status: string;
  argsText?: string | undefined;
  resultText?: string | undefined;
  fullResultText?: string | undefined;
  resultWasTruncated?: boolean | undefined;
  state?: OperatorCommandState | undefined;
  onFetchToolCallFull?: ((toolCallId: string) => Promise<void>) | undefined;
}) {
  const { t } = useT();
  const { state } = props;
  const status = (props.status || "").toLowerCase();
  const request = state?.request;
  const args = parseOperatorCommandArgs(props.argsText);
  const command = request?.command ?? args?.command ?? "";
  const reason = (request?.reason || args?.reason || "").trim();
  const liveCwd = (state?.cwd || request?.cwd || "").trim();
  const liveShell = (state?.shell || request?.shell || "").trim();
  const liveHost = (state?.host || request?.host || "").trim();
  const multiplexer = (state?.multiplexer || request?.multiplexer || "").trim();
  // The executable the shell resolved to: a shell swapped through the
  // configuration must show here, not only its kind.
  const shellPath = (request?.shellPath || "").trim();
  const attachCommand = (request?.attachCommand || "").trim();

  const [answered, setAnswered] = useState<OperatorCommandAction | null>(null);
  const [stopsSent, setStopsSent] = useState(0);
  const [busy, setBusy] = useState(false);
  // The request is gone or another surface took it: Run and Decline would only
  // be refused again.
  const [settled, setSettled] = useState(false);
  // The server says the request is over (404 to an answer): what the last
  // frame claimed - running, stopping - is stale, and Stop would be refused.
  const [requestEnded, setRequestEnded] = useState(false);
  const [note, setNote] = useState("");
  const [loadingFull, setLoadingFull] = useState(false);

  const given = answered ?? state?.answered ?? null;
  const phase = state?.phase;
  const terminal = isTerminal(status);
  let view: View;
  // A command that outlived a cancelled turn still runs, and its request is
  // open until it ends: the card keeps Stop for it.
  if (phase === "stopping" && status === "cancelled") view = "stopping";
  else if (terminal || phase === "ended" || phase === "stopped")
    view = "finished";
  else if (phase === "stopping") view = "stopping";
  else if (phase === "running") view = "running";
  else if (given === "run" || given === "stop") view = "running";
  else if (given === "decline") view = "declining";
  else if (request && !settled) view = "waiting";
  else view = "pending";

  const sessionId = (request?.sessionId || props.sessionId || "").trim();
  const requestId = request?.requestId ?? state?.requestId ?? "";

  const send = useCallback(
    async (action: OperatorCommandAction) => {
      if (!sessionId || !requestId) {
        setNote(t("operatorCommand.ended"));
        setSettled(true);
        return;
      }
      setBusy(true);
      setNote("");
      const res = await submitOperatorCommandAction(
        sessionId,
        requestId,
        action,
      );
      setBusy(false);
      if (res.ok) {
        if (action === "stop") {
          setStopsSent((n) => n + 1);
        } else {
          setAnswered(action);
          markOperatorCommandAnswered(sessionId, requestId, action);
        }
        return;
      }
      if (res.status === 409 && action !== "stop") {
        setNote(t("operatorCommand.alreadyAnswered"));
        setSettled(true);
      } else if (res.status === 404 || res.status === 409) {
        // A Stop refused with 409 is for a request that is not running any
        // more: nothing was answered elsewhere.
        setNote(t("operatorCommand.ended"));
        setSettled(true);
        setRequestEnded(true);
        // Nothing waits on the server any more: a reload must not bring the
        // request back.
        dropOperatorCommandRecord(sessionId, props.toolCallId);
      } else {
        setNote(t("operatorCommand.sendFailed"));
      }
    },
    [props.toolCallId, requestId, sessionId, t],
  );

  const resultSource = props.fullResultText || props.resultText || "";
  const outcome: OperatorCommandOutcome | null = terminal
    ? parseOperatorCommandResult(resultSource)
    : null;
  // A card rebuilt from the history has only the result: its header says
  // where the command ran.
  const where =
    outcome && (outcome.kind === "ran" || outcome.kind === "stopped")
      ? outcome
      : null;
  // The folder the command really ran in wins over the one the call asked for.
  const cwd = liveCwd || where?.cwd || (args?.cwd ?? "").trim();
  const shell = liveShell || where?.shell || "";
  const host = liveHost || where?.host || "";

  // The status line of a finished card: how the run ended, from the result the
  // tool wrote, or - between the last frame and the call's end - from the frame.
  let endLine = "";
  let endTone: "ok" | "bad" | "muted" = "muted";
  let output = "";
  if (view === "finished") {
    if (outcome?.kind === "ran") {
      endLine = t("operatorCommand.exitCode", { code: outcome.exitCode });
      endTone = outcome.exitCode === 0 ? "ok" : "bad";
      output = outcome.output;
    } else if (outcome?.kind === "stopped") {
      endLine = t("operatorCommand.stoppedExit", { code: outcome.exitCode });
      endTone = "bad";
      output = outcome.output;
    } else if (outcome?.kind === "declined") {
      endLine = t("operatorCommand.declined");
      output = outcome.note
        ? t("operatorCommand.declinedNote", { note: outcome.note })
        : "";
    } else if (phase === "stopped" || phase === "ended") {
      // Between the last frame and the call's end, or a call a cancelled
      // turn ended (cancelled, or failed with the cancel's error) while its
      // command ran: the result says nothing of the run, the frame does.
      const code = state?.exitCode;
      if (code === undefined) {
        endLine =
          phase === "stopped"
            ? t("operatorCommand.stopped")
            : t("operatorCommand.endedPlain");
        endTone = phase === "stopped" ? "bad" : "muted";
      } else {
        endLine =
          phase === "stopped"
            ? t("operatorCommand.stoppedExit", { code })
            : t("operatorCommand.exitCode", { code });
        endTone = phase === "ended" && code === 0 ? "ok" : "bad";
      }
      output = state?.output ?? "";
    } else if (outcome?.kind === "other") {
      endLine =
        status === "failed"
          ? t("operatorCommand.failed")
          : status === "cancelled"
            ? t("operatorCommand.cancelled")
            : "";
      endTone = status === "completed" ? "muted" : "bad";
      output = outcome.text;
    } else if (status === "cancelled") {
      endLine = t("operatorCommand.cancelled");
      endTone = "bad";
    } else if (status === "failed") {
      endLine = t("operatorCommand.failed");
      endTone = "bad";
    }
  } else if (view === "running" || view === "stopping") {
    output = state?.output ?? "";
  } else if (view === "declining") {
    endLine = t("operatorCommand.declined");
  }

  const canLoadFull =
    view === "finished" &&
    terminal &&
    props.resultWasTruncated === true &&
    !props.fullResultText &&
    !!props.onFetchToolCallFull;
  const loadFull = useCallback(async () => {
    const fetchFull = props.onFetchToolCallFull;
    if (!fetchFull) return;
    setLoadingFull(true);
    try {
      await fetchFull(props.toolCallId);
    } catch {
      // the preview stays
    } finally {
      setLoadingFull(false);
    }
  }, [props.onFetchToolCallFull, props.toolCallId]);

  const live = (view === "running" || view === "stopping") && !requestEnded;
  const killNext = view === "stopping" || stopsSent > 0;
  const dropped = live ? (state?.droppedBytes ?? 0) : 0;

  return (
    <div
      className="permission-prompt-card operator-command-card"
      data-testid="operator-command-card"
      data-view={view}
    >
      <div className="permission-prompt-head operator-command-head">
        <span className="permission-prompt-title">
          {t("operatorCommand.title")}
        </span>
        <span className="operator-command-badges">
          {shell ? (
            <code
              className="permission-prompt-tool-badge"
              title={t("operatorCommand.shell")}
              data-testid="operator-command-shell"
            >
              {shell}
            </code>
          ) : null}
          {host ? (
            <code
              className="permission-prompt-tool-badge"
              title={t("operatorCommand.host")}
              data-testid="operator-command-host"
            >
              {host}
            </code>
          ) : null}
          {multiplexer ? (
            <code
              className="permission-prompt-tool-badge"
              title={t("operatorCommand.multiplexer")}
              data-testid="operator-command-multiplexer"
            >
              {multiplexer}
            </code>
          ) : null}
        </span>
      </div>
      {cwd || reason || shellPath ? (
        <dl className="operator-command-fields">
          {shellPath ? (
            <div className="operator-command-field">
              <dt>{t("operatorCommand.shell")}</dt>
              <dd
                className="operator-command-cwd"
                data-testid="operator-command-shell-path"
              >
                {shellPath}
              </dd>
            </div>
          ) : null}
          {cwd ? (
            <div className="operator-command-field">
              <dt>{t("operatorCommand.folder")}</dt>
              <dd
                className="operator-command-cwd"
                data-testid="operator-command-cwd"
              >
                {cwd}
              </dd>
            </div>
          ) : null}
          {reason ? (
            <div className="operator-command-field">
              <dt>{t("operatorCommand.reason")}</dt>
              <dd data-testid="operator-command-reason">{reason}</dd>
            </div>
          ) : null}
        </dl>
      ) : null}
      {command ? (
        <ShellCommandBlock
          text={command}
          copyTestId="operator-command-copy"
          className="operator-command-shell"
        />
      ) : null}
      {attachCommand && live ? (
        <details className="operator-command-attach">
          <summary>
            <Chevron className="operator-command-attach-chevron" />
            {t("operatorCommand.attach")}
          </summary>
          <ShellCommandBlock
            text={attachCommand}
            copyTestId="operator-command-attach-copy"
            className="operator-command-shell"
          />
        </details>
      ) : null}
      {output ? (
        <div
          className="tool-call-result-card operator-command-output"
          aria-label={t("operatorCommand.outputAria")}
        >
          <div className="tool-call-result-content">
            <pre
              className="tool-result-pre"
              data-testid="operator-command-output"
            >
              {output}
            </pre>
          </div>
        </div>
      ) : null}
      {dropped > 0 ? (
        <div className="operator-command-dropped muted">
          {t("operatorCommand.dropped", { bytes: dropped })}
        </div>
      ) : null}
      {canLoadFull ? (
        <div className="operator-command-more">
          <button
            type="button"
            className="tool-overflow-toggle"
            data-testid="operator-command-more"
            disabled={loadingFull}
            onClick={() => void loadFull()}
          >
            {loadingFull ? t("messages.toolLoading") : t("messages.toolMore")}
          </button>
        </div>
      ) : null}
      {live ? (
        <div className="operator-command-state" role="status">
          {view === "stopping"
            ? t("operatorCommand.stopping")
            : t("operatorCommand.running")}
        </div>
      ) : endLine ? (
        <div
          className={`operator-command-state operator-command-state--${endTone}`}
          role="status"
          data-testid="operator-command-end"
        >
          {endLine}
        </div>
      ) : null}
      {view === "waiting" ? (
        <div
          className="permission-prompt-actions"
          role="group"
          aria-label={t("operatorCommand.actionsAria")}
        >
          <button
            type="button"
            className="permission-prompt-btn permission-prompt-btn--allow"
            data-testid="operator-command-run"
            disabled={busy}
            onClick={() => void send("run")}
          >
            {t("operatorCommand.run")}
          </button>
          <button
            type="button"
            className="permission-prompt-btn permission-prompt-btn--reject"
            data-testid="operator-command-decline"
            disabled={busy}
            onClick={() => void send("decline")}
          >
            {t("operatorCommand.decline")}
          </button>
        </div>
      ) : live && requestId && sessionId ? (
        <div className="permission-prompt-actions">
          <button
            type="button"
            className="permission-prompt-btn permission-prompt-btn--reject"
            data-testid="operator-command-stop"
            disabled={busy}
            onClick={() => void send("stop")}
          >
            {killNext ? t("operatorCommand.kill") : t("operatorCommand.stop")}
          </button>
        </div>
      ) : null}
      {note ? (
        <div
          className="operator-command-note"
          role="alert"
          data-testid="operator-command-note"
        >
          {note}
        </div>
      ) : null}
    </div>
  );
}
