import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { AppearanceThemePicker } from "./AppearanceModal";
import {
  backdropSnapshot,
  setSmoothBackdrop,
  SMOOTH_BACKDROP_COOKIE,
} from "./backdropPrefs";
import { initLocale } from "../i18n/i18n";

beforeEach(() => {
  initLocale("en");
});

afterEach(() => {
  cleanup();
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
