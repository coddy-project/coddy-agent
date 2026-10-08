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
  /**
   * `up` is its twin on a touch screen, in the same place: the jump to the top
   * offered while the reader scrolls toward the start (issue #342).
   */
  direction?: "down" | "up";
}) {
  const { t } = useT();
  const up = props.direction === "up";
  const label = t(up ? "chat.scrollToTop" : "chat.scrollToBottom");
  return (
    <button
      type="button"
      className={[
        "chat-scroll-bottom",
        up ? "chat-scroll-bottom--up" : "",
        props.visible ? "is-visible" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      data-testid={up ? "chat-scroll-top" : "chat-scroll-bottom"}
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
          d={up ? "M8 13V4m0 0 4 4M8 4 4 8" : "M8 3v9m0 0 4-4m-4 4-4-4"}
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </button>
  );
}
