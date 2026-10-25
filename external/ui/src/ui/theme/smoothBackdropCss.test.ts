import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";
import { BACKDROP_PANELS } from "./canvasBackdrop";

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
  "utf8",
);

// Prettier breaks long values over lines; compare rule bodies squashed.
const squash = (s: string) => s.replace(/\s+/g, " ").trim();

function rule(selector: string): string {
  // a rule of its own, at the start of a line
  const at = css.indexOf(`\n${selector} {`) + 1;
  expect(at, selector).toBeGreaterThan(0);
  return squash(css.slice(at, css.indexOf("}", at)));
}

test("in smooth mode each panel shows its painted image over its plain tint", () => {
  const panels: [string, string, string][] = [
    [".chat-header", "--coddy-smooth-header", "--coddy-chat-header-bg"],
    [".rail-pill", "--coddy-smooth-nav", "--nav"],
    [".composer-card", "--coddy-smooth-glass", "--coddy-glass-panel-bg"],
  ];
  for (const [sel, image, tint] of panels) {
    expect(rule(`html[data-smooth-backdrop="true"] ${sel}`)).toContain(
      `background: var(${image}, none) no-repeat fixed var(${tint});`,
    );
    expect(BACKDROP_PANELS).toContainEqual(
      expect.objectContaining({ selector: sel, tint, image }),
    );
  }
});

test("in smooth mode the panels drop their blur and soft shadows, and the composer its vignette", () => {
  const body = squash(
    css.slice(
      css.indexOf(
        'html[data-smooth-backdrop="true"] :is(.chat-header, .rail-pill, .composer-card)',
      ),
    ),
  );
  const first = body.slice(0, body.indexOf("}"));
  expect(first).toContain(
    'html[data-smooth-backdrop="true"] .composer-wrap-docked .composer-card',
  );
  expect(first).toContain("backdrop-filter: none;");
  expect(first).toContain("-webkit-backdrop-filter: none;");
  expect(first).toContain("box-shadow: none;");
  expect(
    squash(css).includes(
      'html[data-smooth-backdrop="true"] .chat-bottom:has(.composer-wrap-docked)::before { display: none; }',
    ),
  ).toBe(true);
});

test("the canvas sits under the app and takes no pointer", () => {
  const body = rule(".canvas-backdrop");
  expect(body).toContain("position: fixed;");
  expect(body).toContain("z-index: -1;");
  expect(body).toContain("pointer-events: none;");
});

test("over a wallpaper each panel's image is blurred the way its own glass is", () => {
  const filters = Object.fromEntries(
    BACKDROP_PANELS.map((p) => [p.selector, p.filter]),
  );
  expect(rule(".chat-header")).toContain(
    `backdrop-filter: ${filters[".chat-header"]};`,
  );
  expect(rule(".rail-pill")).toContain(
    `backdrop-filter: ${filters[".rail-pill"]};`,
  );
  expect(filters[".composer-card"]).toBe("var(--coddy-glass-panel-backdrop)");
});

test("a wallpaper covers the viewport, dimmed toward the theme's canvas colour", () => {
  const body = rule('html[data-wallpaper="true"]');
  expect(body).toContain("var(--coddy-wallpaper) center / cover no-repeat");
  expect(body).toContain(
    "color-mix( in srgb, var(--coddy-canvas-gradient-bottom) var(--coddy-wallpaper-dim, 40%), transparent )",
  );
  expect(body).toContain("background-attachment: fixed;");
});
