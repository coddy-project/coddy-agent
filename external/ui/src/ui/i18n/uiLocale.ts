import {
  readUiLocaleCookie,
  type UiLocale,
  writeUiLocaleCookie,
  mapSystemLocaleToSupported,
  readNavigatorLanguage,
} from "./localeCookie";

export const UI_LOCALE_DEFAULT: UiLocale = "en";

export function resolveUiLocale(stored: UiLocale | null): UiLocale {
  if (stored === "en" || stored === "ru") {
    return stored;
  }
  return UI_LOCALE_DEFAULT;
}

export function applyUiLocale(locale: UiLocale): void {
  if (typeof document === "undefined") {
    return;
  }
  document.documentElement.lang = locale;
}

export function readAppliedUiLocale(): UiLocale {
  if (typeof document === "undefined") {
    return UI_LOCALE_DEFAULT;
  }
  const lang = document.documentElement.lang;
  if (lang === "ru") {
    return "ru";
  }
  return UI_LOCALE_DEFAULT;
}

/** Parse ?lang= from the current URL (en or ru). */
export function readUiLocaleFromUrl(): UiLocale | null {
  if (typeof window === "undefined") {
    return null;
  }
  const raw = new URLSearchParams(window.location.search).get("lang");
  if (raw === "ru") {
    return "ru";
  }
  if (raw === "en") {
    return "en";
  }
  return null;
}

export function bootstrapUiLocaleFromUrlOrCookie(): UiLocale {
  const fromUrl = readUiLocaleFromUrl();
  if (fromUrl !== null) {
    writeUiLocaleCookie(fromUrl);
    applyUiLocale(fromUrl);
    return fromUrl;
  }
  const stored = readUiLocaleCookie();
  const mode =
    stored !== null
      ? resolveUiLocale(stored)
      : mapSystemLocaleToSupported(readNavigatorLanguage());
  applyUiLocale(mode);
  return mode;
}

export function setUiLocale(locale: UiLocale): void {
  writeUiLocaleCookie(locale);
  applyUiLocale(locale);
}
