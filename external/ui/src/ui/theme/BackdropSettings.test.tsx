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
