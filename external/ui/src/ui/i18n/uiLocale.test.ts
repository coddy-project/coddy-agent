import { afterEach, beforeEach, expect, test } from "vitest";
import {
  UI_LOCALE_DEFAULT,
  resolveUiLocale,
  applyUiLocale,
  readAppliedUiLocale,
  readUiLocaleFromUrl,
  bootstrapUiLocaleFromUrlOrCookie,
} from "./uiLocale";
import { CODDY_UI_LANG_COOKIE, writeUiLocaleCookie } from "./localeCookie";

beforeEach(() => {
  document.cookie = `${CODDY_UI_LANG_COOKIE}=; Max-Age=0; Path=/`;
  document.documentElement.lang = "";
});

afterEach(() => {
  document.cookie = `${CODDY_UI_LANG_COOKIE}=; Max-Age=0; Path=/`;
  document.documentElement.lang = "";
});

test("resolveUiLocale returns stored locale or the default", () => {
  expect(resolveUiLocale("en")).toBe("en");
  expect(resolveUiLocale("ru")).toBe("ru");
  expect(resolveUiLocale(null)).toBe(UI_LOCALE_DEFAULT);
});

test("applyUiLocale / readAppliedUiLocale round-trip through document.lang", () => {
  applyUiLocale("ru");
  expect(document.documentElement.lang).toBe("ru");
  expect(readAppliedUiLocale()).toBe("ru");
  applyUiLocale("en");
  expect(readAppliedUiLocale()).toBe("en");
});

test("readUiLocaleFromUrl parses ?lang= and rejects unknown values", () => {
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/#/settings"),
    configurable: true,
  });
  expect(readUiLocaleFromUrl()).toBeNull();

  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/?lang=ru"),
    configurable: true,
  });
  expect(readUiLocaleFromUrl()).toBe("ru");

  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/?lang=fr"),
    configurable: true,
  });
  expect(readUiLocaleFromUrl()).toBeNull();
});

test("readUiLocaleFromUrl ignores malformed percent encoding", () => {
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/?lang=%"),
    configurable: true,
  });
  expect(readUiLocaleFromUrl()).toBeNull();
});

test("bootstrap prefers ?lang= over cookie and persists it", () => {
  writeUiLocaleCookie("ru");
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/?lang=en"),
    configurable: true,
  });
  expect(bootstrapUiLocaleFromUrlOrCookie()).toBe("en");
  // URL override is persisted to the cookie.
  expect(readUiLocaleCookie()).toBe("en");
  expect(document.documentElement.lang).toBe("en");
});

test("bootstrap falls back to the cookie when no ?lang=", () => {
  writeUiLocaleCookie("ru");
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/"),
    configurable: true,
  });
  expect(bootstrapUiLocaleFromUrlOrCookie()).toBe("ru");
  expect(document.documentElement.lang).toBe("ru");
});

function readUiLocaleCookie() {
  const parts = document.cookie.split(";");
  for (const p of parts) {
    const s = p.trim();
    if (s.startsWith(`${CODDY_UI_LANG_COOKIE}=`)) {
      const v = decodeURIComponent(
        s.slice(CODDY_UI_LANG_COOKIE.length + 1).trim(),
      );
      return v === "en" || v === "ru" ? v : null;
    }
  }
  return null;
}
