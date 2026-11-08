import type { ExtensionAPI } from "./runtime";
import type { ContributionHandle, Owned, ToolView } from "./contributions";

/**
 * Controls in the composer: an extension mounts buttons and small widgets
 * into `actions` (the composer's action row) or `tabs` (the strip of picks),
 * and alter passes rewrite the assembled list before it renders - the
 * declare-then-alter pipeline of the specification, over host and extension
 * controls alike. A control descriptor is plain data: an alter may retitle,
 * reorder, hide or drop any entry.
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
  Omit<ComposerControl, "mount"> & {
    /** Stable identity: `host/<control>` or `<extension>/<control>`. The local
     * `id` stays for compatibility with existing extension alter passes. */
    key?: string;
  }
>;
export type ComposerHostControlDescriptor = ComposerControlDescriptor & {
  key: string;
};
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
type Attached = { placement: string; controlKey: string | undefined };

/** The boxes a placement renders into. The assembled host and extension list
 * runs the alter passes in registration order. Host entries stay plain data
 * for the React surface; extension entries mount into the attached boxes. */
export class ComposerControlHost {
  private boxes = new Map<HTMLElement, Attached>();
  private placed = new Map<HTMLElement, Placed[]>();
  private hostRows: readonly ComposerHostControlDescriptor[] = [];
  private hostOwner: object | null = null;
  private listeners = new Set<() => void>();
  private version = 0;

  constructor(
    private rows: () => Owned<ComposerControl>[],
    private alters: () => readonly Owned<ComposerAlter>[],
    private fail: (key: string, owner: string, error: unknown) => void,
  ) {}

  /** React subscribes here so a registration or alter updates the mixed list,
   * not only the DOM boxes that happened to be attached at the time. */
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
  getSnapshot = () => this.version;

  attach(
    placement: string,
    element: HTMLElement,
    signal: AbortSignal,
    controlKey?: string,
  ): { remove: () => void } {
    if (signal.aborted) throw new Error("Extension disposed");
    this.boxes.set(element, { placement, controlKey });
    this.render(element);
    const remove = () => {
      if (!this.boxes.delete(element)) return;
      signal.removeEventListener("abort", remove);
      this.unmount(element, this.placed.get(element) ?? []);
      this.placed.delete(element);
    };
    signal.addEventListener("abort", remove, { once: true });
    return { remove };
  }

  refresh() {
    for (const element of [...this.boxes.keys()]) this.render(element);
    this.version++;
    for (const listener of [...this.listeners]) listener();
  }

  /** Declares the controls of one mounted composer. React renders these rows
   * itself; declaring them here lets the same alters govern DOM-mounted rows.
   * An old composer unmounting cannot clear a newer composer's declaration. */
  declareHostControls(
    controls: readonly ComposerHostControlDescriptor[],
    owner: object,
  ) {
    if (this.hostOwner === owner && sameDescriptors(this.hostRows, controls)) {
      return;
    }
    this.hostOwner = owner;
    this.hostRows = controls.map((row) => Object.freeze({ ...row }));
    for (const element of [...this.boxes.keys()]) this.render(element);
  }

  clearHostControls(owner: object) {
    if (this.hostOwner !== owner) return;
    this.hostOwner = null;
    this.hostRows = [];
    for (const element of [...this.boxes.keys()]) this.render(element);
  }

  /** The assembled, altered descriptor list - what an alter sees. An explicit
   * host list lets React render its first frame before its declaration effect. */
  descriptors(
    placement?: string,
    hostControls: readonly ComposerHostControlDescriptor[] = this.hostRows,
  ): ComposerControlDescriptor[] {
    const extensionRows = this.rows().filter(
      (row) => !placement || row.placement === placement,
    );
    const declaredHosts = hostControls.filter(
      (row) => !placement || row.placement === placement,
    );
    // Extension controls stood before the static controls in the original
    // anchors, so the unaltered mixed list keeps that order.
    const sources: ComposerControlDescriptor[] = [
      ...extensionRows.map((row) =>
        Object.freeze({
          id: row.id,
          key: row.key,
          placement: row.placement,
          ...(row.title !== undefined ? { title: row.title } : {}),
          ...(row.order !== undefined ? { order: row.order } : {}),
          ...(row.visible !== undefined ? { visible: row.visible } : {}),
        }),
      ),
      ...declaredHosts.map((row) => Object.freeze({ ...row })),
    ];
    let list: readonly ComposerControlDescriptor[] = sources;
    for (const alter of this.alters()) {
      try {
        const next = alter.run(list, { surface: "composer" });
        if (Array.isArray(next)) {
          validateAlteredDescriptors(next);
          list = next;
        }
      } catch (error) {
        // A failing pass leaves the previous pass's complete list in place.
        this.fail(alter.key, alter.owner, error);
      }
    }
    // Restore the identity of a legacy alter that rebuilt a descriptor and
    // omitted `key`. Ambiguous local ids stay unresolved rather than mounting
    // another extension's control.
    list = list.map((row) => {
      if (row.key) return row;
      const candidates = sources.filter(
        (source) => source.id === row.id && source.placement === row.placement,
      );
      const key = candidates.length === 1 ? candidates[0]!.key : undefined;
      return key ? { ...row, key } : row;
    });
    // An `order` sorts before the entries without one; the rest keep the
    // order the alters left them in (the sort is stable).
    return [...list].sort(
      (a, b) =>
        (a.order ?? Number.MAX_SAFE_INTEGER) -
        (b.order ?? Number.MAX_SAFE_INTEGER),
    );
  }

  private render(element: HTMLElement) {
    const attached = this.boxes.get(element);
    if (!attached) return;
    const { placement, controlKey } = attached;
    const kept = this.placed.get(element) ?? [];
    const byKey = new Map(kept.map((entry) => [entry.key, entry]));
    const visible = this.descriptors(placement).filter(
      (descriptor) =>
        descriptor.placement === placement &&
        descriptor.visible !== false &&
        descriptor.key &&
        (!controlKey || descriptor.key === controlKey),
    );
    const rows = this.rows();
    const next: Placed[] = [];
    const mounted = new Set<string>();
    for (const descriptor of visible) {
      const key = descriptor.key!;
      // A host entry has no DOM mount, and an alter that repeats a key must not
      // mount the same extension twice.
      if (key.startsWith("host/") || mounted.has(key)) continue;
      const row = rows.find(
        (candidate) =>
          candidate.key === key && candidate.placement === placement,
      );
      if (!row) continue;
      mounted.add(key);
      const existing = byKey.get(row.key);
      if (existing) {
        byKey.delete(row.key);
        setTitle(existing.box, descriptor.title);
        element.append(existing.box);
        next.push(existing);
        continue;
      }
      const box = element.ownerDocument.createElement("span");
      box.className = "extension-composer-control";
      box.dataset.extensionRenderer = row.key;
      setTitle(box, descriptor.title);
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

function validateAlteredDescriptors(
  rows: readonly ComposerControlDescriptor[],
) {
  for (const row of rows) {
    if (
      !row ||
      typeof row.id !== "string" ||
      !["actions", "tabs"].includes(row.placement) ||
      (row.key !== undefined && typeof row.key !== "string") ||
      (row.title !== undefined && typeof row.title !== "string") ||
      (row.order !== undefined && !Number.isFinite(row.order)) ||
      (row.visible !== undefined && typeof row.visible !== "boolean")
    ) {
      throw new Error("A composer alter returned an invalid control");
    }
  }
}

function setTitle(element: HTMLElement, title: string | undefined) {
  if (title === undefined) element.removeAttribute("title");
  else element.title = title;
}

function sameDescriptors(
  left: readonly ComposerHostControlDescriptor[],
  right: readonly ComposerHostControlDescriptor[],
) {
  return (
    left.length === right.length &&
    left.every((row, index) => {
      const other = right[index]!;
      return (
        row.key === other.key &&
        row.id === other.id &&
        row.placement === other.placement &&
        row.title === other.title &&
        row.order === other.order &&
        row.visible === other.visible
      );
    })
  );
}
