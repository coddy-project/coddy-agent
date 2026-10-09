import React, { useEffect } from "react";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, expect, test } from "vitest";
import { EnvScope } from "./EnvScope";
import { connectRemote, setEnv } from "./remoteEnv";

// A switch between two remotes happens in place (remoteEnv.switchTo): the app is
// started over on the new server rather than the page being reloaded. A token
// rotated under the same server starts it over too: the events stream and
// everything else that holds the old token have to present the new one.

let mounts = 0;

function Probe() {
  useEffect(() => {
    mounts += 1;
  }, []);
  return <div data-testid="probe" />;
}

beforeEach(() => {
  mounts = 0;
  localStorage.clear();
  setEnv({
    mode: "remote",
    baseUrl: "http://relay:1/swarm/nodes/a",
    token: "t",
  });
});

afterEach(() => cleanup());

test("starts the app over on another server, and on a rotated token", () => {
  render(
    <EnvScope>
      <Probe />
    </EnvScope>,
  );
  expect(mounts).toBe(1);
  // The same token again (a configuration read that changed nothing) is not a
  // reason to start over.
  act(() =>
    setEnv({
      mode: "remote",
      baseUrl: "http://relay:1/swarm/nodes/a",
      token: "t",
    }),
  );
  expect(mounts).toBe(1);
  act(() =>
    setEnv({
      mode: "remote",
      baseUrl: "http://relay:1/swarm/nodes/a",
      token: "rotated",
    }),
  );
  expect(mounts).toBe(2);
  act(() =>
    setEnv({
      mode: "remote",
      baseUrl: "http://relay:1/swarm/nodes/b",
      token: "rotated",
    }),
  );
  expect(mounts).toBe(3);
});

// Choosing the same remote again starts the app over on it (remoteEnv.switchTo
// bumps the generation EnvScope keys it by).
test("starts the app over when the same remote is chosen again", () => {
  const realLocation = window.location;
  Object.defineProperty(window, "location", {
    value: { hash: "", reload: () => {} },
    writable: true,
    configurable: true,
  });
  try {
    render(
      <EnvScope>
        <Probe />
      </EnvScope>,
    );
    expect(mounts).toBe(1);
    act(() => connectRemote("http://relay:1/swarm/nodes/a", "t", "a"));
    expect(mounts).toBe(2);
  } finally {
    Object.defineProperty(window, "location", {
      value: realLocation,
      writable: true,
      configurable: true,
    });
  }
});
