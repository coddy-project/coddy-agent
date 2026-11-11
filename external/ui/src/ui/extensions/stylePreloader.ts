import type { ExtensionEntry } from "./runtime";

export const STYLE_PRELOAD_TIMEOUT_MS = 5000;

/** Reads one exact-digest package asset with the bounded startup budget shared
 * by manifest styles and manifest themes. */
export async function fetchExtensionAssetText(
  entry: Pick<ExtensionEntry, "id" | "sha256">,
  path: string,
): Promise<string> {
  const encoded = path.split("/").map(encodeURIComponent).join("/");
  // A stalled asset gets the same bounded startup as a failed one: the app
  // renders, and adoption reports the failure to the extension card.
  const controller = new AbortController();
  const timer = window.setTimeout(
    () => controller.abort(),
    STYLE_PRELOAD_TIMEOUT_MS,
  );
  try {
    const response = await fetch(
      `/coddy/extensions/${encodeURIComponent(entry.id)}/assets/${encoded}?sha256=${encodeURIComponent(entry.sha256)}`,
      { signal: controller.signal },
    );
    if (!response.ok) throw new Error(`Asset ${path}: HTTP ${response.status}`);
    return new TextDecoder().decode(await response.arrayBuffer());
  } finally {
    window.clearTimeout(timer);
  }
}

export type PreloadedExtensionStyles = {
  ordinary: HTMLStyleElement[];
  compat: HTMLStyleElement[];
  error: unknown;
};

type Layer = PreloadedExtensionStyles & {
  digest: string;
  promise: Promise<void>;
};

/** Fetches extension CSS ahead of activation and installs it before the app
 * first commits. JavaScript evaluation is deliberately not part of this gate.
 * A layer is adopted once by activation, which then owns its cleanup. */
export class ExtensionStylePreloader {
  private layers = new Map<string, Layer>();
  constructor(private legacy: () => boolean) {}

  async prepare(entries: readonly ExtensionEntry[]): Promise<void> {
    const wanted = new Map(
      entries
        .filter((entry) => entry.enabled)
        .map((entry) => [entry.id, entry]),
    );
    for (const [id, layer] of this.layers) {
      const entry = wanted.get(id);
      if (entry?.sha256 === layer.digest) continue;
      this.removeLayer(id, layer);
    }
    await Promise.all(
      [...wanted.values()].map((entry) => this.prepareOne(entry)),
    );
  }

  private prepareOne(entry: ExtensionEntry): Promise<void> {
    const existing = this.layers.get(entry.id);
    if (existing?.digest === entry.sha256) return existing.promise;
    if (existing) this.removeLayer(entry.id, existing);
    const layer = {
      digest: entry.sha256,
      ordinary: [],
      compat: [],
      error: null,
      promise: Promise.resolve(),
    } as Layer;
    layer.promise = (async () => {
      try {
        const ordinary = await Promise.all(
          (entry.ui.styles ?? []).map((path) => this.fetchText(entry, path)),
        );
        const compat = this.legacy()
          ? await Promise.all(
              (entry.ui.compat_styles ?? []).map((path) =>
                this.fetchText(entry, path),
              ),
            )
          : [];
        // A newer prepare may have replaced this layer while it fetched.
        if (this.layers.get(entry.id) !== layer) return;
        layer.ordinary = ordinary.map((css) =>
          this.style(entry.id, "ext", css),
        );
        layer.compat = compat.map((css) =>
          this.style(entry.id, "extCompat", css),
        );
      } catch (error) {
        // Startup must not stall on a broken package. Activation receives the
        // same failure and reports it on the extension card.
        layer.error = error;
      }
    })();
    this.layers.set(entry.id, layer);
    return layer.promise;
  }

  /** Hands the installed layer to activation. A missing layer means activation
   * loads styles itself (direct reconcile in tests and older callers). */
  async adopt(entry: ExtensionEntry): Promise<PreloadedExtensionStyles | null> {
    const layer = this.layers.get(entry.id);
    if (!layer || layer.digest !== entry.sha256) return null;
    await layer.promise;
    if (this.layers.get(entry.id) !== layer) return null;
    this.layers.delete(entry.id);
    return {
      ordinary: layer.ordinary,
      compat: layer.compat,
      error: layer.error,
    };
  }

  clear() {
    for (const [id, layer] of this.layers) this.removeLayer(id, layer);
  }

  private removeLayer(id: string, layer: Layer) {
    if (this.layers.get(id) !== layer) return;
    this.layers.delete(id);
    for (const style of [...layer.ordinary, ...layer.compat]) style.remove();
  }

  private style(owner: string, kind: "ext" | "extCompat", css: string) {
    const style = document.createElement("style");
    style.dataset[kind] = owner;
    style.textContent = css;
    document.head.append(style);
    return style;
  }

  private async fetchText(entry: ExtensionEntry, path: string) {
    return fetchExtensionAssetText(entry, path);
  }
}
