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
  const at = css.indexOf(`${selector} {`);
  expect(at, selector).toBeGreaterThanOrEqual(0);
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
    expect(BACKDROP_PANELS).toContainEqual({ selector: sel, tint, image });
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
