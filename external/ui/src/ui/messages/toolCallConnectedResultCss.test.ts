import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../styles.css",
);

test("a structured tool preview and its result form one continuous card", () => {
  const css = readFileSync(cssPath, "utf8");
  const joinedResult = css.match(
    /\.coddy-tool-call-body--connected-result\s*>\s*\.permission-preview\s*\+\s*\.tool-call-result-card\s*\{[^}]*\}/s,
  );

  expect(joinedResult?.[0]).toMatch(/margin-top:\s*-8px/);
  expect(joinedResult?.[0]).toMatch(/border-top:\s*0/);
  expect(joinedResult?.[0]).toMatch(/border-radius:\s*0\s+0\s+12px\s+12px/);
  expect(css).toMatch(
    /\.coddy-tool-call-body--connected-result[^}]*\.permission-preview-viewport\s*\{[^}]*border-radius:\s*0/s,
  );
});
