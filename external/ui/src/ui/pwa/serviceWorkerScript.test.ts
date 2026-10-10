/**
 * public/sw.js is a plain script the browser loads as it is, so the test runs
 * that very file against a stand-in for the worker's global scope.
 */
import { readFileSync } from "node:fs";
import path from "node:path";
import { beforeEach, describe, expect, test, vi } from "vitest";

const source = readFileSync(
  path.resolve(import.meta.dirname, "../../../public/sw.js"),
  "utf8",
);

type Listener = (event: any) => void;
type FakeClient = {
  id: string;
  focused: boolean;
  focus: ReturnType<typeof vi.fn>;
  postMessage: ReturnType<typeof vi.fn>;
};

let listeners: Map<string, Listener>;
let windows: FakeClient[];
let showNotification: ReturnType<typeof vi.fn>;
let openWindow: ReturnType<typeof vi.fn>;
let skipWaiting: ReturnType<typeof vi.fn>;
let claim: ReturnType<typeof vi.fn>;

function client(id: string, focused = false): FakeClient {
  return { id, focused, focus: vi.fn(async () => {}), postMessage: vi.fn() };
}

function boot() {
  listeners = new Map();
  showNotification = vi.fn(async () => {});
  openWindow = vi.fn(async () => null);
  skipWaiting = vi.fn();
  claim = vi.fn(async () => {});
  const self = {
    addEventListener: (type: string, fn: Listener) => listeners.set(type, fn),
    skipWaiting,
    clients: {
      matchAll: vi.fn(async () => windows),
      claim,
      openWindow,
    },
    registration: { showNotification },
  };
  new Function("self", source)(self);
}

/** dispatch runs a listener and waits for what it handed to waitUntil. */
async function dispatch(type: string, event: Record<string, unknown>) {
  let pending: Promise<unknown> = Promise.resolve();
  listeners.get(type)!({
    ...event,
    waitUntil: (p: Promise<unknown>) => {
      pending = p;
    },
  });
  await pending;
}

function notify(fields: Record<string, unknown>, sourceId = "tab-1") {
  return dispatch("message", {
    data: {
      type: "coddy-notify",
      title: "Fix the build",
      body: "The agent finished its turn.",
      tag: "coddy:local:sess_a:turn_finished:t1",
      sessionId: "sess_a",
      env: "local",
      url: "https://coddy.example/#/s/sess_a",
      ...fields,
    },
    source: { id: sourceId },
  });
}

beforeEach(() => {
  windows = [client("tab-1"), client("tab-2")];
  boot();
});

describe("the service worker", () => {
  test("takes over at once and caches nothing", async () => {
    listeners.get("install")!({});
    expect(skipWaiting).toHaveBeenCalled();
    await dispatch("activate", {});
    expect(claim).toHaveBeenCalled();
    expect(listeners.has("fetch")).toBe(false);
  });

  test("shows a tab's notice when no window of the app has the focus", async () => {
    await notify({});
    expect(showNotification).toHaveBeenCalledWith("Fix the build", {
      body: "The agent finished its turn.",
      icon: "/icon-192.png",
      tag: "coddy:local:sess_a:turn_finished:t1",
      data: {
        url: "https://coddy.example/#/s/sess_a",
        sessionId: "sess_a",
        env: "local",
        client: "tab-1",
      },
    });
  });

  test("shows the same tag once when several tabs heard the event", async () => {
    await notify({}, "tab-1");
    await notify({}, "tab-2");
    expect(showNotification).toHaveBeenCalledTimes(1);
    await notify({ tag: "coddy:local:sess_a:turn_finished:t2" });
    expect(showNotification).toHaveBeenCalledTimes(2);
  });

  test("shows nothing while another window of the app has the focus", async () => {
    windows = [client("tab-1"), client("tab-2", true)];
    await notify({});
    expect(showNotification).not.toHaveBeenCalled();
  });

  test("ignores a message that is not a notice", async () => {
    await dispatch("message", { data: { type: "other" }, source: null });
    await dispatch("message", { data: null, source: null });
    expect(showNotification).not.toHaveBeenCalled();
  });

  test("a click focuses the tab that asked and tells it which chat to open", async () => {
    const close = vi.fn();
    await dispatch("notificationclick", {
      notification: {
        close,
        data: {
          url: "https://coddy.example/#/s/sess_a",
          sessionId: "sess_a",
          env: "local",
          client: "tab-2",
        },
      },
    });
    expect(close).toHaveBeenCalled();
    const asked = windows[1]!;
    expect(asked.focus).toHaveBeenCalled();
    expect(asked.postMessage).toHaveBeenCalledWith({
      type: "coddy-open",
      sessionId: "sess_a",
      env: "local",
    });
    expect(windows[0]!.focus).not.toHaveBeenCalled();
    expect(openWindow).not.toHaveBeenCalled();
  });

  test("with the asking tab gone a click goes to any tab, with none to a new window", async () => {
    const data = {
      url: "https://coddy.example/#/s/sess_a",
      sessionId: "sess_a",
      env: "local",
      client: "tab-closed",
    };
    await dispatch("notificationclick", {
      notification: { close: vi.fn(), data },
    });
    expect(windows[0]!.postMessage).toHaveBeenCalled();

    windows = [];
    await dispatch("notificationclick", {
      notification: { close: vi.fn(), data },
    });
    expect(openWindow).toHaveBeenCalledWith("https://coddy.example/#/s/sess_a");
  });
});
