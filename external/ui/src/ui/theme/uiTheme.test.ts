import { afterEach, expect, test } from "vitest";
import {
  CODDY_UI_THEME_COOKIE,
  LIGHT_THEMES,
  UI_THEME_IDS,
} from "./themeCookie";
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
  for (const id of [...LIGHT_THEMES]) {
    if (id.startsWith("ext:")) LIGHT_THEMES.delete(id);
  }
});

test("resolveUiThemeMode defaults to dark for null/unknown", () => {
  expect(resolveUiThemeMode(null)).toBe("dark");
  expect(resolveUiThemeMode("unknown" as never)).toBe("dark");
});

test("resolveUiThemeMode returns any valid theme unchanged", () => {
  for (const id of UI_THEME_IDS) {
    expect(resolveUiThemeMode(id)).toBe(id);
  }
});

test("applyUiTheme sets data-theme to exact theme id", () => {
  for (const id of UI_THEME_IDS) {
    applyUiTheme(id);
    expect(document.documentElement.dataset.theme).toBe(id);
  }
});

test("applyUiTheme sets color-scheme=light only for light theme", () => {
  applyUiTheme("light");
  expect(document.documentElement.style.colorScheme).toBe("light");

  applyUiTheme("dark");
  expect(document.documentElement.style.colorScheme).toBe("dark");

  applyUiTheme("midnight");
  expect(document.documentElement.style.colorScheme).toBe("dark");

  applyUiTheme("nord");
  expect(document.documentElement.style.colorScheme).toBe("dark");

  applyUiTheme("rose-pine");
  expect(document.documentElement.style.colorScheme).toBe("dark");
});

test("readAppliedUiTheme returns current data-theme", () => {
  for (const id of UI_THEME_IDS) {
    applyUiTheme(id);
    expect(readAppliedUiTheme()).toBe(id);
  }
});

test("readAppliedUiTheme defaults to dark when attribute absent", () => {
  document.documentElement.removeAttribute("data-theme");
  expect(readAppliedUiTheme()).toBe("dark");
});

test("setUiTheme persists cookie and applies theme", () => {
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/"),
    configurable: true,
  });
  setUiTheme("nord");
  expect(readAppliedUiTheme()).toBe("nord");
  expect(document.cookie).toContain(`${CODDY_UI_THEME_COOKIE}=nord`);
});

test("setUiTheme light sets color-scheme=light", () => {
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/"),
    configurable: true,
  });
  setUiTheme("light");
  expect(readAppliedUiTheme()).toBe("light");
  expect(document.documentElement.style.colorScheme).toBe("light");
  bootstrapUiThemeFromCookie();
  expect(readAppliedUiTheme()).toBe("light");
});

test("extension ids are accepted without accepting arbitrary strings", () => {
  expect(resolveUiThemeMode("ext:demo/ocean")).toBe("ext:demo/ocean");
  expect(resolveUiThemeMode("ext:demo/Ocean" as never)).toBe("dark");
  applyUiTheme("ext:demo/ocean");
  expect(readAppliedUiTheme()).toBe("ext:demo/ocean");
  document.documentElement.dataset.theme = "ext:any arbitrary text";
  expect(readAppliedUiTheme()).toBe("dark");
});

test("a manifest light theme drives color-scheme and built-ins stay unchanged", () => {
  LIGHT_THEMES.add("ext:demo/daylight");
  applyUiTheme("ext:demo/daylight");
  expect(document.documentElement.style.colorScheme).toBe("light");
  applyUiTheme("dark");
  expect(document.documentElement.style.colorScheme).toBe("dark");
});
