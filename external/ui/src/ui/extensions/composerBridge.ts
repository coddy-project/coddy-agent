/**
 * The composer bridge: the draft and the picks of the composer, handed to an
 * extension as a small imperative API. The composer reports its surface here
 * while it renders - the same pattern as the mounted tool views - and the
 * SDK wraps it as `coddy.composer`. Paste and drop go through `claimPaste` /
 * `claimDrop` first, so an extension can take the event before the host's
 * own attachment and mention handling.
 */
export type ComposerSurface = {
  getText: () => string;
  setText: (text: string) => void;
  /** Replaces the draft around the caret; falls back to appending. */
  insertText: (text: string) => void;
  attachFiles: (files: readonly File[]) => void;
  getMode: () => string;
  setMode: (mode: string) => void;
  getModel: () => string;
  setModel: (modelId: string) => void;
  getReasoning: () => string;
  setReasoning: (level: string) => void;
  /** The permission mode pill of the composer, when the surface shows one. */
  getPermissionMode: () => string;
  textarea: HTMLTextAreaElement | null;
};

type DraftListener = (text: string) => void;
type ClaimEvent = { preventDefault(): void };
type ClaimListener = (event: ClaimEvent) => boolean | void;

const emptySurface = (): ComposerSurface => ({
  getText: () => "",
  setText: () => {},
  insertText: () => {},
  attachFiles: () => {},
  getMode: () => "",
  setMode: () => {},
  getModel: () => "",
  setModel: () => {},
  getReasoning: () => "",
  setReasoning: () => {},
  getPermissionMode: () => "",
  textarea: null,
});

export class ComposerBridge {
  private surface: ComposerSurface = emptySurface();
  private draftListeners = new Map<DraftListener, AbortSignal>();
  private pasteListeners = new Map<ClaimListener, AbortSignal>();
  private dropListeners = new Map<ClaimListener, AbortSignal>();

  /** The composer reports its live surface; a null clears it (unmounted). */
  report(surface: ComposerSurface | null) {
    this.surface = surface ?? emptySurface();
    if (surface) for (const [fn] of this.draftListeners) this.fire(fn);
  }

  text(): string {
    return this.surface.getText();
  }
  setText(text: string) {
    this.surface.setText(text);
    for (const [fn] of this.draftListeners) this.fire(fn);
  }
  insertText(text: string) {
    this.surface.insertText(text);
    for (const [fn] of this.draftListeners) this.fire(fn);
  }
  attachFiles(files: readonly File[]) {
    this.surface.attachFiles(files);
  }
  mode(): string {
    return this.surface.getMode();
  }
  setMode(mode: string) {
    this.surface.setMode(mode);
  }
  model(): string {
    return this.surface.getModel();
  }
  setModel(modelId: string) {
    this.surface.setModel(modelId);
  }
  reasoning(): string {
    return this.surface.getReasoning();
  }
  setReasoning(level: string) {
    this.surface.setReasoning(level);
  }
  permissionMode(): string {
    return this.surface.getPermissionMode();
  }

  onDraftChange(
    signal: AbortSignal,
    listener: DraftListener,
  ): { remove: () => void } {
    return this.watch(this.draftListeners, signal, listener);
  }

  /** Lets one extension take a paste before the host handles it: a listener
   * that returns true claims the event. */
  claimPaste(event: ClaimEvent): boolean {
    return this.claim(this.pasteListeners, event);
  }
  claimDrop(event: ClaimEvent): boolean {
    return this.claim(this.dropListeners, event);
  }
  onPaste(
    signal: AbortSignal,
    listener: ClaimListener,
  ): { remove: () => void } {
    return this.watch(this.pasteListeners, signal, listener);
  }
  onDrop(signal: AbortSignal, listener: ClaimListener): { remove: () => void } {
    return this.watch(this.dropListeners, signal, listener);
  }

  /** The composer calls this when its draft changed on its own. */
  draftChanged() {
    for (const [fn] of this.draftListeners) this.fire(fn);
  }

  private fire(fn: DraftListener) {
    try {
      fn(this.surface.getText());
    } catch {
      /* A listener that throws loses nothing but its own call. */
    }
  }
  private claim(
    map: Map<ClaimListener, AbortSignal>,
    event: ClaimEvent,
  ): boolean {
    for (const [fn, signal] of [...map]) {
      if (signal.aborted) {
        map.delete(fn);
        continue;
      }
      try {
        if (fn(event) === true) return true;
      } catch {
        map.delete(fn);
      }
    }
    return false;
  }
  private watch<T>(
    map: Map<T, AbortSignal>,
    signal: AbortSignal,
    fn: T,
  ): { remove: () => void } {
    if (signal.aborted) throw new Error("Extension disposed");
    if (typeof fn !== "function") throw new Error("A listener is required");
    const remove = () => {
      if (!map.delete(fn)) return;
      signal.removeEventListener("abort", remove);
    };
    map.set(fn, signal);
    signal.addEventListener("abort", remove, { once: true });
    return { remove };
  }
}

export const composerBridge = new ComposerBridge();
