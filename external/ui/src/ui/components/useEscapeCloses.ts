import { useContext, useEffect, useLayoutEffect, useRef } from "react";
import { PaneHidden } from "./PanePortal";

/**
 * Escape closes a menu, a popover or a dialog while it is open, before
 * anything under it hears the key.
 *
 * It is a capture listener on document that claims the key (preventDefault),
 * so the screen of the rail open under the layer stays for the next Escape
 * (nav/railEscape.ts): that screen has listened since before the layer opened,
 * and on the same node in the same phase the older listener runs first. An
 * Escape something nearer already claimed, and one that ends an input
 * method's composition, are left alone. `close` may take a step of the layer's
 * own instead of closing it (a dialog folds an inner row first).
 *
 * A layer in a hidden pane (PaneHidden) does not listen.
 */
export function useEscapeCloses(open: boolean, close: () => void): void {
  const closeRef = useRef(close);
  useLayoutEffect(() => {
    closeRef.current = close;
  });
  const hidden = useContext(PaneHidden);
  useEffect(() => {
    if (!open || hidden) {
      return undefined;
    }
    const onKey = (ev: KeyboardEvent) => {
      if (
        ev.key !== "Escape" ||
        ev.defaultPrevented ||
        ev.isComposing ||
        ev.keyCode === 229
      ) {
        return;
      }
      ev.preventDefault();
      closeRef.current();
    };
    document.addEventListener("keydown", onKey, true);
    return () => document.removeEventListener("keydown", onKey, true);
  }, [open, hidden]);
}
