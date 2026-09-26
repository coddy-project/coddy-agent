/**
 * The paperclip that stands for attached files: on the composer's attach
 * button and on a queued message that carries images. Stroked with the
 * current colour, 1em square unless the caller sizes it.
 */
export function PaperclipIcon(props: { className?: string; size?: number | string }) {
  const size = props.size ?? "1em";
  return (
    <svg
      className={props.className}
      viewBox="0 0 16 16"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      aria-hidden="true"
      focusable="false"
    >
      <path
        d="M13.5 7.5l-6 6A4 4 0 012 8l7-7a2.5 2.5 0 013.5 3.5l-6 6A1 1 0 015 9l5-5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
