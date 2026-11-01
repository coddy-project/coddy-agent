import type { ExtensionAPI } from "./runtime";

/**
 * The surfaces above the chat: a modal, a free-floating overlay and the
 * shell takeover. A modal is one at a time and answers Escape; overlays are
 * boxes the app never dims - a pet, a toast - and several may be open; the
 * shell takeover stops the host drawing its own UI and hands the mount the
 * window, until the handle releases it or the extension goes away.
 */
export type SurfaceMount = (
  element: HTMLElement,
  api: ExtensionAPI,
) => void | (() => void);

export type Modal = {
  id: string;
  title: string;
  mount: SurfaceMount;
};
export type OverlayPosition = Readonly<{
  top?: number;
  right?: number;
  bottom?: number;
  left?: number;
}>;
export type Overlay = {
  id: string;
  mount: SurfaceMount;
  position?: OverlayPosition;
};
export type Shell = {
  id: string;
  mount: SurfaceMount;
};

export type RuntimeModal = Modal & {
  key: string;
  owner: string;
  api: ExtensionAPI;
};
export type RuntimeOverlay = Overlay & {
  key: string;
  owner: string;
  api: ExtensionAPI;
};
export type RuntimeShell = Shell & {
  key: string;
  owner: string;
  api: ExtensionAPI;
};

export function validateModal(modal: Partial<Modal>) {
  if (
    !/^[a-z][a-z0-9-]{0,63}$/.test(modal.id ?? "") ||
    !modal.title?.trim() ||
    typeof modal.mount !== "function"
  )
    throw new Error("A modal needs an id, title and mount function");
}
export function validateOverlay(overlay: Partial<Overlay>) {
  if (
    !/^[a-z][a-z0-9-]{0,63}$/.test(overlay.id ?? "") ||
    typeof overlay.mount !== "function" ||
    (overlay.position !== undefined &&
      (typeof overlay.position !== "object" ||
        overlay.position === null ||
        Object.values(overlay.position).some(
          (v) => typeof v !== "number" || !Number.isFinite(v),
        )))
  )
    throw new Error("An overlay needs an id, a mount and numeric positions");
}
export function validateShell(shell: Partial<Shell>) {
  if (
    !/^[a-z][a-z0-9-]{0,63}$/.test(shell.id ?? "") ||
    typeof shell.mount !== "function"
  )
    throw new Error("A shell takeover needs an id and a mount function");
}
