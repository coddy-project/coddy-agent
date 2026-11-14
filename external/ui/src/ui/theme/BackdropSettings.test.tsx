import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { AppearanceThemePicker } from "./AppearanceModal";
import * as prefs from "./backdropPrefs";
import {
  backdropSnapshot,
  clearWallpaper,
  setWallpaper,
} from "./backdropPrefs";
import { ConfirmProvider } from "../components/useConfirm";
import { initLocale } from "../i18n/i18n";
import { setEnv } from "../env/remoteEnv";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

beforeEach(() => {
  initLocale("en");
  setEnv({ mode: "local" });
  vi.stubGlobal("indexedDB", undefined);
  vi.stubGlobal(
    "createImageBitmap",
    vi.fn(async () => ({ width: 1, height: 1, close() {} })),
  );
  let id = 0;
  Object.assign(URL, {
    createObjectURL: vi.fn(() => `blob:wallpaper-${++id}`),
    revokeObjectURL: vi.fn(),
  });
});
afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  await clearWallpaper();
  vi.restoreAllMocks();
});

function server(initial = ["a.png", "z.png"]) {
  const state = {
    names: [...initial],
    actualName: "uploaded.png",
    created: true,
    bytes: 1,
  };
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    if (String(url).endsWith("/coddy/wallpapers")) {
      return Response.json({
        wallpapers: [...state.names].reverse().map((name) => ({ name })),
      });
    }
    const name = decodeURIComponent(
      String(url).split("/coddy/wallpapers/")[1] ?? "",
    );
    if (method === "POST") {
      if (!state.names.includes(state.actualName))
        state.names.push(state.actualName);
      return Response.json(
        { wallpaper: { name: state.actualName }, created: state.created },
        { status: state.created ? 201 : 200 },
      );
    }
    if (method === "DELETE") {
      state.names = state.names.filter((item) => item !== name);
      return new Response(null, { status: 204 });
    }
    return new Response(new Uint8Array(state.bytes), {
      headers: {
        "Content-Type": "image/png",
        "Content-Length": String(state.bytes),
      },
    });
  });
  vi.stubGlobal("fetch", fetch);
  return {
    state,
    fetch,
    calls: (method: string) =>
      fetch.mock.calls.filter(([, init]) => (init?.method ?? "GET") === method),
  };
}
function mount() {
  return render(
    <ConfirmProvider>
      <AppearanceThemePicker />
    </ConfirmProvider>,
  );
}
async function tile(name = "a.png") {
  const button = await screen.findByTestId(
    `appearance-wallpaper-thumb-${name}`,
  );
  await waitFor(() =>
    expect(button.querySelector("img")).toHaveAttribute(
      "src",
      expect.stringMatching(/^blob:/),
    ),
  );
  return button;
}
function upload(name = "own.png") {
  const file = new File(["picture"], name, { type: "image/png" });
  fireEvent.change(screen.getByTestId("appearance-wallpaper-input"), {
    target: { files: [file] },
  });
  return file;
}
async function select(name = "a.png") {
  fireEvent.click(await tile(name));
  await waitFor(() =>
    expect(
      screen.getByTestId(`appearance-wallpaper-thumb-${name}`),
    ).toHaveAttribute("aria-pressed", "true"),
  );
}
function trash(name = "a.png") {
  fireEvent.click(screen.getByTestId(`appearance-wallpaper-delete-${name}`));
}
async function answer(yes = true, locale = "en") {
  const dialog = await screen.findByRole("dialog");
  fireEvent.click(
    within(dialog).getByRole("button", {
      name: locale === "en" ? (yes ? "Yes" : "No") : yes ? "Да" : "Нет",
    }),
  );
}
function denyStorage() {
  vi.stubGlobal("indexedDB", {
    open: () => {
      throw new Error("Storage denied");
    },
  });
}

describe("Settings → Appearance → Wallpaper", () => {
  test.each(["en", "ru"] as const)(
    "Appearance offers a compact always-visible gallery in %s",
    async (locale) => {
      initLocale(locale);
      const api = server();
      mount();
      expect(
        screen.getByTestId("appearance-wallpaper-grid"),
      ).toBeInTheDocument();
      expect(screen.queryByTestId("appearance-wallpaper-gallery")).toBeNull();
      expect(screen.queryByTestId("appearance-wallpaper-preview")).toBeNull();
      expect(screen.queryByTestId("appearance-smooth-backdrop")).toBeNull();
      const upload = screen.getByTestId("appearance-wallpaper-upload");
      expect(upload.querySelector("svg")).not.toBeNull();
      expect(upload.textContent).toBe("");
      expect(upload).toHaveAccessibleName(
        locale === "en" ? "Upload picture" : "Загрузить картинку",
      );
      await tile();
      expect(api.fetch.mock.calls[0]![0]).toBe("/coddy/wallpapers");
      const theme = document.querySelector(
        '[data-settings-anchor="landmark:appearance:theme"]',
      )!;
      const wallpaper = screen.getByTestId("appearance-wallpaper");
      const language = screen.getByTestId("appearance-language-picker");
      expect(
        theme.compareDocumentPosition(wallpaper) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
      expect(
        wallpaper.compareDocumentPosition(language) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    },
  );
  test("upload selects the actual returned name when no background is set", async () => {
    const api = server([]);
    mount();
    const file = upload();
    await waitFor(() =>
      expect(backdropSnapshot().gallery?.name).toBe("uploaded.png"),
    );
    expect(api.calls("POST")[0]![1]?.body).toBe(file);
    expect(
      screen.getByTestId("appearance-wallpaper-thumb-uploaded.png"),
    ).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("appearance-wallpaper-dim")).toHaveValue("40");
    expect(screen.queryByTestId("appearance-wallpaper-preview")).toBeNull();
  });
  test("upload preserves an existing private background exactly and never uploads it automatically", async () => {
    await setWallpaper(new Blob(["private"], { type: "image/png" }));
    const kept = backdropSnapshot().wallpaper;
    const api = server([]);
    mount();
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-gallery-status"),
      ).toHaveTextContent("No wallpapers"),
    );
    expect(api.calls("POST")).toHaveLength(0);
    upload();
    await tile("uploaded.png");
    expect(backdropSnapshot().wallpaper).toBe(kept);
    expect(backdropSnapshot()).not.toHaveProperty("gallery");
  });
  test("a duplicate upload selects the existing server filename and the grid is sorted", async () => {
    const api = server();
    api.state.actualName = "a.png";
    api.state.created = false;
    mount();
    await tile();
    upload("duplicate.png");
    await waitFor(() => expect(backdropSnapshot().gallery?.name).toBe("a.png"));
    expect(
      within(screen.getByTestId("appearance-wallpaper-grid"))
        .getAllByRole("button", { pressed: false })
        .map((node) => node.getAttribute("title")),
    ).not.toContain("duplicate.png");
    const names = [
      ...document.querySelectorAll(".appearance-wallpaper-thumb"),
    ].map((node) => node.getAttribute("title"));
    expect(names).toEqual(["a.png", "z.png"]);
    expect(api.calls("POST")).toHaveLength(1);
  });
  test("clear background keeps the server file and clears only the browser selection", async () => {
    const api = server();
    mount();
    await select();
    const button = screen.getByTestId("appearance-wallpaper-remove");
    expect(button.textContent).toBe("");
    fireEvent.click(button);
    await waitFor(() => expect(backdropSnapshot().wallpaper).toBeNull());
    expect(api.calls("DELETE")).toHaveLength(0);
    expect(
      screen.getByTestId("appearance-wallpaper-thumb-a.png"),
    ).toBeInTheDocument();
  });
  test("a selected thumbnail is marked without a duplicate preview", async () => {
    server();
    mount();
    await select();
    expect(screen.getByTestId("appearance-wallpaper-thumb-a.png")).toHaveClass(
      "is-selected",
    );
    expect(screen.queryByTestId("appearance-wallpaper-preview")).toBeNull();
    expect(backdropSnapshot()).not.toHaveProperty("credit");
  });
  test("a gallery thumbnail larger than 20 MiB is displayed", async () => {
    const api = server(["large.png"]);
    api.state.bytes = 20 * 1024 * 1024 + 1;
    mount();
    await tile("large.png");
  });
});

describe("wallpaper deletion confirmation", () => {
  test.each(["en", "ru"] as const)(
    "Yes opens focused and No sends no DELETE in %s",
    async (locale) => {
      initLocale(locale);
      const api = server();
      mount();
      const selected = await tile();
      const remove = screen.getByTestId("appearance-wallpaper-delete-a.png");
      expect(selected.contains(remove)).toBe(false);
      expect(selected.parentElement).toBe(remove.parentElement);
      trash();
      const dialog = await screen.findByRole("dialog", {
        name: locale === "en" ? 'Delete "a.png"?' : 'Удалить "a.png"?',
      });
      await waitFor(() =>
        expect(
          within(dialog).getByRole("button", {
            name: locale === "en" ? "Yes" : "Да",
          }),
        ).toHaveFocus(),
      );
      await answer(false, locale);
      expect(api.calls("DELETE")).toHaveLength(0);
    },
  );
  test("Escape dismisses only the confirmation", async () => {
    const api = server();
    mount();
    await tile();
    const behind = vi.fn();
    window.addEventListener("keydown", behind);
    try {
      trash();
      await screen.findByRole("dialog");
      fireEvent.keyDown(document.activeElement!, { key: "Escape" });
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(
        screen.getByTestId("appearance-wallpaper-grid"),
      ).toBeInTheDocument();
      expect(api.calls("DELETE")).toHaveLength(0);
      expect(behind).not.toHaveBeenCalled();
    } finally {
      window.removeEventListener("keydown", behind);
    }
  });
  test.each(["a.png", "z.png"])(
    "confirmed deletion of %s clears only a matching background",
    async (name) => {
      const api = server();
      mount();
      await select();
      const kept = backdropSnapshot().wallpaper;
      trash(name);
      await answer();
      await waitFor(() =>
        expect(
          screen.queryByTestId(`appearance-wallpaper-thumb-${name}`),
        ).toBeNull(),
      );
      expect(api.calls("DELETE")).toHaveLength(1);
      expect(backdropSnapshot().wallpaper).toBe(name === "a.png" ? null : kept);
    },
  );
  test("a filename from another environment is not the active background", async () => {
    setEnv({ mode: "remote", baseUrl: "https://one.test", token: "first" });
    server();
    mount();
    await select();
    const kept = backdropSnapshot().wallpaper;
    act(() =>
      setEnv({ mode: "remote", baseUrl: "https://two.test", token: "second" }),
    );
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-thumb-a.png"),
      ).toHaveAttribute("aria-pressed", "false"),
    );
    trash();
    await answer();
    await waitFor(() =>
      expect(
        screen.queryByTestId("appearance-wallpaper-thumb-a.png"),
      ).toBeNull(),
    );
    expect(backdropSnapshot().wallpaper).toBe(kept);
  });
  test("an environment changed during confirmation never receives the old DELETE", async () => {
    setEnv({ mode: "remote", baseUrl: "https://one.test", token: "first" });
    const api = server();
    mount();
    await tile();
    trash();
    await screen.findByRole("dialog");
    act(() =>
      setEnv({ mode: "remote", baseUrl: "https://two.test", token: "second" }),
    );
    await answer();
    expect(api.calls("DELETE")).toHaveLength(0);
    expect(screen.getByTestId("appearance-wallpaper-grid")).toBeInTheDocument();
  });
  test("server deletion refreshes the grid even when local clear fails", async () => {
    const api = server();
    mount();
    await select();
    const kept = backdropSnapshot().wallpaper;
    denyStorage();
    trash();
    await answer();
    await waitFor(() =>
      expect(
        screen.queryByTestId("appearance-wallpaper-thumb-a.png"),
      ).toBeNull(),
    );
    expect(api.calls("DELETE")).toHaveLength(1);
    expect(backdropSnapshot().wallpaper).toBe(kept);
    expect(screen.getByTestId("appearance-wallpaper-status")).toHaveTextContent(
      "Browser storage",
    );
  });
});

describe("wallpaper lifecycle and failures", () => {
  test.each(["en", "ru"] as const)(
    "initial gallery loading, empty and failed reads are visible in %s",
    async (locale) => {
      initLocale(locale);
      const pending = deferred<Response>();
      vi.stubGlobal(
        "fetch",
        vi.fn(() => pending.promise),
      );
      const view = mount();
      expect(
        screen.getByTestId("appearance-wallpaper-gallery-status"),
      ).toHaveTextContent(locale === "en" ? "Loading" : "Загрузка");
      await act(async () => pending.resolve(Response.json({ wallpapers: [] })));
      expect(
        screen.getByTestId("appearance-wallpaper-gallery-status"),
      ).toHaveTextContent(locale === "en" ? "No wallpapers" : "Нет обоев");
      view.unmount();
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => new Response(null, { status: 500 })),
      );
      mount();
      await waitFor(() =>
        expect(
          screen.getByTestId("appearance-wallpaper-gallery-status"),
        ).toHaveTextContent(locale === "en" ? "could not" : "Не удалось"),
      );
    },
  );
  test("upload storage failure leaves the server file listed without claiming a choice", async () => {
    const api = server([]);
    mount();
    denyStorage();
    upload();
    await tile("uploaded.png");
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-status"),
      ).toHaveTextContent("Browser storage"),
    );
    expect(api.calls("POST")).toHaveLength(1);
    expect(backdropSnapshot().wallpaper).toBeNull();
    expect(
      screen.getByTestId("appearance-wallpaper-thumb-uploaded.png"),
    ).toHaveAttribute("aria-pressed", "false");
  });
  test.each(["unmount", "environment", "clear", "new upload"])(
    "a late selection cannot override %s",
    async (action) => {
      await setWallpaper(new Blob(["kept"], { type: "image/png" }));
      const api = server();
      const view = mount();
      const button = await tile();
      const pending = deferred<Response>();
      api.fetch.mockImplementationOnce(() => pending.promise);
      fireEvent.click(button);
      const signal = api.fetch.mock.calls.at(-1)![1]!.signal!;
      if (action === "unmount") view.unmount();
      if (action === "environment")
        act(() =>
          setEnv({ mode: "remote", baseUrl: "https://two.test", token: "b" }),
        );
      if (action === "clear")
        fireEvent.click(screen.getByTestId("appearance-wallpaper-remove"));
      if (action === "new upload") upload();
      await waitFor(() => expect(signal.aborted).toBe(true));
      const decodes = vi.mocked(createImageBitmap).mock.calls.length;
      await act(async () =>
        pending.resolve(
          new Response(new Uint8Array([2]), {
            headers: { "Content-Type": "image/png" },
          }),
        ),
      );
      expect(vi.mocked(createImageBitmap).mock.calls).toHaveLength(decodes);
      expect(backdropSnapshot().gallery?.name).not.toBe("a.png");
    },
  );
  test("unmount aborts a pending initial listing and ignores its late answer", async () => {
    const pending = deferred<Response>();
    const fetch = vi.fn(
      (_url: RequestInfo | URL, _init?: RequestInit) => pending.promise,
    );
    vi.stubGlobal("fetch", fetch);
    const view = mount();
    const signal = fetch.mock.calls[0]![1]!.signal!;
    view.unmount();
    expect(signal.aborted).toBe(true);
    await act(async () =>
      pending.resolve(Response.json({ wallpapers: [{ name: "late.png" }] })),
    );
    expect(
      screen.queryByTestId("appearance-wallpaper-thumb-late.png"),
    ).toBeNull();
  });
  test("remote uploads, thumbnails, selections and deletes use header authentication", async () => {
    setEnv({
      mode: "remote",
      baseUrl: "https://relay.test/node",
      token: "private",
    });
    const api = server();
    mount();
    await select();
    upload();
    await tile("uploaded.png");
    trash("uploaded.png");
    await answer();
    await waitFor(() => expect(api.calls("DELETE")).toHaveLength(1));
    for (const [url, init] of api.fetch.mock.calls) {
      expect(String(url)).toMatch(
        /^https:\/\/relay.test\/node\/coddy\/wallpapers/,
      );
      expect(String(url)).not.toContain("private");
      expect(new Headers(init?.headers).get("Authorization")).toBe(
        "Bearer private",
      );
    }
  });
});

describe("confirmed gallery mutations survive a failed refresh", () => {
  test("an uploaded file remains listed when refresh and local storage fail", async () => {
    const api = server([]);
    const serve = api.fetch.getMockImplementation()!;
    let uploaded = false;
    api.fetch.mockImplementation(async (url, init) => {
      if (uploaded && String(url).endsWith("/coddy/wallpapers"))
        return new Response(null, { status: 500 });
      const result = await serve(url, init);
      if (init?.method === "POST") uploaded = true;
      return result;
    });
    mount();
    denyStorage();
    upload();
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-status"),
      ).toHaveTextContent("Browser storage"),
    );
    expect(
      screen.getByTestId("appearance-wallpaper-thumb-uploaded.png"),
    ).toHaveAttribute("aria-pressed", "false");
    expect(
      screen.getByTestId("appearance-wallpaper-gallery-status"),
    ).toHaveTextContent("could not");
    expect(backdropSnapshot().wallpaper).toBeNull();
  });
  test("a deleted file stays removed when the following list read fails", async () => {
    const api = server();
    const serve = api.fetch.getMockImplementation()!;
    let deleted = false;
    api.fetch.mockImplementation(async (url, init) => {
      if (deleted && String(url).endsWith("/coddy/wallpapers"))
        return new Response(null, { status: 500 });
      const result = await serve(url, init);
      if (init?.method === "DELETE") deleted = true;
      return result;
    });
    mount();
    await select();
    trash();
    await answer();
    await waitFor(() => expect(backdropSnapshot().wallpaper).toBeNull());
    expect(screen.queryByTestId("appearance-wallpaper-thumb-a.png")).toBeNull();
    expect(
      screen.getByTestId("appearance-wallpaper-gallery-status"),
    ).toHaveTextContent("could not");
  });
});

describe("wallpaper mutation request cancellation", () => {
  test.each(["unmount", "environment", "clear", "new upload"])(
    "a late upload cannot override %s",
    async (action) => {
      const api = server();
      const serve = api.fetch.getMockImplementation()!;
      const pending = deferred<Response>();
      let held = false;
      api.fetch.mockImplementation((url, init) => {
        if (init?.method === "POST" && !held) {
          held = true;
          return pending.promise;
        }
        return serve(url, init);
      });
      const view = mount();
      await tile();
      upload("old.png");
      const signal = api.calls("POST")[0]![1]!.signal!;
      if (action === "unmount") view.unmount();
      if (action === "environment")
        act(() =>
          setEnv({
            mode: "remote",
            baseUrl: "https://other.test",
            token: "new",
          }),
        );
      if (action === "clear")
        fireEvent.click(screen.getByTestId("appearance-wallpaper-remove"));
      if (action === "new upload") {
        upload("new.png");
        await waitFor(() =>
          expect(backdropSnapshot().gallery?.name).toBe("uploaded.png"),
        );
      }
      await waitFor(() => expect(signal.aborted).toBe(true));
      const kept = backdropSnapshot().wallpaper;
      await act(async () =>
        pending.resolve(
          Response.json(
            { wallpaper: { name: "late.png" }, created: true },
            { status: 201 },
          ),
        ),
      );
      expect(backdropSnapshot().wallpaper).toBe(kept);
      expect(
        screen.queryByTestId("appearance-wallpaper-thumb-late.png"),
      ).toBeNull();
    },
  );
  test("a confirmed DELETE stays bound to its original environment across a switch", async () => {
    setEnv({ mode: "remote", baseUrl: "https://one.test", token: "first" });
    const api = server();
    const serve = api.fetch.getMockImplementation()!;
    const pending = deferred<Response>();
    api.fetch.mockImplementation((url, init) =>
      init?.method === "DELETE" ? pending.promise : serve(url, init),
    );
    mount();
    await select();
    const kept = backdropSnapshot().wallpaper;
    trash();
    await answer();
    await waitFor(() => expect(api.calls("DELETE")).toHaveLength(1));
    const signal = api.calls("DELETE")[0]![1]!.signal!;
    act(() =>
      setEnv({ mode: "remote", baseUrl: "https://two.test", token: "second" }),
    );
    expect(signal?.aborted).not.toBe(true);
    await act(async () => pending.resolve(new Response(null, { status: 204 })));
    await waitFor(() => expect(backdropSnapshot().wallpaper).toBeNull());
    expect(kept).not.toBeNull();
  });
  test.each(["POST", "DELETE"])(
    "a refused %s preserves the background and reports the error",
    async (method) => {
      const api = server();
      const serve = api.fetch.getMockImplementation()!;
      api.fetch.mockImplementation((url, init) =>
        init?.method === method
          ? Promise.resolve(new Response(null, { status: 500 }))
          : serve(url, init),
      );
      mount();
      await select();
      const kept = backdropSnapshot().wallpaper;
      if (method === "POST") upload();
      else {
        trash();
        await answer();
      }
      await waitFor(() =>
        expect(
          screen.getByTestId("appearance-wallpaper-status"),
        ).toHaveTextContent(
          method === "POST" ? "could not be uploaded" : "could not be deleted",
        ),
      );
      expect(backdropSnapshot().wallpaper).toBe(kept);
      expect(
        screen.getByTestId("appearance-wallpaper-thumb-a.png"),
      ).toBeInTheDocument();
    },
  );
});

test("gallery captions expose filenames once while delete stays separate", async () => {
  server(["a long filename.png"]);
  mount();
  const button = await tile("a long filename.png");
  const wrapper = screen.getByTestId(
    "appearance-wallpaper-tile-a long filename.png",
  );
  expect(within(wrapper).getByText("a long filename.png")).toBeVisible();
  expect(button).toHaveAccessibleName("a long filename.png");
  expect(button).toHaveAttribute("title", "a long filename.png");
  expect(button.querySelector("img")).toHaveAttribute("alt", "");
  expect(
    button.contains(
      screen.getByTestId("appearance-wallpaper-delete-a long filename.png"),
    ),
  ).toBe(false);
});

test("a delete control is a plain cross, not an icon plate", async () => {
  server();
  mount();
  await tile();
  const remove = screen.getByTestId("appearance-wallpaper-delete-a.png");
  expect(remove.textContent).toBe("×");
  expect(remove.querySelector("svg")).toBeNull();
  expect(remove).toHaveAccessibleName("Delete a.png");
});

test("offscreen thumbnails fetch only after intersection and abort on unmount", async () => {
  const observers: Array<{
    callback: IntersectionObserverCallback;
    disconnect: ReturnType<typeof vi.fn>;
    observe: ReturnType<typeof vi.fn>;
  }> = [];
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      callback: IntersectionObserverCallback;
      disconnect = vi.fn();
      observe = vi.fn();
      unobserve = vi.fn();
      constructor(callback: IntersectionObserverCallback) {
        this.callback = callback;
        observers.push(this);
      }
    },
  );
  const api = server(["a.png", "z.png"]);
  const response = deferred<Response>();
  const serve = api.fetch.getMockImplementation()!;
  api.fetch.mockImplementation((url, init) =>
    String(url).endsWith("/a.png") ? response.promise : serve(url, init),
  );
  const view = mount();
  await screen.findByTestId("appearance-wallpaper-thumb-a.png");
  await waitFor(() => expect(observers).toHaveLength(2));
  expect(
    api.fetch.mock.calls.filter(([url]) =>
      String(url).includes("/wallpapers/"),
    ),
  ).toHaveLength(0);
  act(() =>
    observers[0]!.callback(
      [{ isIntersecting: true } as IntersectionObserverEntry],
      {} as IntersectionObserver,
    ),
  );
  await waitFor(() =>
    expect(
      api.fetch.mock.calls.filter(([url]) => String(url).endsWith("/a.png")),
    ).toHaveLength(1),
  );
  const signal = api.fetch.mock.calls.find(([url]) =>
    String(url).endsWith("/a.png"),
  )![1]!.signal!;
  expect(
    api.fetch.mock.calls.some(([url]) => String(url).endsWith("/z.png")),
  ).toBe(false);
  expect(URL.createObjectURL).not.toHaveBeenCalled();
  view.unmount();
  expect(signal.aborted).toBe(true);
  expect(
    observers.every((observer) => observer.disconnect.mock.calls.length > 0),
  ).toBe(true);
  await act(async () =>
    response.resolve(
      new Response(new Uint8Array([1]), {
        headers: { "Content-Type": "image/png" },
      }),
    ),
  );
  expect(URL.createObjectURL).not.toHaveBeenCalled();
});

describe("upload waits for wallpaper bootstrap", () => {
  test.each(["saved", "empty", "failed", "superseded"])(
    "a pending %s startup settles before auto-selection",
    async (outcome) => {
      const ready = deferred<void>();
      const load = vi
        .spyOn(prefs, "loadWallpaper")
        .mockReturnValue(ready.promise);
      const api = server([]);
      mount();
      upload();
      await waitFor(() => expect(api.calls("POST")).toHaveLength(1));
      await waitFor(() =>
        expect(
          screen.queryByTestId("appearance-wallpaper-thumb-uploaded.png"),
        ).not.toBeNull(),
      );
      expect(load).toHaveBeenCalled();
      expect(backdropSnapshot().wallpaper).toBeNull();
      if (outcome === "saved" || outcome === "superseded") {
        await act(async () => {
          await setWallpaper(new Blob(["saved"], { type: "image/png" }));
        });
      }
      if (outcome === "superseded")
        fireEvent.click(screen.getByTestId("appearance-wallpaper-remove"));
      if (outcome === "failed") {
        const failed = {
          ...prefs.backdropSnapshot(),
          storageError: new Error("read failed"),
        };
        vi.spyOn(prefs, "backdropSnapshot").mockReturnValue(failed);
      }
      const kept = backdropSnapshot().wallpaper;
      await act(async () => ready.resolve());
      if (outcome === "empty")
        await waitFor(() =>
          expect(backdropSnapshot().gallery?.name).toBe("uploaded.png"),
        );
      else
        expect(backdropSnapshot().wallpaper).toBe(
          outcome === "superseded" ? null : kept,
        );
      expect(backdropSnapshot().gallery?.name).not.toBe(
        outcome === "empty" ? "different" : "uploaded.png",
      );
    },
  );
});

test("confirmed deletion clears the selected background before a delayed refresh or unmount", async () => {
  const api = server();
  const serve = api.fetch.getMockImplementation()!;
  const pending = deferred<Response>();
  let deleted = false;
  api.fetch.mockImplementation(async (url, init) => {
    if (deleted && String(url).endsWith("/coddy/wallpapers"))
      return pending.promise;
    const response = await serve(url, init);
    if (init?.method === "DELETE") deleted = true;
    return response;
  });
  const view = mount();
  await select();
  trash();
  await answer();
  await waitFor(() => expect(api.calls("DELETE")).toHaveLength(1));
  await waitFor(() => expect(backdropSnapshot().wallpaper).toBeNull());
  view.unmount();
  await act(async () => pending.resolve(Response.json({ wallpapers: [] })));
  expect(backdropSnapshot().wallpaper).toBeNull();
});

test("a later selection survives a pending server deletion", async () => {
  const api = server();
  const serve = api.fetch.getMockImplementation()!;
  const pending = deferred<Response>();
  api.fetch.mockImplementation((url, init) =>
    init?.method === "DELETE" ? pending.promise : serve(url, init),
  );
  mount();
  await select("a.png");
  trash("a.png");
  await answer();
  await waitFor(() => expect(api.calls("DELETE")).toHaveLength(1));
  await select("z.png");
  const kept = backdropSnapshot().wallpaper;
  await act(async () => pending.resolve(new Response(null, { status: 204 })));
  expect(backdropSnapshot().wallpaper).toBe(kept);
  expect(backdropSnapshot().gallery?.name).toBe("z.png");
});

test("confirmed DELETE survives panel unmount before its response", async () => {
  const api = server();
  const serve = api.fetch.getMockImplementation()!;
  const pending = deferred<Response>();
  api.fetch.mockImplementation((url, init) =>
    init?.method === "DELETE" ? pending.promise : serve(url, init),
  );
  const view = mount();
  await select();
  trash();
  await answer();
  await waitFor(() => expect(api.calls("DELETE")).toHaveLength(1));
  view.unmount();
  await act(async () => pending.resolve(new Response(null, { status: 204 })));
  await waitFor(() => expect(backdropSnapshot().wallpaper).toBeNull());
});

test("a selection still decoding wins over a confirmed pending DELETE", async () => {
  const api = server();
  const serve = api.fetch.getMockImplementation()!;
  const pending = deferred<Response>();
  api.fetch.mockImplementation((url, init) =>
    init?.method === "DELETE" ? pending.promise : serve(url, init),
  );
  mount();
  await select();
  await tile("z.png");
  trash();
  await answer();
  await waitFor(() => expect(api.calls("DELETE")).toHaveLength(1));
  const decoding = deferred<{
    width: number;
    height: number;
    close: () => void;
  }>();
  vi.mocked(createImageBitmap).mockReturnValueOnce(
    decoding.promise as Promise<ImageBitmap>,
  );
  fireEvent.click(screen.getByTestId("appearance-wallpaper-thumb-z.png"));
  await waitFor(() => expect(createImageBitmap).toHaveBeenCalledTimes(2));
  await act(async () => pending.resolve(new Response(null, { status: 204 })));
  await act(async () => decoding.resolve({ width: 1, height: 1, close() {} }));
  await waitFor(() => expect(backdropSnapshot().gallery?.name).toBe("z.png"));
});

test("closing the panel does not abort a confirmed delete's pending storage transaction", async () => {
  server();
  const view = mount();
  await select();
  let tx:
    | {
        oncomplete?: () => void;
        onabort?: () => void;
        abort: ReturnType<typeof vi.fn>;
        objectStore: () => unknown;
      }
    | undefined;
  vi.stubGlobal("indexedDB", {
    open: () => {
      const request: { result?: unknown; onsuccess?: () => void } = {};
      queueMicrotask(() => {
        request.result = {
          close() {},
          transaction: () => {
            tx = {
              abort: vi.fn(() => tx?.onabort?.()),
              objectStore: () => ({ delete: () => ({}) }),
            };
            return tx;
          },
        };
        request.onsuccess?.();
      });
      return request;
    },
  });
  trash();
  await answer();
  await waitFor(() => expect(tx).toBeDefined());
  view.unmount();
  expect(tx!.abort).not.toHaveBeenCalled();
  await act(async () => tx!.oncomplete?.());
  expect(backdropSnapshot().wallpaper).toBeNull();
});

test.each(["failure", "success"])(
  "a newer selection's pending GET %s keeps the previous choice until it resolves",
  async (outcome) => {
    const api = server();
    const serve = api.fetch.getMockImplementation()!;
    const del = deferred<Response>();
    const get = deferred<Response>();
    let hold = false;
    api.fetch.mockImplementation((url, init) => {
      if (init?.method === "DELETE") return del.promise;
      if (hold && String(url).endsWith("/z.png")) return get.promise;
      return serve(url, init);
    });
    mount();
    await select();
    await tile("z.png");
    const kept = backdropSnapshot().wallpaper;
    trash();
    await answer();
    await waitFor(() => expect(api.calls("DELETE")).toHaveLength(1));
    hold = true;
    fireEvent.click(screen.getByTestId("appearance-wallpaper-thumb-z.png"));
    api.state.names = api.state.names.filter((name) => name !== "a.png");
    await act(async () => del.resolve(new Response(null, { status: 204 })));
    await waitFor(() =>
      expect(
        screen.queryByTestId("appearance-wallpaper-thumb-a.png"),
      ).toBeNull(),
    );
    expect(backdropSnapshot().wallpaper).toBe(kept);
    await act(async () =>
      get.resolve(
        outcome === "failure"
          ? new Response(null, { status: 500 })
          : new Response(new Uint8Array([1]), {
              headers: { "Content-Type": "image/png" },
            }),
      ),
    );
    if (outcome === "success")
      await waitFor(() =>
        expect(backdropSnapshot().gallery?.name).toBe("z.png"),
      );
    else expect(backdropSnapshot().wallpaper).toBe(kept);
  },
);

test("optimistically uploaded tile removes empty status even if selection aborts refresh", async () => {
  const api = server([]);
  const serve = api.fetch.getMockImplementation()!;
  const listing = deferred<Response>();
  let uploaded = false;
  api.fetch.mockImplementation(async (url, init) => {
    if (uploaded && String(url).endsWith("/coddy/wallpapers"))
      return listing.promise;
    const response = await serve(url, init);
    if (init?.method === "POST") uploaded = true;
    return response;
  });
  mount();
  await waitFor(() =>
    expect(
      screen.getByTestId("appearance-wallpaper-gallery-status"),
    ).toHaveTextContent("No wallpapers"),
  );
  upload();
  const button = await tile("uploaded.png");
  fireEvent.click(button);
  await waitFor(() =>
    expect(backdropSnapshot().gallery?.name).toBe("uploaded.png"),
  );
  expect(
    screen.queryByTestId("appearance-wallpaper-gallery-status"),
  ).toBeNull();
  await act(async () =>
    listing.resolve(Response.json({ wallpapers: [{ name: "uploaded.png" }] })),
  );
});

test("a newer pending thumbnail GET protects the old selection when DELETE completes", async () => {
  const api = server();
  const serve = api.fetch.getMockImplementation()!;
  const del = deferred<Response>();
  const get = deferred<Response>();
  let hold = false;
  api.fetch.mockImplementation((url, init) =>
    init?.method === "DELETE"
      ? del.promise
      : hold && String(url).endsWith("/z.png")
        ? get.promise
        : serve(url, init),
  );
  mount();
  await select();
  await tile("z.png");
  const kept = backdropSnapshot().wallpaper;
  trash();
  await answer();
  await waitFor(() => expect(api.calls("DELETE")).toHaveLength(1));
  hold = true;
  fireEvent.click(screen.getByTestId("appearance-wallpaper-thumb-z.png"));
  await act(async () => del.resolve(new Response(null, { status: 204 })));
  expect(backdropSnapshot().wallpaper).toBe(kept);
  await act(async () => get.resolve(new Response(null, { status: 500 })));
});

test.each(["selection", "upload"])(
  "cancelling delete preserves an in-flight %s",
  async (kind) => {
    const api = server();
    const serve = api.fetch.getMockImplementation()!;
    const pending = deferred<Response>();
    let hold = false;
    api.fetch.mockImplementation((url, init) => {
      if (
        hold &&
        ((kind === "selection" && String(url).endsWith("/z.png")) ||
          (kind === "upload" && init?.method === "POST"))
      )
        return pending.promise;
      return serve(url, init);
    });
    mount();
    await tile("a.png");
    await tile("z.png");
    hold = true;
    if (kind === "selection")
      fireEvent.click(screen.getByTestId("appearance-wallpaper-thumb-z.png"));
    else upload();
    const signal = api.fetch.mock.calls.at(-1)![1]!.signal!;
    trash("a.png");
    await answer(false);
    expect(api.calls("DELETE")).toHaveLength(0);
    expect(signal.aborted).toBe(false);
    await act(async () =>
      pending.resolve(
        kind === "selection"
          ? new Response(new Uint8Array([1]), {
              headers: { "Content-Type": "image/png" },
            })
          : Response.json(
              { wallpaper: { name: "uploaded.png" }, created: true },
              { status: 201 },
            ),
      ),
    );
    await waitFor(() =>
      expect(backdropSnapshot().gallery?.name).toBe(
        kind === "selection" ? "z.png" : "uploaded.png",
      ),
    );
  },
);

test("deleting the last tile shows the empty state before a delayed refresh", async () => {
  const api = server(["a.png"]);
  const serve = api.fetch.getMockImplementation()!;
  const pending = deferred<Response>();
  let deleted = false;
  api.fetch.mockImplementation(async (url, init) => {
    if (deleted && String(url).endsWith("/coddy/wallpapers"))
      return pending.promise;
    const result = await serve(url, init);
    if (init?.method === "DELETE") deleted = true;
    return result;
  });
  mount();
  await tile();
  trash();
  await answer();
  await waitFor(() =>
    expect(screen.queryByTestId("appearance-wallpaper-thumb-a.png")).toBeNull(),
  );
  expect(
    screen.getByTestId("appearance-wallpaper-gallery-status"),
  ).toHaveTextContent("No wallpapers");
  await act(async () => pending.resolve(Response.json({ wallpapers: [] })));
});

test("a new selection made while delete confirmation is open prevents the delete", async () => {
  const api = server();
  mount();
  await tile("a.png");
  await tile("z.png");
  trash("a.png");
  await screen.findByRole("dialog");
  fireEvent.click(screen.getByTestId("appearance-wallpaper-thumb-z.png"));
  await answer();
  expect(api.calls("DELETE")).toHaveLength(0);
  await waitFor(() => expect(backdropSnapshot().gallery?.name).toBe("z.png"));
});

test("closing the view while delete confirmation is open sends no delete", async () => {
  const api = server();
  const view = mount();
  await tile();
  trash();
  await screen.findByRole("dialog");
  view.unmount();
  expect(api.calls("DELETE")).toHaveLength(0);
});
