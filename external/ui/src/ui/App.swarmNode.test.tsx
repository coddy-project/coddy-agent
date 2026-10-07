import React from "react";
import {
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
import { resetSettingsConfigForTests } from "./settings/settingsConfigStore";
import { resetConfiguredRemotesForTests } from "./env/configuredRemotes";
import { rememberSchedulerLinked } from "./env/pageMemory";
import { installRemoteFetchShim, setEnv } from "./env/remoteEnv";

/**
 * The swarm map over a node (issue #401). Inside a node the environment is the
 * node's mount; the Swarm entry used to switch the whole app back to the relay,
 * which reloaded the page (the map blinked in) and took the node's History and
 * Scheduler away before anything had been left. The map now reads the relay
 * directly and opens over the node, which stays where it is.
 */

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: () => <div data-testid="chat-screen-stub" />,
}));

const RELAY = "http://relay.test";
const NODE_MOUNT = RELAY + "/swarm/nodes/worker-a";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

function heldStream() {
  return new Response(new ReadableStream<Uint8Array>({ start: () => {} }), {
    headers: { "Content-Type": "text/event-stream" },
  });
}

// What the relay is asked directly (localFetch bypasses the environment shim).
const relayAsked: { url: string; auth: string }[] = [];
// Every way the app switches the environment reloads the page; none may run.
const switched = vi.fn();

vi.mock("./env/remoteEnv", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./env/remoteEnv")>();
  return {
    ...actual,
    connectLocal: () => switched("local"),
    connectRemote: (...args: unknown[]) => switched("remote", ...args),
    connectSwarmNode: (...args: unknown[]) => switched("node", ...args),
    connectSwarmRelay: (...args: unknown[]) => switched("relay", ...args),
    localFetch: async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (!url.startsWith(RELAY)) {
        // The page's own server.
        return nodeFetch(input);
      }
      relayAsked.push({
        url,
        auth:
          new Headers(init?.headers ?? undefined).get("Authorization") ?? "",
      });
      const path = url.slice(RELAY.length);
      if (path === "/swarm/info") {
        return json({
          swarm: true,
          name: "office",
          uuid: "r-1",
          version: "1.2.3",
          node_count: 1,
          started_at: "2026-09-27T00:00:00Z",
          registry_warming: false,
        });
      }
      if (path === "/swarm/nodes") {
        return json({ nodes: [] });
      }
      if (path.startsWith("/swarm/sessions")) {
        return json({ sessions: [], warnings: [] });
      }
      if (path === "/swarm/topology") {
        return json({
          root: { uuid: "r-1", name: "office", kind: "relay", online: true },
          nodes: [
            { uuid: "a", name: "worker-a", kind: "agent", online: true },
            { uuid: "b", name: "worker-b", kind: "agent", online: true },
          ],
          edges: [
            { from_uuid: "r-1", to_uuid: "a", name: "worker-a" },
            { from_uuid: "r-1", to_uuid: "b", name: "worker-b" },
          ],
          routes: { a: { path: ["worker-a"] }, b: { path: ["worker-b"] } },
          warnings: [],
        });
      }
      return json({}, 404);
    },
  };
});

// The remotes the page's own server lists (httpserver.remotes).
let pageRemotes: Array<Record<string, string>> = [];
// Set by a test that answers the scheduler routes itself.
let schedulerAnswer: (() => Promise<Response>) | null = null;

// The node, reached the way the environment shim would reach it.
const nodeFetch = vi.fn(
  async (input: RequestInfo | URL, _init?: RequestInit) => {
    const requested = String(input);
    const path = requested.startsWith(NODE_MOUNT)
      ? requested.slice(NODE_MOUNT.length)
      : requested;
    if (path === "/coddy/events") return heldStream();
    if (path === "/coddy/config") {
      return json({ httpserver: { remotes: pageRemotes } });
    }
    if (path === "/coddy/scheduler/jobs") {
      if (schedulerAnswer) {
        return schedulerAnswer();
      }
      return json({
        scheduler: {
          enabled: true,
          dir: "/tmp",
          timeout: "30m",
          max_queue: 1,
          runs_active: 0,
          retain_sessions: 1,
        },
        jobs: [],
      });
    }
    if (path.startsWith("/coddy/sessions?")) return json({ sessions: [] });
    // The page's own server (localFetch falls back to this fetch in a test).
    if (path === "/coddy/info") {
      return json({
        object: "coddy.info",
        version: "1.2.3",
        hostname: "pasha-lt",
      });
    }
    // A node is not a relay.
    return json({}, 404);
  },
);

beforeEach(() => {
  resetSettingsConfigForTests();
  resetConfiguredRemotesForTests();
  initLocale("en");
  localStorage.clear();
  setEnv({
    mode: "remote",
    baseUrl: NODE_MOUNT,
    token: "client",
    name: "worker-a",
    swarmRelay: RELAY,
    swarmNode: "worker-a",
  });
  relayAsked.length = 0;
  pageRemotes = [];
  schedulerAnswer = null;
  nodeFetch.mockClear();
  switched.mockClear();
  vi.stubGlobal("fetch", nodeFetch);
  installRemoteFetchShim();
  history.replaceState(null, "", "/");
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  history.replaceState(null, "", "/");
  localStorage.clear();
  document.cookie = "coddy_sessions_origin=; Path=/; Max-Age=0; SameSite=Lax";
  delete (window as Window & { __coddyFetchShimmed?: boolean })
    .__coddyFetchShimmed;
});

function sessionRequests(): Array<{ url: URL; authorization: string }> {
  return nodeFetch.mock.calls
    .map(([input, init]) => ({
      url: new URL(String(input), "http://node.test"),
      authorization:
        new Headers(init?.headers ?? undefined).get("Authorization") ?? "",
    }))
    .filter(({ url }) => url.pathname.endsWith("/coddy/sessions"));
}

async function openHistoryEnvironmentFilter(): Promise<void> {
  fireEvent.click(await screen.findByTestId("nav-history"));
  fireEvent.click(await screen.findByTestId("sessions-filter-trigger"));
  fireEvent.click(screen.getByTestId("sessions-filter-section-environment"));
}

test("History origin filters the active swarm node without switching environments", async () => {
  document.cookie = "coddy_sessions_origin=local; Path=/; SameSite=Lax";
  pageRemotes = [{ name: "input-relay", url: RELAY }];
  const activeRemote = localStorage.getItem("coddy_env");

  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );

  await waitFor(() =>
    expect(
      sessionRequests().some(
        ({ url, authorization }) =>
          url.origin === RELAY &&
          url.pathname === "/swarm/nodes/worker-a/coddy/sessions" &&
          url.searchParams.get("origin") === "local" &&
          authorization === "Bearer client",
      ),
    ).toBe(true),
  );
  await openHistoryEnvironmentFilter();
  await waitFor(() =>
    expect(
      screen.getByTestId("sessions-filter-section-environment"),
    ).toHaveTextContent("input-relay · Local"),
  );
  expect(screen.getByTestId("sessions-filter-env-local")).toHaveAttribute(
    "aria-checked",
    "true",
  );
  expect(screen.getByTestId(`sessions-filter-env-${RELAY}`)).toHaveAttribute(
    "aria-current",
    "true",
  );

  nodeFetch.mockClear();
  switched.mockClear();
  fireEvent.click(screen.getByTestId("sessions-filter-env-gateway"));

  expect(switched).not.toHaveBeenCalled();
  await waitFor(() =>
    expect(
      sessionRequests().some(
        ({ url, authorization }) =>
          url.origin === RELAY &&
          url.pathname === "/swarm/nodes/worker-a/coddy/sessions" &&
          url.searchParams.get("origin") === "gateway" &&
          authorization === "Bearer client",
      ),
    ).toBe(true),
  );
  expect(document.cookie).toContain("coddy_sessions_origin=gateway");
  expect(localStorage.getItem("coddy_env")).toBe(activeRemote);

  nodeFetch.mockClear();
  switched.mockClear();
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  expect(
    screen.getByTestId("sessions-filter-section-environment"),
  ).toHaveTextContent("input-relay · Gateway");
  fireEvent.click(screen.getByTestId("sessions-filter-section-environment"));
  expect(screen.getByTestId(`sessions-filter-env-${RELAY}`)).toHaveAttribute(
    "aria-current",
    "true",
  );
  fireEvent.click(screen.getByTestId("sessions-filter-env-all"));

  expect(switched).not.toHaveBeenCalled();
  await waitFor(() =>
    expect(
      sessionRequests().some(
        ({ url, authorization }) =>
          url.origin === RELAY &&
          url.pathname === "/swarm/nodes/worker-a/coddy/sessions" &&
          !url.searchParams.has("origin") &&
          authorization === "Bearer client",
      ),
    ).toBe(true),
  );
  expect(document.cookie).not.toContain("coddy_sessions_origin=gateway");
  expect(switched).not.toHaveBeenCalled();
  expect(localStorage.getItem("coddy_env")).toBe(activeRemote);
});

test("History environment selects the exact mounted remote over its parent", async () => {
  document.cookie = "coddy_sessions_origin=gateway; Path=/; SameSite=Lax";
  pageRemotes = [
    { name: "office", url: RELAY },
    { name: "worker-a", url: NODE_MOUNT },
  ];

  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await openHistoryEnvironmentFilter();
  await waitFor(() =>
    expect(
      screen.getByTestId("sessions-filter-section-environment"),
    ).toHaveTextContent("worker-a · Gateway"),
  );

  const exact = screen.getByTestId(`sessions-filter-env-${NODE_MOUNT}`);
  const parent = screen.getByTestId(`sessions-filter-env-${RELAY}`);
  expect(exact).toHaveAttribute("aria-current", "true");
  expect(parent).not.toHaveAttribute("aria-current");
  expect(
    exact
      .closest(".sessions-filter-submenu")
      ?.querySelectorAll('[aria-current="true"]'),
  ).toHaveLength(1);
});

test("History environment selects the deepest nested remote regardless of order", async () => {
  setEnv({
    mode: "remote",
    baseUrl: NODE_MOUNT + "/swarm/nodes/nested",
    token: "client",
    name: "nested",
    swarmRelay: RELAY,
    swarmNode: "worker-a/nested",
  });
  pageRemotes = [
    { name: "worker-a", url: NODE_MOUNT },
    { name: "office", url: RELAY },
  ];

  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await openHistoryEnvironmentFilter();
  await waitFor(() =>
    expect(
      screen.getByTestId("sessions-filter-section-environment"),
    ).toHaveTextContent("worker-a"),
  );

  const deepest = screen.getByTestId(`sessions-filter-env-${NODE_MOUNT}`);
  const parent = screen.getByTestId(`sessions-filter-env-${RELAY}`);
  expect(deepest).toHaveAttribute("aria-current", "true");
  expect(parent).not.toHaveAttribute("aria-current");
  expect(
    deepest
      .closest(".sessions-filter-submenu")
      ?.querySelectorAll('[aria-current="true"]'),
  ).toHaveLength(1);
});

test("the swarm map opens over a node without leaving it", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  fireEvent.click(await screen.findByTestId("nav-swarm"));
  await screen.findByTestId("swarm-view");
  await waitFor(() =>
    expect(relayAsked.some((r) => r.url === RELAY + "/swarm/topology")).toBe(
      true,
    ),
  );
  // Straight to the relay, with the client token the mount takes.
  expect(
    relayAsked.every(
      (r) => r.auth === "Bearer client" || r.url.endsWith("/swarm/info"),
    ),
  ).toBe(true);
  // Nothing was switched: no reload, and the node's screens are still there.
  expect(switched).not.toHaveBeenCalled();
  expect(screen.getByTestId("nav-history")).toBeTruthy();
  expect(screen.getByTestId("nav-swarm")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  expect(JSON.parse(localStorage.getItem("coddy_env") || "{}").swarmNode).toBe(
    "worker-a",
  );
});

// History lies under the map in the stack of the rail's screens, so opening it
// from the rail with the map open over the node closes the map: a drawer opened
// under it would be out of sight.
test("History opened from the rail over the map takes its place", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  fireEvent.click(await screen.findByTestId("nav-swarm"));
  await screen.findByTestId("swarm-view");
  fireEvent.click(screen.getByTestId("nav-history"));
  await waitFor(() => expect(screen.queryByTestId("swarm-view")).toBeNull());
  expect(screen.getByTestId("nav-history")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  expect(switched).not.toHaveBeenCalled();
});

test("Scheduler opened from the rail over the map takes its place", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  fireEvent.click(await screen.findByTestId("nav-swarm"));
  await screen.findByTestId("swarm-view");
  fireEvent.click(await screen.findByTestId("nav-scheduler"));
  await waitFor(() => expect(screen.queryByTestId("swarm-view")).toBeNull());
  expect(switched).not.toHaveBeenCalled();
});

// A click on the node the app is on would go where the app already is: it does
// nothing, and the map stays.
test("the node the app is on does nothing", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  fireEvent.click(await screen.findByTestId("nav-swarm"));
  await screen.findByTestId("swarm-view");
  fireEvent.click(await mapNode("worker-a"));
  expect(screen.getByTestId("swarm-view")).toBeTruthy();
  expect(switched).not.toHaveBeenCalled();
});

// Another node is switched to with the map still open over it: what to do on
// the node is the person's next click.
test("another node is switched to with the map kept open", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  fireEvent.click(await screen.findByTestId("nav-swarm"));
  await screen.findByTestId("swarm-view");
  fireEvent.click(await mapNode("worker-b"));
  expect(switched).toHaveBeenCalledWith(
    "node",
    RELAY,
    ["worker-b"],
    "client",
    "#/swarm",
  );
});

async function mapNode(name: string): Promise<Element> {
  return waitFor(() => {
    const found = [...document.querySelectorAll(".swarm-node")].find((n) =>
      (n.textContent || "").includes(name),
    );
    expect(found).toBeTruthy();
    return found as Element;
  });
}

// A click on the relay itself does switch: the app leaves the node for the
// relay, which is what the click asks for. The chip then names the relay the
// way the environment menu does: by its entry in httpserver.remotes, else by
// the name the relay goes by on the map - never by its address.
test("the relay on the map connects to the relay", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  fireEvent.click(await screen.findByTestId("nav-swarm"));
  await screen.findByTestId("swarm-view");
  fireEvent.click(await mapRelay());
  expect(switched).toHaveBeenCalledWith("relay", RELAY, [], "client", "office");
});

test("the relay the map connects to keeps the name the configuration gives it", async () => {
  pageRemotes = [{ name: "hq", url: RELAY }];
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  fireEvent.click(await screen.findByTestId("nav-swarm"));
  await screen.findByTestId("swarm-view");
  await waitFor(() =>
    expect(
      nodeFetch.mock.calls.some(([u]) => String(u) === "/coddy/config"),
    ).toBe(true),
  );
  await waitFor(() => {
    fireEvent.click(document.querySelector(".swarm-node-relay") as Element);
    expect(switched).toHaveBeenLastCalledWith(
      "relay",
      RELAY,
      [],
      "client",
      "hq",
    );
  });
});

async function mapRelay(): Promise<Element> {
  return waitFor(() => {
    const found = document.querySelector(".swarm-node-relay");
    expect(found).toBeTruthy();
    return found as Element;
  });
}

// The machine the page runs on is drawn above the relay, where the connection
// starts, and a click on it opens it (issue #401).
test("the local machine is on the map above the relay and opens local", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  fireEvent.click(await screen.findByTestId("nav-swarm"));
  await screen.findByTestId("swarm-view");
  const local = await waitFor(() => {
    const found = document.querySelector(".swarm-node-client");
    expect(found).toBeTruthy();
    return found as Element;
  });
  expect(local.getAttribute("role")).toBe("button");
  // Named by the machine's host name, not by the address the page came from.
  await waitFor(() => expect(local.textContent).toContain("pasha-lt"));
  expect(local.textContent).not.toContain(window.location.host);
  fireEvent.click(local);
  expect(switched).toHaveBeenCalledWith("local");
});

// A node's scheduler that answers "not here" (404) after the reader has moved on
// - to a session, another environment - must not rewrite the address the
// reader is at now: the answer is about a screen that is no longer on it.
test("a late 404 from the scheduler leaves the address the reader moved to", async () => {
  // Another server of the page has a scheduler, so this node starts from "yes".
  rememberSchedulerLinked("remote:http://elsewhere", true);
  const pending: Array<() => void> = [];
  schedulerAnswer = () =>
    new Promise<Response>((resolve) => {
      pending.push(() => resolve(json({}, 404)));
    });
  history.replaceState(null, "", "/#/scheduler");
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(pending.length).toBeGreaterThan(1));
  window.location.hash = "#/s/sess_elsewhere";
  await new Promise((r) => setTimeout(r, 20));
  for (const answer of pending) answer();
  await new Promise((r) => setTimeout(r, 50));
  expect(window.location.hash).toBe("#/s/sess_elsewhere");
});
