import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../styles.css",
);

function cssText(): string {
  return readFileSync(cssPath, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
}

function ruleBody(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return (
    new RegExp(`(?:^|})\\s*${escaped}\\s*\\{([^}]*)\\}`).exec(cssText())?.[1] ??
    ""
  );
}

// The server row leads with its chevron and status dot: no rule sizes or dims
// a leading <svg> of the row head any more, like the installed skills list.
test("an MCP server row has no leading icon rule", () => {
  expect(cssText()).not.toMatch(/\.mcp-list-item-head\s*>\s*svg/);
});

// Everything under a server row starts where its name does: the chevron, its
// gap, the status dot and its gap are the inset, and one variable holds it so
// the tools, the empty-tools line and the trust note cannot drift apart.
test("the tools and the trust note start where the server name does", () => {
  expect(ruleBody(".mcp-list")).toMatch(
    /--mcp-row-inset:\s*calc\(22px \+ 8px \+ 9px \+ 8px\)/,
  );
  expect(ruleBody(".mcp-tools")).toMatch(
    /padding:\s*0 0 0 var\(--mcp-row-inset\)/,
  );
  expect(ruleBody(".mcp-tools-empty")).toMatch(
    /padding-left:\s*var\(--mcp-row-inset\)/,
  );
  expect(ruleBody(".mcp-trust-note")).toMatch(
    /margin:\s*6px 0 0 var\(--mcp-row-inset\)/,
  );
});
