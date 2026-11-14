import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import StatisticsView from "./StatisticsView";
import StatisticsSettings from "./StatisticsSettings";
import {
  flushStatisticsSettings,
  statisticsSettingsDirty,
} from "./statisticsSettingsStore";
import { initLocale } from "../i18n/i18n";
import type { StatisticsData } from "./api";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  initLocale("en");
});

test("catalog pricing needs no manual form; a custom tariff is saved with the tab", async () => {
  initLocale("en");
  const data = fixture();
  data.settings!.import.state = "complete";
  const fetch = vi.fn(
    async (_: RequestInfo | URL, init?: RequestInit) =>
      new Response(
        JSON.stringify(init?.method === "PUT" ? data.settings : data),
      ),
  );
  vi.stubGlobal("fetch", fetch);
  render(<StatisticsSettings />);
  // The catalog prices on its own, and the why of it sits behind the (i)
  // beside the legend, as every other tab writes a description.
  expect(
    await screen.findByText("Model prices · USD per million tokens"),
  ).toBeInTheDocument();
  expect(statisticsSettingsDirty()).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Add" }));
  fireEvent.change(screen.getByRole("combobox", { name: "Model" }), {
    target: { value: "account/model" },
  });
  for (const name of ["Input", "Output", "Cached"])
    fireEvent.change(screen.getByRole("spinbutton", { name }), {
      target: { value: "0" },
    });
  // The panel has no Save of its own: the drawer's one writes it.
  expect(screen.queryByRole("button", { name: /Save statistics/ })).toBeNull();
  await waitFor(() => expect(statisticsSettingsDirty()).toBe(true));
  await flushStatisticsSettings();
  expect(fetch).toHaveBeenCalledWith(
    "/coddy/statistics/settings",
    expect.objectContaining({
      method: "PUT",
      body: JSON.stringify({
        enabled: true,
        timezone: "",
        prices: { "account/model": { input: 0, output: 0, cached: 0 } },
      }),
    }),
  );
});

test("a reset warns that every counter goes and clears the whole history", async () => {
  initLocale("en");
  const data = fixture();
  data.settings!.import.state = "complete";
  const fetch = vi.fn(
    async (_: RequestInfo | URL, init?: RequestInit) =>
      new Response(
        JSON.stringify(init?.method === "DELETE" ? { ok: true } : data),
      ),
  );
  vi.stubGlobal("fetch", fetch);
  render(<StatisticsSettings />);
  // Nothing to choose: a reset is everything, which is what the warning says.
  expect(screen.queryByLabelText("Keep")).toBeNull();
  fireEvent.click(await screen.findByRole("button", { name: "Clear" }));
  const dialog = await screen.findByRole("dialog", {
    name: "Reset all statistics?",
  });
  expect(within(dialog).getByText(/cannot be undone/)).toBeInTheDocument();
  fireEvent.click(within(dialog).getByRole("button", { name: "Clear" }));
  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith(
      "/coddy/statistics?keep=none",
      expect.objectContaining({ method: "DELETE" }),
    ),
  );
});

// The filters live at the right edge of the tab strip: an icon with the
// number of them set. Taking them off is the sheet's own button.
test("the filters open from the tab strip, count themselves and clear", async () => {
  initLocale("en");
  const data = fixture();
  data.settings!.import.state = "complete";
  const fetch = vi.fn(async () => new Response(JSON.stringify(data)));
  vi.stubGlobal("fetch", fetch);
  const onClose = vi.fn();
  render(
    <StatisticsView
      onClose={onClose}
      onSettings={vi.fn()}
      swarm={false}
      relayHome={false}
      cwd="/project"
    />,
  );
  await screen.findByText("Token activity");
  expect(screen.queryByLabelText("Model")).toBeNull();
  // Nothing is filtered yet, so there is no count and nothing to clear.
  expect(screen.queryByTestId("statistics-filter-count")).toBeNull();
  const trigger = screen.getByTestId("statistics-filter-trigger");
  expect(trigger).toHaveAccessibleName("Filters");
  trigger.focus();
  fireEvent.click(trigger);
  const dialog = screen.getByRole("dialog", { name: "Filters" });
  fireEvent.change(within(dialog).getByLabelText("Model"), {
    target: { value: "model" },
  });
  await waitFor(() =>
    expect(
      fetch.mock.calls.some((args: unknown[]) =>
        String(args[0]).includes("model=model"),
      ),
    ).toBe(true),
  );
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(onClose).not.toHaveBeenCalled();
  expect(trigger).toHaveFocus();
  expect(screen.getByTestId("statistics-filter-count")).toHaveTextContent("1");
  fireEvent.click(trigger);
  expect(screen.getByLabelText("Model")).toHaveValue("model");
  // The sheet's own button is what takes every filter off.
  fireEvent.click(screen.getByTestId("statistics-filter-clear"));
  await waitFor(() =>
    expect(screen.queryByTestId("statistics-filter-count")).toBeNull(),
  );
});

test("export and settings live behind the actions button", async () => {
  initLocale("en");
  const data = fixture();
  data.settings!.import.state = "complete";
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(data))),
  );
  const onSettings = vi.fn();
  render(
    <StatisticsView
      onClose={vi.fn()}
      onSettings={onSettings}
      swarm={false}
      relayHome={false}
      cwd="/project"
    />,
  );
  await screen.findByText("Token activity");
  expect(screen.queryByRole("button", { name: "Export CSV" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Statistics actions" }));
  expect(
    screen.getByRole("button", { name: "Export CSV" }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Statistics settings" }));
  expect(onSettings).toHaveBeenCalledOnce();
});

test("the settings of the statistics are a tab of the drawer on a phone too", async () => {
  initLocale("en");
  const data = fixture();
  data.settings!.import.state = "complete";
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({ matches: true })),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(data))),
  );
  const onSettings = vi.fn();
  render(
    <StatisticsView
      onClose={vi.fn()}
      onSettings={onSettings}
      swarm={false}
      relayHome={false}
      cwd="/project"
    />,
  );
  await screen.findByText("Token activity");
  fireEvent.click(screen.getByRole("button", { name: "Statistics actions" }));
  fireEvent.click(screen.getByRole("button", { name: "Statistics settings" }));
  // One surface for the settings, with the drawer's own Save under them.
  expect(onSettings).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("dialog")).toBeNull();
});

function fixture(): StatisticsData {
  const report = {
    dataset: "one",
    timezone: "UTC",
    totals: {
      inputTokens: 100,
      outputTokens: 20,
      cachedTokens: 50,
      totalTokens: 120,
      costUsd: null,
      activeMs: null,
      longestMs: null,
      maxDailyTokens: 120,
      requests: 1,
      calls: 1,
      chats: 1,
      skillCalls: 0,
      uniqueSkills: 0,
      currentStreak: 1,
      longestStreak: 1,
      unpricedCalls: 1,
      unknownUsage: 0,
      importedTurns: 0,
    },
    days: [],
    models: [{ name: "model", count: 1, tokens: 120 }],
    providers: [],
    projects: [],
    sources: [],
    reasoning: [],
    skills: [],
  };
  return {
    report,
    allTime: report,
    settings: {
      timezone: "",
      prices: {},
      import: { state: "pending", done: 0, total: 0, failed: 0 },
    },
    installedSkills: 3,
  };
}
