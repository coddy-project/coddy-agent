import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import {
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
import { GALLERY, shuffleGallery, type GalleryPhoto } from "./wallpaperGallery";

// Shuffle reads a catalog over the network; the component tests hand it a batch.
vi.mock("./wallpaperGallery", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./wallpaperGallery")>()),
  shuffleGallery: vi.fn(),
}));

beforeEach(() => {
  initLocale("en");
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
  const picsum = () =>
    [...document.querySelectorAll("img")].filter((img) =>
      img.getAttribute("src")?.startsWith("https://picsum.photos/"),
    );

  test("nothing is requested from the gallery until it is opened", () => {
    render(<AppearanceThemePicker />);
    expect(picsum()).toHaveLength(0);
    fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
    expect(screen.getByTestId("appearance-wallpaper-grid")).toBeInTheDocument();
    expect(picsum()).toHaveLength(GALLERY.length);
    // two photos of one author are still told apart
    expect(
      screen.getByRole("button", { name: `Photo 1 by ${GALLERY[0]!.author}` }),
    ).toBeInTheDocument();
  });

  test("a picked photo becomes the wallpaper with its author's credit", async () => {
    pictures();
    const fetch = vi.fn(async (_url: string) => ({
      ok: true,
      blob: async () => new Blob(["x"], { type: "image/jpeg" }),
    }));
    vi.stubGlobal("fetch", fetch);
    render(<AppearanceThemePicker />);
    fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
    fireEvent.click(
      screen.getByTestId(`appearance-wallpaper-thumb-${GALLERY[0]!.id}`),
    );
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-credit"),
      ).toHaveTextContent(`Photo by ${GALLERY[0]!.author} on Unsplash`),
    );
    expect(String(fetch.mock.calls[0]?.[0])).toMatch(
      new RegExp(`^https://picsum\\.photos/id/${GALLERY[0]!.id}/\\d+/\\d+$`),
    );
    expect(
      screen.getByRole("link", { name: GALLERY[0]!.author }),
    ).toHaveAttribute("href", GALLERY[0]!.url);
    expect(backdropSnapshot().wallpaper).toBe("blob:photo");
  });

  test("a photo that does not load says so", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("offline");
      }),
    );
    render(<AppearanceThemePicker />);
    fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
    fireEvent.click(
      screen.getByTestId(`appearance-wallpaper-thumb-${GALLERY[1]!.id}`),
    );
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-status"),
      ).toHaveTextContent("The photo did not load."),
    );
    expect(backdropSnapshot().wallpaper).toBeNull();
  });

  test("Shuffle swaps the whole batch for new photos", async () => {
    const fresh: GalleryPhoto[] = Array.from({ length: 12 }, (_, i) => ({
      id: String(900 + i),
      author: `New author ${i}`,
      url: `https://unsplash.com/photos/n${i}`,
    }));
    vi.mocked(shuffleGallery).mockResolvedValueOnce(fresh);
    render(<AppearanceThemePicker />);
    fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
    fireEvent.click(screen.getByTestId("appearance-wallpaper-shuffle"));
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-thumb-900"),
      ).toBeInTheDocument(),
    );
    for (const photo of GALLERY) {
      expect(
        screen.queryByTestId(`appearance-wallpaper-thumb-${photo.id}`),
      ).toBeNull();
    }
    expect(picsum()).toHaveLength(12);
    expect(vi.mocked(shuffleGallery)).toHaveBeenCalledWith(GALLERY);
  });

  test("a shuffle that fails says so and keeps the batch on screen", async () => {
    vi.mocked(shuffleGallery).mockResolvedValueOnce(null);
    render(<AppearanceThemePicker />);
    fireEvent.click(screen.getByTestId("appearance-wallpaper-gallery"));
    fireEvent.click(screen.getByTestId("appearance-wallpaper-shuffle"));
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-wallpaper-shuffle-status"),
      ).toHaveTextContent("New photos did not load."),
    );
    expect(
      screen.getByTestId(`appearance-wallpaper-thumb-${GALLERY[0]!.id}`),
    ).toBeInTheDocument();
  });
});
