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

/** Theme tokens an extension overrides for the life of its registration:
 * the eight colour names of the snapshot, or any theme token by its
 * `--name` - the app repaints from the whole token layer, not only from
 * the eight colours the snapshot resolves. */
export type ExtensionThemeOverride = Readonly<Record<string, string>>;
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
  /** Every theme token of the active theme, name to computed value. */
  tokens: () => Readonly<Record<string, string>>;
  /** Overrides theme tokens while the handle lives. A later registration of
   * another extension wins the shared tokens. */
  override: (colors: ExtensionThemeOverride) => ThemeOverrideHandle;
};

/** An override key: one of the eight colour names, or a `--token` name. */
export function validOverrideKey(name: string) {
  return (
    name in EXTENSION_THEME_COLOR_VARS ||
    (/^--[a-z][a-z0-9-]{0,63}$/.test(name) && name.length <= 64)
  );
}

/** A value an override may carry: never a rule of its own, and one of the
 * eight colour names must parse as a CSS colour. */
export function validOverrideValue(key: string, value: string, doc?: Document) {
  if (typeof value !== "string" || !value.trim() || /[;{}]|url\(/i.test(value))
    return false;
  if (!(key in EXTENSION_THEME_COLOR_VARS)) return true;
  const root = doc ?? (typeof document === "undefined" ? null : document);
  if (!root) return true;
  const probe = root.createElement("span");
  probe.style.color = value;
  return probe.style.color !== "";
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

/** The custom properties the app's own stylesheets declare on root-like
 * selectors - the token layer the app repaints from. */
export function discoverThemeTokens(doc: Document = document): string[] {
  const names = new Set<string>();
  const consider = (selector: string | undefined) =>
    !!selector &&
    (selector.includes(":root") ||
      selector.includes("[data-theme") ||
      /\bhtml\b/.test(selector));
  for (const sheet of Array.from(doc.styleSheets)) {
    let rules: CSSRuleList;
    try {
      rules = sheet.cssRules;
    } catch {
      continue;
    }
    const walk = (list: CSSRuleList) => {
      for (const rule of Array.from(list)) {
        if (rule instanceof CSSStyleRule) {
          if (!consider(rule.selectorText)) continue;
          for (const name of Array.from(rule.style)) {
            if (name.startsWith("--")) names.add(name);
          }
        }
        const nested = (rule as unknown as { cssRules?: CSSRuleList }).cssRules;
        if (nested && !(rule instanceof CSSStyleRule)) walk(nested);
      }
    };
    walk(rules);
  }
  return [...names].sort();
}

/** Every theme token of the active theme, name to computed value. A token
 * that computes to nothing is left out; the map is frozen like every payload
 * an extension receives. */
export function readExtensionThemeTokens(
  doc: Document = document,
): Readonly<Record<string, string>> {
  const win = doc.defaultView ?? window;
  const computed = win.getComputedStyle(doc.documentElement);
  const out: Record<string, string> = {};
  for (const name of discoverThemeTokens(doc)) {
    const value = computed.getPropertyValue(name).trim();
    if (value) out[name] = value;
  }
  return Object.freeze(out);
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
      if (!validOverrideKey(name) || !validOverrideValue(name, String(value)))
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
      .map(([name, value]) => {
        const token =
          name in EXTENSION_THEME_COLOR_VARS
            ? EXTENSION_THEME_COLOR_VARS[name as ExtensionThemeColor]
            : name;
        return `${token}: ${value}`;
      })
      .join(";");
    let style = this.styles.get(owner);
    if (!style) {
      style = doc.createElement("style");
      style.dataset.extThemeOverride = owner;
      doc.head.append(style);
      this.styles.set(owner, style);
    }
    // `html[data-theme]` out-specifies the theme layer's own root rules, so
    // the override paints over whichever theme is applied.
    style.textContent = `html[data-theme], :root{${body}}`;
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
      tokens: () => {
        if (signal.aborted) throw new Error("Extension disposed");
        return readExtensionThemeTokens();
      },
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
