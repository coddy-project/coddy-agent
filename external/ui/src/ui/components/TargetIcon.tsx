/**
 * The target that stands for the session goal: the composer's goal mark,
 * at the head of the goal popover and on the goal rows of the transcript.
 * Stroked with the current colour, 1em square unless the caller sizes it.
 */
export function TargetIcon(props: {
  className?: string;
  size?: number | string;
}) {
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
      <circle cx="8" cy="8" r="6.25" />
      <circle cx="8" cy="8" r="3.25" />
      <circle cx="8" cy="8" r="0.6" fill="currentColor" />
    </svg>
  );
}
