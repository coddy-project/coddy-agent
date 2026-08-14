import {
  isUiLocale,
  UI_LOCALE_DEFAULT,
  UI_LOCALE_IDS,
  type UiLocale,
} from "./locales";

export const CODDY_UI_LANG_COOKIE = "coddy_ui_lang";

const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

export { UI_LOCALE_IDS, type UiLocale } from "./locales";

export function readUiLocaleCookie(): UiLocale | null {
  if (typeof document === "undefined") {
    return null;
  }
  const parts = document.cookie.split(";");
  for (const p of parts) {
    const s = p.trim();
    if (!s.startsWith(`${CODDY_UI_LANG_COOKIE}=`)) {
      continue;
    }
    let v: string;
    try {
      v = decodeURIComponent(s.slice(CODDY_UI_LANG_COOKIE.length + 1).trim());
    } catch {
      return null;
    }
    if (isUiLocale(v)) {
      return v;
    }
    return null;
  }
  return null;
}

export function mapSystemLocaleToSupported(lang: string): UiLocale {
  const normalized = lang.trim().toLowerCase().replaceAll("_", "-");
  if (isUiLocale(normalized)) {
    return normalized;
  }
  const base = normalized.split("-")[0] ?? "";
  return isUiLocale(base) ? base : UI_LOCALE_DEFAULT;
}

export function readNavigatorLanguage(): string {
  if (typeof navigator === "undefined") {
    return "en";
  }
  return navigator.language || navigator.languages?.[0] || "en";
}

export function writeUiLocaleCookie(locale: UiLocale): void {
  if (typeof document === "undefined") {
    return;
  }
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${CODDY_UI_LANG_COOKIE}=${encodeURIComponent(locale)}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}

/** Clear the locale cookie (used by the "Auto" option in the picker). */
export function clearUiLocaleCookie(): void {
  if (typeof document === "undefined") {
    return;
  }
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${CODDY_UI_LANG_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax${secure}`;
}
