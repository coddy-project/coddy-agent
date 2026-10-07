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

// Tools start at the whole-server switch, while text that follows a server row
// stays under its name. The two independent insets keep both columns stable.
test("the tools align with the server switch and text stays under the name", () => {
  expect(ruleBody(".mcp-list")).toMatch(
    /--mcp-row-inset:\s*calc\(var\(--mcp-switch-inset\) \+ 38px \+ 8px \+ 9px \+ 8px\)/,
  );
  expect(ruleBody(".mcp-tools")).toMatch(
    /padding:\s*0 0 0 var\(--mcp-switch-inset\)/,
  );
  expect(ruleBody(".mcp-tools-empty")).toMatch(
    /padding-left:\s*var\(--mcp-row-inset\)/,
  );
  expect(ruleBody(".mcp-trust-note")).toMatch(
    /margin:\s*6px 0 0 var\(--mcp-row-inset\)/,
  );
});

test("MCP switches share a left column and server rows have no bottom rule", () => {
  expect(ruleBody(".mcp-list")).toMatch(
    /--mcp-switch-inset:\s*calc\(22px \+ 8px\)/,
  );
  expect(ruleBody(".mcp-tools")).toMatch(
    /padding:\s*0 0 0 var\(--mcp-switch-inset\)/,
  );
  expect(ruleBody(".mcp-list-item")).not.toMatch(/border-bottom/);
});

// The trust note names the file a declaration came from, one long path with
// nothing to break at; it wraps anywhere rather than run past the row on a
// phone (42px at 360px wide before this rule).
test("the trust note wraps a long path instead of overflowing the row", () => {
  expect(ruleBody(".mcp-trust-note")).toMatch(/overflow-wrap:\s*anywhere/);
});
