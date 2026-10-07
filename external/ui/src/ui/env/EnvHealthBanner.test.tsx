import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import type { RemoteProbe } from "./remoteProbe";
import { setEnv, type CoddyEnv } from "./remoteEnv";

// The banner reads the active environment's probe; what the network answers is
// the probe's business (remoteProbe.test.ts), so the probe is given here.
let probe: RemoteProbe | null = null;
vi.mock("./activeHealth", () => ({
  useActiveEnvProbe: () => ({ health: probe ? "down" : "up", probe }),
}));

import { EnvHealthBanner } from "./EnvHealthBanner";

function show(env: CoddyEnv, answer: RemoteProbe) {
  setEnv(env);
  probe = answer;
  render(<EnvHealthBanner />);
  return screen.getByTestId("env-health-banner");
}

const relayEnv: CoddyEnv = {
  mode: "remote",
  baseUrl: "http://relay.lan:12346",
  token: "",
  name: "office-relay",
};
const agentEnv: CoddyEnv = {
  mode: "remote",
  baseUrl: "http://box:12345",
  token: "t",
  name: "box",
};

describe("EnvHealthBanner says why the environment is not usable (issue #401)", () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    cleanup();
    setEnv({ mode: "local" });
    probe = null;
    document.documentElement.style.removeProperty("--coddy-env-banner-h");
  });

  // The banner used to send the operator of a relay to httpserver.cors, a key
  // of the other kind of server.
  it("sends a relay that refuses the token to its client token", () => {
    const banner = show(relayEnv, { reach: "unauthorized", relay: true });
    expect(banner.textContent).toContain("swarm.auth_token");
    expect(banner.textContent).not.toContain("httpserver.cors");
  });

  it("sends an agent that refuses the token to its own token", () => {
    const banner = show(agentEnv, { reach: "unauthorized", relay: false });
    expect(banner.textContent).toContain("httpserver.auth_token");
  });

  // Blocked by CORS, the browser cannot read whether this is a relay, so both
  // settings are named, with the origin to put in them.
  it("names both CORS settings and this page's origin when the answer was blocked", () => {
    const banner = show(agentEnv, { reach: "cors", relay: false });
    expect(banner.textContent).toContain("swarm.cors");
    expect(banner.textContent).toContain("httpserver.cors");
    expect(banner.textContent).toContain(window.location.origin);
  });

  // jsdom serves the test page from http://localhost:3000, which is the laptop
  // case: the page is on a loopback address, so the toggle that admits it on
  // any port is named beside the exact-origin list.
  it("names the allow_loopback toggle when this page is on a loopback address", () => {
    expect(window.location.origin).toMatch(/^http:\/\/localhost/);
    const banner = show(agentEnv, { reach: "cors", relay: false });
    expect(banner.textContent).toContain("httpserver.cors.allow_loopback");
    expect(banner.textContent).toContain("swarm.cors.allow_loopback");
    expect(banner.textContent).toContain("httpserver.cors.allowed_origins");
  });

  it("names only the relay's loopback toggle inside a node reached through a relay", () => {
    const banner = show(
      {
        mode: "remote",
        baseUrl: "http://relay.lan:12346/swarm/nodes/worker-a",
        token: "t",
        name: "worker-a",
        swarmRelay: "http://relay.lan:12346",
        swarmNode: "worker-a",
      },
      { reach: "cors", relay: false },
    );
    expect(banner.textContent).toContain("swarm.cors.allow_loopback");
    expect(banner.textContent).not.toContain("httpserver.cors");
  });

  // A node reached through a relay is answered by the relay's CORS.
  it("names only the relay's CORS setting inside a node reached through a relay", () => {
    const banner = show(
      {
        mode: "remote",
        baseUrl: "http://relay.lan:12346/swarm/nodes/worker-a",
        token: "t",
        name: "worker-a",
        swarmRelay: "http://relay.lan:12346",
        swarmNode: "worker-a",
      },
      { reach: "cors", relay: false },
    );
    expect(banner.textContent).toContain("swarm.cors");
    expect(banner.textContent).not.toContain("httpserver.cors");
  });

  it("says a remote that does not answer is not answering", () => {
    const banner = show(agentEnv, { reach: "down", relay: false });
    expect(banner.textContent).toMatch(/does not answer/i);
  });

  // The stacked shell moves the page down by the banner's height, which it
  // publishes as a custom property while it is on screen.
  it("publishes its height while it is shown and withdraws it when it goes", () => {
    show(agentEnv, { reach: "down", relay: false });
    expect(
      document.documentElement.style.getPropertyValue("--coddy-env-banner-h"),
    ).toMatch(/px$/);
    cleanup();
    expect(
      document.documentElement.style.getPropertyValue("--coddy-env-banner-h"),
    ).toBe("");
  });
});
