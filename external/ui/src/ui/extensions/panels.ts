import type { ExtensionAPI } from "./runtime";

/** A surface of its own: a slide-over beside the chat, a full overlay, or a
 * drawer under the rail's screens. One panel of a kind is open at a time. */
export type Panel = {
  id: string;
  title: string;
  placement: "slide-over" | "full" | "drawer";
  mount: (element: HTMLElement, api: ExtensionAPI) => void | (() => void);
};
export type RuntimePanel = Panel & {
  key: string;
  owner: string;
  api: ExtensionAPI;
};

export function validatePanel(panel: Partial<Panel>) {
  if (
    !/^[a-z][a-z0-9-]{0,63}$/.test(panel.id ?? "") ||
    !panel.title?.trim() ||
    !["slide-over", "full", "drawer"].includes(panel.placement ?? "") ||
    typeof panel.mount !== "function"
  )
    throw new Error("A panel needs an id, title, placement and mount function");
}
