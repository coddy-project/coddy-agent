import { stylesSource, stylesPartial } from "../../stylesSource";
import { expect, test } from "vitest";

const css = stylesSource();
const squash = (s: string) => s.replace(/\s+/g, " ").trim();

test("a wallpaper covers the viewport, dimmed toward the theme's canvas colour", () => {
  const body = squash(
    css.match(
      /html\[data-wallpaper="true"\]\[data-simple="on"\] \{([^}]+)\}/,
    )?.[1] ?? "",
  );
  expect(body).toContain("var(--coddy-wallpaper) center / cover no-repeat");
  expect(body).toContain(
    "color-mix( in srgb, var(--coddy-canvas-gradient-bottom) var(--coddy-wallpaper-dim, 40%), transparent )",
  );
  expect(body).toContain("background-attachment: fixed;");
});

test("a wallpaper remains visible in simplified mode", () => {
  const base = stylesPartial("base");
  expect(base).toMatch(
    /html\[data-wallpaper="true"\],\s*html\[data-wallpaper="true"\]\[data-simple="on"\] \{/,
  );
});

test("wallpaper leaves the native gradients, grain and panel glass in place without a backdrop canvas", () => {
  expect(css).not.toContain(".canvas-backdrop");
  expect(css).not.toContain("data-smooth-backdrop");
  expect(css).not.toContain("--coddy-smooth-");
  const base = stylesPartial("base");
  expect(base).toMatch(/html \{[^}]*background:\s*var\(--coddy-grain\),/);
  expect(base).toContain("1200px 600px at 30% 10%");
  expect(base).toContain("900px 500px at 80% 70%");
  expect(base).toContain("var(--coddy-canvas-glow-purple)");
  expect(base).toContain("var(--coddy-canvas-glow-indigo)");
  expect(base).toContain("background-image: var(--coddy-grain)");
  expect(stylesPartial("chat/screen")).toContain("backdrop-filter: blur(14px)");
  expect(stylesPartial("shell/rail")).toContain("backdrop-filter: blur(16px)");
  expect(stylesPartial("composer/card")).toContain(
    "backdrop-filter: var(--coddy-glass-panel-backdrop)",
  );
});

test("the compact wallpaper gallery keeps destructive controls beside selection", () => {
  const dock = stylesPartial("settings/dock");
  const rule = (selector: string) =>
    squash(
      dock.match(
        new RegExp(
          selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + " \\{([^}]+)\\}",
        ),
      )?.[1] ?? "",
    );
  expect(rule(".appearance-wallpaper-heading")).toContain("display: flex;");
  expect(rule(".appearance-wallpaper-actions")).toContain("margin-left: auto;");
  expect(rule(".appearance-wallpaper-tile")).toContain("position: relative;");
  expect(rule(".appearance-wallpaper-delete")).toContain("position: absolute;");
  expect(rule(".appearance-wallpaper-delete")).toContain("top: 2px;");
  expect(rule(".appearance-wallpaper-delete")).toContain("right: 2px;");
  expect(rule(".appearance-wallpaper-delete")).toContain("width: 30px;");
  expect(rule(".appearance-wallpaper-delete")).toContain("height: 30px;");
  expect(rule(".appearance-wallpaper-thumb.is-selected")).toContain(
    "border-color: var(--accent);",
  );
  expect(dock).toContain(".appearance-wallpaper-thumb:hover:not(.is-selected)");
  expect(dock).not.toContain(".appearance-wallpaper-preview");
});

test("wallpaper gallery uses two columns and a compact trash with a 40px hit area", () => {
  const dock = stylesPartial("settings/dock");
  expect(dock).toMatch(
    /\.appearance-wallpaper-gallery\s*\{[^}]*grid-template-columns:\s*repeat\(2,\s*minmax\(0,\s*1fr\)\)/,
  );
  expect(dock).toMatch(
    /\.appearance-wallpaper-caption\s*\{[^}]*text-overflow:\s*ellipsis/,
  );
  expect(dock).toMatch(
    /\.appearance-wallpaper-delete\s*\{[^}]*width:\s*30px;[^}]*height:\s*30px/,
  );
  expect(dock).toMatch(
    /\.appearance-wallpaper-delete::after\s*\{[^}]*inset:\s*-5px/,
  );
});
