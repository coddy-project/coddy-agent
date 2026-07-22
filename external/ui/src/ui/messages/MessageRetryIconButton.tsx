// MessageRetryIconButton is the circular-arrows "refresh" action shown next to
// the copy button on a failed/system message, letting the user re-run the last
// turn when the model did not respond. Reuses the copy button's styling.
export function MessageRetryIconButton(props: {
  onRetry: () => void;
  tooltip: string;
  ariaLabel: string;
  dataTestId: string;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      className="msg-copy-icon-btn"
      aria-label={props.ariaLabel}
      data-testid={props.dataTestId}
      disabled={props.disabled}
      title={props.disabled ? undefined : props.tooltip}
      onClick={() => props.onRetry()}
    >
      <svg
        className="msg-copy-icon-btn__glyph"
        width="16"
        height="16"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.9"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden
      >
        <path d="M21 2v6h-6" />
        <path d="M3 12a9 9 0 0 1 15-6.7L21 8" />
        <path d="M3 22v-6h6" />
        <path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
      </svg>
    </button>
  );
}
