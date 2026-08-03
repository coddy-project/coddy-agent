import { afterEach, expect, test } from "vitest";
import { render, screen, cleanup, act } from "@testing-library/react";
import React from "react";
import { I18nProvider, useT } from "./I18nProvider";
import { initLocale, setLocale } from "./i18n";

function Consumer() {
  const { locale, t } = useT();
  return (
    <span data-testid="c">
      {locale}:{t("settings.title")}
    </span>
  );
}

afterEach(() => {
  cleanup();
  initLocale("en");
});

test("useT inside the provider reflects the active locale and re-renders on change", () => {
  initLocale("en");
  render(
    <I18nProvider>
      <Consumer />
    </I18nProvider>,
  );
  expect(screen.getByTestId("c").textContent).toBe("en:Settings");
  act(() => {
    setLocale("ru");
  });
  expect(screen.getByTestId("c").textContent).toBe("ru:Настройки");
});

test("useT outside the provider falls back to translate (english default)", () => {
  initLocale("en");
  render(<Consumer />);
  expect(screen.getByTestId("c").textContent).toBe("en:Settings");
});
