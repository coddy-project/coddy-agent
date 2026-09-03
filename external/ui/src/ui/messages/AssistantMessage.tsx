import { Markdown } from "../markdown/Markdown";
import { useT } from "../i18n/I18nProvider";
import {
  formatUtcToLocalFullDetail,
  formatUtcToLocalHM,
} from "./formatMessageTime";
import { MessageCopyIconButton } from "./MessageCopyIconButton";

export function AssistantMessage(props: {
  content: string;
  streaming?: boolean;
  createdAtUtc?: string;
}) {
  const { t } = useT();
  const showFoot =
    !props.streaming &&
    (props.content.trim() !== "" || Boolean(props.createdAtUtc));
  const timeHM = props.createdAtUtc
    ? formatUtcToLocalHM(props.createdAtUtc)
    : "";
  const timeFull =
    props.createdAtUtc && timeHM
      ? formatUtcToLocalFullDetail(props.createdAtUtc)
      : "";
  return (
    <div className="msg-assistant-stack">
      <div className="msg msg-assistant">
        <Markdown text={props.content} />
        {showFoot ? (
          <div className="msg-assistant-foot">
            <MessageCopyIconButton
              textToCopy={props.content}
              tooltip={t("messages.copyMessage")}
              ariaLabel={t("messages.copyMessage")}
              dataTestId="assistant-message-copy"
            />
            {timeHM ? (
              <time
                className="msg-assistant-time"
                dateTime={props.createdAtUtc}
                title={timeFull || undefined}
              >
                {timeHM}
              </time>
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}
