import { useEffect, useRef, useState } from "react";
import { isUnclaimedEscape } from "../nav/railEscape";
/** The faces of the dock beside the chat: its background tasks, its edits.
 * The files open in a window of their own (files/FilesView.tsx). */
export type RightDockTab = "tasks" | "changes";

/** One open state and one active tab determine the dock's width and focus.
 * Route and Escape handlers in the shell use this same close operation. */
export function useRightDock(
  initialOpen = false,
  initialTab: RightDockTab = "tasks",
) {
  const [open, setOpen] = useState(initialOpen);
  const [tab, setTab] = useState<RightDockTab>(initialTab);
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
  return {
    open,
    setOpen,
    tab,
    setTab,
  };
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
