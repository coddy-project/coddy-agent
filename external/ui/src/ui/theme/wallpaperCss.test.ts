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
