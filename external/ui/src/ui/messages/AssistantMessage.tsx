import { memo, useState } from "react";
import { ApiImage, ApiImageLightbox } from "../components/ApiImage";
import { useT } from "../i18n/I18nProvider";
import type { TranscriptFile } from "../chat/types";

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
  files?: readonly TranscriptFile[];
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
  const { t } = useT();
  const [open, setOpen] = useState<TranscriptFile | null>(null);
  const images = (props.files || []).filter(
    (f) =>
      f.mimeType.toLowerCase().startsWith("image/") && (f.url || f.previewUrl),
  );
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
      <h2 className="sr-only">{t("messages.assistantHeading")}</h2>
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
        {images.map((file, index) => (
          <button
            key={`${index}:${file.url || file.previewUrl}`}
            type="button"
            className="msg-generated-image"
            aria-label={t("messages.openGeneratedImage", {
              fileName: file.name,
            })}
            onClick={() => setOpen(file)}
          >
            <ApiImage
              className="msg-generated-image-content"
              src={(file.url || file.previewUrl)!}
              alt={file.name}
              data-testid="generated-image"
            />
          </button>
        ))}
        {open ? (
          <ApiImageLightbox
            src={(open.url || open.previewUrl)!}
            alt={open.name}
            onClose={() => setOpen(null)}
          />
        ) : null}
      </div>
    </div>
  );
});
