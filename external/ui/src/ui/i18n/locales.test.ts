import { expect, test } from "vitest";
import {
  isUiLocale,
  UI_LOCALES,
  UI_LOCALE_DEFAULT,
  UI_LOCALE_IDS,
} from "./locales";

test("locale ids and metadata come from one registry", () => {
  expect(UI_LOCALE_IDS).toEqual(Object.keys(UI_LOCALES));
  for (const locale of UI_LOCALE_IDS) {
    expect(UI_LOCALES[locale].id).toBe(locale);
    expect(UI_LOCALES[locale].labelKey).toBe(`appearance.locale.${locale}`);
  }
});

test("locale registry exposes a valid default and rejects unknown ids", () => {
  expect(isUiLocale(UI_LOCALE_DEFAULT)).toBe(true);
  expect(isUiLocale("de")).toBe(false);
});
