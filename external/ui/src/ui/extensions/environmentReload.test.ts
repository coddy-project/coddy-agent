import { expect, test, vi } from "vitest";
import {
  connectRemote,
  requireEnvironmentReload,
  setEnv,
  subscribeEnv,
} from "../env/remoteEnv";

test("evaluated UI code forces reload on node switches and credential rotation", () => {
  const realLocation = window.location;
  const reload = vi.fn();
  Object.defineProperty(window, "location", {
    value: { hash: "", reload },
    writable: true,
    configurable: true,
  });
  let unsubscribe = () => {};
  try {
    setEnv({
      mode: "remote",
      baseUrl: "http://relay/swarm/nodes/a",
      token: "a",
    });
    const changed = vi.fn();
    unsubscribe = subscribeEnv(changed);
    requireEnvironmentReload();
    connectRemote("http://relay/swarm/nodes/b", "b");
    expect(reload).toHaveBeenCalledOnce();
    expect(changed).not.toHaveBeenCalled();
    setEnv({
      mode: "remote",
      baseUrl: "http://relay/swarm/nodes/b",
      token: "rotated",
    });
    expect(reload).toHaveBeenCalledTimes(2);
    expect(changed).not.toHaveBeenCalled();
  } finally {
    unsubscribe();
    Object.defineProperty(window, "location", {
      value: realLocation,
      writable: true,
      configurable: true,
    });
  }
});
