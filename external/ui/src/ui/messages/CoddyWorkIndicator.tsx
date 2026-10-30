/** The unplated logo: its outer contour sweeps around the still inner ring. */
export function CoddyWorkIndicator() {
  return (
    <svg
      className="coddy-work-indicator"
      viewBox="-15 -15 30 30"
      width="14"
      height="14"
      fill="none"
      stroke="currentColor"
      strokeLinecap="round"
      aria-hidden="true"
      focusable="false"
    >
      <circle
        className="coddy-work-indicator-c"
        r="10.25"
        strokeWidth="2.85"
        pathLength="100"
        strokeDasharray="75.5 24.5"
        strokeDashoffset="12.3"
        transform="rotate(90)"
      />
      <circle className="coddy-work-indicator-ring" r="6" strokeWidth="2" />
    </svg>
  );
}
