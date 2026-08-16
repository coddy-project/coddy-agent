import { afterEach, expect, test, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { Settings } from "./Settings";
import { I18nProvider } from "../i18n/I18nProvider";
import { initLocale, setLocale } from "../i18n/i18n";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  initLocale("en");
});

// Regression: while the config schema is still loading (fetch pending, no error),
// the Appearance section rendered inside `.settings-scroll-placeholder` — a
// `display:flex; align-items:center; justify-content:center` box meant for the
// "Loading…" spinner. That shrank the theme swatch grid to its content width and
// off-centered it, so the swatches looked crookedly placed. Appearance is real
// (client-side) content and must render in the normal scroll flow so the grid
// fills the panel width.
test("appearance renders outside the centered loading placeholder", async () => {
  // Never-resolving fetch keeps schema null and loadErr null: the loading window.
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>(() => {})),
  );

  const { container } = render(
    <Settings onClose={() => {}} initialSection="appearance" />,
  );

  await waitFor(() =>
    expect(container.querySelector(".appearance-swatch-grid")).toBeTruthy(),
  );

  // The swatch grid must not be nested inside the centered placeholder box.
  expect(
    container.querySelector(
      ".settings-scroll-placeholder .appearance-swatch-grid",
    ),
  ).toBeNull();
});

test("section labels re-render when the locale changes", async () => {
  initLocale("en");
  const schema = {
    type: "object",
    "x-coddy-property-order": ["providers", "tools"],
    properties: {
      providers: {
        type: "array",
        title: "LLM providers",
        items: { type: "object" },
      },
      tools: {
        type: "object",
        title: "Tools and permissions",
        properties: {},
      },
    },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => ({
      ok: true,
      json: async () => (path.endsWith("/schema") ? schema : {}),
    })),
  );

  render(
    <I18nProvider>
      <Settings onClose={() => {}} initialSection="appearance" />
    </I18nProvider>,
  );

  await screen.findByRole("button", { name: "LLM providers" });
  act(() => {
    setLocale("ru");
  });

  await screen.findByRole("button", { name: "Провайдеры LLM" });
  expect(screen.getByRole("button", { name: "Оформление" })).toBeTruthy();
  expect(
    screen.getByRole("button", { name: "Инструменты и разрешения" }),
  ).toBeTruthy();
});
