import { expect, test } from "vitest";
import { stylesSource } from "../../stylesSource";

const css = stylesSource();

test("the terminal uses the same centred width as the conversation", () => {
  const block = /^\.terminal-panel\s*\{([^}]+)\}/m.exec(css)?.[1] ?? "";
  expect(block).toMatch(/width:\s*calc\(100% - 28px\)/);
  expect(block).toMatch(/max-width:\s*920px/);
  expect(block).toMatch(/margin:\s*0 auto 14px/);
  expect(block).toMatch(/box-sizing:\s*border-box/);
});

test("the terminal stays centred in the chat column beside Files", () => {
  const block =
    /^\s*\.shell-main\.shell-files-open > \.terminal-panel\s*\{([^}]+)\}/m.exec(
      css,
    )?.[1] ?? "";
  expect(block).toContain("var(--coddy-files-panel-band)");
  expect(block).toContain("920px");
});

test("the terminal gives the Tasks panel the same room as the chat", () => {
  const block =
    /^\s*\.shell-main\.shell-tasks-open > \.terminal-panel\s*\{([^}]+)\}/m.exec(
      css,
    )?.[1] ?? "";
  expect(block).toContain("var(--coddy-tasks-reserve)");
  expect(block).toContain("920px");
  expect(block).toMatch(/margin-left:\s*max\(/);
  expect(block).toMatch(/margin-right:\s*calc\(/);
});

test("the command line has no separate Run button", () => {
  expect(css).not.toMatch(/\.terminal-input-row button/);
});
