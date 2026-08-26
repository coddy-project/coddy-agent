import { useT } from "../i18n/I18nProvider";
import { stripCoddyAttachmentsForUserDisplay } from "../skills/stripCoddyAttachments";
import { segmentSlashKnownSpans } from "../skills/segmentComposerSlashSpans";
import {
  formatUtcToLocalFullDetail,
  formatUtcToLocalHM,
} from "./formatMessageTime";
import { MessageCopyIconButton } from "./MessageCopyIconButton";
import { fileTypeIcon } from "./fileTypeIcon";

function fmtBytes(
  n: number,
  t: (key: string, params?: Record<string, string | number>) => string,
): string {
  if (n < 1024) return t("composer.bytesB", { n });
  if (n < 1024 * 1024)
    return t("composer.bytesKB", { n: (n / 1024).toFixed(1) });
  return t("composer.bytesMB", { n: (n / (1024 * 1024)).toFixed(1) });
}

export function UserMessage(props: {
  content: string;
  createdAtUtc?: string;
  /** Known skill names — renders `/name` tokens as chip spans when the name is in the set. */
  knownSkillNames?: Set<string>;
  /** Called when the user clicks the Edit button. */
  onEdit?: (content: string) => void;
  /** Files attached to this message. `previewUrl` is a client-only blob URL (until reload). */
  files?: {
    name: string;
    mimeType: string;
    sizeBytes?: number;
    previewUrl?: string;
  }[];
}) {
  const { t } = useT();
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
            return (
              <span
                key={idx}
                className={`msg-user-file-chip${f.previewUrl ? " msg-user-file-chip--image" : ""}`}
                title={tip}
              >
                <span className="msg-user-file-chip-icon" aria-hidden="true">
                  {f.previewUrl ? (
                    <img
                      className="msg-user-file-thumb"
                      src={f.previewUrl}
                      alt=""
                      data-testid="msg-user-file-thumb"
                    />
                  ) : (
                    svg
                  )}
                </span>
                <span className="msg-user-file-chip-name">{f.name}</span>
              </span>
            );
          })}
        </div>
      ) : null}
      <div className="msg msg-user msg-user--editable">
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
                  <span key={i}>{seg.value}</span>
                ),
              )
            : display}
        </div>
        {props.onEdit ? (
          <button
            type="button"
            className="msg-user-edit"
            aria-label={t("messages.editMessage")}
            title={t("messages.editMessage")}
            data-testid="user-message-edit"
            onClick={() => props.onEdit!(props.content)}
          >
            ✎
          </button>
        ) : null}
      </div>
      <div className="msg-user-foot">
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
    </div>
  );
}
