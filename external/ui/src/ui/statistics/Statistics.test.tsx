import {
  act,
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
    await screen.findByText("Model prices per million tokens"),
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
        usdToRub: 90,
        prices: {
          "account/model": {
            input: 0,
            output: 0,
            cached: 0,
            currency: "USD",
          },
        },
      }),
    }),
  );
});

test("each custom model tariff defaults to USD and can be saved in RUB", async () => {
  initLocale("en");
  const data = fixture();
  Object.assign(data.settings!, { currency: "USD", usdToRub: 90 });
  data.settings!.prices["neuraldeep/kimi-k2.6"] = {
    input: 1,
    output: 4,
    cached: 0.5,
  };
  data.settings!.import.state = "complete";
  const fetch = vi.fn(
    async (_: RequestInfo | URL, init?: RequestInit) =>
      new Response(
        JSON.stringify(init?.method === "PUT" ? data.settings : data),
      ),
  );
  vi.stubGlobal("fetch", fetch);
  render(<StatisticsSettings />);

  const currency = await screen.findByTestId("statistics-price-currency-0");
  const rate = screen.getByRole("spinbutton", { name: "RUB per USD" });
  expect(currency).toHaveValue("USD");
  expect(rate).toHaveValue(90);
  fireEvent.change(currency, { target: { value: "RUB" } });
  await waitFor(() => expect(statisticsSettingsDirty()).toBe(true));
  await flushStatisticsSettings();

  expect(fetch).toHaveBeenCalledWith(
    "/coddy/statistics/settings",
    expect.objectContaining({
      method: "PUT",
      body: JSON.stringify({
        enabled: true,
        timezone: "",
        usdToRub: 90,
        prices: {
          "neuraldeep/kimi-k2.6": {
            input: 1,
            output: 4,
            cached: 0.5,
            currency: "RUB",
          },
        },
      }),
    }),
  );
});

test("the Filters currency select converts the estimated cost and shows the rate", async () => {
  initLocale("en");
  const data = fixture();
  data.report.totals.costUsd = 1.25;
  Object.assign(data.settings!, { currency: "USD", usdToRub: 90 });
  data.settings!.import.state = "complete";
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    if (
      String(url).startsWith("/coddy/statistics/settings") &&
      init?.method === "PUT"
    ) {
      Object.assign(data.settings!, JSON.parse(String(init.body)));
      return new Response(JSON.stringify(data.settings));
    }
    return new Response(JSON.stringify(data));
  });
  vi.stubGlobal("fetch", fetch);
  render(
    <StatisticsView
      onClose={vi.fn()}
      onSettings={vi.fn()}
      swarm={false}
      relayHome={false}
      cwd="/project"
    />,
  );

  await screen.findByText("Token activity");
  expect(
    screen
      .getByTestId("statistics-cost-value")
      .textContent?.replace(/\s+/g, " "),
  ).toContain(
    new Intl.NumberFormat("en", {
      style: "currency",
      currency: "USD",
      maximumFractionDigits: 2,
    })
      .format(1.25)
      .replace(/\s+/g, " "),
  );
  expect(screen.getByTestId("statistics-cost-rate")).toHaveTextContent(
    "1 USD = 90 RUB",
  );
  fireEvent.click(screen.getByRole("button", { name: "Filters" }));
  const currency = await screen.findByRole("combobox", { name: "Currency" });
  expect(currency.tagName).toBe("SELECT");
  expect(currency).toHaveValue("USD");
  fireEvent.change(currency, { target: { value: "RUB" } });
  await waitFor(() =>
    expect(
      screen
        .getByTestId("statistics-cost-value")
        .textContent?.replace(/\s+/g, " "),
    ).toContain(
      new Intl.NumberFormat("en", {
        style: "currency",
        currency: "RUB",
        maximumFractionDigits: 2,
      })
        .format(112.5)
        .replace(/\s+/g, " "),
    ),
  );
  expect(fetch).toHaveBeenCalledWith(
    "/coddy/statistics/settings",
    expect.objectContaining({
      method: "PUT",
      body: JSON.stringify({ currency: "RUB" }),
    }),
  );
});

test("the Filters currency selector is hidden for swarm aggregates", async () => {
  initLocale("en");
  const data = fixture();
  delete data.settings;
  const fetch = vi.fn(async () => new Response(JSON.stringify(data)));
  vi.stubGlobal("fetch", fetch);
  render(
    <StatisticsView
      onClose={vi.fn()}
      onSettings={vi.fn()}
      swarm
      relayHome
      cwd="/project"
    />,
  );

  await screen.findByText("Token activity");
  fireEvent.click(screen.getByRole("button", { name: "Filters" }));
  expect(await screen.findByRole("combobox", { name: "Scope" })).toHaveValue(
    "swarm",
  );
  expect(screen.queryByTestId("statistics-display-currency")).toBeNull();
});

test("a stale report cannot undo the saved Filters currency", async () => {
  initLocale("en");
  const data = fixture();
  data.report.totals.costUsd = 1.25;
  Object.assign(data.settings!, { currency: "USD", usdToRub: 90 });
  data.settings!.import.state = "complete";
  const staleData = JSON.parse(JSON.stringify(data)) as StatisticsData;
  let reportReads = 0;
  let releaseStale: (() => void) | undefined;
  const fetch = vi.fn(
    (url: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const path = String(url);
      if (
        path.startsWith("/coddy/statistics/settings") &&
        init?.method === "PUT"
      ) {
        Object.assign(data.settings!, JSON.parse(String(init.body)));
        return Promise.resolve(new Response(JSON.stringify(data.settings)));
      }
      if (path.startsWith("/coddy/statistics?")) {
        reportReads++;
        if (reportReads === 2) {
          return new Promise<Response>((resolve) => {
            releaseStale = () =>
              resolve(new Response(JSON.stringify(staleData)));
          });
        }
      }
      return Promise.resolve(new Response(JSON.stringify(data)));
    },
  );
  vi.stubGlobal("fetch", fetch);
  render(
    <StatisticsView
      onClose={vi.fn()}
      onSettings={vi.fn()}
      swarm={false}
      relayHome={false}
      cwd="/project"
    />,
  );

  await screen.findByText("Token activity");
  fireEvent.change(screen.getByRole("combobox", { name: "Period" }), {
    target: { value: "30" },
  });
  await waitFor(() => expect(reportReads).toBe(2));
  fireEvent.click(screen.getByRole("button", { name: "Filters" }));
  const currency = await screen.findByRole("combobox", { name: "Currency" });
  fireEvent.change(currency, { target: { value: "RUB" } });
  await waitFor(() => expect(currency).toHaveValue("RUB"));
  await waitFor(() => expect(reportReads).toBe(3));

  await act(async () => {
    releaseStale?.();
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  expect(screen.getByTestId("statistics-display-currency")).toHaveValue("RUB");
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
