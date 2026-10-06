import { useEffect, useRef, useState } from "react";
import { isUnclaimedEscape } from "../nav/railEscape";
/** The dock beside the chat holds its background tasks. The edits and the
 * files open in windows of their own (changes/DiffViewerModal.tsx,
 * files/FilesView.tsx). One open state, and the focus given back to whatever
 * opened it; route and Escape handlers in the shell use this same close
 * operation. */
export function useRightDock(initialOpen = false) {
  const [open, setOpen] = useState(initialOpen);
  const previous = useRef(false);
  const returnFocus = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (open && !previous.current)
      returnFocus.current =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null;
    if (!open && previous.current && returnFocus.current?.isConnected)
      returnFocus.current.focus({ preventScroll: true });
    previous.current = open;
  }, [open]);
  return { open, setOpen };
}

/** Menus and dialogs claim Escape first. The shell enables this only while
 * the dock is the visible surface, then closes through the same route action. */
export function useRightDockEscape(open: boolean, onClose: () => void) {
  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (!isUnclaimedEscape(event)) return;
      event.preventDefault();
      event.stopPropagation();
      onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);
}
