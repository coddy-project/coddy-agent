import React from "react";
import { cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  RAIL_SCREENS,
  isUnclaimedEscape,
  railScreenOnTop,
  useRailEscapeStep,
  useRailScreenEscape,
  type RailScreenId,
  type RailScreens,
} from "./railEscape";

afterEach(() => {
  cleanup();
});

/** Every screen of the rail, the ones named open, each with a close to watch. */
function screensWith(...open: RailScreenId[]): RailScreens {
  const screens = {} as RailScreens;
  for (const id of RAIL_SCREENS) {
    screens[id] = { open: open.includes(id), close: vi.fn() };
  }
  return screens;
}

function RailEscape(props: { screens: RailScreens }) {
  useRailScreenEscape(props.screens);
  return null;
}

function Step(props: { screen: RailScreenId; step: (() => void) | null }) {
  useRailEscapeStep(props.screen, props.step);
  return null;
}

/** Escape as a keyboard sends it, at the page; false when something claimed it. */
function pressEscape(
  init: Record<string, unknown> = {},
  target: Element = document.body,
) {
  return fireEvent.keyDown(target, { key: "Escape", ...init });
}

describe("the screens of the rail", () => {
  it("are every item of the rail but sign-out, the topmost first", () => {
    expect([...RAIL_SCREENS]).toEqual([
      "docs",
      "settings",
      "swarm",
      "scheduler",
      "history",
    ]);
  });

  it("put the open screen drawn highest on top", () => {
    expect(railScreenOnTop(screensWith())).toBeNull();
    expect(railScreenOnTop(screensWith("history"))).toBe("history");
    expect(railScreenOnTop(screensWith("history", "scheduler"))).toBe(
      "scheduler",
    );
    expect(railScreenOnTop(screensWith("history", "docs"))).toBe("docs");
    expect(railScreenOnTop(screensWith("swarm", "settings"))).toBe("settings");
  });
});

describe("an Escape a screen may take", () => {
  const keydown = (init: KeyboardEventInit) =>
    new KeyboardEvent("keydown", { cancelable: true, ...init });

  it("is the key alone, pressed once, outside a composition and unclaimed", () => {
    expect(isUnclaimedEscape(keydown({ key: "Escape" }))).toBe(true);
    expect(isUnclaimedEscape(keydown({ key: "Enter" }))).toBe(false);
  });

  it("is not one something nearer the key claimed", () => {
    const ev = keydown({ key: "Escape" });
    ev.preventDefault();
    expect(isUnclaimedEscape(ev)).toBe(false);
  });

  it("is not a held key, a composition's or one with a modifier", () => {
    expect(isUnclaimedEscape(keydown({ key: "Escape", repeat: true }))).toBe(
      false,
    );
    expect(
      isUnclaimedEscape(keydown({ key: "Escape", isComposing: true })),
    ).toBe(false);
    expect(isUnclaimedEscape(keydown({ key: "Escape", keyCode: 229 }))).toBe(
      false,
    );
    for (const mod of ["altKey", "ctrlKey", "metaKey", "shiftKey"] as const) {
      expect(isUnclaimedEscape(keydown({ key: "Escape", [mod]: true }))).toBe(
        false,
      );
    }
  });
});

describe("useRailScreenEscape", () => {
  it("closes the screen on top, and only that one", () => {
    const screens = screensWith("history", "scheduler");
    render(<RailEscape screens={screens} />);
    expect(pressEscape()).toBe(false);
    expect(screens.scheduler.close).toHaveBeenCalledTimes(1);
    expect(screens.history.close).not.toHaveBeenCalled();
  });

  it("closes a screen added to the table like any other", () => {
    for (const id of RAIL_SCREENS) {
      const screens = screensWith(id);
      const { unmount } = render(<RailEscape screens={screens} />);
      pressEscape();
      expect(screens[id].close).toHaveBeenCalledTimes(1);
      unmount();
    }
  });

  it("keeps an Escape that closed a screen from the chat under it", () => {
    // The question card listens on window: its Escape skips the questions.
    const underneath = vi.fn();
    window.addEventListener("keydown", underneath);
    try {
      render(<RailEscape screens={screensWith("settings")} />);
      pressEscape();
      expect(underneath).not.toHaveBeenCalled();
    } finally {
      window.removeEventListener("keydown", underneath);
    }
  });

  it("leaves the key alone while no screen is open", () => {
    const underneath = vi.fn();
    window.addEventListener("keydown", underneath);
    try {
      const screens = screensWith();
      render(<RailEscape screens={screens} />);
      expect(pressEscape()).toBe(true);
      expect(underneath).toHaveBeenCalledTimes(1);
      for (const id of RAIL_SCREENS) {
        expect(screens[id].close).not.toHaveBeenCalled();
      }
    } finally {
      window.removeEventListener("keydown", underneath);
    }
  });

  it("closes nothing on an Escape a control of the screen took", () => {
    const screens = screensWith("docs");
    const { getByRole } = render(
      <>
        <RailEscape screens={screens} />
        <input
          aria-label="search"
          onKeyDown={(e) => {
            if (e.key === "Escape") e.preventDefault();
          }}
        />
      </>,
    );
    pressEscape({}, getByRole("textbox"));
    expect(screens.docs.close).not.toHaveBeenCalled();
    pressEscape();
    expect(screens.docs.close).toHaveBeenCalledTimes(1);
  });

  it("closes nothing on a held, composing or modified Escape", () => {
    const screens = screensWith("history");
    render(<RailEscape screens={screens} />);
    pressEscape({ repeat: true });
    pressEscape({ isComposing: true });
    pressEscape({ ctrlKey: true });
    pressEscape({ shiftKey: true });
    expect(screens.history.close).not.toHaveBeenCalled();
  });

  it("follows the table as the screens open and close", () => {
    const first = screensWith("history");
    const { rerender } = render(<RailEscape screens={first} />);
    const second = screensWith("settings");
    rerender(<RailEscape screens={second} />);
    pressEscape();
    expect(second.settings.close).toHaveBeenCalledTimes(1);
    expect(first.history.close).not.toHaveBeenCalled();
  });
});

describe("useRailEscapeStep", () => {
  it("takes the screen's step back before it closes the screen", () => {
    const screens = screensWith("settings");
    const back = vi.fn();
    const { rerender } = render(
      <>
        <RailEscape screens={screens} />
        <Step screen="settings" step={back} />
      </>,
    );
    pressEscape();
    expect(back).toHaveBeenCalledTimes(1);
    expect(screens.settings.close).not.toHaveBeenCalled();
    // No step left (the head shows no arrow): the screen closes.
    rerender(
      <>
        <RailEscape screens={screens} />
        <Step screen="settings" step={null} />
      </>,
    );
    pressEscape();
    expect(back).toHaveBeenCalledTimes(1);
    expect(screens.settings.close).toHaveBeenCalledTimes(1);
  });

  it("runs the step as it stands now, not as it was registered", () => {
    const screens = screensWith("settings");
    const first = vi.fn();
    const latest = vi.fn();
    const { rerender } = render(
      <>
        <RailEscape screens={screens} />
        <Step screen="settings" step={first} />
      </>,
    );
    rerender(
      <>
        <RailEscape screens={screens} />
        <Step screen="settings" step={latest} />
      </>,
    );
    pressEscape();
    expect(latest).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
  });

  it("belongs to its own screen: another screen on top closes", () => {
    const screens = screensWith("docs");
    const back = vi.fn();
    render(
      <>
        <RailEscape screens={screens} />
        <Step screen="settings" step={back} />
      </>,
    );
    pressEscape();
    expect(back).not.toHaveBeenCalled();
    expect(screens.docs.close).toHaveBeenCalledTimes(1);
  });

  it("goes with the screen that registered it", () => {
    const screens = screensWith("settings");
    const back = vi.fn();
    const { rerender } = render(
      <>
        <RailEscape screens={screens} />
        <Step screen="settings" step={back} />
      </>,
    );
    rerender(<RailEscape screens={screens} />);
    pressEscape();
    expect(back).not.toHaveBeenCalled();
    expect(screens.settings.close).toHaveBeenCalledTimes(1);
  });
});
