import { memo } from "react";

import { Markdown } from "../markdown/Markdown";
import { formatUtcToLocalFullDetail } from "./formatMessageTime";
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
  /** When the answer was written: the tooltip of the answer. An answer carries
   *  no copy control and no time under it; the row they made appeared as a turn
   *  ended and pushed what follows the answer down. */
  createdAtUtc?: string;
  artifacts?: ReadonlyMap<string, ToolArtifact>;
  onMentionArtifact?: (path: string) => void;
}) {
  const timeFull = props.createdAtUtc
    ? formatUtcToLocalFullDetail(props.createdAtUtc)
    : "";
  const contentTokens = props.artifacts
    ? groupAssistantArtifactTokens(
        tokenizeAssistantArtifacts(props.content, props.artifacts),
      )
    : [{ type: "markdown" as const, text: props.content }];
  return (
    <div className="msg-assistant-stack" data-row-id={props.rowId}>
      <div
        className="msg msg-assistant"
        {...(timeFull ? { title: timeFull } : {})}
      >
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
      </div>
    </div>
  );
});
