import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, expect, test } from "vitest";
import { CODDY_UI_THEME_COOKIE } from "./themeCookie";

const html = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../../src/index.html"),
  "utf8",
);
const bootstrap = html.match(/<script>([\s\S]*?)<\/script>/)?.[1] ?? "";
if (!bootstrap)
  throw new Error("The built index has no first-paint theme script");

function paint(cookie: string) {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
  document.cookie = `${CODDY_UI_THEME_COOKIE}=${encodeURIComponent(cookie)}; Path=/`;
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.style.removeProperty("color-scheme");
  new Function(bootstrap)();
}

afterEach(() => {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.style.removeProperty("color-scheme");
});

test("first paint accepts a strict extension id", () => {
  paint("ext:demo-pack/ocean-blue");
  expect(document.documentElement.dataset.theme).toBe(
    "ext:demo-pack/ocean-blue",
  );
  // The manifest is not available to the inline bootstrap; prepare applies its
  // declared mode before the app commits. Dark is the safe provisional mode.
  expect(document.documentElement.style.colorScheme).toBe("dark");
});

test.each([
  "sepia",
  "ext:demo",
  "ext:demo/Ocean",
  "ext:Demo/ocean",
  "ext:demo/ocean/extra",
  "ext:demo/ocean-",
  "ext:any arbitrary string",
  `ext:demo/${"a".repeat(65)}`,
])("first paint rejects invalid id %s", (id) => {
  paint(id);
  expect(document.documentElement.dataset.theme).toBe("dark");
});

test("first paint keeps built-in light behavior", () => {
  paint("light");
  expect(document.documentElement.dataset.theme).toBe("light");
  expect(document.documentElement.style.colorScheme).toBe("light");
});
