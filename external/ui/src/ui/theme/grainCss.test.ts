import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
  "utf8",
);

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
  // the default block, which every dark theme inherits, and the light one
  expect(squash(css.slice(0, css.indexOf("}")))).toMatch(
    /^:root, \[data-theme="dark"\] \{.*--coddy-grain: url\("data:image\/png;base64,[A-Za-z0-9+/=]+"\);/,
  );
  expect(rule('[data-theme="light"]')).toContain("--coddy-grain: none;");
  expect(css.match(/--coddy-grain:/g)).toHaveLength(2);
});
