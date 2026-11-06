import { afterEach, describe, expect, test, vi } from "vitest";
import {
  applyServerThemeDefaults,
  loadUiDefaults,
  parseUiDefaults,
  resetUiDefaultsCache,
} from "./uiDefaults";
import { readAppliedUiTheme } from "./uiTheme";

afterEach(() => {
  vi.unstubAllGlobals();
  resetUiDefaultsCache();
  document.cookie = "coddy_ui_theme=; Path=/; Max-Age=0";
  document.documentElement.dataset.theme = "dark";
});

describe("parseUiDefaults", () => {
  test("reads theme and wallpaper from the info answer", () => {
    expect(
      parseUiDefaults({
        object: "coddy.info",
        ui: { theme: "neuraldeep", wallpaper: "night.png" },
      }),
    ).toEqual({ theme: "neuraldeep", wallpaper: "night.png" });
  });

  test("an answer without ui parses to an empty record", () => {
    expect(parseUiDefaults({ object: "coddy.info" })).toEqual({});
    expect(parseUiDefaults(null)).toEqual({});
    expect(parseUiDefaults("nope")).toEqual({});
  });

  test("non-string members are ignored", () => {
    expect(parseUiDefaults({ ui: { theme: 5, wallpaper: null } })).toEqual({
      theme: undefined,
      wallpaper: undefined,
    });
  });
});

describe("loadUiDefaults", () => {
  test("fetches /coddy/info once and caches the parsed defaults", async () => {
    const fetch = vi.fn(async (_input: string) => ({
      ok: true,
      json: async () => ({ ui: { theme: "neuraldeep" } }),
    }));
    const first = await loadUiDefaults(fetch as never);
    const second = await loadUiDefaults(fetch as never);
    expect(first).toEqual({ theme: "neuraldeep" });
    expect(second).toEqual({ theme: "neuraldeep" });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0]?.[0]).toBe("/coddy/info");
  });

  test("a failed fetch resolves to empty defaults and is not retried", async () => {
    const fetch = vi.fn(async () => ({ ok: false, status: 500 }));
    expect(await loadUiDefaults(fetch as never)).toEqual({});
    expect(await loadUiDefaults(fetch as never)).toEqual({});
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});

describe("applyServerThemeDefaults", () => {
  test("applies the server theme while no cookie chooses one", () => {
    applyServerThemeDefaults({ theme: "neuraldeep" });
    expect(readAppliedUiTheme()).toBe("neuraldeep");
  });

  test("a theme the client does not know leaves the current one", () => {
    applyServerThemeDefaults({ theme: "sepia" });
    expect(readAppliedUiTheme()).toBe("dark");
  });

  test("a cookie wins over the server default", () => {
    document.cookie = "coddy_ui_theme=nord; Path=/";
    document.documentElement.dataset.theme = "nord";
    applyServerThemeDefaults({ theme: "neuraldeep" });
    expect(readAppliedUiTheme()).toBe("nord");
  });
});
