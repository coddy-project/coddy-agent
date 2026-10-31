import { afterEach, describe, expect, test, vi } from "vitest";
import {
  discoverThemeTokens,
  ExtensionThemes,
  readExtensionEffects,
  validOverrideKey,
  validOverrideValue,
} from "./theme";
import type { ExtensionTheme } from "./theme";

const theme = (id: "dark" | "light"): ExtensionTheme =>
  Object.freeze({
    id: id as never,
    mode: id === "light" ? ("light" as const) : ("dark" as const),
    colors: Object.freeze({}) as never,
  });

const quiet = () =>
  new ExtensionThemes(
    vi.fn(),
    () => theme("dark"),
    () => () => {},
    () => Object.freeze({ reducedMotion: false, engine: "modern" }),
  );

afterEach(() => {
  document
    .querySelectorAll("style[data-ext-theme-override]")
    .forEach((el) => el.remove());
});

describe("theme overrides and effects", () => {
  test("an override paints its tokens and remove drops the layer", () => {
    const themes = quiet();
    const controller = new AbortController();
    const api = themes.api("demo", controller.signal);
    const handle = api.override({ bg: "#101010", accent: "tomato" });
    const style = document.querySelector("[data-ext-theme-override='demo']");
    // The selector out-specifies the theme layer's own root rules.
    expect(style?.textContent).toBe(
      "html[data-theme], :root{--bg: #101010;--accent: tomato}",
    );
    handle.update({ bg: "#202020" });
    expect(
      document.querySelector("[data-ext-theme-override='demo']")?.textContent,
    ).toBe("html[data-theme], :root{--bg: #202020}");
    handle.remove();
    expect(
      document.querySelector("[data-ext-theme-override='demo']"),
    ).toBeNull();
  });

  test("an override reaches any theme token, not only the eight colours", () => {
    const themes = quiet();
    const api = themes.api("demo", new AbortController().signal);
    api.override({
      "--coddy-surface-subtle": "#123456",
      "--syntax-keyword": "rgb(1, 2, 3)",
    });
    const text = document.querySelector(
      "[data-ext-theme-override='demo']",
    )?.textContent;
    expect(text).toContain("--coddy-surface-subtle: #123456");
    expect(text).toContain("--syntax-keyword: rgb(1, 2, 3)");
  });

  test("the lifetime signal drops the layer", () => {
    const themes = quiet();
    const controller = new AbortController();
    themes.api("demo", controller.signal).override({ bg: "#000" });
    controller.abort();
    expect(
      document.querySelector("[data-ext-theme-override='demo']"),
    ).toBeNull();
    expect(() =>
      themes.api("demo", controller.signal).override({ bg: "#000" }),
    ).toThrow(/disposed/);
  });

  test("a key that is no token, and a colour that is no colour, are refused", () => {
    expect(validOverrideKey("bg")).toBe(true);
    expect(validOverrideKey("--coddy-surface-subtle")).toBe(true);
    expect(validOverrideKey("--Bad Name")).toBe(false);
    expect(validOverrideKey("display")).toBe(false);
    expect(validOverrideValue("bg", "#fff")).toBe(true);
    expect(validOverrideValue("bg", "hsl(1 2% 3%)")).toBe(true);
    expect(validOverrideValue("bg", "red; } body { display:none")).toBe(false);
    expect(validOverrideValue("bg", "url(javascript:alert(1))")).toBe(false);
    expect(validOverrideValue("bg", "definitely-not-a-colour")).toBe(false);
    expect(validOverrideValue("bg", "")).toBe(false);
    // An arbitrary token takes any CSS value; only rules are refused.
    expect(validOverrideValue("--radius", "12px")).toBe(true);
    expect(validOverrideValue("--radius", "12px; color: red")).toBe(false);
    const themes = quiet();
    expect(() =>
      themes
        .api("demo", new AbortController().signal)
        .override({ bg: "red{}" }),
    ).toThrow(/Invalid theme override/);
    expect(() =>
      themes
        .api("demo", new AbortController().signal)
        .override({ display: "block" }),
    ).toThrow(/Invalid theme override/);
  });

  test("tokens are discovered from the app's own root rules", () => {
    const style = document.createElement("style");
    style.textContent =
      '[data-theme="dark"] { --found-a: #111; --found-b: 2px } .cls { --not-a-token: 1 }';
    document.head.append(style);
    try {
      const names = discoverThemeTokens();
      expect(names).toContain("--found-a");
      expect(names).toContain("--found-b");
      expect(names).not.toContain("--not-a-token");
    } finally {
      style.remove();
    }
  });

  test("effects listeners fire only when the snapshot changes", () => {
    let effects: { reducedMotion: boolean; engine: "modern" | "legacy" } = {
      reducedMotion: false,
      engine: "modern",
    };
    const themes = new ExtensionThemes(
      vi.fn(),
      () => theme("dark"),
      (onChange) => {
        onChange();
        return () => {};
      },
      () => Object.freeze(effects),
    );
    const listener = vi.fn();
    themes.api("demo", new AbortController().signal).onEffectsChange(listener);
    // The subscription fires changed() once at registration and once more
    // below; an unchanged snapshot delivers nothing.
    themes.changed();
    expect(listener).not.toHaveBeenCalled();
    effects = { reducedMotion: true, engine: "legacy" as const };
    themes.changed();
    expect(listener).toHaveBeenCalledOnce();
    expect(listener).toHaveBeenCalledWith(
      Object.freeze({ reducedMotion: true, engine: "legacy" }),
    );
    expect(
      themes.api("demo", new AbortController().signal).getEffects(),
    ).toEqual({ reducedMotion: true, engine: "legacy" });
  });

  test("effects read the host flags and the engine tier", () => {
    document.documentElement.dataset.effects = "reduced";
    expect(readExtensionEffects().reducedMotion).toBe(true);
    delete document.documentElement.dataset.effects;
  });
});
