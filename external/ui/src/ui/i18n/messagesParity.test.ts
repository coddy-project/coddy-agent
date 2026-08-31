import { expect, test } from "vitest";
import { pluralCategories } from "./i18n";
import { UI_LOCALES, UI_LOCALE_DEFAULT, UI_LOCALE_IDS } from "./locales";

const CLDR_CATEGORIES = ["zero", "one", "two", "few", "many", "other"];

/**
 * Splits a dictionary into plain keys and plural families. A family is a base key the
 * default locale spells out as `base.one` + `base.other`; each locale then has to carry
 * exactly the categories its own CLDR rules can produce, so family members are compared
 * separately from the flat key set.
 */
function splitDictionary(dict: Record<string, string>): {
  plain: string[];
  families: string[];
} {
  const families = Object.keys(dict)
    .filter(
      (key) =>
        key.endsWith(".one") &&
        dict[`${key.slice(0, -".one".length)}.other`] !== undefined,
    )
    .map((key) => key.slice(0, -".one".length));
  const familySet = new Set(families);
  const plain = Object.keys(dict).filter(
    (key) => familyOf(key, familySet) === "",
  );
  return { plain, families };
}

/** The plural family a key belongs to, or "" when it is a plain key. */
function familyOf(key: string, families: Set<string>): string {
  const dot = key.lastIndexOf(".");
  if (dot < 0) return "";
  const base = key.slice(0, dot);
  return families.has(base) && CLDR_CATEGORIES.includes(key.slice(dot + 1))
    ? base
    : "";
}

test("every registered dictionary exposes the default locale keys", () => {
  const { plain, families } = splitDictionary(
    UI_LOCALES[UI_LOCALE_DEFAULT].messages,
  );
  for (const locale of UI_LOCALE_IDS) {
    const expected = new Set(plain);
    for (const base of families) {
      for (const category of pluralCategories(locale)) {
        expected.add(`${base}.${category}`);
      }
    }
    const localeKeys = new Set(Object.keys(UI_LOCALES[locale].messages));
    const missing = [...expected].filter((key) => !localeKeys.has(key)).sort();
    const extra = [...localeKeys].filter((key) => !expected.has(key)).sort();
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
  const families = new Set(splitDictionary(defaults).families);
  for (const locale of UI_LOCALE_IDS) {
    // Walk the locale's own keys so the extra categories a language carries (ru "few" /
    // "many") are checked against the family's default entry instead of being skipped.
    for (const [key, translated] of Object.entries(
      UI_LOCALES[locale].messages,
    )) {
      const base = familyOf(key, families);
      const fallback = base ? defaults[`${base}.other`] : defaults[key];
      if (fallback === undefined) continue;
      expect(token(translated), `token mismatch for ${locale}:${key}`).toEqual(
        token(fallback),
      );
    }
  }
});
