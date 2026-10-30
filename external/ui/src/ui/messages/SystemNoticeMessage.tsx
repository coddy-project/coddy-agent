import { memo } from "react";

import {
  formatUtcToLocalFullDetail,
  formatUtcToLocalHM,
} from "./formatMessageTime";
import { useT } from "../i18n/I18nProvider";
import { MessageCopyIconButton } from "./MessageCopyIconButton";
import { MessageRetryIconButton } from "./MessageRetryIconButton";
import {
  formatGenerationSpeed,
  type GenerationSummary,
} from "../chat/generationSpeed";
import { formatElapsedSeconds } from "../chat/liveStatus";
import { CoddyWorkIndicator } from "./CoddyWorkIndicator";
import { TurnTasksSegment } from "./TypingDotsMessage";
import { formatTurnTokens } from "../chat/turnProgress";

export const SystemNoticeMessage = memo(function SystemNoticeMessage(props: {
  /** The transcript row id, stamped on the row so the transcript window can
   *  find it on screen. */
  rowId?: string;
  /** error: a failed request or turn; notice: information the operator should see once. */
  level: "error" | "notice";
  message: string;
  generation?: GenerationSummary;
  createdAtUtc?: string;
  /** When provided, a refresh button re-runs the last turn (e.g. after a no-response error). */
  onRetry?: () => void;
  /** Tasks the finished turn left running: its line says so and its mark turns. */
  runningTasks?: number;
  /** Opens the Tasks panel from that count. */
  onOpenTasks?: () => void;
}) {
  const { t, tp } = useT();
  if (props.generation) {
    // The live line as the turn left it: its own parts, the mark at rest and no
    // phrase, since no step is under way. When it finished is its tooltip.
    const { elapsedMs, outputTokens, tokensPerSecond } = props.generation;
    const at = props.createdAtUtc;
    const runningTasks = props.runningTasks ?? 0;
    const tasksLabel = tp("tasks.running", runningTasks);
    return (
      <div
        className="msg-assistant-stack"
        data-row-id={props.rowId}
        data-testid="generation-summary"
      >
        <div
          className={
            "typing-dots" + (runningTasks > 0 ? "" : " typing-dots--finished")
          }
          {...(at ? { title: formatUtcToLocalFullDetail(at) } : {})}
        >
          <CoddyWorkIndicator />
          <span className="typing-dots-status typing-dots-status--finished">
            <span className="typing-dots-turn">
              {elapsedMs !== undefined ? (
                <span className="typing-dots-turn-item typing-dots-turn-time">
                  {formatElapsedSeconds(elapsedMs)}
                </span>
              ) : null}
              <span className="typing-dots-turn-item">
                {tp("status.turnTokens", outputTokens, {
                  shown: formatTurnTokens(outputTokens),
                })}
                {" · "}
                {t("status.generationSpeed", {
                  speed: formatGenerationSpeed(tokensPerSecond),
                })}
              </span>
              {runningTasks > 0 ? (
                <TurnTasksSegment
                  label={tasksLabel}
                  {...(props.onOpenTasks
                    ? {
                        onOpen: props.onOpenTasks,
                        aria: t("tasks.openAria", { label: tasksLabel }),
                      }
                    : {})}
                />
              ) : null}
            </span>
          </span>
        </div>
      </div>
    );
  }
  const timeHM = props.createdAtUtc
    ? formatUtcToLocalHM(props.createdAtUtc)
    : "";
  const timeFull =
    props.createdAtUtc && timeHM
      ? formatUtcToLocalFullDetail(props.createdAtUtc)
      : "";
  return (
    <div
      className={`msg-system-stack msg-system-stack-${props.level}`}
      data-row-id={props.rowId}
    >
      <div
        className={`msg msg-system msg-system-${props.level}`}
        role={props.level === "error" ? "alert" : "status"}
      >
        <div className="msg-system-label">{t("messages.systemLabel")}</div>
        <pre className="msg-system-body">{props.message}</pre>
      </div>
      <div className="msg-system-foot">
        <MessageCopyIconButton
          textToCopy={props.message}
          tooltip={t("messages.copyMessage")}
          ariaLabel={t("messages.copyErrorMessage")}
          dataTestId="system-message-copy"
        />
        {props.level === "error" && props.onRetry ? (
          <MessageRetryIconButton
            onRetry={props.onRetry}
            tooltip={t("messages.refresh")}
            ariaLabel={t("messages.retryLastMessage")}
            dataTestId="system-message-retry"
          />
        ) : null}
        {timeHM ? (
          <time
            className="msg-system-time"
            dateTime={props.createdAtUtc}
            title={timeFull || undefined}
          >
            {timeHM}
          </time>
        ) : null}
      </div>
    </div>
  );
});
