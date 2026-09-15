import { useT } from "../i18n/I18nProvider";

/**
 * Stands in for the composer on an archived conversation.
 *
 * Archiving is the operator saying "not now". Leaving the composer there would
 * invite a prompt that silently pulls the conversation back into the working
 * list, so the slot says what the state is and offers the one action that
 * changes it. Nothing is refused on the server: the archive is a shelf, not a
 * lock, and taking it off the shelf is one click away.
 */
export function ArchivedSessionNotice(props: {
  onUnarchive: () => void;
  /** True while the request is in flight, so the button cannot be pressed twice. */
  busy?: boolean;
}) {
  const { t } = useT();
  return (
    <div
      className="archived-session-notice"
      role="note"
      data-testid="archived-session-notice"
    >
      <span className="archived-session-text">{t("chat.archived.notice")}</span>
      <button
        type="button"
        className="archived-session-action"
        data-testid="archived-session-unarchive"
        disabled={!!props.busy}
        onClick={props.onUnarchive}
      >
        {t("chat.archived.unarchive")}
      </button>
    </div>
  );
}
