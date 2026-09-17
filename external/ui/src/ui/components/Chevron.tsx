/**
 * The app's one chevron: the "›" the transcript rows fold with, pointing right while
 * closed and turning down when open. A dropdown indicator points down while closed
 * and up when open. Every disclosure, submenu and dropdown uses it rather than a
 * glyph of its own; the visual contract is DESIGN.md (Chevron).
 */
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
    />
  );
}
