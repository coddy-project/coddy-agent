import { useT } from "../i18n/I18nProvider";

/**
 * Takes the reader back to the newest message after they scrolled up. It lives
 * in the composer's column (`.chat-bottom-inner`) rather than against the
 * viewport, so it rides with the composer on every shell: see DESIGN.md,
 * Transcript scroll-to-bottom button.
 *
 * It stays mounted for the whole chat and crosses between its two states with
 * a transition, because an unmounted node cannot animate its way out.
 */
export function ScrollToBottomButton(props: {
  visible: boolean;
  onClick: () => void;
}) {
  const { t } = useT();
  const label = t("chat.scrollToBottom");
  return (
    <button
      type="button"
      className={
        props.visible ? "chat-scroll-bottom is-visible" : "chat-scroll-bottom"
      }
      data-testid="chat-scroll-bottom"
      data-visible={props.visible ? "true" : "false"}
      aria-label={label}
      aria-hidden={props.visible ? undefined : true}
      inert={!props.visible}
      tabIndex={props.visible ? undefined : -1}
      title={label}
      onClick={(e) => {
        // The button is about to be hidden; focus must not stay behind on it.
        e.currentTarget.blur();
        props.onClick();
      }}
    >
      <svg
        width="16"
        height="16"
        viewBox="0 0 16 16"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
        aria-hidden
      >
        <path
          d="M8 3v9m0 0 4-4m-4 4-4-4"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </button>
  );
}
