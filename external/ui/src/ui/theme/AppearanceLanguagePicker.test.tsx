import { afterEach, beforeEach, expect, test } from "vitest";
import { cleanup, render, screen, fireEvent } from "@testing-library/react";
import { AppearanceLanguagePicker } from "./AppearanceModal";
import { CODDY_UI_LANG_COOKIE } from "../i18n/localeCookie";
import { initLocale } from "../i18n/i18n";

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

test("renders the three Auto / English / Русский options", () => {
  render(<AppearanceLanguagePicker />);
  expect(screen.getByTestId("lang-option-auto")).toBeTruthy();
  expect(screen.getByTestId("lang-option-en")).toBeTruthy();
  expect(screen.getByTestId("lang-option-ru")).toBeTruthy();
});

test("Auto is active when no cookie is stored", () => {
  render(<AppearanceLanguagePicker />);
  expect(
    screen.getByTestId("lang-option-auto").getAttribute("data-active-locale"),
  ).toBe("true");
  expect(
    screen.getByTestId("lang-option-en").getAttribute("data-active-locale"),
  ).toBe("false");
});

test("clicking Русский persists the cookie and switches the labels to russian", () => {
  render(<AppearanceLanguagePicker />);
  fireEvent.click(screen.getByTestId("lang-option-ru"));
  expect(readCookie()).toBe("ru");
  expect(document.documentElement.lang).toBe("ru");
  // Section heading is now Russian.
  expect(screen.getByText("Язык")).toBeTruthy();
  // Русский is now the active option.
  expect(
    screen.getByTestId("lang-option-ru").getAttribute("data-active-locale"),
  ).toBe("true");
});

test("clicking Auto clears the cookie", () => {
  // Start from an explicit ru choice.
  document.cookie = `${CODDY_UI_LANG_COOKIE}=ru; Path=/`;
  render(<AppearanceLanguagePicker />);
  expect(
    screen.getByTestId("lang-option-ru").getAttribute("data-active-locale"),
  ).toBe("true");
  fireEvent.click(screen.getByTestId("lang-option-auto"));
  expect(readCookie()).toBeNull();
  expect(
    screen.getByTestId("lang-option-auto").getAttribute("data-active-locale"),
  ).toBe("true");
});
