import { afterEach, beforeEach, expect, test } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { AppearanceThemePicker } from "./AppearanceModal";
import { extensionThemeRegistry } from "../extensions/themeRegistry";
import { initLocale } from "../i18n/i18n";
import {
  CODDY_UI_THEME_COOKIE,
  readUiThemeCookie,
  UI_THEME_IDS,
} from "./themeCookie";

const entry = { id: "demo", enabled: true, sha256: "exact-digest" };
const manifest = JSON.stringify({
  themes: [
    {
      id: "ocean",
      title: "Ocean from manifest",
      mode: "light",
      styles: ["ocean.css"],
      swatch: { bg: "#e0f2fe", accent: "#0284c7", text: "#0c4a6e" },
    },
    { id: "ember", name: "Ember by name", styles: ["ember.css"] },
  ],
});

async function prepare() {
  await act(async () => {
    await extensionThemeRegistry.prepare([entry], async (_owner, path) =>
      path === "manifest.json" ? manifest : `/* ${path} */`,
    );
  });
}

beforeEach(() => {
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/"),
    configurable: true,
  });
  initLocale("en");
  document.documentElement.dataset.theme = "dark";
});

afterEach(() => {
  cleanup();
  extensionThemeRegistry.clear();
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.style.removeProperty("color-scheme");
  document
    .querySelectorAll("style[data-ext-theme]")
    .forEach((style) => style.remove());
});

test("renders built-ins and manifest swatches with manifest labels and colours", async () => {
  render(<AppearanceThemePicker />);
  await prepare();

  expect(
    screen.getAllByRole("button", { pressed: false }).length,
  ).toBeGreaterThan(UI_THEME_IDS.length);
  const ocean = screen.getByTestId("theme-swatch-ext:demo/ocean");
  expect(ocean).toHaveAttribute("aria-label", "Ocean from manifest");
  expect(ocean).toHaveTextContent("Ocean from manifest");
  expect(ocean.querySelector(".appearance-swatch-preview")).toHaveStyle({
    background: "#e0f2fe",
  });
  expect(ocean.querySelector(".appearance-swatch-label")).toHaveStyle({
    color: "#0c4a6e",
    background: "#e0f2fe",
  });
  expect(screen.getByTestId("theme-swatch-ext:demo/ember")).toHaveTextContent(
    "Ember by name",
  );
  for (const id of UI_THEME_IDS) {
    expect(screen.getByTestId(`theme-swatch-${id}`)).toBeInTheDocument();
  }
});

test("selecting a manifest swatch loads its CSS and a built-in removes it", async () => {
  render(<AppearanceThemePicker />);
  await prepare();

  fireEvent.click(screen.getByTestId("theme-swatch-ext:demo/ocean"));
  await waitFor(() => expect(readUiThemeCookie()).toBe("ext:demo/ocean"));
  expect(document.documentElement.style.colorScheme).toBe("light");
  expect(screen.getByTestId("theme-swatch-ext:demo/ocean")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  expect(
    document.querySelectorAll('style[data-ext-theme="ext:demo/ocean"]'),
  ).toHaveLength(1);

  fireEvent.click(screen.getByTestId("theme-swatch-nord"));
  await waitFor(() => expect(readUiThemeCookie()).toBe("nord"));
  expect(document.querySelectorAll("style[data-ext-theme]")).toHaveLength(0);
});
