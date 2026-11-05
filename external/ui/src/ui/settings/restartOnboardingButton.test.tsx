import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { Settings } from "./Settings";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function stubPendingFetch() {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>(() => {})),
  );
}

test("Appearance shows Restart onboarding and invokes the callback", async () => {
  stubPendingFetch();
  const onRestartOnboarding = vi.fn();
  render(
    <Settings
      onClose={() => {}}
      initialSection="appearance"
      onRestartOnboarding={onRestartOnboarding}
    />,
  );
  await waitFor(() => expect(screenButton()).toBeTruthy());
  fireEvent.click(screenButton());
  expect(onRestartOnboarding).toHaveBeenCalledTimes(1);
});

test("Restart onboarding is absent without a callback", async () => {
  stubPendingFetch();
  const { container, queryByTestId } = render(
    <Settings onClose={() => {}} initialSection="appearance" />,
  );
  await waitFor(() =>
    expect(container.querySelector(".appearance-swatch-grid")).toBeTruthy(),
  );
  expect(queryByTestId("settings-restart-onboarding")).toBeNull();
});

function screenButton(): HTMLElement {
  const button = document.querySelector(
    '[data-testid="settings-restart-onboarding"]',
  );
  if (!button) throw new Error("Restart onboarding button not found");
  return button as HTMLElement;
}
