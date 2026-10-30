import { readAppliedUiTheme, subscribeAppliedUiTheme } from "../theme/uiTheme";
import { LIGHT_THEMES, type UiThemeMode } from "../theme/themeCookie";
import type { EventHandle } from "./events";

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

export type ExtensionThemeAPI = {
  get: () => ExtensionTheme;
  onChange: (
    listener: (theme: Readonly<ExtensionTheme>) => unknown,
  ) => EventHandle;
};

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

/** The theme API of the extensions that are running: one snapshot reader and
 * one subscription to the theme on <html>, delivered by extension id in
 * ascending ASCII order like the lifecycle events. A failing listener is
 * removed and reported; nothing it does gates the host. */
export class ExtensionThemes {
  private rows = new Set<ThemeRow>();
  private stop: (() => void) | null = null;
  constructor(
    private report: (owner: string, message: string) => void,
    private read: () => ExtensionTheme = readExtensionTheme,
    private subscribe: (
      onChange: () => void,
    ) => () => void = subscribeAppliedUiTheme,
  ) {}
  api(owner: string, signal: AbortSignal): ExtensionThemeAPI {
    return {
      get: () => {
        if (signal.aborted) throw new Error("Extension disposed");
        return this.read();
      },
      onChange: (listener) => this.on(owner, signal, listener),
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
  }
}
