import { readUiThemeCookie, type UiThemeMode, writeUiThemeCookie } from "./themeCookie";

export const UI_THEME_DEFAULT: UiThemeMode = "dark";

export function resolveUiThemeMode(stored: UiThemeMode | null): UiThemeMode {
  return stored === "light" ? "light" : UI_THEME_DEFAULT;
}

export function applyUiTheme(mode: UiThemeMode): void {
  if (typeof document === "undefined") {
    return;
  }
  document.documentElement.dataset.theme = mode;
  document.documentElement.style.colorScheme = mode;
}

export function readAppliedUiTheme(): UiThemeMode {
  if (typeof document === "undefined") {
    return UI_THEME_DEFAULT;
  }
  return document.documentElement.dataset.theme === "light" ? "light" : "dark";
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
