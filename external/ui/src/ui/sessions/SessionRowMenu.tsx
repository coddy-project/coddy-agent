import { useEffect } from "react";
import { createPortal } from "react-dom";

/** One line of a row menu. */
export type SessionRowMenuItem = {
  key: string;
  label: string;
  testId: string;
  /** Drawn in the destructive colour, and separated from what is above it. */
  danger?: boolean;
  onPick: () => void;
};

/**
 * What can be done to one conversation, behind the row's ⋮.
 *
 * The row used to carry an icon per action, which cost the title a button's
 * width for each one and made a mis-click a delete. One control opens the list
 * instead, and the list has room for the words.
 *
 * It is rendered into the document rather than into the drawer: the drawer is
 * `overflow: hidden` and would cut the menu off at its edge.
 */
export function SessionRowMenu(props: {
  open: boolean;
  onClose: () => void;
  /** The ⋮ button's rectangle; the menu hangs under it. */
  anchor: DOMRect | null;
  items: SessionRowMenuItem[];
  ariaLabel: string;
}) {
  const { open, onClose } = props;

  useEffect(() => {
    if (!open) {
      return;
    }
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        ev.stopPropagation();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [open, onClose]);

  if (!open) {
    return null;
  }

  const anchor = props.anchor;
  // Below the button by default; above it when the row sits near the foot of
  // the window and the menu would be cut off there.
  const opensUp =
    !!anchor &&
    typeof window !== "undefined" &&
    anchor.bottom + 160 > window.innerHeight;

  const menu = (
    <>
      <button
        type="button"
        className="sessions-filter-backdrop"
        aria-hidden="true"
        tabIndex={-1}
        onMouseDown={(ev) => {
          ev.preventDefault();
          onClose();
        }}
      />
      <div
        className="session-row-menu"
        role="menu"
        aria-label={props.ariaLabel}
        style={
          anchor
            ? {
                right: Math.max(8, window.innerWidth - anchor.right),
                ...(opensUp
                  ? { bottom: window.innerHeight - anchor.top + 6 }
                  : { top: anchor.bottom + 6 }),
              }
            : undefined
        }
      >
        {props.items.map((item) => (
          <button
            key={item.key}
            type="button"
            className={`session-row-menu-item${item.danger ? " is-danger" : ""}`}
            role="menuitem"
            data-testid={item.testId}
            onClick={(ev) => {
              ev.preventDefault();
              ev.stopPropagation();
              item.onPick();
              onClose();
            }}
          >
            {item.label}
          </button>
        ))}
      </div>
    </>
  );

  return typeof document === "undefined"
    ? menu
    : createPortal(menu, document.body);
}
