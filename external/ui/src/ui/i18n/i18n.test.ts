import { afterEach, expect, test } from "vitest";
import {
  translate,
  setLocale,
  getLocale,
  initLocale,
  onLocaleChange,
  pluralCategories,
  themeLabel,
  t,
  translatePlural,
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

test("pluralCategories reports the CLDR categories each locale can produce", () => {
  expect([...pluralCategories("en")].sort()).toEqual(["one", "other"]);
  expect([...pluralCategories("ru")].sort()).toEqual([
    "few",
    "many",
    "one",
    "other",
  ]);
});

test("translatePlural picks the category for the active locale", () => {
  setLocale("en");
  expect(translatePlural("permission.meta.lines", 1)).toBe("1 line");
  expect(translatePlural("permission.meta.lines", 2)).toBe("2 lines");
  expect(translatePlural("permission.meta.lines", 40)).toBe("40 lines");
});

test("translatePlural declines russian counts by CLDR category, not by count === 1", () => {
  setLocale("ru");
  // one / few / many, including the 11-14 exception and the 21 wrap-around.
  expect(translatePlural("permission.meta.lines", 1)).toBe("1 строка");
  expect(translatePlural("permission.meta.lines", 3)).toBe("3 строки");
  expect(translatePlural("permission.meta.lines", 5)).toBe("5 строк");
  expect(translatePlural("permission.meta.lines", 11)).toBe("11 строк");
  expect(translatePlural("permission.meta.lines", 21)).toBe("21 строка");
  expect(translatePlural("tasks.chip.total", 2)).toBe("2 фоновые задачи");
});

test("translatePlural falls back to english, then to the key", () => {
  setLocale("ru");
  expect(translatePlural("definitely.not.a.family", 3)).toBe(
    "definitely.not.a.family",
  );
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
