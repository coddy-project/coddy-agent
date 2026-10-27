import { afterEach, describe, expect, it, vi } from "vitest";

import {
  PANEL_SOURCE,
  panelTabForTurn,
  schemeOf,
  startPanelNavigation,
  startPanelTheme,
  WEBUI_SOURCE,
} from "./bridge";
import {
  BROWSER_PANEL_STORAGE_KEY,
  browserPanelLaunch,
  captureBrowserPanelLaunch,
  resetBrowserPanelLaunchForTests,
  takeBrowserPanelTicket,
} from "./launch";

const ORIGIN = "chrome-extension://abcdefghijklmnopabcdefghijklmnop";
const TICKET = "a".repeat(64);

type Posted = { message: Record<string, unknown>; origin: string };

/**
 * fakeFrame stands in for the web UI loaded in the panel's frame: its own
 * address, storage and history, and a parent window that records what the
 * page posts to it.
 */
function fakeFrame(search: string, inFrame = true) {
  const posted: Posted[] = [];
  const events = new EventTarget();
  const store = new Map<string, string>();
  const location = { search, pathname: "/", hash: "#/s/sess_1" };
  const parent = {
    postMessage: (message: Record<string, unknown>, origin: string) =>
      posted.push({ message, origin }),
  };
  const win = {
    location,
    parent,
    document,
    sessionStorage: {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
    },
    history: {
      state: null,
      replaceState: (_s: unknown, _t: string, url: string) => {
        const u = new URL(url, "http://coddy.test");
        location.search = u.search;
        location.pathname = u.pathname;
        location.hash = u.hash;
      },
    },
    addEventListener: events.addEventListener.bind(events),
    removeEventListener: events.removeEventListener.bind(events),
    setTimeout: (fn: () => void, ms: number) => window.setTimeout(fn, ms),
    clearTimeout: (id: number) => window.clearTimeout(id),
    getComputedStyle: window.getComputedStyle.bind(window),
    MutationObserver: window.MutationObserver,
  };
  if (!inFrame) win.parent = win as unknown as typeof parent;
  /** answer delivers a message to the page as the panel would send it. */
  const answer = (data: unknown, origin = ORIGIN, source: unknown = parent) =>
    events.dispatchEvent(
      Object.assign(new Event("message"), { data, origin, source }),
    );
  return { win: win as unknown as Window, posted, store, location, answer };
}

afterEach(() => {
  resetBrowserPanelLaunchForTests();
  delete document.documentElement.dataset.browserPanel;
});

describe("the panel's launch", () => {
  it("keeps the ticket and the panel's origin, and takes them out of the address", () => {
    const f = fakeFrame(
      `?coddy_panel=${TICKET}&coddy_panel_origin=${encodeURIComponent(ORIGIN)}&lang=en`,
    );
    const launch = captureBrowserPanelLaunch(f.win);
    expect(launch).toEqual({ origin: ORIGIN, ticket: TICKET });
    expect(f.location.search).toBe("?lang=en");
    expect(f.location.hash).toBe("#/s/sess_1");
    expect(document.documentElement.dataset.browserPanel).toBe("true");
    expect(JSON.parse(f.store.get(BROWSER_PANEL_STORAGE_KEY)!)).toEqual(launch);
  });

  it("hands the ticket out once", () => {
    const f = fakeFrame(
      `?coddy_panel=${TICKET}&coddy_panel_origin=${encodeURIComponent(ORIGIN)}`,
    );
    captureBrowserPanelLaunch(f.win);
    expect(takeBrowserPanelTicket(f.win)).toBe(TICKET);
    expect(takeBrowserPanelTicket(f.win)).toBe("");
    expect(browserPanelLaunch()?.origin).toBe(ORIGIN);
  });

  it("is no panel in a top window, and still clears the address", () => {
    const f = fakeFrame(
      `?coddy_panel=${TICKET}&coddy_panel_origin=${encodeURIComponent(ORIGIN)}`,
      false,
    );
    expect(captureBrowserPanelLaunch(f.win)).toBeNull();
    expect(f.location.search).toBe("");
    expect(document.documentElement.dataset.browserPanel).toBeUndefined();
  });

  it("takes a Firefox extension's origin", () => {
    const firefox = "moz-extension://0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d";
    const f = fakeFrame(
      `?coddy_panel=${TICKET}&coddy_panel_origin=${encodeURIComponent(firefox)}`,
    );
    expect(captureBrowserPanelLaunch(f.win)?.origin).toBe(firefox);
  });

  it("refuses an origin that is not an extension's", () => {
    const f = fakeFrame(
      `?coddy_panel=${TICKET}&coddy_panel_origin=${encodeURIComponent("https://evil.example")}`,
    );
    expect(captureBrowserPanelLaunch(f.win)).toBeNull();
  });

  it("stays the panel's after a reload of the frame", () => {
    const f = fakeFrame(
      `?coddy_panel=${TICKET}&coddy_panel_origin=${encodeURIComponent(ORIGIN)}`,
    );
    captureBrowserPanelLaunch(f.win);
    takeBrowserPanelTicket(f.win);
    resetBrowserPanelLaunchForTests();
    expect(captureBrowserPanelLaunch(f.win)).toEqual({
      origin: ORIGIN,
      ticket: "",
    });
  });
});

describe("the tab of a turn", () => {
  function inPanel() {
    const f = fakeFrame(
      `?coddy_panel=${TICKET}&coddy_panel_origin=${encodeURIComponent(ORIGIN)}`,
    );
    captureBrowserPanelLaunch(f.win);
    return f;
  }

  it("asks the panel at its origin and takes its answer", async () => {
    const f = inPanel();
    const pending = panelTabForTurn("sess_1", "Read this page", f.win, 1000);
    expect(f.posted).toHaveLength(1);
    const asked = f.posted[0]!;
    expect(asked.origin).toBe(ORIGIN);
    expect(asked.message).toMatchObject({
      source: WEBUI_SOURCE,
      type: "turn",
      sessionId: "sess_1",
      title: "Read this page",
    });
    const tab = {
      browser_id: "chrome-1",
      tab_id: 7,
      url: "https://example.com/",
      title: "Example",
    };
    f.answer({
      source: PANEL_SOURCE,
      type: "turn",
      requestId: asked.message.requestId,
      tab,
    });
    await expect(pending).resolves.toEqual(tab);
  });

  it("ignores an answer from another origin or window, and gives up in time", async () => {
    vi.useFakeTimers();
    try {
      const f = inPanel();
      const pending = panelTabForTurn("sess_1", "x", f.win, 3000);
      const requestId = f.posted[0]!.message.requestId;
      const tab = { browser_id: "b", tab_id: 3, url: "", title: "" };
      f.answer(
        { source: PANEL_SOURCE, type: "turn", requestId, tab },
        "https://evil.example",
      );
      f.answer(
        { source: PANEL_SOURCE, type: "turn", requestId, tab },
        ORIGIN,
        {},
      );
      vi.advanceTimersByTime(3000);
      await expect(pending).resolves.toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it("asks nothing outside the panel", async () => {
    const f = fakeFrame("");
    await expect(panelTabForTurn("sess_1", "x", f.win)).resolves.toBeNull();
    expect(f.posted).toHaveLength(0);
  });
});

describe("the panel's theme", () => {
  it("tells dark from light", () => {
    expect(schemeOf("rgb(15, 15, 16)")).toBe("dark");
    expect(schemeOf("rgba(250, 250, 252, 0.9)")).toBe("light");
    expect(schemeOf("color(srgb 0.171765 0.171765 0.175216)")).toBe("dark");
    expect(schemeOf("color(srgb 0.97 0.97 0.98 / 0.8)")).toBe("light");
  });

  it("reports the page's colours to the panel's origin", () => {
    const f = fakeFrame(
      `?coddy_panel=${TICKET}&coddy_panel_origin=${encodeURIComponent(ORIGIN)}`,
    );
    captureBrowserPanelLaunch(f.win);
    const stop = startPanelTheme(f.win);
    stop();
    expect(f.posted).toHaveLength(1);
    expect(f.posted[0]!.origin).toBe(ORIGIN);
    expect(f.posted[0]!.message).toMatchObject({
      source: WEBUI_SOURCE,
      type: "theme",
    });
    const theme = f.posted[0]!.message.theme as Record<string, string>;
    expect(Object.keys(theme).sort()).toEqual(
      ["accent", "bg", "border", "muted", "scheme", "surface", "text"].sort(),
    );
  });

  it("reports nothing outside the panel", () => {
    const f = fakeFrame("");
    startPanelTheme(f.win)();
    expect(f.posted).toHaveLength(0);
  });
});

describe("a chat the panel opens", () => {
  function inPanel() {
    const f = fakeFrame(
      `?coddy_panel=${TICKET}&coddy_panel_origin=${encodeURIComponent(ORIGIN)}`,
    );
    captureBrowserPanelLaunch(f.win);
    return f;
  }

  it("shows the chat of this server the panel names", () => {
    const f = inPanel();
    const stop = startPanelNavigation(() => true, f.win);
    f.answer({ source: PANEL_SOURCE, type: "open", sessionId: "sess_ab12" });
    expect(f.location.hash).toBe("#/s/sess_ab12");
    stop();
    f.answer({ source: PANEL_SOURCE, type: "open", sessionId: "sess_cd34" });
    expect(f.location.hash).toBe("#/s/sess_ab12");
  });

  it("takes only a session id of this server, from the panel, in the panel's environment", () => {
    const f = inPanel();
    let local = true;
    startPanelNavigation(() => local, f.win);
    const before = f.location.hash;
    // A chat of another Coddy process, an id that is not one, another source.
    f.answer({
      source: PANEL_SOURCE,
      type: "open",
      sessionId: "laptop/sess_1",
    });
    f.answer({ source: PANEL_SOURCE, type: "open", sessionId: "../settings" });
    f.answer({ source: WEBUI_SOURCE, type: "open", sessionId: "sess_ab12" });
    // Another origin, another window.
    f.answer(
      { source: PANEL_SOURCE, type: "open", sessionId: "sess_ab12" },
      "https://evil.example",
    );
    f.answer(
      { source: PANEL_SOURCE, type: "open", sessionId: "sess_ab12" },
      ORIGIN,
      {},
    );
    expect(f.location.hash).toBe(before);
    // The web UI switched to another environment: its chats are not the panel's.
    local = false;
    f.answer({ source: PANEL_SOURCE, type: "open", sessionId: "sess_ab12" });
    expect(f.location.hash).toBe(before);
  });

  it("listens to nothing outside the panel", () => {
    const f = fakeFrame("", false);
    captureBrowserPanelLaunch(f.win);
    startPanelNavigation(() => true, f.win);
    f.answer({ source: PANEL_SOURCE, type: "open", sessionId: "sess_ab12" });
    expect(f.location.hash).toBe("#/s/sess_1");
  });
});
