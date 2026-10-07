import { memo } from "react";

import { Markdown } from "../markdown/Markdown";
import { useT } from "../i18n/I18nProvider";
import {
  formatUtcToLocalFullDetail,
  formatUtcToLocalHM,
} from "./formatMessageTime";
import { MessageCopyIconButton } from "./MessageCopyIconButton";
import { ToolArtifactCards } from "./ToolArtifactCards";
import {
  groupAssistantArtifactTokens,
  tokenizeAssistantArtifacts,
} from "../chat/inlineArtifacts";
import type { ToolArtifact } from "../chat/toolArtifacts";

export const AssistantMessage = memo(function AssistantMessage(props: {
  content: string;
  /** The transcript row id, stamped on the row so the transcript window can
   *  find it on screen. */
  rowId?: string;
  streaming?: boolean;
  createdAtUtc?: string;
  /** Only the answer that hands the turn back carries the action row. The answers a
   *  turn leaves behind between tool calls would stack the same copy button and the
   *  same minute over and over, which reads as chrome rather than as information. */
  showFoot?: boolean;
  artifacts?: ReadonlyMap<string, ToolArtifact>;
  onMentionArtifact?: (path: string) => void;
}) {
  const { t } = useT();
  const showFoot =
    props.showFoot !== false &&
    !props.streaming &&
    (props.content.trim() !== "" || Boolean(props.createdAtUtc));
  const timeHM = props.createdAtUtc
    ? formatUtcToLocalHM(props.createdAtUtc)
    : "";
  const timeFull =
    props.createdAtUtc && timeHM
      ? formatUtcToLocalFullDetail(props.createdAtUtc)
      : "";
  const contentTokens = props.artifacts
    ? groupAssistantArtifactTokens(
        tokenizeAssistantArtifacts(props.content, props.artifacts),
      )
    : [{ type: "markdown" as const, text: props.content }];
  return (
    <div className="msg-assistant-stack" data-row-id={props.rowId}>
      <div className="msg msg-assistant">
        {contentTokens.map((token, index) =>
          token.type === "artifacts" ? (
            <ToolArtifactCards
              key={`${token.artifacts.map((artifact) => artifact.id).join("-")}-${index}`}
              artifacts={token.artifacts}
              inline
              {...(props.onMentionArtifact
                ? { onMention: props.onMentionArtifact }
                : {})}
            />
          ) : token.text ? (
            <Markdown
              key={index}
              text={token.text}
              {...(props.streaming ? { streaming: true } : {})}
            />
          ) : null,
        )}
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
});
