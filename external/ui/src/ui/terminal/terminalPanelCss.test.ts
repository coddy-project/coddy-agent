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

test("the terminal and composer share one full-width measured block", () => {
  const block =
    /^\.chat-bottom-inner > \.terminal-panel\s*\{([^}]+)\}/m.exec(css)?.[1] ??
    "";
  expect(block).toMatch(/width:\s*100%/);
  expect(block).toMatch(/max-width:\s*none/);
  expect(block).toMatch(/margin:\s*0 0 8px/);
});

test("the adaptive terminal cannot consume the whole viewport", () => {
  expect(css).toMatch(/max-height:\s*min\(360px, 36svh\)/);
  expect(css).toMatch(/max-height:\s*30svh/);
});

test("the command line has no separate Run button", () => {
  expect(css).not.toMatch(/\.terminal-input-row button/);
});
