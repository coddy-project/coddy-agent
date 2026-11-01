/**
 * Named anchor points of the host surface. The host keeps an empty box at
 * each anchor while its surface exists; `ui.anchor(name)` hands the box to an
 * extension and `ui.onAnchor` covers anchors of a surface that is not open
 * yet (a rail screen, the sessions toolbar).
 */
export const EXTENSION_ANCHORS = [
  "composer.actions",
  "composer.tabs",
  "chat.header",
  "nav.rail",
  "statusline",
  "sessions.toolbar",
  "settings.footer",
] as const;
export type ExtensionAnchorName = (typeof EXTENSION_ANCHORS)[number];

export function isExtensionAnchorName(
  name: string,
): name is ExtensionAnchorName {
  return (EXTENSION_ANCHORS as readonly string[]).includes(name);
}

type AnchorListener = (element: HTMLElement | null) => void;

export class AnchorRegistry {
  private elements = new Map<string, HTMLElement>();
  private listeners = new Map<string, Map<AnchorListener, AbortSignal>>();

  attach(name: string, element: HTMLElement) {
    if (!isExtensionAnchorName(name)) return;
    if (this.elements.get(name) === element) return;
    this.elements.set(name, element);
    this.emit(name, element);
  }
  detach(name: string, element: HTMLElement) {
    if (this.elements.get(name) !== element) return;
    this.elements.delete(name);
    this.emit(name, null);
  }
  get(name: string): HTMLElement | null {
    return this.elements.get(name) ?? null;
  }
  on(
    name: string,
    signal: AbortSignal,
    listener: AnchorListener,
  ): { remove: () => void } {
    if (!isExtensionAnchorName(name)) throw new Error("Unknown anchor");
    if (signal.aborted) throw new Error("Extension disposed");
    const key = name;
    const remove = () => {
      const map = this.listeners.get(key);
      if (!map?.has(listener)) return;
      map.delete(listener);
      if (!map.size) this.listeners.delete(key);
      signal.removeEventListener("abort", remove);
    };
    let map = this.listeners.get(key);
    if (!map) this.listeners.set(key, (map = new Map()));
    map.set(listener, signal);
    signal.addEventListener("abort", remove, { once: true });
    const current = this.get(name);
    if (current) {
      try {
        listener(current);
      } catch {
        remove();
      }
    }
    return { remove };
  }
  private emit(name: string, element: HTMLElement | null) {
    for (const [listener, signal] of [...(this.listeners.get(name) ?? [])]) {
      if (signal.aborted) continue;
      try {
        listener(element);
      } catch {
        /* A listener that throws loses its registration. */
        this.listeners.get(name)?.delete(listener);
      }
    }
  }
}
