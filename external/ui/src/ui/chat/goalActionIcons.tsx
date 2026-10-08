import type { SVGProps } from "react";

/**
 * The glyphs of the goal menu's actions, drawn like the scheduler's toolbar:
 * 18px on a 24px grid, stroked or filled with the current colour.
 */

const common: SVGProps<SVGSVGElement> = {
  viewBox: "0 0 24 24",
  width: 18,
  height: 18,
  "aria-hidden": true,
};

const stroked: SVGProps<SVGSVGElement> = {
  ...common,
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 2,
  strokeLinecap: "round",
  strokeLinejoin: "round",
};

/** Resume: the turn starts again. */
export function GoalIconResume() {
  return (
    <svg {...common}>
      <path
        d="M8 5.5v13a1 1 0 001.5.86l10.5-6.5a1 1 0 000-1.72L9.5 4.64A1 1 0 008 5.5z"
        fill="currentColor"
      />
    </svg>
  );
}

export function GoalIconPause() {
  return (
    <svg {...common}>
      <rect x="6" y="5" width="4" height="14" rx="1" fill="currentColor" />
      <rect x="14" y="5" width="4" height="14" rx="1" fill="currentColor" />
    </svg>
  );
}

export function GoalIconEdit() {
  return (
    <svg {...stroked}>
      <path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
    </svg>
  );
}

export function GoalIconClear() {
  return (
    <svg {...stroked}>
      <path d="M3 6h18" />
      <path d="M8 6V4a2 2 0 012-2h4a2 2 0 012 2v2" />
      <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6h14z" />
      <path d="M10 11v6M14 11v6" />
    </svg>
  );
}
