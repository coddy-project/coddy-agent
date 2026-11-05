import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "../i18n/I18nProvider";
import { initLocale } from "../i18n/i18n";
import { GuidedTour } from "./GuidedTour";
import { TOUR_STEPS } from "./tourSteps";
import { isTourSeen, markTourSeen, resetTour } from "./tourState";

function renderTour(onClose = vi.fn()) {
  document.body.innerHTML = `
    <button data-testid="nav-new-chat"></button>
    <button data-testid="nav-settings"></button>
    <button data-testid="nav-history"></button>
    <textarea id="composer"></textarea>
    <button class="mode-llm"></button>
    <button id="btn-send"></button>`;
  render(
    <I18nProvider>
      <GuidedTour open steps={TOUR_STEPS} onClose={onClose} />
    </I18nProvider>,
  );
  return onClose;
}

describe("GuidedTour", () => {
  beforeEach(() => {
    initLocale("en");
    resetTour();
  });
  afterEach(cleanup);

  it("walks forward and back through only the anchors on the page", () => {
    renderTour();
    // The order a first conversation happens in: open a chat, find the ones
    // before it, then the settings.
    expect(screen.getByTestId("tour-counter").textContent).toBe("Step 1 of 6");
    expect(screen.getByTestId("tour-title").textContent).toBe("New chat");
    fireEvent.click(screen.getByTestId("tour-next"));
    expect(screen.getByTestId("tour-title").textContent).toBe("History");
    fireEvent.click(screen.getByTestId("tour-next"));
    expect(screen.getByTestId("tour-title").textContent).toBe("Settings");
    fireEvent.click(screen.getByTestId("tour-back"));
    expect(screen.getByTestId("tour-title").textContent).toBe("History");
  });

  it("closes on the final step and on Escape", () => {
    const onClose = renderTour();
    for (let i = 0; i < 6; i++)
      fireEvent.click(screen.getByTestId("tour-next"));
    expect(onClose).toHaveBeenCalledTimes(1);
    cleanup();
    const second = renderTour();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(second).toHaveBeenCalledTimes(1);
  });

  it("closes when no anchor exists", () => {
    document.body.innerHTML = "";
    const onClose = vi.fn();
    render(
      <I18nProvider>
        <GuidedTour open steps={TOUR_STEPS} onClose={onClose} />
      </I18nProvider>,
    );
    expect(onClose).toHaveBeenCalled();
  });

  it("keeps the step when the parent re-renders with a fresh onClose", () => {
    document.body.innerHTML = `
      <button data-testid="nav-new-chat"></button>
      <button data-testid="nav-settings"></button>
      <button data-testid="nav-history"></button>
      <textarea id="composer"></textarea>
      <button class="mode-llm"></button>
      <button id="btn-send"></button>`;
    const { rerender } = render(
      <I18nProvider>
        <GuidedTour open steps={TOUR_STEPS} onClose={vi.fn()} />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByTestId("tour-next"));
    fireEvent.click(screen.getByTestId("tour-next"));
    expect(screen.getByTestId("tour-counter").textContent).toBe("Step 3 of 6");
    // A parent that passes a new closure on every render must not send the
    // walk back to step 1.
    rerender(
      <I18nProvider>
        <GuidedTour open steps={TOUR_STEPS} onClose={vi.fn()} />
      </I18nProvider>,
    );
    expect(screen.getByTestId("tour-counter").textContent).toBe("Step 3 of 6");
  });

  it("keeps the one-shot flag in local storage", () => {
    expect(isTourSeen()).toBe(false);
    markTourSeen();
    expect(isTourSeen()).toBe(true);
    resetTour();
    expect(isTourSeen()).toBe(false);
  });
});
