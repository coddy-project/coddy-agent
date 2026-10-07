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

test("a command block and its output keep one gap at the seam, not two paddings", () => {
  const css = readFileSync(cssPath, "utf8");
  // The command block already carries its own inset inside the card, so the
  // output panel below it adds no top padding of its own.
  expect(css).toMatch(
    /\.coddy-tool-call-body--connected-result\s*>\s*\.permission-preview:has\(\.permission-preview-shell\)\s*\+\s*\.tool-call-result-card\s+\.tool-call-result-content\s*\{[^}]*padding-top:\s*0/s,
  );
});

test("background transcript rows leave task actions to the Tasks panel", () => {
  const css = readFileSync(cssPath, "utf8");
  expect(css).not.toMatch(/\.tool-bgtask-actions\b/);
});

test("every truncated result keeps its More tab attached to the output card", () => {
  const css = readFileSync(cssPath, "utf8");
  const toggle = css.match(
    /\.coddy-tool-call-body\s*>\s*\.tool-result-toggle-row\s*\{([^}]*)\}/s,
  );
  expect(toggle?.[1]).toMatch(/margin:\s*-8px\s+0\s+2px/);
  expect(toggle?.[1]).toMatch(/align-self:\s*flex-start/);
});

test("a background row carries no status block of its own on the summary line", () => {
  const css = readFileSync(cssPath, "utf8");
  // The row shows the task's clock in the ordinary `.thinking-dur` slot; the
  // status, the estimate and the exit code are read in the Tasks panel.
  expect(css).not.toMatch(/\.tool-bgtask-chip\b/);
  expect(css).not.toMatch(/\.tool-bgtask-state\b/);
});
