import { afterEach, expect, test, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { Settings } from "./Settings";
import { resetSettingsConfigForTests } from "./settingsConfigStore";
import { I18nProvider } from "../i18n/I18nProvider";
import { initLocale, setLocale } from "../i18n/i18n";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  initLocale("en");
  // The app keeps the config Settings read; every test starts without it.
  resetSettingsConfigForTests();
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

// The drawer is as wide as three theme cards a row need, not wider: 680px of
// drawer leaves the Appearance grid about 432px (the 200px section rail and
// the panel's paddings take 248px), which auto-fill minmax(130px, 1fr) with a
// 10px gap turns into three columns of about 137px - the cards keep their
// size, the drawer gives way.
test("the settings drawer holds three theme cards a row", async () => {
  const { readFileSync } = await import("node:fs");
  const { dirname, join } = await import("node:path");
  const { fileURLToPath } = await import("node:url");
  const css = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
    "utf8",
  );
  const drawer = /^\.settings\.drawer\s*\{\s*width:\s*min\(\s*(\d+)px/m.exec(
    css,
  );
  expect(drawer).not.toBeNull();
  const grid = /^\.appearance-swatch-grid\s*\{([^}]*)\}/m.exec(css);
  expect(grid).not.toBeNull();
  const min = Number(/minmax\((\d+)px/.exec(grid![1]!)![1]);
  const gap = Number(/gap:\s*(\d+)px/.exec(grid![1]!)![1]);
  const content = Number(drawer![1]) - 248;
  expect(Math.floor((content + gap) / (min + gap))).toBe(3);
});

// Going from one open row straight to a row of another list
// (#/settings/models?id=demo/m1 to #/settings/providers?id=demo) must open
// that row. The tab catches up with the address one render late; in that
// render the models list used to take "demo" for one of its own rows, find
// none, and rewrite the address back to #/settings/models.
test("an address naming a row of another list opens it without rewriting the address", async () => {
  const schema = {
    type: "object",
    "x-coddy-property-order": ["providers", "models"],
    properties: {
      providers: {
        type: "array",
        title: "LLM providers",
        items: {
          type: "object",
          properties: { name: { type: "string", title: "Provider id" } },
        },
      },
      models: {
        type: "array",
        title: "Logical models",
        items: {
          type: "object",
          properties: { model: { type: "string", title: "Model id" } },
        },
      },
    },
  };
  const config = {
    providers: [{ name: "demo" }],
    models: [{ model: "demo/m1" }],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => ({
      ok: true,
      json: async () =>
        path.endsWith("/schema")
          ? schema
          : path.endsWith("/coddy/config")
            ? config
            : { ok: true, models: [] },
    })),
  );
  window.location.hash = "#/settings/models?id=demo%2Fm1";
  const writes: string[] = [];
  const replace = history.replaceState.bind(history);
  vi.spyOn(history, "replaceState").mockImplementation((data, unused, url) => {
    writes.push(String(url ?? ""));
    replace(data, unused, url);
  });

  const { rerender } = render(
    <Settings
      onClose={() => {}}
      initialSection="models"
      initialItem="demo/m1"
    />,
  );
  await screen.findByTestId("settings-head-back");

  window.location.hash = "#/settings/providers?id=demo";
  rerender(
    <Settings
      onClose={() => {}}
      initialSection="providers"
      initialItem="demo"
    />,
  );

  await waitFor(() =>
    expect(
      (screen.getByLabelText("Provider id") as HTMLInputElement).value,
    ).toBe("demo"),
  );
  expect(writes.filter((w) => w.endsWith("#/settings/models"))).toEqual([]);
});

// The drawer's head leads back from a row form on every width: titled after
// the form ("Provider settings"), its arrow closes it back onto the list and
// the head reads "Settings" again. The form carries no back link of its own.
test("the head titles an open row form and its arrow goes back to the list", async () => {
  const schema = {
    type: "object",
    properties: {
      providers: {
        type: "array",
        title: "LLM providers",
        items: {
          type: "object",
          properties: { name: { type: "string", title: "Provider id" } },
        },
      },
    },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => ({
      ok: true,
      json: async () =>
        path.endsWith("/schema")
          ? schema
          : path.endsWith("/coddy/config")
            ? { providers: [{ name: "demo" }] }
            : { ok: true, models: [] },
    })),
  );
  window.location.hash = "#/settings/providers";
  const { container } = render(
    <Settings onClose={() => {}} initialSection="providers" />,
  );

  const row = await screen.findByTestId("settings-master-item-0");
  expect(container.querySelector(".sessions-head")?.textContent).toContain(
    "Settings",
  );
  act(() => {
    row.click();
  });

  await waitFor(() =>
    expect(container.querySelector(".settings-head-section")?.textContent).toBe(
      "Provider settings",
    ),
  );
  expect(screen.queryByTestId("settings-detail-back")).toBeNull();
  const back = screen.getByTestId("settings-head-back");
  expect(back.getAttribute("aria-label")).toBe("Back to LLM providers");

  act(() => {
    back.click();
  });
  expect(screen.getByTestId("settings-master-item-0")).toBeTruthy();
  expect(container.querySelector(".settings-head-section")).toBeNull();
});
