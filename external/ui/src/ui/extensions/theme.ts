import { readAppliedUiTheme, subscribeAppliedUiTheme } from "../theme/uiTheme";
import { LIGHT_THEMES, type UiThemeMode } from "../theme/themeCookie";
import type { EventHandle } from "./events";
import { engineInfo } from "./compat";

/** The colours an extension draws with, resolved to computed colours. */
export type ExtensionThemeColor =
  | "bg"
  | "text"
  | "muted"
  | "accent"
  | "danger"
  | "link"
  | "border"
  | "surface";

export type ExtensionTheme = Readonly<{
  id: UiThemeMode;
  mode: "light" | "dark";
  colors: Readonly<Record<ExtensionThemeColor, string>>;
}>;

/** What the host runs with: reduced motion, and the engine tier of the shell. */
export type ExtensionEffects = Readonly<{
  reducedMotion: boolean;
  engine: "modern" | "legacy";
}>;

/** Colour tokens an extension overrides for the life of its registration. */
export type ExtensionThemeOverride = Readonly<
  Partial<Record<ExtensionThemeColor, string>>
>;
export type ThemeOverrideHandle = {
  update: (colors: ExtensionThemeOverride) => void;
  remove: () => void;
};

export type ExtensionThemeAPI = {
  get: () => ExtensionTheme;
  onChange: (
    listener: (theme: Readonly<ExtensionTheme>) => unknown,
  ) => EventHandle;
  getEffects: () => ExtensionEffects;
  onEffectsChange: (
    listener: (effects: ExtensionEffects) => unknown,
  ) => EventHandle;
  /** Overrides theme tokens with CSS colours while the handle lives. A later
   * registration of another extension wins the shared tokens. */
  override: (colors: ExtensionThemeOverride) => ThemeOverrideHandle;
};

/** A colour an override may carry: a CSS colour, never a rule of its own. */
export function validOverrideColor(value: string) {
  return (
    typeof value === "string" && !!value.trim() && !/[;{}]|url\(/i.test(value)
  );
}

/** Where each colour of ExtensionTheme comes from in the web UI's tokens. */
export const EXTENSION_THEME_COLOR_VARS: Record<ExtensionThemeColor, string> = {
  bg: "--bg",
  text: "--text",
  muted: "--muted",
  accent: "--accent",
  danger: "--coddy-danger",
  link: "--coddy-link",
  border: "--coddy-glass-panel-border",
  surface: "--coddy-glass-panel-bg",
};

/**
 * readExtensionTheme resolves the active theme's tokens to computed colours,
 * rgb() or color(srgb ...), the way the browser panel's colours are resolved
 * (browserPanel/bridge.ts): a token may be written with var() or color-mix(),
 * so each one is read through a probe element. The snapshot is frozen like
 * every payload an extension receives.
 */
export function readExtensionTheme(doc: Document = document): ExtensionTheme {
  const id = readAppliedUiTheme();
  const win = doc.defaultView ?? window;
  const probe = doc.createElement("span");
  probe.style.display = "none";
  (doc.body ?? doc.documentElement).appendChild(probe);
  const colors = {} as Record<ExtensionThemeColor, string>;
  try {
    for (const [name, token] of Object.entries(EXTENSION_THEME_COLOR_VARS) as [
      ExtensionThemeColor,
      string,
    ][]) {
      probe.style.color = "";
      probe.style.color = `var(${token})`;
      colors[name] = win.getComputedStyle(probe).color;
    }
  } finally {
    probe.remove();
  }
  return Object.freeze({
    id,
    mode: LIGHT_THEMES.has(id) ? ("light" as const) : ("dark" as const),
    colors: Object.freeze(colors),
  });
}

type ThemeRow = {
  owner: string;
  signal: AbortSignal;
  run: (theme: Readonly<ExtensionTheme>) => unknown;
  remove: () => void;
};
type EffectsRow = {
  owner: string;
  signal: AbortSignal;
  run: (effects: ExtensionEffects) => unknown;
  remove: () => void;
};

export function readExtensionEffects(doc?: Document): ExtensionEffects {
  const root = doc ?? (typeof document === "undefined" ? null : document);
  if (!root)
    return Object.freeze({ reducedMotion: false, engine: engineInfo().tier });
  const win = root.defaultView ?? window;
  let reduced = root.documentElement.dataset.effects === "reduced";
  try {
    if (typeof win.matchMedia === "function")
      reduced =
        reduced ||
        win.matchMedia("(prefers-reduced-motion: reduce)")?.matches === true;
  } catch {
    /* A stub without a MediaQueryList reports no preference. */
  }
  return Object.freeze({ reducedMotion: reduced, engine: engineInfo().tier });
}

/** One override stylesheet per owner, so a dispose takes only its own layer. */
class ThemeOverrides {
  private styles = new Map<string, HTMLStyleElement>();
  private colors = new Map<string, ExtensionThemeOverride>();
  constructor(private doc?: Document) {}
  private get root(): Document | null {
    return this.doc ?? (typeof document === "undefined" ? null : document);
  }
  set(owner: string, colors: ExtensionThemeOverride) {
    for (const [name, value] of Object.entries(colors)) {
      if (
        !(name in EXTENSION_THEME_COLOR_VARS) ||
        !validOverrideColor(String(value))
      )
        throw new Error("Invalid theme override");
    }
    this.colors.set(owner, colors);
    this.paint(owner);
  }
  remove(owner: string) {
    this.colors.delete(owner);
    this.styles.get(owner)?.remove();
    this.styles.delete(owner);
  }
  private paint(owner: string) {
    const colors = this.colors.get(owner);
    const doc = this.root;
    if (!colors || !doc) return;
    const body = Object.entries(colors)
      .map(
        ([name, value]) =>
          `${EXTENSION_THEME_COLOR_VARS[name as ExtensionThemeColor]}: ${value}`,
      )
      .join(";");
    let style = this.styles.get(owner);
    if (!style) {
      style = doc.createElement("style");
      style.dataset.extThemeOverride = owner;
      doc.head.append(style);
      this.styles.set(owner, style);
    }
    style.textContent = `:root{${body}}`;
  }
}

/** The theme API of the extensions that are running: one snapshot reader and
 * one subscription to the theme on <html>, delivered by extension id in
 * ascending ASCII order like the lifecycle events. A failing listener is
 * removed and reported; nothing it does gates the host. */
export class ExtensionThemes {
  private rows = new Set<ThemeRow>();
  private effectRows = new Set<EffectsRow>();
  private stop: (() => void) | null = null;
  private lastEffects: ExtensionEffects | null = null;
  private overrides = new ThemeOverrides();
  constructor(
    private report: (owner: string, message: string) => void,
    private read: () => ExtensionTheme = readExtensionTheme,
    private subscribe: (
      onChange: () => void,
    ) => () => void = subscribeAppliedUiTheme,
    private readEffects: () => ExtensionEffects = readExtensionEffects,
  ) {}
  api(owner: string, signal: AbortSignal): ExtensionThemeAPI {
    return {
      get: () => {
        if (signal.aborted) throw new Error("Extension disposed");
        return this.read();
      },
      onChange: (listener) => this.on(owner, signal, listener),
      getEffects: () => {
        if (signal.aborted) throw new Error("Extension disposed");
        return this.readEffects();
      },
      onEffectsChange: (listener) => this.onEffects(owner, signal, listener),
      override: (colors) => {
        if (signal.aborted) throw new Error("Extension disposed");
        this.overrides.set(owner, colors);
        const handle: ThemeOverrideHandle = {
          update: (next) => {
            if (signal.aborted) throw new Error("Extension disposed");
            this.overrides.set(owner, next);
          },
          remove: () => {
            this.overrides.remove(owner);
            signal.removeEventListener("abort", handle.remove);
          },
        };
        signal.addEventListener("abort", handle.remove, { once: true });
        return handle;
      },
    };
  }
  on(
    owner: string,
    signal: AbortSignal,
    listener: (theme: Readonly<ExtensionTheme>) => unknown,
  ): EventHandle {
    if (signal.aborted) throw new Error("Extension disposed");
    if (typeof listener !== "function")
      throw new Error("Invalid theme change listener");
    const row: ThemeRow = {
      owner,
      signal,
      run: listener,
      remove: () => {
        this.rows.delete(row);
        signal.removeEventListener("abort", row.remove);
        if (this.rows.size === 0 && this.stop) {
          this.stop();
          this.stop = null;
        }
      },
    };
    this.rows.add(row);
    signal.addEventListener("abort", row.remove, { once: true });
    if (!this.stop) this.stop = this.subscribe(() => this.changed());
    return { remove: row.remove };
  }
  onEffects(
    owner: string,
    signal: AbortSignal,
    listener: (effects: ExtensionEffects) => unknown,
  ): EventHandle {
    if (signal.aborted) throw new Error("Extension disposed");
    if (typeof listener !== "function")
      throw new Error("Invalid effects change listener");
    const row: EffectsRow = {
      owner,
      signal,
      run: listener,
      remove: () => {
        this.effectRows.delete(row);
        signal.removeEventListener("abort", row.remove);
        if (this.rows.size === 0 && this.effectRows.size === 0 && this.stop) {
          this.stop();
          this.stop = null;
        }
      },
    };
    this.effectRows.add(row);
    signal.addEventListener("abort", row.remove, { once: true });
    if (!this.stop) this.stop = this.subscribe(() => this.changed());
    return { remove: row.remove };
  }
  /** Hands every listener one frozen snapshot of the theme as it is now. */
  changed() {
    const theme = this.read();
    for (const row of [...this.rows].sort((a, b) =>
      a.owner < b.owner ? -1 : a.owner > b.owner ? 1 : 0,
    )) {
      if (row.signal.aborted || !this.rows.has(row)) continue;
      const fail = (error: unknown) => {
        row.remove();
        if (!row.signal.aborted) this.report(row.owner, String(error));
      };
      try {
        Promise.resolve(row.run(theme)).catch(fail);
      } catch (error) {
        fail(error);
      }
    }
    const effects = this.readEffects();
    // The first snapshot is the baseline listeners are registered against;
    // they hear a change, never the state the world was already in.
    if (!this.lastEffects) {
      this.lastEffects = effects;
      return;
    }
    const changed =
      this.lastEffects.reducedMotion !== effects.reducedMotion ||
      this.lastEffects.engine !== effects.engine;
    this.lastEffects = effects;
    if (!changed) return;
    for (const row of [...this.effectRows].sort((a, b) =>
      a.owner < b.owner ? -1 : a.owner > b.owner ? 1 : 0,
    )) {
      if (row.signal.aborted || !this.effectRows.has(row)) continue;
      const fail = (error: unknown) => {
        row.remove();
        if (!row.signal.aborted) this.report(row.owner, String(error));
      };
      try {
        Promise.resolve(row.run(effects)).catch(fail);
      } catch (error) {
        fail(error);
      }
    }
  }
}
