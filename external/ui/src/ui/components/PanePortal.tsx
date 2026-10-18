import { createContext, useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";

/**
 * True in a pane that is mounted but hidden under another view (Settings keeps
 * its tab under the search results). A layer in it is deaf: it draws nothing,
 * claims no Escape and keeps its state. Only PanePortal and useEscapeCloses
 * read it; a portal or an Escape listener outside them is not covered.
 */
export const PaneHidden = createContext(false);

// A portal for a layer in a pane; nothing while the pane is hidden, since a
// portal escapes the pane's `display: none`.
export function PanePortal(props: { children: ReactNode }) {
  const hidden = useContext(PaneHidden);
  return hidden ? null : createPortal(props.children, document.body);
}
