import { afterEach, expect, test } from "vitest";
import { CODDY_UI_THEME_COOKIE } from "./themeCookie";
import {
  applyUiTheme,
  bootstrapUiThemeFromCookie,
  readAppliedUiTheme,
  resolveUiThemeMode,
  setUiTheme,
} from "./uiTheme";

afterEach(() => {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.style.removeProperty("color-scheme");
});

test("resolveUiThemeMode defaults to dark", () => {
  expect(resolveUiThemeMode(null)).toBe("dark");
  expect(resolveUiThemeMode("dark")).toBe("dark");
  expect(resolveUiThemeMode("light")).toBe("light");
});

test("applyUiTheme sets data-theme and color-scheme", () => {
  applyUiTheme("light");
  expect(document.documentElement.dataset.theme).toBe("light");
  expect(document.documentElement.style.colorScheme).toBe("light");
  applyUiTheme("dark");
  expect(document.documentElement.dataset.theme).toBe("dark");
  expect(document.documentElement.style.colorScheme).toBe("dark");
});

test("setUiTheme persists cookie and applies", () => {
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/"),
    configurable: true,
  });
  setUiTheme("light");
  expect(readAppliedUiTheme()).toBe("light");
  expect(document.cookie).toContain(`${CODDY_UI_THEME_COOKIE}=light`);
  bootstrapUiThemeFromCookie();
  expect(readAppliedUiTheme()).toBe("light");
});
