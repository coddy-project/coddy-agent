import { afterEach, expect, test } from "vitest";
import {
  CODDY_UI_LANG_COOKIE,
  UI_LOCALE_IDS,
  readUiLocaleCookie,
  writeUiLocaleCookie,
  clearUiLocaleCookie,
  mapSystemLocaleToSupported,
  type UiLocale,
} from "./localeCookie";

function setLocation(href: string) {
  Object.defineProperty(window, "location", {
    value: new URL(href),
    configurable: true,
  });
}

afterEach(() => {
  document.cookie = `${CODDY_UI_LANG_COOKIE}=; Max-Age=0; Path=/`;
});

test("write then read ui locale cookie for all locales", () => {
  setLocation("http://127.0.0.1:5173/");
  for (const id of UI_LOCALE_IDS) {
    document.cookie = `${CODDY_UI_LANG_COOKIE}=; Max-Age=0; Path=/`;
    writeUiLocaleCookie(id);
    expect(readUiLocaleCookie()).toBe(id);
  }
});

test("en and ru round-trip", () => {
  setLocation("http://127.0.0.1:5173/");
  document.cookie = `${CODDY_UI_LANG_COOKIE}=; Max-Age=0; Path=/`;
  writeUiLocaleCookie("en");
  expect(readUiLocaleCookie()).toBe("en");
  writeUiLocaleCookie("ru");
  expect(readUiLocaleCookie()).toBe("ru");
});

test("invalid cookie value is ignored", () => {
  document.cookie = `${CODDY_UI_LANG_COOKIE}=fr; Path=/`;
  expect(readUiLocaleCookie()).toBeNull();
});

test("clearUiLocaleCookie removes the stored locale", () => {
  setLocation("http://127.0.0.1:5173/");
  writeUiLocaleCookie("ru");
  expect(readUiLocaleCookie()).toBe("ru");
  clearUiLocaleCookie();
  expect(readUiLocaleCookie()).toBeNull();
});

test("mapSystemLocaleToSupported routes russian variants to ru, else en", () => {
  const cases: Array<[string, UiLocale]> = [
    ["ru", "ru"],
    ["ru-RU", "ru"],
    ["ru-UA", "ru"],
    ["en-US", "en"],
    ["fr-FR", "en"],
    ["DE", "en"],
  ];
  for (const [input, expected] of cases) {
    expect(mapSystemLocaleToSupported(input)).toBe(expected);
  }
});
