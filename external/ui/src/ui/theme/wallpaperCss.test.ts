import { stylesSource, stylesPartial } from "../../stylesSource";
import { expect, test } from "vitest";

const css = stylesSource();
const squash = (s: string) => s.replace(/\s+/g, " ").trim();
const dock = stylesPartial("settings/dock");

/** Every declaration the sheet writes for the delete control, at any state. */
function deleteDeclarations(): string[] {
  return [...dock.matchAll(/([^{}]*)\{([^}]*)\}/g)]
    .filter(([, selector]) =>
      (selector ?? "").includes(".appearance-wallpaper-delete"),
    )
    .flatMap(([, , body]) =>
      squash(body ?? "")
        .split(";")
        .map((declaration) => declaration.trim())
        .filter(Boolean),
    );
}

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

test("wallpaper gallery keeps two columns and filenames", () => {
  const flat = squash(dock);
  expect(flat).toMatch(
    /\.appearance-wallpaper-gallery \{ [^}]*grid-template-columns: repeat\(2, minmax\(0, 1fr\)\)/,
  );
  expect(flat).toMatch(
    /\.appearance-wallpaper-caption \{ [^}]*text-overflow: ellipsis/,
  );
});

// The delete control is the composer attachment's remove: a bare cross over the
// picture, invisible until the pointer or the keyboard comes to it. A framed
// trash plate over a thumbnail read as part of the picture and hid it.
test("the wallpaper delete control is a bare cross with a 40px hit area", () => {
  const flat = squash(dock);
  expect(flat).toMatch(
    /\.appearance-wallpaper-delete \{[^}]*width: 30px;[^}]*height: 30px/,
  );
  expect(flat).toMatch(
    /\.appearance-wallpaper-delete::after \{ [^}]*inset: -5px/,
  );
  // A light glyph with a hard dark edge stays legible over pictures in every theme.
  expect(flat).toMatch(
    /\.appearance-wallpaper-delete \{[^}]*font-size: 20px;[^}]*line-height: 1;/,
  );
  expect(flat).toMatch(
    /\.appearance-wallpaper-delete \{[^}]*color: rgba\(255, 255, 255, 0\.82\);/,
  );
  expect(flat).toMatch(
    /\.appearance-wallpaper-delete \{[^}]*-webkit-text-stroke: 0\.6px rgba\(0, 0, 0, 0\.8\);[^}]*paint-order: stroke fill;/,
  );
  expect(flat).toMatch(
    /\.appearance-wallpaper-delete:hover, \.appearance-wallpaper-delete:focus-visible \{ color: #fff; \}/,
  );
  // No plate in any state: no ground, no frame, no shadow, no destructive red.
  for (const declaration of deleteDeclarations()) {
    const property = declaration.split(":")[0]!.trim();
    const value = declaration.slice(property.length + 1).trim();
    if (property.startsWith("background"))
      expect(value, declaration).toBe("none");
    if (/^border(?!-radius)/.test(property))
      expect(value, declaration).toMatch(/^(0|none)$/);
    if (property.startsWith("box-shadow"))
      expect(value, declaration).toBe("none");
    expect(declaration).not.toContain("--coddy-danger");
  }
  // It fades in and out, as the attachment's does; simplified mode drops this.
  expect(flat).toMatch(
    /\.appearance-wallpaper-delete \{[^}]*transition: [^;}]*opacity 0\.15s/,
  );
});

// An invisible control that still takes pointer events would swallow the tap
// that selects the thumbnail under it, on every tile at once.
test("the wallpaper delete control is hidden until hovered, focused or tapped", () => {
  const flat = squash(dock);
  expect(flat).toMatch(
    /\.appearance-wallpaper-delete \{[^}]*opacity: 0;[^}]*pointer-events: none;/,
  );
  // The hover reveal belongs to a pointer, so a touch does not leave it up.
  const hoverReveal =
    "@media (hover: hover) { .appearance-wallpaper-tile:hover .appearance-wallpaper-delete { opacity: 1; pointer-events: auto; }";
  expect(flat).toContain(hoverReveal);
  expect(
    flat.match(
      /\.appearance-wallpaper-tile:hover \.appearance-wallpaper-delete/g,
    ),
  ).toHaveLength(1);
  // A keyboard reaches it without the tile's hover, and keeps its accent ring.
  expect(flat).toContain(
    ".appearance-wallpaper-delete:focus-visible { opacity: 1; pointer-events: auto; }",
  );
  expect(flat).toMatch(
    /\.appearance-wallpaper-delete:focus-visible, \.appearance-wallpaper-thumb:focus-visible \{ outline: 2px solid var\(--accent\); outline-offset: 2px; \}/,
  );
  // A finger has no hover, so a tap's first press must find the cross there.
  expect(flat).toContain(
    "@media (hover: none) { .appearance-wallpaper-delete { opacity: 1; pointer-events: auto; }",
  );
  // Mouse focus on the tile would keep the cross up after the pointer leaves.
  expect(flat).not.toContain(".appearance-wallpaper-tile:focus-within");
  // The two reveals are gated by input capability and nothing else, so a touch
  // never leaves the cross up and no width query decides it.
  expect(flat.match(/@media \(hover: hover\)/g)).toHaveLength(1);
  expect(flat.match(/@media \(hover: none\)/g)).toHaveLength(1);
  expect(
    flat.match(/\.appearance-wallpaper-delete \{ opacity: 1/g),
  ).toHaveLength(2);
});
