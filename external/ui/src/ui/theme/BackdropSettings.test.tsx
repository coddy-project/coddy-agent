import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { AppearanceThemePicker } from "./AppearanceModal";
import {
  backdropSnapshot,
  clearWallpaper,
  setSmoothBackdrop,
  SMOOTH_BACKDROP_COOKIE,
} from "./backdropPrefs";
import { initLocale } from "../i18n/i18n";
import { connectRemote, setEnv } from "../env/remoteEnv";

beforeEach(() => {
  initLocale("en");
  setEnv({ mode: "local" });
});

afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  await clearWallpaper();
  setSmoothBackdrop(false);
  document.cookie = `${SMOOTH_BACKDROP_COOKIE}=; Path=/; Max-Age=0`;
});

function theSwitch(): HTMLElement {
  return screen.getByTestId("appearance-smooth-backdrop-switch");
}

describe("Settings → Appearance → Gradients", () => {
  test("the smooth gradients switch stands under the notifications, off", () => {
    render(<AppearanceThemePicker />);
    const notifications = screen.getByTestId("appearance-notifications");
    const gradients = screen.getByTestId("appearance-smooth-backdrop");
    expect(
      notifications.compareDocumentPosition(gradients) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.getByText("Gradients")).toBeInTheDocument();
    expect(theSwitch()).not.toBeChecked();
  });

  test("turning it on is remembered in this browser", () => {
    render(<AppearanceThemePicker />);
    fireEvent.click(theSwitch());
    expect(theSwitch()).toBeChecked();
    expect(backdropSnapshot().smooth).toBe(true);
    expect(document.cookie).toContain(`${SMOOTH_BACKDROP_COOKIE}=on`);
    fireEvent.click(theSwitch());
    expect(theSwitch()).not.toBeChecked();
    expect(document.cookie).not.toContain(SMOOTH_BACKDROP_COOKIE);
  });
});

describe("Settings → Appearance → Wallpaper", () => {
  function choose(width: number, height: number) {
    vi.stubGlobal("createImageBitmap", async () => ({
      width,
      height,
      close() {},
    }));
    // jsdom has no object URLs
    Object.assign(URL, {
      createObjectURL: vi.fn(() => "blob:picture"),
      revokeObjectURL: vi.fn(),
    });
    const file = new File(["x"], "night.png", { type: "image/png" });
    fireEvent.change(screen.getByTestId("appearance-wallpaper-input"), {
      target: { files: [file] },
    });
  }

  test("a chosen picture shows its preview, the dimming and Remove", async () => {
    render(<AppearanceThemePicker />);
    const gradients = screen.getByTestId("appearance-smooth-backdrop");
    const wallpaper = screen.getByTestId("appearance-wallpaper");
    expect(
      gradients.compareDocumentPosition(wallpaper) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.queryByTestId("appearance-wallpaper-remove")).toBeNull();
    choose(1920, 1080);
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-preview"),
      ).toHaveAttribute("src", "blob:picture"),
    );
    expect(screen.getByTestId("appearance-wallpaper-dim")).toHaveValue("40");
    fireEvent.click(screen.getByTestId("appearance-wallpaper-remove"));
    await waitFor(() =>
      expect(screen.queryByTestId("appearance-wallpaper-preview")).toBeNull(),
    );
  });

  test("a picture under 1280x720 is refused and the message names its size", async () => {
    render(<AppearanceThemePicker />);
    choose(800, 600);
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-status"),
      ).toHaveTextContent(
        "The picture is 800x600: a wallpaper needs at least 1280x720.",
      ),
    );
    expect(backdropSnapshot().wallpaper).toBeNull();
  });
});

describe("Settings → Appearance → Wallpaper → Gallery", () => {
  const name = "night sky #1.png";
  const imagePath = `/coddy/wallpapers/${encodeURIComponent(name)}`;
  function pictures() {
    vi.stubGlobal("createImageBitmap", async () => ({
      width: 1920,
      height: 1080,
      close() {},
    }));
    Object.assign(URL, {
      createObjectURL: vi.fn(() => "blob:photo"),
      revokeObjectURL: vi.fn(),
    });
  }
  function server(names = [name]) {
    const fetch = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) =>
      String(_url).endsWith("/coddy/wallpapers")
        ? Response.json({ wallpapers: names.map((name) => ({ name })) })
        : new Response(new Uint8Array([1]), {
            headers: { "Content-Type": "image/png" },
          }),
    );
    vi.stubGlobal("fetch", fetch);
    return fetch;
  }
  async function open() {
    fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
    return screen.findByRole("button", { name });
  }
  test("nothing is requested from the server gallery until it is opened", async () => {
    const fetch = server();
    render(<AppearanceThemePicker />);
    expect(fetch).not.toHaveBeenCalled();
    await open();
    expect(fetch.mock.calls[0]?.[0]).toBe("/coddy/wallpapers");
    expect(
      screen.getByRole("button", { name }).querySelector("img"),
    ).toBeInTheDocument();
    expect(screen.getByText(/CODDY_HOME\/wallpapers/)).toHaveTextContent(
      "~/.coddy/wallpapers",
    );
    expect(screen.queryByTestId("appearance-wallpaper-shuffle")).toBeNull();
    expect(document.querySelector('img[src^="https://"]')).toBeNull();
  });
  test("a picked server picture becomes the browser wallpaper without credits", async () => {
    pictures();
    const fetch = server();
    render(<AppearanceThemePicker />);
    fireEvent.click(await open());
    await waitFor(() =>
      expect(backdropSnapshot().wallpaper).toBe("blob:photo"),
    );
    expect(fetch.mock.calls.some(([url]) => String(url) === imagePath)).toBe(
      true,
    );
    expect(screen.queryByTestId("appearance-wallpaper-credit")).toBeNull();
    expect(
      screen.getByTestId("appearance-wallpaper-remove"),
    ).toBeInTheDocument();
  });
  test("remote thumbnails and selections carry authentication only in headers", async () => {
    pictures();
    setEnv({
      mode: "remote",
      baseUrl: "https://relay.test/node",
      token: "private",
    });
    const fetch = server();
    render(<AppearanceThemePicker />);
    const button = await open();
    await waitFor(() =>
      expect(button.querySelector("img")).toHaveAttribute("src", "blob:photo"),
    );
    fireEvent.click(button);
    await waitFor(() =>
      expect(backdropSnapshot().wallpaper).toBe("blob:photo"),
    );
    for (const [url, init] of fetch.mock.calls) {
      expect(String(url)).toMatch(
        /^https:\/\/relay\.test\/node\/coddy\/wallpapers/,
      );
      expect(String(url)).not.toContain("private");
      expect(new Headers(init?.headers).get("Authorization")).toBe(
        "Bearer private",
      );
    }
  });
  test.each(["en", "ru"] as const)(
    "loading, empty and error states are visible in %s",
    async (locale) => {
      initLocale(locale);
      let resolve!: (response: Response) => void;
      vi.stubGlobal(
        "fetch",
        vi.fn(
          () =>
            new Promise<Response>((done) => {
              resolve = done;
            }),
        ),
      );
      render(<AppearanceThemePicker />);
      fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
      expect(
        screen.getByTestId("appearance-wallpaper-gallery-status"),
      ).toHaveTextContent(locale === "en" ? "Loading" : "Загрузка");
      await act(async () => resolve(Response.json({ wallpapers: [] })));
      expect(
        screen.getByTestId("appearance-wallpaper-gallery-status"),
      ).toHaveTextContent(locale === "en" ? "No wallpapers" : "Нет обоев");
      fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
      fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
      await act(async () => resolve(new Response(null, { status: 500 })));
      expect(
        screen.getByTestId("appearance-wallpaper-gallery-status"),
      ).toHaveTextContent(locale === "en" ? "could not" : "Не удалось");
    },
  );
  test.each(["unmount", "environment", "upload", "remove"])(
    "a late gallery selection cannot override %s",
    async (action) => {
      pictures();
      const fetch = server();
      const view = render(<AppearanceThemePicker />);
      const button = await open();
      let resolve!: (response: Response) => void;
      fetch.mockImplementationOnce(
        () =>
          new Promise<Response>((done) => {
            resolve = done;
          }),
      );
      fireEvent.click(button);
      const signal = fetch.mock.calls.at(-1)![1]!.signal!;
      if (action === "unmount") view.unmount();
      if (action === "environment") {
        setEnv({ mode: "remote", baseUrl: "https://one.test", token: "a" });
        act(() => connectRemote("https://two.test", "b"));
      }
      if (action === "upload" || action === "remove") {
        fireEvent.change(screen.getByTestId("appearance-wallpaper-input"), {
          target: {
            files: [new File(["upload"], "own.png", { type: "image/png" })],
          },
        });
        await waitFor(() =>
          expect(backdropSnapshot().wallpaper).toBe("blob:photo"),
        );
        if (action === "remove") {
          fireEvent.click(screen.getByTestId("appearance-wallpaper-remove"));
          await waitFor(() => expect(backdropSnapshot().wallpaper).toBeNull());
        }
      }
      expect(signal.aborted).toBe(true);
      const calls = vi.mocked(URL.createObjectURL).mock.calls.length;
      await act(async () =>
        resolve(
          new Response(new Uint8Array([2]), {
            headers: { "Content-Type": "image/png" },
          }),
        ),
      );
      expect(vi.mocked(URL.createObjectURL).mock.calls).toHaveLength(calls);
    },
  );
  test("a selection that fails reports the localized picture error", async () => {
    pictures();
    const fetch = server();
    render(<AppearanceThemePicker />);
    const button = await open();
    fetch.mockResolvedValueOnce(new Response(null, { status: 500 }));
    fireEvent.click(button);
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-status"),
      ).toHaveTextContent("The picture could not be loaded."),
    );
    expect(backdropSnapshot().wallpaper).toBeNull();
  });

  test("Remove cancels a gallery picture still being decoded", async () => {
    pictures();
    server();
    render(<AppearanceThemePicker />);
    fireEvent.change(screen.getByTestId("appearance-wallpaper-input"), {
      target: { files: [new File(["own"], "own.png", { type: "image/png" })] },
    });
    await waitFor(() =>
      expect(backdropSnapshot().wallpaper).toBe("blob:photo"),
    );
    const button = await open();
    let resolve!: (bitmap: {
      width: number;
      height: number;
      close: () => void;
    }) => void;
    const decode = vi.fn(
      () =>
        new Promise<{ width: number; height: number; close: () => void }>(
          (done) => {
            resolve = done;
          },
        ),
    );
    vi.stubGlobal("createImageBitmap", decode);
    fireEvent.click(button);
    await waitFor(() => expect(decode).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("appearance-wallpaper-remove"));
    await waitFor(() => expect(backdropSnapshot().wallpaper).toBeNull());
    const calls = vi.mocked(URL.createObjectURL).mock.calls.length;
    await act(async () => resolve({ width: 1920, height: 1080, close() {} }));
    expect(backdropSnapshot().wallpaper).toBeNull();
    expect(vi.mocked(URL.createObjectURL).mock.calls).toHaveLength(calls);
  });

  test("closing the gallery aborts a pending list and drops its late answer", async () => {
    let resolve!: (response: Response) => void;
    const fetch = vi.fn(
      (_url: RequestInfo | URL, _init?: RequestInit) =>
        new Promise<Response>((done) => {
          resolve = done;
        }),
    );
    vi.stubGlobal("fetch", fetch);
    render(<AppearanceThemePicker />);
    fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
    const signal = fetch.mock.calls[0]![1]!.signal!;
    fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
    expect(signal.aborted).toBe(true);
    await act(async () => resolve(Response.json({ wallpapers: [{ name }] })));
    expect(screen.queryByRole("button", { name })).toBeNull();
  });
});
