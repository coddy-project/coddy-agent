import { messagesEn } from "./messages/en";
import { messagesRu } from "./messages/ru";

type UiLocaleDefinition = {
  id: string;
  labelKey: string;
  messages: Record<string, string>;
};

/**
 * Single registry for every locale shipped by the SPA. Adding a language means
 * adding its dictionary and one entry here; the picker, validation, bootstrap,
 * and parity tests all derive their supported locale set from this object.
 */
export const UI_LOCALES = {
  en: {
    id: "en",
    labelKey: "appearance.locale.en",
    messages: messagesEn,
  },
  ru: {
    id: "ru",
    labelKey: "appearance.locale.ru",
    messages: messagesRu,
  },
} as const satisfies Record<string, UiLocaleDefinition>;

export type UiLocale = keyof typeof UI_LOCALES;

export const UI_LOCALE_DEFAULT: UiLocale = "en";

export const UI_LOCALE_IDS = Object.keys(UI_LOCALES) as UiLocale[];

export function isUiLocale(value: string): value is UiLocale {
  return Object.prototype.hasOwnProperty.call(UI_LOCALES, value);
}
