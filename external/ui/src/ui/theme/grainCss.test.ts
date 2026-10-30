import { expect, test } from "vitest";
import { stylesSource } from "../../stylesSource";

const css = stylesSource();

// Prettier breaks long values over lines; compare rule bodies squashed.
const squash = (s: string) => s.replace(/\s+/g, " ").trim();

function rule(selector: string): string {
  const at = css.indexOf(`\n${selector} {`);
  expect(at, selector).toBeGreaterThanOrEqual(0);
  return squash(css.slice(at, css.indexOf("}", at)));
}

test("the canvas carries the grain over its gradients", () => {
  expect(rule("html")).toMatch(
    /^html \{ color-scheme: dark; background: var\(--coddy-grain\), radial-gradient\(/,
  );
});

test("the glass panels keep their blur and carry the grain over their tint", () => {
  expect(rule(".chat-header")).toContain("backdrop-filter: blur(14px);");
  expect(rule(".rail-pill")).toContain("backdrop-filter: blur(16px);");
  // the composer card heads the list of the shared glass rule
  const glass = css.slice(css.indexOf("\n.composer-card,\n"));
  expect(squash(glass.slice(0, glass.indexOf("}")))).toContain(
    "backdrop-filter: var(--coddy-glass-panel-backdrop);",
  );
  expect(rule("html :is(.chat-header, .rail-pill, .composer-card)")).toContain(
    "background-image: var(--coddy-grain);",
  );
});

test("the dark themes share one grain tile and the light theme has none", () => {
  // The default block, which every dark theme inherits. The stylesheet opens
  // with the SCSS names, so the block is looked for rather than taken as the
  // head of the file, and its body ends at the `}` on its own line.
  const at = css.indexOf(':root,\n[data-theme="dark"] {');
  expect(at).toBeGreaterThanOrEqual(0);
  const dark = squash(css.slice(at, css.indexOf("\n}", at)));
  expect(dark).toMatch(
    /^:root, \[data-theme="dark"\] \{.*--coddy-grain: url\("data:image\/png;base64,[A-Za-z0-9+/=]+"\);/,
  );
  expect(rule('[data-theme="light"]')).toContain("--coddy-grain: none;");
  // Those two and the simplified mode, which clears it in every theme.
  expect(css.match(/--coddy-grain:/g)).toHaveLength(3);
});
