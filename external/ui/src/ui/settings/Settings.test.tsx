import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { useEffect, useLayoutEffect, useState } from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { Settings } from "./Settings";
import {
  noteSettingsConfigReloaded,
  resetSettingsConfigForTests,
} from "./settingsConfigStore";
import { parseAppHash } from "../scheduler/hashRoute";
import { initLocale } from "../i18n/i18n";

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

type ServerState = {
  config: Record<string, unknown>;
  /** What PUT /coddy/config answers; ok unless a test says otherwise. */
  put: { status: number; body: Record<string, unknown> };
};

let server: ServerState;

/** A coddy serve behind fetch: the schema, the config document, a save. */
function stubServer() {
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    const reply = (status: number, body: unknown) => ({
      ok: status >= 200 && status < 300,
      status,
      json: async () => structuredClone(body),
    });
    if (path === "/coddy/config/schema") return reply(200, schema);
    if (path === "/coddy/config" && method === "GET") {
      return reply(200, server.config);
    }
    if (path === "/coddy/config/validate") return reply(200, { ok: true });
    if (path === "/coddy/config" && method === "PUT") {
      if (server.put.status === 200) {
        server.config = JSON.parse(String(init?.body)) as Record<
          string,
          unknown
        >;
      }
      return reply(server.put.status, server.put.body);
    }
    return reply(200, { ok: true, models: [] });
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
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
  const first = render(<Settings onClose={() => {}} initialSection="providers" />);
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
  expect(
    (screen.getByLabelText("Max turns") as HTMLInputElement).value,
  ).toBe("70");
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
        skeleton: document.querySelector('[data-testid="settings-skeleton"]') !== null,
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
  await waitFor(() => expect(server.config).toMatchObject({ agent: { max_turns: 45 } }));
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
  server.config = { ...server.config, providers: [{ name: "demo" }, { name: "spare" }] };
  window.location.hash = "#/settings/providers?id=spare";
  render(<RoutedSettings />);
  await waitFor(() =>
    expect((screen.getByLabelText("Provider id") as HTMLInputElement).value).toBe(
      "spare",
    ),
  );

  server.config = { ...server.config, providers: [{ name: "spare" }, { name: "demo" }] };
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
