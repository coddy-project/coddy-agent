import React, {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useSyncExternalStore,
} from "react";
import {
  getLocale,
  onLocaleChange,
  translate,
  type TranslateParams,
} from "./i18n";
import type { UiLocale } from "./localeCookie";

type I18nContextValue = {
  locale: UiLocale;
  t: (key: string, params?: TranslateParams) => string;
};

const I18nContext = createContext<I18nContextValue | null>(null);

export function I18nProvider(props: { children: React.ReactNode }) {
  const locale = useSyncExternalStore(
    onLocaleChange,
    getLocale,
    () => "en" as UiLocale,
  );

  const t = useCallback(
    (key: string, params?: TranslateParams) => translate(key, params),
    [locale],
  );

  const value = useMemo(() => ({ locale, t }), [locale, t]);

  return (
    <I18nContext.Provider value={value}>{props.children}</I18nContext.Provider>
  );
}

export function useT(): I18nContextValue {
  const ctx = useContext(I18nContext);
  if (ctx) {
    return ctx;
  }
  return {
    locale: getLocale(),
    t: translate,
  };
}
