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
  setWallpaper,
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
});

test.each(["en", "ru"] as const)(
  "Appearance offers wallpaper without a gradient control in %s",
  (locale) => {
    initLocale(locale);
    render(<AppearanceThemePicker />);
    expect(screen.queryByTestId("appearance-smooth-backdrop")).toBeNull();
    expect(
      screen.queryByRole("switch", {
        name: /Smooth gradients|Плавные градиенты/,
      }),
    ).toBeNull();
    expect(
      screen.getByTestId("appearance-wallpaper-choose"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("appearance-wallpaper-gallery"),
    ).toBeInTheDocument();
  },
);

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

  test("a small picture shows its preview without a size refusal", async () => {
    render(<AppearanceThemePicker />);
    choose(1, 1);
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-preview"),
      ).toHaveAttribute("src", "blob:picture"),
    );
    expect(screen.queryByTestId("appearance-wallpaper-status")).toBeNull();
    expect(backdropSnapshot().wallpaper).toBe("blob:picture");
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
  test("a gallery thumbnail larger than 20 MiB is displayed", async () => {
    render(<AppearanceThemePicker />);
    pictures();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: RequestInfo | URL) =>
        String(url).endsWith("/coddy/wallpapers")
          ? Response.json({ wallpapers: [{ name }] })
          : new Response(new Uint8Array(20 * 1024 * 1024 + 1), {
              headers: {
                "Content-Type": "image/png",
                "Content-Length": String(20 * 1024 * 1024 + 1),
              },
            }),
      ),
    );
    const photo = await open();
    await waitFor(() =>
      expect(photo.querySelector("img")).toHaveAttribute("src", "blob:photo"),
    );
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
      await waitFor(() =>
        expect(button.querySelector("img")).toHaveAttribute(
          "src",
          "blob:photo",
        ),
      );
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

describe("wallpaper storage errors", () => {
  function denyStorage() {
    vi.stubGlobal("indexedDB", {
      open: () => {
        throw new Error("Storage denied");
      },
    });
  }
  test.each(["en", "ru"] as const)(
    "failed replacement and removal keep the preview in %s",
    async (locale) => {
      initLocale(locale);
      vi.stubGlobal("createImageBitmap", async () => ({
        width: 1920,
        height: 1080,
        close() {},
      }));
      Object.assign(URL, {
        createObjectURL: vi.fn(() => "blob:kept"),
        revokeObjectURL: vi.fn(),
      });
      await setWallpaper(new Blob(["kept"], { type: "image/png" }));
      render(<AppearanceThemePicker />);
      denyStorage();
      fireEvent.change(screen.getByTestId("appearance-wallpaper-input"), {
        target: {
          files: [new File(["new"], "new.png", { type: "image/png" })],
        },
      });
      const text =
        locale === "en"
          ? "Browser storage could not read or change"
          : "Не удалось прочитать или изменить обои";
      await waitFor(() =>
        expect(
          screen.getByTestId("appearance-wallpaper-status"),
        ).toHaveTextContent(text),
      );
      expect(
        screen.getByTestId("appearance-wallpaper-preview"),
      ).toHaveAttribute("src", "blob:kept");
      fireEvent.click(screen.getByTestId("appearance-wallpaper-remove"));
      await waitFor(() =>
        expect(
          screen.getByTestId("appearance-wallpaper-status"),
        ).toHaveTextContent(text),
      );
      expect(
        screen.getByTestId("appearance-wallpaper-preview"),
      ).toHaveAttribute("src", "blob:kept");
    },
  );

  test.each(["en", "ru"] as const)(
    "a stored error is visible when Appearance opens in %s",
    async (locale) => {
      initLocale(locale);
      vi.stubGlobal("createImageBitmap", async () => ({
        width: 1920,
        height: 1080,
        close() {},
      }));
      denyStorage();
      await act(async () => {
        await setWallpaper(new Blob(["picture"], { type: "image/png" }));
      });
      render(<AppearanceThemePicker />);
      expect(
        screen.getByTestId("appearance-wallpaper-status"),
      ).toHaveTextContent(
        locale === "en"
          ? "Browser storage could not read or change"
          : "Не удалось прочитать или изменить обои",
      );
    },
  );
});
