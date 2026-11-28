import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";
import {
  CODDY_UI_THEME_COOKIE,
  UI_THEME_IDS,
  readUiThemeCookie,
  writeUiThemeCookie,
  type UiThemeMode,
} from "./themeCookie";

test("write then read ui theme cookie for all themes", () => {
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/"),
    configurable: true,
  });
  for (const id of UI_THEME_IDS) {
    document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
    writeUiThemeCookie(id);
    expect(readUiThemeCookie()).toBe(id);
  }
});

test("dark and light round-trip", () => {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/"),
    configurable: true,
  });
  writeUiThemeCookie("light");
  expect(readUiThemeCookie()).toBe("light");
  writeUiThemeCookie("dark");
  expect(readUiThemeCookie()).toBe("dark");
});

test("invalid cookie value is ignored", () => {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=sepia; Path=/`;
  expect(readUiThemeCookie()).toBeNull();
});

test("a strict extension theme id round-trips through the cookie", () => {
  writeUiThemeCookie("ext:demo-pack/ocean-blue");
  expect(readUiThemeCookie()).toBe("ext:demo-pack/ocean-blue");
  expect(document.cookie).toContain(
    `${CODDY_UI_THEME_COOKIE}=ext%3Ademo-pack%2Focean-blue`,
  );
});

test.each([
  "ext:demo",
  "ext:demo/Ocean",
  "ext:Demo/ocean",
  "ext:demo/ocean/extra",
  "ext:demo/ocean-",
  "ext:demo//ocean",
  `ext:demo/${"a".repeat(65)}`,
  "ext:any arbitrary text",
])("invalid extension cookie id %s is ignored", (id) => {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
  document.cookie = `${CODDY_UI_THEME_COOKIE}=${encodeURIComponent(id)}; Path=/`;
  expect(readUiThemeCookie()).toBeNull();
});

test("an invalid id cannot be written through a cast", () => {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
  writeUiThemeCookie("ext:demo/NOT-VALID" as UiThemeMode);
  expect(readUiThemeCookie()).toBeNull();
});

test("UI_THEME_IDS contains exactly 8 entries", () => {
  expect(UI_THEME_IDS).toHaveLength(8);
  const expected: UiThemeMode[] = [
    "dark",
    "neuraldeep",
    "light",
    "midnight",
    "solarized-dark",
    "monokai",
    "nord",
    "rose-pine",
  ];
  expect(UI_THEME_IDS).toEqual(expected);
});

test("index.html bootstrap accepts every UI_THEME_IDS entry", () => {
  const html = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "../../index.html"),
    "utf8",
  );
  for (const id of UI_THEME_IDS) {
    const entry = id.includes("-") ? `"${id}": 1` : `${id}: 1`;
    expect(html, `index.html VALID lacks ${id}`).toContain(entry);
  }
});
