/**
 * The bell that stands for a wake: on a task card it says the running task will
 * wake the agent when it ends, or that the finished one did. Stroked with the
 * current colour, 1em square unless the caller sizes it.
 */
export function BellIcon(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      viewBox="0 0 16 16"
      width="1em"
      height="1em"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M4 11.5V7a4 4 0 0 1 8 0v4.5l1 1H3z" />
      <path d="M6.5 14a1.6 1.6 0 0 0 3 0" />
    </svg>
  );
}
