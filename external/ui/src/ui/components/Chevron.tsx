/**
 * The app's one chevron: the fold marker a transcript row opens with, pointing
 * right while closed and turning down when open. A dropdown indicator points
 * down while closed and up when open. Every disclosure, submenu and dropdown
 * uses it rather than a glyph of its own; the visual contract is DESIGN.md
 * (Chevron), held by chevronContract.test.tsx.
 *
 * It is drawn, not typed. As the text glyph "›" its ink sat wherever the
 * platform's font put it inside the line box - a different place on every
 * machine - and no amount of nudging `top` could centre it everywhere. The
 * polyline below mirrors about the centre of its viewBox on both axes, so the
 * ink is centred in the box by construction and the box is what the CSS places.
 */

/** Side of the square the chevron is drawn in, in CSS pixels and viewBox units. */
const SIZE = 14;

export function Chevron(props: {
  open?: boolean;
  pointing?: "right" | "down";
  className?: string;
}) {
  return (
    <span
      className={[
        "coddy-chevron",
        props.pointing === "down" ? "coddy-chevron--down" : "",
        props.open ? "is-open" : "",
        props.className || "",
      ]
        .filter(Boolean)
        .join(" ")}
      aria-hidden="true"
    >
      <svg
        viewBox={`0 0 ${SIZE} ${SIZE}`}
        width={SIZE}
        height={SIZE}
        fill="none"
        focusable="false"
      >
        {/* 1.5 units either side of (7, 7) and 3 above, 3 below: a 3x6 extent
            that, once the 1.6 round stroke is on it, renders 4.6x7.6 CSS pixels
            of ink - the size the 15px glyph it replaces drew. */}
        <polyline
          points="5.5,4 8.5,7 5.5,10"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </span>
  );
}
