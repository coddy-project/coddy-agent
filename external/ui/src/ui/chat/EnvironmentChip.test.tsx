import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { EnvironmentChip } from "./EnvironmentChip";

// On a relay there is no composer, so the chip sits in the swarm header at the
// top of the screen. A menu that always grew upward left it off-screen there.
describe("EnvironmentChip menu direction", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          ({ ok: true, status: 200, json: async () => ({}) }) as Response,
      ),
    );
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  const rect = (top: number): DOMRect =>
    ({
      left: 40,
      top,
      bottom: top + 28,
      right: 160,
      width: 120,
      height: 28,
      x: 40,
      y: top,
      toJSON: () => ({}),
    }) as DOMRect;

  it("opens downward when the chip is near the top of the window", async () => {
    render(<EnvironmentChip />);
    const btn = screen.getByTestId("composer-env-btn");
    btn.getBoundingClientRect = () => rect(48);
    fireEvent.click(btn);
    const menu = await screen.findByTestId("composer-env-menu");
    expect(menu).toHaveClass("opens-down");
    expect(menu.style.top).toBe("84px");
    expect(menu.style.bottom).toBe("");
  });

  // The menu opens from a click, so the focus stays on the chip: Escape has
  // to reach the menu from there too.
  it("closes on Escape with the focus still on the chip", async () => {
    render(<EnvironmentChip />);
    const btn = screen.getByTestId("composer-env-btn");
    btn.getBoundingClientRect = () => rect(48);
    fireEvent.click(btn);
    await screen.findByTestId("composer-env-menu");
    expect(fireEvent.keyDown(btn, { key: "Escape" })).toBe(false);
    expect(screen.queryByTestId("composer-env-menu")).toBeNull();
  });

  // In the swarm header of a relay the chip is the last thing on the right, so
  // a menu hung from its left edge ran past the window at 1280 px.
  it("stays inside the window when the chip is near the right edge", async () => {
    render(<EnvironmentChip />);
    const btn = screen.getByTestId("composer-env-btn");
    const right = window.innerWidth - 80;
    btn.getBoundingClientRect = () =>
      ({ ...rect(48), left: right - 76, right, x: right - 76, width: 76 }) as DOMRect;
    fireEvent.click(btn);
    const menu = await screen.findByTestId("composer-env-menu");
    const width = Math.min(300, window.innerWidth - 24);
    const left = parseFloat(menu.style.left);
    expect(left + width).toBeLessThanOrEqual(window.innerWidth - 12);
    expect(left).toBeGreaterThanOrEqual(12);
  });

  it("keeps opening upward from the composer at the foot", async () => {
    render(<EnvironmentChip />);
    const btn = screen.getByTestId("composer-env-btn");
    btn.getBoundingClientRect = () => rect(window.innerHeight - 90);
    fireEvent.click(btn);
    const menu = await screen.findByTestId("composer-env-menu");
    expect(menu).toHaveClass("opens-up");
    expect(menu.style.bottom).not.toBe("");
    expect(menu.style.top).toBe("");
  });
});

// ---------------------------------------------------------------------------
// Issue #401: a relay listed as a remote. Every question the menu asks goes
// through localFetch, which bypasses the environment shim, so the module is
// mocked and a small fake network answers for the local server, a relay and
// a plain agent.

type Handler = (url: string, init?: RequestInit) => Response | Error;
let network: Handler = () => new Error("no network in this test");

vi.mock("../env/remoteEnv", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../env/remoteEnv")>();
  return {
    ...actual,
    localFetch: (input: RequestInfo | URL, init?: RequestInit) => {
      const out = network(String(input), init);
      return out instanceof Error ? Promise.reject(out) : Promise.resolve(out);
    },
  };
});

import { getEnv, setEnv } from "../env/remoteEnv";

const RELAY = "http://relay.lan:12346";

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function bearer(init?: RequestInit): string {
  return new Headers(init?.headers ?? undefined).get("Authorization") ?? "";
}

/** The local server's config lists these remotes; the relay takes `client`. */
function fleet(opts: {
  remotes: Array<Record<string, string>>;
  relayToken?: string;
  cors?: boolean;
}): Handler {
  return (url, init) => {
    if (url === "/coddy/config") {
      return json(200, { httpserver: { remotes: opts.remotes } });
    }
    if (!url.startsWith(RELAY)) {
      return new TypeError("Failed to fetch");
    }
    if (init?.mode === "no-cors") {
      return new Response(null, { status: 200 });
    }
    if (opts.cors === false) {
      return new TypeError("Failed to fetch");
    }
    const path = url.slice(RELAY.length);
    if (path === "/swarm/info") {
      return json(200, { swarm: true, name: "office" });
    }
    if (path.startsWith("/swarm/")) {
      if (bearer(init) !== "Bearer " + (opts.relayToken ?? "client")) {
        return json(401, { error: { message: "unauthorized" } });
      }
      if (path === "/swarm/topology") {
        return json(200, {
          nodes: [
            { uuid: "r", name: "office", kind: "relay", online: true },
            { uuid: "a", name: "worker-a", kind: "agent", online: true },
            { uuid: "b", name: "worker-b", kind: "agent", online: false },
          ],
          routes: { a: { path: ["worker-a"] }, b: { path: ["worker-b"] } },
        });
      }
      return json(200, { nodes: [] });
    }
    return new Response("404 page not found\n", { status: 404 });
  };
}

function stubLocation() {
  const loc = {
    hash: "",
    origin: "http://localhost:3000",
    href: "http://localhost:3000/",
    reload: vi.fn(),
  };
  Object.defineProperty(window, "location", {
    value: loc,
    writable: true,
    configurable: true,
  });
  return loc;
}

function remoteRow(name: string): HTMLElement {
  return screen.getByRole("menuitem", { name: new RegExp("^" + name) });
}

async function openEnvMenu() {
  fireEvent.click(screen.getByTestId("composer-env-btn"));
  return screen.findByTestId("composer-env-menu");
}

describe("EnvironmentChip with a relay as a remote (issue #401)", () => {
  const realLocation = window.location;
  beforeEach(() => {
    localStorage.clear();
    setEnv({ mode: "local" });
  });
  afterEach(() => {
    cleanup();
    Object.defineProperty(window, "location", {
      value: realLocation,
      writable: true,
      configurable: true,
    });
  });

  // A relay serves no /v1 at all: probing its model catalog left a healthy,
  // authorized relay red for good.
  it("shows a relay that accepts the saved token as reachable", async () => {
    localStorage.setItem("coddy_env_tokens", JSON.stringify({ [RELAY]: "client" }));
    network = fleet({ remotes: [{ name: "office-relay", url: RELAY }] });
    render(<EnvironmentChip />);
    await openEnvMenu();
    await waitFor(() =>
      expect(
        remoteRow("office-relay").querySelector(".env-status")?.getAttribute("data-state"),
      ).toBe("up"),
    );
  });

  it("says a relay without a token needs one and offers to enter it", async () => {
    network = fleet({ remotes: [{ name: "office-relay", url: RELAY }] });
    render(<EnvironmentChip />);
    const menu = await openEnvMenu();
    await within(menu).findByText(/swarm\.auth_token/);
    expect(
      remoteRow("office-relay").querySelector(".env-status")?.getAttribute("data-state"),
    ).toBe("down");
    fireEvent.click(within(menu).getByRole("button", { name: /token/i }));
    expect(screen.getByTestId("composer-env-add-url")).toHaveValue(RELAY);
  });

  // With swarm.cors off the browser gets no answer at all, which used to look
  // exactly like a machine that is off.
  it("names the CORS setting and this page's origin when the browser is kept from the answer", async () => {
    network = fleet({ remotes: [{ name: "office-relay", url: RELAY }], cors: false });
    render(<EnvironmentChip />);
    const menu = await openEnvMenu();
    const hint = await within(menu).findByText(/swarm\.cors/);
    expect(hint.textContent).toContain(window.location.origin);
    expect(hint.textContent).toContain("httpserver.cors");
    // jsdom serves the page from http://localhost:3000 - the laptop case - so
    // the line also names the toggle that admits this page on any port.
    expect(window.location.origin).toMatch(/^http:\/\/localhost/);
    expect(hint.textContent).toContain("allow_loopback");
  });

  it("uses the token the configuration carries for the remote", async () => {
    const loc = stubLocation();
    network = fleet({
      remotes: [{ name: "office-relay", url: RELAY, token: "from-config" }],
      relayToken: "from-config",
    });
    render(<EnvironmentChip />);
    await openEnvMenu();
    await waitFor(() =>
      expect(
        remoteRow("office-relay").querySelector(".env-status")?.getAttribute("data-state"),
      ).toBe("up"),
    );
    fireEvent.click(remoteRow("office-relay"));
    const env = getEnv();
    expect(env.mode === "remote" && env.token).toBe("from-config");
    expect(loc.reload).toHaveBeenCalled();
  });

  // The nice-to-have of the issue: a node under the relay is one click away.
  it("lists the agents of an authorized relay and enters one in one click", async () => {
    stubLocation();
    localStorage.setItem("coddy_env_tokens", JSON.stringify({ [RELAY]: "client" }));
    network = fleet({ remotes: [{ name: "office-relay", url: RELAY }] });
    render(<EnvironmentChip />);
    const menu = await openEnvMenu();
    const node = await within(menu).findByRole("menuitem", { name: /worker-a/ });
    expect(within(menu).getByRole("menuitem", { name: /worker-b/ })).toBeTruthy();
    fireEvent.click(node);
    const env = getEnv();
    if (env.mode !== "remote") throw new Error("expected a remote environment");
    expect(env.baseUrl).toBe(RELAY + "/swarm/nodes/worker-a");
    expect(env.swarmRelay).toBe(RELAY);
    expect(env.token).toBe("client");
  });

  // The name in httpserver.remotes is optional. A remote left unnamed goes by
  // what it calls itself - a relay by its name, an agent by its host name - and
  // its address stays on the right, said once.
  it("names an unnamed remote by what it reports, and says its address once", async () => {
    const loc = stubLocation();
    const AGENT = "http://gpu-box.lan:12345";
    const DEAD = "http://10.0.0.9:12345";
    localStorage.setItem(
      "coddy_env_tokens",
      JSON.stringify({ [RELAY]: "client", [AGENT]: "agent" }),
    );
    const relayNet = fleet({ remotes: [{ url: RELAY }, { url: AGENT }, { url: DEAD }] });
    network = (url, init) => {
      if (url.startsWith(AGENT)) {
        if (bearer(init) !== "Bearer agent") return json(401, {});
        if (url === AGENT + "/v1/models") return json(200, { data: [] });
        if (url === AGENT + "/coddy/info") {
          return json(200, { object: "coddy.info", version: "1.2.36", hostname: "gpu-box" });
        }
        return json(404, {});
      }
      return relayNet(url, init);
    };
    render(<EnvironmentChip />);
    const menu = await openEnvMenu();
    await within(menu).findByRole("menuitem", { name: /^office/ });
    const agent = await within(menu).findByRole("menuitem", { name: /^gpu-box/ });
    expect(agent.querySelector(".mode-env-sub")?.textContent).toBe("gpu-box.lan:12345");
    // Nothing names the one that does not answer: its address is its name.
    const dead = within(menu).getByRole("menuitem", { name: /^10\.0\.0\.9:12345/ });
    expect(dead.querySelector(".mode-env-sub")?.textContent ?? "").toBe("");
    // The chip carries the same name once the remote is chosen.
    fireEvent.click(agent);
    const env = getEnv();
    expect(env.mode === "remote" && env.name).toBe("gpu-box");
    expect(loc.reload).toHaveBeenCalled();
  });

  // A remote committed to the config while the page is open was invisible until
  // the composer happened to mount again.
  it("reads the configured remotes again when the menu opens", async () => {
    network = fleet({ remotes: [] });
    render(<EnvironmentChip />);
    const first = await openEnvMenu();
    await waitFor(() =>
      expect(within(first).queryByRole("menuitem", { name: /office-relay/ })).toBeNull(),
    );
    fireEvent.click(screen.getByTestId("composer-env-btn"));
    network = fleet({ remotes: [{ name: "office-relay", url: RELAY }] });
    const second = await openEnvMenu();
    expect(
      await within(second).findByRole("menuitem", { name: /office-relay/ }),
    ).toBeTruthy();
  });
});

// With a relay's agents and the hints under remotes that cannot be reached the
// menu outgrew the window from the start screen and hid "Connect to...".
describe("EnvironmentChip menu height", () => {
  afterEach(() => cleanup());

  it("takes the room between the chip and the window's edge, and scrolls past it", async () => {
    network = () => new TypeError("offline");
    render(<EnvironmentChip />);
    const btn = screen.getByTestId("composer-env-btn");
    btn.getBoundingClientRect = () =>
      ({ left: 40, top: 300, bottom: 328, right: 160, width: 120, height: 28, x: 40, y: 300, toJSON: () => ({}) }) as DOMRect;
    fireEvent.click(btn);
    const menu = await screen.findByTestId("composer-env-menu");
    expect(menu).toHaveClass("opens-down");
    expect(parseFloat(menu.style.maxHeight)).toBe(window.innerHeight - 328 - 8 - 12);
  });
});
