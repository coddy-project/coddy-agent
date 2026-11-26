export const CODDY_UI_THEME_COOKIE = "coddy_ui_theme";

const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

export type BuiltInUiThemeMode =
  | "dark"
  | "light"
  | "midnight"
  | "solarized-dark"
  | "monokai"
  | "nord"
  | "rose-pine"
  | "neuraldeep";

export type ExtensionUiThemeId = `ext:${string}/${string}`;
export type UiThemeMode = BuiltInUiThemeMode | ExtensionUiThemeId;

/** All valid built-in theme identifiers, in display order. */
export const UI_THEME_IDS: BuiltInUiThemeMode[] = [
  "dark",
  "neuraldeep",
  "light",
  "midnight",
  "solarized-dark",
  "monokai",
  "nord",
  "rose-pine",
];

/** Whether a theme is light (color-scheme: light). The registry adds and
 * removes manifest-declared light extension themes as packages reconcile. */
export const LIGHT_THEMES = new Set<UiThemeMode>(["light"]);

const EXTENSION_THEME_ID =
  /^ext:([a-z][a-z0-9]*(?:-[a-z0-9]+)*)\/([a-z][a-z0-9]*(?:-[a-z0-9]+)*)$/;

export function isBuiltInUiThemeMode(
  value: string,
): value is BuiltInUiThemeMode {
  return (UI_THEME_IDS as string[]).includes(value);
}

/** Public extension ids are deliberately narrower than arbitrary cookie text:
 * an owner accepted by the package store and a theme id of the same grammar. */
export function isExtensionUiThemeId(
  value: string,
): value is ExtensionUiThemeId {
  const match = EXTENSION_THEME_ID.exec(value);
  return match !== null && match[1]!.length <= 32 && match[2]!.length <= 64;
}

export function isUiThemeId(value: string): value is UiThemeMode {
  return isBuiltInUiThemeMode(value) || isExtensionUiThemeId(value);
}

export function readUiThemeCookie(): UiThemeMode | null {
  if (typeof document === "undefined") {
    return null;
  }
  const parts = document.cookie.split(";");
  for (const p of parts) {
    const s = p.trim();
    if (!s.startsWith(`${CODDY_UI_THEME_COOKIE}=`)) {
      continue;
    }
    let value: string;
    try {
      value = decodeURIComponent(
        s.slice(CODDY_UI_THEME_COOKIE.length + 1).trim(),
      );
    } catch {
      return null;
    }
    return isUiThemeId(value) ? value : null;
  }
  return null;
}

export function writeUiThemeCookie(mode: UiThemeMode): void {
  if (typeof document === "undefined" || !isUiThemeId(mode)) {
    return;
  }
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${CODDY_UI_THEME_COOKIE}=${encodeURIComponent(mode)}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}
