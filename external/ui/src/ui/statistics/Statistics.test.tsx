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
import { initLocale } from "../i18n/i18n";
import type { StatisticsData } from "./api";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  initLocale("en");
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

test("the first visit offers background import, and a later visit keeps its progress", async () => {
  initLocale("en");
  const data = fixture();
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input) === "/coddy/statistics/import") {
      expect(JSON.parse(String(init?.body))).toEqual({ action: "start" });
      data.settings!.import = {
        state: "running",
        done: 7,
        total: 40,
        failed: 0,
      };
      return new Response(JSON.stringify(data.settings!.import));
    }
    return new Response(JSON.stringify(data));
  });
  vi.stubGlobal("fetch", fetch);
  const props = {
    onClose: vi.fn(),
    onSettings: vi.fn(),
    swarm: false,
    relayHome: false,
    cwd: "/project",
  };
  const view = render(<StatisticsView {...props} />);
  const dialog = await screen.findByRole("dialog", {
    name: "Import history from old chats?",
  });
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Import history" }),
  );
  await screen.findByText("Chats processed: 7 / 40");
  expect(screen.queryByRole("dialog")).toBeNull();
  view.unmount();
  render(<StatisticsView {...props} />);
  await screen.findByText("Chats processed: 7 / 40");
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("cleanup confirms the selected period and sends it to the server", async () => {
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
  const keep = await screen.findByLabelText("Keep");
  fireEvent.change(keep, { target: { value: "week" } });
  fireEvent.click(screen.getByRole("button", { name: "Clear statistics" }));
  const dialog = await screen.findByRole("dialog", {
    name: "Clear recorded statistics?",
  });
  expect(within(dialog).getByText(/Last week/)).toBeInTheDocument();
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Clear statistics" }),
  );
  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith(
      "/coddy/statistics?keep=week",
      expect.objectContaining({ method: "DELETE" }),
    ),
  );
});
