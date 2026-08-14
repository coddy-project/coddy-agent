import {
  readUiLocaleCookie,
  type UiLocale,
  writeUiLocaleCookie,
  mapSystemLocaleToSupported,
  readNavigatorLanguage,
} from "./localeCookie";
import { isUiLocale, UI_LOCALE_DEFAULT } from "./locales";

export { UI_LOCALE_DEFAULT } from "./locales";

export function resolveUiLocale(stored: UiLocale | null): UiLocale {
  return stored ?? UI_LOCALE_DEFAULT;
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
  const lang = document.documentElement.lang.trim().toLowerCase();
  return isUiLocale(lang) ? lang : UI_LOCALE_DEFAULT;
}

/** Parse a registered locale id from ?lang= in the current URL. */
export function readUiLocaleFromUrl(): UiLocale | null {
  if (typeof window === "undefined") {
    return null;
  }
  const raw = new URLSearchParams(window.location.search).get("lang");
  return raw !== null && isUiLocale(raw) ? raw : null;
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
