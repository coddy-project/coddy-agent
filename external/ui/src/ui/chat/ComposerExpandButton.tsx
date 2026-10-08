import { useT } from "../i18n/I18nProvider";

/**
 * Expands the docked composer over the chat for a long prompt, and folds it
 * back (issue #342). A circle of the scroll-to-bottom button's size and glass,
 * right over the composer's right edge, with the jumps of the transcript
 * standing above it - so the two read as a pair of different controls: the
 * jump is an arrow, this one arrows that spread to the corners (or come back
 * together, expanded). It never moves. See DESIGN.md, Composer field height
 * and expand.
 */
export function ComposerExpandButton(props: {
  expanded: boolean;
  onToggle: () => void;
}) {
  const { t } = useT();
  const label = t(props.expanded ? "composer.collapse" : "composer.expand");
  return (
    <button
      type="button"
      className={
        props.expanded
          ? "chat-expand-composer is-expanded"
          : "chat-expand-composer"
      }
      data-testid="composer-expand"
      aria-pressed={props.expanded}
      aria-label={label}
      title={label}
      // The caret stays in the field, where the reader was writing, and a
      // phone keeps its keyboard open.
      onMouseDown={(e) => e.preventDefault()}
      onClick={props.onToggle}
    >
      <svg
        width="16"
        height="16"
        viewBox="0 0 16 16"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
        aria-hidden
      >
        {props.expanded ? (
          <path
            d="M2.5 9.5h4v4M13.5 6.5h-4v-4M6.5 9.5l-4 4M9.5 6.5l4-4"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        ) : (
          <path
            d="M9.5 2.5h4v4M6.5 13.5h-4v-4M13.5 2.5l-4 4M2.5 13.5l4-4"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        )}
      </svg>
    </button>
  );
}
