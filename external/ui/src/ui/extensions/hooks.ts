import type { ExtensionAPI } from "./runtime";
import type { ContributionHandle, Owned, ToolView } from "./contributions";

/**
 * Hooks in interface elements: an element declares the slots it offers in its
 * own registration - `hooks: ["summary"]` on a panel or page names
 * `<owner>/summary` - and any extension fills one with
 * `hooks.register({into, mount})`. The host mounts the contributions into
 * `hooks.slot(name, element)` where the declaring element renders: empty
 * slots draw nothing, and contributions appear and leave live.
 */
export type HookContribution = {
  id: string;
  /** The slot to fill, as `<owner>/<hook>`: the declaring extension's id and
   * the hook name it put in its registration. */
  into: string;
  mount: (
    element: HTMLElement,
    api: ExtensionAPI,
  ) => void | (() => void) | ToolView;
};
export type HookRef = Readonly<{ owner: string; hook: string }>;
export type HookSlotHandle = { remove: () => void };

export function parseHookRef(into: string): HookRef | null {
  const match = /^([a-z][a-z0-9-]{0,63})\/([a-z][a-z0-9-]{0,63})$/.exec(
    into ?? "",
  );
  return match ? Object.freeze({ owner: match[1]!, hook: match[2]! }) : null;
}

export function isHookName(name: string) {
  return /^[a-z][a-z0-9-]{0,63}$/.test(name);
}

export function validateHookContribution(contribution: HookContribution) {
  if (
    !isHookName(contribution.id ?? "") ||
    !parseHookRef(contribution.into ?? "") ||
    typeof contribution.mount !== "function"
  )
    throw new Error("A hook contribution needs an id, into and mount function");
}

type Mounted = { key: string; box: HTMLElement; cleanup: () => void };

/** The boxes a hook renders into, one per `hooks.slot` call. A change to the
 * contribution rows updates each box in place: a new contribution mounts, a
 * removed one unmounts, and one that stays keeps its DOM and state - only
 * moved to its place. Order is owner id, then registration order. */
export class HookSlotHost {
  private boxes = new Map<string, Map<HTMLElement, Mounted[]>>();
  constructor(
    private rows: () => Owned<HookContribution>[],
    private fail: (key: string, owner: string, error: unknown) => void,
  ) {}

  /** Renders the contributions of `ref` into `element` and keeps it live
   * until the handle removes it or `signal` aborts. */
  attach(
    ref: string,
    element: HTMLElement,
    signal: AbortSignal,
  ): HookSlotHandle {
    if (signal.aborted) throw new Error("Extension disposed");
    if (!parseHookRef(ref)) throw new Error("Unknown hook reference");
    let boxes = this.boxes.get(ref);
    if (!boxes) this.boxes.set(ref, (boxes = new Map()));
    boxes.set(element, []);
    this.render(ref, element);
    const remove = () => {
      const placed = boxes!.get(element);
      if (!boxes!.delete(element)) return;
      signal.removeEventListener("abort", remove);
      this.unmount(element, placed ?? []);
      if (!boxes!.size) this.boxes.delete(ref);
    };
    signal.addEventListener("abort", remove, { once: true });
    return { remove };
  }

  /** Updates every box after the contribution rows changed. A render that
   * drops a failing contribution triggers one of these re-entrantly; that
   * pass is skipped - the box is already consistent. */
  refresh() {
    if (this.rendering) return;
    this.rendering = true;
    try {
      for (const [ref, boxes] of this.boxes)
        for (const element of [...boxes.keys()]) this.render(ref, element);
    } finally {
      this.rendering = false;
    }
  }
  private rendering = false;

  private matching(ref: string) {
    return this.rows()
      .filter((row) => row.into === ref)
      .sort((a, b) => (a.owner < b.owner ? -1 : a.owner > b.owner ? 1 : 0));
  }

  private render(ref: string, element: HTMLElement) {
    const boxes = this.boxes.get(ref);
    if (!boxes || !boxes.has(element)) return;
    const placed = boxes.get(element) ?? [];
    const byKey = new Map(placed.map((entry) => [entry.key, entry]));
    const next: Mounted[] = [];
    for (const row of this.matching(ref)) {
      const existing = byKey.get(row.key);
      if (existing) {
        byKey.delete(row.key);
        element.append(existing.box);
        next.push(existing);
        continue;
      }
      const box = element.ownerDocument.createElement("div");
      box.className = "extension-hook-view";
      box.dataset.extensionRenderer = row.key;
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
          throw new Error("Hook mount must be synchronous");
        }
      } catch (error) {
        this.fail(row.key, row.owner, error);
        box.remove();
        continue;
      }
      next.push({ key: row.key, box, cleanup: () => cleanup() });
    }
    // What no row claims any more leaves with its cleanup.
    this.unmount(element, [...byKey.values()], next);
    boxes.set(element, next);
  }

  /** Drops the entries `gone` from the box; `keep` stays mounted. */
  private unmount(element: HTMLElement, gone: Mounted[], keep: Mounted[] = []) {
    const kept = new Set(keep.map((entry) => entry.box));
    for (const entry of gone) {
      if (kept.has(entry.box)) continue;
      entry.box.remove();
      try {
        entry.cleanup();
      } catch (error) {
        this.fail(entry.key, entry.key.split("/")[0]!, error);
      }
    }
    element.replaceChildren(...keep.map((entry) => entry.box));
  }
}
