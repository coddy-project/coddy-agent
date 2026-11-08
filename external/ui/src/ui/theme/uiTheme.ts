import {
  isUiThemeId,
  LIGHT_THEMES,
  readUiThemeCookie,
  type UiThemeMode,
  writeUiThemeCookie,
} from "./themeCookie";

export const UI_THEME_DEFAULT: UiThemeMode = "dark";

export function resolveUiThemeMode(stored: UiThemeMode | null): UiThemeMode {
  return stored !== null && isUiThemeId(stored) ? stored : UI_THEME_DEFAULT;
}

export function applyUiTheme(mode: UiThemeMode): void {
  if (typeof document === "undefined") {
    return;
  }
  const resolved = resolveUiThemeMode(mode);
  document.documentElement.dataset.theme = resolved;
  document.documentElement.style.colorScheme = LIGHT_THEMES.has(resolved)
    ? "light"
    : "dark";
}

export function readAppliedUiTheme(): UiThemeMode {
  if (typeof document === "undefined") {
    return UI_THEME_DEFAULT;
  }
  const theme = document.documentElement.dataset.theme;
  return theme && isUiThemeId(theme) ? theme : UI_THEME_DEFAULT;
}

export function bootstrapUiThemeFromCookie(): UiThemeMode {
  const mode = resolveUiThemeMode(readUiThemeCookie());
  applyUiTheme(mode);
  return mode;
}

export function setUiTheme(mode: UiThemeMode): void {
  const resolved = resolveUiThemeMode(mode);
  writeUiThemeCookie(resolved);
  applyUiTheme(resolved);
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
