import { readFileSync } from "node:fs";
import { afterEach, expect, test, vi } from "vitest";
import { stylesFiles } from "../../stylesSource";
import { readExtensionThemeTokens } from "./theme";
import {
  EXTENSION_THEME_TOKENS,
  THEME_TOKEN_CONTRACT_VERSION,
} from "./themeTokens";

const paletteOrSharedTokens =
  /[\\/]styles[\\/](?:themes[\\/]_(?!light-overrides)[^\\/]+|_tokens)\.scss$/;

afterEach(() => {
  document.querySelectorAll("style[data-theme-token-test]").forEach((el) => {
    el.remove();
  });
});

test("the frozen token contract exactly matches the built-in palettes and shared tokens", () => {
  const sources = stylesFiles().filter((file) =>
    paletteOrSharedTokens.test(file),
  );
  expect(sources).toHaveLength(9);
  const declared = new Set<string>();
  for (const file of sources) {
    const source = readFileSync(file, "utf8");
    for (const name of source.match(/--[a-zA-Z_][a-zA-Z0-9_-]*(?=\s*:)/g) ?? [])
      declared.add(name);
  }
  expect([...EXTENSION_THEME_TOKENS]).toEqual([...declared].sort());
  expect(EXTENSION_THEME_TOKENS).toHaveLength(86);
  expect(Object.isFrozen(EXTENSION_THEME_TOKENS)).toBe(true);
  expect(THEME_TOKEN_CONTRACT_VERSION).toBe(1);
});

test("token reads return the whole contract, computed values and empty strings", () => {
  const getPropertyValue = vi.fn((name: string) =>
    name === "--bg" ? "  " : `computed(${name})`,
  );
  const doc = {
    documentElement: document.documentElement,
    defaultView: {
      getComputedStyle: () => ({ getPropertyValue }),
    },
  } as unknown as Document;

  const tokens = readExtensionThemeTokens(doc);

  expect(Object.keys(tokens)).toEqual(EXTENSION_THEME_TOKENS);
  expect(tokens["--bg"]).toBe("");
  expect(tokens["--accent"]).toBe("computed(--accent)");
  expect(getPropertyValue).toHaveBeenCalledTimes(EXTENSION_THEME_TOKENS.length);
  expect(Object.isFrozen(tokens)).toBe(true);
});

test("injected extension styles cannot change the token contract shape", () => {
  const before = readExtensionThemeTokens();
  const style = document.createElement("style");
  style.dataset.themeTokenTest = "true";
  style.textContent =
    ':root, [data-theme="dark"] { --extension-private-token: red; --accent: #010203; }';
  document.head.append(style);

  const after = readExtensionThemeTokens();

  expect(Object.keys(after)).toEqual(EXTENSION_THEME_TOKENS);
  expect(Object.keys(after)).toEqual(Object.keys(before));
  expect(after).not.toHaveProperty("--extension-private-token");
  expect(Object.isFrozen(after)).toBe(true);
});
