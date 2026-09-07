export const CODDY_UI_THEME_COOKIE = "coddy_ui_theme";

const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

export type UiThemeMode =
  | "dark"
  | "light"
  | "midnight"
  | "solarized-dark"
  | "monokai"
  | "nord"
  | "rose-pine";

/** All valid theme identifiers, in display order. */
export const UI_THEME_IDS: UiThemeMode[] = [
  "dark",
  "light",
  "midnight",
  "solarized-dark",
  "monokai",
  "nord",
  "rose-pine",
];

/** Whether a theme is light (color-scheme: light). All others are dark. */
export const LIGHT_THEMES = new Set<UiThemeMode>(["light"]);

function isValidTheme(v: string): v is UiThemeMode {
  return (UI_THEME_IDS as string[]).includes(v);
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
    const v = decodeURIComponent(
      s.slice(CODDY_UI_THEME_COOKIE.length + 1).trim(),
    );
    if (isValidTheme(v)) {
      return v;
    }
    return null;
  }
  return null;
}

export function writeUiThemeCookie(mode: UiThemeMode): void {
  if (typeof document === "undefined") {
    return;
  }
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${CODDY_UI_THEME_COOKIE}=${encodeURIComponent(mode)}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}
