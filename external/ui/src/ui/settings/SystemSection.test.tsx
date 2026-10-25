import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  onTestFinished,
  test,
  vi,
} from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { pageWithoutClipboardApi, selectedOnce } from "../clipboardTestKit";
import SystemSection from "./SystemSection";
import { Sparkline } from "./Sparkline";
import { fakeStatus } from "./systemStatus.fakes";
import { compact, seconds, tasksText } from "./systemStatus";
import { resetSystemStatusForTests } from "./systemStatusApi";
import { landmarkAnchor } from "./settingsSections";
import { PaneHidden } from "../components/PanePortal";
import { resetServerVersionForTests } from "../chat/HeroFooter";
import { installRemoteFetchShim, setEnv } from "../env/remoteEnv";
import { initLocale } from "../i18n/i18n";

type Reply = { status?: number; body?: unknown } | Error;

/**
 * A coddy serve behind fetch that answers the status route from a list (the
 * last answer repeats) and /coddy/info with a version.
 */
function serve(
  replies: Reply[],
  info: { version?: string } = { version: "1.2.35" },
) {
  const reads: RequestInit[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path.endsWith("/coddy/info")) {
      return new Response(JSON.stringify(info), { status: 200 });
    }
    if (path.endsWith("/coddy/status")) {
      reads.push(init ?? {});
      const reply = replies[Math.min(reads.length, replies.length) - 1]!;
      if (reply instanceof Error) {
        throw reply;
      }
      return new Response(JSON.stringify(reply.body ?? {}), {
        status: reply.status ?? 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    return new Response("", { status: 404 });
  });
  vi.stubGlobal("fetch", fetch);
  return Object.assign(fetch, { reads });
}

const doc = (over: Parameters<typeof fakeStatus>[0] = {}) => ({
  body: fakeStatus(over),
});

/** Lets the read the tab starts on mount answer. */
const settled = () =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });

async function open(
  replies: Reply[],
  info?: { version?: string },
): Promise<ReturnType<typeof serve>> {
  const fetch = serve(replies, info);
  render(<SystemSection />);
  await settled();
  return fetch;
}

const summary = () => screen.getByTestId("system-summary");
const block = (id: string) => screen.getByTestId(`system-${id}`);
const heading = (name: string) => screen.getByRole("heading", { name });

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-10-15T10:00:30Z"));
  initLocale("en");
  resetSystemStatusForTests();
  resetServerVersionForTests();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  setEnv({ mode: "local" });
  localStorage.clear();
  delete (window as Window & { __coddyFetchShimmed?: boolean })
    .__coddyFetchShimmed;
  delete (document as { visibilityState?: unknown }).visibilityState;
});

const BLOCKS = [
  ["installation", "This installation"],
  ["subsystems", "Subsystems"],
  ["disk", "Disk"],
  ["activity", "Activity"],
] as const;

describe("the four blocks that are always drawn", () => {
  test("stand on the first frame, before the first read returns, as focusable headings", () => {
    vi.stubGlobal("fetch", () => new Promise(() => {}));
    render(<SystemSection />);
    for (const [id, title] of BLOCKS) {
      const h = heading(title);
      expect(h.parentElement).toBe(block(id));
      expect(h.getAttribute("tabindex")).toBe("-1");
    }
    expect(screen.queryByText("Last hour")).toBeNull();
    expect(screen.queryByRole("img")).toBeNull();
  });

  // The settings search finds a block by its heading and takes the operator to
  // the box that carries the anchor (settingsReveal.ts).
  test("carry their landmark anchors on the box, which is named by the heading", async () => {
    vi.stubGlobal("fetch", () => new Promise(() => {}));
    render(<SystemSection />);
    for (const [id, title] of BLOCKS) {
      const box = block(id);
      expect(box.getAttribute("data-settings-anchor")).toBe(
        landmarkAnchor("status", id),
      );
      const h = heading(title);
      expect(box.getAttribute("aria-labelledby")).toBe(h.id);
      expect(h.classList.contains("appearance-section-label")).toBe(true);
    }
  });

  test("a heading that is no landmark carries neither an anchor nor the landmark class", async () => {
    await open([doc()]);
    for (const title of ["Last hour", "Model providers since start"]) {
      const h = heading(title);
      expect(h.closest("[data-settings-anchor]")).toBeNull();
      expect(h.classList.contains("appearance-section-label")).toBe(false);
    }
    expect(
      [...document.querySelectorAll("[data-settings-anchor]")].map((el) =>
        el.getAttribute("data-settings-anchor"),
      ),
    ).toEqual(BLOCKS.map(([id]) => landmarkAnchor("status", id)));
  });

  test("keep standing when the server does not report its status", async () => {
    await open([{ status: 404 }]);
    for (const title of [
      "This installation",
      "Subsystems",
      "Disk",
      "Activity",
    ]) {
      expect(heading(title)).toBeTruthy();
    }
  });
});

describe("the summary", () => {
  test("is the only live region, and polite", async () => {
    await open([doc()]);
    const regions = document.querySelectorAll("[aria-live], [role='status']");
    expect([...regions]).toEqual([summary()]);
    expect(summary().getAttribute("aria-live")).toBe("polite");
    expect(summary().textContent).not.toContain("Updated");
  });

  test("names what needs attention, each item a button that takes the focus to its block", async () => {
    await open([doc()]);
    expect(summary().textContent).toContain("2 need attention");
    const disk = within(summary()).getByRole("button", { name: "Disk low" });
    const restart = within(summary()).getByRole("button", {
      name: "Restart pending",
    });
    fireEvent.click(disk);
    expect(document.activeElement).toBe(heading("Disk"));
    fireEvent.click(restart);
    expect(document.activeElement).toBe(heading("Subsystems"));
    expect(summary().getAttribute("data-state")).toBe("problems");
  });

  test("words a full disk as full, a stopped subsystem and a failing provider by name", async () => {
    await open([
      doc({
        disk: { availableBytes: 0, sizeBytes: 1_000_000_000 },
        attention: [
          { code: "disk_low" },
          { code: "subsystem_down", subsystem: "metrics" },
          { code: "provider_failing", provider: "local-mlx" },
        ],
      }),
    ]);
    expect(summary().textContent).toContain("3 need attention");
    within(summary()).getByRole("button", { name: "Disk full" });
    within(summary()).getByRole("button", { name: "Metrics exporter stopped" });
    const provider = within(summary()).getByRole("button", {
      name: "local-mlx failing",
    });
    fireEvent.click(provider);
    expect(document.activeElement).toBe(heading("Model providers since start"));
  });

  test("says one thing needs attention in the singular", async () => {
    await open([doc({ attention: [{ code: "restart_required" }] })]);
    expect(summary().textContent).toContain("1 needs attention");
  });

  // A page with nothing to report says nothing: "Everything OK" told the
  // reader what the blocks below already show.
  test("with nothing to report draws no summary, and the live region stays for what comes next", async () => {
    await open([doc({ attention: undefined, restartRequired: undefined })]);
    expect(summary().getAttribute("data-state")).toBe("clear");
    expect(summary().textContent).toBe("");
    expect(summary().getAttribute("aria-live")).toBe("polite");
    expect(within(summary()).queryAllByRole("button")).toEqual([]);
  });

  test("with a block missing says what is missing and never everything is OK", async () => {
    await open([
      doc({
        attention: undefined,
        restartRequired: undefined,
        disk: undefined,
        unavailable: ["disk"],
      }),
    ]);
    expect(summary().getAttribute("data-state")).toBe("partial");
    expect(summary().textContent).not.toContain("Everything OK");
    expect(summary().textContent).toContain("not available");
    fireEvent.click(within(summary()).getByRole("button", { name: "Disk" }));
    expect(document.activeElement).toBe(heading("Disk"));
  });

  test("a partial reading next to problems names both", async () => {
    await open([
      doc({
        disk: undefined,
        unavailable: ["disk"],
        attention: [{ code: "restart_required" }],
      }),
    ]);
    expect(summary().getAttribute("data-state")).toBe("problems");
    expect(summary().textContent).toContain("1 needs attention");
    expect(summary().textContent).toContain("not available");
    expect(summary().textContent).not.toContain("Everything OK");
  });

  test("after a refresh that failed is stale: the last values stay, greyed, and nothing is OK", async () => {
    const fetch = await open([
      doc({ attention: undefined, restartRequired: undefined }),
      new Error("down"),
    ]);
    expect(summary().textContent).toBe("");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(fetch.reads).toHaveLength(2);
    expect(summary().getAttribute("data-state")).toBe("stale");
    expect(summary().textContent).not.toContain("Everything OK");
    expect(summary().textContent).toContain("Can't refresh");
    // The live region names no age: it would go stale itself, and the footer
    // already says when the values were read.
    expect(summary().textContent).not.toMatch(/ago/);
    // The values of the last read are still there, marked as old.
    expect(block("installation").textContent).toContain("1.3.12");
    expect(screen.getByTestId("system-section").className).toContain(
      "is-stale",
    );
  });
});

describe("the restart notice", () => {
  const notice = () => screen.queryByTestId("system-restart");

  test("names the saved address when a listener moved, and points to the page that tells how", async () => {
    await open([doc()]);
    expect(notice()!.textContent).toContain(
      "Saved address 0.0.0.0:12345 applies after Coddy restarts.",
    );
    const link = within(notice()!).getByRole("link", {
      name: "How to restart",
    });
    expect(link.getAttribute("href")).toBe(
      "#/docs/operate/serve#applying-a-saved-address",
    );
    expect(document.body.textContent).not.toContain("coddy serve restart");
  });

  test("says a saved change when no listener moved", async () => {
    await open([
      doc({
        subsystems: [
          { kind: "httpserver", state: "running", address: "127.0.0.1:12345" },
        ],
      }),
    ]);
    expect(notice()!.textContent).toContain(
      "A saved change applies after Coddy restarts.",
    );
    expect(notice()!.textContent).not.toContain("Saved address");
    within(notice()!).getByRole("link", { name: "How to restart" });
    expect(document.body.textContent).not.toContain("coddy serve restart");
  });

  test("names every address that moved", async () => {
    await open([
      doc({
        subsystems: [
          {
            kind: "httpserver",
            state: "running",
            address: "127.0.0.1:12345",
            pendingAddress: "0.0.0.0:12345",
          },
          {
            kind: "metrics",
            state: "running",
            address: "127.0.0.1:12347",
            pendingAddress: "0.0.0.0:12347",
          },
        ],
      }),
    ]);
    expect(notice()!.textContent).toContain(
      "Saved address 0.0.0.0:12345, 0.0.0.0:12347 applies after Coddy restarts.",
    );
  });

  test("in Russian, with and without an address", async () => {
    initLocale("ru");
    await open([doc()]);
    expect(notice()!.textContent).toContain(
      "Сохранённый адрес 0.0.0.0:12345 применится после перезапуска Coddy.",
    );
    cleanup();
    resetSystemStatusForTests();
    await open([
      doc({
        subsystems: [
          { kind: "httpserver", state: "running", address: "127.0.0.1:12345" },
        ],
      }),
    ]);
    expect(notice()!.textContent).toContain(
      "Сохранённое изменение применится после перезапуска Coddy.",
    );
  });

  test("says what refused the saved settings, with the server's own words", async () => {
    await open([
      doc({
        restartRefused: "listen tcp 0.0.0.0:12345: address already in use",
      }),
    ]);
    expect(notice()!.textContent).toContain(
      "The saved settings can't start: listen tcp 0.0.0.0:12345: address already in use",
    );
    expect(document.body.textContent).not.toContain("coddy serve restart");
  });

  test("a refusal wears the error tone and a pending restart does not", async () => {
    await open([doc()]);
    expect(notice()!.dataset["tone"]).toBeUndefined();
    cleanup();
    resetSystemStatusForTests();
    await open([doc({ restartRefused: "nothing to start" })]);
    expect(notice()!.dataset["tone"]).toBe("error");
  });

  test("is not drawn when nothing waits for a restart", async () => {
    await open([doc({ restartRequired: undefined, attention: undefined })]);
    expect(notice()).toBeNull();
  });
});

describe("this installation", () => {
  test("names the build, the platform, how long it runs, who started it and where its config is", async () => {
    await open([doc()]);
    const rows = Object.fromEntries(
      [...block("installation").querySelectorAll("dt")].map((dt) => [
        dt.textContent,
        dt.nextElementSibling!.textContent,
      ]),
    );
    expect(rows["Version"]).toBe("1.3.12 (050fbbbf)");
    expect(rows["Built with"]).toBe("cli · gateway · http · metrics · ui");
    expect(rows["Platform"]).toBe("linux/arm64 · go1.26.9");
    // generatedAt - startedAt: the server's clock on both ends
    expect(rows["Running for"]).toBe("1h");
    expect(rows["Started by"]).toBe("systemd service");
    expect(rows["Config file"]).toContain("~/.coddy/config.yaml");
    // a release keeps its notes one click away
    expect(
      within(block("installation"))
        .getByRole("link", { name: "1.3.12" })
        .getAttribute("href"),
    ).toBe("https://github.com/coddy-project/coddy-agent/releases/tag/1.3.12");
  });

  test("leaves out what the server does not say and keeps a build without tags", async () => {
    await open([
      doc({
        build: {
          version: "1.3.12-dirty",
          goVersion: "go1.26.9",
          os: "linux",
          arch: "arm64",
        },
        process: { startedAt: "2026-10-15T09:59:30Z", startedBy: "foreground" },
      }),
    ]);
    const labels = [...block("installation").querySelectorAll("dt")].map(
      (dt) => dt.textContent,
    );
    expect(labels).not.toContain("Built with");
    expect(labels).not.toContain("Config file");
    expect(block("installation").textContent).toContain("1.3.12-dirty");
    expect(within(block("installation")).queryByRole("link")).toBeNull();
    expect(block("installation").textContent).toContain("foreground");
  });

  test("copies the config file's path with a button", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    await open([doc()]);
    const copy = within(block("installation")).getByRole("button", {
      name: /copy/i,
    });
    expect(copy.className).toContain("system-copy");
    await act(async () => {
      fireEvent.click(copy);
    });
    expect(writeText).toHaveBeenCalledWith("~/.coddy/config.yaml");
  });

  // A page served over plain HTTP from another machine is no secure context:
  // it has no navigator.clipboard, and the button must not say Copied for a
  // path that never reached the clipboard.
  test("without a clipboard API it copies through the page and says Copied only when that worked", async () => {
    const page = pageWithoutClipboardApi(false);
    await open([doc()]);
    const copy = within(block("installation")).getByRole("button", {
      name: /copy/i,
    });
    await act(async () => {
      fireEvent.click(copy);
    });
    expect(page.copies).toEqual(selectedOnce("~/.coddy/config.yaml"));
    expect(copy.textContent).toBe("Copy");
    page.answer(true);
    await act(async () => {
      fireEvent.click(copy);
    });
    expect(copy.textContent).toBe("Copied");
  });

  test("a clipboard that refuses leaves the button saying Copy when the page cannot copy either", async () => {
    const writeText = vi.fn(async () => {
      throw new Error("denied");
    });
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    Object.defineProperty(document, "execCommand", {
      value: () => false,
      configurable: true,
    });
    onTestFinished(() => {
      delete (document as { execCommand?: unknown }).execCommand;
    });
    await open([doc()]);
    const copy = within(block("installation")).getByRole("button", {
      name: /copy/i,
    });
    await act(async () => {
      fireEvent.click(copy);
    });
    expect(writeText).toHaveBeenCalled();
    expect(copy.textContent).toBe("Copy");
  });

  test("the version is in monospace, like the addresses and the config path", async () => {
    await open([doc()]);
    const dt = [...block("installation").querySelectorAll("dt")].find(
      (d) => d.textContent === "Version",
    )!;
    const code = dt.nextElementSibling!.querySelector(".system-code");
    expect(code?.textContent).toBe("1.3.12");
  });
});

describe("subsystems", () => {
  test("names each one with its state and address, and a listener that will move with its new address", async () => {
    await open([doc()]);
    const box = block("subsystems");
    const row = (name: string) => {
      const dt = within(box).getByText(name, { selector: "dt" });
      return dt.nextElementSibling!.textContent!;
    };
    expect(row("HTTP server")).toContain("running");
    expect(row("HTTP server")).toContain("127.0.0.1:12345");
    expect(row("HTTP server")).toContain("0.0.0.0:12345 after restart");
    expect(row("Metrics exporter")).toContain("127.0.0.1:12347");
  });

  test("says whether a scrape needs a token and never shows one", async () => {
    await open([
      doc({
        subsystems: [
          {
            kind: "metrics",
            state: "running",
            address: "127.0.0.1:12347",
            credential: "token",
          },
          { kind: "gateway", state: "starting" },
        ],
      }),
    ]);
    const box = block("subsystems");
    expect(box.textContent).toContain("/metrics");
    expect(box.textContent).toContain("token set");
    expect(
      within(box).getByText("Telegram bot", { selector: "dt" }),
    ).toBeTruthy();
    expect(box.textContent).toContain("starting");
    cleanup();
    resetSystemStatusForTests();
    await open([
      doc({
        subsystems: [
          {
            kind: "metrics",
            state: "running",
            address: "127.0.0.1:12347",
            credential: "none",
          },
        ],
      }),
    ]);
    expect(block("subsystems").textContent).toContain("no token");
  });

  test("an unknown kind is named as the server names it", async () => {
    await open([
      doc({ subsystems: [{ kind: "future-thing", state: "stopped" }] }),
    ]);
    expect(block("subsystems").textContent).toContain("future-thing");
    expect(block("subsystems").textContent).toContain("stopped");
  });
});

describe("disk", () => {
  test("is one meter for the sessions disk, named, with its values and what is free of what", async () => {
    await open([doc()]);
    const meters = screen.getAllByRole("meter");
    expect(meters).toHaveLength(1);
    const meter = meters[0]!;
    expect(block("disk").contains(meter)).toBe(true);
    expect(meter.getAttribute("aria-label")).toBe("Sessions disk used");
    expect(meter.getAttribute("aria-valuemin")).toBe("0");
    expect(meter.getAttribute("aria-valuemax")).toBe("100");
    expect(Number(meter.getAttribute("aria-valuenow"))).toBeGreaterThan(99);
    expect(block("disk").textContent).toContain("398 MB free of 250 GB");
    // below 512 MiB the sessions disk warns
    expect(meter.getAttribute("data-tone")).toBe("warn");
  });

  test("does not warn with room to spare and draws no second volume", async () => {
    await open([
      doc({
        disk: { availableBytes: 100_000_000_000, sizeBytes: 250_000_000_000 },
        attention: undefined,
      }),
    ]);
    expect(screen.getAllByRole("meter")).toHaveLength(1);
    expect(screen.getByRole("meter").getAttribute("data-tone")).toBe("ok");
    expect(block("disk").textContent).not.toMatch(/home/i);
  });

  test("says it is not available when the reading failed, with no meter and no zero", async () => {
    await open([doc({ disk: undefined, unavailable: ["disk"] })]);
    expect(screen.queryByRole("meter")).toBeNull();
    expect(block("disk").textContent).toContain("Not available");
    expect(block("disk").textContent).not.toMatch(/\b0 B\b/);
  });

  test("says it is not available when the server sent no disk at all", async () => {
    await open([doc({ disk: undefined, unavailable: undefined })]);
    expect(screen.queryByRole("meter")).toBeNull();
    expect(block("disk").textContent).toContain("Not available");
  });
});

describe("activity", () => {
  test("counts the turns, names the oldest and the unfinished tasks by kind", async () => {
    await open([
      doc({
        turns: { running: 2, oldestSeconds: 340 },
        tasks: {
          agent: { running: 1 },
          command: { queued: 1, running: 2 },
          server: { running: 1 },
        },
      }),
    ]);
    const text = block("activity").textContent!;
    expect(text).toContain("Turns running");
    expect(text).toContain("2");
    expect(text).toContain("5m 40s");
    expect(text).toContain("commands");
    expect(text).toContain("2 running");
    expect(text).toContain("1 queued");
    expect(text).toContain("subagents");
    expect(text).toContain("servers");
  });

  test("in Russian the running count agrees with its number, the queued one does not need to", async () => {
    initLocale("ru");
    await open([
      doc({
        tasks: {
          agent: { running: 1 },
          command: { running: 3, queued: 4 },
          server: { running: 5 },
        },
      }),
    ]);
    expect(block("activity").textContent).toContain(
      "команды: 3 выполняются · 4 в очереди; субагенты: 1 выполняется; серверы: 5 выполняются",
    );
  });

  describe("the prompts waiting for an answer", () => {
    // Each kind is a row of its own, like the turns: a label and a count.
    const waiting = (label: string) =>
      within(block("activity")).getByText(label, { selector: "dt" })
        .nextElementSibling!.textContent;

    test("count the permission prompts and the questions", async () => {
      await open([doc({ prompts: { permission: 1, question: 2 } })]);
      expect(waiting("Awaiting permission")).toBe("1");
      expect(waiting("Awaiting an answer")).toBe("2");
    });

    test("a kind the document leaves out is none, and the whole object may be left out", async () => {
      await open([doc({ prompts: { question: 3 } })]);
      expect(waiting("Awaiting permission")).toBe("0");
      expect(waiting("Awaiting an answer")).toBe("3");
      cleanup();
      resetSystemStatusForTests();
      await open([doc()]);
      expect(waiting("Awaiting permission")).toBe("0");
      expect(waiting("Awaiting an answer")).toBe("0");
    });

    test("are no attention item", async () => {
      await open([
        doc({
          prompts: { permission: 2, question: 1 },
          attention: undefined,
          restartRequired: undefined,
        }),
      ]);
      expect(summary().getAttribute("data-state")).toBe("clear");
      expect(summary().textContent).toBe("");
    });

    test("in Russian", async () => {
      initLocale("ru");
      await open([doc({ prompts: { permission: 2, question: 5 } })]);
      // The label names what waits, not how many: a count of 2 or 5 beside
      // a verb in the plural would read as a sentence about the number.
      expect(waiting("Ожидание разрешения")).toBe("2");
      expect(waiting("Ожидание ответа")).toBe("5");
    });
  });

  test("a turn that started under a second ago is 0 seconds old, not 0 days", async () => {
    await open([doc({ turns: { running: 1, oldestSeconds: 0 } })]);
    const text = block("activity").textContent!;
    expect(text).toContain("0s");
    expect(text).not.toContain("0d");
  });

  test("with nothing running and no tasks says so, without an oldest turn", async () => {
    await open([doc({ turns: { running: 0 }, tasks: undefined })]);
    const text = block("activity").textContent!;
    expect(text).not.toContain("Oldest");
    expect(text).toContain("None");
  });
});

describe("a relay that does not register the node", () => {
  const relay = {
    code: "swarm_join_down",
    relay: "relay.example:12346",
    // 12 minutes before the document's generatedAt.
    offlineSince: "2026-10-15T09:48:00Z",
  };
  const lines = () =>
    [...block("subsystems").querySelectorAll("p.system-warn")].map(
      (p) => p.textContent,
    );

  test("is a summary link that takes the focus to the subsystems", async () => {
    await open([doc({ attention: [relay] })]);
    expect(summary().textContent).toContain("1 needs attention");
    expect(summary().getAttribute("data-state")).toBe("problems");
    fireEvent.click(
      within(summary()).getByRole("button", { name: "Relay unreachable" }),
    );
    expect(document.activeElement).toBe(heading("Subsystems"));
  });

  test("is told in the subsystems with the relay and for how long, in the format of Running for", async () => {
    await open([doc({ attention: [relay] })]);
    expect(lines()).toEqual([
      "Not registered with the relay relay.example:12346 for 12m",
    ]);
  });

  test("one line for each relay, and each is counted", async () => {
    await open([
      doc({
        attention: [
          relay,
          {
            ...relay,
            relay: "other.example",
            offlineSince: "2026-10-15T09:00:00Z",
          },
        ],
      }),
    ]);
    expect(summary().textContent).toContain("2 need attention");
    expect(lines()).toEqual([
      "Not registered with the relay relay.example:12346 for 12m",
      "Not registered with the relay other.example for 1h",
    ]);
  });

  test.each([
    ["without a time", undefined],
    ["with a time that does not parse", "yesterday"],
    ["with a time after the document's", "2026-10-15T10:05:00Z"],
  ])("%s leaves the duration out", async (_why, offlineSince) => {
    await open([
      doc({
        attention: [
          {
            code: "swarm_join_down",
            relay: "relay.example:12346",
            ...(offlineSince === undefined ? {} : { offlineSince }),
          },
        ],
      }),
    ]);
    expect(lines()).toEqual([
      "Not registered with the relay relay.example:12346",
    ]);
  });

  test("a clear reading has no such line", async () => {
    await open([doc({ attention: undefined, restartRequired: undefined })]);
    expect(lines()).toEqual([]);
  });

  test("stays among the last values of a refresh that failed", async () => {
    await open([doc({ attention: [relay] }), new Error("down")]);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(summary().getAttribute("data-state")).toBe("stale");
    expect(lines()).toEqual([
      "Not registered with the relay relay.example:12346 for 12m",
    ]);
  });

  test("in Russian", async () => {
    initLocale("ru");
    await open([doc({ attention: [relay] })]);
    expect(lines()).toEqual([
      "Нет регистрации на релее relay.example:12346 уже 12 мин",
    ]);
    fireEvent.click(
      within(summary()).getByRole("button", { name: "Релей недоступен" }),
    );
    expect(document.activeElement).toBe(heading("Подсистемы"));
  });
});

describe("the charts and the providers come with the metrics build only", () => {
  test("with metrics: the last hour as four charts, each an image named by its metric and latest value", async () => {
    await open([doc()]);
    expect(heading("Last hour")).toBeTruthy();
    const names = screen
      .getAllByRole("img")
      .map((img) => img.getAttribute("aria-label"));
    expect(names).toHaveLength(4);
    expect(names[0]).toMatch(/^Attempts per minute: ok /);
    expect(names[1]).toMatch(/^Attempt time: 5\.8 s/);
    expect(names[2]).toMatch(/^Tokens per minute: in 38K · out 2\.1K/);
    expect(names[3]).toMatch(/^Load: turns 2 · tasks 3/);
  });

  test("the Last hour heading takes the focus like every block's heading", async () => {
    await open([doc()]);
    expect(heading("Last hour").getAttribute("tabindex")).toBe("-1");
  });

  test("every card has a legend: the attempt time's names the average", async () => {
    await open([doc()]);
    const legends = [...document.querySelectorAll(".system-chart")].map(
      (card) => card.querySelector(".system-chart-legend")!.textContent,
    );
    expect(legends.every((text) => (text ?? "").length > 0)).toBe(true);
    expect(legends[1]).toBe("Avg time");
  });

  test("successful attempts are the ok tone and failed ones the error tone, never the accent", async () => {
    await open([doc()]);
    const card = document.querySelectorAll(".system-chart")[0]!;
    const tones = [...card.querySelectorAll("svg")].map(
      (svg) => /system-spark--(\w+)/.exec(svg.getAttribute("class")!)?.[1],
    );
    expect(tones).toEqual(["ok", "error"]);
    const keys = [...card.querySelectorAll(".system-chart-legend span")].map(
      (span) => span.className,
    );
    expect(keys).toEqual(["system-key--ok", "system-key--error"]);
  });

  test("with metrics: a row for each provider since start", async () => {
    await open([doc()]);
    const table = heading("Model providers since start").parentElement!;
    expect(table.textContent).toContain("local-mlx");
    expect(table.textContent).toContain("1,204");
    expect(table.textContent).toContain("0.3%");
    expect(table.textContent).toContain("6.1 s");
    expect(table.textContent).toMatch(/12 min/);
  });

  test("without metrics: no heading, no chart and no placeholder", async () => {
    await open([doc({ metrics: undefined })]);
    expect(screen.queryByText("Last hour")).toBeNull();
    expect(screen.queryByText("Model providers since start")).toBeNull();
    expect(screen.queryByRole("img")).toBeNull();
    expect(
      screen.getByTestId("system-section").querySelector("svg"),
    ).toBeNull();
    expect(screen.getByTestId("system-section").textContent).not.toMatch(
      /no data|empty|nothing/i,
    );
  });
});

describe("a server that cannot be read", () => {
  test("404: says it does not report its status, with its version", async () => {
    await open([{ status: 404 }], { version: "1.2.35" });
    expect(summary().textContent).toContain(
      "This server doesn't report its status (version 1.2.35)",
    );
    expect(summary().textContent).not.toContain("Everything OK");
    expect(screen.queryByTestId("system-restart")).toBeNull();
  });

  test("401: says the server refused", async () => {
    await open([{ status: 401 }]);
    expect(summary().textContent).toContain("refused");
    expect(summary().textContent).not.toContain("Everything OK");
  });

  test("a node a relay cannot reach: offline, with when it was last seen", async () => {
    await open([
      {
        status: 502,
        body: {
          error: {
            node: "worker-a",
            reason: "node is registered but not reachable",
            last_seen: "2026-10-15T09:41:00Z",
          },
        },
      },
    ]);
    expect(summary().textContent).toContain("worker-a is offline");
    expect(summary().textContent).toMatch(/Last seen .*19 min/);
  });

  test("a first read that failed says so, and shows no number of its own", async () => {
    await open([new Error("down")]);
    expect(summary().textContent).toContain("Can't read the status");
    expect(summary().textContent).not.toContain("Everything OK");
    expect(summary().textContent).not.toMatch(/\d need/);
  });
});

describe("refreshing", () => {
  test("reads when it opens and then every 15 seconds while shown and visible", async () => {
    const fetch = await open([doc()]);
    expect(fetch.reads).toHaveLength(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(14_999);
    });
    expect(fetch.reads).toHaveLength(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(fetch.reads).toHaveLength(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(fetch.reads).toHaveLength(4);
  });

  test("skips a tick while the page is hidden and reads again on the first tick after it is shown", async () => {
    const fetch = await open([doc()]);
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => "hidden",
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(fetch.reads).toHaveLength(1);
    delete (document as { visibilityState?: unknown }).visibilityState;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(fetch.reads).toHaveLength(2);
  });

  test("stops when the tab is put away under search results, and when it unmounts", async () => {
    const fetch = serve([doc()]);
    const view = render(
      <PaneHidden.Provider value={true}>
        <SystemSection />
      </PaneHidden.Provider>,
    );
    await settled();
    const opened = fetch.reads.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(fetch.reads).toHaveLength(opened);
    view.unmount();

    const again = serve([doc()]);
    const second = render(<SystemSection />);
    await settled();
    second.unmount();
    const afterUnmount = again.reads.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(again.reads).toHaveLength(afterUnmount);
  });

  test("says how old the values are in a line that is not live", async () => {
    await open([doc()]);
    const updated = screen.getByTestId("system-updated");
    expect(updated.textContent).toMatch(/^Updated /);
    expect(updated.closest("[aria-live]")).toBeNull();
    const first = updated.textContent;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(screen.getByTestId("system-updated").textContent).not.toBe(first);
  });

  // A block that was a new component type on every render was remounted by
  // each read: the focused heading and the copy button lost their focus.
  test("keeps the focused heading in place when the next read arrives", async () => {
    const fetch = await open([
      doc(),
      doc({ turns: { running: 7, oldestSeconds: 12 } }),
    ]);
    fireEvent.click(
      within(summary()).getByRole("button", { name: "Disk low" }),
    );
    const focused = heading("Disk");
    expect(document.activeElement).toBe(focused);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(fetch.reads).toHaveLength(2);
    // the new document was drawn ...
    expect(block("activity").textContent).toContain("7");
    // ... into the same elements
    expect(heading("Disk")).toBe(focused);
    expect(focused.isConnected).toBe(true);
    expect(document.activeElement).toBe(focused);
  });

  test("keeps the copy button, its focus and its Copied state across the next read", async () => {
    vi.stubGlobal("navigator", { clipboard: { writeText: async () => {} } });
    const fetch = await open([doc(), doc()]);
    // 1.5 s before the read, so the 2 s Copied state is still showing after it
    await act(async () => {
      await vi.advanceTimersByTimeAsync(13_500);
    });
    const copy = within(block("installation")).getByRole("button", {
      name: "Copy",
    });
    copy.focus();
    await act(async () => {
      fireEvent.click(copy);
    });
    expect(copy.textContent).toBe("Copied");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(fetch.reads).toHaveLength(2);
    const after = within(block("installation")).getByRole("button", {
      name: "Copied",
    });
    expect(after).toBe(copy);
    expect(document.activeElement).toBe(copy);
  });

  test("never writes the settings: it draws no field and marks nothing unsaved", async () => {
    await open([doc()]);
    expect(
      screen
        .getByTestId("system-section")
        .querySelectorAll("input, select, textarea"),
    ).toHaveLength(0);
  });
});

describe("another environment", () => {
  test("a remote environment's status is read from the remote, with its token in a header", async () => {
    const fetch = serve([doc()]);
    installRemoteFetchShim();
    setEnv({ mode: "remote", baseUrl: "http://box.lan:12345", token: "tok-9" });
    render(<SystemSection />);
    await settled();
    const call = fetch.mock.calls.find(([input]) =>
      String(input).endsWith("/coddy/status"),
    )!;
    expect(String(call[0])).toBe("http://box.lan:12345/coddy/status");
    expect(new Headers(call[1]?.headers).get("Authorization")).toBe(
      "Bearer tok-9",
    );
  });

  test("a node behind a relay is read from its mount", async () => {
    const fetch = serve([doc()]);
    installRemoteFetchShim();
    setEnv({
      mode: "remote",
      baseUrl: "http://relay.lan:12346/swarm/nodes/worker-a",
      token: "client",
      swarmRelay: "http://relay.lan:12346",
      swarmNode: "worker-a",
    });
    render(<SystemSection />);
    await settled();
    expect(fetch.mock.calls.map(([input]) => String(input))).toContain(
      "http://relay.lan:12346/swarm/nodes/worker-a/coddy/status",
    );
  });
});

describe("in Russian", () => {
  test("names the blocks, the count and the people and things in the words of the interface", async () => {
    initLocale("ru");
    await open([
      doc({
        attention: [
          { code: "disk_low" },
          { code: "restart_required" },
          { code: "subsystem_down", subsystem: "metrics" },
        ],
        tasks: { agent: { running: 1 } },
      }),
    ]);
    expect(summary().textContent).toContain("3 требуют внимания");
    expect(heading("Эта установка")).toBeTruthy();
    expect(block("installation").textContent).toContain("Запущен через");
    expect(block("activity").textContent).toContain("субагенты");
  });
});

describe("Sparkline", () => {
  const polylines = (container: HTMLElement) =>
    [...container.querySelectorAll("polyline")].map((p) =>
      p.getAttribute("points"),
    );

  test("draws one line for each run of points and a gap for each missing one", () => {
    const { container } = render(
      <Sparkline
        values={[1, 2, null, 3, 4, null, null, 5]}
        label="Attempts per minute"
        latest="5"
      />,
    );
    expect(polylines(container)).toHaveLength(3);
    // A gap leaves its column empty: nothing is drawn at the x of a missing
    // point (the third, the sixth and the seventh of eight over 240 units).
    const xs = polylines(container)
      .join(" ")
      .split(" ")
      .map((p) => Number(p.split(",")[0]));
    for (const gap of [2, 5, 6]) {
      expect(xs).not.toContain(Number(((gap * 240) / 7).toFixed(1)));
    }
    for (const run of [0, 1, 3, 4, 7]) {
      expect(xs).toContain(Number(((run * 240) / 7).toFixed(1)));
    }
  });

  test("is an image named by its metric and its latest value", () => {
    render(
      <Sparkline values={[1, 2, 3]} label="Attempts per minute" latest="3" />,
    );
    expect(
      screen.getByRole("img", { name: "Attempts per minute: 3" }),
    ).toBeTruthy();
  });

  test("draws nothing but the image when every point is missing", () => {
    const { container } = render(
      <Sparkline values={[null, null]} label="Load" latest="" />,
    );
    expect(polylines(container)).toEqual([]);
    expect(screen.getByRole("img")).toBeTruthy();
  });

  test("a constant series is a flat line inside the plot, not on its edge or NaN", () => {
    const { container } = render(
      <Sparkline values={[3, 3, 3]} label="Load" latest="3" />,
    );
    const points = polylines(container)[0]!;
    expect(points).not.toContain("NaN");
  });
});

describe("formatting", () => {
  test("the unfinished tasks read the same in English whatever the count", () => {
    expect(
      tasksText({
        agent: { running: 1 },
        command: { queued: 1, running: 2 },
        server: { running: 21 },
      }),
    ).toBe(
      "commands: 2 running · 1 queued; subagents: 1 running; servers: 21 running",
    );
  });

  test("a running count takes the Russian form of its number", () => {
    initLocale("ru");
    const running = (n: number) => tasksText({ command: { running: n } });
    expect(running(1)).toBe("команды: 1 выполняется");
    expect(running(21)).toBe("команды: 21 выполняется");
    expect(running(2)).toBe("команды: 2 выполняются");
    expect(running(24)).toBe("команды: 24 выполняются");
    expect(running(5)).toBe("команды: 5 выполняются");
    expect(running(11)).toBe("команды: 11 выполняются");
    expect(running(0)).toBe("команды: 0 выполняются");
    // a state the dictionary does not know is named as the server names it
    expect(tasksText({ command: { paused: 2 } })).toBe("команды: 2 paused");
  });

  test("a token rate is a whole number below a thousand and compact above", () => {
    // A rate per minute is fractional; under a thousand the decimal is noise,
    // as the other charts' values show none.
    expect(compact(356.1)).toBe("356");
    expect(compact(2100)).toBe("2.1K");
    expect(compact(38000)).toBe("38K");
    expect(compact(2.5)).toBe("2.5");
  });

  test("a time under a second is told in milliseconds, never as 0 seconds", () => {
    expect(seconds(5.8)).toBe("5.8 sec");
    expect(seconds(42)).toBe("42 sec");
    expect(seconds(0.25)).toBe("250 ms");
    expect(seconds(0.00052)).toBe("0.5 ms");
    expect(seconds(0)).toBe("0 ms");
    initLocale("ru");
    expect(seconds(0.25)).toBe("250 мс");
  });
});

/**
 * Contract: the System tab fits a phone and a finger.
 *
 * jsdom does no layout, so what keeps the page from scrolling sideways at 390px
 * and the copy button reachable is pinned in the stylesheet: values and
 * addresses wrap, the grid may shrink below its content, the copy button is at
 * least 44px, and the charts become one-line rows under the phone width.
 */
describe("the stylesheet", () => {
  const dir = dirname(fileURLToPath(import.meta.url));
  const css = readFileSync(join(dir, "../../styles.scss"), "utf8").replace(
    /\/\*[\s\S]*?\*\//g,
    "",
  );

  /** Collapses whitespace, which Prettier spreads values over lines with. */
  const squash = (text: string) => text.replace(/\s+/g, " ").trim();

  /** A grid track list as one line, with no space inside a function's parentheses. */
  const tracks = (body: string) =>
    squash(/grid-template-columns:\s*([^;]+);/.exec(body)?.[1] ?? "")
      .replace(/\(\s+/g, "(")
      .replace(/\s+\)/g, ")");

  /** The declarations of every rule whose selector list names `selector` exactly. */
  function bodyOf(selector: string, source: string = css): string {
    return [...source.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
      .filter((m) =>
        m[1]!
          .split(",")
          .map((s) => squash(s))
          .includes(selector),
      )
      .map((m) => squash(m[2]!))
      .join(" ");
  }

  /** The text of the phone block(s): what sits under `@media (max-width: 599px)`. */
  function phoneRules(): string {
    const opener = "@media (max-width: 599px) {";
    const out: string[] = [];
    for (let at = css.indexOf(opener); at !== -1; ) {
      let depth = 0;
      let i = at + opener.length - 1;
      for (; i < css.length; i++) {
        if (css[i] === "{") depth++;
        else if (css[i] === "}" && --depth === 0) break;
      }
      out.push(css.slice(at + opener.length, i));
      at = css.indexOf(opener, i);
    }
    return out.join("\n");
  }

  const px = (body: string, property: string): number =>
    Number(
      new RegExp(`(?:^|[;\\s])${property}\\s*:\\s*(\\d+(?:\\.\\d+)?)px`).exec(
        body,
      )?.[1] ?? 0,
    );

  test("the copy button is at least 44px in both directions", () => {
    const body = bodyOf(".system-copy");
    expect(px(body, "min-width")).toBeGreaterThanOrEqual(44);
    expect(px(body, "min-height")).toBeGreaterThanOrEqual(44);
  });

  test("a value, an address and a path wrap instead of widening the page", () => {
    expect(bodyOf(".system-kv dd")).toMatch(/overflow-wrap:\s*anywhere/);
    expect(bodyOf(".system-kv dd")).toMatch(/min-width:\s*0/);
    expect(bodyOf(".system-section")).toMatch(/min-width:\s*0/);
    expect(bodyOf(".system-kv")).toMatch(/minmax\(0,\s*1fr\)/);
    // no value is held to one line
    expect(bodyOf(".system-kv dd")).not.toMatch(/white-space:\s*nowrap/);
    expect(bodyOf(".system-code")).toMatch(/overflow-wrap:\s*anywhere/);
    // a relay's host:port in the sentence about it
    expect(bodyOf(".system-relay")).toMatch(/overflow-wrap:\s*anywhere/);
  });

  test("the stale tab greys its values and the summary keeps its colour", () => {
    expect(bodyOf(".system-section.is-stale .system-blocks")).toMatch(
      /opacity:\s*0?\.\d+/,
    );
    expect(bodyOf(".system-section.is-stale .system-summary")).not.toMatch(
      /opacity/,
    );
  });

  test("the meter's track is a bar whose fill follows the tone", () => {
    expect(bodyOf(".system-meter")).toMatch(/height:\s*\d+px/);
    expect(css).toMatch(/\.system-meter\[data-tone="warn"\]/);
  });

  test("under the phone width a chart is one line: title, line, value", () => {
    const phone = phoneRules();
    expect(phone).toMatch(/\.system-chart\s*\{[^}]*grid-template-columns/);
    expect(phone).toMatch(/\.system-chart-legend\s*\{[^}]*display:\s*none/);
  });

  test("the badge sits after the rail's label and at the tile's top right", () => {
    expect(bodyOf(".settings-nav-badge")).toMatch(/border-radius/);
    expect(css).toMatch(/\.settings-tile-badge\s*\{[^}]*position:\s*absolute/);
    expect(css).toMatch(/\.settings-tile-badge\s*\{[^}]*top:/);
    expect(css).toMatch(/\.settings-tile-badge\s*\{[^}]*right:/);
  });

  test("a legend swatch has the colour of its line", () => {
    // The base swatch rule names the legend's span; a tone has to outrank it.
    for (const tone of ["error", "ok", "muted"]) {
      expect(
        bodyOf(`.system-chart-legend > .system-key--${tone}::before`),
      ).toMatch(/background:/);
    }
  });

  test("a chart's title and value share the card's head without running into each other", () => {
    // The title keeps at least its longest word and the value wraps into the
    // room that is left: with an "auto" value track the value took its whole
    // width and the title's words ran under it (Russian always, English with
    // four-digit rates).
    const columns = tracks(bodyOf(".system-chart"));
    expect(columns).toMatch(
      /^minmax\(min-content, ?1fr\) minmax\(0, ?max-content\)$/,
    );
    expect(bodyOf(".system-chart-value")).toMatch(/text-align:\s*right/);
  });

  test("the disk meter keeps a width of its own beside a long free-space text", () => {
    const columns = tracks(bodyOf(".system-disk"));
    const meter = /minmax\((\d+)px, ?1fr\)/.exec(columns);
    expect(Number(meter?.[1] ?? 0)).toBeGreaterThanOrEqual(80);
    expect(columns).toMatch(/minmax\(0, ?max-content\)$/);
  });

  test("the meter is a 6px track like the usage meter's", () => {
    expect(px(bodyOf(".system-meter"), "height")).toBe(6);
  });

  /** The value a declaration of `property` has in a rule body, squashed. */
  const declared = (body: string, property: string): string =>
    squash(
      new RegExp(`(?:^|[;\\s])${property}\\s*:\\s*([^;]+);`).exec(body)?.[1] ??
        "",
    );

  test("the summary is the app's notice: a settings frame, and for problems the usage banner's amber", () => {
    // Every state but problems is a quiet frame like a settings fieldset.
    const base = bodyOf(".system-summary");
    expect(declared(base, "border")).toBe(
      "1px solid var(--coddy-glass-panel-border)",
    );
    expect(declared(base, "background")).toBe("var(--coddy-surface-fieldset)");
    // Problems wear the composer's usage banner: its border and its ground.
    const banner = bodyOf(".usage-banner");
    const problems = bodyOf('.system-summary[data-state="problems"]');
    expect(declared(problems, "border-color")).toBe(
      declared(banner, "border").replace(/^1px solid /, ""),
    );
    expect(declared(problems, "background")).toBe(
      declared(banner, "background"),
    );
    expect(declared(problems, "color")).toBe("var(--text)");
    // A clear page draws no summary: the empty region has no frame and
    // takes back the gap the section puts after it.
    expect(bodyOf('.system-summary[data-state="clear"]')).toBe("");
    const empty = bodyOf(".system-summary:empty");
    expect(declared(empty, "border")).toBe("0");
    expect(declared(empty, "padding")).toBe("0");
    expect(declared(empty, "margin-bottom")).toBe(
      `-${px(bodyOf(".system-section"), "gap")}px`,
    );
  });

  test("the restart notice is an information notice: the edit banner's accent frame", () => {
    const notice = bodyOf(".system-restart");
    expect(declared(notice, "border")).toBe(
      declared(bodyOf(".composer-edit-banner"), "border"),
    );
    expect(declared(notice, "background")).toBe(
      "var(--coddy-surface-fieldset)",
    );
  });

  test("a chart card is framed like a settings fieldset", () => {
    const card = bodyOf(".system-chart");
    expect(declared(card, "border")).toBe(
      "1px solid var(--coddy-glass-panel-border)",
    );
    expect(declared(card, "background")).toBe("var(--coddy-surface-fieldset)");
  });

  test("the attention badge is the rail's count: the accent and its on-accent text", () => {
    const rail = bodyOf(".rail-active-count");
    for (const badge of [".settings-nav-badge", ".settings-tile-badge"]) {
      const body = bodyOf(badge);
      expect(declared(body, "background")).toBe(declared(rail, "background"));
      expect(declared(body, "color")).toBe(declared(rail, "color"));
    }
  });

  test("a stopped subsystem, a refused restart and failed attempts wear the error tone", () => {
    expect(bodyOf('.system-pill[data-state="stopped"]')).toMatch(
      /var\(--system-error\)/,
    );
    expect(bodyOf('.system-restart[data-tone="error"]')).toMatch(
      /var\(--system-error\)/,
    );
    expect(bodyOf(".system-spark--error")).toMatch(/var\(--system-error\)/);
    expect(bodyOf(".system-chart-legend > .system-key--error::before")).toMatch(
      /var\(--system-error\)/,
    );
  });

  test("the ok and error tones come from the theme, so every preset keeps its own green and red", () => {
    const tokens = bodyOf(".settings.drawer");
    expect(tokens).toMatch(/--system-ok:[^;]*var\(--coddy-todo-completed\)/);
    expect(tokens).toMatch(/--system-error:[^;]*var\(--coddy-danger\)/);
    // No dark preset overrides them with a literal of its own.
    expect(
      bodyOf('html:not([data-theme="light"]) .settings.drawer'),
    ).not.toMatch(/--system-(ok|error)/);
  });

  test("labels, headings, legends and notes are quiet text mixed from the theme's text, not --muted", () => {
    // --muted is too dim on several presets (nord's reads 2.1:1 on its canvas);
    // the settings rail and tiles mix --text instead.
    expect(bodyOf(".settings.drawer")).toMatch(
      /--system-quiet:\s*color-mix\(in srgb, var\(--text\) \d+%, var\(--coddy-blend-base\)\)/,
    );
    const systemRules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].filter((m) =>
      /\.system-/.test(m[1]!),
    );
    const muted = systemRules
      .filter((m) => /var\(--muted\)/.test(m[2]!))
      .map((m) => squash(m[1]!));
    expect(muted).toEqual([]);
  });

  test("under the phone width a value stands at the right of its row", () => {
    expect(phoneRules()).toMatch(/\.system-kv dd\s*\{[^}]*text-align:\s*right/);
  });

  test("under the phone width every chart's line starts and ends at the same place", () => {
    const columns =
      /\.system-chart\s*\{[^}]*grid-template-columns:\s*([^;]+);/.exec(
        phoneRules(),
      )?.[1];
    const parts = squash(columns ?? "")
      .replace(/\(([^)]*)\)/g, (group) => group.replace(/\s+/g, ""))
      .split(" ");
    // The title and the value columns have fixed widths, so a long value does
    // not shorten its line next to the others.
    expect(parts).toHaveLength(3);
    expect(parts[0]).toMatch(/^\d+(\.\d+)?(px|rem)$/);
    expect(parts[2]).toMatch(/^\d+(\.\d+)?(px|rem)$/);
  });
});
