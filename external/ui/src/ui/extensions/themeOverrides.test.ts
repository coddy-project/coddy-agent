import { describe, expect, test, vi } from "vitest";
import {
  ExtensionThemes,
  readExtensionEffects,
  validOverrideColor,
} from "./theme";
import type { ExtensionTheme } from "./theme";

const theme = (id: "dark" | "light"): ExtensionTheme =>
  Object.freeze({
    id: id as never,
    mode: id === "light" ? ("light" as const) : ("dark" as const),
    colors: Object.freeze({}) as never,
  });

describe("theme overrides and effects", () => {
  test("an override paints its tokens and remove drops the layer", () => {
    const themes = new ExtensionThemes(
      vi.fn(),
      () => theme("dark"),
      () => () => {},
      () => Object.freeze({ reducedMotion: false, engine: "modern" }),
    );
    const controller = new AbortController();
    const api = themes.api("demo", controller.signal);
    const handle = api.override({ bg: "#101010", accent: "tomato" });
    const style = document.querySelector("[data-ext-theme-override='demo']");
    expect(style?.textContent).toBe(":root{--bg: #101010;--accent: tomato}");
    handle.update({ bg: "#202020" });
    expect(
      document.querySelector("[data-ext-theme-override='demo']")?.textContent,
    ).toBe(":root{--bg: #202020}");
    handle.remove();
    expect(
      document.querySelector("[data-ext-theme-override='demo']"),
    ).toBeNull();
  });

  test("the lifetime signal drops the layer", () => {
    const themes = new ExtensionThemes(
      vi.fn(),
      () => theme("dark"),
      () => () => {},
      () => Object.freeze({ reducedMotion: false, engine: "modern" }),
    );
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

  test("a colour that could carry a rule is refused", () => {
    expect(validOverrideColor("#fff")).toBe(true);
    expect(validOverrideColor("hsl(1 2% 3%)")).toBe(true);
    expect(validOverrideColor("red; } body { display:none")).toBe(false);
    expect(validOverrideColor("url(javascript:alert(1))")).toBe(false);
    expect(validOverrideColor("")).toBe(false);
    const themes = new ExtensionThemes(
      vi.fn(),
      () => theme("dark"),
      () => () => {},
      () => Object.freeze({ reducedMotion: false, engine: "modern" }),
    );
    expect(() =>
      themes
        .api("demo", new AbortController().signal)
        .override({ bg: "red{}" }),
    ).toThrow(/Invalid theme override/);
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
