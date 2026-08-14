import { afterEach, beforeEach, expect, test } from "vitest";
import { cleanup, render, screen, fireEvent } from "@testing-library/react";
import { AppearanceLanguagePicker } from "./AppearanceModal";
import { CODDY_UI_LANG_COOKIE } from "../i18n/localeCookie";
import { initLocale } from "../i18n/i18n";
import { UI_LOCALE_IDS } from "../i18n/locales";

function setLocation(href: string) {
  Object.defineProperty(window, "location", {
    value: new URL(href),
    configurable: true,
  });
}

beforeEach(() => {
  setLocation("http://127.0.0.1:5173/");
  document.cookie = `${CODDY_UI_LANG_COOKIE}=; Max-Age=0; Path=/`;
  document.documentElement.lang = "";
  initLocale("en");
});

afterEach(() => {
  cleanup();
  document.cookie = `${CODDY_UI_LANG_COOKIE}=; Max-Age=0; Path=/`;
  document.documentElement.lang = "";
  initLocale("en");
});

function readCookie(): string | null {
  const parts = document.cookie.split(";");
  for (const p of parts) {
    const s = p.trim();
    if (s.startsWith(`${CODDY_UI_LANG_COOKIE}=`)) {
      return decodeURIComponent(
        s.slice(CODDY_UI_LANG_COOKIE.length + 1).trim(),
      );
    }
  }
  return null;
}

test("renders one language select with Auto and every registered locale", () => {
  render(<AppearanceLanguagePicker />);
  const select = screen.getByRole("combobox", {
    name: "Language",
  }) as HTMLSelectElement;
  expect(select).toBe(screen.getByTestId("appearance-language-select"));
  expect([...select.options].map((option) => option.value)).toEqual([
    "auto",
    ...UI_LOCALE_IDS,
  ]);
});

test("Auto is selected when no cookie is stored", () => {
  render(<AppearanceLanguagePicker />);
  expect(screen.getByTestId("appearance-language-select")).toHaveValue("auto");
});

test("selecting Русский persists the cookie and switches the labels to russian", () => {
  render(<AppearanceLanguagePicker />);
  fireEvent.change(screen.getByTestId("appearance-language-select"), {
    target: { value: "ru" },
  });
  expect(readCookie()).toBe("ru");
  expect(document.documentElement.lang).toBe("ru");
  // Section heading is now Russian.
  expect(screen.getByText("Язык")).toBeTruthy();
  expect(screen.getByTestId("appearance-language-select")).toHaveValue("ru");
});

test("selecting Auto clears the cookie", () => {
  // Start from an explicit ru choice.
  document.cookie = `${CODDY_UI_LANG_COOKIE}=ru; Path=/`;
  render(<AppearanceLanguagePicker />);
  const select = screen.getByTestId("appearance-language-select");
  expect(select).toHaveValue("ru");
  fireEvent.change(select, { target: { value: "auto" } });
  expect(readCookie()).toBeNull();
  expect(select).toHaveValue("auto");
});
