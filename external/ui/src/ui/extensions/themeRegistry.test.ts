import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import {
  ExtensionThemeRegistry,
  parseExtensionThemeManifest,
  type ExtensionThemeEntry,
  type FetchExtensionAssetText,
} from "./themeRegistry";
import {
  CODDY_UI_THEME_COOKIE,
  LIGHT_THEMES,
  readUiThemeCookie,
  type UiThemeMode,
} from "../theme/themeCookie";
import { setUiTheme } from "../theme/uiTheme";

const entry: ExtensionThemeEntry = {
  id: "demo",
  enabled: true,
  sha256: "package-digest",
};

const manifest = JSON.stringify({
  themes: [
    {
      id: "ocean",
      title: "Ocean blue",
      name: "Ignored when title exists",
      mode: "light",
      styles: ["themes/ocean.css"],
      swatch: { bg: "#eef8ff", accent: "#0369a1", text: "#0c4a6e" },
    },
    {
      id: "ember",
      name: "Ember",
      styles: ["ember.css", "ember-extra.css"],
    },
  ],
});

function selected(id: UiThemeMode) {
  setUiTheme(id);
}

beforeEach(() => {
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/"),
    configurable: true,
  });
});

afterEach(() => {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.style.removeProperty("color-scheme");
  document
    .querySelectorAll("style[data-ext-theme]")
    .forEach((style) => style.remove());
  for (const id of [...LIGHT_THEMES]) {
    if (id.startsWith("ext:")) LIGHT_THEMES.delete(id);
  }
});

describe("manifest themes", () => {
  test("parses public ids, labels, modes, styles and swatch fallbacks", () => {
    expect(parseExtensionThemeManifest("demo", manifest)).toEqual([
      {
        id: "ext:demo/ocean",
        owner: "demo",
        themeId: "ocean",
        title: "Ocean blue",
        mode: "light",
        styles: ["themes/ocean.css"],
        swatch: { bg: "#eef8ff", accent: "#0369a1", text: "#0c4a6e" },
      },
      {
        id: "ext:demo/ember",
        owner: "demo",
        themeId: "ember",
        title: "Ember",
        mode: "dark",
        styles: ["ember.css", "ember-extra.css"],
        swatch: { bg: "#121212", accent: "#9333ea", text: "#ffffff" },
      },
    ]);
  });

  test.each([
    ["themes is not an array", { themes: {} }],
    ["uppercase theme id", { themes: [{ id: "Ocean", styles: ["a.css"] }] }],
    ["empty theme id", { themes: [{ id: "", styles: ["a.css"] }] }],
    ["trailing hyphen", { themes: [{ id: "ocean-", styles: ["a.css"] }] }],
    [
      "invalid mode",
      { themes: [{ id: "ocean", mode: "auto", styles: ["a.css"] }] },
    ],
    ["missing styles", { themes: [{ id: "ocean" }] }],
    ["empty styles", { themes: [{ id: "ocean", styles: [] }] }],
    ["parent path", { themes: [{ id: "ocean", styles: ["../a.css"] }] }],
    ["non-CSS asset", { themes: [{ id: "ocean", styles: ["a.js"] }] }],
    [
      "partial swatch",
      {
        themes: [{ id: "ocean", styles: ["a.css"], swatch: { bg: "#fff" } }],
      },
    ],
  ])("rejects %s", (_name, value) => {
    expect(() =>
      parseExtensionThemeManifest("demo", JSON.stringify(value)),
    ).toThrow();
  });

  test("rejects duplicate ids in one manifest", () => {
    expect(() =>
      parseExtensionThemeManifest(
        "demo",
        JSON.stringify({
          themes: [
            { id: "ocean", styles: ["one.css"] },
            { id: "ocean", styles: ["two.css"] },
          ],
        }),
      ),
    ).toThrow(/duplicate/i);
  });

  test("a manifest without themes contributes none", () => {
    expect(parseExtensionThemeManifest("demo", "{}")).toEqual([]);
  });
});

describe("ExtensionThemeRegistry", () => {
  test("loads only the selected theme CSS before prepare returns", async () => {
    selected("ext:demo/ocean");
    const registry = new ExtensionThemeRegistry();
    const calls: { digest: string; path: string }[] = [];
    const fetchText: FetchExtensionAssetText = vi.fn(async (owner, path) => {
      calls.push({ digest: owner.sha256, path });
      if (path === "manifest.json") return manifest;
      return `/* ${path} */`;
    });

    await registry.prepare([entry], fetchText);

    expect(calls).toEqual([
      { digest: "package-digest", path: "manifest.json" },
      { digest: "package-digest", path: "themes/ocean.css" },
    ]);
    expect(registry.list().map((theme) => theme.id)).toEqual([
      "ext:demo/ocean",
      "ext:demo/ember",
    ]);
    expect(readUiThemeCookie()).toBe("ext:demo/ocean");
    expect(document.documentElement.dataset.theme).toBe("ext:demo/ocean");
    expect(document.documentElement.style.colorScheme).toBe("light");
    const styles = document.querySelectorAll(
      'style[data-ext-theme="ext:demo/ocean"]',
    );
    expect(styles).toHaveLength(1);
    expect(styles[0]?.textContent).toBe("/* themes/ocean.css */");
    registry.clear();
  });

  test("applies another dynamic theme on demand and removes it for a built-in", async () => {
    const registry = new ExtensionThemeRegistry();
    const paths: string[] = [];
    const fetchText: FetchExtensionAssetText = async (_owner, path) => {
      paths.push(path);
      return path === "manifest.json" ? manifest : `/* ${path} */`;
    };
    await registry.prepare([entry], fetchText);
    expect(paths).toEqual(["manifest.json"]);

    await expect(registry.apply("ext:demo/ember")).resolves.toBe(
      "ext:demo/ember",
    );
    expect(paths).toEqual(["manifest.json", "ember.css", "ember-extra.css"]);
    expect(readUiThemeCookie()).toBe("ext:demo/ember");
    expect(document.documentElement.style.colorScheme).toBe("dark");
    expect(
      document.querySelectorAll('style[data-ext-theme="ext:demo/ember"]'),
    ).toHaveLength(2);

    await expect(registry.apply("nord")).resolves.toBe("nord");
    expect(document.querySelectorAll("style[data-ext-theme]")).toHaveLength(0);
    expect(readUiThemeCookie()).toBe("nord");
    registry.clear();
  });

  test("falls back to dark and rewrites the cookie when the selection is revoked", async () => {
    selected("ext:demo/ocean");
    const registry = new ExtensionThemeRegistry();
    const fetchText: FetchExtensionAssetText = async (_owner, path) =>
      path === "manifest.json" ? manifest : "/* ocean */";
    const listener = vi.fn();
    registry.subscribe(listener);
    await registry.prepare([entry], fetchText);
    expect(listener).toHaveBeenCalledTimes(1);

    await registry.prepare([], fetchText);

    expect(registry.list()).toEqual([]);
    expect(listener).toHaveBeenCalledTimes(2);
    expect(readUiThemeCookie()).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(document.documentElement.style.colorScheme).toBe("dark");
    expect(document.querySelectorAll("style[data-ext-theme]")).toHaveLength(0);
    registry.clear();
  });

  test("falls back to dark when selected CSS fails without rejecting prepare", async () => {
    selected("ext:demo/ocean");
    const registry = new ExtensionThemeRegistry();
    const fetchText: FetchExtensionAssetText = async (_owner, path) => {
      if (path === "manifest.json") return manifest;
      throw new Error("asset missing");
    };

    await expect(registry.prepare([entry], fetchText)).resolves.toBeUndefined();

    expect(readUiThemeCookie()).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(document.querySelectorAll("style[data-ext-theme]")).toHaveLength(0);
    expect(registry.list()).toHaveLength(2);
    registry.clear();
  });

  test("rejects duplicate full ids across entries", async () => {
    const registry = new ExtensionThemeRegistry();
    const other = { ...entry, sha256: "another-digest" };
    const fetchText: FetchExtensionAssetText = async () => manifest;

    await expect(registry.prepare([entry, other], fetchText)).rejects.toThrow(
      /duplicate.*ext:demo\/ocean/i,
    );
    expect(registry.list()).toEqual([]);
    registry.clear();
  });
});
