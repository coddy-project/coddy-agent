import { stripCoddyAttachmentsForUserDisplay } from "../skills/stripCoddyAttachments";
import { segmentSlashKnownSpans } from "../skills/segmentComposerSlashSpans";
import {
  formatUtcToLocalFullDetail,
  formatUtcToLocalHM,
} from "./formatMessageTime";
import { MessageCopyIconButton } from "./MessageCopyIconButton";
import { fileTypeIcon } from "./fileTypeIcon";

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

export function UserMessage(props: {
  content: string;
  createdAtUtc?: string;
  /** Known skill names — renders `/name` tokens as chip spans when the name is in the set. */
  knownSkillNames?: Set<string>;
  /** Called when the user clicks the Edit button. */
  onEdit?: (content: string) => void;
  /** Files attached to this message. */
  files?: { name: string; mimeType: string; sizeBytes?: number }[];
}) {
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
        <div className="msg-user-files" aria-label="Attached files">
          {props.files.map((f, idx) => {
            const { svg, label } = fileTypeIcon(f.mimeType, f.name);
            const tip = f.sizeBytes != null
              ? `${f.name}\n${label} · ${fmtBytes(f.sizeBytes)}`
              : `${f.name}\n${label}`;
            return (
              <span key={idx} className="msg-user-file-chip" title={tip}>
                <span className="msg-user-file-chip-icon" aria-hidden="true">{svg}</span>
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
            aria-label="Edit message"
            title="Edit message"
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
          tooltip="Copy message"
          ariaLabel="Copy message"
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
