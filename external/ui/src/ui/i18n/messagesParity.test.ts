import { expect, test } from "vitest";
import { UI_LOCALES, UI_LOCALE_DEFAULT, UI_LOCALE_IDS } from "./locales";

test("every registered dictionary exposes the default locale keys", () => {
  const defaultKeys = new Set(
    Object.keys(UI_LOCALES[UI_LOCALE_DEFAULT].messages),
  );
  for (const locale of UI_LOCALE_IDS) {
    const localeKeys = new Set(Object.keys(UI_LOCALES[locale].messages));
    const missing = [...defaultKeys].filter((key) => !localeKeys.has(key));
    const extra = [...localeKeys].filter((key) => !defaultKeys.has(key));
    expect({ locale, missing, extra }).toEqual({
      locale,
      missing: [],
      extra: [],
    });
  }
});

test("no dictionary value is the empty string", () => {
  for (const locale of UI_LOCALE_IDS) {
    for (const [key, value] of Object.entries(UI_LOCALES[locale].messages)) {
      expect(value, `empty ${locale} value for ${key}`).not.toBe("");
    }
  }
});

test("every interpolation token used in the default locale exists everywhere", () => {
  const token = (s: string) =>
    (s.match(/\{(\w+)\}/g) ?? []).map((m) => m).sort();
  const defaults = UI_LOCALES[UI_LOCALE_DEFAULT].messages;
  for (const locale of UI_LOCALE_IDS) {
    for (const [key, fallback] of Object.entries(defaults)) {
      const translated = UI_LOCALES[locale].messages[key];
      if (translated === undefined) continue;
      expect(token(translated), `token mismatch for ${locale}:${key}`).toEqual(
        token(fallback),
      );
    }
  }
});
