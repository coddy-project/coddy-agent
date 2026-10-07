/**
 * Contract: a settings frame never widens the settings panel.
 *
 * A <fieldset> defaults to a min-content inline size. Nested frames - a
 * per-chat override inside a bot's block on the Gateways tab - added their
 * padding up past a 390px panel and scrolled it sideways. jsdom does no
 * layout, so the rule is pinned here.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

test("every settings fieldset may shrink below its content", () => {
  const body = [
    ...css.matchAll(/(?:^|\})\s*\.settings-fieldset\s*\{([^}]*)\}/g),
  ]
    .map((m) => m[1])
    .join(";");
  expect(body).toMatch(
    /(?:^|[;\s])(?:min-inline-size|min-width)\s*:\s*0(?:px)?\s*(?:;|$)/,
  );
});
