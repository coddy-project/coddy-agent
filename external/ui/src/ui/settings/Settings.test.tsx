import {
  afterEach,
  beforeEach,
  describe,
  expect,
  onTestFinished,
  test,
  vi,
} from "vitest";
import { useEffect, useLayoutEffect, useState, type ReactNode } from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { Settings } from "./Settings";
import {
  noteSettingsConfigReloaded,
  resetSettingsConfigForTests,
} from "./settingsConfigStore";
import {
  REVEAL_WAIT_MS,
  SEARCH_TARGET_MS,
  SETTINGS_REVEAL_EVENT,
} from "./settingsReveal";
import { parseAppHash } from "../scheduler/hashRoute";
import { initLocale } from "../i18n/i18n";
import { OpenRailScreen } from "../nav/railEscape.fakes";
import { pressBack } from "../telegram/backButton";
import {
  AGENT_SCHEMA,
  RELAY_SCHEMA,
  configFromSchema,
  mountSettings,
  openSettings,
  resetPage,
  schemaWithout,
  stubServer as stubServerFor,
  type MountOptions,
  type ServerState,
} from "./settingsServer.fakes";

// settingsIndexRowsKey runs once per render of the drawer and the tab under it, buildSettingsSearchIndex once per index made.
const rowsKeyCalls = vi.hoisted(() => ({ count: 0 }));
const indexBuilds = vi.hoisted(() => ({ count: 0 }));
vi.mock("./settingsSearch", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./settingsSearch")>();
  return {
    ...actual,
    buildSettingsSearchIndex: (
      input: Parameters<typeof actual.buildSettingsSearchIndex>[0],
    ) => {
      indexBuilds.count += 1;
      return actual.buildSettingsSearchIndex(input);
    },
    settingsIndexRowsKey: (
      doc: Parameters<typeof actual.settingsIndexRowsKey>[0],
    ) => {
      rowsKeyCalls.count += 1;
      return actual.settingsIndexRowsKey(doc);
    },
  };
});

const schema = {
  type: "object",
  "x-coddy-property-order": ["providers", "models", "agent"],
  properties: {
    providers: {
      type: "array",
      title: "LLM providers",
      items: {
        type: "object",
        properties: { name: { type: "string", title: "Provider id" } },
      },
    },
    models: {
      type: "array",
      title: "Logical models",
      items: {
        type: "object",
        properties: { model: { type: "string", title: "Model id" } },
      },
    },
    agent: {
      type: "object",
      title: "ReAct loop",
      properties: { max_turns: { type: "integer", title: "Max turns" } },
    },
  },
};

let server: ServerState;

/** A coddy serve behind fetch, reading the state this file keeps in `server`. */
function stubServer() {
  return stubServerFor(() => server);
}

function configReads(fetch: ReturnType<typeof stubServer>): string[] {
  return fetch.mock.calls
    .filter(
      ([path, init]) =>
        String(path).startsWith("/coddy/config") &&
        ((init as RequestInit | undefined)?.method ?? "GET") === "GET",
    )
    .map(([path]) => String(path));
}

beforeEach(() => {
  initLocale("en");
  resetSettingsConfigForTests();
  server = {
    schema,
    config: {
      providers: [{ name: "demo" }],
      models: [{ model: "demo/m1" }, { model: "demo/m2" }],
      agent: { max_turns: 40 },
    },
    put: { status: 200, body: { ok: true } },
  };
  window.location.hash = "";
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  resetSettingsConfigForTests();
});

// Issue #359: Settings is mounted only while it is open, and every mount read the
// schema and the config again, starting from nothing - so each open drew the
// drawer empty (Appearance and Sessions only), then rebuilt it. The app keeps the
// copy it read, and a second open draws the tab from it in its first frame.
test("reopening Settings draws the tab from the copy it keeps, without waiting for a request", async () => {
  const fetch = stubServer();
  const first = render(
    <Settings onClose={() => {}} initialSection="providers" />,
  );
  await screen.findByTestId("settings-master-item-0");
  first.unmount();
  fetch.mockClear();

  const { container } = render(
    <Settings onClose={() => {}} initialSection="providers" />,
  );

  // The first frame, with nothing awaited: no Appearance in between.
  expect(screen.getByTestId("settings-master-item-0").textContent).toBe("demo");
  expect(container.querySelector(".appearance-swatch-grid")).toBeNull();
  expect(
    screen.getByTestId("settings-tab-providers").getAttribute("aria-current"),
  ).toBe("page");
  expect(configReads(fetch)).toEqual([]);
});

// The very first open has nothing to draw from. It shows the tab the address
// asks for as a skeleton, not the Appearance tab that needs no schema.
test("the first open shows the requested tab as a skeleton, not Appearance", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>(() => {})),
  );
  const { container } = render(
    <Settings onClose={() => {}} initialSection="providers" />,
  );

  expect(screen.getByTestId("settings-skeleton")).toBeTruthy();
  expect(container.querySelector(".appearance-swatch-grid")).toBeNull();
  expect(
    screen.getByTestId("settings-tab-appearance").getAttribute("aria-current"),
  ).toBeNull();
});

test("on the narrow shell the first open shows the requested section as a skeleton, not the tiles", () => {
  const original = window.matchMedia;
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: query.includes("max-width"),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
  try {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    const { container } = render(
      <Settings onClose={() => {}} initialSection="providers" />,
    );

    expect(screen.getByTestId("settings-skeleton")).toBeTruthy();
    expect(screen.queryByTestId("settings-tile-grid")).toBeNull();
    expect(container.querySelector(".settings-head-section")?.textContent).toBe(
      "LLM providers",
    );
  } finally {
    window.matchMedia = original;
  }
});

// Escape takes the step the head's arrow takes while the head shows one: a
// section on the narrow shell goes back to the tiles, one still loading too,
// and only then does the drawer close.
test("on the narrow shell Escape takes a section, even one still loading, back to the tiles first", () => {
  const original = window.matchMedia;
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: query.includes("max-width"),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
  try {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    const closeDrawer = vi.fn();
    render(
      <>
        <OpenRailScreen id="settings" onClose={closeDrawer} />
        <Settings onClose={closeDrawer} initialSection="providers" />
      </>,
    );
    expect(screen.getByTestId("settings-skeleton")).toBeTruthy();

    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(screen.getByTestId("settings-tile-grid")).toBeTruthy();
    expect(closeDrawer).not.toHaveBeenCalled();

    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(closeDrawer).toHaveBeenCalledTimes(1);
  } finally {
    window.matchMedia = original;
  }
});

// The kept copy follows the server: config_reloaded reads it again in the
// background. An open form takes the new copy while it holds no edits of its
// own; with unsaved edits it keeps them, and the next open starts from the new
// copy.
test("a configuration reload reaches the kept copy and an untouched open form, never unsaved edits", async () => {
  stubServer();
  const view = render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  expect(input.value).toBe("40");

  server.config = { ...server.config, agent: { max_turns: 60 } };
  act(() => noteSettingsConfigReloaded());
  await waitFor(() => expect(input.value).toBe("60"));

  fireEvent.change(input, { target: { value: "45" } });
  server.config = { ...server.config, agent: { max_turns: 70 } };
  act(() => noteSettingsConfigReloaded());
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });
  expect(input.value).toBe("45");

  view.unmount();
  render(<Settings onClose={() => {}} initialSection="agent" />);
  expect((screen.getByLabelText("Max turns") as HTMLInputElement).value).toBe(
    "70",
  );
});

// A save says it worked on the Save button alone: green for a couple of seconds,
// no line of text. The line used to stay on screen for as long as the drawer did.
test("a save lights the Save button green for two seconds and writes no text", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  stubServer();
  const { container } = render(
    <Settings onClose={() => {}} initialSection="agent" />,
  );
  await screen.findByLabelText("Max turns");
  const save = screen.getByTestId("settings-save");

  await act(async () => {
    fireEvent.click(save);
  });
  await waitFor(() => expect(save.className).toContain("is-saved"));
  expect(container.querySelector(".settings-lead-pane")).toBeNull();
  expect(container.querySelector(".settings-ok")).toBeNull();

  await act(async () => {
    await vi.advanceTimersByTimeAsync(2100);
  });
  expect(save.className).not.toContain("is-saved");
});

// A failed save reports what went wrong where the status band stands, in words,
// and the Save button does not pretend it worked.
test("a failed save shows the error in the status band and leaves the button as it was", async () => {
  stubServer();
  server.put = {
    status: 400,
    body: { ok: false, error: "models[0].model: provider demo is not listed" },
  };
  const { container } = render(
    <Settings onClose={() => {}} initialSection="agent" />,
  );
  await screen.findByLabelText("Max turns");
  const save = screen.getByTestId("settings-save");

  await act(async () => {
    fireEvent.click(save);
  });

  await waitFor(() =>
    expect(
      container.querySelector(".settings-lead-pane .settings-error")
        ?.textContent,
    ).toBe("models[0].model: provider demo is not listed"),
  );
  expect(save.className).not.toContain("is-saved");
});

/**
 * Settings the way App mounts it: the section and the row come from the address,
 * which the drawer itself rewrites and App reads back on hashchange.
 */
function RoutedSettings() {
  const read = () => {
    const route = parseAppHash();
    return route.branch === "settings"
      ? { section: route.section, item: route.item }
      : { section: null, item: null };
  };
  const [route, setRoute] = useState(read);
  useEffect(() => {
    const on = () => setRoute(read());
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return (
    <Settings
      onClose={() => {}}
      initialSection={route.section}
      initialItem={route.item}
    />
  );
}

// Issue #359: with a provider open, going to Logical models opened the first
// model instead of the list (00dbbee6 fixed the path through SettingsSection;
// this holds the drawer with its address).
test("going to Logical models with a provider open shows the list of models", async () => {
  stubServer();
  window.location.hash = "#/settings/providers";
  const { container } = render(<RoutedSettings />);
  fireEvent.click(await screen.findByTestId("settings-master-item-0"));
  await waitFor(() =>
    expect(window.location.hash).toBe("#/settings/providers?id=demo"),
  );

  fireEvent.click(screen.getByTestId("settings-tab-models"));

  await waitFor(() => expect(window.location.hash).toBe("#/settings/models"));
  expect(screen.getByTestId("settings-master-item-0").textContent).toBe(
    "demo/m1",
  );
  expect(container.querySelector(".settings-detail")).toBeNull();
  expect(container.querySelector(".settings-head-section")).toBeNull();
});

// The first open with an address naming a row: the tab is drawn from the first
// read once it lands, rows and all, so the row opens and the address keeps it.
// A frame drawn from an empty document in between would show the list with no
// rows, and the list takes an address naming a row it lacks for a stale one.
test("a first open at an address naming a row opens that row and keeps the address", async () => {
  stubServer();
  window.location.hash = "#/settings/models?id=demo%2Fm2";
  const writes: string[] = [];
  const replace = history.replaceState.bind(history);
  const spy = vi
    .spyOn(history, "replaceState")
    .mockImplementation((data, unused, url) => {
      writes.push(String(url ?? ""));
      replace(data, unused, url);
    });
  try {
    const { container } = render(<RoutedSettings />);
    // The model field shows the id after the provider's name and its slash.
    await waitFor(() =>
      expect(
        (screen.getByLabelText("Model id") as HTMLInputElement).value,
      ).toBe("m2"),
    );
    expect(container.querySelector(".settings-head-section")?.textContent).toBe(
      "Model settings",
    );
    expect(window.location.hash).toBe("#/settings/models?id=demo%2Fm2");
    expect(writes).toEqual([]);
  } finally {
    spy.mockRestore();
  }
});

// After a save the form shows what it saved, while the copy read after the save
// is still on its way - never the copy from before the save.
test("a save keeps its values on screen until the fresh copy lands", async () => {
  const fetch = stubServer();
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "45" } });

  // Hold the reads that come after the save.
  let release: () => void = () => {};
  const held = new Promise<void>((r) => {
    release = r;
  });
  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if ((init?.method ?? "GET") === "GET" && path.startsWith("/coddy/config")) {
      await held;
    }
    return answer(path, init);
  });

  await act(async () => {
    fireEvent.click(screen.getByTestId("settings-save"));
  });
  await waitFor(() =>
    expect(screen.getByTestId("settings-save").className).toContain("is-saved"),
  );
  expect(input.value).toBe("45");

  await act(async () => {
    release();
    await held;
  });
  await waitFor(() => expect(input.value).toBe("45"));
  expect(server.config).toMatchObject({ agent: { max_turns: 45 } });
});

// The copy the app keeps takes what a save wrote at once. A reopen before the
// read after the save lands - or after that read failed - draws the saved
// values, never the ones the save replaced: saving again from those would put
// them back.
test("a reopen right after a save draws what was saved, even when the read after it fails", async () => {
  const fetch = stubServer();
  const view = render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "45" } });

  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if ((init?.method ?? "GET") === "GET" && path.startsWith("/coddy/config")) {
      return {
        ok: false,
        status: 502,
        json: async () => ({}),
      } as unknown as Awaited<ReturnType<typeof answer>>;
    }
    return answer(path, init);
  });
  await act(async () => {
    fireEvent.click(screen.getByTestId("settings-save"));
  });
  await waitFor(() =>
    expect(screen.getByTestId("settings-save").className).toContain("is-saved"),
  );
  view.unmount();

  render(<Settings onClose={() => {}} initialSection="agent" />);
  expect((screen.getByLabelText("Max turns") as HTMLInputElement).value).toBe(
    "45",
  );
});

// Reload drops the edits the form held when it was pressed; a field typed
// into while its read is on its way keeps what was typed.
test("edits typed while Reload is on its way stay", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const fetch = stubServer();
  render(<Settings onClose={() => {}} initialSection="agent" />);
  await screen.findByLabelText("Max turns");

  let release: () => void = () => {};
  const held = new Promise<void>((r) => {
    release = r;
  });
  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if ((init?.method ?? "GET") === "GET" && path.startsWith("/coddy/config")) {
      await held;
    }
    return answer(path, init);
  });
  await act(async () => {
    fireEvent.click(screen.getByTestId("settings-reload"));
  });
  fireEvent.change(screen.getByLabelText("Max turns"), {
    target: { value: "55" },
  });
  await act(async () => {
    release();
    await vi.advanceTimersByTimeAsync(600);
  });

  expect((screen.getByLabelText("Max turns") as HTMLInputElement).value).toBe(
    "55",
  );
});

// A save that fails right after one that went through does not keep the green
// of the first: the band says what went wrong and the button is back to itself.
test("a failed save right after a successful one leaves the button as it was", async () => {
  stubServer();
  const { container } = render(
    <Settings onClose={() => {}} initialSection="agent" />,
  );
  await screen.findByLabelText("Max turns");
  const save = screen.getByTestId("settings-save");
  await act(async () => {
    fireEvent.click(save);
  });
  await waitFor(() => expect(save.className).toContain("is-saved"));

  server.put = { status: 400, body: { ok: false, error: "agent: refused" } };
  await act(async () => {
    fireEvent.click(save);
  });
  await waitFor(() =>
    expect(
      container.querySelector(".settings-lead-pane .settings-error")
        ?.textContent,
    ).toBe("agent: refused"),
  );
  expect(save.className).not.toContain("is-saved");
});

// A first read that failed is read again by the next open. Until that read
// ends, the requested tab shows as loading - the error of the attempt before
// is not the state of this one, and Appearance does not stand in for the tab.
test("a reopen after a failed first read shows the requested tab loading, not Appearance", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: false, status: 502, json: async () => ({}) })),
  );
  const first = render(
    <Settings onClose={() => {}} initialSection="providers" />,
  );
  await waitFor(() =>
    expect(
      first.container.querySelector(".settings-lead-pane .settings-error"),
    ).toBeTruthy(),
  );
  first.unmount();

  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>(() => {})),
  );
  // What the first frame shows, before the effects that start the new read.
  const frames: { skeleton: boolean; appearance: boolean }[] = [];
  function FirstFrame() {
    useLayoutEffect(() => {
      frames.push({
        skeleton:
          document.querySelector('[data-testid="settings-skeleton"]') !== null,
        appearance: document.querySelector(".appearance-swatch-grid") !== null,
      });
    }, []);
    return null;
  }
  const { container } = render(
    <>
      <Settings onClose={() => {}} initialSection="providers" />
      <FirstFrame />
    </>,
  );
  expect(frames).toEqual([{ skeleton: true, appearance: false }]);
  await act(async () => {});
  expect(screen.getByTestId("settings-skeleton")).toBeTruthy();
  expect(container.querySelector(".settings-lead-pane")).toBeNull();
});

// A save that ends after the operator typed on saved what it sent, not what the
// form shows now: the button does not turn green, and the form still holds
// edits a reload of the server's config leaves alone.
test("a save during which the operator typed on does not turn the button green", async () => {
  const fetch = stubServer();
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "45" } });

  let release: () => void = () => {};
  const held = new Promise<void>((r) => {
    release = r;
  });
  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if (init?.method === "PUT") {
      await held;
    }
    return answer(path, init);
  });
  const save = screen.getByTestId("settings-save");
  await act(async () => {
    fireEvent.click(save);
  });
  fireEvent.change(input, { target: { value: "46" } });
  await act(async () => {
    release();
    await held;
  });
  await waitFor(() =>
    expect(server.config).toMatchObject({ agent: { max_turns: 45 } }),
  );
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });

  expect(save.className).not.toContain("is-saved");
  expect(input.value).toBe("46");
});

// A reload of the server's config reached an open provider form that holds no
// edits, and the providers came back in another order. The form stays on the
// provider the address names instead of showing whichever one took its place.
test("an open row form keeps its row when a reload reorders the list", async () => {
  stubServer();
  server.config = {
    ...server.config,
    providers: [{ name: "demo" }, { name: "spare" }],
  };
  window.location.hash = "#/settings/providers?id=spare";
  render(<RoutedSettings />);
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Provider id") as HTMLInputElement).value,
    ).toBe("spare"),
  );

  server.config = {
    ...server.config,
    providers: [{ name: "spare" }, { name: "demo" }],
  };
  act(() => noteSettingsConfigReloaded());
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });

  expect((screen.getByLabelText("Provider id") as HTMLInputElement).value).toBe(
    "spare",
  );
  expect(window.location.hash).toBe("#/settings/providers?id=spare");
});

// A newer copy the untouched form takes while a save runs is no edit of the
// operator's: the save still turns the button green, and the form keeps
// following the server's config afterwards.
test("a copy taken while a save runs does not keep the button from turning green", async () => {
  const fetch = stubServer();
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;

  let release: () => void = () => {};
  const held = new Promise<void>((r) => {
    release = r;
  });
  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if (init?.method === "PUT") {
      await held;
    }
    return answer(path, init);
  });
  const save = screen.getByTestId("settings-save");
  await act(async () => {
    fireEvent.click(save);
  });
  server.config = { ...server.config, agent: { max_turns: 41 } };
  act(() => noteSettingsConfigReloaded());
  await waitFor(() => expect(input.value).toBe("41"));
  await act(async () => {
    release();
    await held;
  });
  await waitFor(() => expect(save.className).toContain("is-saved"));

  server.config = { ...server.config, agent: { max_turns: 43 } };
  act(() => noteSettingsConfigReloaded());
  await waitFor(() => expect(input.value).toBe("43"));
});

// ---- the settings search

/** The page's animation frames, run by hand: a reveal waits one, and jsdom's own fire late under act. */
function frameClock() {
  const queue = new Map<number, FrameRequestCallback>();
  let next = 0;
  const spies = [
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
      queue.set(++next, callback);
      return next;
    }),
    vi.spyOn(window, "cancelAnimationFrame").mockImplementation((id) => {
      queue.delete(id);
    }),
  ];
  return {
    /** Lets the observers see what changed and runs the frames asked for, until the page is quiet. */
    async settle() {
      for (let round = 0; round < 8; round++) {
        await act(async () => {
          await Promise.resolve();
          await Promise.resolve();
        });
        const due = [...queue.values()];
        queue.clear();
        if (due.length > 0) {
          await act(async () => {
            due.forEach((callback) => callback(performance.now()));
          });
        }
      }
    },
    restore: () => spies.forEach((spy) => spy.mockRestore()),
  };
}

/** The drawer, open on a page that follows its address the way App's does. */
const drawer = (opts: MountOptions = {}) =>
  openSettings({ routed: true, ...opts });

const searchInput = () =>
  document.querySelector<HTMLInputElement>(".settings-search-input")!;

const typeQuery = (text: string) =>
  fireEvent.change(searchInput(), { target: { value: text } });

const options = () => [
  ...document.querySelectorAll<HTMLElement>('[role="option"]'),
];

const selected = () =>
  options().filter((o) => o.getAttribute("aria-selected") === "true");

/** The result for a box (and a row): its dataset says which it is. */
function optionOf(anchor: string, row?: string): HTMLElement {
  const found = options().find(
    (o) =>
      o.dataset["settingsTarget"] === anchor &&
      (row === undefined || o.dataset["settingsRow"] === row),
  );
  if (found === undefined) {
    throw new Error(`no result for ${anchor} ${row ?? ""}`);
  }
  return found;
}

/** The boxes of the page that carry an anchor, in document order. */
const boxesOf = (anchor: string, scope: ParentNode = document) =>
  [...scope.querySelectorAll<HTMLElement>("[data-settings-anchor]")].filter(
    (b) => b.dataset["settingsAnchor"] === anchor,
  );

const boxOf = (anchor: string, scope: ParentNode = document) =>
  boxesOf(anchor, scope)[0] ?? null;

const rowForm = (row: string) =>
  [...document.querySelectorAll<HTMLElement>(".settings-detail")].find(
    (form) => form.dataset["settingsRow"] === row,
  ) ?? null;

const headTitle = () =>
  document.querySelector(".sessions-head")!.firstElementChild!.textContent;

/**
 * What a test needs to place a (i)'s tip, which jsdom cannot lay out: the tip
 * gets a size, the (i) a place, and a touch pins the tip the way a phone does.
 * A tip of 100 x 40 opens above a 20px (i) at (left, top), centred on it: for
 * an (i) at (400, 300) its style is left 360px, top 252px.
 */
const TIP = { width: 100, height: 40, hint: 20 } as const;

/** Gives every element the size of a tip until the test finishes. */
function stubTipSize(): void {
  const sizes = [
    vi
      .spyOn(HTMLElement.prototype, "offsetWidth", "get")
      .mockReturnValue(TIP.width),
    vi
      .spyOn(HTMLElement.prototype, "offsetHeight", "get")
      .mockReturnValue(TIP.height),
  ];
  onTestFinished(() => sizes.forEach((spy) => spy.mockRestore()));
}

/** Puts the (i) at a place in the viewport. */
function placeHint(hint: HTMLElement, left: number, top: number): void {
  const size = TIP.hint;
  hint.getBoundingClientRect = () =>
    ({
      left,
      top,
      right: left + size,
      bottom: top + size,
      width: size,
      height: size,
      x: left,
      y: top,
      toJSON: () => ({}),
    }) as DOMRect;
}

/** A touch on the (i), which pins its tip open. */
function pinTipByTouch(hint: HTMLElement): void {
  // jsdom has no PointerEvent: a plain event carries the pointer type.
  const touch = new Event("pointerdown", { bubbles: true });
  Object.defineProperty(touch, "pointerType", { value: "touch" });
  fireEvent(hint, touch);
  fireEvent.click(hint);
}

const isRinged = (el: Element) => el.classList.contains("is-search-target");

const tabIsOpen = (id: string) =>
  screen.getByTestId(`settings-tab-${id}`).getAttribute("aria-current") ===
  "page";

/** Whether the "Advanced settings" fold in a row form (or the fold itself) is open. */
const advancedOpen = (form: HTMLElement) =>
  within(form)
    .getByRole("button", { name: "Advanced settings" })
    .getAttribute("aria-expanded") === "true";

const expectAddress = (hash: string) => expect(window.location.hash).toBe(hash);

const expectCursor = (el: Element | null) =>
  expect(document.activeElement).toBe(el);

/** How a reveal scrolls: to the middle of the area, or to the top of a tall setting. */
const smoothTo = (block: "center" | "start") => ({ block, behavior: "smooth" });

/** Answers some GET routes of the stubbed server with the given bodies. */
function answering(
  fetch: ReturnType<typeof stubServerFor>,
  routes: Record<string, unknown>,
) {
  const base = fetch.getMockImplementation()!;
  fetch.mockImplementation((async (path: string, init?: RequestInit) => {
    const route = new URL(String(path), "http://coddy.test").pathname;
    if (route in routes && (init?.method ?? "GET") === "GET") {
      return {
        ok: true,
        status: 200,
        json: async () => structuredClone(routes[route]),
      };
    }
    return base(path, init);
  }) as never);
}

/** Fake timers that leave the animation frames to frameClock (the fake clock would take them over). */
function useClockWithFrames() {
  vi.useFakeTimers({
    shouldAdvanceTime: true,
    toFake: [
      "setTimeout",
      "clearTimeout",
      "setInterval",
      "clearInterval",
      "Date",
    ],
  });
}

const tick = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });

describe("settings search", () => {
  let scrolled: ReturnType<typeof vi.fn>;
  let frames: ReturnType<typeof frameClock>;
  /** The page at each scroll to the middle: the box or a fold above it hidden, and the fold's own body. */
  let atScroll: { hidden: boolean; foldBodyHidden: boolean }[];
  /** What a reveal scrolled to (the middle of the area by default), in order. */
  const scrolledTo = (block = "center") =>
    scrolled.mock.contexts.filter(
      (_, i) =>
        (scrolled.mock.calls[i]?.[0] as { block?: string })?.block === block,
    ) as Element[];

  beforeEach(() => {
    atScroll = [];
    scrolled = vi.fn(function (this: Element, arg?: { block?: string }) {
      if (arg?.block === "center") {
        atScroll.push({
          hidden: this.closest("[hidden]") !== null,
          foldBodyHidden:
            this.querySelector<HTMLElement>(
              ":scope > .settings-form-group-body",
            )?.hidden === true,
        });
      }
    });
    Element.prototype.scrollIntoView = scrolled;
    frames = frameClock();
  });

  afterEach(() => {
    delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
    frames.restore();
    resetPage();
  });

  /** Types the query, chooses the result for a box (of a row) and lets the page reveal it. */
  async function choose(query: string, anchor: string, row?: string) {
    typeQuery(query);
    fireEvent.click(optionOf(anchor, row));
    await frames.settle();
  }

  /** Clicks a control of the drawer and lets the page settle. */
  async function press(testId: string) {
    fireEvent.click(screen.getByTestId(testId));
    await frames.settle();
  }

  /** The field takes the cursor back and Enter opens the highlighted result. */
  async function enterAgain() {
    act(() => searchInput().focus());
    fireEvent.keyDown(searchInput(), { key: "Enter" });
    await frames.settle();
  }

  /** Moves the address the way a pasted link or a hashchange does. */
  async function goTo(hash: string) {
    await act(async () => {
      window.location.hash = hash;
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
    await frames.settle();
  }

  const arrow = (key: "ArrowDown" | "ArrowUp", times = 1) => {
    for (let i = 0; i < times; i++) {
      fireEvent.keyDown(searchInput(), { key });
    }
  };

  const pressEscape = (target: Element = document.body) =>
    fireEvent.keyDown(target, { key: "Escape" });

  const badge = () => document.querySelector(".settings-search-kbd");
  const slash = (
    target: Element = document.body,
    init: KeyboardEventInit = {},
  ) => fireEvent.keyDown(target, { key: "/", ...init });

  test("the slash badge shows in an empty field that has no cursor, and goes while the field has the cursor or holds text", async () => {
    const view = await drawer({ section: "agent" });
    expect(badge()?.textContent).toBe("/");
    expect(badge()!.getAttribute("aria-hidden")).toBe("true");
    // The cursor is in the field: the shortcut has done its work.
    act(() => searchInput().focus());
    expect(badge()).toBeNull();
    act(() => searchInput().blur());
    expect(badge()).not.toBeNull();
    // Text in the field, with or without the cursor.
    act(() => searchInput().focus());
    typeQuery("proxy");
    expect(badge()).toBeNull();
    act(() => searchInput().blur());
    expect(badge()).toBeNull();
    // Cleared again, the cursor gone: the badge is back.
    act(() => searchInput().focus());
    pressEscape(searchInput());
    expect(searchInput().value).toBe("");
    expect(badge()).toBeNull();
    act(() => searchInput().blur());
    expect(badge()).not.toBeNull();
    view.unmount();
    // A narrow desktop window has a keyboard: the badge shows over the tiles too.
    const narrow = await drawer({ width: 390 });
    expect(screen.getByTestId("settings-tile-grid")).toBeTruthy();
    expect(badge()).not.toBeNull();
    act(() => searchInput().focus());
    expect(badge()).toBeNull();
    narrow.unmount();
    // After a jump that leaves the cursor in the revealed setting and the field empty.
    await drawer({ section: "agent", field: "agent:max_turns" });
    await frames.settle();
    expectCursor(screen.getByLabelText("Max turns"));
    expect(searchInput().value).toBe("");
    expect(badge()).not.toBeNull();
  });

  test("a touch-only device has no key to press, so no badge is drawn on any width", async () => {
    for (const width of [1280, 390]) {
      const view = await drawer({ width, touch: true });
      expect(searchInput()).not.toBeNull();
      expect(badge()).toBeNull();
      view.unmount();
    }
  });

  test("slash puts the cursor in the settings search unless a field has it or a dialog is open", async () => {
    const view = await drawer({ section: "agent" });
    // Nothing has the cursor: the search takes it, and no "/" is typed.
    expect(slash()).toBe(false);
    expectCursor(searchInput());
    act(() => searchInput().blur());
    // A modifier makes it a shortcut that belongs to the browser.
    expect(slash(document.body, { ctrlKey: true })).toBe(true);
    expect(slash(document.body, { metaKey: true })).toBe(true);
    expectCursor(document.body);
    // A field with the cursor takes the character.
    const field = screen.getByLabelText("Max turns");
    act(() => field.focus());
    expect(slash(field)).toBe(true);
    expectCursor(field);
    act(() => field.blur());
    // A dialog is open: the cursor stays where it was.
    const dialog = document.body.appendChild(document.createElement("div"));
    dialog.setAttribute("aria-modal", "true");
    expect(slash()).toBe(true);
    expectCursor(document.body);
    dialog.remove();
    view.unmount();
    // On a narrow screen with a tab open the field is not drawn: slash goes back to the tiles.
    await drawer({ width: 390, section: "agent" });
    expect(searchInput()).toBeNull();
    expect(screen.getByLabelText("Max turns")).toBeTruthy();
    expect(slash()).toBe(false);
    expect(screen.getByTestId("settings-tile-grid")).toBeTruthy();
    expectAddress("#/settings");
    expectCursor(searchInput());
    // On the tiles the field takes the cursor at once.
    act(() => searchInput().blur());
    expect(slash()).toBe(false);
    expectCursor(searchInput());
  });

  test("typing a query shows the matching settings of every tab in place of the sections, with the words marked", async () => {
    const view = await drawer({ section: "agent" });
    expect(searchInput().disabled).toBe(false);
    expect(screen.queryByRole("listbox")).toBeNull();

    typeQuery("proxy");

    const list = screen.getByRole("listbox", { name: "Matching settings" });
    const found = within(list).getAllByRole("option");
    const targets = found.map(
      (o) => `${o.dataset["settingsTarget"]}|${o.dataset["settingsRow"] ?? ""}`,
    );
    expect(targets).toContain("providers:proxy|openai");
    expect(targets).toContain("providers:proxy|anthropic");
    expect(targets).toContain("gateways:telegram.proxy|");
    // The words of the query are marked wherever they stand.
    const marked = [...found[0]!.querySelectorAll("mark")].map((m) =>
      m.textContent!.toLowerCase(),
    );
    expect(marked.length).toBeGreaterThan(0);
    expect(marked.every((m) => m === "proxy")).toBe(true);
    // They stand in place of the sections: the rail names no tab, the tab under them is hidden.
    expect(document.querySelector('[aria-current="page"]')).toBeNull();
    const tab = document.querySelector<HTMLElement>(
      '[data-settings-tab="agent"]',
    )!;
    expect(tab.hidden).toBe(true);
    expect(screen.getByLabelText("Max turns")).toBeTruthy();
    expect(within(tab).queryByRole("listbox")).toBeNull();
    expect(searchInput().getAttribute("aria-expanded")).toBe("true");
    view.unmount();
    // The field waits for the first read.
    const first = mountSettings({ routed: true });
    expect(searchInput().disabled).toBe(true);
    await waitFor(() => expect(searchInput().disabled).toBe(false));
    first.unmount();
  });

  test("choosing a result writes the setting into the address, next to its row, and a tab writes none", async () => {
    await drawer();
    await choose("max_turns", "agent:max_turns");
    expectAddress("#/settings/agent?field=agent%3Amax_turns");
    // A field of a row: the row and the setting, and the row opening keeps the setting.
    await choose("proxy", "providers:proxy", "openai");
    expectAddress("#/settings/providers?id=openai&field=providers%3Aproxy");
    // A block of a row, a dot and a colon in the anchor of a list's field.
    await choose("advanced", "group:providers:advanced", "anthropic");
    expectAddress(
      "#/settings/providers?id=anthropic&field=group%3Aproviders%3Aadvanced",
    );
    await choose("component", "logger:levels.component");
    expectAddress("#/settings/logger?field=logger%3Alevels.component");
    // A tab result is the tab: no setting in the address.
    await choose("ReAct loop", "tab:agent");
    expectAddress("#/settings/agent");
    // The history sidebar stays next to the setting a choice writes.
    window.location.hash = "#/settings?history=1";
    await choose("max_turns", "agent:max_turns");
    expectAddress("#/settings/agent?field=agent%3Amax_turns&history=1");
  });

  type Landing = (box: HTMLElement, form: HTMLElement | null) => void;
  const onMaxTurns: Landing = () => {
    expect(tabIsOpen("agent")).toBe(true);
    expectCursor(screen.getByLabelText("Max turns"));
  };
  const onProxyField: Landing = (box, form) => {
    expect(form!.getAttribute("role")).toBe("group");
    expect(advancedOpen(form!)).toBe(true);
    expectCursor(within(box).getByLabelText("Proxy URL"));
  };
  const onPairingTokens: Landing = (box) => {
    expect(tabIsOpen("swarm")).toBe(true);
    expect(box.contains(document.activeElement)).toBe(true);
  };

  // Each row opens the drawer, reaches a setting by address or by choosing a result, and says where it lands.
  const revealCases: [
    title: string,
    open: MountOptions,
    pick: [query: string, anchor: string, row?: string] | null,
    landing: Landing,
  ][] = [
    [
      "an address that names a setting reveals it as a choice does: the tab, the ring, the scroll and the control",
      { section: "agent", field: "agent:max_turns" },
      null,
      onMaxTurns,
    ],
    [
      "an address that names a field of a row opens the row, unfolds its block and reveals the field",
      { section: "providers", item: "openai", field: "providers:proxy" },
      null,
      onProxyField,
    ],
    [
      "on a touch-only device an address that names a setting takes the focus to its box",
      { section: "agent", field: "agent:max_turns", width: 390, touch: true },
      null,
      (box) => {
        expectCursor(box);
        expect(box.getAttribute("role")).toBe("group");
        expect(box.getAttribute("aria-label")).toBe("Max turns");
      },
    ],
    [
      "a relay's own settings open on the setting an address names",
      { schema: RELAY_SCHEMA, section: "swarm", field: "swarm:pairing_tokens" },
      null,
      onPairingTokens,
    ],
    [
      "a folded group named by an address opens as it does when chosen",
      {
        section: "providers",
        item: "anthropic",
        field: "group:providers:advanced",
      },
      null,
      (fold) => {
        expect(fold.classList.contains("is-open")).toBe(true);
        expectCursor(fold);
      },
    ],
    [
      "choosing a field of a provider row opens that row and unfolds the block it stands in",
      {},
      ["proxy", "providers:proxy", "openai"],
      onProxyField,
    ],
    [
      "choosing a field of another row of the open list reveals it in that row, not in the one that was open",
      { section: "providers", item: "openai" },
      ["proxy", "providers:proxy", "anthropic"],
      (box) => {
        expectAddress(
          "#/settings/providers?id=anthropic&field=providers%3Aproxy",
        );
        expect(rowForm("openai")).toBeNull();
        expectCursor(within(box).getByLabelText("Proxy URL"));
      },
    ],
    [
      "a relay's settings are searched too",
      { schema: RELAY_SCHEMA },
      ["pairing", "swarm:pairing_tokens"],
      onPairingTokens,
    ],
    [
      "choosing a field of the Metrics tab opens that tab, rings the setting and puts the cursor in it",
      {},
      ["listen port", "metrics:port"],
      () => {
        expect(tabIsOpen("metrics")).toBe(true);
        expectCursor(screen.getByLabelText("Listen port"));
      },
    ],
    [
      "choosing a field in another tab opens that tab, rings the setting and puts the cursor in it",
      {},
      ["max_turns", "agent:max_turns"],
      onMaxTurns,
    ],
    [
      "a folded group chosen from the results opens, is scrolled to whole and ringed, and takes the cursor as the named fieldset",
      {},
      ["advanced settings", "group:providers:advanced", "openai"],
      (fold) => {
        expect(fold.classList.contains("settings-fieldset--collapsible")).toBe(
          true,
        );
        // The fold that is the box opened, not only a fold above it.
        expect(fold.classList.contains("is-open")).toBe(true);
        expect(advancedOpen(fold)).toBe(true);
        // The cursor is on the fieldset, named by its legend alone.
        expectCursor(fold);
        expect(fold.hasAttribute("aria-label")).toBe(false);
        expect(fold.getAttribute("role")).toBeNull();
      },
    ],
    [
      "a setting hidden in a folded block is found, unfolded and then revealed, never replaced by its list's box",
      {},
      ["timeout_ms", "providers:timeout_ms", "anthropic"],
      (box, form) => {
        expect(advancedOpen(form!)).toBe(true);
        expect(document.activeElement?.getAttribute("aria-label")).toBe(
          "Request timeout ms",
        );
        // Neither the row form nor the fold took the ring.
        expect(isRinged(form!)).toBe(false);
        expect(
          document.querySelector(
            ".settings-fieldset--collapsible.is-search-target",
          ),
        ).toBeNull();
      },
    ],
  ];

  /** What the results offer: the box each result opens, a tab as `tab:<id>`. */
  const offered = () => options().map((o) => o.dataset["settingsTarget"] ?? "");

  test("the Metrics tab is a tab and a result, and a binary without its tag offers neither", async () => {
    // The whole schema: the tab, its field and the tab's name as results.
    const whole = await drawer();
    expect(screen.getByTestId("settings-tab-metrics").textContent).toContain(
      "Metrics",
    );
    typeQuery("metrics");
    expect(offered()).toContain("tab:metrics");
    expect(offered()).toContain("metrics:port");
    typeQuery("scrape");
    expect(offered()).toEqual(["metrics:token"]);
    whole.unmount();

    // The server left the section out of the schema; the document keeps it.
    const config = configFromSchema(AGENT_SCHEMA);
    expect(config["metrics"]).toBeTruthy();
    await drawer({ schema: schemaWithout(AGENT_SCHEMA, "metrics"), config });
    expect(screen.queryByTestId("settings-tab-metrics")).toBeNull();
    for (const query of ["metrics", "listen port", "scrape", "prometheus"]) {
      typeQuery(query);
      expect(
        offered().filter((t) => t === "tab:metrics" || /^metrics:/.test(t)),
        query,
      ).toEqual([]);
    }
    typeQuery("scrape");
    expect(screen.getByTestId("settings-search-empty")).toBeTruthy();
    expect(options()).toEqual([]);
  });

  test.each(revealCases)("%s", async (_title, open, pick, landing) => {
    useClockWithFrames();
    await drawer(open);
    const address = window.location.hash;
    if (pick !== null) {
      await choose(...pick);
    } else {
      await frames.settle();
    }
    const row = pick?.[2] ?? open.item;
    const form = row ? rowForm(row) : null;
    const box = boxOf(pick?.[1] ?? open.field!, form ?? document)!;
    expect(scrolledTo()).toEqual([box]);
    expect(scrolled).toHaveBeenCalledWith(smoothTo("center"));
    // Nothing above the box, and no fold body, was still hidden when the page scrolled.
    expect(atScroll).toEqual([{ hidden: false, foldBodyHidden: false }]);
    expect(isRinged(box)).toBe(true);
    landing(box, form);
    if (pick === null) {
      // An address is left as it was: no second write, no new entry.
      expectAddress(address);
    } else {
      // Chosen again from the field, it is revealed again and lands the same way.
      act(() => (document.activeElement as HTMLElement).blur());
      await enterAgain();
      expect(scrolledTo()).toEqual([box, box]);
      expect(isRinged(box)).toBe(true);
      landing(box, form);
    }
    // The ring goes after its time.
    await tick(SEARCH_TARGET_MS + 50);
    expect(isRinged(box)).toBe(false);
  });

  test("an address named while the first read is on its way, or changed later, reveals the setting once, and the drawer's own write is not a second reveal", async () => {
    const early = mountSettings({
      routed: true,
      section: "agent",
      field: "agent:max_turns",
    });
    expect(boxOf("agent:max_turns")).toBeNull();
    await frames.settle();
    await waitFor(() => expect(boxOf("agent:max_turns")).not.toBeNull());
    await frames.settle();
    expect(scrolledTo()).toEqual([boxOf("agent:max_turns")]);
    expectCursor(screen.getByLabelText("Max turns"));
    early.unmount();
    scrolled.mockClear();

    await drawer({ section: "agent" });
    await frames.settle();
    expect(scrolledTo()).toEqual([]);

    await goTo("#/settings/agent?field=agent%3Amax_turns");
    expect(scrolledTo()).toEqual([boxOf("agent:max_turns")]);
    expectCursor(screen.getByLabelText("Max turns"));
    // Another setting of the tab: a new reveal.
    await goTo("#/settings/agent?field=agent%3Aqueue_mode");
    expect(scrolledTo()).toEqual([
      boxOf("agent:max_turns"),
      boxOf("agent:queue_mode"),
    ]);
    expect(isRinged(boxOf("agent:queue_mode")!)).toBe(true);
    // A choice writes the address and the page reads it back: one reveal.
    await choose("llm_retry_max", "agent:llm_retry_max");
    expectAddress("#/settings/agent?field=agent%3Allm_retry_max");
    await frames.settle();
    expect(scrolledTo()).toHaveLength(3);
    expect(scrolledTo()[2]).toBe(boxOf("agent:llm_retry_max"));
  });

  test("a stale anchor opens the tab and the row and says nothing", async () => {
    const errors = vi.spyOn(console, "error");
    const warnings = vi.spyOn(console, "warn");
    onTestFinished(() => {
      errors.mockRestore();
      warnings.mockRestore();
    });
    await drawer({ section: "agent", field: "agent:ghost" });
    await frames.settle();
    expect(tabIsOpen("agent")).toBe(true);
    expect(screen.getByLabelText("Max turns")).toBeTruthy();
    // A real anchor under a row the list does not have is as stale.
    await goTo("#/settings/providers?id=openai&field=providers%3Aghost");
    expect(rowForm("openai")).not.toBeNull();
    // An anchor of another tab does not reveal in this one.
    await goTo("#/settings/logger?field=agent%3Amax_turns");

    expect(scrolledTo()).toEqual([]);
    expect(document.querySelector(".is-search-target")).toBeNull();
    expect(errors).not.toHaveBeenCalled();
    expect(warnings).not.toHaveBeenCalled();
  });

  test("a link followed while results show puts them away and reveals its setting", async () => {
    await drawer({ section: "agent" });
    typeQuery("proxy");
    expect(screen.queryByTestId("settings-search-results")).not.toBeNull();
    await goTo("#/settings/agent?field=agent%3Amax_turns");
    expect(screen.queryByTestId("settings-search-results")).toBeNull();
    const box = boxOf("agent:max_turns")!;
    expect(scrolledTo()).toEqual([box]);
    expect(isRinged(box)).toBe(true);
    expectCursor(screen.getByLabelText("Max turns"));
  });

  test("the setting leaves the address when the operator goes elsewhere, and a new choice replaces it", async () => {
    const view = await drawer({
      section: "providers",
      item: "openai",
      field: "providers:proxy",
    });
    await frames.settle();
    expectAddress("#/settings/providers?id=openai&field=providers%3Aproxy");
    // The head's back arrow closes the row: the list, no setting.
    await press("settings-head-back");
    expectAddress("#/settings/providers");
    // Another row opened from the list carries none of it.
    await goTo("#/settings/providers?id=openai&field=providers%3Aproxy");
    fireEvent.click(screen.getByTestId("settings-head-back"));
    await press("settings-master-item-1");
    expectAddress("#/settings/providers?id=anthropic");
    // A rail click.
    await goTo("#/settings/agent?field=agent%3Amax_turns");
    await press("settings-tab-logger");
    expectAddress("#/settings/logger");
    // A new choice replaces the old setting.
    await goTo("#/settings/agent?field=agent%3Amax_turns");
    await choose("queue", "agent:queue_mode");
    expectAddress("#/settings/agent?field=agent%3Aqueue_mode");
    view.unmount();
    // On the narrow shell the head's back arrow and a tile leave none either.
    await drawer({ section: "agent", field: "agent:max_turns", width: 390 });
    await frames.settle();
    expectAddress("#/settings/agent?field=agent%3Amax_turns");
    await press("settings-head-back");
    expectAddress("#/settings");
    await press("settings-tile-logger");
    expectAddress("#/settings/logger");
  });

  test("a block of a tab is found by its name and takes the ring, and so does a block of a model's row", async () => {
    await drawer();
    await choose("Model and turns", "group:agent:turn");
    const turn = boxOf("group:agent:turn")!;
    expect(turn.querySelector("legend")!.textContent!.trim()).toBe(
      "Model and turns",
    );
    expect(scrolledTo()).toEqual([turn]);
    expect(isRinged(turn)).toBe(true);
    expectCursor(turn);
    expectAddress("#/settings/agent?field=group%3Aagent%3Aturn");
    // The blocks of a model's row form are results per row.
    await choose("Generation", "group:models:generation", "demo/qwen3-27b");
    const form = rowForm("demo/qwen3-27b")!;
    const generation = boxOf("group:models:generation", form)!;
    expect(generation.tagName).toBe("FIELDSET");
    expect(scrolledTo().at(-1)).toBe(generation);
    expect(isRinged(generation)).toBe(true);
    expectCursor(generation);
    expect(isRinged(form)).toBe(false);
  });

  test("a nested object and a list are found by their legends, and the whole fieldset is scrolled to and ringed", async () => {
    await drawer();
    const cases: [string, string][] = [
      ["Tool result eviction", "compaction:result_eviction"],
      ["Component levels", "logger:levels"],
    ];
    for (const [name, anchor] of cases) {
      await choose(name, anchor);
      const box = boxOf(anchor)!;
      const group = box.querySelector(":scope > fieldset")!;
      expect(group.querySelector("legend")!.textContent).toContain(name);
      expect(scrolledTo().at(-1)).toBe(box);
      expect(isRinged(box)).toBe(true);
      // The box holds the whole fieldset and rings it from outside.
      expect(box.contains(group)).toBe(true);
      expect(box.contains(document.activeElement)).toBe(true);
    }
  });

  test("with results over an open row the head reads Settings, and with two rows of one name the second open, neither row's fields are offered", async () => {
    // The document holds two providers called "shared" and one without a name.
    await drawer({ section: "providers", item: "shared" });
    expect(headTitle()).toBe("Provider settings");
    expect(screen.getByTestId("settings-head-back")).toBeTruthy();
    typeQuery("proxy");
    expect(headTitle()).toBe("Settings");
    expect(screen.queryByTestId("settings-head-back")).toBeNull();

    const rows = options()
      .filter((o) => o.dataset["settingsTarget"] === "providers:proxy")
      .map((o) => o.dataset["settingsRow"]);
    expect(rows).toEqual([
      "openai",
      "anthropic",
      "neuraldeep",
      "codex",
      "devin",
    ]);
    expect(options().map((o) => o.dataset["settingsRow"])).not.toContain(
      "shared",
    );
    typeQuery("");
    expect(headTitle()).toBe("Provider settings");
    expect(screen.getByTestId("settings-head-back")).toBeTruthy();
  });

  test("the query stays after a choice, and the field brings the results back with the chosen one highlighted", async () => {
    await drawer();
    await choose("proxy", "gateways:telegram.proxy");
    expect(searchInput().value).toBe("proxy");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(searchInput().hasAttribute("aria-activedescendant")).toBe(false);

    act(() => searchInput().focus());
    const chosen = optionOf("gateways:telegram.proxy");
    expect(selected()).toEqual([chosen]);
    expect(searchInput().getAttribute("aria-activedescendant")).toBe(chosen.id);
    // A rail click puts the results away and keeps the query.
    await press("settings-tab-agent");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(searchInput().value).toBe("proxy");
    expect(tabIsOpen("agent")).toBe(true);
    // Emptying the field forgets the result chosen with the query, as Escape does.
    typeQuery("");
    typeQuery("proxy");
    expect(options()[0]!.dataset["settingsTarget"]).not.toBe(
      "gateways:telegram.proxy",
    );
    expect(selected()).toEqual([options()[0]]);
    expect(searchInput().getAttribute("aria-activedescendant")).toBe(
      options()[0]!.id,
    );
  });

  // The rows: query, anchor, row and the name of the control focused.
  const focusCases: [string, string, string, string][] = [
    ["proxy", "providers:proxy", "openai", "Proxy URL"],
    ["model id", "models:model", "demo/qwen3-27b", "Model id"],
    ["proxy", "providers:proxy", "direct", "Proxy URL"],
  ];

  test.each([
    [
      "a fine pointer",
      "the control named by the primary name (Proxy URL's input, a model's id input), never a switch beside it, a disabled input or the (i)",
      false,
    ],
    [
      "a touch-only device",
      "the setting's named box, so no keyboard opens",
      true,
    ],
  ])("on %s the focus goes to %s", async (_pointer, _focus, touch) => {
    const config = configFromSchema(AGENT_SCHEMA);
    const providers = config["providers"] as Record<string, unknown>[];
    // A provider that goes direct: its Proxy URL input is disabled.
    providers.push({ ...providers[0]!, name: "direct", proxy: "none" });
    for (const [query, anchor, row, name] of focusCases) {
      const view = await drawer(
        touch ? { config, width: 390, touch: true } : { config },
      );
      await choose(query, anchor, row);
      const box = boxOf(anchor, rowForm(row)!)!;
      const focused = document.activeElement as HTMLElement;
      expect(scrolledTo().at(-1)).toBe(box);
      if (!touch && row !== "direct") {
        // The id input, not the Provider combobox before it; the URL input, not the switch or the (i).
        expect(focused.tagName).toBe("INPUT");
        expect(focused.getAttribute("aria-label")).toBe(name);
        expect(box.contains(focused)).toBe(true);
      } else {
        // No keyboard rises: the box itself, named.
        expect(focused).toBe(box);
        expect(focused.getAttribute("role")).toBe("group");
        expect(focused.getAttribute("aria-label")).toBe(name);
      }
      view.unmount();
    }
  });

  test("the result's title is the primary name, whatever order the box draws its names in", async () => {
    await drawer();
    const title = (anchor: string, row: string) =>
      optionOf(anchor, row).querySelector(".settings-hit-title")!.textContent;
    typeQuery("ignore system");
    // The word is in the second name only: it is shown beside the first.
    expect(title("providers:proxy", "openai")).toBe(
      "Proxy URL · Ignore system proxy",
    );

    await choose("ignore system", "providers:proxy", "openai");
    // The box draws the switch before the URL.
    const drawn = boxOf("providers:proxy", rowForm("openai")!)!.textContent!;
    expect(drawn.indexOf("Ignore system proxy")).toBeLessThan(
      drawn.indexOf("Proxy URL"),
    );

    typeQuery("provider");
    expect(title("models:model", "demo/qwen3-27b")).toBe("Model id · Provider");
  });

  test("searching and choosing a result leave unsaved edits as they were", async () => {
    const view = await drawer({ section: "agent" });
    const saved = structuredClone(view.server.config);
    const turns = () =>
      (screen.getByLabelText("Max turns") as HTMLInputElement).value;
    fireEvent.change(screen.getByLabelText("Max turns"), {
      target: { value: "45" },
    });
    const status = () => screen.getByTestId("settings-save-status").textContent;
    expect(status()).toBe("Unsaved changes");

    typeQuery("proxy");
    expect(status()).toBe("Unsaved changes");
    // The tab under the results keeps what was typed.
    expect(turns()).toBe("45");

    await choose("proxy", "gateways:telegram.proxy");
    expect(status()).toBe("Unsaved changes");

    await press("settings-tab-agent");
    expect(turns()).toBe("45");
    expect(
      view.fetch.mock.calls.some(
        ([, init]) => (init as RequestInit | undefined)?.method === "PUT",
      ),
    ).toBe(false);
    expect(view.server.config).toEqual(saved);
  });

  /** The drawer with a rail screen open under it, which hears the Escape nothing claims. */
  async function drawerUnderRailScreen() {
    const closeDrawer = vi.fn();
    await drawer({
      beside: <OpenRailScreen id="settings" onClose={closeDrawer} />,
    });
    return closeDrawer;
  }

  test("Escape with results showing clears the query wherever the focus is", async () => {
    const closeDrawer = await drawerUnderRailScreen();
    // With the cursor nowhere, on a result and in the field.
    for (const focusOn of [
      () => document.body,
      () => options()[0]!,
      searchInput,
    ]) {
      typeQuery("proxy");
      const target = focusOn();
      act(() => target.focus());
      pressEscape(target);
      expect(searchInput().value).toBe("");
      expect(screen.queryByRole("listbox")).toBeNull();
    }
    expect(closeDrawer).not.toHaveBeenCalled();
  });

  test("Escape in the field with the results hidden clears the query, and with no query takes the drawer's own step", async () => {
    const closeDrawer = await drawerUnderRailScreen();

    act(() => searchInput().focus());
    typeQuery("max_turns");
    fireEvent.keyDown(searchInput(), { key: "Enter" });
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(searchInput().value).toBe("max_turns");
    // The cursor has not left the field yet.
    expectCursor(searchInput());
    pressEscape(searchInput());
    expect(searchInput().value).toBe("");
    expect(closeDrawer).not.toHaveBeenCalled();

    pressEscape();
    expect(closeDrawer).toHaveBeenCalledTimes(1);
  });

  test("the Telegram back button clears shown results first, then goes back from a tab to the tiles", async () => {
    const closeDrawer = vi.fn();
    await drawer({
      width: 390,
      touch: true,
      telegram: true,
      beside: <OpenRailScreen id="settings" onClose={closeDrawer} />,
    });
    // Telegram presses Back outside any React handler.
    const pressed: string[] = [];
    const back = () =>
      act(() => {
        pressed.push(pressBack(window, document));
      });
    await choose("max_turns", "agent:max_turns");
    expect(
      document.querySelector('input[aria-label="Max turns"]'),
    ).not.toBeNull();
    // Back from the tab: the tiles, with the query kept and its results away.
    back();
    expect(screen.getByTestId("settings-tile-grid")).toBeTruthy();
    expect(searchInput().value).toBe("max_turns");
    expect(screen.queryByRole("listbox")).toBeNull();
    // Tapped open again, the next Back clears the results, and the tiles stay.
    fireEvent.click(searchInput());
    expect(screen.getByRole("listbox")).toBeTruthy();
    expect(screen.queryByTestId("settings-tile-grid")).toBeNull();
    back();
    expect(searchInput().value).toBe("");
    expect(screen.getByTestId("settings-tile-grid")).toBeTruthy();
    expect(closeDrawer).not.toHaveBeenCalled();
    // Back again leaves Settings, as before.
    back();
    expect(closeDrawer).toHaveBeenCalledTimes(1);
    expect(pressed).toEqual(["escape", "escape", "escape"]);
  });

  // Each row opens a layer under the results (`beside` is rendered before the drawer, so it
  // hears Escape first), checks it is gone from the page while they show, and that
  // `clearResults` (Escape, the drawer staying open) brings it back as it was.
  type Layer = (beside: ReactNode, clearResults: () => void) => Promise<void>;
  const layersUnderResults: [string, Layer][] = [
    [
      "a tip pinned by a touch, the results scrolled meanwhile, placed again",
      async (beside, clearResults) => {
        stubTipSize();
        await drawer({ section: "agent", beside });
        const hint = screen.getAllByTestId("field-hint")[0]!;
        const tip = () => screen.queryByTestId("field-hint-tip");
        placeHint(hint, 400, 300);
        pinTipByTouch(hint);
        expect(tip()?.style.left).toBe("360px");
        expect(tip()?.style.top).toBe("252px");

        typeQuery("proxy");
        expect(tip()).toBeNull();
        expect(document.body.querySelector('[role="tooltip"]')).toBeNull();
        // Nothing the results do closes it: not their scroll, a resize, nor a press on them.
        const results = screen.getByTestId("settings-search-results");
        fireEvent.scroll(results);
        act(() => {
          window.dispatchEvent(new Event("resize"));
        });
        fireEvent.mouseDown(results);
        fireEvent.touchStart(results);
        placeHint(hint, 500, 100); // the (i) moved while the results were up
        clearResults();
        // As it was: open, placed against the (i) where it is now, pinned until something outside it moves.
        expect(tip()?.textContent).toBeTruthy();
        expect(tip()?.style.left).toBe("460px");
        expect(tip()?.style.top).toBe("52px");
        fireEvent.mouseLeave(hint);
        fireEvent.blur(hint);
        expect(tip()).not.toBeNull();
        act(() => {
          document.dispatchEvent(new Event("scroll"));
        });
        expect(tip()).toBeNull();
      },
    ],
    [
      "an open tag editor",
      async (beside, clearResults) => {
        const { fetch } = await drawer({ beside });
        const session = {
          id: "sess_one",
          title: "Refactor the parser",
          cwd: "/home/u/projects/coddy",
          updatedAt: "2026-09-02T10:00:00Z",
        };
        answering(fetch, {
          "/coddy/sessions": {
            sessions: [session],
            hasMore: false,
            nextCursor: null,
          },
        });
        fireEvent.click(screen.getByTestId("settings-tab-sessions_manager"));
        fireEvent.click(
          await screen.findByTestId("sessions-manager-tag-add-sess_one"),
        );
        fireEvent.change(await screen.findByTestId("session-tag-input"), {
          target: { value: "beta" },
        });
        const backdrop = () =>
          document.body.querySelector(".sessions-filter-backdrop");

        typeQuery("proxy");
        expect(screen.queryByTestId("session-tag-editor")).toBeNull();
        expect(backdrop()).toBeNull();
        clearResults();
        // The editor is back, open, with the draft it held.
        expect(
          (screen.getByTestId("session-tag-input") as HTMLInputElement).value,
        ).toBe("beta");
        expect(backdrop()).not.toBeNull();
      },
    ],
    [
      "the Skills install results",
      async (beside, clearResults) => {
        await drawer({ section: "skills", beside });
        const installInput = (await screen.findByTestId(
          "skills-install-input",
        )) as HTMLInputElement;
        const installResults = () =>
          screen.queryByTestId("skills-install-results");
        fireEvent.change(installInput, { target: { value: "alpha" } });
        expect(installResults()).toBeTruthy();

        typeQuery("proxy");
        // Still there, under the hidden tab, and deaf to the key.
        expect(installResults()!.closest("[hidden]")).not.toBeNull();
        clearResults();
        // As it was: the results hang under the box, and Escape takes them first again.
        expect(installInput.value).toBe("alpha");
        expect(installResults()!.closest("[hidden]")).toBeNull();
        clearResults();
        expect(installResults()).toBeNull();
        expect(installInput.value).toBe("");
      },
    ],
  ];

  test.each(layersUnderResults)(
    "while results show, %s draws nothing in the page and claims no Escape, and comes back as it was",
    async (_layer, run) => {
      const closeDrawer = vi.fn();
      await run(<OpenRailScreen id="settings" onClose={closeDrawer} />, () => {
        pressEscape();
        expect(searchInput().value).toBe("");
        expect(screen.queryByRole("listbox")).toBeNull();
        expect(closeDrawer).not.toHaveBeenCalled();
      });
    },
  );

  test("the highlighted result stays in view and stays the active descendant as the results change, and a click or focus in the field, or a trailing space in the query, keeps it", async () => {
    await drawer();
    act(() => searchInput().focus());
    typeQuery("proxy");
    const input = searchInput();
    const activeOption = () =>
      document.getElementById(input.getAttribute("aria-activedescendant")!);

    expect(selected()).toEqual([options()[0]]);
    expect(activeOption()).toBe(options()[0]);
    // The arrows move it, wrap around, and scroll it into view.
    arrow("ArrowDown", 2);
    expect(selected()).toEqual([options()[2]]);
    expect(activeOption()).toBe(options()[2]);
    expect(scrolled).toHaveBeenLastCalledWith({ block: "nearest" });
    expect(scrolledTo("nearest").at(-1)).toBe(options()[2]);
    // A click or focus in the field, or a trailing space in the query, keeps it.
    fireEvent.click(input);
    fireEvent.focus(input);
    typeQuery("proxy ");
    expect(selected()).toEqual([options()[2]]);
    arrow("ArrowUp", 3);
    expect(selected()).toEqual([options().at(-1)]);
    expect(scrolledTo("nearest").at(-1)).toBe(options().at(-1));
    arrow("ArrowDown");
    expect(selected()).toEqual([options()[0]]);
    // The chosen result stays highlighted while it is among the hits ...
    const chosen = () => optionOf("gateways:telegram.proxy");
    fireEvent.click(chosen());
    await frames.settle();
    act(() => input.focus());
    expect(selected()).toEqual([chosen()]);
    typeQuery("proxy url");
    expect(selected()).toEqual([chosen()]);
    expect(activeOption()).toBe(chosen());
    expect(scrolledTo("nearest").at(-1)).toBe(chosen());
    // ... and the first one takes it over when it is not.
    typeQuery("timeout");
    expect(options().map((o) => o.dataset["settingsTarget"])).not.toContain(
      "gateways:telegram.proxy",
    );
    expect(selected()).toEqual([options()[0]]);
    expect(activeOption()).toBe(options()[0]);
    // A query nothing matches says so, and leaves no active descendant.
    typeQuery("zzzzqq");
    expect(
      screen.getByText("No matching settings; try other or fewer words"),
    ).toBeTruthy();
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(input.getAttribute("aria-expanded")).toBe("false");
    expect(input.hasAttribute("aria-activedescendant")).toBe(false);
  });

  test("a pointer over the highlighted result does not skip the next scroll into view, and one resting over the results does not take the highlight when they change under it", async () => {
    await drawer();
    typeQuery("proxy");
    const hover = (index: number, x: number, y: number) =>
      fireEvent.mouseMove(options()[index]!, { clientX: x, clientY: y });
    // Over the result already highlighted nothing moves, and the next change still scrolls to it.
    hover(0, 40, 80);
    hover(0, 42, 82);
    const before = scrolledTo("nearest").length;
    typeQuery("proxy url");
    expect(scrolledTo("nearest").length).toBeGreaterThan(before);
    expect(scrolledTo("nearest").at(-1)).toBe(options()[0]);
    // A pointer moved onto a result highlights it.
    hover(2, 40, 200);
    hover(2, 41, 202);
    expect(selected()).toEqual([options()[2]]);
    // New results under a resting pointer: the browser reports a move at the same spot, no move of the operator's.
    typeQuery("proxy");
    expect(selected()).toEqual([options()[0]]);
    hover(2, 41, 202);
    expect(selected()).toEqual([options()[0]]);
    // Moving it again does.
    hover(2, 44, 206);
    expect(selected()).toEqual([options()[2]]);
  });

  test("a Russian page finds a setting by its Russian name and by its config key", async () => {
    await drawer({ lang: "ru" });
    expect(searchInput().placeholder).toBe("Поиск по настройкам");
    typeQuery("max_turns");
    // The name the page draws for the setting, read from the page.
    const name = optionOf("agent:max_turns").querySelector(
      ".settings-hit-title",
    )!.textContent!;
    expect(name).toMatch(/[а-яё]/i);

    await choose(name, "agent:max_turns");
    expectCursor(screen.getByLabelText(name));
    expect(tabIsOpen("agent")).toBe(true);
  });

  test("an entry added to a list after the search was built is revealed in that entry, and with the list empty again the list is revealed", async () => {
    const config = configFromSchema(AGENT_SCHEMA);
    (config["logger"] as Record<string, unknown>)["levels"] = [
      { component: "component-0", level: "debug" },
    ];
    await drawer({ config });
    const listBox = () => boxOf("logger:levels")!;
    const entries = () => boxesOf("logger:levels.component");

    await choose("component", "logger:levels.component");
    expect(entries()).toHaveLength(1);
    expect(scrolledTo().at(-1)).toBe(entries()[0]);
    expect(document.activeElement?.getAttribute("aria-label")).toBe(
      "Component",
    );
    // An entry added: the index holds no count, and the first entry in the document is revealed.
    fireEvent.click(within(listBox()).getByRole("button", { name: "Add" }));
    expect(entries()).toHaveLength(2);
    await enterAgain();
    expect(scrolledTo().at(-1)).toBe(entries()[0]);
    expect(entries()[0]!.contains(document.activeElement)).toBe(true);
    // Both removed: the list draws no entry, and the list itself is revealed.
    const remove = () =>
      within(listBox()).getAllByRole("button", { name: "Remove" })[0]!;
    fireEvent.click(remove());
    fireEvent.click(remove());
    expect(entries()).toHaveLength(0);
    await enterAgain();
    expect(scrolledTo().at(-1)).toBe(listBox());
    expect(isRinged(listBox())).toBe(true);
  });

  test("a setting taller than its scroll area is scrolled to by its top, so its name is not cut off", async () => {
    await drawer();
    const area = () =>
      document.querySelector<HTMLElement>(
        '.settings-scroll[data-settings-tab="agent"]',
      )!;
    const tall = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect");
    onTestFinished(() => tall.mockRestore());
    await choose("Model and turns", "group:agent:turn");
    // No layout in jsdom: a box of no height is not taller than anything.
    expect(scrolled).toHaveBeenLastCalledWith(smoothTo("center"));
    // The same group, now taller than the area it stands in.
    Object.defineProperty(area(), "clientHeight", {
      configurable: true,
      value: 300,
    });
    tall.mockImplementation(function (this: HTMLElement) {
      return {
        height:
          this.dataset["settingsAnchor"] === "group:agent:turn" ? 900 : 20,
      } as DOMRect;
    });
    await enterAgain();
    expect(scrolled).toHaveBeenLastCalledWith(smoothTo("start"));
    expect(scrolled.mock.contexts.at(-1)).toBe(boxOf("group:agent:turn"));
  });

  test("typing a query neither builds the index again nor renders the tab under the results again", async () => {
    await drawer({ section: "agent" });
    const built = indexBuilds.count;
    // The results start showing with the first word: the drawer renders for it.
    typeQuery("p");
    const rendered = rowsKeyCalls.count;

    for (const query of ["pr", "pro", "prox", "proxy", "proxy ", "proxy u"]) {
      typeQuery(query);
    }
    arrow("ArrowDown");
    arrow("ArrowUp");
    fireEvent.mouseMove(options()[1]!, { clientX: 40, clientY: 120 });
    fireEvent.mouseMove(options()[1]!, { clientX: 42, clientY: 122 });
    expect(options().length).toBeGreaterThan(0);
    // The index is made when the tabs, the schema, the rows or the language change.
    expect(indexBuilds.count).toBe(built);
    expect(rowsKeyCalls.count).toBe(rendered);
    // The counter does see a render: putting the results away is one.
    typeQuery("");
    expect(rowsKeyCalls.count).toBeGreaterThan(rendered);
  });

  test("a reveal whose setting never appears gives up quietly within a second", async () => {
    useClockWithFrames();
    await drawer();
    const errors = vi.spyOn(console, "error");
    onTestFinished(() => errors.mockRestore());
    // The fold above the setting does not hear the request to open: the reveal has nothing to go to.
    const deaf = (event: Event) => event.stopPropagation();
    const listenDeaf = () =>
      document.addEventListener(SETTINGS_REVEAL_EVENT, deaf, true);
    const hearAgain = () =>
      document.removeEventListener(SETTINGS_REVEAL_EVENT, deaf, true);
    onTestFinished(hearAgain);
    const chooseAgain = async () => {
      fireEvent.click(searchInput());
      await choose("timeout_ms", "providers:timeout_ms", "anthropic");
    };

    listenDeaf();
    await chooseAgain();
    const form = rowForm("anthropic")!;
    const fold = within(form).getByRole("button", {
      name: "Advanced settings",
    });
    const box = boxOf("providers:timeout_ms", form)!;
    expect(box.closest("[hidden]")).not.toBeNull();
    expect(scrolledTo()).toEqual([]);
    // Within the second it is still looking: let the fold hear, and the setting is gone to.
    await tick(REVEAL_WAIT_MS - 100);
    hearAgain();
    fireEvent.click(fold);
    await frames.settle();
    expect(scrolledTo()).toEqual([box]);
    expect(isRinged(box)).toBe(true);
    // Past the second it has given up: the block opening later takes nobody anywhere, silently.
    await tick(SEARCH_TARGET_MS);
    fireEvent.click(fold);
    expect(box.closest("[hidden]")).not.toBeNull();
    listenDeaf();
    await chooseAgain();
    await tick(REVEAL_WAIT_MS + 50);
    hearAgain();
    fireEvent.click(fold);
    await frames.settle();
    expect(box.closest("[hidden]")).toBeNull();
    expect(scrolledTo()).toEqual([box]);
    expect(isRinged(box)).toBe(false);
    expect(errors).not.toHaveBeenCalled();
  });

  test("a new choice cancels the reveal before it, and closing the drawer takes the ring away", async () => {
    const view = await drawer();
    typeQuery("llm");
    const [first, second, third] = [
      "agent:llm_min_interval_ms",
      "agent:llm_retry_base_ms",
      "agent:llm_retry_max",
    ] as const;
    const ringed = () =>
      [...document.querySelectorAll(".is-search-target")].filter(
        (el) => el.closest("[hidden]") === null,
      );

    fireEvent.click(optionOf(first));
    await frames.settle();
    const firstBox = boxOf(first)!;
    expect(ringed()).toEqual([firstBox]);
    // A second choice takes the ring of the first away at once, before its own frame ...
    fireEvent.click(searchInput());
    fireEvent.click(optionOf(second));
    expect(isRinged(firstBox)).toBe(false);
    // ... and a third, before that frame, is the only one that is revealed.
    fireEvent.click(searchInput());
    fireEvent.click(optionOf(third));
    await frames.settle();
    const thirdBox = boxOf(third)!;
    expect(scrolledTo()).toEqual([firstBox, thirdBox]);
    expect(ringed()).toEqual([thirdBox]);
    expect(thirdBox.contains(document.activeElement)).toBe(true);
    // Closing the drawer does not leave its ring behind.
    view.unmount();
    expect(isRinged(thirdBox)).toBe(false);
  });

  test("Enter and Escape while an input method composes leave the search alone", async () => {
    const closeDrawer = await drawerUnderRailScreen();
    act(() => searchInput().focus());
    typeQuery("proxy");
    const composing: KeyboardEventInit[] = [
      { isComposing: true },
      { keyCode: 229 }, // Safari reports the key that ends a composition so
    ];

    for (const init of composing) {
      fireEvent.keyDown(searchInput(), { key: "Enter", ...init });
      expect(screen.getByRole("listbox")).toBeTruthy();
      expect(scrolled).not.toHaveBeenCalledWith(
        expect.objectContaining({ block: "center" }),
      );
      expectAddress("#/settings");

      fireEvent.keyDown(searchInput(), { key: "Escape", ...init });
      expect(searchInput().value).toBe("proxy");
      expect(screen.getByRole("listbox")).toBeTruthy();
      expect(closeDrawer).not.toHaveBeenCalled();
    }
    // The same keys, the composition over, are the search's again.
    pressEscape(searchInput());
    expect(searchInput().value).toBe("");
    expect(closeDrawer).not.toHaveBeenCalled();
    typeQuery("proxy");
    fireEvent.keyDown(searchInput(), { key: "Enter" });
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(window.location.hash).not.toBe("#/settings");
  });

  test("the number found is announced once typing pauses", async () => {
    useClockWithFrames();
    await drawer();
    const status = () =>
      within(screen.getByTestId("settings-search-results")).getByRole("status");

    typeQuery("p");
    typeQuery("pr");
    typeQuery("proxy");
    const count = options().length;
    expect(count).toBeGreaterThan(1);
    expect(status().textContent).toBe("");
    await tick(450);
    expect(status().textContent).toBe("");
    await tick(100);
    expect(status().textContent).toBe(`${count} settings found`);
    // Past the limit the rest is counted in a line of its own; the count announced is of all found.
    typeQuery("e");
    expect(options()).toHaveLength(50);
    const more = Number(
      /^(\d+) more settings match - add a word to narrow the list$/.exec(
        screen.getByTestId("settings-search-more").textContent!,
      )![1],
    );
    expect(more).toBeGreaterThan(1);
    await tick(550);
    expect(status().textContent).toBe(`${50 + more} settings found`);
  });
});
