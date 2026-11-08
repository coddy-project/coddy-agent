import { afterEach, describe, expect, test, vi } from "vitest";
import { activate } from "../../../../../examples/extensions/neuraldeep-stability/main.js";

const payload = {
  page: { updated_at: "2026-11-08T12:00:00Z" },
  components: [
    {
      id: "m14",
      name: "API",
      status: "operational",
      uptime_pct: 99.78,
    },
  ],
  history: {
    m14: [
      { date: "2026-11-07", status: 1, uptime_pct: 100, down_checks: 0 },
      {
        date: "2026-11-08",
        status: 2,
        uptime_pct: 99.5,
        down_checks: 1,
        windows: ["15:17–15:42 МСК (~25 мин)"],
      },
    ],
  },
};

function setup(answer = payload) {
  let registration;
  const remove = vi.fn();
  const coddy = {
    settings: {
      registerBlock: vi.fn((block) => {
        registration = block;
        return { remove };
      }),
    },
  };
  const lifetime = new AbortController();
  const fetcher = vi.fn(async () => Response.json(answer));
  const api = { signal: lifetime.signal, net: { fetch: fetcher } };
  activate(coddy);
  return { registration, remove, lifetime, fetcher, api };
}

afterEach(() => {
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

describe("the NeuralDeep stability example", () => {
  test("matches only neuraldeep and renders uptime through the server proxy", async () => {
    const { registration, fetcher, api } = setup();
    expect(registration.match({ row: { type: "neuraldeep" } })).toBe(true);
    expect(registration.match({ row: { type: "openai" } })).toBe(false);
    const element = document.createElement("div");
    document.body.append(element);
    const cleanup = registration.mount(element, api, {
      section: "providers",
      rowName: "neuraldeep",
      row: { type: "neuraldeep" },
    });
    await vi.waitFor(() =>
      expect(element.textContent).toContain("All systems operational"),
    );
    expect(fetcher).toHaveBeenCalledWith(
      "https://status.neuraldeep.ru/api/v2/uptime.json",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(element.textContent).toContain("API");
    expect(element.textContent).toContain("99.78%");
    expect(element.textContent).toContain("Open status page");
    expect(element.querySelectorAll(".nd-stability-day")).toHaveLength(2);
    expect(
      element.querySelector('.nd-stability-day[data-status="2"]').title,
    ).toContain("15:17–15:42 МСК");
    cleanup();
    expect(element.querySelector(".nd-stability")).toBeNull();
  });

  test("cleanup aborts the proxy request", () => {
    const { registration } = setup();
    const controller = new AbortController();
    const pending = new Promise(() => {});
    const api = {
      signal: controller.signal,
      net: {
        fetch: vi.fn((_url, options) => {
          options.signal.addEventListener("abort", () => {
            controller.abort();
          });
          return pending;
        }),
      },
    };
    const element = document.createElement("div");
    const cleanup = registration.mount(element, api, {
      section: "providers",
      rowName: "neuraldeep",
    });
    const requestSignal = api.net.fetch.mock.calls[0][1].signal;
    expect(requestSignal.aborted).toBe(false);
    cleanup();
    expect(requestSignal.aborted).toBe(true);
  });
});
