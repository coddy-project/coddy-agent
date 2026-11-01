import type { ExtensionAPI } from "./runtime";
import type { ContributionHandle, Owned, ToolView } from "./contributions";

/**
 * Controls in the composer: an extension mounts buttons and small widgets
 * into `actions` (the composer's action row) or `tabs` (the strip of picks),
 * and alter passes rewrite the assembled list before it renders - the
 * declare-then-alter pipeline of the specification, over these controls
 * first. A control descriptor is plain data: an alter may retitle, reorder,
 * hide or drop any entry.
 */
export type ComposerControl = {
  id: string;
  placement: "actions" | "tabs";
  title?: string;
  /** Lower sorts first; default is registration order. */
  order?: number;
  /** An alter may hide an entry without unregistering it. */
  visible?: boolean;
  mount: (
    element: HTMLElement,
    api: ExtensionAPI,
  ) => void | (() => void) | ToolView;
};
export type ComposerControlDescriptor = Readonly<
  Omit<ComposerControl, "mount">
>;
export type ComposerAlterFn = (
  controls: readonly ComposerControlDescriptor[],
  context: { surface: "composer" },
) => readonly ComposerControlDescriptor[];
export type ComposerAlter = {
  id: string;
  run: ComposerAlterFn;
};
export type ComposerControlsAPI = {
  register: (control: ComposerControl) => ContributionHandle<ComposerControl>;
  /** Rewrites the assembled control list before it renders; later passes see
   * the earlier ones' answer. Requires the `ui.alter` manifest scope. */
  alter: (pass: ComposerAlterFn) => { remove: () => void };
};

export function validateComposerControl(control: ComposerControl) {
  if (
    !/^[a-z][a-z0-9-]{0,63}$/.test(control.id ?? "") ||
    !["actions", "tabs"].includes(control.placement ?? "") ||
    typeof control.mount !== "function" ||
    (control.order !== undefined && !Number.isFinite(control.order)) ||
    (control.visible !== undefined && typeof control.visible !== "boolean")
  )
    throw new Error("A composer control needs an id, placement and mount");
}

export function validateComposerAlter(pass: ComposerAlterFn) {
  if (typeof pass !== "function") throw new Error("An alter needs a function");
}

type Placed = { key: string; box: HTMLElement; cleanup: () => void };

/** The boxes a placement renders into. The assembled list runs the alter
 * passes in registration order; what survives mounts in order, and a change
 * updates the boxes in place like the hook slots do. */
export class ComposerControlHost {
  private boxes = new Map<string, Set<HTMLElement>>();
  private placed = new Map<HTMLElement, Placed[]>();
  constructor(
    private rows: () => Owned<ComposerControl>[],
    private alters: () => readonly Owned<ComposerAlter>[],
    private fail: (key: string, owner: string, error: unknown) => void,
  ) {}

  attach(
    placement: string,
    element: HTMLElement,
    signal: AbortSignal,
  ): { remove: () => void } {
    if (signal.aborted) throw new Error("Extension disposed");
    let set = this.boxes.get(placement);
    if (!set) this.boxes.set(placement, (set = new Set()));
    set.add(element);
    this.render(placement, element);
    const remove = () => {
      if (!set!.delete(element)) return;
      signal.removeEventListener("abort", remove);
      this.unmount(element, this.placed.get(element) ?? []);
      this.placed.delete(element);
      if (!set!.size) this.boxes.delete(placement);
    };
    signal.addEventListener("abort", remove, { once: true });
    return { remove };
  }

  refresh() {
    for (const [placement, set] of this.boxes)
      for (const element of [...set]) this.render(placement, element);
  }

  /** The assembled, altered descriptor list - what an alter sees. */
  descriptors(placement?: string): ComposerControlDescriptor[] {
    const rows = this.rows().filter(
      (row) => !placement || row.placement === placement,
    );
    let list: readonly ComposerControlDescriptor[] = rows.map((row) =>
      Object.freeze({
        id: row.id,
        placement: row.placement,
        ...(row.title !== undefined ? { title: row.title } : {}),
        ...(row.order !== undefined ? { order: row.order } : {}),
        ...(row.visible !== undefined ? { visible: row.visible } : {}),
      }),
    );
    for (const alter of this.alters()) {
      try {
        const next = alter.run(list, { surface: "composer" });
        if (Array.isArray(next)) list = next;
      } catch (error) {
        this.fail(alter.key, alter.owner, error);
      }
    }
    // An `order` sorts before the entries without one; the rest keep the
    // order the alters left them in (the sort is stable).
    return [...list].sort(
      (a, b) =>
        (a.order ?? Number.MAX_SAFE_INTEGER) -
        (b.order ?? Number.MAX_SAFE_INTEGER),
    );
  }

  private render(placement: string, element: HTMLElement) {
    const kept = this.placed.get(element) ?? [];
    const byKey = new Map(kept.map((entry) => [entry.key, entry]));
    const visible = this.descriptors(placement).filter(
      (descriptor) => descriptor.visible !== false,
    );
    const rows = this.rows();
    const next: Placed[] = [];
    for (const descriptor of visible) {
      const row = rows.find(
        (candidate) =>
          candidate.placement === placement && candidate.id === descriptor.id,
      );
      if (!row) continue;
      const existing = byKey.get(row.key);
      if (existing) {
        byKey.delete(row.key);
        element.append(existing.box);
        next.push(existing);
        continue;
      }
      const box = element.ownerDocument.createElement("span");
      box.className = "extension-composer-control";
      box.dataset.extensionRenderer = row.key;
      if (descriptor.title) box.title = descriptor.title;
      element.append(box);
      let cleanup = () => {};
      try {
        const result = row.mount(box, row.api);
        if (typeof result === "function") cleanup = result;
        else if (result && typeof result === "object" && !("then" in result)) {
          const view: ToolView = result;
          cleanup = () => view.dispose?.();
        } else if (result !== undefined) {
          Promise.resolve(result).catch(() => {});
          throw new Error("Control mount must be synchronous");
        }
      } catch (error) {
        this.fail(row.key, row.owner, error);
        box.remove();
        continue;
      }
      next.push({ key: row.key, box, cleanup: () => cleanup() });
    }
    for (const entry of byKey.values()) {
      entry.box.remove();
      try {
        entry.cleanup();
      } catch (error) {
        this.fail(entry.key, entry.key.split("/")[0]!, error);
      }
    }
    this.placed.set(element, next);
    // The order an alter chose is the order the boxes stand in.
    element.replaceChildren(...next.map((entry) => entry.box));
  }

  private unmount(element: HTMLElement, entries: Placed[]) {
    for (const entry of entries) {
      try {
        entry.cleanup();
      } catch (error) {
        this.fail(entry.key, entry.key.split("/")[0]!, error);
      }
    }
    element.replaceChildren();
  }
}
