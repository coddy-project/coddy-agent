import {
  formatUtcToLocalFullDetail,
  formatUtcToLocalHM,
} from "./formatMessageTime";
import { MessageCopyIconButton } from "./MessageCopyIconButton";
import { MessageRetryIconButton } from "./MessageRetryIconButton";

export function SystemNoticeMessage(props: {
  level: "error";
  message: string;
  createdAtUtc?: string;
  /** When provided, a refresh button re-runs the last turn (e.g. after a no-response error). */
  onRetry?: () => void;
}) {
  const timeHM = props.createdAtUtc
    ? formatUtcToLocalHM(props.createdAtUtc)
    : "";
  const timeFull =
    props.createdAtUtc && timeHM
      ? formatUtcToLocalFullDetail(props.createdAtUtc)
      : "";
  return (
    <div className="msg-system-stack">
      <div className={`msg msg-system msg-system-${props.level}`} role="alert">
        <div className="msg-system-label">System</div>
        <pre className="msg-system-body">{props.message}</pre>
      </div>
      <div className="msg-system-foot">
        <MessageCopyIconButton
          textToCopy={props.message}
          tooltip="Copy message"
          ariaLabel="Copy error message"
          dataTestId="system-message-copy"
        />
        {props.onRetry ? (
          <MessageRetryIconButton
            onRetry={props.onRetry}
            tooltip="Refresh"
            ariaLabel="Retry the last message"
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
}
