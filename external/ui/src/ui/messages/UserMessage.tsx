import { memo, useState } from "react";

import { useT } from "../i18n/I18nProvider";
import { ImageLightbox } from "../components/ImageLightbox";
import { stripCoddyAttachmentsForUserDisplay } from "../skills/stripCoddyAttachments";
import { segmentSlashKnownSpans } from "../skills/segmentComposerSlashSpans";
import {
  formatUtcToLocalFullDetail,
  formatUtcToLocalHM,
} from "./formatMessageTime";
import { MessageCopyIconButton } from "./MessageCopyIconButton";
import { fileTypeIcon } from "./fileTypeIcon";
import { splitDocMentions } from "../docs/docMentions";
import { appNavHrefDocs } from "../scheduler/hashRoute";

/** Prose of a sent message with its **`@coddy:`** mentions as links to the reader. */
function withDocMentions(text: string, keyPrefix: string) {
  return splitDocMentions(text).map((part, i) => {
    if (part.type === "text") {
      return <span key={`${keyPrefix}-${i}`}>{part.value}</span>;
    }
    const cut = part.ref.indexOf("#");
    const href =
      cut < 0 ? appNavHrefDocs(part.ref) : appNavHrefDocs(part.ref.slice(0, cut), part.ref.slice(cut + 1));
    return (
      <a key={`${keyPrefix}-${i}`} className="coddy-doc-mention" href={href}>
        {part.literal}
      </a>
    );
  });
}

function fmtBytes(
  n: number,
  t: (key: string, params?: Record<string, string | number>) => string,
): string {
  if (n < 1024) return t("composer.bytesB", { n });
  if (n < 1024 * 1024)
    return t("composer.bytesKB", { n: (n / 1024).toFixed(1) });
  return t("composer.bytesMB", { n: (n / (1024 * 1024)).toFixed(1) });
}

export const UserMessage = memo(function UserMessage(props: {
  content: string;
  createdAtUtc?: string;
  /** Known skill names — renders `/name` tokens as chip spans when the name is in the set. */
  knownSkillNames?: Set<string>;
  /** Called when the user clicks the Edit button. */
  onEdit?: (content: string, userMsgIndex: number) => void;
  /** Index of this message among user messages; passed back to onEdit. */
  userMsgIndex?: number;
  /**
   * Files attached to this message. `previewUrl` is the bounded thumbnail (a
   * client-only blob URL until the server snapshot arrives); `url` is the
   * full-size asset a preview card opens enlarged, absent on a message sent
   * before that route existed and on an asset no longer in the bundle.
   */
  files?: {
    name: string;
    mimeType: string;
    sizeBytes?: number;
    previewUrl?: string;
    url?: string;
  }[];
}) {
  const { t } = useT();
  // The attachment opened over the page, if any: one viewer per message.
  const [lightbox, setLightbox] = useState<{ src: string; alt: string } | null>(
    null,
  );
  const display = stripCoddyAttachmentsForUserDisplay(props.content);
  const timeHM = props.createdAtUtc
    ? formatUtcToLocalHM(props.createdAtUtc)
    : "";
  const timeFull =
    props.createdAtUtc && timeHM
      ? formatUtcToLocalFullDetail(props.createdAtUtc)
      : "";
  const bodySegments =
    props.knownSkillNames && props.knownSkillNames.size > 0
      ? segmentSlashKnownSpans(display, props.knownSkillNames)
      : null;

  return (
    <div className="msg-user-stack">
      {props.files && props.files.length > 0 ? (
        <div
          className="msg-user-files"
          aria-label={t("messages.attachedFiles")}
        >
          {props.files.map((f, idx) => {
            const { svg, label } = fileTypeIcon(f.mimeType, f.name);
            const tip =
              f.sizeBytes != null
                ? `${f.name}\n${label} · ${fmtBytes(f.sizeBytes, t)}`
                : `${f.name}\n${label}`;
            // The card shows the bounded thumbnail and opens the original;
            // a message that predates the full-size route opens its preview
            // rather than losing the click.
            const thumbSrc = f.previewUrl || f.url;
            const fullSrc = f.url || f.previewUrl;
            if (thumbSrc && fullSrc) {
              return (
                <span
                  key={idx}
                  className="msg-user-file-chip msg-user-file-chip--image msg-user-file-card"
                  title={tip}
                >
                  <button
                    type="button"
                    className="msg-user-file-card-open"
                    aria-label={t("messages.openAttachmentImage", {
                      fileName: f.name,
                    })}
                    data-testid="msg-user-file-open"
                    onClick={() => setLightbox({ src: fullSrc, alt: f.name })}
                  >
                    <img
                      className="msg-user-file-thumb"
                      src={thumbSrc}
                      alt=""
                      data-testid="msg-user-file-thumb"
                    />
                  </button>
                </span>
              );
            }
            return (
              <span key={idx} className="msg-user-file-chip" title={tip}>
                <span className="msg-user-file-chip-icon" aria-hidden="true">
                  {svg}
                </span>
                <span className="msg-user-file-chip-name">{f.name}</span>
              </span>
            );
          })}
        </div>
      ) : null}
      <div className="msg msg-user">
        <div className="msg-user-body" data-testid="user-message-body">
          {bodySegments
            ? bodySegments.map((seg, i) =>
                seg.type === "slash" ? (
                  <span
                    key={i}
                    className="coddy-skill-chip"
                    data-testid="coddy-skill-span"
                    data-skill-name={seg.name}
                  >
                    {seg.literal}
                  </span>
                ) : (
                  <span key={i}>{withDocMentions(seg.value, String(i))}</span>
                ),
              )
            : withDocMentions(display, "b")}
        </div>
      </div>
      <div className="msg-user-foot">
        {props.onEdit ? (
          <button
            type="button"
            className="msg-copy-icon-btn msg-user-edit"
            aria-label={t("messages.editMessage")}
            title={t("messages.editMessage")}
            data-testid="user-message-edit"
            onClick={() => props.onEdit!(props.content, props.userMsgIndex ?? 0)}
          >
            <svg
              className="msg-copy-icon-btn__glyph"
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden
            >
              <path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z" />
            </svg>
          </button>
        ) : null}
        <MessageCopyIconButton
          textToCopy={display}
          tooltip={t("messages.copyMessage")}
          ariaLabel={t("messages.copyMessage")}
          dataTestId="user-message-copy"
        />
        {timeHM ? (
          <time
            className="msg-user-time"
            dateTime={props.createdAtUtc}
            title={timeFull || undefined}
          >
            {timeHM}
          </time>
        ) : null}
      </div>
      {lightbox ? (
        <ImageLightbox
          src={lightbox.src}
          alt={lightbox.alt}
          onClose={() => setLightbox(null)}
        />
      ) : null}
    </div>
  );
});
