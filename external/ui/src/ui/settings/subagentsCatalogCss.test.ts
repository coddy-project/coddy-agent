import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../styles.css",
);

function cssText(): string {
  return readFileSync(cssPath, "utf8");
}

// Regression carried over from the port: a definition's absolute path, digest
// and tool list are long and unbreakable, and a <code> does not wrap on its own.
// Without these rules one project row widens the catalog fieldset past the
// settings panel and pushes the approve shield out of view.
test("the subagent catalog cannot grow wider than the settings panel", () => {
  const css = cssText();
  expect(css).toMatch(
    /\.settings-subagents-section \.subagents-catalog-box,[^{]*\{[^}]*min-inline-size:\s*0[^}]*max-width:\s*100%/s,
  );
  expect(css).toMatch(
    /\.settings-subagents-section \.mcp-trust-note code\s*\{[^}]*overflow-wrap:\s*anywhere/s,
  );
});

// The fact labels of a subagent declaration are longer than the MCP ones
// ("permissions", "инструменты"): the label column is widened, not left to
// break mid-word.
test("the declaration facts have room for their labels", () => {
  expect(cssText()).toMatch(
    /\.settings-subagents-section \.mcp-trust-facts dt\s*\{[^}]*flex:\s*0 0 104px/s,
  );
});

// A definition awaiting approval is a gate, not a suggestion: amber, the same
// colour as the approve shield.
test("the needs-approval badge uses the approval amber", () => {
  expect(cssText()).toMatch(
    /\.subagents-badge-pending\s*\{[^}]*color:\s*#ff9f0a/s,
  );
});
