export const CODDY_UI_LANG_COOKIE = "coddy_ui_lang";

const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

export type UiLocale = "en" | "ru";

export const UI_LOCALE_IDS: UiLocale[] = ["en", "ru"];

function isValidLocale(v: string): v is UiLocale {
  return (UI_LOCALE_IDS as string[]).includes(v);
}

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
    const v = decodeURIComponent(
      s.slice(CODDY_UI_LANG_COOKIE.length + 1).trim(),
    );
    if (isValidLocale(v)) {
      return v;
    }
    return null;
  }
  return null;
}

export function mapSystemLocaleToSupported(lang: string): UiLocale {
  const base = lang.trim().toLowerCase().split("-")[0];
  if (base === "ru") {
    return "ru";
  }
  return "en";
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
