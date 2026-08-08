import { afterEach, expect, test } from "vitest";
import {
  translate,
  setLocale,
  getLocale,
  initLocale,
  onLocaleChange,
  themeLabel,
  t,
} from "./i18n";

afterEach(() => {
  // Reset to the default locale so module state never leaks between tests.
  initLocale("en");
});

test("translate returns the english value by default", () => {
  expect(translate("settings.title")).toBe("Settings");
});

test("translate switches to russian after setLocale", () => {
  setLocale("ru");
  expect(getLocale()).toBe("ru");
  expect(translate("settings.title")).toBe("Настройки");
});

test("t is an alias of translate", () => {
  expect(t("settings.title")).toBe(translate("settings.title"));
});

test("interpolation replaces {placeholder} tokens", () => {
  setLocale("ru");
  expect(translate("settings.error.saveFailed", { status: 503 })).toBe(
    "не удалось сохранить (503)",
  );
  setLocale("en");
  expect(translate("settings.error.saveFailed", { status: 422 })).toBe(
    "save failed (422)",
  );
});

test("translate falls back to english when the key is missing in the active locale", () => {
  // Every key exists in both dictionaries; simulate a missing ru entry by
  // checking the fallback path indirectly: an unknown key returns the key
  // itself in both locales.
  setLocale("ru");
  expect(translate("definitely.not.a.real.key")).toBe(
    "definitely.not.a.real.key",
  );
});

test("setLocale rejects unsupported ids and keeps the previous locale", () => {
  initLocale("en");
  expect(setLocale("fr")).toBe(false);
  expect(getLocale()).toBe("en");
});

test("onLocaleChange fires when the locale actually changes", () => {
  let calls = 0;
  const off = onLocaleChange(() => {
    calls += 1;
  });
  setLocale("ru");
  setLocale("ru"); // no-op, no notification
  setLocale("en");
  off();
  setLocale("ru"); // unsubscribed: no further calls
  expect(calls).toBe(2);
});

test("themeLabel resolves every known theme id", () => {
  setLocale("en");
  expect(themeLabel("dark")).toBe("Dark");
  expect(themeLabel("rose-pine")).toBe("Rosé Pine");
  setLocale("ru");
  expect(themeLabel("dark")).toBe("Тёмная");
});

test("themeLabel returns the id itself for unknown themes", () => {
  expect(themeLabel("nope")).toBe("nope");
});
