import type { SVGProps } from "react";

const common: SVGProps<SVGSVGElement> = {
  viewBox: "0 0 24 24",
  width: 18,
  height: 18,
  "aria-hidden": true,
};

export function SchedulerIconPlus(props: { className?: string }) {
  return (
    <svg {...common} className={props.className} fill="none">
      <path
        d="M12 5v14M5 12h14"
        stroke="currentColor"
        strokeWidth={2}
        strokeLinecap="round"
      />
    </svg>
  );
}

export function SchedulerIconPause(props: { className?: string }) {
  return (
    <svg {...common} className={props.className}>
      <rect x="6" y="5" width="4" height="14" rx="1" fill="currentColor" />
      <rect x="14" y="5" width="4" height="14" rx="1" fill="currentColor" />
    </svg>
  );
}

export function SchedulerIconPlay(props: { className?: string }) {
  return (
    <svg {...common} className={props.className}>
      <path d="M8 5v14l11-7L8 5z" fill="currentColor" />
    </svg>
  );
}

export function SchedulerIconTrash(props: { className?: string }) {
  return (
    <svg
      {...common}
      className={props.className}
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M3 6h18" />
      <path d="M8 6V4a2 2 0 012-2h4a2 2 0 012 2v2" />
      <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6h14z" />
      <path d="M10 11v6M14 11v6" />
    </svg>
  );
}
