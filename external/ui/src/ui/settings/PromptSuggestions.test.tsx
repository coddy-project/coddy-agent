import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { mountSettings, resetPage } from "./settingsServer.fakes";

afterEach(() => {
  cleanup();
  resetPage();
});

test("prompt suggestions default on and save both switch positions", async () => {
  const { server } = mountSettings({ section: "agent", config: { agent: {} } });
  const toggle = await screen.findByRole("switch", {
    name: "Prompt suggestions",
  });
  expect(toggle.getAttribute("aria-checked")).toBe("true");
  fireEvent.click(toggle);
  fireEvent.click(screen.getByTestId("settings-save"));
  await waitFor(() =>
    expect(server.config).toMatchObject({
      agent: { prompt_suggestions: false },
    }),
  );
  await waitFor(() =>
    expect(screen.getByTestId("settings-save").className).toContain("is-saved"),
  );
  fireEvent.click(toggle);
  fireEvent.click(screen.getByTestId("settings-save"));
  await waitFor(() =>
    expect(server.config).toMatchObject({
      agent: { prompt_suggestions: true },
    }),
  );
});

test("prompt suggestions use the Russian label and preserve explicit false", async () => {
  mountSettings({
    section: "agent",
    lang: "ru",
    config: { agent: { prompt_suggestions: false } },
  });
  const toggle = await screen.findByRole("switch", {
    name: "Подсказки следующего промпта",
  });
  expect(toggle.getAttribute("aria-checked")).toBe("false");
});
