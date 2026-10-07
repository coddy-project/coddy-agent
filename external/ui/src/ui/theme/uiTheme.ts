import {
  LIGHT_THEMES,
  UI_THEME_IDS,
  readUiThemeCookie,
  type UiThemeMode,
  writeUiThemeCookie,
} from "./themeCookie";

export const UI_THEME_DEFAULT: UiThemeMode = "dark";

export function resolveUiThemeMode(stored: UiThemeMode | null): UiThemeMode {
  if (stored !== null && (UI_THEME_IDS as string[]).includes(stored)) {
    return stored;
  }
  return UI_THEME_DEFAULT;
}

export function applyUiTheme(mode: UiThemeMode): void {
  if (typeof document === "undefined") {
    return;
  }
  document.documentElement.dataset.theme = mode;
  document.documentElement.style.colorScheme = LIGHT_THEMES.has(mode)
    ? "light"
    : "dark";
}

export function readAppliedUiTheme(): UiThemeMode {
  if (typeof document === "undefined") {
    return UI_THEME_DEFAULT;
  }
  const t = document.documentElement.dataset.theme as string | undefined;
  if (t && (UI_THEME_IDS as string[]).includes(t)) {
    return t as UiThemeMode;
  }
  return UI_THEME_DEFAULT;
}

export function bootstrapUiThemeFromCookie(): UiThemeMode {
  const mode = resolveUiThemeMode(readUiThemeCookie());
  applyUiTheme(mode);
  return mode;
}

export function setUiTheme(mode: UiThemeMode): void {
  writeUiThemeCookie(mode);
  applyUiTheme(mode);
}

/** One observer of <html> for every subscriber, however many diagrams a page shows. */
const themeListeners = new Set<() => void>();
let themeWatch: MutationObserver | null = null;

/**
 * Subscribes to the theme on <html>, for useSyncExternalStore: what a component
 * draws in the theme's colours outside CSS (a diagram) follows a theme switch.
 */
export function subscribeAppliedUiTheme(onChange: () => void): () => void {
  if (
    typeof document === "undefined" ||
    typeof MutationObserver === "undefined"
  ) {
    return () => {};
  }
  themeListeners.add(onChange);
  if (!themeWatch) {
    themeWatch = new MutationObserver(() => {
      for (const listener of [...themeListeners]) listener();
    });
    themeWatch.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
  }
  return () => {
    themeListeners.delete(onChange);
    if (themeListeners.size === 0 && themeWatch) {
      themeWatch.disconnect();
      themeWatch = null;
    }
  };
}
