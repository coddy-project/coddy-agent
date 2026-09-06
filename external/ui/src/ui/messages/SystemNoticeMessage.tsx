import { memo } from "react";

import {
  formatUtcToLocalFullDetail,
  formatUtcToLocalHM,
} from "./formatMessageTime";
import { useT } from "../i18n/I18nProvider";
import { MessageCopyIconButton } from "./MessageCopyIconButton";
import { MessageRetryIconButton } from "./MessageRetryIconButton";

export const SystemNoticeMessage = memo(function SystemNoticeMessage(props: {
  /** error: a failed request or turn; notice: information the operator should see once. */
  level: "error" | "notice";
  message: string;
  createdAtUtc?: string;
  /** When provided, a refresh button re-runs the last turn (e.g. after a no-response error). */
  onRetry?: () => void;
}) {
  const { t } = useT();
  const timeHM = props.createdAtUtc
    ? formatUtcToLocalHM(props.createdAtUtc)
    : "";
  const timeFull =
    props.createdAtUtc && timeHM
      ? formatUtcToLocalFullDetail(props.createdAtUtc)
      : "";
  return (
    <div className={`msg-system-stack msg-system-stack-${props.level}`}>
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
