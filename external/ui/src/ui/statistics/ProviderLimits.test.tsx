import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { initLocale } from "../i18n/i18n";
import { ProviderLimits } from "./ProviderLimits";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  initLocale("en");
});

test("all windows, unused providers, unavailable and disabled rows stay visible", async () => {
  initLocale("en");
  const fetch = vi.fn(
    async () =>
      new Response(
        JSON.stringify({
          providers: [
            {
              provider: "subscription",
              providerType: "neuraldeep",
              windows: [
                {
                  id: "day",
                  label: "Day",
                  usedPercent: 0,
                  used: 0,
                  limit: 100,
                  remaining: 100,
                },
                {
                  id: "week",
                  label: "Week",
                  usedPercent: 75,
                  used: 75,
                  limit: 100,
                  remaining: 25,
                  resetsAt: "2026-11-05T12:00:00Z",
                },
              ],
              rate: { used: 2, limit: 60, remaining: 58, resetInSec: 10 },
              blockedModels: [
                { model: "limited-model", retryAt: "2026-11-05T12:00:00Z" },
              ],
            },
            { provider: "unused", providerType: "openai", unsupported: true },
            {
              provider: "disabled",
              providerType: "codex",
              unsupported: true,
              disabled: true,
            },
            {
              provider: "offline",
              providerType: "devin",
              error: "unavailable",
            },
          ],
        }),
      ),
  );
  vi.stubGlobal("fetch", fetch);
  render(
    <ProviderLimits
      aggregate={false}
      nodes={undefined}
      node=""
      timezone="UTC"
    />,
  );
  await screen.findByRole("heading", { name: "subscription" });
  expect(screen.getAllByRole("progressbar")).toHaveLength(2);
  expect(screen.getByText("0% used")).toBeInTheDocument();
  expect(screen.getByText("limited-model")).toBeInTheDocument();
  for (const name of ["unused", "disabled", "offline"])
    expect(screen.getByRole("heading", { name })).toBeInTheDocument();
  expect(
    screen.getByText("This provider does not expose account limits to Coddy."),
  ).toBeInTheDocument();
  expect(
    screen.getByText("Limit reads are disabled in this provider’s settings."),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith(
      "/coddy/providers/usage?refresh=1",
      expect.anything(),
    ),
  );
});

test("swarm keeps equal provider names on separate nodes and reports failed nodes", async () => {
  initLocale("en");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes("offline")
        ? new Response("unavailable", { status: 503 })
        : new Response(
            JSON.stringify({
              providers: [{ provider: "same-name", unsupported: true }],
            }),
          ),
    ),
  );
  render(
    <ProviderLimits
      aggregate
      nodes={["first", "second", "offline"].map((name) => ({
        name,
        dataset: name,
        path: [name],
        import: { state: "complete", done: 0, total: 0, failed: 0 },
      }))}
      node=""
      timezone="UTC"
    />,
  );
  await waitFor(() =>
    expect(screen.getAllByRole("heading", { name: "same-name" })).toHaveLength(
      2,
    ),
  );
  expect(screen.getByRole("heading", { name: "offline" })).toBeInTheDocument();
  expect(screen.getByRole("alert")).toHaveTextContent("Could not read limits.");
});
