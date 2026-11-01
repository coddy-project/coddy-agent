import { readdirSync, readFileSync, statSync } from "node:fs";
import { basename, join, relative } from "node:path";
import { describe, expect, it } from "vitest";
import { stylesEntryPath, stylesFiles } from "./stylesSource";
import { UI_THEME_IDS } from "./ui/theme/themeCookie";

// How the stylesheet is put together: styles.scss is the order of the cascade
// and nothing else, every partial under styles/ is in that order, and a theme
// file is one theme's palette.

const stylesDir = join(stylesEntryPath, "..", "styles");
const themesDir = join(stylesDir, "themes");

function scssFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return scssFiles(path);
    return name.endsWith(".scss") ? [path] : [];
  });
}

/** The source with its comments taken out, so a word in a comment is no rule. */
function code(path: string): string {
  return readFileSync(path, "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/^\s*\/\/.*$/gm, "");
}

function declaredTokens(path: string): string[] {
  return [...code(path).matchAll(/^\s*(--[\w-]+)\s*:/gm)].map((m) => m[1]!);
}

describe("styles.scss and its partials", () => {
  it("holds @use rules and comments only", () => {
    const lines = code(stylesEntryPath)
      .split("\n")
      .filter((line) => line.trim() !== "");
    expect(lines.length).toBeGreaterThan(0);
    for (const line of lines) expect(line).toMatch(/^@use "styles\/[^"]+";$/);
  });

  it("reaches every partial under styles/, so none is left out of the build", () => {
    const reached = new Set(stylesFiles());
    const orphans = scssFiles(stylesDir).filter((path) => !reached.has(path));
    expect(orphans.map((path) => relative(stylesDir, path))).toEqual([]);
  });

  // Sass emits a module where it is first used, so a partial that used another
  // partial with rules would move those rules up the cascade, whatever the
  // entry says.
  it("lets a partial use only the Sass helpers, so the entry alone sets the order", () => {
    for (const path of scssFiles(stylesDir)) {
      const uses = [...code(path).matchAll(/^@use "([^"]+)"/gm)].map(
        (m) => m[1]!,
      );
      for (const url of uses) {
        expect(url, relative(stylesDir, path)).toMatch(
          /^sass:|(^|\/)abstracts$/,
        );
      }
    }
  });

  it("names every partial once in the entry", () => {
    const uses = [
      ...readFileSync(stylesEntryPath, "utf8").matchAll(/^@use "([^"]+)"/gm),
    ].map((m) => m[1]!);
    expect(new Set(uses).size).toBe(uses.length);
  });
});

describe("theme files", () => {
  const palettes = readdirSync(themesDir)
    .filter((name) => name !== "_light-overrides.scss")
    .map((name) => basename(name, ".scss").replace(/^_/, ""));

  it("are one per theme the app offers", () => {
    expect([...palettes].sort()).toEqual([...UI_THEME_IDS].sort());
  });

  it("each hold one rule, the palette of their own theme", () => {
    for (const theme of palettes) {
      const source = code(join(themesDir, `_${theme}.scss`));
      const selectors = [...source.matchAll(/^([^\s@{}][^{}]*)\{/gm)].map((m) =>
        m[1]!.replace(/\s+/g, " ").trim(),
      );
      const own =
        theme === "dark"
          ? ':root, [data-theme="dark"]'
          : `[data-theme="${theme}"]`;
      expect(selectors, theme).toEqual([own]);
    }
  });

  it("declare none of the tokens every theme shares", () => {
    const shared = new Set(declaredTokens(join(stylesDir, "_tokens.scss")));
    expect(shared.size).toBeGreaterThan(0);
    for (const theme of palettes) {
      const redeclared = declaredTokens(
        join(themesDir, `_${theme}.scss`),
      ).filter((token) => shared.has(token));
      expect(redeclared, theme).toEqual([]);
    }
  });
});
