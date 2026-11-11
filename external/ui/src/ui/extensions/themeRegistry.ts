import {
  isBuiltInUiThemeMode,
  isExtensionUiThemeId,
  isUiThemeId,
  LIGHT_THEMES,
  type ExtensionUiThemeId,
  type UiThemeMode,
} from "../theme/themeCookie";
import {
  applyUiTheme,
  readAppliedUiTheme,
  setUiTheme,
  UI_THEME_DEFAULT,
} from "../theme/uiTheme";

/** The registry needs only the package identity and exact archive digest. The
 * runtime's ExtensionEntry is structurally assignable without an import cycle. */
export type ExtensionThemeEntry = Readonly<{
  id: string;
  enabled: boolean;
  sha256: string;
}>;

/** Reads one asset from the exact package digest carried by entry. */
export type FetchExtensionAssetText = (
  entry: ExtensionThemeEntry,
  path: string,
) => Promise<string>;

export type ExtensionThemeSwatch = Readonly<{
  bg: string;
  accent: string;
  text: string;
}>;

export type ExtensionThemeDescriptor = Readonly<{
  id: ExtensionUiThemeId;
  owner: string;
  themeId: string;
  title: string;
  mode: "light" | "dark";
  styles: readonly string[];
  swatch: ExtensionThemeSwatch;
}>;

type RegistryRow = Readonly<{
  entry: ExtensionThemeEntry;
  descriptor: ExtensionThemeDescriptor;
}>;

const ID_SEGMENT = /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/;
const ASSET_PATH = /^[a-zA-Z0-9_./-]+$/;
const DARK_SWATCH = {
  bg: "#121212",
  accent: "#9333ea",
  text: "#ffffff",
} as const;
const LIGHT_SWATCH = {
  bg: "#f8f8fa",
  accent: "#7c3aed",
  text: "#18181b",
} as const;

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function validOwner(owner: string): boolean {
  return owner.length <= 32 && ID_SEGMENT.test(owner);
}

function validThemeId(id: unknown): id is string {
  return typeof id === "string" && id.length <= 64 && ID_SEGMENT.test(id);
}

function validStylePath(path: unknown): path is string {
  if (
    typeof path !== "string" ||
    !path.endsWith(".css") ||
    !ASSET_PATH.test(path) ||
    path.startsWith("/")
  )
    return false;
  return path
    .split("/")
    .every((segment) => segment !== "" && segment !== "." && segment !== "..");
}

function optionalLabel(value: unknown): string | undefined {
  if (value === undefined) return undefined;
  if (typeof value !== "string" || !value.trim() || value.length > 120)
    throw new Error(
      "A theme label must be a nonempty string of at most 120 characters",
    );
  return value.trim();
}

function swatch(value: unknown, mode: "light" | "dark"): ExtensionThemeSwatch {
  if (value === undefined)
    return { ...(mode === "light" ? LIGHT_SWATCH : DARK_SWATCH) };
  if (!object(value)) throw new Error("A theme swatch must be an object");
  const out: Record<string, string> = {};
  for (const key of ["bg", "accent", "text"]) {
    const color = value[key];
    if (
      typeof color !== "string" ||
      !color.trim() ||
      color.length > 128 ||
      /[;{}]|url\(/i.test(color)
    )
      throw new Error(`Invalid theme swatch colour: ${key}`);
    out[key] = color.trim();
  }
  return out as unknown as ExtensionThemeSwatch;
}

/** Parses and validates the frontend-owned part of manifest.json. Unknown
 * fields remain the backend's concern; a malformed themes field contributes no
 * theme instead of weakening the public id or asset-path grammar. */
export function parseExtensionThemeManifest(
  owner: string,
  source: string,
): ExtensionThemeDescriptor[] {
  if (!validOwner(owner)) throw new Error(`Invalid extension owner: ${owner}`);
  const manifest: unknown = JSON.parse(source);
  if (!object(manifest))
    throw new Error("manifest.json must contain an object");
  if (manifest.themes === undefined) return [];
  if (!Array.isArray(manifest.themes))
    throw new Error("manifest themes must be an array");
  const seen = new Set<string>();
  return manifest.themes.map((value) => {
    if (!object(value)) throw new Error("A manifest theme must be an object");
    if (!validThemeId(value.id))
      throw new Error("A manifest theme needs a lowercase id");
    const id = `ext:${owner}/${value.id}`;
    if (!isExtensionUiThemeId(id) || seen.has(id))
      throw new Error(`Duplicate or invalid extension theme: ${id}`);
    seen.add(id);
    const mode = value.mode ?? "dark";
    if (mode !== "light" && mode !== "dark")
      throw new Error(`Invalid mode for ${id}`);
    if (
      !Array.isArray(value.styles) ||
      value.styles.length === 0 ||
      !value.styles.every(validStylePath)
    )
      throw new Error(`Invalid styles for ${id}`);
    const title =
      optionalLabel(value.title) ?? optionalLabel(value.name) ?? value.id;
    return {
      id,
      owner,
      themeId: value.id,
      title,
      mode,
      styles: [...value.styles],
      swatch: swatch(value.swatch, mode),
    };
  });
}

/** Manifest themes are package data, not JavaScript registrations. Preparing a
 * new exact package set replaces the catalog, loads the selected layer before
 * the first app commit, and forgets a selection whose package or asset died. */
export class ExtensionThemeRegistry {
  private descriptors: readonly ExtensionThemeDescriptor[] = [];
  private rows = new Map<ExtensionUiThemeId, RegistryRow>();
  private listeners = new Set<() => void>();
  private fetchText: FetchExtensionAssetText | null = null;
  private operation = 0;
  private active: { id: ExtensionUiThemeId; digest: string } | null = null;
  private styles: HTMLStyleElement[] = [];

  list = (): readonly ExtensionThemeDescriptor[] => this.descriptors;

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  private publish() {
    for (const listener of [...this.listeners]) listener();
  }

  async prepare(
    entries: readonly ExtensionThemeEntry[],
    fetchText: FetchExtensionAssetText,
  ): Promise<void> {
    const operation = ++this.operation;
    const loaded = await Promise.all(
      entries
        .filter((entry) => entry.enabled)
        .map(async (entry): Promise<RegistryRow[]> => {
          try {
            const source = await fetchText(entry, "manifest.json");
            return parseExtensionThemeManifest(entry.id, source).map(
              (descriptor) => ({ entry, descriptor }),
            );
          } catch {
            // A broken package cannot hold the application startup gate or keep
            // an old manifest's themes alive after its digest changed.
            return [];
          }
        }),
    );
    if (operation !== this.operation) return;
    const next = loaded.flat();
    const rows = new Map<ExtensionUiThemeId, RegistryRow>();
    for (const row of next) {
      if (rows.has(row.descriptor.id))
        throw new Error(`Duplicate extension theme: ${row.descriptor.id}`);
      rows.set(row.descriptor.id, row);
    }

    for (const descriptor of this.descriptors) {
      if (descriptor.mode === "light") LIGHT_THEMES.delete(descriptor.id);
    }
    this.descriptors = next.map((row) => row.descriptor);
    this.rows = rows;
    this.fetchText = fetchText;
    for (const descriptor of this.descriptors) {
      if (descriptor.mode === "light") LIGHT_THEMES.add(descriptor.id);
    }
    this.publish();

    const selected = readAppliedUiTheme();
    if (!isExtensionUiThemeId(selected)) {
      this.removeActiveStyles();
      return;
    }
    const row = rows.get(selected);
    if (!row) {
      this.fallback();
      return;
    }
    await this.load(row, operation);
  }

  /** Applies a built-in immediately, or fetches and installs a dynamic layer
   * before its id becomes active. Failure and an unknown id both select dark. */
  async apply(mode: UiThemeMode): Promise<UiThemeMode> {
    const operation = ++this.operation;
    if (isBuiltInUiThemeMode(mode)) {
      this.removeActiveStyles();
      setUiTheme(mode);
      return mode;
    }
    if (!isUiThemeId(mode) || !isExtensionUiThemeId(mode)) {
      this.fallback();
      return UI_THEME_DEFAULT;
    }
    const row = this.rows.get(mode);
    if (!row || !this.fetchText) {
      this.fallback();
      return UI_THEME_DEFAULT;
    }
    return this.load(row, operation);
  }

  private async load(
    row: RegistryRow,
    operation: number,
  ): Promise<UiThemeMode> {
    const { descriptor } = row;
    if (
      this.active?.id === descriptor.id &&
      this.active.digest === row.entry.sha256
    ) {
      setUiTheme(descriptor.id);
      return descriptor.id;
    }
    const fetchText = this.fetchText;
    if (!fetchText) {
      if (operation === this.operation) this.fallback();
      return readAppliedUiTheme();
    }
    try {
      const styles = await Promise.all(
        descriptor.styles.map((path) => fetchText(row.entry, path)),
      );
      if (operation !== this.operation || this.rows.get(descriptor.id) !== row)
        return readAppliedUiTheme();
      this.removeActiveStyles();
      this.styles = styles.map((css) => {
        const style = document.createElement("style");
        style.dataset.extTheme = descriptor.id;
        style.dataset.extThemeOwner = descriptor.owner;
        style.textContent = css;
        document.head.append(style);
        return style;
      });
      this.active = { id: descriptor.id, digest: row.entry.sha256 };
      setUiTheme(descriptor.id);
      return descriptor.id;
    } catch {
      if (operation === this.operation) this.fallback();
      return readAppliedUiTheme();
    }
  }

  private fallback() {
    this.removeActiveStyles();
    setUiTheme(UI_THEME_DEFAULT);
    // Keep this explicit even though setUiTheme already applies it: fallback is
    // also the place that guarantees an invalid stored id is replaced.
    applyUiTheme(UI_THEME_DEFAULT);
  }

  private removeActiveStyles() {
    for (const style of this.styles) style.remove();
    this.styles = [];
    this.active = null;
  }

  /** Drops package state without changing the operator's selected theme. */
  clear() {
    ++this.operation;
    for (const descriptor of this.descriptors) {
      if (descriptor.mode === "light") LIGHT_THEMES.delete(descriptor.id);
    }
    this.descriptors = [];
    this.rows.clear();
    this.fetchText = null;
    this.removeActiveStyles();
    this.publish();
  }
}

export const extensionThemeRegistry = new ExtensionThemeRegistry();
