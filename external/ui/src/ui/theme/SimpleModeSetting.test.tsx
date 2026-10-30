import fs from "node:fs";
import path from "node:path";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { AppearanceThemePicker } from "./AppearanceModal";
import {
  bootstrapUiSimpleFromCookie,
  CODDY_UI_SIMPLE_COOKIE,
  motionIsReduced,
} from "./uiSimple";
import { initLocale } from "../i18n/i18n";

function clear() {
  document.cookie = `${CODDY_UI_SIMPLE_COOKIE}=; Max-Age=0; Path=/`;
  delete document.documentElement.dataset.simple;
}

beforeEach(() => {
  clear();
  initLocale("en");
});

afterEach(() => {
  cleanup();
  clear();
  vi.unstubAllGlobals();
});

const hasCookie = () =>
  document.cookie
    .split(";")
    .some((p) => p.trim() === `${CODDY_UI_SIMPLE_COOKIE}=1`);

const theSwitch = () => screen.getByTestId("appearance-simple-switch");

test("the switch stands between the theme grid and the language", () => {
  render(<AppearanceThemePicker />);
  const picker = screen.getByTestId("appearance-theme-picker");
  const blocks = [...picker.children];
  const grid = blocks.findIndex((b) =>
    b.querySelector(".appearance-swatch-grid"),
  );
  const simple = blocks.indexOf(screen.getByTestId("appearance-simple"));
  const language = blocks.indexOf(
    screen.getByTestId("appearance-language-picker"),
  );
  expect(grid).toBeGreaterThanOrEqual(0);
  expect(simple).toBe(grid + 1);
  expect(language).toBe(simple + 1);
});

test("the name of the mode is the switch's label, and what it turns off is in the tooltip", () => {
  render(<AppearanceThemePicker />);
  const block = screen.getByTestId("appearance-simple");
  expect(block.querySelector(".appearance-section-label")).toBeNull();
  expect(block.querySelector("label")?.textContent).toBe("Simplified mode");
  expect(theSwitch()).toHaveAccessibleName("Simplified mode");
  // The label says what it is, and nothing else stands next to the switch.
  expect(block.textContent).not.toContain("shadows");
  const hint = block.querySelector(
    ".settings-field-hint-btn, button[aria-label]:not([role=switch])",
  );
  expect(hint).not.toBeNull();
});

test("turning the switch on marks the page and the cookie, and turning it off clears both", async () => {
  render(<AppearanceThemePicker />);
  const checked = () => theSwitch().getAttribute("aria-checked");
  expect(checked()).toBe("false");
  expect(document.documentElement.dataset.simple).toBeUndefined();

  fireEvent.click(theSwitch());
  expect(document.documentElement.dataset.simple).toBe("on");
  expect(hasCookie()).toBe(true);
  await waitFor(() => expect(checked()).toBe("true"));

  fireEvent.click(theSwitch());
  expect(document.documentElement.dataset.simple).toBeUndefined();
  expect(hasCookie()).toBe(false);
  await waitFor(() => expect(checked()).toBe("false"));
});

test("a theme picked while the mode is on leaves it on", () => {
  render(<AppearanceThemePicker />);
  fireEvent.click(theSwitch());
  fireEvent.click(screen.getByTestId("theme-swatch-nord"));
  expect(document.documentElement.dataset.theme).toBe("nord");
  expect(document.documentElement.dataset.simple).toBe("on");
  expect(hasCookie()).toBe(true);
});

test("a page loaded with the cookie starts in the mode, before the first paint too", () => {
  document.cookie = `${CODDY_UI_SIMPLE_COOKIE}=1; Path=/`;
  bootstrapUiSimpleFromCookie();
  expect(document.documentElement.dataset.simple).toBe("on");
  render(<AppearanceThemePicker />);
  expect(theSwitch().getAttribute("aria-checked")).toBe("true");

  // The inline script of the page, which runs before the stylesheet paints.
  const html = fs.readFileSync(
    path.join(process.cwd(), "src/index.html"),
    "utf8",
  );
  expect(html).toContain(`${CODDY_UI_SIMPLE_COOKIE}=1`);
  expect(html).toContain('dataset.simple = "on"');
});

test("motion counts as reduced in the mode", () => {
  vi.stubGlobal("matchMedia", () => ({ matches: false }));
  expect(motionIsReduced()).toBe(false);
  document.documentElement.dataset.simple = "on";
  expect(motionIsReduced()).toBe(true);
  delete document.documentElement.dataset.simple;
  vi.stubGlobal("matchMedia", () => ({ matches: true }));
  expect(motionIsReduced()).toBe(true);
});
