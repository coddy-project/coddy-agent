import type { SVGProps } from "react";
import type { MarkupTool } from "./markupModel";

/**
 * The glyphs of the markup toolbar, drawn like the other toolbars of the
 * app: 20px on a 24px grid, stroked with the current colour. The console's
 * window draws the same shapes (external/cli/markup/frame.go).
 */

const stroked: SVGProps<SVGSVGElement> = {
  viewBox: "0 0 24 24",
  width: 20,
  height: 20,
  "aria-hidden": true,
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.8,
  strokeLinecap: "round",
  strokeLinejoin: "round",
};

function PenIcon() {
  return (
    <svg {...stroked}>
      <path d="M16.5 3.5l4 4L8 20H4v-4L16.5 3.5z" />
      <path d="M14 6l4 4" />
    </svg>
  );
}

function EllipseIcon() {
  return (
    <svg {...stroked}>
      <circle cx="12" cy="12" r="8" />
    </svg>
  );
}

function RectIcon() {
  return (
    <svg {...stroked}>
      <rect x="4" y="6" width="16" height="12" />
    </svg>
  );
}

function ArrowIcon() {
  return (
    <svg {...stroked}>
      <path d="M5 19L18 6" />
      <path d="M10 5h9v9" />
    </svg>
  );
}

function TextIcon() {
  return (
    <svg {...stroked}>
      <path d="M5 6V4.5h14V6" />
      <path d="M12 4.5V20" />
      <path d="M9 20h6" />
    </svg>
  );
}

function CropIcon() {
  return (
    <svg {...stroked}>
      <path d="M7 2v15h15" />
      <path d="M2 7h15v15" />
    </svg>
  );
}

export function RotateIcon() {
  return (
    <svg {...stroked}>
      <path d="M20 12a8 8 0 1 1-2.34-5.66" />
      <path d="M20 4v5h-5" />
    </svg>
  );
}

export function UndoIcon() {
  return (
    <svg {...stroked}>
      <path d="M9 14L4 9l5-5" />
      <path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11" />
    </svg>
  );
}

export function MarkupToolIcon({ tool }: { tool: MarkupTool }) {
  switch (tool) {
    case "pen":
      return <PenIcon />;
    case "ellipse":
      return <EllipseIcon />;
    case "rect":
      return <RectIcon />;
    case "arrow":
      return <ArrowIcon />;
    case "text":
      return <TextIcon />;
    case "crop":
      return <CropIcon />;
  }
}

/** The pencil on an attached picture's card that opens the editor. */
export function MarkupEditIcon() {
  return (
    <svg {...stroked} width={14} height={14} strokeWidth={2}>
      <path d="M16.5 3.5l4 4L8 20H4v-4L16.5 3.5z" />
    </svg>
  );
}
