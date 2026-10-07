import React from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";
import { RAIL_SCREENS, type RailScreenId } from "./nav/railEscape";
import { resetSettingsConfigForTests } from "./settings/settingsConfigStore";
import { shellStackMaxWidthMediaQuery } from "./shellBreakpoint";
import {
  appNavHrefDocs,
  appNavHrefHistory,
  appNavHrefScheduler,
  appNavHrefSettings,
  appNavHrefSwarm,
} from "./scheduler/hashRoute";

/**
 * Escape closes the screen the rail opened - History, Scheduler, Swarm, Docs,
 * Settings - the way its close control does: the screen goes and the address
 * is the chat's again. Before, only History and the scheduler listened for the
 * key, and History left `#/history` in the address behind it.
 */

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: (props: {
    backgroundTasksOpen?: boolean;
    onOpenBackgroundTasks?: () => void;
  }) => (
    <div data-testid="chat-screen-stub">
      <button
        type="button"
        data-testid="open-tasks"
        onClick={() => props.onOpenBackgroundTasks?.()}
      >
        Open tasks
      </button>
      <output data-testid="chat-tasks-open">
        {String(props.backgroundTasksOpen === true)}
      </output>
    </div>
  ),
}));

const SID = "sess_a";
const OTHER_SID = "sess_b";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const docsContents = {
  version: "1.2.3",
  groups: [
    {
      id: "features",
      title: "Features",
      summary: "What it does.",
      pages: [
        { slug: "features/modes", title: "Operating modes", summary: "Modes." },
      ],
    },
  ],
};

const docsPage = {
  version: "1.2.3",
  slug: "features/modes",
  title: "Operating modes",
  summary: "Modes.",
  group: { id: "features", title: "Features" },
  anchor: "",
  markdown: "# Operating modes\n\nAgent, plan and ask.\n",
  headings: [{ level: 1, text: "Operating modes", anchor: "operating-modes" }],
  prev: null,
  next: null,
  url: "https://coddy.dev/docs/features/modes",
};

// A list section whose row opens a form: the Settings head then leads back
// to the list with an arrow, and Escape takes that step first.
const configSchema = {
  type: "object",
  "x-coddy-property-order": ["models"],
  properties: {
    models: {
      type: "array",
      title: "Logical models",
      items: {
        type: "object",
        properties: { model: { type: "string", title: "Model" } },
      },
    },
  },
};

const scheduler = {
  enabled: true,
  dir: "/tmp/jobs",
  timeout: "30m",
  max_queue: 4,
  runs_active: 3,
  retain_sessions: 10,
};

const job = {
  job_id: "nightly",
  description: "Nightly report",
  schedule: "0 3 * * *",
  paused: false,
  running: false,
};

function heldStream() {
  return new Response(new ReadableStream<Uint8Array>({ start: () => {} }), {
    headers: { "Content-Type": "text/event-stream" },
  });
}

// A relay answers the probe; the tests of the relay's own screens turn it on.
let relay = false;

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const path = String(input);
  if (path === "/coddy/events") return heldStream();
  if (path === "/swarm/info" && relay) {
    return json({
      swarm: true,
      name: "relay",
      uuid: "r-1",
      version: "1.2.3",
      node_count: 0,
      started_at: "2026-09-26T00:00:00Z",
      registry_warming: false,
    });
  }
  if (path.startsWith("/coddy/scheduler/jobs/nightly")) return json(job);
  if (path.startsWith("/coddy/scheduler/jobs"))
    return json({ scheduler, jobs: [job] });
  if (path === "/coddy/docs") return json(docsContents);
  if (path.startsWith("/coddy/docs/page")) return json(docsPage);
  if (path === "/coddy/config/schema") return json(configSchema);
  if (path === "/coddy/config")
    return json({ models: [{ model: "fake/alpha" }] });
  if (path.startsWith("/coddy/sessions?")) {
    return json({
      active_count: 2,
      sessions: [
        { id: SID, title: "A chat" },
        { id: OTHER_SID, title: "Another chat" },
      ],
    });
  }
  if (path.startsWith(`/coddy/sessions/${SID}/messages`)) {
    return json({ session_id: SID, messages: [] });
  }
  if (path.startsWith(`/coddy/sessions/${OTHER_SID}/messages`)) {
    return json({ session_id: OTHER_SID, messages: [] });
  }
  return json({}, 404);
});

beforeEach(() => {
  relay = false;
  // Settings keeps a copy of the config between openings; each test starts
  // with none, as a fresh page does.
  resetSettingsConfigForTests();
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", "/");
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  history.replaceState(null, "", "/");
});

function mountInChat() {
  history.replaceState(null, "", `/#/s/${SID}`);
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
}

/** Escape as a keyboard sends it: at whatever has the focus, up to the window. */
function pressEscape(
  target: Element = document.activeElement ?? document.body,
) {
  return fireEvent.keyDown(target, { key: "Escape" });
}

const settle = () => act(async () => new Promise((r) => setTimeout(r, 50)));

async function backInChat(screenTestId: string) {
  await waitFor(() => expect(screen.queryByTestId(screenTestId)).toBeNull());
  await waitFor(() => expect(window.location.hash).toBe(`#/s/${SID}`));
}

test("Escape closes the documentation reader opened from the rail", async () => {
  mountInChat();
  fireEvent.click(await screen.findByTestId("nav-docs"));
  await screen.findByText("Agent, plan and ask.");
  pressEscape();
  await backInChat("docs-view");
});

test("Escape closes Settings opened from the rail", async () => {
  mountInChat();
  fireEvent.click(await screen.findByTestId("nav-settings"));
  await screen.findByTestId("settings-screen");
  pressEscape();
  await backInChat("settings-screen");
});

test("Escape still closes History, and the address leaves #/history", async () => {
  mountInChat();
  fireEvent.click(await screen.findByTestId("nav-history"));
  await screen.findByTestId("sessions");
  pressEscape();
  await backInChat("sessions");
});

test("Escape still closes the scheduler, its job editor first", async () => {
  mountInChat();
  fireEvent.click(await screen.findByTestId("nav-scheduler"));
  await screen.findByTestId("scheduler-drawer");
  fireEvent.click(await screen.findByText("nightly"));
  await waitFor(() =>
    expect(window.location.hash).toBe("#/scheduler/jobs/nightly"),
  );
  pressEscape();
  await waitFor(() => expect(window.location.hash).toBe("#/scheduler"));
  expect(screen.getByTestId("scheduler-drawer")).toBeTruthy();
  pressEscape();
  await backInChat("scheduler-drawer");
});

test("in Settings Escape leaves an open row for its list first, then Settings", async () => {
  mountInChat();
  history.replaceState(null, "", "/#/settings/models?id=fake%2Falpha");
  window.dispatchEvent(new HashChangeEvent("hashchange"));
  await screen.findByTestId("settings-head-back");
  pressEscape();
  await waitFor(() =>
    expect(screen.queryByTestId("settings-head-back")).toBeNull(),
  );
  expect(screen.getByTestId("settings-master-item-0")).toBeTruthy();
  expect(window.location.hash).toBe("#/settings/models");
  pressEscape();
  await backInChat("settings-screen");
});

test("on the stacked shell Escape takes Settings back to its tiles first, then closes it", async () => {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query === shellStackMaxWidthMediaQuery,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
  mountInChat();
  history.replaceState(null, "", "/#/settings/appearance");
  window.dispatchEvent(new HashChangeEvent("hashchange"));
  await screen.findByTestId("settings-head-back");
  await settle();
  pressEscape();
  await screen.findByTestId("settings-tile-grid");
  expect(screen.queryByTestId("settings-head-back")).toBeNull();
  await waitFor(() => expect(window.location.hash).toBe("#/settings"));
  pressEscape();
  await backInChat("settings-screen");
});

test("on desktop Tasks stays open across rail screens and uses local panel state", async () => {
  mountInChat();
  fireEvent.click(await screen.findByTestId("open-tasks"));
  await screen.findByTestId("bgtasks-panel");
  expect(window.location.hash).toBe(`#/s/${SID}`);

  for (const [trigger, panel] of [
    ["nav-history", "sessions"],
    ["nav-settings", "settings-screen"],
    ["nav-scheduler", "scheduler-drawer"],
    ["nav-docs", "docs-view"],
  ] as const) {
    fireEvent.click(await screen.findByTestId(trigger));
    await screen.findByTestId(panel);
    expect(screen.getByTestId("bgtasks-panel")).toBeTruthy();
  }

  fireEvent.click(screen.getByTestId("bgtasks-panel-close"));
  await waitFor(() => expect(screen.queryByTestId("bgtasks-panel")).toBeNull());
  expect(window.location.hash).toMatch(/^#\/docs/);
  expect(window.location.hash).not.toContain("/tasks");
  fireEvent.click(screen.getByTestId("open-tasks"));
  await screen.findByTestId("bgtasks-panel");
  expect(window.location.hash).toMatch(/^#\/docs/);
  expect(window.location.hash).not.toContain("/tasks");
});

// Tasks belongs to the chat on screen, not to the rail: on desktop it stays
// open while History picks another chat and shows that chat's tasks, and the
// backdrop that takes History down leaves it where it was.
test("on desktop Tasks follows the chat picked in History and outlives its backdrop", async () => {
  mountInChat();
  fireEvent.click(await screen.findByTestId("open-tasks"));
  await screen.findByTestId("bgtasks-panel");

  fireEvent.click(await screen.findByTestId("nav-history"));
  await screen.findByTestId("sessions");
  fireEvent.click(await screen.findByTestId(`session-row-${OTHER_SID}`));
  await waitFor(() =>
    expect(window.location.hash).toMatch(new RegExp(`^#/s/${OTHER_SID}`)),
  );
  expect(screen.getByTestId("bgtasks-panel")).toBeTruthy();
  await waitFor(() =>
    expect(
      fetchMock.mock.calls.some(([input]) =>
        String(input).startsWith(
          `/coddy/sessions/${OTHER_SID}/background-tasks`,
        ),
      ),
    ).toBe(true),
  );

  fireEvent.click(document.querySelector(".backdrop.is-open")!);
  await waitFor(() => expect(screen.queryByTestId("sessions")).toBeNull());
  await settle();
  expect(screen.getByTestId("bgtasks-panel")).toBeTruthy();
  expect(window.location.hash).toMatch(new RegExp(`^#/s/${OTHER_SID}`));
});

test("on the stacked shell a rail screen closes the full-screen Tasks panel", async () => {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query === shellStackMaxWidthMediaQuery,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
  mountInChat();
  fireEvent.click(await screen.findByTestId("open-tasks"));
  await screen.findByTestId("bgtasks-panel");
  expect(window.location.hash).toBe(`#/s/${SID}/tasks`);

  fireEvent.click(await screen.findByTestId("nav-history"));
  await screen.findByTestId("sessions");
  expect(screen.queryByTestId("bgtasks-panel")).toBeNull();
});

test("an Escape the documentation search takes clears it and leaves the reader open", async () => {
  mountInChat();
  fireEvent.click(await screen.findByTestId("nav-docs"));
  await screen.findByText("Agent, plan and ask.");
  const search = screen.getByTestId("docs-search");
  fireEvent.change(search, { target: { value: "modes" } });
  pressEscape(search);
  expect((search as HTMLInputElement).value).toBe("");
  expect(screen.getByTestId("docs-view")).toBeTruthy();
  // With nothing left to clear, the next one is the reader's.
  pressEscape(search);
  await backInChat("docs-view");
});

/**
 * Where each screen of the rail lives: its address, which is where its rail
 * link points, and what it draws. A Record: a screen added to the rail does not
 * compile here until it says both, and the test below then holds it to Escape.
 */
const RAIL_SCREEN_PAGES: Record<
  RailScreenId,
  { href: string; testId: string }
> = {
  history: { href: appNavHrefHistory(), testId: "sessions" },
  scheduler: { href: appNavHrefScheduler(), testId: "scheduler-drawer" },
  swarm: { href: appNavHrefSwarm(), testId: "swarm-view" },
  docs: { href: appNavHrefDocs(), testId: "docs-view" },
  settings: { href: appNavHrefSettings(), testId: "settings-screen" },
};

test("every screen of the rail closes on Escape and gives the address back to the chat", async () => {
  for (const id of RAIL_SCREENS) {
    const page = RAIL_SCREEN_PAGES[id];
    mountInChat();
    await screen.findByTestId("nav-settings");
    history.replaceState(null, "", `/${page.href}`);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
    await screen.findByTestId(page.testId);
    // What the screen reads on opening is in (the reader moves on to its
    // first page): the operator presses Escape on a screen they see.
    await settle();
    pressEscape();
    await waitFor(() =>
      expect(screen.queryByTestId(page.testId), id).toBeNull(),
    );
    await waitFor(() => expect(window.location.hash, id).toBe(`#/s/${SID}`));
    cleanup();
  }
});

test("an Escape that folds a row menu of History leaves History open", async () => {
  mountInChat();
  fireEvent.click(await screen.findByTestId("nav-history"));
  fireEvent.click(await screen.findByTestId(`session-menu-${SID}`));
  await screen.findByTestId(`session-menu-rename-${SID}`);
  pressEscape();
  expect(screen.queryByTestId(`session-menu-rename-${SID}`)).toBeNull();
  expect(screen.getByTestId("sessions")).toBeTruthy();
  pressEscape();
  await backInChat("sessions");
});

test("the backdrop takes the swarm screen down too", async () => {
  mountInChat();
  await screen.findByTestId("nav-settings");
  history.replaceState(null, "", `/${appNavHrefSwarm()}`);
  window.dispatchEvent(new HashChangeEvent("hashchange"));
  await screen.findByTestId("swarm-view");
  fireEvent.click(document.querySelector(".backdrop.is-open")!);
  await backInChat("swarm-view");
});

test("on a relay the swarm is home: Escape leaves it, and closes what opened over it", async () => {
  relay = true;
  history.replaceState(null, "", "/");
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await screen.findByTestId("swarm-view");
  pressEscape();
  expect(screen.getByTestId("swarm-view")).toBeTruthy();
  fireEvent.click(await screen.findByTestId("nav-settings"));
  await screen.findByTestId("settings-screen");
  pressEscape();
  await waitFor(() =>
    expect(screen.queryByTestId("settings-screen")).toBeNull(),
  );
  expect(screen.getByTestId("swarm-view")).toBeTruthy();
  expect(window.location.hash).toBe("");
});

// On a relay the map is home: its entry in the rail stays lit while the map is
// on screen, and the brand, which starts a new chat on an agent, leads there
// too - a relay has no chat to start (issue #401).
test("on a relay the Swarm entry stays lit, and the brand leads to the map", async () => {
  relay = true;
  history.replaceState(null, "", "/");
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await screen.findByTestId("swarm-view");
  await waitFor(() =>
    expect(screen.getByTestId("nav-swarm")).toHaveAttribute(
      "aria-pressed",
      "true",
    ),
  );
  // The page is the relay's own, which has no documentation to read.
  await waitFor(() => expect(screen.queryByTestId("nav-docs")).toBeNull());
  fireEvent.click(await screen.findByTestId("nav-settings"));
  await screen.findByTestId("settings-screen");
  await waitFor(() =>
    expect(screen.getByTestId("nav-swarm")).toHaveAttribute(
      "aria-pressed",
      "false",
    ),
  );
  fireEvent.click(screen.getByTestId("nav-home"));
  await screen.findByTestId("swarm-view");
  await waitFor(() =>
    expect(screen.getByTestId("nav-swarm")).toHaveAttribute(
      "aria-pressed",
      "true",
    ),
  );
});
